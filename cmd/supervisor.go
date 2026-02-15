package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/config"
	"github.com/timvw/pane-patrol/internal/evaluator"
	telem "github.com/timvw/pane-patrol/internal/otel"
	"github.com/timvw/pane-patrol/internal/supervisor"
)

var supervisorCmd = &cobra.Command{
	Use:   "supervisor",
	Short: "Interactive TUI to monitor and unblock AI coding agents",
	Long: `Launch an interactive terminal UI that continuously scans all panes,
shows which AI coding agents are blocked, and lets you unblock them
with LLM-suggested actions or free-form text input.

Configuration is loaded from .pane-supervisor.yaml or environment variables.
See the README for all configuration options.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSupervisor()
	},
}

func init() {
	rootCmd.AddCommand(supervisorCmd)
}

func runSupervisor() error {
	ctx := context.Background()

	// Load configuration: defaults -> config file -> env vars.
	// Also apply any CLI flags that were set on the root command.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// CLI flags override config file values for shared settings
	if flagProvider != "" && flagProvider != "anthropic" {
		cfg.Provider = flagProvider
	}
	if flagModel != "" {
		cfg.Model = flagModel
	}
	if flagBaseURL != "" {
		cfg.BaseURL = flagBaseURL
	}
	if flagAPIKey != "" {
		cfg.APIKey = flagAPIKey
	}
	if flagMaxTokens > 0 {
		cfg.MaxTokens = flagMaxTokens
	}

	if cfg.ConfigFile != "" {
		fmt.Fprintf(os.Stderr, "config: loaded %s\n", cfg.ConfigFile)
	}

	// Initialize OTEL (no-op if no endpoint configured)
	tel, err := telem.Init(ctx, telem.OTELConfig{
		Endpoint: cfg.OTELEndpoint,
		Headers:  cfg.OTELHeaders,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: otel init failed: %v\n", err)
	}
	if tel != nil {
		defer tel.Shutdown(ctx)
	}

	// Auto-detect multiplexer
	m, err := getMultiplexer()
	if err != nil {
		return fmt.Errorf("no supported terminal multiplexer found: %w", err)
	}

	// Create evaluator from config
	eval, err := newSupervisorEvaluator(cfg)
	if err != nil {
		return err
	}

	// Generate a session ID to group all scans from this supervisor run
	sessionID := fmt.Sprintf("ps-%d-%d", os.Getpid(), time.Now().Unix())

	// Resolve own pane to skip self-evaluation
	selfTarget := resolveSelfTarget()

	scanner := &supervisor.Scanner{
		Mux:             m,
		Evaluator:       eval,
		Filter:          cfg.Filter,
		ExcludeSessions: cfg.ExcludeSessions,
		Parallel:        cfg.Parallel,
		SessionID:       sessionID,
		SelfTarget:      selfTarget,
		// Cache disabled for now to ensure fresh LLM evaluations each scan
		// Cache: supervisor.NewVerdictCache(cfg.CacheTTLDuration),
	}

	tui := &supervisor.TUI{
		Scanner:         scanner,
		RefreshInterval: cfg.RefreshDuration,
	}

	return tui.Run(ctx)
}

// newSupervisorEvaluator creates an LLM evaluator from the loaded config.
func newSupervisorEvaluator(cfg *config.Config) (evaluator.Evaluator, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("no API key found. Set api_key in config file, or PANE_PATROL_API_KEY / AZURE_OPENAI_API_KEY / ANTHROPIC_API_KEY env var")
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

// resolveSelfTarget returns the tmux target (session:window.pane) for the pane
// running this process. Uses TMUX_PANE env var and tmux display-message.
// Returns empty string if not running inside tmux or resolution fails.
func resolveSelfTarget() string {
	paneID := os.Getenv("TMUX_PANE")
	if paneID == "" {
		return ""
	}
	cmd := exec.Command("tmux", "display-message", "-t", paneID,
		"-p", "#{session_name}:#{window_index}.#{pane_index}")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	target := strings.TrimSpace(string(out))
	if target != "" {
		fmt.Fprintf(os.Stderr, "self-target: %s (excluded from scans)\n", target)
	}
	return target
}
