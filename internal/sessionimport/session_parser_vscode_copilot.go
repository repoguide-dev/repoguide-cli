package sessionimport

import (
	"bufio"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// VS Code's built-in "GitHub Copilot Chat" extension stores each chat session
// under <UserConfigDir>/<Code variant>/User/workspaceStorage/<hash>/chatSessions/<id>.json(l).
// Pre-1.109 VS Code writes a flat JSON snapshot; 1.109+ writes an append-only
// .jsonl "operation log" (kind 0 = full base object, 1 = Set, 2 = Push, 3 = Delete
// at a JSON path "k"). The sibling workspace.json in the hash folder holds the
// repo path as a file:// URI. This is a distinct source from the "copilot" CLI
// agent (~/.copilot/session-state), which uses a different format entirely.

var vscodeVariants = []string{"Code", "Code - Insiders"}

func vscodeCopilotSessionPaths() []string {
	configRoot, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	var paths []string
	for _, variant := range vscodeVariants {
		base := filepath.Join(configRoot, variant, "User", "workspaceStorage")
		paths = append(paths, glob(filepath.Join(base, "*", "chatSessions", "*.json"))...)
		paths = append(paths, glob(filepath.Join(base, "*", "chatSessions", "*.jsonl"))...)
	}
	return paths
}

// vscodeCopilotSessionCwd reads the workspace.json two directories up from the
// session file (chatSessions/<file> -> workspaceStorage/<hash>/workspace.json).
// ponytail: only single-folder workspaces ("folder" key) are resolved; multi-root
// .code-workspace files ("workspace" key) are skipped, add if that turns out common.
func vscodeCopilotSessionCwd(sessionPath string) string {
	hashDir := filepath.Dir(filepath.Dir(sessionPath))
	data, err := os.ReadFile(filepath.Join(hashDir, "workspace.json"))
	if err != nil {
		return ""
	}
	var ws struct {
		Folder string `json:"folder"`
	}
	if err := json.Unmarshal(data, &ws); err != nil || ws.Folder == "" {
		return ""
	}
	return fileURIToPath(ws.Folder)
}

func fileURIToPath(uri string) string {
	p := strings.TrimPrefix(uri, "file://")
	if decoded, err := url.PathUnescape(p); err == nil {
		p = decoded
	}
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // "/C:/foo" -> "C:/foo"
	}
	return p
}

