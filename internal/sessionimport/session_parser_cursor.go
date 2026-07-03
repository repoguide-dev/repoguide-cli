package sessionimport

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func readCursorSession(path string) (SessionSummary, error) {
	session := SessionSummary{
		Agent: "cursor",
		Path:  path,
		ID:    strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)),
	}
	if info, err := os.Stat(path); err == nil {
		session.Timestamp = info.ModTime()
	}

	// prefer meta.json from chats for title and timestamp (pick any matching workspace)
	if metaPaths := glob(home(".cursor", "chats", "*", session.ID, "meta.json")); len(metaPaths) > 0 {
		if data, err := os.ReadFile(metaPaths[0]); err == nil {
			var meta struct {
				Title       string `json:"title"`
				UpdatedAtMs int64  `json:"updatedAtMs"`
			}
			if err := json.Unmarshal(data, &meta); err == nil {
				if meta.Title != "" {
					session.Name = meta.Title
				}
				if meta.UpdatedAtMs > 0 {
					session.Timestamp = time.UnixMilli(meta.UpdatedAtMs)
				}
			}
		}
	}

	file, err := os.Open(path)
	if err != nil {
		if session.Name == "" {
			session.Name = "(untitled)"
		}
		return session, nil
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		var env cursorEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil || env.Role != "assistant" {
			continue
		}
		for _, item := range env.Message.Content {
			if item.Type != "tool_use" {
				continue
			}
			if session.Cwd == "" {
				session.Cwd = cursorExtractCwd(item.Name, item.Input)
			}
			if isRepoGuideUnderstandTaskToolName(item.Name) {
				session.UsedRepoGuide = true
			}
		}
		if session.Cwd != "" && session.UsedRepoGuide {
			break
		}
	}

	if session.Name == "" {
		session.Name = "(untitled)"
	}
	return session, nil
}

func buildCursorSessionEvents(path string) ([]SessionEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var events []SessionEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		var env cursorEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &env); err != nil {
			continue
		}
		switch env.Role {
		case "user":
			for _, item := range env.Message.Content {
				switch item.Type {
				case "text":
					text := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(item.Text), "<user_query>"), "</user_query>"))
					if text != "" {
						events = append(events, SessionEvent{Kind: "prompt", Role: "user", Text: text})
					}
				case "tool_result":
					events = append(events, SessionEvent{
						Kind:       "tool_result",
						Role:       "user",
						ToolCallID: item.ToolUseID,
						Text:       item.Content,
						IsError:    item.IsError,
					})
				}
			}
		case "assistant":
			for _, item := range env.Message.Content {
				switch item.Type {
				case "text":
					if strings.TrimSpace(item.Text) != "" {
						events = append(events, SessionEvent{Kind: "assistant_message", Role: "assistant", Text: item.Text})
					}
				case "tool_use":
					call := SessionEvent{
						Kind:       "tool_call",
						Role:       "assistant",
						ToolName:   item.Name,
						ToolCallID: item.ID,
					}
					call.ReadPaths, call.WritePaths, call.Command, call.CommandText = parseCursorToolInput(item.Name, item.Input)
					call.SearchQuery = parseCursorSearchQuery(item.Name, item.Input)
					events = append(events, call)
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

func parseCursorToolInput(toolName string, raw json.RawMessage) ([]string, []string, []string, string) {
	var inp map[string]any
	if err := json.Unmarshal(raw, &inp); err != nil {
		return nil, nil, nil, ""
	}
	switch toolName {
	case "Read":
		if p := toString(inp["path"]); p != "" {
			return normalizePaths([]string{p}), nil, nil, ""
		}
	case "Edit", "WriteFile":
		p := toString(inp["path"])
		if p == "" {
			p = toString(inp["target_file"])
		}
		if p != "" {
			return normalizePaths([]string{p}), normalizePaths([]string{p}), nil, ""
		}
	case "Shell":
		cmd := toString(inp["command"])
		readPaths, writePaths := derivePathsFromShell(cmd)
		return readPaths, writePaths, []string{cmd}, cmd
	}
	return nil, nil, nil, ""
}

func parseCursorSearchQuery(toolName string, raw json.RawMessage) string {
	var inp map[string]any
	if err := json.Unmarshal(raw, &inp); err != nil {
		return ""
	}
	switch toolName {
	case "Grep":
		return toString(inp["pattern"])
	case "Glob":
		return toString(inp["glob_pattern"])
	}
	return ""
}

func cursorExtractCwd(toolName string, raw json.RawMessage) string {
	var inp map[string]any
	if err := json.Unmarshal(raw, &inp); err != nil {
		return ""
	}
	switch toolName {
	case "Glob":
		if dir := toString(inp["target_directory"]); filepath.IsAbs(dir) {
			return dir
		}
	case "Read", "Edit", "WriteFile", "Grep":
		if p := toString(inp["path"]); filepath.IsAbs(p) {
			info, err := os.Stat(p)
			if err == nil && info.IsDir() {
				return p
			}
			return filepath.Dir(p)
		}
	case "Shell":
		cmd := toString(inp["command"])
		for _, part := range strings.SplitN(cmd, "&&", 2) {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "cd ") {
				dir := strings.Trim(strings.TrimPrefix(part, "cd "), " '\"")
				if filepath.IsAbs(dir) {
					return dir
				}
			}
		}
	}
	return ""
}

// cursorEnvelope is the per-line shape of a Cursor agent transcript JSONL.
type cursorEnvelope struct {
	Role    string `json:"role"`
	Message struct {
		Content []struct {
			Type      string          `json:"type"`
			ID        string          `json:"id"`
			Name      string          `json:"name"`
			Text      string          `json:"text"`
			Input     json.RawMessage `json:"input"`
			ToolUseID string          `json:"tool_use_id"`
			Content   string          `json:"content"`
			IsError   bool            `json:"is_error"`
		} `json:"content"`
	} `json:"message"`
}
