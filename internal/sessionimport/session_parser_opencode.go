package sessionimport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// OpenCode stores each session as storage/session/{projectHash}/{sessionID}.json,
// with per-message and per-part JSON files under storage/message/{sessionID}/*.json
// and storage/part/{messageID}/*.json. See https://opencode.ai and
// github.com/sst/opencode packages/core/src/v1/session.ts for the schema.
// ponytail: file-based storage only; recent opencode versions can persist to an
// opencode.db SQLite file instead - add DB reads if that becomes the common case.

type opencodeSessionInfo struct {
	ID        string  `json:"id"`
	Title     string  `json:"title"`
	Directory string  `json:"directory"`
	Cost      float64 `json:"cost"`
	Model     *struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Time struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

type opencodeMessage struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Tokens     *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
}

type opencodePart struct {
	Type   string          `json:"type"`
	Text   string          `json:"text"`
	Tool   string          `json:"tool"`
	CallID string          `json:"callID"`
	State  json.RawMessage `json:"state"`
}

type opencodeToolState struct {
	Status string          `json:"status"`
	Input  json.RawMessage `json:"input"`
	Output string          `json:"output"`
	Error  string          `json:"error"`
}

func opencodeStorageRoot(sessionInfoPath string) string {
	return filepath.Dir(filepath.Dir(filepath.Dir(sessionInfoPath)))
}

func readOpencodeSessionInfo(path string) (opencodeSessionInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return opencodeSessionInfo{}, err
	}
	var info opencodeSessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return opencodeSessionInfo{}, err
	}
	if info.ID == "" {
		info.ID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return info, nil
}

func readOpencodeSession(path string) (SessionSummary, error) {
	info, err := readOpencodeSessionInfo(path)
	if err != nil {
		return SessionSummary{}, err
	}

	session := SessionSummary{
		Agent: "opencode",
		Path:  path,
		ID:    info.ID,
		Name:  info.Title,
		Cwd:   info.Directory,
	}
	if info.Model != nil {
		session.Model = info.Model.ID
	}
	if info.Time.Created > 0 {
		session.Timestamp = time.UnixMilli(info.Time.Created)
	} else if stat, err := os.Stat(path); err == nil {
		session.Timestamp = stat.ModTime()
	}
	if session.Name == "" {
		session.Name = "(untitled)"
	}

	root := opencodeStorageRoot(path)
	for _, msgPath := range glob(filepath.Join(root, "message", session.ID, "*.json")) {
		messageID := strings.TrimSuffix(filepath.Base(msgPath), filepath.Ext(msgPath))
		for _, partPath := range glob(filepath.Join(root, "part", messageID, "*.json")) {
			data, err := os.ReadFile(partPath)
			if err != nil {
				continue
			}
			var part opencodePart
			if err := json.Unmarshal(data, &part); err != nil {
				continue
			}
			if part.Type == "tool" && isRepoGuideUnderstandTaskToolName(part.Tool) {
				session.UsedRepoGuide = true
				break
			}
		}
		if session.UsedRepoGuide {
			break
		}
	}

	return session, nil
}

