package parser

import (
	"fmt"
	"strings"

	"github.com/timvw/pane-patrol/internal/model"
)

// CodexParser recognizes the OpenAI Codex CLI TUI.
//
// Source reference: codex-rs/tui/src/bottom_pane/approval_overlay.rs
// Built with Rust (ratatui + crossterm).
//
// Approval dialogs:
//
//	Exec: "Would you like to run the following command?"
//	  Options: "Yes, proceed" / "Yes, and don't ask again for commands that start with `{prefix}`" / "No, and tell Codex what to do differently"
//	Edit: "Would you like to make the following edits?"
//	  Options: "Yes, proceed" / "Yes, and don't ask again for these files" / "No, and tell Codex what to do differently"
//	Network: "Do you want to approve access to \"{host}\"?"
//	  Options: "Yes, just this once" / "Yes, and allow this host for this session" / "No, and tell Codex what to do differently"
//	MCP: "{server_name} needs your approval."
//	User input: "Yes, provide the requested info" / "No, but continue without it" / "Cancel this request"
//
// Source reference: codex-rs/tui/src/bottom_pane/request_user_input/mod.rs
// Question dialog (RequestUserInputOverlay): agent asks user questions with numbered options.
//   - Options rendered as "› 1. Label" (selected) or "  1. Label" (unselected)
//   - "None of the above" option when is_other is true
//   - Footer tips: "enter to submit answer" (single), "enter to submit all" (multi-question)
//   - "esc to interrupt", "tab to add notes", "←/→ to navigate questions"
//
// Active state: "Working" header with elapsed time, "({elapsed} · {key} to interrupt)"
// Footer: "Plan mode" / "Pair Programming mode" / "Execute mode"
// Post-approval: "✔ You approved codex to run"
// Command display: "$ " prefix, "Reason: " prefix
type CodexParser struct{}

func (p *CodexParser) Name() string { return "codex" }

