// Package supervisor provides the scan loop, TUI, and nudge transport
// for the pane-supervisor command.
//
// ZFC compliance: This package never interprets pane content. It displays
// LLM verdicts and executes user-confirmed actions via tmux send-keys.
// All judgment calls (blocked detection, action suggestions) come from the LLM.
package supervisor

import (
	"fmt"
	"os/exec"
	"time"
)

// NudgePane sends a keystroke sequence to a tmux pane using the Gastown-reliable
// nudge pattern: literal mode + debounce + Escape (for vim mode) + Enter with retry.
//
// For literal text (e.g., "y", "yes", "continue"), keys are sent with -l flag.
// For control sequences (e.g., "C-c", "Enter", "Escape", "Down"), keys are sent raw.
func NudgePane(paneID, keys string) error {
	if isControlSequence(keys) {
		return nudgeRaw(paneID, keys)
	}
	return nudgeLiteral(paneID, keys)
}

// nudgeLiteral sends literal text followed by Enter (Gastown-reliable pattern).
func nudgeLiteral(paneID, keys string) error {
	// 1. Send text in literal mode
	if err := tmuxSendKeys(paneID, "-l", keys); err != nil {
		return fmt.Errorf("send literal keys: %w", err)
	}

	// 2. Wait for paste to complete
	time.Sleep(500 * time.Millisecond)

	// 3. Send Escape to exit vim INSERT mode if enabled
	_ = tmuxSendKeys(paneID, "", "Escape")
	time.Sleep(100 * time.Millisecond)

	// 4. Send Enter with retry
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(200 * time.Millisecond)
		}
		if err := tmuxSendKeys(paneID, "", "Enter"); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("failed to send Enter after 3 attempts: %w", lastErr)
}

// nudgeRaw sends a control sequence (no literal mode, no Enter).
func nudgeRaw(paneID, keys string) error {
	return tmuxSendKeys(paneID, "", keys)
}

// tmuxSendKeys runs tmux send-keys with optional flags.
func tmuxSendKeys(paneID, flag, keys string) error {
	var args []string
	args = append(args, "send-keys", "-t", paneID)
	if flag != "" {
		args = append(args, flag)
	}
	args = append(args, keys)

	cmd := exec.Command("tmux", args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux send-keys failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// isControlSequence returns true if the keys string is a tmux control sequence
// rather than literal text to type.
func isControlSequence(keys string) bool {
	switch keys {
	case "Enter", "Escape", "Up", "Down", "Left", "Right",
		"Tab", "BTab", "Space", "BSpace", "DC":
		return true
	}
	// C-x patterns (Ctrl+key)
	if len(keys) == 3 && keys[0] == 'C' && keys[1] == '-' {
		return true
	}
	// M-x patterns (Meta/Alt+key)
	if len(keys) == 3 && keys[0] == 'M' && keys[1] == '-' {
		return true
	}
	return false
}
