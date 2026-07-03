// Package runtime wires Store + AI + Jobs into a running Services set.
// Call NewLocalRuntime for the CLI (SQLite), NewCloudRuntime for the backend (Postgres).
package runtime

import (
	"github.com/repoguide/repoguide-cli/internal/ai"
	"github.com/repoguide/repoguide-cli/internal/services"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
)

// Runtime is the fully assembled application layer.
type Runtime struct {
	Store    storecontract.Store
	Services *services.Services
	Jobs     storecontract.JobDispatcher
}

// Config shared by both local and cloud runtimes.
type Config struct {
	// AnthropicAPIKey for the AI client. Defaults to ANTHROPIC_API_KEY env var.
	AnthropicAPIKey string
	// UseClaudeCLI routes AI calls through the `claude` CLI instead of the HTTP API.
	UseClaudeCLI bool
}

// New builds a Runtime from the given store, dispatcher, and config.
// Use the runtime-specific constructors (NewLocalRuntime, NewCloudRuntime) in
// their respective adapter packages.
func New(s storecontract.Store, jobs storecontract.JobDispatcher, cfg Config) *Runtime {
	var aiClient ai.LLM
	if cfg.UseClaudeCLI {
		aiClient = ai.NewCLIClient()
	} else {
		aiClient = ai.NewClient(cfg.AnthropicAPIKey)
	}
	svc := services.New(services.Deps{
		Store: s,
		AI:    aiClient,
		Jobs:  jobs,
		Mode:  "local",
	})
	return &Runtime{Store: s, Services: svc, Jobs: jobs}
}
