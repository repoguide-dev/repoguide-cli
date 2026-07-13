package internal

import (
	"path/filepath"

	"github.com/repoguide/repoguide-cli/internal/config"
	"github.com/repoguide/repoguide-cli/internal/runtime"
)

const (
	LocalAIBackendAPI       = "api"
	LocalAIBackendClaudeCLI = "claude_cli"
	LocalAIBackendCodexCLI  = "codex_cli"
	LocalAIBackendGeminiCLI = "gemini_cli"
)

func LocalAIBackendFromRuntimeConfig(cfg runtime.Config) string {
	if cfg.UseClaudeCLI {
		return LocalAIBackendClaudeCLI
	}
	switch cfg.CLIBackend {
	case "claude":
		return LocalAIBackendClaudeCLI
	case "codex":
		return LocalAIBackendCodexCLI
	case "gemini":
		return LocalAIBackendGeminiCLI
	}
	if cfg.AnthropicAPIKey != "" {
		return LocalAIBackendAPI
	}
	return ""
}

func LocalRuntimeConfigForRepo(repoID string) runtime.Config {
	if repoID != "" {
		if cfg, err := LoadRepoConfigFile(filepath.Join(RepoGuideDir(), "repos", repoID)); err == nil {
			if resolved := localRuntimeConfigFromRepoConfig(cfg); resolved.CLIBackend != "" || resolved.UseClaudeCLI || resolved.AnthropicAPIKey != "" {
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
		return runtime.Config{CLIBackend: "claude"}
	case LocalAIBackendCodexCLI:
		return runtime.Config{CLIBackend: "codex"}
	case LocalAIBackendGeminiCLI:
		return runtime.Config{CLIBackend: "gemini"}
	default:
		return runtime.Config{}
	}
}
