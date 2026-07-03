package sessionimport

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GitHub Copilot CLI stores each session as ~/.copilot/session-state/{id}/,
// with a flat-scalar workspace.yaml (cwd, name, model...) and an events.jsonl
// event log. See https://docs.github.com/en/copilot/concepts/agents/copilot-cli/chronicle.

type copilotWorkspace struct {
	ID   string
	Cwd  string
	Name string
}

type copilotEventEnvelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func copilotSessionDir(eventsPath string) string {
	return filepath.Dir(eventsPath)
}

// readCopilotWorkspace parses the flat "key: value" lines in workspace.yaml.
// ponytail: hand-rolled parser instead of a yaml dependency; the file only ever
// has flat scalar values, add real YAML parsing if that assumption breaks.
func readCopilotWorkspace(dir string) copilotWorkspace {
	ws := copilotWorkspace{}
	data, err := os.ReadFile(filepath.Join(dir, "workspace.yaml"))
	if err != nil {
		return ws
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "id":
			ws.ID = value
		case "cwd":
			ws.Cwd = value
		case "name":
			ws.Name = value
		}
	}
	return ws
}

func readCopilotSession(eventsPath string) (SessionSummary, error) {
	dir := copilotSessionDir(eventsPath)
	ws := readCopilotWorkspace(dir)
	if ws.ID == "" {
		ws.ID = filepath.Base(dir)
	}

	session := SessionSummary{
		Agent: "copilot",
		Path:  eventsPath,
		ID:    ws.ID,
		Name:  ws.Name,
		Cwd:   ws.Cwd,
	}
	if session.Name == "" {
		session.Name = "(untitled)"
	}
	if stat, err := os.Stat(eventsPath); err == nil {
		session.Timestamp = stat.ModTime()
	}

	f, err := os.Open(eventsPath)
	if err != nil {
		return session, nil
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var env copilotEventEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}
		switch env.Type {
		case "session.start":
			var data struct {
				StartTime     string `json:"startTime"`
				SelectedModel string `json:"selectedModel"`
			}
			_ = json.Unmarshal(env.Data, &data)
			if session.Model == "" {
				session.Model = data.SelectedModel
			}
			if t, err := time.Parse(time.RFC3339Nano, data.StartTime); err == nil {
				session.Timestamp = t
			}
		case "tool.execution_start":
			var data struct {
				ToolName string `json:"toolName"`
			}
			_ = json.Unmarshal(env.Data, &data)
			if isRepoGuideUnderstandTaskToolName(data.ToolName) {
				session.UsedRepoGuide = true
			}
		}
	}

	return session, nil
}

func buildCopilotSessionEvents(eventsPath string) ([]SessionEvent, error) {
	f, err := os.Open(eventsPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []SessionEvent
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var env struct {
			Type      string          `json:"type"`
			Data      json.RawMessage `json:"data"`
			Timestamp string          `json:"timestamp"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}

		switch env.Type {
		case "user.message":
			var data struct {
				Content string `json:"content"`
			}
			_ = json.Unmarshal(env.Data, &data)
			if strings.TrimSpace(data.Content) == "" {
				continue
			}
			events = append(events, SessionEvent{
				Kind:      "prompt",
				Role:      "user",
				Timestamp: env.Timestamp,
				Text:      data.Content,
			})

		case "assistant.message":
			var data struct {
				Model        string `json:"model"`
				Content      string `json:"content"`
				ToolRequests []struct {
					ToolCallID string          `json:"toolCallId"`
					Name       string          `json:"name"`
					Arguments  json.RawMessage `json:"arguments"`
				} `json:"toolRequests"`
			}
			_ = json.Unmarshal(env.Data, &data)
			if strings.TrimSpace(data.Content) != "" {
				events = append(events, SessionEvent{
					Kind:      "assistant_message",
					Role:      "assistant",
					Timestamp: env.Timestamp,
					Text:      data.Content,
					Model:     data.Model,
				})
			}
			for _, call := range data.ToolRequests {
				event := SessionEvent{
					Kind:       "tool_call",
					Role:       "assistant",
					Timestamp:  env.Timestamp,
					ToolName:   call.Name,
					ToolCallID: call.ToolCallID,
					Model:      data.Model,
				}
				event.ReadPaths, event.WritePaths, event.Command, event.CommandText = parseCopilotToolInput(call.Name, call.Arguments)
				event.SearchQuery = parseCopilotSearchQuery(call.Name, call.Arguments)
				events = append(events, event)
			}

		case "tool.execution_complete":
			var data struct {
				ToolCallID string `json:"toolCallId"`
				Success    bool   `json:"success"`
				Result     struct {
					Content string `json:"content"`
				} `json:"result"`
			}
			_ = json.Unmarshal(env.Data, &data)
			events = append(events, SessionEvent{
				Kind:       "tool_result",
				Role:       "user",
				Timestamp:  env.Timestamp,
				Text:       data.Result.Content,
				ToolCallID: data.ToolCallID,
				IsError:    !data.Success,
			})

		case "session.shutdown":
			var data struct {
				CurrentModel string `json:"currentModel"`
				ModelMetrics map[string]struct {
					Usage struct {
						InputTokens      int64 `json:"inputTokens"`
						OutputTokens     int64 `json:"outputTokens"`
						CacheReadTokens  int64 `json:"cacheReadTokens"`
						CacheWriteTokens int64 `json:"cacheWriteTokens"`
					} `json:"usage"`
				} `json:"modelMetrics"`
			}
			_ = json.Unmarshal(env.Data, &data)
			for model, metrics := range data.ModelMetrics {
				events = append(events, SessionEvent{
					Kind:      "token_usage",
					Role:      "assistant",
					Timestamp: env.Timestamp,
					Model:     model,
					TokenUsage: &TokenUsage{
						InputTokens:      metrics.Usage.InputTokens,
						OutputTokens:     metrics.Usage.OutputTokens,
						CacheReadTokens:  metrics.Usage.CacheReadTokens,
						CacheWriteTokens: metrics.Usage.CacheWriteTokens,
						Cumulative:       true,
					},
				})
			}
		}
	}

	numberSessionEvents(events)
	return events, nil
}

func parseCopilotToolInput(toolName string, raw json.RawMessage) ([]string, []string, []string, string) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, nil, ""
	}

	switch toolName {
	case "view":
		return normalizePaths([]string{toString(input["path"])}), nil, nil, ""
	case "create":
		path := toString(input["path"])
		return nil, normalizePaths([]string{path}), nil, ""
	case "edit":
		path := toString(input["path"])
		return normalizePaths([]string{path}), normalizePaths([]string{path}), nil, ""
	case "bash", "write_bash":
		cmd := toString(input["command"])
		readPaths, writePaths := derivePathsFromShell(cmd)
		return readPaths, writePaths, []string{"bash", "-lc", cmd}, cmd
	default:
		return nil, nil, nil, ""
	}
}

func parseCopilotSearchQuery(toolName string, raw json.RawMessage) string {
	if toolName != "grep" && toolName != "glob" {
		return ""
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	return strings.TrimSpace(toString(input["pattern"]))
}
