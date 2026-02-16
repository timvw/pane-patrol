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
