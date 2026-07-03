package mcp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

type MCPSmokeOptions struct {
	Command  string
	Args     []string
	Task     string
	RepoPath string
	TopicID  string
	Only     string // if set, only run this tool call (e.g. "understand_task")
}

type MCPSmokeCheck struct {
	Name     string
	OK       bool
	Detail   string
	Response map[string]any
}

type MCPSmokeTopic struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

type MCPSmokeReport struct {
	Checks   []MCPSmokeCheck
	Topics   []MCPSmokeTopic
	TopicID  string
	RepoID   string
	RepoPath string
}

func (r MCPSmokeReport) Success() bool {
	for _, check := range r.Checks {
		if !check.OK {
			return false
		}
	}
	return true
}

func RunMCPContextPack(opts MCPSmokeOptions) (map[string]any, error) {
	cmd := exec.Command(opts.Command, opts.Args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	client := newMCPSmokeClient(stdout, stdin)

	if _, err := client.request("initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "repoguide-smoke", "version": "0.0.0"},
	}); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	_ = client.notify("notifications/initialized", map[string]any{})

	resp, err := client.request("tools/call", map[string]any{
		"name": "repoguide_get_repo_experience",
		"arguments": map[string]any{
			"task":     opts.Task,
			"topic_id": opts.TopicID,
		},
	})
	_ = stdin.Close()
	_ = cmd.Wait()
	if err != nil {
		return nil, err
	}

	result, _, err := extractStructuredContent(resp.Result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func RunMCPSmoke(opts MCPSmokeOptions) (MCPSmokeReport, error) {
	cmd := exec.Command(opts.Command, opts.Args...)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return MCPSmokeReport{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return MCPSmokeReport{}, err
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return MCPSmokeReport{}, err
	}

	client := newMCPSmokeClient(stdout, stdin)
	report, runErr := runMCPSmoke(client, opts.Task, opts.RepoPath, opts.TopicID, opts.Only)

	closeErr := stdin.Close()
	waitErr := cmd.Wait()

	if runErr != nil {
		if stderr.Len() > 0 {
			return report, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(stderr.String()))
		}
		return report, runErr
	}
	if closeErr != nil {
		return report, closeErr
	}
	if waitErr != nil {
		if stderr.Len() > 0 {
			return report, fmt.Errorf("mcp server exited with error: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
		}
		return report, fmt.Errorf("mcp server exited with error: %w", waitErr)
	}

	return report, nil
}

type mcpSmokeClient struct {
	reader *bufio.Reader
	writer *bufio.Writer
	nextID int
}

func newMCPSmokeClient(stdout io.Reader, stdin io.Writer) *mcpSmokeClient {
	return &mcpSmokeClient{
		reader: bufio.NewReader(stdout),
		writer: bufio.NewWriter(stdin),
		nextID: 1,
	}
}

func (c *mcpSmokeClient) notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	return c.write(body)
}

func (c *mcpSmokeClient) request(method string, params any) (mcpResponse, error) {
	id := c.nextID
	c.nextID++

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return mcpResponse{}, err
	}
	if err := c.write(body); err != nil {
		return mcpResponse{}, err
	}

	respBody, err := readMCPMessage(c.reader)
	if err != nil {
		return mcpResponse{}, err
	}

	var resp mcpResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return mcpResponse{}, err
	}
	if resp.Error != nil {
		return resp, fmt.Errorf("%s", resp.Error.Message)
	}
	return resp, nil
}

func (c *mcpSmokeClient) write(body []byte) error {
	if _, err := fmt.Fprintf(c.writer, "Content-Length: %d\r\n\r\n", len(body)); err != nil {
		return err
	}
	if _, err := c.writer.Write(body); err != nil {
		return err
	}
	return c.writer.Flush()
}

