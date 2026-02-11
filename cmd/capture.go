package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var captureCmd = &cobra.Command{
	Use:   "capture <target>",
	Short: "Capture the visible content of a pane",
	Long: `Capture the visible content of a terminal multiplexer pane and print it to stdout.

The target format depends on the multiplexer:
  tmux:   session:window.pane  (e.g., "mysession:0.0")
  zellij: (not yet supported)

This is pure transport — no interpretation of the content.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		target := args[0]

		m, err := getMultiplexer()
		if err != nil {
			return err
		}

		content, err := m.CapturePane(cmd.Context(), target)
		if err != nil {
			return fmt.Errorf("failed to capture pane %q: %w", target, err)
		}

		fmt.Fprint(os.Stdout, content)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(captureCmd)
}
