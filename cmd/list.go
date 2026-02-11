package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var flagFilter string

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all pane targets",
	Long: `List all terminal multiplexer panes as targets.

Each line is a pane target that can be passed to other commands (capture, check).
Optionally filter by session name using a regex pattern.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		m, err := getMultiplexer()
		if err != nil {
			return err
		}

		panes, err := m.ListPanes(cmd.Context(), flagFilter)
		if err != nil {
			return fmt.Errorf("failed to list panes: %w", err)
		}

		for _, p := range panes {
			fmt.Println(p.Target)
		}
		return nil
	},
}

func init() {
	listCmd.Flags().StringVar(&flagFilter, "filter", "", "regex pattern to filter by session name")
	rootCmd.AddCommand(listCmd)
}