func (p *CodexParser) Parse(content string, processTree []string) *Result {
	if !p.isCodex(content, processTree) {
		return nil
	}

	// Check idle at bottom FIRST: if the bottom of the screen shows a clear
	// idle prompt, any dialog text or active indicators above it are stale
	// (from a prior turn or the agent's own output) and should be ignored.
	if p.isIdleAtBottom(content) {
		return &Result{
			Agent:      "codex",
			Blocked:    true,
			Reason:     "idle at prompt",
			WaitingFor: "idle at prompt",
			Actions: []model.Action{
				{Keys: "Enter", Label: "submit / continue", Risk: "low", Raw: true},
			},
			Recommended: 0,
			Reasoning:   "deterministic parser: Codex TUI detected, idle prompt at bottom of screen",
		}
	}

	// Not idle — check for dialog states.
	if r := p.parseExecApproval(content); r != nil {
		return r
	}
	if r := p.parseEditApproval(content); r != nil {
		return r
	}
	if r := p.parseNetworkApproval(content); r != nil {
		return r
	}
	if r := p.parseMCPApproval(content); r != nil {
		return r
	}
	if r := p.parseQuestionDialog(content); r != nil {
		return r
	}
	if r := p.parseUserInputRequest(content); r != nil {
		return r
	}

	if p.isActiveExecution(content) {
		return &Result{
			Agent:     "codex",
			Blocked:   false,
			Reason:    "actively working",
			Reasoning: "deterministic parser: detected Codex working/execution indicators",
		}
	}

	// Default: idle at prompt (fallthrough for unrecognized Codex state)
	return &Result{
		Agent:      "codex",
		Blocked:    true,
		Reason:     "idle at prompt",
		WaitingFor: "idle at prompt",
		Actions: []model.Action{
			{Keys: "Enter", Label: "submit / continue", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex TUI detected, no active execution indicators, agent is idle",
	}
}

// isIdleAtBottom checks if the bottom of the screen shows a clear idle
// prompt. Codex's idle state has ">" prompt and/or "Plan mode  shift+tab to cycle".
//
// Returns false if active execution indicators are also present in the bottom
// lines — the "Plan mode" footer is persistent and can coexist with "Working"
// during active execution.
func (p *CodexParser) isIdleAtBottom(content string) bool {
	lines := strings.Split(content, "\n")
	bottom := bottomNonEmpty(lines, bottomLines)
	hasIdle := false
	for _, line := range bottom {
		trimmed := strings.TrimSpace(line)

		// Question dialog footer indicators (RequestUserInputOverlay)
		if strings.Contains(trimmed, "enter to submit answer") ||
			strings.Contains(trimmed, "enter to submit all") {
			return false
		}

		// Active indicators that override idle signals
		if trimmed == "Working" || strings.HasPrefix(trimmed, "Working") {
			return false
		}
		if strings.Contains(trimmed, "to interrupt)") {
			return false
		}
		if strings.HasPrefix(trimmed, "└ ") || strings.HasPrefix(trimmed, "└") {
			return false
		}
		if strings.Contains(trimmed, "✔ You approved codex to run") {
			return false
		}

		// Idle signals
		if trimmed == ">" || strings.HasPrefix(trimmed, "> ") {
			hasIdle = true
		}
		if strings.Contains(trimmed, "Plan mode") && strings.Contains(trimmed, "shift+tab") {
			hasIdle = true
		}
	}
	return hasIdle
}

func (p *CodexParser) isCodex(content string, processTree []string) bool {
	for _, proc := range processTree {
		lower := strings.ToLower(proc)
		if strings.Contains(lower, "codex") {
			return true
		}
	}
	// Fallback: look for Codex-specific TUI markers
	if strings.Contains(content, "Would you like to run the following command?") {
		return true
	}
	if strings.Contains(content, "Would you like to make the following edits?") {
		return true
	}
	if strings.Contains(content, "approved codex to run") {
		return true
	}
	// Mode indicators unique to Codex
	if (strings.Contains(content, "Plan mode") || strings.Contains(content, "Pair Programming mode") ||
		strings.Contains(content, "Execute mode")) && strings.Contains(content, "shift+tab to cycle") {
		return true
	}
	return false
}

// parseExecApproval detects "Would you like to run the following command?"
func (p *CodexParser) parseExecApproval(content string) *Result {
	if !strings.Contains(content, "Would you like to run the following command?") {
		return nil
	}

	waitingFor := extractBlock(content, "Would you like to run the following command?")

	return &Result{
		Agent:      "codex",
		Blocked:    true,
		Reason:     "command approval dialog",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "Enter", Label: "yes, proceed (approve command)", Risk: "medium", Raw: true},
			{Keys: "Down Enter", Label: "yes, and don't ask again for this prefix", Risk: "medium", Raw: true},
			{Keys: "Down Down Enter", Label: "no, tell Codex what to do differently", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "cancel", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex command approval dialog detected",
	}
}

// parseEditApproval detects "Would you like to make the following edits?"
func (p *CodexParser) parseEditApproval(content string) *Result {
	if !strings.Contains(content, "Would you like to make the following edits?") {
		return nil
	}

	waitingFor := extractBlock(content, "Would you like to make the following edits?")

	return &Result{
		Agent:      "codex",
		Blocked:    true,
		Reason:     "edit approval dialog",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "Enter", Label: "yes, proceed (approve edits)", Risk: "medium", Raw: true},
			{Keys: "Down Enter", Label: "yes, and don't ask again for these files", Risk: "medium", Raw: true},
			{Keys: "Down Down Enter", Label: "no, tell Codex what to do differently", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "cancel", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex edit approval dialog detected",
	}
}

// parseNetworkApproval detects 'Do you want to approve access to "{host}"?'
func (p *CodexParser) parseNetworkApproval(content string) *Result {
	if !strings.Contains(content, "Do you want to approve access to") {
		return nil
	}

	waitingFor := extractBlock(content, "Do you want to approve access to")

	return &Result{
		Agent:      "codex",
		Blocked:    true,
		Reason:     "network access approval dialog",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "Enter", Label: "yes, just this once", Risk: "medium", Raw: true},
			{Keys: "Down Enter", Label: "yes, allow this host for session", Risk: "medium", Raw: true},
			{Keys: "Down Down Enter", Label: "no, tell Codex what to do differently", Risk: "low", Raw: true},
			{Keys: "Escape", Label: "cancel", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex network approval dialog detected",
	}
}

// parseMCPApproval detects "{server_name} needs your approval."
func (p *CodexParser) parseMCPApproval(content string) *Result {
	if !strings.Contains(content, "needs your approval") {
		return nil
	}

	waitingFor := extractBlock(content, "needs your approval")

	return &Result{
		Agent:      "codex",
		Blocked:    true,
		Reason:     "MCP server approval dialog",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "Enter", Label: "approve", Risk: "medium", Raw: true},
			{Keys: "Escape", Label: "cancel", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex MCP server approval dialog detected",
	}
}

// parseQuestionDialog detects the Codex RequestUserInputOverlay question dialog.
//
// Source: codex-rs/tui/src/bottom_pane/request_user_input/mod.rs
// The overlay renders a question with numbered options ("› 1. Label" or "  1. Label"),
// footer tips like "enter to submit answer" or "enter to submit all", and optionally
// "None of the above" for is_other questions.
func (p *CodexParser) parseQuestionDialog(content string) *Result {
	lines := strings.Split(content, "\n")
	bottom := bottomNonEmpty(lines, bottomLines)

	// Detection: "enter to submit answer" or "enter to submit all" in footer.
	// These are unique to the RequestUserInputOverlay and don't appear in
	// approval dialogs (which use "Press Enter to confirm or Esc to cancel").
	hasSubmitFooter := false
	for _, line := range bottom {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "enter to submit answer") ||
			strings.Contains(trimmed, "enter to submit all") {
			hasSubmitFooter = true
			break
		}
	}
	if !hasSubmitFooter {
		return nil
	}

	// Extract question text and options for WaitingFor.
	waitingFor := extractQuestionSummary(lines)

	// Count numbered options. Codex renders them as "› 1. Label" (selected)
	// or "  1. Label" (unselected). countNumberedOptions strips known
	// border/cursor prefixes (┃, ›) via stripDialogPrefix.
	optionCount := countNumberedOptions(lines)

	optionLabels := extractOptionLabels(lines)

	actions := make([]model.Action, 0, optionCount+2)
	for i := 1; i <= optionCount && i <= 9; i++ {
		label := fmt.Sprintf("select option %d", i)
		if i-1 < len(optionLabels) && optionLabels[i-1] != "" {
			label = optionLabels[i-1]
		}
		actions = append(actions, model.Action{
			Keys:  fmt.Sprintf("%d", i),
			Label: label,
			Risk:  "low",
			Raw:   true,
		})
	}
	actions = append(actions, model.Action{
		Keys:  "Enter",
		Label: "submit answer",
		Risk:  "low",
		Raw:   true,
	})
	actions = append(actions, model.Action{
		Keys:  "Escape",
		Label: "interrupt / dismiss",
		Risk:  "low",
		Raw:   true,
	})

	return &Result{
		Agent:       "codex",
		Blocked:     true,
		Reason:      "question dialog waiting for answer",
		WaitingFor:  waitingFor,
		Actions:     actions,
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex question dialog detected (enter to submit footer)",
	}
}

// parseUserInputRequest detects user input request dialogs.
func (p *CodexParser) parseUserInputRequest(content string) *Result {
	if !strings.Contains(content, "Yes, provide the requested info") {
		return nil
	}

	waitingFor := extractBlock(content, "Yes, provide the requested info")

	return &Result{
		Agent:      "codex",
		Blocked:    true,
		Reason:     "requesting user input",
		WaitingFor: waitingFor,
		Actions: []model.Action{
			{Keys: "Enter", Label: "yes, provide the requested info", Risk: "low", Raw: true},
			{Keys: "Down Enter", Label: "no, continue without it", Risk: "low", Raw: true},
			{Keys: "Down Down Enter", Label: "cancel this request", Risk: "low", Raw: true},
		},
		Recommended: 0,
		Reasoning:   "deterministic parser: Codex user input request dialog detected",
	}
}

// isActiveExecution checks for Codex working/execution indicators in the
// bottom portion of the captured content. Only the last bottomLines lines
// are scanned to avoid false positives from stale indicators in scrollback.
func (p *CodexParser) isActiveExecution(content string) bool {
	lines := strings.Split(content, "\n")
	lines = bottomNonEmpty(lines, bottomLines)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// "Working" header (status indicator)
		if trimmed == "Working" || strings.HasPrefix(trimmed, "Working") {
			return true
		}
		// Elapsed time with interrupt hint: "({elapsed} · {key} to interrupt)"
		if strings.Contains(trimmed, "to interrupt)") {
			return true
		}
		// Details prefix from status indicator
		if strings.HasPrefix(trimmed, "└ ") || strings.HasPrefix(trimmed, "└") {
			return true
		}
		// Post-approval execution indicator
		if strings.Contains(trimmed, "✔ You approved codex to run") {
			return true
		}
	}
	return false
}
