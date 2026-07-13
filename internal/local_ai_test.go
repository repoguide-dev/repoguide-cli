package internal

import (
	"testing"

	"github.com/repoguide/repoguide-cli/internal/runtime"
)

func TestLocalAIBackendFromRuntimeConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  runtime.Config
		want string
	}{
		{"api", runtime.Config{AnthropicAPIKey: "key"}, LocalAIBackendAPI},
		{"legacy Claude CLI", runtime.Config{UseClaudeCLI: true}, LocalAIBackendClaudeCLI},
		{"Claude CLI", runtime.Config{CLIBackend: "claude"}, LocalAIBackendClaudeCLI},
		{"Codex CLI", runtime.Config{CLIBackend: "codex"}, LocalAIBackendCodexCLI},
		{"Gemini CLI", runtime.Config{CLIBackend: "gemini"}, LocalAIBackendGeminiCLI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LocalAIBackendFromRuntimeConfig(tt.cfg); got != tt.want {
				t.Fatalf("LocalAIBackendFromRuntimeConfig() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalRuntimeConfigFromRepoConfig(t *testing.T) {
	tests := []struct {
		backend string
		want    string
	}{
		{LocalAIBackendClaudeCLI, "claude"},
		{LocalAIBackendCodexCLI, "codex"},
		{LocalAIBackendGeminiCLI, "gemini"},
	}
	for _, tt := range tests {
		t.Run(tt.backend, func(t *testing.T) {
			if got := localRuntimeConfigFromRepoConfig(RepoConfig{LocalAIBackend: tt.backend}).CLIBackend; got != tt.want {
				t.Fatalf("CLIBackend = %q, want %q", got, tt.want)
			}
		})
	}
}
