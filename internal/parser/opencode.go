package parser

import (
	"strings"

	"github.com/timvw/pane-patrol/internal/model"
)

// OpenCodeParser recognizes the OpenCode TUI.
//
// Source reference: packages/opencode/src/cli/cmd/tui/routes/session/permission.tsx
// Permission dialog title: "△ Permission required"
// Options: "Allow once", "Allow always", "Reject"
// Reject stage: "△ Reject permission" + "Tell OpenCode what to do differently"
// Footer: "⇆ select  enter confirm"
// Active: Knight Rider scanner (■/⬝ or ⬥◆⬩⬪·), Build/Plan indicators
// Idle: input prompt visible, no active indicators
type OpenCodeParser struct{}

func (p *OpenCodeParser) Name() string { return "opencode" }

func (p *OpenCodeParser) Parse(content string, processTree []string) *Result {
	if !p.isOpenCode(content, processTree) {
		return nil
	}

	// Check idle at bottom FIRST: if the bottom of the screen shows a clear
	// idle prompt, any dialog text or active indicators above it are stale
	// (from a prior turn or the agent's own output) and should be ignored.
	if p.isIdleAtBottom(content) {
		return &Result{
			Agent:      "opencode",
			Blocked:    true,
			Reason:     "idle at prompt",
			WaitingFor: "idle at prompt",
			Actions: []model.Action{
				{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
			},
			Recommended: 0,
			Reasoning:   "deterministic parser: OpenCode TUI detected, idle prompt at bottom of screen",
		}
	}

	// Not idle — check for dialog states.
	if r := p.parsePermissionDialog(content); r != nil {
		return r
	}
	if r := p.parseRejectDialog(content); r != nil {
		return r
	}

	if p.isActiveExecution(content) {
		return &Result{
			Agent:     "opencode",
			Blocked:   false,
			Reason:    "actively executing",
			Reasoning: "deterministic parser: detected active execution indicators (spinner, Build/Plan, progress bar)",
		}
	}

	// Default: idle at prompt (fallthrough for unrecognized OpenCode state)
	return &Result{
		Agent:      "opencode",
		Blocked:    true,
		Reason:     "idle at prompt",
		WaitingFor: "idle at prompt",
		Actions: []model.Action{
			{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: OpenCode TUI detected, no active execution indicators, agent is idle",
	}
}

// isIdleAtBottom checks if the bottom of the screen shows a clear idle
// prompt. OpenCode's idle state has "> " prompt line.
//
// Returns false if active execution indicators are also present in the bottom
// lines — the input prompt may briefly coexist with active indicators during
// state transitions.
func (p *OpenCodeParser) isIdleAtBottom(content string) bool {
	lines := strings.Split(content, "\n")
	bottom := bottomNonEmpty(lines, bottomLines)
	hasPrompt := false
	for _, line := range bottom {
		trimmed := strings.TrimSpace(line)

		// Dialog indicators that override idle signals: the reject dialog
		// has a "> " text input prompt that looks like the idle prompt, but
		// coexists with "△ Reject permission" or "△ Permission required".
		if strings.Contains(trimmed, "△ Reject permission") || strings.Contains(trimmed, "△ Permission required") {
			return false
		}
		// Permission dialog footer
		if strings.Contains(trimmed, "⇆ select") && strings.Contains(trimmed, "enter confirm") {
			return false
		}

		// Active indicators that override idle signals
		if strings.Contains(trimmed, "■") && strings.Contains(trimmed, "⬝") {
			return false
		}
		if strings.Contains(trimmed, "⬥") || strings.Contains(trimmed, "⬩") || strings.Contains(trimmed, "⬪") {
			return false
		}
		if strings.Contains(trimmed, "esc interrupt") || strings.Contains(trimmed, "esc to interrupt") ||
			strings.Contains(trimmed, "esc again to interrupt") || strings.Contains(trimmed, "press esc to stop") {
			return false
		}
		for _, r := range trimmed {
			if r >= '⠋' && r <= '⠿' {
				return false
			}
		}

		// Idle signal
		if trimmed == ">" || strings.HasPrefix(trimmed, "> ") {
			hasPrompt = true
		}
	}
	return hasPrompt
}

// isOpenCode checks if this pane is running OpenCode based on the process tree
// and characteristic TUI elements.
func (p *OpenCodeParser) isOpenCode(content string, processTree []string) bool {
	for _, proc := range processTree {
		lower := strings.ToLower(proc)
		if strings.Contains(lower, "opencode") {
			return true
		}
	}
	// Fallback: look for OpenCode-specific TUI markers in content.
	// These are unique to OpenCode and won't appear in other agents.
	if strings.Contains(content, "△ Permission required") {
		return true
	}
	if strings.Contains(content, "△ Reject permission") {
		return true
	}
	// OpenCode footer pattern: "⇆ select  enter confirm"
	if strings.Contains(content, "⇆ select") {
		return true
	}
	return false
}

// parsePermissionDialog detects "△ Permission required" dialogs.
func (p *OpenCodeParser) parsePermissionDialog(content string) *Result {
	if !strings.Contains(content, "△ Permission required") {
		return nil
	}

	waitingFor := extractBlock(content, "△ Permission required")

	return &Result{
		Agent:      "opencode",
		Blocked:    true,
		Reason:     "permission dialog waiting for approval",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "Enter", Label: "allow once (confirm selected option)", Risk: "medium", Raw: true},
			{Keys: "Down Enter", Label: "allow always", Risk: "medium", Raw: true},
			{Keys: "Down Down Enter", Label: "reject", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "dismiss dialog", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: OpenCode permission dialog detected (△ Permission required)",
	}
}

// parseRejectDialog detects the "△ Reject permission" follow-up.
func (p *OpenCodeParser) parseRejectDialog(content string) *Result {
	if !strings.Contains(content, "△ Reject permission") {
		return nil
	}

	return &Result{
		Agent:      "opencode",
		Blocked:    true,
		Reason:     "reject dialog — waiting for alternative instructions",
		WaitingFor: "△ Reject permission\nTell OpenCode what to do differently",
		Actions: []model.Action{
			{Keys: "Escape", Label: "cancel rejection, return to permission dialog", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: OpenCode reject dialog detected (△ Reject permission)",
	}
}

// isActiveExecution checks for active execution indicators in the bottom
// portion of the captured content. Only the last bottomLines lines are
// scanned to avoid false positives from stale indicators in scrollback.
func (p *OpenCodeParser) isActiveExecution(content string) bool {
	lines := strings.Split(content, "\n")
	lines = bottomNonEmpty(lines, bottomLines)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Knight Rider scanner animation (blocks style)
		if strings.Contains(trimmed, "■") && strings.Contains(trimmed, "⬝") {
			return true
		}
		// Knight Rider scanner animation (diamonds style)
		if strings.Contains(trimmed, "⬥") || strings.Contains(trimmed, "⬩") || strings.Contains(trimmed, "⬪") {
			return true
		}
		// Build/Plan indicators with activity
		if (strings.Contains(trimmed, "▣ Build") || strings.Contains(trimmed, "■ Build") ||
			strings.Contains(trimmed, "▣ Plan")) && !strings.Contains(content, "△ Permission required") {
			return true
		}
		// esc interrupt in status bar
		if strings.Contains(trimmed, "esc interrupt") || strings.Contains(trimmed, "esc to interrupt") ||
			strings.Contains(trimmed, "esc again to interrupt") || strings.Contains(trimmed, "press esc to stop") {
			return true
		}
		// Braille spinner characters
		for _, r := range trimmed {
			if r >= '⠋' && r <= '⠿' {
				return true
			}
		}
		// Subagent/task with toolcalls (but not stuck — 0 toolcalls)
		if (strings.Contains(trimmed, "Task") || strings.Contains(trimmed, "task")) &&
			strings.Contains(trimmed, "toolcall") && !strings.Contains(trimmed, "(0 toolcall") {
			return true
		}
		// QUEUED label
		if strings.Contains(trimmed, "QUEUED") {
			return true
		}
	}
	return false
}

// extractBlock extracts a contextual block of text around a marker line.
// Returns the marker line plus surrounding non-empty lines for the waiting_for field.
func extractBlock(content, marker string) string {
	lines := strings.Split(content, "\n")
	markerIdx := -1
	for i, line := range lines {
		if strings.Contains(line, marker) {
			markerIdx = i
			break
		}
	}
	if markerIdx < 0 {
		return marker
	}

	// Collect from marker line through the next few non-empty lines (up to 6)
	var block []string
	for i := markerIdx; i < len(lines) && len(block) < 6; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" && len(block) > 1 {
			break // stop at first blank line after we have some content
		}
		if trimmed != "" {
			block = append(block, trimmed)
		}
	}
	return strings.Join(block, "\n")
}
