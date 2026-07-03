// Package services holds the domain logic layer. Services are runtime-agnostic:
// they receive a Store, an AI client, and a job dispatcher, and produce results
// that HTTP handlers, MCP tools, and CLI commands can call without knowing
// whether storage is SQLite or Postgres.
package services

import (
	"github.com/repoguide/repoguide-cli/internal/ai"
	storecontract "github.com/repoguide/repoguide-cli/internal/store"
)

// Deps holds everything a service set needs at construction time.
type Deps struct {
	Store storecontract.Store
	AI    ai.LLM
	Jobs  storecontract.JobDispatcher
	Mode  string // "local" | "cloud"
}

// Services is the central application hub. Handlers and MCP tools get a *Services
// and call methods on specific sub-services instead of touching store or AI directly.
type Services struct {
	Repos    *RepoService
	Sessions *SessionService
	Topics   *TopicService
	Context  *ContextService
	Feedback *FeedbackService
	Jobs     *JobService
	MCP      *MCPService
}

// New builds a fully wired Services from the given Deps.
func New(d Deps) *Services {
	s := &Services{}
	s.Repos = &RepoService{store: d.Store}
	s.Sessions = &SessionService{store: d.Store}
	s.Topics = &TopicService{store: d.Store, ai: d.AI, jobs: d.Jobs}
	s.Context = &ContextService{store: d.Store, ai: d.AI}
	s.Feedback = &FeedbackService{store: d.Store, ai: d.AI}
	s.Jobs = &JobService{store: d.Store, ai: d.AI, services: s}
	s.MCP = &MCPService{store: d.Store, ai: d.AI}
	return s
}
