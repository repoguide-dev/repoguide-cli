package sessionimport

import (
	"bufio"
	"encoding/json"
	"os"
)

func buildCodexSessionEvents(path string) ([]SessionEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var events []SessionEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		var raw struct {
			Timestamp string          `json:"timestamp"`
			Type      string          `json:"type"`
			Payload   json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &raw); err != nil {
			continue
		}

		switch raw.Type {
		case "session_meta":
			var payload struct {
				ID  string `json:"id"`
				Cwd string `json:"cwd"`
			}
			if err := json.Unmarshal(raw.Payload, &payload); err == nil {
				events = append(events, SessionEvent{
					Kind:      "session_meta",
					Timestamp: raw.Timestamp,
					Metadata: map[string]string{
						"id":  payload.ID,
						"cwd": payload.Cwd,
					},
				})
			}
		case "turn_context":
			var payload struct {
				Cwd   string `json:"cwd"`
				Model string `json:"model"`
			}
			if err := json.Unmarshal(raw.Payload, &payload); err == nil {
				events = append(events, SessionEvent{
					Kind:      "turn_context",
					Timestamp: raw.Timestamp,
					Model:     payload.Model,
					Metadata: map[string]string{
						"cwd": payload.Cwd,
					},
				})
			}
		case "response_item":
			var payload struct {
				Type      string `json:"type"`
				Role      string `json:"role"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				CallID    string `json:"call_id"`
				Output    string `json:"output"`
				Content   []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "message":
				text := joinContentTexts(payload.Content)
				kind := "assistant_message"
				if payload.Role == "user" {
					kind = "prompt"
				}
				events = append(events, SessionEvent{
					Kind:      kind,
					Role:      payload.Role,
					Timestamp: raw.Timestamp,
					Text:      text,
				})
			case "function_call":
				event := SessionEvent{
					Kind:       "tool_call",
					Timestamp:  raw.Timestamp,
					ToolName:   payload.Name,
					ToolCallID: payload.CallID,
				}
				if payload.Name == "shell" || payload.Name == "exec_command" || payload.Name == "shell_command" {
					command, commandText, readPaths, writePaths := parseCodexShellArguments(payload.Arguments)
					event.Command = command
					event.CommandText = commandText
					event.ReadPaths = readPaths
					event.WritePaths = writePaths
					event.LinesAdded, event.LinesRemoved = countPatchLines(commandText)
				}
				events = append(events, event)
			case "function_call_output":
				events = append(events, SessionEvent{
					Kind:       "tool_result",
					Timestamp:  raw.Timestamp,
					ToolCallID: payload.CallID,
					Text:       payload.Output,
				})
			}
		case "event_msg":
			var payload struct {
				Type    string          `json:"type"`
				Message string          `json:"message"`
				Text    string          `json:"text"`
				Info    json.RawMessage `json:"info"`
			}
			if err := json.Unmarshal(raw.Payload, &payload); err != nil {
				continue
			}
			switch payload.Type {
			case "user_message":
				events = append(events, SessionEvent{
					Kind:      "prompt",
					Role:      "user",
					Timestamp: raw.Timestamp,
					Text:      payload.Message,
				})
			case "agent_message":
				events = append(events, SessionEvent{
					Kind:      "assistant_message",
					Role:      "assistant",
					Timestamp: raw.Timestamp,
					Text:      payload.Message,
				})
			case "agent_reasoning":
				events = append(events, SessionEvent{
					Kind:      "assistant_reasoning",
					Role:      "assistant",
					Timestamp: raw.Timestamp,
					Text:      payload.Text,
				})
			case "token_count":
				if usage := parseCodexTokenUsage(payload.Info); usage != nil {
					events = append(events, SessionEvent{
						Kind:       "token_usage",
						Timestamp:  raw.Timestamp,
						TokenUsage: usage,
					})
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	numberSessionEvents(events)
	return events, nil
}

func parseCodexTokenUsage(info json.RawMessage) *TokenUsage {
	var payload struct {
		Total struct {
			InputTokens  int64 `json:"input_tokens"`
			CachedInput  int64 `json:"cached_input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"total_token_usage"`
		Last struct {
			InputTokens  int64 `json:"input_tokens"`
			CachedInput  int64 `json:"cached_input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"last_token_usage"`
	}
	if err := json.Unmarshal(info, &payload); err != nil {
		return nil
	}
	if payload.Last.InputTokens > 0 || payload.Last.CachedInput > 0 || payload.Last.OutputTokens > 0 {
		return &TokenUsage{
			InputTokens:     payload.Last.InputTokens,
			OutputTokens:    payload.Last.OutputTokens,
			CacheReadTokens: payload.Last.CachedInput,
			Cumulative:      false,
		}
	}
	if payload.Total.InputTokens == 0 && payload.Total.CachedInput == 0 && payload.Total.OutputTokens == 0 {
		return nil
	}
	return &TokenUsage{
		InputTokens:     payload.Total.InputTokens,
		OutputTokens:    payload.Total.OutputTokens,
		CacheReadTokens: payload.Total.CachedInput,
		Cumulative:      true,
	}
}

func parseCodexShellArguments(raw string) ([]string, string, []string, []string) {
	// shell_command uses {"command":"..."}, exec_command uses {"cmd":"..."}, legacy shell uses {"command":[...]}
	var payload struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, "", nil, nil
	}
	if payload.Cmd != "" {
		readPaths, writePaths := derivePathsFromShell(payload.Cmd)
		return []string{payload.Cmd}, payload.Cmd, readPaths, writePaths
	}
	// command field may be a string or array
	if len(payload.Command) > 0 {
		var cmdStr string
		if err := json.Unmarshal(payload.Command, &cmdStr); err == nil {
			readPaths, writePaths := derivePathsFromShell(cmdStr)
			return []string{cmdStr}, cmdStr, readPaths, writePaths
		}
	}
	var cmdArr []string
	if err := json.Unmarshal(payload.Command, &cmdArr); err != nil {
		return nil, "", nil, nil
	}
	commandText := joinCommand(cmdArr)
	script := ""
	if len(cmdArr) >= 3 && (cmdArr[1] == "-lc" || cmdArr[1] == "-c") {
		script = cmdArr[2]
	}
	readPaths, writePaths := derivePathsFromShell(script)
	return cmdArr, commandText, readPaths, writePaths
}
