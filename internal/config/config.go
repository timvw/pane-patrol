// Package config loads pane-supervisor configuration from file and environment.
//
// Precedence (highest to lowest):
//  1. Environment variables (PANE_PATROL_*)
//  2. Config file
//  3. Built-in defaults
//
// Config file search order:
//  1. .pane-supervisor.yaml in current directory
//  2. ~/.config/pane-supervisor/config.yaml
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds all pane-supervisor configuration.
type Config struct {
	// LLM settings
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	MaxTokens int64  `yaml:"max_tokens"`

	// Scan settings
	Filter   string `yaml:"filter"`
	Parallel int    `yaml:"parallel"`

	// Refresh and cache
	Refresh  string `yaml:"refresh"`   // Go duration string, e.g. "30s"
	CacheTTL string `yaml:"cache_ttl"` // Go duration string, e.g. "5m"

	// Auto-nudge
	AutoNudge        bool   `yaml:"auto_nudge"`          // Enable automatic nudging of blocked panes
	AutoNudgeMaxRisk string `yaml:"auto_nudge_max_risk"` // Maximum risk level to auto-nudge: "low" (default), "medium", "high"

	// OTEL
	OTELEndpoint string `yaml:"otel_endpoint"`
	OTELHeaders  string `yaml:"otel_headers"` // Comma-separated key=value pairs, e.g. "Authorization=Basic abc123"

	// Parsed durations (not from YAML, set after loading)
	RefreshDuration  time.Duration `yaml:"-"`
	CacheTTLDuration time.Duration `yaml:"-"`

	// ConfigFile is the path to the config file that was loaded (empty if none).
	ConfigFile string `yaml:"-"`
}

// Defaults returns a Config with all default values.
func Defaults() *Config {
	return &Config{
		Provider:  "anthropic",
		Model:     "claude-sonnet-4-5",
		MaxTokens: 4096,
		Parallel:  10,
		Refresh:   "30s",
		CacheTTL:  "0",
	}
}

// Load reads configuration from file and environment variables.
// Environment variables always override file values.
func Load() (*Config, error) {
	cfg := Defaults()

	// Try to load config file
	if path, data, err := findConfigFile(); err == nil {
		var fileCfg Config
		if err := yaml.Unmarshal(data, &fileCfg); err != nil {
			return nil, fmt.Errorf("parsing config file %s: %w", path, err)
		}
		cfg.ConfigFile = path
		mergeFile(cfg, &fileCfg)
	}

	// Environment variables override everything
	mergeEnv(cfg)

	// Parse durations
	var err error
	cfg.RefreshDuration, err = parseDurationOrDisable(cfg.Refresh, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid refresh interval %q: %w", cfg.Refresh, err)
	}
	cfg.CacheTTLDuration, err = parseDurationOrDisable(cfg.CacheTTL, 5*time.Minute)
	if err != nil {
		return nil, fmt.Errorf("invalid cache TTL %q: %w", cfg.CacheTTL, err)
	}

	return cfg, nil
}

// findConfigFile searches for a config file and returns its path and contents.
func findConfigFile() (string, []byte, error) {
	// 1. Current directory
	if data, err := os.ReadFile(".pane-supervisor.yaml"); err == nil {
		return ".pane-supervisor.yaml", data, nil
	}

	// 2. XDG config dir / ~/.config
	if home, err := os.UserHomeDir(); err == nil {
		path := filepath.Join(home, ".config", "pane-supervisor", "config.yaml")
		if data, err := os.ReadFile(path); err == nil {
			return path, data, nil
		}
	}

	return "", nil, fmt.Errorf("no config file found")
}

