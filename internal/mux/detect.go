package mux

import (
	"fmt"
	"os"
	"os/exec"
)

// Detect auto-detects the active terminal multiplexer.
// It checks environment variables first, then falls back to checking
// if the multiplexer binary exists and has a running server.
//
// ZFC compliance: This is infrastructure plumbing (which mux binary to call),
// not content interpretation. Acceptable per ZFC principles.
func Detect() (Multiplexer, error) {
	// Check environment variables first.
	if os.Getenv("TMUX") != "" {
		return NewTmux(), nil
	}
	if os.Getenv("ZELLIJ") != "" {
		return nil, fmt.Errorf("zellij support is not yet implemented")
	}

	// Fall back to checking for running tmux server.
	if tmuxPath, err := exec.LookPath("tmux"); err == nil && tmuxPath != "" {
		// Check if tmux server is running by listing sessions.
		cmd := exec.Command("tmux", "list-sessions")
		if err := cmd.Run(); err == nil {
			return NewTmux(), nil
		}
	}

	return nil, fmt.Errorf("no supported terminal multiplexer detected (set $TMUX or install tmux)")
}

// FromName creates a Multiplexer by name.
func FromName(name string) (Multiplexer, error) {
	switch name {
	case "tmux":
		return NewTmux(), nil
	case "zellij":
		return nil, fmt.Errorf("zellij support is not yet implemented")
	default:
		return nil, fmt.Errorf("unknown multiplexer: %q (supported: tmux)", name)
	}
}
