package parser

import (
	"strings"

	"github.com/timvw/pane-patrol/internal/model"
)

// ClaudeCodeParser recognizes the Claude Code TUI.
//
// Source reference: binary analysis of /opt/homebrew/Caskroom/claude-code/2.1.42/claude
// Built with Bun, uses Ink (React terminal framework) in raw mode.
//
// Permission dialog: "Claude needs your permission to use {toolName}"
// Bash approval: "Do you want to proceed?" with numbered options
// Edit approval: "Do you want to make this edit to {filename}?"
// Footer: "Esc to cancel · Tab to amend"
// Active: tool-specific progress messages
// Auto-resolve: "Auto-selecting in {N}s…"
//
// Input handling: Permission dialogs use the Select component (UA) which
// renders numbered options (1. Yes, 2. Yes and don't ask again, 3. No).
// The Select component handles input via:
//
//   - Number keys (1/2/3): direct option selection via raw useInput handler
//   - Enter: select:accept (confirms focused option)
//   - Escape: select:cancel
//   - Up/Down, j/k: navigation
//
// IMPORTANT: The "y" and "n" keys are bound to confirm:yes/confirm:no in the
// Confirmation keybinding context, but the permission dialog components do NOT
// register handlers for these actions. The keystrokes are consumed by the
// keybinding resolver and silently dropped. Use numeric keys instead.
type ClaudeCodeParser struct{}

func (p *ClaudeCodeParser) Name() string { return "claude_code" }

func (p *ClaudeCodeParser) Parse(content string, processTree []string) *Result {
	if !p.isClaudeCode(content, processTree) {
		return nil
	}

	if r := p.parsePermissionDialog(content); r != nil {
		return r
	}
	if r := p.parseEditApproval(content); r != nil {
		return r
	}
	if r := p.parseAutoResolve(content); r != nil {
		return r
	}
	if p.isActiveExecution(content) {
		return &Result{
			Agent:     "claude_code",
			Blocked:   false,
			Reason:    "actively executing",
			Reasoning: "deterministic parser: detected active tool execution indicators",
		}
	}

	// Default: idle at prompt
	return &Result{
		Agent:      "claude_code",
		Blocked:    true,
		Reason:     "idle at prompt",
		WaitingFor: "idle at prompt",
		Actions: []model.Action{
			{Keys: "Enter", Label: "send empty message / continue", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Claude Code TUI detected, no active execution indicators, agent is idle",
	}
}

func (p *ClaudeCodeParser) isClaudeCode(content string, processTree []string) bool {
	for _, proc := range processTree {
		lower := strings.ToLower(proc)
		// Match "claude" process but not "claude-code-supervisor" etc.
		if strings.Contains(lower, "claude") && !strings.Contains(lower, "pane-patrol") &&
			!strings.Contains(lower, "pane-supervisor") {
			return true
		}
	}
	// Fallback: look for Claude Code-specific TUI markers
	if strings.Contains(content, "Claude needs your permission") {
		return true
	}
	if strings.Contains(content, "Esc to cancel") && strings.Contains(content, "Tab to amend") {
		return true
	}
	if strings.Contains(content, "Do you want to proceed?") && p.hasNumberedOptions(content) {
		return true
	}
	// "? for shortcuts" is the persistent footer in Claude Code's TUI
	if strings.Contains(content, "? for shortcuts") {
		return true
	}
	// "✻" is Claude Code's unique thinking/working indicator
	if strings.Contains(content, "✻") {
		return true
	}
	return false
}

// parsePermissionDialog detects "Claude needs your permission to use" or
// "Do you want to proceed?" with numbered Yes/No options.
func (p *ClaudeCodeParser) parsePermissionDialog(content string) *Result {
	hasPermission := strings.Contains(content, "Claude needs your permission")
	hasProceed := strings.Contains(content, "Do you want to proceed?")

	if !hasPermission && !hasProceed {
		return nil
	}

	waitingFor := p.extractPermissionSummary(content, hasPermission)

	// Determine if "don't ask again" option is available
	hasDontAsk := strings.Contains(content, "don't ask again") ||
		strings.Contains(content, "Yes, and don")

	// Actions use numeric keys for the Select component's direct selection.
	// The dialog shows: 1. Yes, 2. Yes and don't ask again, 3. No
	actions := []model.Action{
		{Keys: "1", Label: "approve (yes)", Risk: "medium", Raw: true},
	}
	if hasDontAsk {
		actions = append(actions, model.Action{
			Keys: "2", Label: "approve and don't ask again", Risk: "medium", Raw: true,
		})
		actions = append(actions, model.Action{
			Keys: "3", Label: "deny (no)", Risk: "low", Raw: true,
		})
	} else {
		actions = append(actions, model.Action{
			Keys: "Escape", Label: "deny (cancel)", Risk: "low", Raw: true,
		})
	}

	return &Result{
		Agent:       "claude_code",
		Blocked:     true,
		Reason:      "permission dialog waiting for approval",
		WaitingFor:  waitingFor,
		Actions:     actions,
		Recommended: 0,
		Reasoning:   "deterministic parser: Claude Code permission dialog detected",
	}
}

// parseEditApproval detects "Do you want to make this edit to {filename}?"
func (p *ClaudeCodeParser) parseEditApproval(content string) *Result {
	if !strings.Contains(content, "Do you want to make this edit to") {
		return nil
	}

	waitingFor := p.extractEditSummary(content)

	// Edit approval also uses the Select component with numbered options.
	// Typically: 1. Yes, 2. No (or similar)
	return &Result{
		Agent:      "claude_code",
		Blocked:    true,
		Reason:     "edit approval dialog",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "1", Label: "approve edit", Risk: "medium", Raw: true},
			{Keys: "Escape", Label: "reject edit", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Claude Code edit approval dialog detected",
	}
}

// parseAutoResolve detects "Auto-selecting in {N}s…" — the agent will
// auto-resolve soon, so it's technically not blocked.
func (p *ClaudeCodeParser) parseAutoResolve(content string) *Result {
	if !strings.Contains(content, "Auto-selecting in") {
		return nil
	}

	return &Result{
		Agent:     "claude_code",
		Blocked:   false,
		Reason:    "auto-resolving permission dialog",
		Reasoning: "deterministic parser: auto-select countdown detected, will resolve without intervention",
	}
}

// isActiveExecution checks for tool execution indicators.
//
// Claude Code uses "✻" as its thinking/working indicator. The pattern is:
//
//	Active:    "✻ Scampering… (2m 22s · ↓ 2.8k tokens)" — verb + ellipsis
//	Completed: "✻ Worked for 3m 10s" — past tense, no ellipsis
//
// The verbs are randomized (Scampering, Pondering, Reasoning, Thinking, etc.)
// so we match on the "✻" prefix + ellipsis rather than specific verbs.
func (p *ClaudeCodeParser) isActiveExecution(content string) bool {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// "✻" thinking/working indicator with ellipsis = active
		// "✻ Worked for" = completed (not active)
		if strings.HasPrefix(trimmed, "✻") && !strings.HasPrefix(trimmed, "✻ Worked") {
			if strings.Contains(trimmed, "…") || strings.Contains(trimmed, "...") {
				return true
			}
		}

		// Tool-specific progress messages (without ✻ prefix)
		if strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...") {
			if strings.Contains(trimmed, "Fetching") || strings.Contains(trimmed, "Reading") ||
				strings.Contains(trimmed, "Writing") || strings.Contains(trimmed, "Searching") ||
				strings.Contains(trimmed, "Running") || strings.Contains(trimmed, "Executing") {
				return true
			}
		}
		// Braille spinner
		for _, r := range trimmed {
			if r >= '⠋' && r <= '⠿' {
				return true
			}
		}
		// Active tool use with streaming output
		if strings.Contains(trimmed, "Searching:") || strings.Contains(trimmed, "Fetching") {
			return true
		}
	}
	return false
}