// mergeFile applies non-zero file values onto cfg.
func mergeFile(cfg *Config, file *Config) {
	if file.Provider != "" {
		cfg.Provider = file.Provider
	}
	if file.Model != "" {
		cfg.Model = file.Model
	}
	if file.BaseURL != "" {
		cfg.BaseURL = file.BaseURL
	}
	if file.APIKey != "" {
		cfg.APIKey = file.APIKey
	}
	if file.MaxTokens > 0 {
		cfg.MaxTokens = file.MaxTokens
	}
	if file.Filter != "" {
		cfg.Filter = file.Filter
	}
	if file.Parallel > 0 {
		cfg.Parallel = file.Parallel
	}
	if file.Refresh != "" {
		cfg.Refresh = file.Refresh
	}
	if file.CacheTTL != "" {
		cfg.CacheTTL = file.CacheTTL
	}
	if file.AutoNudge {
		cfg.AutoNudge = file.AutoNudge
	}
	if file.AutoNudgeMaxRisk != "" {
		cfg.AutoNudgeMaxRisk = file.AutoNudgeMaxRisk
	}
	if file.OTELEndpoint != "" {
		cfg.OTELEndpoint = file.OTELEndpoint
	}
	if file.OTELHeaders != "" {
		cfg.OTELHeaders = file.OTELHeaders
	}
}

// mergeEnv applies environment variables onto cfg. Env always wins.
func mergeEnv(cfg *Config) {
	if v := os.Getenv("PANE_PATROL_PROVIDER"); v != "" {
		cfg.Provider = v
	}
	if v := os.Getenv("PANE_PATROL_MODEL"); v != "" {
		cfg.Model = v
	}
	if v := os.Getenv("PANE_PATROL_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("PANE_PATROL_API_KEY"); v != "" {
		cfg.APIKey = v
	}
	if v := os.Getenv("PANE_PATROL_FILTER"); v != "" {
		cfg.Filter = v
	}
	if v := os.Getenv("PANE_PATROL_REFRESH"); v != "" {
		cfg.Refresh = v
	}
	if v := os.Getenv("PANE_PATROL_CACHE_TTL"); v != "" {
		cfg.CacheTTL = v
	}
	if v := os.Getenv("PANE_PATROL_AUTO_NUDGE"); v == "true" || v == "1" {
		cfg.AutoNudge = true
	}
	if v := os.Getenv("PANE_PATROL_AUTO_NUDGE_MAX_RISK"); v != "" {
		cfg.AutoNudgeMaxRisk = v
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); v != "" {
		cfg.OTELEndpoint = v
	}
	if v := os.Getenv("OTEL_EXPORTER_OTLP_HEADERS"); v != "" {
		cfg.OTELHeaders = v
	}

	// API key fallbacks
	if cfg.APIKey == "" {
		if v := os.Getenv("AZURE_OPENAI_API_KEY"); v != "" {
			cfg.APIKey = v
		}
	}
	if cfg.APIKey == "" {
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			cfg.APIKey = v
		}
	}
	if cfg.APIKey == "" {
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			cfg.APIKey = v
		}
	}

	// Azure base URL fallback
	if cfg.BaseURL == "" {
		if rn := os.Getenv("AZURE_RESOURCE_NAME"); rn != "" {
			switch cfg.Provider {
			case "anthropic":
				cfg.BaseURL = fmt.Sprintf("https://%s.services.ai.azure.com/anthropic/", rn)
			case "openai":
				cfg.BaseURL = fmt.Sprintf("https://%s.openai.azure.com/openai/v1", rn)
			}
		}
	}
}

// parseDurationOrDisable parses a duration string. "0", "off", "disable" return 0.
// Empty string returns the fallback value.
func parseDurationOrDisable(s string, fallback time.Duration) (time.Duration, error) {
	if s == "" {
		return fallback, nil
	}
	if s == "0" || s == "off" || s == "disable" {
		return 0, nil
	}
	return time.ParseDuration(s)
}

// IsAzureEndpoint returns true if the URL is an Azure endpoint.
func IsAzureEndpoint(url string) bool {
	return len(url) > 0 && (contains(url, ".azure.com") || contains(url, ".azure.us"))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
