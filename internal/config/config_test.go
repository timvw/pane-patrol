package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaults(t *testing.T) {
	cfg := Defaults()

	if cfg.Provider != "anthropic" {
		t.Errorf("Provider: got %q, want %q", cfg.Provider, "anthropic")
	}
	if cfg.Model != "claude-sonnet-4-5" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "claude-sonnet-4-5")
	}
	if cfg.MaxTokens != 4096 {
		t.Errorf("MaxTokens: got %d, want %d", cfg.MaxTokens, 4096)
	}
	if cfg.Parallel != 10 {
		t.Errorf("Parallel: got %d, want %d", cfg.Parallel, 10)
	}
	if cfg.Refresh != "5s" {
		t.Errorf("Refresh: got %q, want %q", cfg.Refresh, "5s")
	}
}

func TestMatchesExcludeList(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		patterns []string
		want     bool
	}{
		{
			name:     "exact match",
			input:    "my-session",
			patterns: []string{"my-session"},
			want:     true,
		},
		{
			name:     "exact no match",
			input:    "my-session",
			patterns: []string{"other-session"},
			want:     false,
		},
		{
			name:     "prefix glob match",
			input:    "AIGGTM-1234-feature",
			patterns: []string{"AIGGTM-*"},
			want:     true,
		},
		{
			name:     "prefix glob no match",
			input:    "my-session",
			patterns: []string{"AIGGTM-*"},
			want:     false,
		},
		{
			name:     "prefix glob exact prefix",
			input:    "AIGGTM-",
			patterns: []string{"AIGGTM-*"},
			want:     true,
		},
		{
			name:     "empty patterns",
			input:    "anything",
			patterns: []string{},
			want:     false,
		},
		{
			name:     "nil patterns",
			input:    "anything",
			patterns: nil,
			want:     false,
		},
		{
			name:     "multiple patterns first match",
			input:    "AIGGTM-999",
			patterns: []string{"foo", "AIGGTM-*", "bar"},
			want:     true,
		},
		{
			name:     "multiple patterns last match",
			input:    "bar",
			patterns: []string{"foo", "AIGGTM-*", "bar"},
			want:     true,
		},
		{
			name:     "star only matches everything",
			input:    "anything",
			patterns: []string{"*"},
			want:     true,
		},
		{
			name:     "empty name with star",
			input:    "",
			patterns: []string{"*"},
			want:     true,
		},
		{
			name:     "empty name no match",
			input:    "",
			patterns: []string{"foo"},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesExcludeList(tt.input, tt.patterns)
			if got != tt.want {
				t.Errorf("MatchesExcludeList(%q, %v) = %v, want %v",
					tt.input, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestIsAzureEndpoint(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"https://myresource.openai.azure.com/openai/v1", true},
		{"https://myresource.services.ai.azure.com/anthropic/", true},
		{"https://myresource.azure.us/foo", true},
		{"https://api.anthropic.com/", false},
		{"https://api.openai.com/v1", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			got := IsAzureEndpoint(tt.url)
			if got != tt.want {
				t.Errorf("IsAzureEndpoint(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestParseDurationOrDisable(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMs  int64 // milliseconds, -1 means check error
		wantErr bool
	}{
		{"empty returns fallback", "", 5000, false},
		{"zero disables", "0", 0, false},
		{"off disables", "off", 0, false},
		{"disable disables", "disable", 0, false},
		{"valid duration", "30s", 30000, false},
		{"valid short duration", "500ms", 500, false},
		{"invalid", "not-a-duration", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDurationOrDisable(tt.input, 5000*1e6) // 5s fallback in ns
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseDurationOrDisable(%q): error = %v, wantErr = %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got.Milliseconds() != tt.wantMs {
				t.Errorf("parseDurationOrDisable(%q) = %v, want %dms", tt.input, got, tt.wantMs)
			}
		})
	}
}

func TestLoadFromFile(t *testing.T) {
	// Create a temp directory with a config file
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".pane-patrol.yaml")
	content := `provider: openai
model: gpt-4o-mini
api_key: test-key-123
max_tokens: 8192
parallel: 5
refresh: "10s"
exclude_sessions:
  - "AIGGTM-*"
  - "private"
auto_nudge: true
auto_nudge_max_risk: medium
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// Change to temp dir so Load() finds the config
	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// Clear env vars that might interfere
	for _, key := range []string{
		"PANE_PATROL_PROVIDER", "PANE_PATROL_MODEL", "PANE_PATROL_API_KEY",
		"PANE_PATROL_BASE_URL", "PANE_PATROL_MAX_TOKENS", "PANE_PATROL_FILTER",
		"PANE_PATROL_REFRESH", "PANE_PATROL_CACHE_TTL", "PANE_PATROL_EXCLUDE_SESSIONS",
		"PANE_PATROL_AUTO_NUDGE", "PANE_PATROL_AUTO_NUDGE_MAX_RISK",
		"AZURE_OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"AZURE_RESOURCE_NAME",
	} {
		t.Setenv(key, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Provider != "openai" {
		t.Errorf("Provider: got %q, want %q", cfg.Provider, "openai")
	}
	if cfg.Model != "gpt-4o-mini" {
		t.Errorf("Model: got %q, want %q", cfg.Model, "gpt-4o-mini")
	}
	if cfg.APIKey != "test-key-123" {
		t.Errorf("APIKey: got %q, want %q", cfg.APIKey, "test-key-123")
	}
	if cfg.MaxTokens != 8192 {
		t.Errorf("MaxTokens: got %d, want %d", cfg.MaxTokens, 8192)
	}
	if cfg.Parallel != 5 {
		t.Errorf("Parallel: got %d, want %d", cfg.Parallel, 5)
	}
	if cfg.AutoNudge != true {
		t.Errorf("AutoNudge: got %v, want true", cfg.AutoNudge)
	}
	if cfg.AutoNudgeMaxRisk != "medium" {
		t.Errorf("AutoNudgeMaxRisk: got %q, want %q", cfg.AutoNudgeMaxRisk, "medium")
	}
	if len(cfg.ExcludeSessions) != 2 {
		t.Fatalf("ExcludeSessions: got %d entries, want 2", len(cfg.ExcludeSessions))
	}
	if cfg.ExcludeSessions[0] != "AIGGTM-*" {
		t.Errorf("ExcludeSessions[0]: got %q, want %q", cfg.ExcludeSessions[0], "AIGGTM-*")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	// Create a temp directory with a config file
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".pane-patrol.yaml")
	content := `provider: openai
model: gpt-4o-mini
api_key: file-key
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	origDir, _ := os.Getwd()
	defer os.Chdir(origDir)
	os.Chdir(dir)

	// Clear interfering env vars, then set the ones we want
	for _, key := range []string{
		"PANE_PATROL_BASE_URL", "PANE_PATROL_MAX_TOKENS", "PANE_PATROL_FILTER",
		"PANE_PATROL_REFRESH", "PANE_PATROL_CACHE_TTL", "PANE_PATROL_EXCLUDE_SESSIONS",
		"PANE_PATROL_AUTO_NUDGE", "PANE_PATROL_AUTO_NUDGE_MAX_RISK",
		"AZURE_OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"AZURE_RESOURCE_NAME",
	} {
		t.Setenv(key, "")
	}

	// Env should override file
	t.Setenv("PANE_PATROL_PROVIDER", "anthropic")
	t.Setenv("PANE_PATROL_MODEL", "claude-sonnet-4-5")
	t.Setenv("PANE_PATROL_API_KEY", "env-key")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Provider != "anthropic" {
		t.Errorf("Provider: got %q, want %q (env should override file)", cfg.Provider, "anthropic")
	}
	if cfg.Model != "claude-sonnet-4-5" {
		t.Errorf("Model: got %q, want %q (env should override file)", cfg.Model, "claude-sonnet-4-5")
	}
	if cfg.APIKey != "env-key" {
		t.Errorf("APIKey: got %q, want %q (env should override file)", cfg.APIKey, "env-key")
	}
}