func buildOpencodeSessionEvents(path string) ([]SessionEvent, error) {
	info, err := readOpencodeSessionInfo(path)
	if err != nil {
		return nil, err
	}

	root := opencodeStorageRoot(path)
	msgPaths := glob(filepath.Join(root, "message", info.ID, "*.json"))
	sort.Strings(msgPaths)

	var events []SessionEvent
	for _, msgPath := range msgPaths {
		msgData, err := os.ReadFile(msgPath)
		if err != nil {
			continue
		}
		var msg opencodeMessage
		if err := json.Unmarshal(msgData, &msg); err != nil {
			continue
		}
		messageID := msg.ID
		if messageID == "" {
			messageID = strings.TrimSuffix(filepath.Base(msgPath), filepath.Ext(msgPath))
		}
		timestamp := ""
		if msg.Time.Created > 0 {
			timestamp = time.UnixMilli(msg.Time.Created).UTC().Format(time.RFC3339Nano)
		}

		var msgEvents []SessionEvent
		var promptParts []string

		partPaths := glob(filepath.Join(root, "part", messageID, "*.json"))
		sort.Strings(partPaths)
		for _, partPath := range partPaths {
			partData, err := os.ReadFile(partPath)
			if err != nil {
				continue
			}
			var part opencodePart
			if err := json.Unmarshal(partData, &part); err != nil {
				continue
			}

			switch part.Type {
			case "text":
				if strings.TrimSpace(part.Text) == "" {
					continue
				}
				if msg.Role == "user" {
					promptParts = append(promptParts, part.Text)
				} else {
					msgEvents = append(msgEvents, SessionEvent{
						Kind:      "assistant_message",
						Role:      "assistant",
						Timestamp: timestamp,
						Text:      part.Text,
						Model:     msg.ModelID,
					})
				}
			case "tool":
				var state opencodeToolState
				_ = json.Unmarshal(part.State, &state)
				call := SessionEvent{
					Kind:       "tool_call",
					Role:       "assistant",
					Timestamp:  timestamp,
					ToolName:   part.Tool,
					ToolCallID: part.CallID,
					Model:      msg.ModelID,
				}
				call.ReadPaths, call.WritePaths, call.Command, call.CommandText = parseOpencodeToolInput(part.Tool, state.Input)
				call.SearchQuery = parseOpencodeSearchQuery(part.Tool, state.Input)
				msgEvents = append(msgEvents, call)

				switch state.Status {
				case "completed":
					msgEvents = append(msgEvents, SessionEvent{
						Kind:       "tool_result",
						Role:       "user",
						Timestamp:  timestamp,
						Text:       state.Output,
						ToolCallID: part.CallID,
					})
				case "error":
					msgEvents = append(msgEvents, SessionEvent{
						Kind:       "tool_result",
						Role:       "user",
						Timestamp:  timestamp,
						Text:       state.Error,
						ToolCallID: part.CallID,
						IsError:    true,
					})
				}
			}
		}

		if len(promptParts) > 0 {
			msgEvents = append([]SessionEvent{{
				Kind:      "prompt",
				Role:      "user",
				Timestamp: timestamp,
				Text:      strings.Join(promptParts, "\n"),
			}}, msgEvents...)
		}

		if msg.Role == "assistant" && msg.Tokens != nil {
			usage := &TokenUsage{
				InputTokens:      msg.Tokens.Input,
				OutputTokens:     msg.Tokens.Output,
				CacheReadTokens:  msg.Tokens.Cache.Read,
				CacheWriteTokens: msg.Tokens.Cache.Write,
			}
			if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheReadTokens > 0 || usage.CacheWriteTokens > 0 {
				msgEvents = append(msgEvents, SessionEvent{
					Kind:       "token_usage",
					Role:       "assistant",
					Timestamp:  timestamp,
					Model:      msg.ModelID,
					TokenUsage: usage,
				})
			}
		}

		events = append(events, msgEvents...)
	}
	numberSessionEvents(events)
	return events, nil
}

func parseOpencodeToolInput(toolName string, raw json.RawMessage) ([]string, []string, []string, string) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, nil, ""
	}

	switch toolName {
	case "read":
		path := toString(input["filePath"])
		return normalizePaths([]string{path}), nil, nil, ""
	case "write":
		path := toString(input["filePath"])
		return nil, normalizePaths([]string{path}), nil, ""
	case "edit":
		path := toString(input["filePath"])
		return normalizePaths([]string{path}), normalizePaths([]string{path}), nil, ""
	case "bash":
		cmd := toString(input["command"])
		readPaths, writePaths := derivePathsFromShell(cmd)
		return readPaths, writePaths, []string{"bash", "-lc", cmd}, cmd
	default:
		return nil, nil, nil, ""
	}
}

func parseOpencodeSearchQuery(toolName string, raw json.RawMessage) string {
	if toolName != "grep" && toolName != "glob" && toolName != "webfetch" {
		return ""
	}
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return ""
	}
	for _, key := range []string{"pattern", "query", "url"} {
		if value := strings.TrimSpace(toString(input[key])); value != "" {
			return value
		}
	}
	return ""
}
