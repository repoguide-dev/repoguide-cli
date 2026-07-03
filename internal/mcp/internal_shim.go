package mcp

import (
	"encoding/json"

	internalpkg "github.com/repoguide/repoguide-cli/internal"
	"github.com/repoguide/repoguide-cli/internal/repo"
	"github.com/repoguide/repoguide-cli/internal/services"
	"github.com/repoguide/repoguide-cli/internal/sessionimport"
)

// This file re-exposes root-package ("internal"), internal/repo, and
// internal/sessionimport types and helpers under the unqualified names the
// mcp*.go files already use, so those files (originally written as part of
// package internal) did not need per-call-site edits when they were split
// into this package.

type MCPTopicContext = sessionimport.MCPTopicContext
type MCPSearchContext = sessionimport.MCPSearchContext
type CloudClient = sessionimport.CloudClient

var ErrRepoNotInitialized = repo.ErrRepoNotInitialized

func home(parts ...string) string { return internalpkg.Home(parts...) }

func gitOutputAt(repoRoot string, args ...string) (string, error) {
	return internalpkg.GitOutputAt(repoRoot, args...)
}

func writeJSON(path string, value any) error { return internalpkg.WriteJSON(path, value) }

func RepoGuideDir() string { return repo.RepoGuideDir() }

func SetManagedCommitHooks(storeDir, repoRoot string, enabled bool) ([]internalpkg.HookStatus, error) {
	return internalpkg.SetManagedCommitHooks(storeDir, repoRoot, enabled)
}

func HideInjectedFiles(repoRoot string) { internalpkg.HideInjectedFiles(repoRoot) }

func GitRepoID(repoRoot string) (string, error) { return internalpkg.GitRepoID(repoRoot) }

func ListConfiguredRepos() ([]internalpkg.RepoConfig, error) { return repo.ListConfiguredRepos() }

type RepoConfig = internalpkg.RepoConfig
type InitOptions = internalpkg.InitOptions
type InitResult = internalpkg.InitResult

func InitRepo(opts InitOptions) (InitResult, error) { return internalpkg.InitRepo(opts) }

func LoadRepoConfigFile(storeDir string) (RepoConfig, error) {
	return internalpkg.LoadRepoConfigFile(storeDir)
}

func commitHooksEnabled(cfg RepoConfig) bool { return internalpkg.CommitHooksEnabled(cfg) }

const (
	hookPreCommit  = internalpkg.HookPreCommit
	hookPostCommit = internalpkg.HookPostCommit
)

func gitHookFilePath(repoRoot, hookName string) (string, error) {
	return internalpkg.GitHookFilePath(repoRoot, hookName)
}

func hookFileContainsManagedBlock(path string) bool {
	return internalpkg.HookFileContainsManagedBlock(path)
}

func RunManagedCommitHook(action string) error { return internalpkg.RunManagedCommitHook(action) }

func gitShowIndexFile(repoRoot, path string) (string, error) {
	return internalpkg.GitShowIndexFile(repoRoot, path)
}

type MCPCallCreateRequest = sessionimport.MCPCallCreateRequest
type MCPFeedbackRequest = sessionimport.MCPFeedbackRequest

func NewCloudClient(baseURL, token string, svc *services.Services, localSvcFactory func(repoID string) *services.Services) CloudClient {
	return sessionimport.NewCloudClient(baseURL, token, svc, localSvcFactory)
}

// jsonString is duplicated from internal/sessionimport/session_parser_helpers.go
// (a trivial, self-contained JSON-string-decoding helper).
func jsonString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err == nil {
		return value
	}
	return ""
}
