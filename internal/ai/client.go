package ai

import (
	"context"
	"os"

	"github.com/repoguide/repoguide-core/contracts/v1"
	"github.com/repoguide/repoguide-core/model"
)

// LLM is the AI interface used by the services layer. Implemented by *Client.
type LLM interface {
	DeriveTopicsAndGenerateContext(ctx context.Context, bundle contracts.RepoAnalysisBundle) ([]model.TopicContext, Usage, error)
	GenerateRepoContext(ctx context.Context, bundle contracts.RepoAnalysisBundle, docs map[string]string) (string, Usage, error)
	GenerateTopicContextFromFeedback(ctx context.Context, suggestedName, feedback string, bundle contracts.RepoAnalysisBundle, events []model.SessionEvent, existingTopics []model.TopicContext) (*model.TopicContext, string, Usage, error)
	SelectTopic(ctx context.Context, repoContext string, topics []TopicSummary, task string, prompts []string) (SelectTopicResult, Usage, error)
	WriteOrientationHint(ctx context.Context, repoContext string, topic model.TopicContext, task string, prompts []string, priorSessions []PriorSession) (string, bool, Usage, error)
	ClassifyFeedback(ctx context.Context, fb *model.MCPFeedback) (*model.FeedbackClassification, Usage, error)
	CurateTopicContext(ctx context.Context, topic *model.TopicContext, feedbacks []*model.MCPFeedback, pending []model.TopicPatchSuggestion, sessions []TopicCurationSession) (*TopicCuration, Usage, error)
	PatchRepoContext(ctx context.Context, current string, feedbacks []*model.MCPFeedback, sessions []RepoContextSession) (*RepoContextPatch, Usage, error)
}

// Client is a handle to the AI capabilities. APIKey overrides ANTHROPIC_API_KEY
// env var. UseCLI routes calls through the `claude` CLI instead of the HTTP API.
// Inject LLM into services; swap for a nil/stub in tests.
type Client struct {
	APIKey string
	UseCLI bool
}

// NewClient returns a Client backed by the Anthropic HTTP API.
// Empty apiKey falls back to ANTHROPIC_API_KEY env var.
func NewClient(apiKey string) *Client {
	return &Client{APIKey: apiKey}
}

// NewCLIClient returns a Client that calls `claude -p` as a subprocess.
func NewCLIClient() *Client {
	return &Client{UseCLI: true}
}

// withKey temporarily sets the API key in env (and the useCLI global) for calls that read from env.
// ponytail: global env/flag mutation; acceptable for single-process CLI; revisit if concurrent calls matter.
func (c *Client) withKey(f func()) {
	if c != nil && c.UseCLI {
		prev := useCLI
		useCLI = true
		defer func() { useCLI = prev }()
		f()
		return
	}
	if c == nil || c.APIKey == "" {
		f()
		return
	}
	prev := os.Getenv("ANTHROPIC_API_KEY")
	_ = os.Setenv("ANTHROPIC_API_KEY", c.APIKey)
	defer func() { _ = os.Setenv("ANTHROPIC_API_KEY", prev) }()
	f()
}

func (c *Client) DeriveTopicsAndGenerateContext(ctx context.Context, bundle contracts.RepoAnalysisBundle) ([]model.TopicContext, Usage, error) {
	var topics []model.TopicContext
	var usage Usage
	var err error
	c.withKey(func() { topics, usage, err = DeriveTopicsAndGenerateContext(ctx, bundle) })
	return topics, usage, err
}

func (c *Client) GenerateRepoContext(ctx context.Context, bundle contracts.RepoAnalysisBundle, docs map[string]string) (string, Usage, error) {
	var content string
	var usage Usage
	var err error
	c.withKey(func() { content, usage, err = GenerateRepoContext(ctx, bundle, docs) })
	return content, usage, err
}

func (c *Client) GenerateTopicContextFromFeedback(ctx context.Context, suggestedName, feedback string, bundle contracts.RepoAnalysisBundle, events []model.SessionEvent, existingTopics []model.TopicContext) (*model.TopicContext, string, Usage, error) {
	var topic *model.TopicContext
	var reason string
	var usage Usage
	var err error
	c.withKey(func() {
		topic, reason, usage, err = GenerateTopicContextFromFeedback(ctx, suggestedName, feedback, bundle, events, existingTopics)
	})
	return topic, reason, usage, err
}

func (c *Client) SelectTopic(ctx context.Context, repoContext string, topics []TopicSummary, task string, prompts []string) (SelectTopicResult, Usage, error) {
	var result SelectTopicResult
	var usage Usage
	var err error
	c.withKey(func() { result, usage, err = SelectTopic(ctx, repoContext, topics, task, prompts) })
	return result, usage, err
}

func (c *Client) WriteOrientationHint(ctx context.Context, repoContext string, topic model.TopicContext, task string, prompts []string, priorSessions []PriorSession) (string, bool, Usage, error) {
	var hint string
	var found bool
	var usage Usage
	var err error
	c.withKey(func() {
		hint, found, usage, err = WriteOrientationHint(ctx, repoContext, topic, task, prompts, priorSessions)
	})
	return hint, found, usage, err
}

func (c *Client) ClassifyFeedback(ctx context.Context, fb *model.MCPFeedback) (*model.FeedbackClassification, Usage, error) {
	var clf *model.FeedbackClassification
	var usage Usage
	var err error
	c.withKey(func() { clf, usage, err = ClassifyFeedback(ctx, fb) })
	return clf, usage, err
}

func (c *Client) CurateTopicContext(ctx context.Context, topic *model.TopicContext, feedbacks []*model.MCPFeedback, pending []model.TopicPatchSuggestion, sessions []TopicCurationSession) (*TopicCuration, Usage, error) {
	var curation *TopicCuration
	var usage Usage
	var err error
	c.withKey(func() { curation, usage, err = CurateTopicContext(ctx, topic, feedbacks, pending, sessions) })
	return curation, usage, err
}

func (c *Client) PatchRepoContext(ctx context.Context, current string, feedbacks []*model.MCPFeedback, sessions []RepoContextSession) (*RepoContextPatch, Usage, error) {
	var patch *RepoContextPatch
	var usage Usage
	var err error
	c.withKey(func() { patch, usage, err = PatchRepoContext(ctx, current, feedbacks, sessions) })
	return patch, usage, err
}
