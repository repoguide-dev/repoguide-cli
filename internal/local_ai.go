package internal

import (
	"path/filepath"

	"github.com/repoguide/repoguide-cli/internal/config"
	"github.com/repoguide/repoguide-cli/internal/runtime"
)

const (
	LocalAIBackendAPI       = "api"
	LocalAIBackendClaudeCLI = "claude_cli"
)

func LocalAIBackendFromRuntimeConfig(cfg runtime.Config) string {
	if cfg.UseClaudeCLI {
		return LocalAIBackendClaudeCLI
	}
	if cfg.AnthropicAPIKey != "" {
		return LocalAIBackendAPI
	}
	return ""
}

func LocalRuntimeConfigForRepo(repoID string) runtime.Config {
	if repoID != "" {
		if cfg, err := LoadRepoConfigFile(filepath.Join(RepoGuideDir(), "repos", repoID)); err == nil {
			if resolved := localRuntimeConfigFromRepoConfig(cfg); resolved.UseClaudeCLI || resolved.AnthropicAPIKey != "" {
				return resolved
			}
		}
	}
	if key := config.AnthropicAPIKey(); key != "" {
		return runtime.Config{AnthropicAPIKey: key}
	}
	if config.UseClaudeCLI() {
		return runtime.Config{UseClaudeCLI: true}
	}
	return runtime.Config{}
}

func localRuntimeConfigFromRepoConfig(cfg RepoConfig) runtime.Config {
	switch cfg.LocalAIBackend {
	case LocalAIBackendAPI:
		if key := config.AnthropicAPIKey(); key != "" {
			return runtime.Config{AnthropicAPIKey: key}
		}
		return runtime.Config{}
	case LocalAIBackendClaudeCLI:
		return runtime.Config{UseClaudeCLI: true}
	default:
		return runtime.Config{}
	}
}
