package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/repoguide/repoguide-core/contracts/v1"
)

// ErrServiceUnavailable is returned when the Claude API is temporarily unavailable
// (spend limit hit, rate limit, or similar). Callers should surface a generic
// "service unavailable" message rather than exposing the raw API response.
var ErrServiceUnavailable = errors.New("AI service temporarily unavailable")

// useCLI and cliBackend are set by Client.withKey when the client is configured
// to use a local AI CLI.
// ponytail: global flag, same thread-safety caveat as ANTHROPIC_API_KEY env mutation.
var useCLI bool
var cliBackend string

const anthropicVersion = "2023-06-01"
const anthropicRequestTimeout = 15 * time.Minute

var anthropicAPIURL = "https://api.anthropic.com/v1/messages"
var anthropicHTTPClient = &http.Client{Timeout: anthropicRequestTimeout}

type Usage = contracts.Usage

// callClaudeCLI invokes the configured local AI CLI and returns the response text.
// system is prepended to the user prompt when non-empty; no caching, no token counts.
func callClaudeCLI(ctx context.Context, model, system, userPrompt string) (string, Usage, error) {
	prompt := userPrompt
	if system != "" {
		prompt = system + "\n\n" + userPrompt
	}
	backend := cliBackend
	if backend == "" {
		backend = "claude"
	}
	var cmd *exec.Cmd
	switch backend {
	case "claude":
		cmd = exec.CommandContext(ctx, "claude", "-p", "--model", model, "--no-session-persistence")
		cmd.Stdin = strings.NewReader(prompt)
	case "codex":
		// Codex uses the model configured in the logged-in CLI profile.
		cmd = exec.CommandContext(ctx, "codex", "exec", "--skip-git-repo-check", "-")
		cmd.Stdin = strings.NewReader(prompt)
	case "gemini":
		// Gemini uses the model configured in the logged-in CLI profile.
		cmd = exec.CommandContext(ctx, "gemini", "-p", "")
		cmd.Stdin = strings.NewReader(prompt)
	default:
		return "", Usage{}, fmt.Errorf("unsupported local AI CLI backend %q", backend)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", Usage{}, fmt.Errorf("%s cli: %w", backend, err)
	}
	return strings.TrimSpace(string(out)), Usage{Model: model}, nil
}

// callClaude sends a single user message to the Anthropic Messages API and returns the assistant text.
func callClaude(ctx context.Context, model, userPrompt string) (string, Usage, error) {
	return callClaudeMaxTokens(ctx, model, userPrompt, 8192)
}

func callClaudeMaxTokens(ctx context.Context, model, userPrompt string, maxTokens int) (string, Usage, error) {
	if useCLI {
		return callClaudeCLI(ctx, model, "", userPrompt)
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", Usage{}, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	requestCtx, cancel := newAnthropicRequestContext(ctx)
	defer cancel()

	payload := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   []map[string]string{{"role": "user", "content": userPrompt}},
	}
	if model == "claude-sonnet-5" {
		payload["thinking"] = map[string]string{"type": "disabled"}
		payload["output_config"] = map[string]string{"effort": "medium"}
	}
	body, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	resp, err := anthropicHTTPClient.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, classifyAPIError(resp.StatusCode, data)
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		StopReason string `json:"stop_reason"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", Usage{}, fmt.Errorf("claude API: parse response: %w", err)
	}
	usage := Usage{Model: model, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}
	for _, c := range out.Content {
		if c.Type == "text" {
			return c.Text, usage, nil
		}
	}
	contentTypes := make([]string, 0, len(out.Content))
	for _, content := range out.Content {
		contentTypes = append(contentTypes, content.Type)
	}
	return "", usage, fmt.Errorf("claude API: no text content (stop_reason=%s content_types=%v input_tokens=%d output_tokens=%d)", out.StopReason, contentTypes, usage.InputTokens, usage.OutputTokens)
}

// callClaudeWithSystem sends a call with a cached system prompt and a per-request user message.
func callClaudeWithSystem(ctx context.Context, model, system, userPrompt string, maxTokens int) (string, Usage, error) {
	if useCLI {
		return callClaudeCLI(ctx, model, system, userPrompt)
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		return "", Usage{}, fmt.Errorf("ANTHROPIC_API_KEY not set")
	}

	requestCtx, cancel := newAnthropicRequestContext(ctx)
	defer cancel()

	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"system": []map[string]any{{
			"type":          "text",
			"text":          system,
			"cache_control": map[string]string{"type": "ephemeral"},
		}},
		"messages": []map[string]any{{"role": "user", "content": userPrompt}},
	})

	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", Usage{}, err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	resp, err := anthropicHTTPClient.Do(req)
	if err != nil {
		return "", Usage{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", Usage{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return "", Usage{}, classifyAPIError(resp.StatusCode, data)
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", Usage{}, fmt.Errorf("claude API: parse response: %w", err)
	}
	usage := Usage{Model: model, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}
	for _, c := range out.Content {
		if c.Type == "text" {
			return c.Text, usage, nil
		}
	}
	return "", usage, fmt.Errorf("claude API: no text content in response")
}

// classifyAPIError maps a non-200 Claude API response to either ErrServiceUnavailable
// (spend/rate limits, overload) or a generic internal error with status+body for logging.
// ponytail: string-contains check; extend to JSON parse if more error types need distinction.
func classifyAPIError(status int, body []byte) error {
	raw := fmt.Errorf("claude API %d: %s", status, body)
	if status == http.StatusTooManyRequests ||
		bytes.Contains(body, []byte("usage limits")) ||
		bytes.Contains(body, []byte("usage_limit")) ||
		bytes.Contains(body, []byte("overloaded")) {
		return fmt.Errorf("%w: %w", ErrServiceUnavailable, raw)
	}
	return raw
}

func newAnthropicRequestContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), anthropicRequestTimeout)
}

// stripFences removes optional markdown code fences from LLM output.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.Index(s, "\n"); i != -1 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}

// extractJSON pulls the outermost {...} from an LLM response, handling prose
// wrappers and trailing code fences that stripFences misses when output doesn't
// start with a fence.
func extractJSON(s string) string {
	s = stripFences(s)
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start == -1 || end == -1 || end < start {
		return s
	}
	return s[start : end+1]
}
