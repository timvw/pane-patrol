// Package parser provides deterministic parsers for known AI coding agents.
//
// Each parser recognizes a specific agent's TUI patterns (permission dialogs,
// active execution indicators, idle states) and produces a verdict without
// calling an LLM. This is protocol parsing — we know exactly what strings
// these agents render because we read their source code.
//
// The Registry tries each registered parser in order. If none matches, the
// pane is reported as unrecognized (no fallback).
package parser

import (
	"strings"

	"github.com/timvw/pane-patrol/internal/model"
)

// Result is the output of a deterministic parser. It maps directly to the
// fields in model.Verdict.
type Result struct {
	Agent       string // e.g., "opencode", "claude_code", "codex"
	Blocked     bool
	Reason      string
	WaitingFor  string
	Actions     []model.Action
	Recommended int
	Reasoning   string
	Subagents   []model.SubagentInfo
}

// AgentParser recognizes a specific agent's TUI output and produces a
// deterministic verdict. Parse returns nil if the content does not belong
// to this agent.
type AgentParser interface {
	// Name returns the agent identifier (e.g., "opencode").
	Name() string

	// Parse examines pane content and the process tree. Returns a *Result
	// if this parser recognizes the agent, or nil if it does not.
	Parse(content string, processTree []string) *Result
}

// Registry holds an ordered list of parsers and tries each one.
type Registry struct {
	parsers []AgentParser
}

// NewRegistry creates a registry with the default set of parsers for
// the three supported agents: OpenCode, Claude Code, and Codex.
func NewRegistry() *Registry {
	return &Registry{
		parsers: []AgentParser{
			&OpenCodeParser{},
			&CodexParser{},
			&ClaudeCodeParser{},
		},
	}
}

// Parse tries each registered parser in order. Returns the first match,
// or nil if no parser recognizes the content.
func (r *Registry) Parse(content string, processTree []string) *Result {
	for _, p := range r.parsers {
		if result := p.Parse(content, processTree); result != nil {
			return result
		}
	}
	return nil
}

// bottomLines is the number of non-empty lines from the bottom of the
// captured content to examine for idle/active state. This must be small
// enough that stale indicators from prior turns—even in short captures—
// are excluded when a clear idle prompt is present at the very bottom.
// 8 lines gives enough room for status bars and multi-line dialogs while
// still excluding old output from prior turns.
const bottomLines = 8

// bottomNonEmpty returns the last n non-empty (after trimming) lines from
// a slice. This gives us the "current state" at the bottom of the screen,
// skipping trailing blank lines that terminals often have.
func bottomNonEmpty(lines []string, n int) []string {
	// Trim trailing empty lines first
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	return lines[start:end]
}

// isNumberedOption returns true if the trimmed line starts with a digit
// followed by a period (e.g., "1. PostgreSQL", "2. SQLite"). This matches
// the numbered option rendering used by both OpenCode and Codex question dialogs.
func isNumberedOption(trimmed string) bool {
	if len(trimmed) < 2 {
		return false
	}
	return trimmed[0] >= '1' && trimmed[0] <= '9' && trimmed[1] == '.'
}

// stripDialogPrefix removes known TUI border/cursor characters from the
// start of a trimmed line. This handles:
//   - OpenCode: "┃" (U+2503) thick vertical border from SplitBorder component
//   - Codex: "›" (U+203A) selection cursor
//   - Multi-select: "[✓]" or "[ ]" checkbox prefixes after the option number
//
// The result is re-trimmed so callers get clean content for matching.
func stripDialogPrefix(trimmed string) string {
	s := trimmed
	// Strip OpenCode border "┃" and Codex cursor "›"
	for {
		if strings.HasPrefix(s, "┃") {
			s = strings.TrimPrefix(s, "┃")
			s = strings.TrimLeft(s, " ")
			continue
		}
		if strings.HasPrefix(s, "›") {
			s = strings.TrimPrefix(s, "›")
			s = strings.TrimLeft(s, " ")
			continue
		}
		break
	}
	return s
}

// countNumberedOptions counts lines matching the "N. label" pattern in the
// provided lines slice. Strips known TUI border/cursor prefixes (┃, ›)
// before matching. Used to determine how many options are visible.
func countNumberedOptions(lines []string) int {
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		stripped := stripDialogPrefix(trimmed)
		if isNumberedOption(stripped) {
			count++
		}
	}
	return count
}

