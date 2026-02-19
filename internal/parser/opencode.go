package parser

import (
	"fmt"
	"regexp"
	"strconv"
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
//
// Source reference: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
// packages/opencode/src/tool/question.ts
// Question dialog: agent asks user a question with numbered options.
//   - Single-question: options listed as "1.", "2.", etc. with labels.
//   - Multi-question: tab-style headers at top (question headers + "Confirm" tab).
//   - Custom answer: last option is "Type your own answer".
//   - Multi-select: options prefixed with "[✓]" or "[ ]".
//   - Footer: "⇆ tab" (multi-question), "↑↓ select", "enter confirm/toggle/submit", "esc dismiss"
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
	if r := p.parseQuestionDialog(content); r != nil {
		return r
	}

	if p.isActiveExecution(content) {
		return &Result{
			Agent:     "opencode",
			Blocked:   false,
			Reason:    "actively executing",
			Reasoning: "deterministic parser: detected active execution indicators (spinner, Build/Plan, progress bar)",
			Subagents: p.parseSubagentTasks(content),
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
		// Question dialog footer: "↑↓ select" is unique to question dialogs
		// (permission dialogs use "⇆ select" instead).
		if strings.Contains(trimmed, "↑↓") && strings.Contains(trimmed, "select") {
			return false
		}
		if strings.Contains(trimmed, "esc dismiss") {
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
		// Build/Plan indicators (match isActiveExecution)
		if strings.Contains(trimmed, "▣ Build") || strings.Contains(trimmed, "■ Build") ||
			strings.Contains(trimmed, "▣ Plan") {
			return false
		}
		// QUEUED label and active toolcall indicators
		if strings.Contains(trimmed, "QUEUED") {
			return false
		}
		if (strings.Contains(trimmed, "Task") || strings.Contains(trimmed, "task")) &&
			strings.Contains(trimmed, "toolcall") && !strings.Contains(trimmed, "(0 toolcall") {
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
	// Question dialog footer: "↑↓ select" + "esc dismiss"
	if strings.Contains(content, "↑↓") && strings.Contains(content, "select") &&
		strings.Contains(content, "esc dismiss") {
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

// parseQuestionDialog detects the OpenCode question tool dialog.
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
//
// The question dialog renders numbered options (1., 2., etc.) with an
// "↑↓ select" + "esc dismiss" footer. It may also show "Type your own answer"
// as the last option. Multi-question forms have a tab header row and "⇆ tab"
// in the footer. The last tab is always "Confirm" which shows a review summary
// and only "enter submit" + "esc dismiss" (no "↑↓ select").
//
// This method detects both regular question tabs (with numbered options) and
// the Confirm tab (with "Review" text and no options).
func (p *OpenCodeParser) parseQuestionDialog(content string) *Result {
	lines := strings.Split(content, "\n")
	bottom := bottomNonEmpty(lines, bottomLines)

	// Detection: look for question dialog footer indicators in bottom lines.
	// "↑↓" + "select" is present on regular question tabs.
	// "⇆ tab" is present on all tabs of multi-question forms (including Confirm).
	hasQuestionFooter := false
	hasTabFooter := false
	for _, line := range bottom {
		stripped := stripDialogPrefix(strings.TrimSpace(line))
		if strings.Contains(stripped, "↑↓") && strings.Contains(stripped, "select") {
			hasQuestionFooter = true
		}
		if strings.Contains(stripped, "⇆") && strings.Contains(stripped, "tab") {
			hasTabFooter = true
		}
	}

	// Check for Confirm tab: has ⇆ tab footer but no ↑↓ select, and "Review" text.
	if hasTabFooter && !hasQuestionFooter {
		return p.parseConfirmTab(lines, content)
	}

	if !hasQuestionFooter {
		return nil
	}

	// Parse tab headers for multi-question forms.
	tabHeaders := parseTabHeaders(lines)

	// Extract question text and options for WaitingFor.
	waitingFor := extractQuestionSummary(lines)
	// Prefix with tab header info if present.
	if len(tabHeaders) > 0 {
		waitingFor = "[tabs] " + strings.Join(tabHeaders, " | ") + "\n" + waitingFor
	}

	// Build actions. Number keys directly select options in OpenCode's question dialog.
	// Count visible numbered options in the bottom portion to avoid stale matches.
	optionCount := countNumberedOptions(lines)

	// Detect multi-select: options have [✓]/[ ] checkbox prefixes.
	// In multi-select, number keys toggle checkboxes (not select-and-submit),
	// and Enter submits the selection.
	isMultiSelect := false
	for _, line := range lines {
		stripped := stripDialogPrefix(strings.TrimSpace(line))
		if isNumberedOption(stripped) && (strings.Contains(stripped, "[ ]") || strings.Contains(stripped, "[✓]")) {
			isMultiSelect = true
			break
		}
	}

	optionLabels := extractOptionLabels(lines)

	actions := make([]model.Action, 0, optionCount+4)
	for i := 1; i <= optionCount && i <= 9; i++ {
		label := fmt.Sprintf("select option %d", i)
		if isMultiSelect {
			label = fmt.Sprintf("toggle option %d", i)
		}
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
	// Multi-select: add explicit Submit action (Enter sends selection).
	if isMultiSelect {
		actions = append(actions, model.Action{
			Keys:  "Enter",
			Label: "submit selection",
			Risk:  "low",
			Raw:   true,
		})
	}
	// Tab navigation actions for multi-question forms.
	if hasTabFooter {
		actions = append(actions, model.Action{
			Keys:  "Tab",
			Label: "next tab",
			Risk:  "low",
			Raw:   true,
		})
		actions = append(actions, model.Action{
			Keys:  "BTab",
			Label: "prev tab",
			Risk:  "low",
			Raw:   true,
		})
	}
	actions = append(actions, model.Action{
		Keys:  "Escape",
		Label: "dismiss question",
		Risk:  "low",
		Raw:   true,
	})

	// For multi-select, recommend Submit (after all toggle actions).
	// For single-select, recommend first option.
	recommended := 0
	if isMultiSelect {
		recommended = optionCount // index of the Submit action
	}

	return &Result{
		Agent:       "opencode",
		Blocked:     true,
		Reason:      "question dialog waiting for answer",
		WaitingFor:  waitingFor,
		Actions:     actions,
		Recommended: recommended,
		Reasoning:   "deterministic parser: OpenCode question dialog detected (↑↓ select footer)",
	}
}

// parseConfirmTab handles the Confirm tab of a multi-question form.
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
//
// The Confirm tab shows "Review" followed by a summary of answers for each
// question. It has no numbered options — only Enter to submit all answers
// and Escape to dismiss.
func (p *OpenCodeParser) parseConfirmTab(lines []string, content string) *Result {
	// Verify this is a Confirm tab by looking for "Review" text.
	hasReview := false
	for _, line := range lines {
		stripped := stripDialogPrefix(trimRightPanel(strings.TrimSpace(line)))
		if stripped == "Review" {
			hasReview = true
			break
		}
	}
	if !hasReview {
		return nil
	}

	tabHeaders := parseTabHeaders(lines)

	// Build WaitingFor: include tab headers and review content.
	var waitParts []string
	if len(tabHeaders) > 0 {
		waitParts = append(waitParts, "[tabs] "+strings.Join(tabHeaders, " | "))
	}
	waitParts = append(waitParts, "[confirm tab]")

	// Collect review lines (after "Review", before footer).
	// Apply trimRightPanel before stripDialogPrefix to catch right-panel
	// junk (paths, git branches) separated by 10+ spaces from the border.
	inReview := false
	for _, line := range lines {
		stripped := stripDialogPrefix(trimRightPanel(strings.TrimSpace(line)))
		if stripped == "Review" {
			inReview = true
			continue
		}
		if inReview {
			if isFooterLine(stripped) || stripped == "" {
				if isFooterLine(stripped) {
					break
				}
				continue
			}
			waitParts = append(waitParts, stripped)
		}
	}

	waitingFor := strings.Join(waitParts, "\n")

	actions := []model.Action{
		{Keys: "Enter", Label: "submit all answers", Risk: "low", Raw: true},
		{Keys: "Tab", Label: "next tab", Risk: "low", Raw: true},
		{Keys: "BTab", Label: "prev tab", Risk: "low", Raw: true},
		{Keys: "Escape", Label: "dismiss question", Risk: "low", Raw: true},
	}

	return &Result{
		Agent:       "opencode",
		Blocked:     true,
		Reason:      "question dialog confirm tab",
		WaitingFor:  waitingFor,
		Actions:     actions,
		Recommended: 0, // recommend Enter (submit)
		Reasoning:   "deterministic parser: OpenCode question Confirm tab detected (⇆ tab footer + Review)",
	}
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

// taskTitleRe matches a running Task block title line.
// The spinner character is followed by the agent type and " Task".
// Example: "⠹ General Task" → AgentType="General"
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/index.tsx (line 1877)
//
//	title = "# " + Locale.titlecase(subagent_type) + " Task"
//	Spinner strips the "# " prefix: title.replace(/^# /, "")
var taskTitleRe = regexp.MustCompile(`(\S+)\s+Task$`)

// taskBodyRe matches the description + toolcalls line below a Task title.
// Example: "implement the feature (3 toolcalls)" → description="implement the feature", N=3
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/index.tsx (line 1888)
//
//	"{description} ({tools().length} toolcalls)"
var taskBodyRe = regexp.MustCompile(`^(.+?)\s+\((\d+)\s+toolcalls?\)$`)

// parseSubagentTasks scans pane content for running Task blocks and returns
// SubagentInfo for each one found. Only considers lines in the bottom portion
// of the screen (bottomLines) to avoid stale completed tasks in scrollback.
//
// A running Task block has a braille spinner on the title line. Completed
// tasks show "# {Type} Task" (no spinner) and are ignored.
func (p *OpenCodeParser) parseSubagentTasks(content string) []model.SubagentInfo {
	lines := strings.Split(content, "\n")
	bottom := bottomNonEmpty(lines, bottomLines)

	var subagents []model.SubagentInfo
	for i, line := range bottom {
		trimmed := strings.TrimSpace(line)

		// Look for a braille spinner character followed by "{Type} Task"
		if !hasBrailleSpinner(trimmed) {
			continue
		}
		m := taskTitleRe.FindStringSubmatch(trimmed)
		if m == nil {
			continue
		}

		info := model.SubagentInfo{
			AgentType: m[1],
		}

		// Next line should be the description + toolcalls
		if i+1 < len(bottom) {
			bodyTrimmed := strings.TrimSpace(bottom[i+1])
			if bm := taskBodyRe.FindStringSubmatch(bodyTrimmed); bm != nil {
				info.Description = bm[1]
				info.ToolCalls, _ = strconv.Atoi(bm[2])
			}
		}

		// Line after that may be the current tool: "└ {ToolName} {title}"
		if i+2 < len(bottom) {
			toolTrimmed := strings.TrimSpace(bottom[i+2])
			if strings.HasPrefix(toolTrimmed, "└ ") {
				info.CurrentTool = strings.TrimPrefix(toolTrimmed, "└ ")
			}
		}

		subagents = append(subagents, info)
	}
	return subagents
}

// hasBrailleSpinner returns true if the string contains a braille spinner
// character (U+280B to U+283F).
func hasBrailleSpinner(s string) bool {
	for _, r := range s {
		if r >= '⠋' && r <= '⠿' {
			return true
		}
	}
	return false
}
