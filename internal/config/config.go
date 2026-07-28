// Package config manages the RepoGuide global config file.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Config struct {
	AnthropicAPIKey string `json:"anthropic_api_key,omitempty"`
	UseClaudeCLI    bool   `json:"use_claude_cli,omitempty"`
	AutoFeedback    bool   `json:"auto_feedback,omitempty"`
	// HoldoutPct is the percentage of sessions for which the MCP server
	// withholds the repo-experience briefing, creating a randomized control
	// group. 0 disables the experiment.
	HoldoutPct int `json:"holdout_pct,omitempty"`
}

// dataDirName is overridden in the local development build.
var dataDirName = ".repoguide"

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, dataDirName, "config.json")
}

func load() Config {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return Config{}
	}
	var c Config
	_ = json.Unmarshal(data, &c)
	return c
}

func save(c Config) error {
	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0600)
}

// AnthropicAPIKey returns the Anthropic API key: env var first, then config file.
func AnthropicAPIKey() string {
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		return v
	}
	return load().AnthropicAPIKey
}

// SetAnthropicAPIKey writes the key to the global config file.
func SetAnthropicAPIKey(key string) error {
	c := load()
	c.AnthropicAPIKey = key
	return save(c)
}

// AutoFeedback reports whether the Stop hook should submit feedback
// automatically instead of asking the user first. Defaults to false (ask).
func AutoFeedback() bool {
	return load().AutoFeedback
}

// SetAutoFeedback saves the auto-feedback preference to the global config file.
func SetAutoFeedback(v bool) error {
	c := load()
	c.AutoFeedback = v
	return save(c)
}

// HoldoutPct returns the configured holdout percentage, clamped to 0-100.
// 0 means the experiment is off and every session gets its briefing.
func HoldoutPct() int {
	v := load().HoldoutPct
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// SetHoldoutPct saves the holdout percentage to the global config file.
func SetHoldoutPct(v int) error {
	if v < 0 || v > 100 {
		return fmt.Errorf("holdout must be between 0 and 100, got %d", v)
	}
	c := load()
	c.HoldoutPct = v
	return save(c)
}

// UseClaudeCLI returns true when the user has configured the `claude` CLI as the AI backend.
func UseClaudeCLI() bool {
	return load().UseClaudeCLI
}

// SetUseClaudeCLI saves the claude CLI preference to the global config file.
func SetUseClaudeCLI(v bool) error {
	c := load()
	c.UseClaudeCLI = v
	return save(c)
}