// extractOptionLabels returns the text after "N. " for each numbered option
// line found in the provided lines. Border/cursor prefixes (┃, ›) are
// stripped. The returned slice is ordered by appearance, up to 9 entries.
// Example: "┃  3. [✓] Authentication" → "[✓] Authentication"
func extractOptionLabels(lines []string) []string {
	var labels []string
	for _, line := range lines {
		stripped := stripDialogPrefix(strings.TrimSpace(line))
		if isNumberedOption(stripped) {
			// Skip past "N. " (3 chars), then trim right-side panel junk.
			// Terminal captures may include status bar content on the right
			// side of the line, separated by large whitespace gaps.
			label := trimRightPanel(strings.TrimSpace(stripped[3:]))
			labels = append(labels, label)
			if len(labels) >= 9 {
				break
			}
		}
	}
	return labels
}

// trimRightPanel removes right-side status bar content from a terminal line.
// OpenCode's TUI has a split layout where the dialog is on the left and file
// listings/status are on the right. In terminal captures these appear as one
// long line separated by a large gap of whitespace (10+ spaces).
func trimRightPanel(s string) string {
	// Find first run of 10+ spaces — everything after is right-panel junk
	spaceCount := 0
	for i, ch := range s {
		if ch == ' ' {
			spaceCount++
			if spaceCount >= 10 {
				return strings.TrimSpace(s[:i-spaceCount+1])
			}
		} else {
			spaceCount = 0
		}
	}
	return s
}

// extractQuestionSummary extracts question text and visible options from
// a question dialog. Looks for the question text above the first numbered
// option, then collects option labels with their description lines.
func extractQuestionSummary(lines []string) string {
	// Find the first numbered option line. Strip known border/cursor
	// prefixes (┃ from OpenCode, › from Codex) before checking.
	firstOptIdx := -1
	for i, line := range lines {
		trimmed := trimRightPanel(strings.TrimSpace(line))
		stripped := stripDialogPrefix(trimmed)
		if isNumberedOption(stripped) {
			firstOptIdx = i
			break
		}
	}
	if firstOptIdx < 0 {
		return "question dialog"
	}

	// Collect question text from lines above the first option.
	// Strip border prefixes so the output is clean.
	var questionLines []string
	for i := firstOptIdx - 1; i >= 0 && len(questionLines) < 4; i-- {
		trimmed := trimRightPanel(strings.TrimSpace(lines[i]))
		stripped := stripDialogPrefix(trimmed)
		if stripped == "" && len(questionLines) > 0 {
			break
		}
		if stripped != "" {
			questionLines = append(questionLines, stripped)
		}
	}
	// Reverse (collected bottom-up)
	for i, j := 0, len(questionLines)-1; i < j; i, j = i+1, j-1 {
		questionLines[i], questionLines[j] = questionLines[j], questionLines[i]
	}

	// Collect option labels with their description lines.
	// Each numbered option ("N. label") may be followed by indented description
	// lines before the next numbered option or footer. We collect up to 6
	// options with up to 2 description lines each. Border/cursor prefixes
	// are stripped so the output is clean.
	//
	// IMPORTANT: trimRightPanel is applied BEFORE stripDialogPrefix so that
	// right-panel junk separated by 10+ spaces from the border character is
	// caught. If applied after, stripDialogPrefix's TrimLeft collapses the
	// gap and leaves the junk word (e.g., "tool" from a wrapped path).
	var optionLines []string
	optCount := 0
	for i := firstOptIdx; i < len(lines) && optCount < 9; i++ {
		trimmed := trimRightPanel(strings.TrimSpace(lines[i]))
		stripped := stripDialogPrefix(trimmed)
		if isNumberedOption(stripped) {
			optionLines = append(optionLines, stripped)
			optCount++
			// Collect description lines below this option (up to 2)
			descCount := 0
			for j := i + 1; j < len(lines) && descCount < 2; j++ {
				dt := trimRightPanel(strings.TrimSpace(lines[j]))
				ds := stripDialogPrefix(dt)
				if ds == "" || isNumberedOption(ds) {
					break
				}
				// Skip footer lines
				if isFooterLine(ds) {
					break
				}
				optionLines = append(optionLines, "  "+ds)
				descCount++
				i = j // advance outer loop past description
			}
		}
	}

	question := strings.Join(questionLines, "\n")
	options := strings.Join(optionLines, "\n")
	if question != "" && options != "" {
		return question + "\n" + options
	}
	if question != "" {
		return question
	}
	if options != "" {
		return options
	}
	return "question dialog"
}

