package sessionimport

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"
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
			case "patch_apply_end":
				// Current Codex applies edits through a native patch tool rather than a
				// shell apply_patch heredoc, so the unified diffs here are the only
				// authoritative record of which files changed and by how many lines.
				if event, ok := parseCodexPatchApply(raw.Payload, raw.Timestamp); ok {
					events = append(events, event)
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

// parseCodexPatchApply turns a patch_apply_end payload into a single edit event
// carrying every file the patch touched and the diff's line counts. Failed
// patches are dropped: nothing changed on disk, so they aren't edits.
func parseCodexPatchApply(raw json.RawMessage, timestamp string) (SessionEvent, bool) {
	var payload struct {
		CallID  string `json:"call_id"`
		Success bool   `json:"success"`
		Changes map[string]struct {
			UnifiedDiff string `json:"unified_diff"`
		} `json:"changes"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || !payload.Success || len(payload.Changes) == 0 {
		return SessionEvent{}, false
	}
	event := SessionEvent{
		Kind:       "patch_apply",
		Timestamp:  timestamp,
		ToolName:   "apply_patch",
		ToolCallID: payload.CallID,
	}
	writes := make([]string, 0, len(payload.Changes))
	for path, change := range payload.Changes {
		if strings.TrimSpace(path) == "" {
			continue
		}
		writes = append(writes, path)
		added, removed := countUnifiedDiffLines(change.UnifiedDiff)
		event.LinesAdded += added
		event.LinesRemoved += removed
	}
	if len(writes) == 0 {
		return SessionEvent{}, false
	}
	sort.Strings(writes)
	event.WritePaths = writes
	return event, true
}

// countUnifiedDiffLines counts +/- lines in a unified diff body, skipping the
// file and hunk headers so they aren't mistaken for content.
func countUnifiedDiffLines(diff string) (added, removed int) {
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") || strings.HasPrefix(line, "@@"):
			continue
		case strings.HasPrefix(line, "+"):
			added++
		case strings.HasPrefix(line, "-"):
			removed++
		}
	}
	return added, removed
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
			InputTokens:     uncachedInput(payload.Last.InputTokens, payload.Last.CachedInput),
			OutputTokens:    payload.Last.OutputTokens,
			CacheReadTokens: payload.Last.CachedInput,
			Cumulative:      false,
		}
	}
	if payload.Total.InputTokens == 0 && payload.Total.CachedInput == 0 && payload.Total.OutputTokens == 0 {
		return nil
	}
	return &TokenUsage{
		InputTokens:     uncachedInput(payload.Total.InputTokens, payload.Total.CachedInput),
		OutputTokens:    payload.Total.OutputTokens,
		CacheReadTokens: payload.Total.CachedInput,
		Cumulative:      true,
	}
}

// uncachedInput converts OpenAI's cache-inclusive input_tokens to the
// cache-exclusive form TokenUsage requires. Codex reports
// cached_input_tokens as a subset of input_tokens, so leaving the total in
// place charges every cached token at the full input rate and again at the
// cache rate.
//
// Clamped at zero: a provider that ever reports cached > input would
// otherwise produce negative tokens and a negative cost.
func uncachedInput(input, cached int64) int64 {
	if cached >= input {
		return 0
	}
	return input - cached
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
