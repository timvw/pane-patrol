package model

import (
	"fmt"
	"strings"
	"time"
)

// Pane represents a terminal multiplexer pane.
type Pane struct {
	// Target is the fully qualified pane identifier (e.g., "session:0.0").
	Target string `json:"target"`
	// Session is the session name.
	Session string `json:"session"`
	// Window is the window index.
	Window int `json:"window"`
	// Pane is the pane index.
	Pane int `json:"pane"`
	// PID is the pane's shell process ID.
	PID int `json:"pid"`
	// Command is the current command running in the pane (e.g., "node", "bash").
	Command string `json:"command"`
	// ProcessTree is the list of child processes (command lines) running in the pane.
	ProcessTree []string `json:"process_tree,omitempty"`
}

// Verdict is the result of an LLM evaluation of a pane's content.
type Verdict struct {
	// Target is the fully qualified pane identifier.
	Target string `json:"target"`
	// Session is the session name.
	Session string `json:"session"`
	// Window is the window index.
	Window int `json:"window"`
	// Pane is the pane index.
	Pane int `json:"pane"`
	// Command is the current command running in the pane.
	Command string `json:"command"`

	// Agent is the detected agent name (e.g., "claude_code", "opencode", "codex", "not_an_agent").
	// Set by deterministic parsers for known agents, or the LLM for unknown agents.
	Agent string `json:"agent"`
	// Blocked indicates whether the pane is waiting for human input.
	Blocked bool `json:"blocked"`
	// Reason is a one-line summary of the verdict.
	Reason string `json:"reason"`
	// WaitingFor is a verbatim extract of the dialog, prompt, or question the
	// agent is blocked on. Only populated when blocked is true.
	WaitingFor string `json:"waiting_for"`
	// Reasoning is the LLM's detailed step-by-step analysis.
	Reasoning string `json:"reasoning"`

	// Actions is a list of possible actions to unblock the pane.
	// Set by deterministic parsers for known agents, or the LLM for unknown agents.
	// Only populated when the pane is blocked.
	Actions []Action `json:"actions,omitempty"`
	// Recommended is the 0-based index into Actions for the recommended action.
	Recommended int `json:"recommended"`

	// Usage tracks token consumption for this evaluation.
	Usage TokenUsage `json:"usage,omitempty"`

	// Content is the raw pane capture. Only populated when verbose mode is enabled.
	Content string `json:"content,omitempty"`

	// Model is the LLM model that produced this verdict.
	Model string `json:"model"`
	// Provider is the LLM provider used (e.g., "anthropic", "openai").
	Provider string `json:"provider"`
	// EvaluatedAt is the timestamp when the evaluation was performed.
	EvaluatedAt time.Time `json:"evaluated_at"`
	// DurationMs is the wall-clock time in milliseconds for capture + evaluation.
	DurationMs int64 `json:"duration_ms"`
}

// Action represents a possible action to unblock a pane.
type Action struct {
	// Keys is the tmux send-keys input (e.g., "y", "C-c", "Enter").
	Keys string `json:"keys"`
	// Label is a human-readable description of what this action does.
	Label string `json:"label"`
	// Risk is the risk level: "low", "medium", "high".
	Risk string `json:"risk"`
	// Raw, when true, sends Keys as a single raw keypress (no Escape+Enter
	// appended). Use this for TUIs that run in raw mode and process each
	// keypress individually (e.g., Claude Code, OpenCode, Codex).
	// LLM-generated actions leave this false (default literal mode).
	Raw bool `json:"raw,omitempty"`
}

// TokenUsage tracks LLM token consumption for a single evaluation.
type TokenUsage struct {
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`

	// CacheReadInputTokens is the number of input tokens read from the
	// provider's prompt cache (Anthropic cache_read_input_tokens,
	// OpenAI prompt_tokens_details.cached_tokens).
	CacheReadInputTokens int64 `json:"cache_read_input_tokens,omitempty"`
	// CacheCreationInputTokens is the number of input tokens used to
	// create a new cache entry (Anthropic only).
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens,omitempty"`
}

// BuildProcessHeader returns a process metadata header prepended to pane
// content before evaluation. Provides context for both parsers and LLM.
// Returns an empty string if no process info is available.
func BuildProcessHeader(pane Pane) string {
	if pane.PID <= 0 && len(pane.ProcessTree) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Process Info]\n")
	b.WriteString(fmt.Sprintf("Session: %s\n", pane.Session))
	b.WriteString(fmt.Sprintf("Shell PID: %d\n", pane.PID))
	b.WriteString(fmt.Sprintf("Shell command: %s\n", pane.Command))
	if len(pane.ProcessTree) > 0 {
		b.WriteString("Child processes:\n")
		for _, proc := range pane.ProcessTree {
			b.WriteString(fmt.Sprintf("  %s\n", proc))
		}
	} else {
		b.WriteString("Child processes: (none)\n")
	}
	b.WriteString("\n[Terminal Content]\n")
	return b.String()
}

// LLMVerdict is the JSON structure returned by the LLM.
// This is parsed from the LLM's response text.
type LLMVerdict struct {
	Agent       string   `json:"agent"`
	Blocked     bool     `json:"blocked"`
	Reason      string   `json:"reason"`
	WaitingFor  string   `json:"waiting_for"`
	Actions     []Action `json:"actions,omitempty"`
	Recommended int      `json:"recommended"`
	Reasoning   string   `json:"reasoning"`

	// Usage is populated by the evaluator, not parsed from the LLM response.
	Usage TokenUsage `json:"-"`
}
