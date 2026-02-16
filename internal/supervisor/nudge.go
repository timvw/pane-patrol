// Package supervisor provides the scan loop, TUI, and nudge transport
// for the pane-supervisor command.
//
// This package displays verdicts (from deterministic parsers or LLM) and
// executes user-confirmed actions via tmux send-keys.
package supervisor

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// SendKeysFunc sends keys to a pane with an optional flag (e.g. "-l" for literal mode).
// The default implementation shells out to tmux send-keys.
// Tests can replace this to avoid exec.Command.
type SendKeysFunc func(paneID, flag, keys string) error

// defaultSendKeys runs tmux send-keys with optional flags.
func defaultSendKeys(paneID, flag, keys string) error {
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

// Nudger sends keystroke sequences to tmux panes using the Gastown-reliable
// nudge pattern. Inject a custom SendKeys function for testing.
type Nudger struct {
	SendKeys SendKeysFunc
	// Sleep is an injectable delay function. Defaults to time.Sleep.
	Sleep func(time.Duration)
}

// DefaultNudger returns a Nudger that shells out to tmux.
func DefaultNudger() *Nudger {
	return &Nudger{
		SendKeys: defaultSendKeys,
		Sleep:    time.Sleep,
	}
}

// NudgePane sends a keystroke sequence to a tmux pane.
//
// When raw is true, keys are sent as a single raw keypress — no Escape or
// Enter appended. Use this for TUIs in raw mode (Claude Code, OpenCode, Codex).
//
// When raw is false (default), literal text (e.g., "yes", "continue") is sent
// with -l flag using the Gastown pattern: literal → debounce → Escape → Enter.
// Control sequences (e.g., "C-c", "Enter") are always sent raw regardless.
func (n *Nudger) NudgePane(paneID, keys string, raw bool) error {
	if raw || isControlSequence(keys) {
		return n.nudgeRaw(paneID, keys)
	}
	return n.nudgeLiteral(paneID, keys)
}

// nudgeLiteral sends literal text followed by Enter (Gastown-reliable pattern).
func (n *Nudger) nudgeLiteral(paneID, keys string) error {
	sleep := n.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	sendKeys := n.SendKeys
	if sendKeys == nil {
		sendKeys = defaultSendKeys
	}

	// 1. Send text in literal mode
	if err := sendKeys(paneID, "-l", keys); err != nil {
		return fmt.Errorf("send literal keys: %w", err)
	}

	// 2. Wait for paste to complete
	sleep(500 * time.Millisecond)

	// 3. Send Escape to exit vim INSERT mode if enabled
	_ = sendKeys(paneID, "", "Escape")
	sleep(100 * time.Millisecond)

	// 4. Send Enter with retry
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			sleep(200 * time.Millisecond)
		}
		if err := sendKeys(paneID, "", "Enter"); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return fmt.Errorf("failed to send Enter after 3 attempts: %w", lastErr)
}

// nudgeRaw sends a control sequence (no literal mode, no Enter).
// Supports space-separated key sequences (e.g., "Down Enter", "Down Down Enter")
// where each token is sent as a separate raw keystroke with a small delay.
func (n *Nudger) nudgeRaw(paneID, keys string) error {
	sendKeys := n.SendKeys
	if sendKeys == nil {
		sendKeys = defaultSendKeys
	}
	sleep := n.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	// Split on spaces to support multi-key sequences like "Down Enter"
	parts := splitKeySequence(keys)
	for i, part := range parts {
		if i > 0 {
			sleep(100 * time.Millisecond)
		}
		if err := sendKeys(paneID, "", part); err != nil {
			return fmt.Errorf("send raw key %q (step %d): %w", part, i+1, err)
		}
	}
	return nil
}

// splitKeySequence splits a key string by spaces, but only if each token
// looks like a control sequence. If any token is not a control sequence,
// the entire string is treated as a single key (for backward compatibility).
func splitKeySequence(keys string) []string {
	parts := strings.Split(keys, " ")
	if len(parts) <= 1 {
		return []string{keys}
	}
	// Verify all parts are valid control sequences
	for _, part := range parts {
		if !isControlSequence(part) {
			return []string{keys}
		}
	}
	return parts
}

// NudgePane is a convenience function using the default tmux nudger.
func NudgePane(paneID, keys string, raw bool) error {
	return DefaultNudger().NudgePane(paneID, keys, raw)
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
