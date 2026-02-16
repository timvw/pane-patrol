package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/config"
	"github.com/timvw/pane-patrol/internal/evaluator"
	"github.com/timvw/pane-patrol/internal/mux"
)

var (
	// Global flags.
	flagMux       string
	flagProvider  string
	flagModel     string
	flagBaseURL   string
	flagAPIKey    string
	flagMaxTokens int64
	flagVerbose   bool
)

var rootCmd = &cobra.Command{
	Use:   "pane-patrol",
	Short: "Terminal pane monitor for AI coding agents",
	Long: `pane-patrol monitors terminal multiplexer panes for blocked AI coding agents.

It uses deterministic parsers for known agents (OpenCode, Claude Code, Codex)
and falls back to LLM evaluation for unknown agents. When an agent is blocked
(permission dialogs, confirmation prompts, idle at prompt), pane-patrol
suggests and can auto-execute unblocking actions.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagMux, "mux", envOrDefault("PANE_PATROL_MUX", ""), "terminal multiplexer: tmux, zellij (default: auto-detect)")
	rootCmd.PersistentFlags().StringVar(&flagProvider, "provider", envOrDefault("PANE_PATROL_PROVIDER", "anthropic"), "LLM provider: anthropic, openai")
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", envOrDefault("PANE_PATROL_MODEL", ""), "LLM model name (default: claude-sonnet-4-5 for anthropic, gpt-4o-mini for openai)")
	rootCmd.PersistentFlags().StringVar(&flagBaseURL, "base-url", envOrDefault("PANE_PATROL_BASE_URL", ""), "override LLM API base URL")
	rootCmd.PersistentFlags().StringVar(&flagAPIKey, "api-key", envOrDefault("PANE_PATROL_API_KEY", ""), "override LLM API key")
	rootCmd.PersistentFlags().Int64Var(&flagMaxTokens, "max-tokens", 0, "max completion tokens (default: 4096; increase for reasoning models)")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "include raw pane content in output")
}

// getMultiplexer returns the configured or auto-detected multiplexer.
func getMultiplexer() (mux.Multiplexer, error) {
	if flagMux != "" {
		return mux.FromName(flagMux)
	}
	return mux.Detect()
}

// getEvaluator builds a Config from CLI flags/env and delegates to
// newEvaluatorFromConfig, which is the single factory shared with the
// supervisor command.
func getEvaluator() (evaluator.Evaluator, error) {
	cfg := &config.Config{
		Provider:  flagProvider,
		Model:     flagModel,
		BaseURL:   flagBaseURL,
		APIKey:    flagAPIKey,
		MaxTokens: flagMaxTokens,
	}

	// Apply the same env-var resolution the supervisor uses.
	config.ResolveEnvDefaults(cfg)

	return newEvaluatorFromConfig(cfg)
}

// newEvaluatorFromConfig is the single evaluator factory used by both
// the check/scan commands (via getEvaluator) and the supervisor command.
func newEvaluatorFromConfig(cfg *config.Config) (evaluator.Evaluator, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key found. Set api_key in config file, or PANE_PATROL_API_KEY / AZURE_OPENAI_API_KEY / ANTHROPIC_API_KEY / OPENAI_API_KEY env var")
	}

	extraHeaders := map[string]string{}
	if config.IsAzureEndpoint(cfg.BaseURL) {
		extraHeaders["api-key"] = cfg.APIKey
	}

	switch cfg.Provider {
	case "anthropic":
		return evaluator.NewAnthropicEvaluator(evaluator.AnthropicConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ExtraHeaders: extraHeaders,
		}), nil

	case "openai":
		return evaluator.NewOpenAIEvaluator(evaluator.OpenAIConfig{
			BaseURL:      cfg.BaseURL,
			APIKey:       cfg.APIKey,
			Model:        cfg.Model,
			MaxTokens:    cfg.MaxTokens,
			ExtraHeaders: extraHeaders,
		}), nil

	default:
		return nil, fmt.Errorf("unknown provider %q (supported: anthropic, openai)", cfg.Provider)
	}
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
