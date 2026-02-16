package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/config"
	"github.com/timvw/pane-patrol/internal/evaluator"
	telem "github.com/timvw/pane-patrol/internal/otel"
	"github.com/timvw/pane-patrol/internal/supervisor"
)

var flagNoEmbed bool

var supervisorCmd = &cobra.Command{
	Use:   "supervisor",
	Short: "Interactive TUI to monitor and unblock AI coding agents",
	Long: `Launch an interactive terminal UI that continuously scans all panes,
shows which AI coding agents are blocked, and lets you unblock them
with LLM-suggested actions or free-form text input.

If not already running inside tmux, the supervisor automatically
re-launches itself in a new tmux session so that navigation (click,
Enter, post-action jump) works correctly. Use --no-embed to disable
this behavior.

Configuration is loaded from .pane-patrol.yaml or environment variables.
See the README for all configuration options.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runSupervisor(cmd)
	},
}

func init() {
	supervisorCmd.Flags().BoolVar(&flagNoEmbed, "no-embed", false,
		"Do not auto-embed in a tmux session (navigation will not work outside tmux)")
	rootCmd.AddCommand(supervisorCmd)
}

func runSupervisor(cmd *cobra.Command) error {
	// Auto-embed in tmux if not already inside one.
	// Navigation (switch-client) requires an active tmux client, so
	// we re-exec the same command inside a new tmux session.
	if !flagNoEmbed {
		autoEmbedInTmux()
	}

	ctx := context.Background()

	// Load configuration: defaults -> config file -> env vars.
	// Also apply any CLI flags that were set on the root command.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	// CLI flags override config file values for shared settings.
	// Use Cobra's Changed() to detect flags explicitly set by the user,
	// so that e.g. --provider anthropic correctly overrides a config file value of openai.
	if cmd.Flags().Changed("provider") {
		cfg.Provider = flagProvider
	}
	if cmd.Flags().Changed("model") {
		cfg.Model = flagModel
	}
	if cmd.Flags().Changed("base-url") {
		cfg.BaseURL = flagBaseURL
	}
	if cmd.Flags().Changed("api-key") {
		cfg.APIKey = flagAPIKey
	}
	if cmd.Flags().Changed("max-tokens") {
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
		Cache:           supervisor.NewVerdictCache(cfg.CacheTTLDuration),
	}

	tui := &supervisor.TUI{
		Scanner:          scanner,
		RefreshInterval:  cfg.RefreshDuration,
		AutoNudge:        cfg.AutoNudge,
		AutoNudgeMaxRisk: cfg.AutoNudgeMaxRisk,
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

// autoEmbedInTmux re-launches the current process inside a tmux session
// when not already running under tmux. This ensures navigation commands
// (switch-client) have an active client. On success, the current process
// is replaced (syscall.Exec) and this function never returns. On failure,
// it prints a warning and returns so the supervisor can run with degraded
// navigation.
func autoEmbedInTmux() {
	if os.Getenv("TMUX") != "" {
		return // already inside tmux
	}

	tmuxPath, err := exec.LookPath("tmux")
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: tmux not found in PATH, navigation will not work\n")
		return
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not resolve executable path: %v\n", err)
		return
	}

	wd, err := os.Getwd()
	if err != nil {
		wd = "/"
	}

	// Pick a session name, avoiding conflicts with existing sessions.
	sessionName := "pane-patrol-supervisor"
	hasSession := exec.Command(tmuxPath, "has-session", "-t", sessionName)
	if hasSession.Run() == nil {
		// Session exists — let tmux auto-name instead
		sessionName = ""
	}

	// Build: tmux new-session [-s name] -c <wd> <exe> <args...>
	tmuxArgs := []string{"tmux", "new-session"}
	if sessionName != "" {
		tmuxArgs = append(tmuxArgs, "-s", sessionName)
	}
	tmuxArgs = append(tmuxArgs, "-c", wd, exe)
	tmuxArgs = append(tmuxArgs, os.Args[1:]...)

	fmt.Fprintf(os.Stderr, "not inside tmux — auto-embedding in tmux session")
	if sessionName != "" {
		fmt.Fprintf(os.Stderr, " %q", sessionName)
	}
	fmt.Fprintf(os.Stderr, "\n")

	// Replace this process with tmux. On success, this never returns.
	if err := syscall.Exec(tmuxPath, tmuxArgs, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not auto-embed in tmux: %v\n", err)
		fmt.Fprintf(os.Stderr, "navigation (click/Enter/post-action jump) will not work\n")
		fmt.Fprintf(os.Stderr, "use --no-embed to suppress this warning\n")
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