// readVSCodeChatSessionState loads a chat session file (either format) into a
// generic map so both schema variants can be read without a rigid struct.
func readVSCodeChatSessionState(path string) (map[string]any, error) {
	if strings.HasSuffix(path, ".jsonl") {
		return replayVSCodeChatLog(path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var state map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return state, nil
}

func replayVSCodeChatLog(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var state map[string]any
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		var patch struct {
			Kind int   `json:"kind"`
			K    []any `json:"k"`
			V    any   `json:"v"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &patch); err != nil {
			continue
		}
		switch patch.Kind {
		case 0:
			if m, ok := patch.V.(map[string]any); ok {
				state = m
			}
		case 1:
			state = applyVSCodePatch(state, patch.K, "set", patch.V)
		case 2:
			state = applyVSCodePatch(state, patch.K, "push", patch.V)
		case 3:
			state = applyVSCodePatch(state, patch.K, "delete", nil)
		}
	}
	return state, nil
}

func applyVSCodePatch(root map[string]any, path []any, op string, value any) map[string]any {
	if root == nil || len(path) == 0 {
		return root
	}
	result := vscodePatchRecurse(root, path, op, value)
	if m, ok := result.(map[string]any); ok {
		return m
	}
	return root
}

func vscodePatchRecurse(node any, path []any, op string, value any) any {
	key := path[0]
	if len(path) == 1 {
		switch op {
		case "set":
			return vscodeSetChild(node, key, value)
		case "push":
			return vscodeSetChild(node, key, append(vscodeAsSlice(vscodeGetChild(node, key)), value))
		case "delete":
			return vscodeDeleteChild(node, key)
		}
		return node
	}
	child := vscodeGetChild(node, key)
	return vscodeSetChild(node, key, vscodePatchRecurse(child, path[1:], op, value))
}

func vscodeAsSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func vscodeGetChild(node any, key any) any {
	switch n := node.(type) {
	case map[string]any:
		if k, ok := key.(string); ok {
			return n[k]
		}
	case []any:
		if idx := vscodeIndex(key, len(n)); idx >= 0 {
			return n[idx]
		}
	}
	return nil
}

func vscodeSetChild(node any, key any, value any) any {
	switch n := node.(type) {
	case map[string]any:
		if k, ok := key.(string); ok {
			n[k] = value
		}
		return n
	case []any:
		if idx := vscodeIndex(key, len(n)); idx >= 0 {
			n[idx] = value
		}
		return n
	case nil:
		if k, ok := key.(string); ok {
			return map[string]any{k: value}
		}
	}
	return node
}

func vscodeDeleteChild(node any, key any) any {
	switch n := node.(type) {
	case map[string]any:
		if k, ok := key.(string); ok {
			delete(n, k)
		}
		return n
	case []any:
		if idx := vscodeIndex(key, len(n)); idx >= 0 {
			return append(n[:idx], n[idx+1:]...)
		}
		return n
	}
	return node
}

func vscodeIndex(key any, length int) int {
	f, ok := key.(float64)
	if !ok {
		return -1
	}
	idx := int(f)
	if idx < 0 || idx >= length {
		return -1
	}
	return idx
}

func vscodeStr(m map[string]any, key string) string {
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func vscodeRequests(state map[string]any) []map[string]any {
	raw, _ := state["requests"].([]any)
	requests := make([]map[string]any, 0, len(raw))
	for _, r := range raw {
		if m, ok := r.(map[string]any); ok {
			requests = append(requests, m)
		}
	}
	return requests
}

func vscodeTimestampToRFC3339(ms float64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(int64(ms)).UTC().Format(time.RFC3339Nano)
}

// vscodeResponsePartText extracts assistant text from a markdownContent-shaped
// response part, trying the field layouts seen across VS Code schema versions.
func vscodeResponsePartText(part map[string]any) string {
	if content, ok := part["content"].(map[string]any); ok {
		if v := vscodeStr(content, "value"); v != "" {
			return v
		}
	}
	if v, ok := part["content"].(string); ok && v != "" {
		return v
	}
	if v := vscodeStr(part, "value"); v != "" {
		return v
	}
	return vscodeStr(part, "text")
}

func readVSCodeCopilotSession(path string) (SessionSummary, error) {
	state, err := readVSCodeChatSessionState(path)
	if err != nil || state == nil {
		return SessionSummary{}, nil
	}

	id := vscodeStr(state, "sessionId")
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	session := SessionSummary{
		Agent: "vscode-copilot",
		Path:  path,
		ID:    id,
		Cwd:   vscodeCopilotSessionCwd(path),
	}

	requests := vscodeRequests(state)
	if len(requests) > 0 {
		if message, ok := requests[0]["message"].(map[string]any); ok {
			session.Name = vscodeStr(message, "text")
		}
	}
	if session.Name == "" {
		session.Name = "(untitled)"
	}

	if creationDate, ok := state["creationDate"].(float64); ok {
		session.Timestamp = time.UnixMilli(int64(creationDate)).UTC()
	}
	if stat, err := os.Stat(path); err == nil {
		session.Timestamp = stat.ModTime()
	}

	for _, req := range requests {
		if model := vscodeStr(req, "modelId"); model != "" {
			session.Model = model
		}
		response, _ := req["response"].([]any)
		for _, r := range response {
			part, ok := r.(map[string]any)
			if !ok {
				continue
			}
			if part["kind"] == "toolInvocationSerialized" && isRepoGuideUnderstandTaskToolName(vscodeStr(part, "toolId")) {
				session.UsedRepoGuide = true
			}
		}
	}

	return session, nil
}

func buildVSCodeCopilotSessionEvents(path string) ([]SessionEvent, error) {
	state, err := readVSCodeChatSessionState(path)
	if err != nil || state == nil {
		return nil, err
	}

	var events []SessionEvent
	for _, req := range vscodeRequests(state) {
		timestamp := ""
		if ts, ok := req["timestamp"].(float64); ok {
			timestamp = vscodeTimestampToRFC3339(ts)
		}
		model := vscodeStr(req, "modelId")

		if message, ok := req["message"].(map[string]any); ok {
			if text := vscodeStr(message, "text"); strings.TrimSpace(text) != "" {
				events = append(events, SessionEvent{
					Kind:      "prompt",
					Role:      "user",
					Timestamp: timestamp,
					Text:      text,
				})
			}
		}

		response, _ := req["response"].([]any)
		for _, r := range response {
			part, ok := r.(map[string]any)
			if !ok {
				continue
			}
			switch part["kind"] {
			case "markdownContent":
				if text := vscodeResponsePartText(part); strings.TrimSpace(text) != "" {
					events = append(events, SessionEvent{
						Kind:      "assistant_message",
						Role:      "assistant",
						Timestamp: timestamp,
						Text:      text,
						Model:     model,
					})
				}
			case "toolInvocationSerialized":
				event := SessionEvent{
					Kind:       "tool_call",
					Role:       "assistant",
					Timestamp:  timestamp,
					ToolName:   vscodeStr(part, "toolId"),
					ToolCallID: vscodeStr(part, "toolCallId"),
					Model:      model,
				}
				if msg, ok := part["invocationMessage"].(map[string]any); ok {
					event.CommandText = vscodeStr(msg, "value")
				}
				events = append(events, event)
			}
		}
	}

	numberSessionEvents(events)
	return events, nil
}
