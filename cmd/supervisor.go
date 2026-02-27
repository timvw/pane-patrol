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
	"github.com/timvw/pane-patrol/internal/events"
	telem "github.com/timvw/pane-patrol/internal/otel"
	"github.com/timvw/pane-patrol/internal/parser"
	"github.com/timvw/pane-patrol/internal/supervisor"
)

var flagNoEmbed bool
var flagTheme string
var flagEventSocket string

var supervisorCmd = &cobra.Command{
	Use:   "supervisor",
	Short: "Interactive TUI to monitor and unblock AI coding agents",
	Long: `Launch an interactive terminal UI that continuously scans all panes,
shows which AI coding agents are blocked, and lets you unblock them
with suggested actions or free-form text input.

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
	supervisorCmd.Flags().StringVar(&flagTheme, "theme", "dark",
		"Color theme: dark, light")
	supervisorCmd.Flags().StringVar(&flagEventSocket, "event-socket", "",
		"Unix datagram socket path for hook events")
	rootCmd.AddCommand(supervisorCmd)
}

func runSupervisor(cmd *cobra.Command) error {
	// Auto-embed in tmux if not already inside one.
	// Navigation (switch-client) requires an active tmux client, so
	// we re-exec the same command inside a new tmux session.
	if !flagNoEmbed {
		autoEmbedInTmux()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel() // cancels in-flight scan goroutines when the TUI exits

	// Load configuration: defaults -> config file -> env vars.
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	if cfg.ConfigFile != "" {
		fmt.Fprintf(os.Stderr, "config: loaded %s\n", cfg.ConfigFile)
	}

	// Wire build version into OTEL service metadata
	telem.Version = Version

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

	// Generate a session ID to group all scans from this supervisor run
	sessionID := fmt.Sprintf("ps-%d-%d", os.Getpid(), time.Now().Unix())

	// Resolve own pane to skip self-evaluation.
	// Also exclude the entire session containing this pane — other panes in
	// the supervisor session (e.g., from split windows) are not useful to scan
	// and would show as a collapsed session row in the TUI.
	selfTarget := resolveSelfTarget()
	if selfTarget != "" {
		if colonIdx := strings.LastIndex(selfTarget, ":"); colonIdx > 0 {
			selfSession := selfTarget[:colonIdx]
			cfg.ExcludeSessions = append(cfg.ExcludeSessions, selfSession)
			fmt.Fprintf(os.Stderr, "self-session: %s (excluded from scans)\n", selfSession)
		}
	}

	var metrics *telem.Metrics
	if tel != nil {
		metrics = tel.Metrics
	}

	scanner := &supervisor.Scanner{
		Mux:             m,
		Parsers:         parser.NewRegistry(),
		Filter:          cfg.Filter,
		ExcludeSessions: cfg.ExcludeSessions,
		Parallel:        cfg.Parallel,
		Metrics:         metrics,
		SessionID:       sessionID,
		SelfTarget:      selfTarget,
		Cache:           supervisor.NewVerdictCache(cfg.CacheTTLDuration),
	}

	socketPath := flagEventSocket
	if socketPath == "" {
		socketPath = events.DefaultSocketPath()
	}
	eventStore := events.NewStore(3 * time.Minute)
	collector := events.NewCollector(eventStore, socketPath)
	if err := collector.Start(ctx); err != nil {
		return fmt.Errorf("hook collector: %w", err)
	}
	fmt.Fprintf(os.Stderr, "hook collector: listening on %s\n", collector.SocketPath())

	scanner.EventStore = eventStore
	scanner.EventOnly = true
	scanner.Cache = nil

	tui := &supervisor.TUI{
		Scanner:          scanner,
		RefreshInterval:  cfg.RefreshDuration,
		AutoNudge:        cfg.AutoNudge,
		AutoNudgeMaxRisk: cfg.AutoNudgeMaxRisk,
		ThemeName:        flagTheme,
	}

	return tui.Run(ctx)
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

	if sessionName != "" {
		fmt.Fprintf(os.Stderr, "not inside tmux — auto-embedding in tmux session %q\n", sessionName)
	} else {
		fmt.Fprintf(os.Stderr, "not inside tmux — auto-embedding in a new tmux session\n")
	}

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
