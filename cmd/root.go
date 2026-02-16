package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/timvw/pane-patrol/internal/mux"
)

var (
	// Global flags.
	flagMux     string
	flagVerbose bool
)

var rootCmd = &cobra.Command{
	Use:   "pane-patrol",
	Short: "Terminal pane monitor for AI coding agents",
	Long: `pane-patrol monitors terminal multiplexer panes for blocked AI coding agents.

It uses deterministic parsers for known agents (OpenCode, Claude Code, Codex).
When an agent is blocked (permission dialogs, confirmation prompts, idle at
prompt), pane-patrol suggests and can auto-execute unblocking actions.`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagMux, "mux", envOrDefault("PANE_PATROL_MUX", ""), "terminal multiplexer: tmux, zellij (default: auto-detect)")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "include raw pane content in output")
}

// getMultiplexer returns the configured or auto-detected multiplexer.
func getMultiplexer() (mux.Multiplexer, error) {
	if flagMux != "" {
		return mux.FromName(flagMux)
	}
	return mux.Detect()
}

func envOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}
