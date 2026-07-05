package sessionimport

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

func buildClaudeSessionEvents(path string) ([]SessionEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var events []SessionEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}
		rawType := jsonString(raw["type"])
		timestamp := jsonString(raw["timestamp"])

		switch rawType {
		case "user":
			var payload struct {
				Message struct {
					Content any `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
				continue
			}
			events = append(events, buildClaudeUserEvents(timestamp, payload.Message.Content)...)
		case "assistant":
			var payload struct {
				Message struct {
					Model   string `json:"model"`
					Content []struct {
						Type  string          `json:"type"`
						ID    string          `json:"id"`
						Name  string          `json:"name"`
						Text  string          `json:"text"`
						Input json.RawMessage `json:"input"`
					} `json:"content"`
					Usage struct {
						InputTokens              int64 `json:"input_tokens"`
						OutputTokens             int64 `json:"output_tokens"`
						CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
						CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
					} `json:"usage"`
				} `json:"message"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &payload); err != nil {
				continue
			}
			for _, item := range payload.Message.Content {
				switch item.Type {
				case "text":
					events = append(events, SessionEvent{
						Kind:      "assistant_message",
						Role:      "assistant",
						Timestamp: timestamp,
						Text:      item.Text,
						Model:     payload.Message.Model,
					})
				case "tool_use":
					call := SessionEvent{
						Kind:       "tool_call",
						Role:       "assistant",
						Timestamp:  timestamp,
						ToolName:   item.Name,
						ToolCallID: item.ID,
						Model:      payload.Message.Model,
					}
					call.ReadPaths, call.WritePaths, call.Command, call.CommandText, call.LinesAdded, call.LinesRemoved = parseClaudeToolInput(item.Name, item.Input)
					call.SearchQuery = parseClaudeSearchQuery(item.Name, item.Input)
					events = append(events, call)
				}
			}
			usage := &TokenUsage{
				InputTokens:      payload.Message.Usage.InputTokens,
				OutputTokens:     payload.Message.Usage.OutputTokens,
				CacheReadTokens:  payload.Message.Usage.CacheReadInputTokens,
				CacheWriteTokens: payload.Message.Usage.CacheCreationInputTokens,
				Cumulative:       false,
			}
			if usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheReadTokens > 0 || usage.CacheWriteTokens > 0 {
				events = append(events, SessionEvent{
					Kind:       "token_usage",
					Role:       "assistant",
					Timestamp:  timestamp,
					Model:      payload.Message.Model,
					TokenUsage: usage,
				})
			}
		case "attachment":
			attachmentType := nestedJSONRawString(raw["attachment"], "type")
			if attachmentType == "hook_success" || attachmentType == "skill_listing" {
				events = append(events, SessionEvent{
					Kind:      "system",
					Timestamp: timestamp,
					Metadata: map[string]string{
						"attachmentType": attachmentType,
					},
				})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	numberSessionEvents(events)
	return events, nil
}

func parseClaudeSearchQuery(toolName string, raw json.RawMessage) string {
	if toolName != "Grep" && toolName != "WebSearch" && toolName != "WebFetch" {
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

func parseClaudeToolInput(toolName string, raw json.RawMessage) ([]string, []string, []string, string, int, int) {
	var input map[string]any
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, nil, nil, "", 0, 0
	}

	switch toolName {
	case "Read":
		path := toString(input["file_path"])
		return normalizePaths([]string{path}), nil, nil, "", 0, 0
	case "Write":
		path := toString(input["file_path"])
		return nil, normalizePaths([]string{path}), nil, "", countLines(toString(input["content"])), 0
	case "Edit":
		path := toString(input["file_path"])
		added, removed := diffLineCounts(toString(input["old_string"]), toString(input["new_string"]))
		return normalizePaths([]string{path}), normalizePaths([]string{path}), nil, "", added, removed
	case "Bash":
		cmd := toString(input["command"])
		readPaths, writePaths := derivePathsFromShell(cmd)
		return readPaths, writePaths, []string{"bash", "-lc", cmd}, cmd, 0, 0
	default:
		return nil, nil, nil, "", 0, 0
	}
}

func buildClaudeUserEvents(timestamp string, content any) []SessionEvent {
	switch value := content.(type) {
	case string:
		if strings.TrimSpace(value) == "" {
			return nil
		}
		return []SessionEvent{{Kind: "prompt", Role: "user", Timestamp: timestamp, Text: value}}
	case []any:
		var events []SessionEvent
		var promptParts []string
		for _, item := range value {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			switch toString(obj["type"]) {
			case "text":
				if text := toString(obj["text"]); text != "" {
					promptParts = append(promptParts, text)
				}
			case "tool_result":
				isError, _ := obj["is_error"].(bool)
				toolUseID := toString(obj["tool_use_id"])
				text := toString(obj["content"])
				events = append(events, SessionEvent{
					Kind:       "tool_result",
					Role:       "user",
					Timestamp:  timestamp,
					Text:       text,
					ToolCallID: toolUseID,
					IsError:    isError,
				})
			}
		}
		if len(promptParts) > 0 {
			events = append([]SessionEvent{{
				Kind:      "prompt",
				Role:      "user",
				Timestamp: timestamp,
				Text:      strings.Join(promptParts, "\n"),
			}}, events...)
		}
		return events
	default:
		return nil
	}
}