// parseTabHeaders extracts tab names from an OpenCode multi-question dialog.
// OpenCode renders tab headers as space-separated labels in a row above the
// question text. Tab names are separated by 3+ spaces. The last tab is always
// "Confirm" (added by the TUI, not from question data).
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/question.tsx
//
// Example: "  ┃   Next steps   Aspire wt config   Skill overlap   Confirm"
// Returns: ["Next steps", "Aspire wt config", "Skill overlap", "Confirm"]
//
// The search is bottom-up and restricted to lines inside the dialog box
// (those with a ┃ border prefix) to avoid matching unrelated content like
// table headers in scrollback.
//
// Returns nil if no tab header line is found.
func parseTabHeaders(lines []string) []string {
	// Search bottom-up: the tab header is above the question options, inside
	// the dialog. Only consider lines with a dialog border prefix (┃ or ›)
	// to avoid matching table headers or other content from scrollback.
	for i := len(lines) - 1; i >= 0; i-- {
		// Apply trimRightPanel early: right-panel status bar content (paths,
		// git branches) is separated by 10+ spaces. Without this, a review
		// line like "Skill overlap: ... <100 spaces> ~/path" gets 2 segments
		// and is misidentified as a tab header.
		trimmed := trimRightPanel(strings.TrimSpace(lines[i]))
		if trimmed == "" {
			continue
		}
		// Only consider lines inside the dialog box
		if !hasDialogPrefix(trimmed) {
			continue
		}
		stripped := stripDialogPrefix(trimmed)
		if stripped == "" {
			continue
		}
		// Skip lines that are numbered options, footer, or question text
		if isNumberedOption(stripped) || isFooterLine(stripped) {
			continue
		}
		// Tab header has 3+ spaces between segments and at least 2 segments
		parts := splitTabSegments(stripped)
		if len(parts) >= 2 {
			return parts
		}
	}
	return nil
}

// hasDialogPrefix returns true if the line starts with a dialog border
// character (┃ or ›), indicating it is inside an OpenCode dialog box.
func hasDialogPrefix(s string) bool {
	return strings.HasPrefix(s, "┃") || strings.HasPrefix(s, "›")
}

// splitTabSegments splits a string on runs of 3+ whitespace characters.
// Used to parse OpenCode tab header lines where tab names like
// "Next steps   Aspire wt config   Confirm" are separated by multiple spaces.
func splitTabSegments(s string) []string {
	var segments []string
	var current strings.Builder
	spaceCount := 0
	for _, ch := range s {
		if ch == ' ' || ch == '\t' {
			spaceCount++
		} else {
			if spaceCount >= 3 && current.Len() > 0 {
				segments = append(segments, strings.TrimSpace(current.String()))
				current.Reset()
			} else if spaceCount > 0 {
				current.WriteString(strings.Repeat(" ", spaceCount))
			}
			spaceCount = 0
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		segments = append(segments, strings.TrimSpace(current.String()))
	}
	return segments
}

// isFooterLine returns true if the line looks like a dialog footer (keyboard hints).
// Used by extractQuestionSummary to stop collecting description lines.
func isFooterLine(trimmed string) bool {
	// OpenCode footers
	if strings.Contains(trimmed, "⇆ select") || strings.Contains(trimmed, "⇆ tab") {
		return true
	}
	if strings.Contains(trimmed, "↑↓") && strings.Contains(trimmed, "select") {
		return true
	}
	if strings.Contains(trimmed, "esc dismiss") {
		return true
	}
	if strings.Contains(trimmed, "enter confirm") {
		return true
	}
	// Codex footers
	if strings.Contains(trimmed, "enter to submit") {
		return true
	}
	if strings.Contains(trimmed, "esc to interrupt") {
		return true
	}
	if strings.Contains(trimmed, "tab to add notes") {
		return true
	}
	return false
}

// progressVerbs are tool-specific action words used by Claude Code in its
// progress messages (e.g., "Fetching…", "Reading file.go…").
var progressVerbs = []string{
	"Fetching", "Reading", "Writing", "Searching", "Running", "Executing",
}

// hasProgressVerb returns true if the trimmed line looks like an active
// progress message. Matches two patterns:
//
//  1. Line ends with the verb, optionally followed by ellipsis:
//     "Fetching…", "Reading...", bare "Fetching"
//  2. Line starts with the verb and ends with ellipsis (verb + argument):
//     "Fetching https://api.example.com/data…"
//     "Reading src/main.go…"
//
// This avoids false matches on mid-sentence English like
// "Reading the file was successful" (no ellipsis, verb not at end).
func hasProgressVerb(trimmed string) bool {
	hasEllipsis := strings.HasSuffix(trimmed, "…") || strings.HasSuffix(trimmed, "...")
	for _, verb := range progressVerbs {
		// Pattern 1: verb at end of line (with or without ellipsis)
		if strings.HasSuffix(trimmed, verb) ||
			strings.HasSuffix(trimmed, verb+"…") ||
			strings.HasSuffix(trimmed, verb+"...") {
			return true
		}
		// Pattern 2: verb at start + ellipsis at end (e.g., "Fetching url…")
		if hasEllipsis && strings.HasPrefix(trimmed, verb) {
			return true
		}
	}
	return false
}
