// Package parser provides deterministic parsers for known AI coding agents.
//
// Each parser recognizes a specific agent's TUI patterns (permission dialogs,
// active execution indicators, idle states) and produces a verdict without
// calling an LLM. This is protocol parsing — we know exactly what strings
// these agents render because we read their source code.
//
// The Registry tries each registered parser in order. If none matches, the
// caller falls back to LLM evaluation.
package parser

import (
	"strings"

	"github.com/timvw/pane-patrol/internal/model"
)

// Result is the output of a deterministic parser. It maps directly to the
// fields in model.Verdict that would normally come from the LLM.
type Result struct {
	Agent       string // e.g., "opencode", "claude_code", "codex"
	Blocked     bool
	Reason      string
	WaitingFor  string
	Actions     []model.Action
	Recommended int
	Reasoning   string
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
			&ClaudeCodeParser{},
			&CodexParser{},
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

// countNumberedOptions counts lines matching the "N. label" pattern in the
// provided lines slice. Used to determine how many options are visible.
func countNumberedOptions(lines []string) int {
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isNumberedOption(trimmed) {
			count++
		}
	}
	return count
}

// extractQuestionSummary extracts question text and visible options from
// a question dialog. Looks for the question text above the first numbered
// option, then collects the option labels.
func extractQuestionSummary(lines []string) string {
	// Find the first numbered option line
	firstOptIdx := -1
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isNumberedOption(trimmed) {
			firstOptIdx = i
			break
		}
	}
	if firstOptIdx < 0 {
		return "question dialog"
	}

	// Collect question text from lines above the first option
	var questionLines []string
	for i := firstOptIdx - 1; i >= 0 && len(questionLines) < 4; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" && len(questionLines) > 0 {
			break
		}
		if trimmed != "" {
			questionLines = append(questionLines, trimmed)
		}
	}
	// Reverse (collected bottom-up)
	for i, j := 0, len(questionLines)-1; i < j; i, j = i+1, j-1 {
		questionLines[i], questionLines[j] = questionLines[j], questionLines[i]
	}

	// Collect option labels (up to 6 to keep WaitingFor concise)
	var optionLabels []string
	for i := firstOptIdx; i < len(lines) && len(optionLabels) < 6; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if isNumberedOption(trimmed) {
			optionLabels = append(optionLabels, trimmed)
		}
	}

	question := strings.Join(questionLines, "\n")
	options := strings.Join(optionLabels, "\n")
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