func runMCPSmoke(client *mcpSmokeClient, task, repoPath, requestedTopicID, only string) (MCPSmokeReport, error) {
	report := MCPSmokeReport{RepoPath: repoPath}

	appendCheck := func(name string, ok bool, detail string) {
		report.Checks = append(report.Checks, MCPSmokeCheck{Name: name, OK: ok, Detail: detail})
	}

	appendCheck("start_server", true, "spawned mcp serve")

	if _, err := client.request("initialize", map[string]any{
		"protocolVersion": mcpProtocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "repoguide-smoke",
			"version": "0.0.0",
		},
	}); err != nil {
		appendCheck("initialize", false, err.Error())
		return report, err
	}
	appendCheck("initialize", true, mcpProtocolVersion)

	if err := client.notify("notifications/initialized", map[string]any{}); err != nil {
		return report, err
	}

	toolsResp, err := client.request("tools/list", map[string]any{})
	if err != nil {
		appendCheck("tools_list", false, err.Error())
		return report, err
	}

	toolNames, err := extractToolNames(toolsResp.Result)
	if err != nil {
		appendCheck("tools_list", false, err.Error())
		return report, err
	}
	appendCheck("tools_list", true, strings.Join(toolNames, ", "))
	if only == "" {
		appendCheck("has_repoguide_list_topics", contains(toolNames, "repoguide_list_topics"), "repoguide_list_topics")
		appendCheck("has_repoguide_get_test_context", contains(toolNames, "repoguide_get_test_context"), "repoguide_get_test_context")
		appendCheck("has_repoguide_get_search_context", contains(toolNames, "repoguide_get_search_context"), "repoguide_get_search_context")
		appendCheck("has_repoguide_get_repo_experience", contains(toolNames, "repoguide_get_repo_experience"), "repoguide_get_repo_experience")
		if !report.Success() {
			return report, nil
		}
	}

	listTopicsResp, err := client.request("tools/call", map[string]any{
		"name": "repoguide_list_topics",
		"arguments": map[string]any{
			"task":      task,
			"repo_path": repoPath,
		},
	})
	if err != nil {
		if only == "" {
			appendCheck("call_repoguide_list_topics", false, err.Error())
		}
		return report, err
	}

	topicsPayload, topicsErr, err := extractStructuredContent(listTopicsResp.Result)
	if err != nil {
		if only == "" {
			appendCheck("call_repoguide_list_topics", false, err.Error())
		}
		return report, err
	}
	if topicsErr != "" {
		if only == "" {
			appendCheck("call_repoguide_list_topics", false, topicsErr)
		}
		return report, nil
	}

	topicID, repoID, allTopics := extractTopics(topicsPayload)
	if strings.TrimSpace(requestedTopicID) != "" {
		topicID = requestedTopicID
	}
	report.TopicID = topicID
	report.RepoID = repoID
	report.Topics = allTopics
	if only == "" {
		report.Checks = append(report.Checks, MCPSmokeCheck{Name: "call_repoguide_list_topics", OK: true, Detail: fmt.Sprintf("topics=%d", len(allTopics)), Response: topicsPayload})
	}

	run := func(tool string) bool { return only == "" || only == tool }

	if run("understand_task") {
		understandArgs := map[string]any{
			"task":      task,
			"repo_path": repoPath,
		}
		if repoID != "" {
			understandArgs["repo_id"] = repoID
		}
		if requestedTopicID != "" {
			understandArgs["topic_id"] = requestedTopicID
		}
		understandResp, err := client.request("tools/call", map[string]any{
			"name":      "repoguide_get_repo_experience",
			"arguments": understandArgs,
		})
		if err != nil {
			appendCheck("call_repoguide_get_repo_experience", false, err.Error())
			return report, err
		}
		if understandPayload, understandErr, err := extractStructuredContent(understandResp.Result); err != nil {
			appendCheck("call_repoguide_get_repo_experience", false, err.Error())
			return report, err
		} else if understandErr != "" {
			appendCheck("call_repoguide_get_repo_experience", false, understandErr)
		} else {
			report.Checks = append(report.Checks, MCPSmokeCheck{Name: "call_repoguide_get_repo_experience", OK: true, Response: understandPayload})
			// When a topic is resolved, understand_task should embed test + search context inline.
			if resolvedTopic, _ := understandPayload["topic_id"].(string); resolvedTopic != "" {
				text, _ := understandPayload["text"].(string)
				appendCheck("understand_task_has_test_context", strings.Contains(text, "<test_context>"), fmt.Sprintf("topic_id=%q", resolvedTopic))
				appendCheck("understand_task_has_search_context", strings.Contains(text, "<search_context>"), fmt.Sprintf("topic_id=%q", resolvedTopic))
			}
		}
	}

	return report, nil
}

func extractToolNames(result any) ([]string, error) {
	payload, ok := result.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tools/list result type = %T", result)
	}
	rawTools, ok := payload["tools"].([]any)
	if !ok {
		return nil, fmt.Errorf("tools/list tools type = %T", payload["tools"])
	}
	names := make([]string, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("tool type = %T", rawTool)
		}
		name, _ := tool["name"].(string)
		if name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}

func extractStructuredContent(result any) (map[string]any, string, error) {
	payload, ok := result.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("tool result type = %T", result)
	}
	if isError, _ := payload["isError"].(bool); isError {
		content, _ := payload["content"].([]any)
		if len(content) > 0 {
			if first, ok := content[0].(map[string]any); ok {
				if text, _ := first["text"].(string); text != "" {
					return nil, text, nil
				}
			}
		}
		return nil, "tool call failed", nil
	}
	structured, ok := payload["structuredContent"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("structuredContent type = %T", payload["structuredContent"])
	}
	return structured, "", nil
}

func extractTopics(payload map[string]any) (firstID, repoID string, topics []MCPSmokeTopic) {
	repoID, _ = payload["repo_id"].(string)
	rawTopics, ok := payload["topics"].([]any)
	if !ok {
		return "", repoID, nil
	}
	for _, rawTopic := range rawTopics {
		t, ok := rawTopic.(map[string]any)
		if !ok {
			continue
		}
		id, _ := t["id"].(string)
		title, _ := t["title"].(string)
		if title == "" {
			title, _ = t["name"].(string)
		}
		if id == "" {
			continue
		}
		if firstID == "" {
			firstID = id
		}
		topics = append(topics, MCPSmokeTopic{ID: id, Title: title})
	}
	return firstID, repoID, topics
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
