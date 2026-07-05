package sessionimport

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Gemini CLI stores each session as ~/.gemini/tmp/{projectHash}/chats/session-*.jsonl:
// a header line (sessionId, startTime) followed by user/gemini/error/info entries.
// Messages are rewritten in place as they stream, so the same message "id" can
// appear multiple times with a growing toolCalls list - callers must dedupe by id.
// The project's cwd lives in a sibling ".project_root" file.
// See https://github.com/google-gemini/gemini-cli.

type geminiChatHeader struct {
	SessionID string `json:"sessionId"`
	StartTime string `json:"startTime"`
}

type geminiFunctionResponse struct {
	Name     string `json:"name"`
	Response struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	} `json:"response"`
}

type geminiToolCall struct {
	Name   string          `json:"name"`
	Args   json.RawMessage `json:"args"`
	Result []struct {
		FunctionResponse geminiFunctionResponse `json:"functionResponse"`
	} `json:"result"`
}

type geminiChatEntry struct {
	ID        string           `json:"id"`
	Timestamp string           `json:"timestamp"`
	Type      string           `json:"type"`
	Content   json.RawMessage  `json:"content"`
	Model     string           `json:"model"`
	ToolCalls []geminiToolCall `json:"toolCalls"`
}

func geminiProjectDir(chatPath string) string {
	return filepath.Dir(filepath.Dir(chatPath))
}

func readGeminiProjectRoot(projectDir string) string {
	data, err := os.ReadFile(filepath.Join(projectDir, ".project_root"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func geminiContentText(raw json.RawMessage) string {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

func readGeminiSession(path string) (SessionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return SessionSummary{}, err
	}
	defer f.Close()

	session := SessionSummary{
		Agent: "gemini",
		Path:  path,
		Cwd:   readGeminiProjectRoot(geminiProjectDir(path)),
		Name:  "(untitled)",
	}
	if stat, err := os.Stat(path); err == nil {
		session.Timestamp = stat.ModTime()
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if first {
			first = false
			var header geminiChatHeader
			if err := json.Unmarshal(line, &header); err == nil {
				session.ID = header.SessionID
				if t, err := time.Parse(time.RFC3339Nano, header.StartTime); err == nil {
					session.Timestamp = t
				}
			}
			continue
		}

		var entry geminiChatEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}
		if entry.Model != "" {
			session.Model = entry.Model
		}
		for _, call := range entry.ToolCalls {
			if isRepoGuideUnderstandTaskToolName(call.Name) {
				session.UsedRepoGuide = true
			}
		}
	}
	if session.ID == "" {
		session.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	return session, nil
}

func buildGeminiSessionEvents(path string) ([]SessionEvent, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var events []SessionEvent
	emittedMessage := map[string]bool{}
	emittedToolCalls := map[string]int{} // message id -> number of tool calls already emitted

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)

	first := true
	for scanner.Scan() {
		line := scanner.Bytes()
		if first {
			first = false
			continue
		}

		var entry geminiChatEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			continue
		}

		switch entry.Type {
		case "user":
			if emittedMessage[entry.ID] {
				continue
			}
			emittedMessage[entry.ID] = true
			text := geminiContentText(entry.Content)
			if strings.TrimSpace(text) == "" {
				continue
			}
			events = append(events, SessionEvent{
				Kind:      "prompt",
				Role:      "user",
				Timestamp: entry.Timestamp,
				Text:      text,
			})

		case "gemini":
			if !emittedMessage[entry.ID] {
				emittedMessage[entry.ID] = true
				text := geminiContentText(entry.Content)
				if strings.TrimSpace(text) != "" {
					events = append(events, SessionEvent{
						Kind:      "assistant_message",
						Role:      "assistant",
						Timestamp: entry.Timestamp,
						Text:      text,
						Model:     entry.Model,
					})
				}
			}

			for i := emittedToolCalls[entry.ID]; i < len(entry.ToolCalls); i++ {
				call := entry.ToolCalls[i]
				event := SessionEvent{
					Kind:      "tool_call",
					Role:      "assistant",
					Timestamp: entry.Timestamp,
					ToolName:  call.Name,
					Model:     entry.Model,
				}
				event.ReadPaths, event.WritePaths, event.Command, event.CommandText, event.LinesAdded, event.LinesRemoved = parseGeminiToolInput(call.Name, call.Args)
				event.SearchQuery = parseGeminiSearchQuery(call.Name, call.Args)
				events = append(events, event)

				if len(call.Result) > 0 {
					resp := call.Result[0].FunctionResponse.Response
					text := resp.Output
					isError := resp.Error != ""
					if isError {
						text = resp.Error
					}
					events = append(events, SessionEvent{
						Kind:      "tool_result",
						Role:      "user",
						Timestamp: entry.Timestamp,
						Text:      text,
						IsError:   isError,
					})
				}
			}
			emittedToolCalls[entry.ID] = len(entry.ToolCalls)

		case "error":
			events = append(events, SessionEvent{
				Kind:      "tool_result",
				Role:      "user",
				Timestamp: entry.Timestamp,
				Text:      geminiContentText(entry.Content),
				IsError:   true,
			})
		}
	}

	numberSessionEvents(events)
	return events, nil
}

func parseGeminiToolInput(toolName string, raw json.RawMessage) ([]string, []string, []string, string, int, int) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, nil, "", 0, 0
	}

	switch toolName {
	case "read_file":
		return normalizePaths([]string{toString(input["file_path"])}), nil, nil, "", 0, 0
	case "write_file":
		path := toString(input["file_path"])
		return nil, normalizePaths([]string{path}), nil, "", countLines(toString(input["content"])), 0
	case "replace":
		path := toString(input["file_path"])
		added, removed := diffLineCounts(toString(input["old_string"]), toString(input["new_string"]))
		return normalizePaths([]string{path}), normalizePaths([]string{path}), nil, "", added, removed
	case "list_directory":
		return normalizePaths([]string{toString(input["dir_path"])}), nil, nil, "", 0, 0
	case "run_shell_command":
		cmd := toString(input["command"])
		readPaths, writePaths := derivePathsFromShell(cmd)
		return readPaths, writePaths, []string{"bash", "-lc", cmd}, cmd, 0, 0
	default:
		return nil, nil, nil, "", 0, 0
	}
}

func parseGeminiSearchQuery(toolName string, raw json.RawMessage) string {
	if toolName != "grep_search" && toolName != "glob" {
		return ""
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	return strings.TrimSpace(toString(input["pattern"]))
}