// extractPermissionSummary produces a structured WaitingFor like:
//
//	"Bash — $ git -C /path log --oneline"
//	"Read — /etc/hosts"
//	"Write — src/main.go"
//
// When "Claude needs your permission to use {tool}" is visible, extracts
// the tool name and any detail lines (commands, file paths) between the
// permission header and "Do you want to proceed?". When the header has
// scrolled off, falls back to context lines above "Do you want to proceed?".
func (p *ClaudeCodeParser) extractPermissionSummary(content string, hasPermission bool) string {
	lines := strings.Split(content, "\n")

	// Try to extract tool name from "Claude needs your permission to use {tool}"
	var toolName string
	var permIdx int = -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "Claude needs your permission to use") {
			permIdx = i
			// Extract tool name after "to use "
			if idx := strings.Index(trimmed, "to use "); idx >= 0 {
				toolName = strings.TrimSpace(trimmed[idx+7:])
			}
			break
		}
	}

	// Find "Do you want to proceed?" to know where the dialog options start
	var proceedIdx int = -1
	for i, line := range lines {
		if strings.Contains(line, "Do you want to proceed?") {
			proceedIdx = i
			break
		}
	}

	// Collect detail lines between the permission header and proceed prompt.
	// These contain commands ($ git ...), file paths, descriptions, etc.
	var details []string
	startIdx := permIdx + 1
	if permIdx < 0 {
		// Header scrolled off — look backwards from "Do you want to proceed?"
		if proceedIdx > 0 {
			for i := proceedIdx - 1; i >= 0 && len(details) < 6; i-- {
				trimmed := strings.TrimSpace(lines[i])
				if trimmed == "" && len(details) > 0 {
					break
				}
				if trimmed != "" {
					details = append(details, trimmed)
				}
			}
			// Reverse (collected bottom-up)
			for i, j := 0, len(details)-1; i < j; i, j = i+1, j-1 {
				details[i], details[j] = details[j], details[i]
			}
		}
	} else {
		endIdx := proceedIdx
		if endIdx < 0 {
			endIdx = len(lines)
		}
		for i := startIdx; i < endIdx && len(details) < 6; i++ {
			trimmed := strings.TrimSpace(lines[i])
			if trimmed != "" {
				details = append(details, trimmed)
			}
		}
	}

	// Build summary: "Tool — detail" or just the detail lines
	detail := strings.Join(details, "\n")
	if toolName != "" && detail != "" {
		return toolName + " — " + detail
	}
	if toolName != "" {
		return toolName
	}
	if detail != "" {
		return detail
	}
	return "permission dialog"
}

// extractEditSummary produces a WaitingFor like:
//
//	"Edit — src/main.go"
//
// from "Do you want to make this edit to {filename}?"
func (p *ClaudeCodeParser) extractEditSummary(content string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if idx := strings.Index(trimmed, "Do you want to make this edit to "); idx >= 0 {
			// Extract filename after "to " and before "?"
			rest := trimmed[idx+len("Do you want to make this edit to "):]
			rest = strings.TrimSuffix(rest, "?")
			rest = strings.TrimSpace(rest)
			if rest != "" {
				return "Edit — " + rest
			}
		}
	}
	return extractBlock(content, "Do you want to make this edit to")
}

func (p *ClaudeCodeParser) hasNumberedOptions(content string) bool {
	return strings.Contains(content, "1.") && strings.Contains(content, "2.") &&
		(strings.Contains(content, "Yes") || strings.Contains(content, "No"))
}
