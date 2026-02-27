package model

import (
	"fmt"
	"strings"
	"time"
)

// EvalSource constants identify how a Verdict was produced.
const (
	EvalSourceParser = "parser"
	EvalSourceCache  = "cache"
	EvalSourceError  = "error"
	EvalSourceEvent  = "event"
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

// Verdict is the result of evaluating a pane's content.
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
	// Set by deterministic parsers for known agents.
	Agent string `json:"agent"`
	// Blocked indicates whether the pane is waiting for human input.
	Blocked bool `json:"blocked"`
	// Reason is a one-line summary of the verdict.
	Reason string `json:"reason"`
	// WaitingFor is a verbatim extract of the dialog, prompt, or question the
	// agent is blocked on. Only populated when blocked is true.
	WaitingFor string `json:"waiting_for"`
	// Reasoning is the detailed step-by-step analysis.
	Reasoning string `json:"reasoning"`

	// Actions is a list of possible actions to unblock the pane.
	// Set by deterministic parsers for known agents.
	// Only populated when the pane is blocked.
	Actions []Action `json:"actions,omitempty"`
	// Recommended is the 0-based index into Actions for the recommended action.
	Recommended int `json:"recommended"`
	// Subagents lists detected subagent tasks parsed from TUI content.
	// Populated by deterministic parsers when a running Task block is visible.
	Subagents []SubagentInfo `json:"subagents,omitempty"`

	// Content is the raw pane capture. Only populated when verbose mode is enabled.
	Content string `json:"content,omitempty"`

	// EvalSource records how this verdict was produced.
	// Use the EvalSource* constants.
	EvalSource string `json:"eval_source"`

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
	Raw bool `json:"raw,omitempty"`
}

// SubagentInfo describes a detected subagent task parsed from TUI content.
//
// Source: packages/opencode/src/cli/cmd/tui/routes/session/index.tsx
// Task component renders: spinner + "{AgentType} Task", "{description} ({N} toolcalls)",
// "└ {ToolName} {title}", and "view subagents" keybind hint.
type SubagentInfo struct {
	// AgentType is the subagent type (e.g., "General", "Explore").
	// Extracted from the Task block title "{AgentType} Task".
	AgentType string `json:"agent_type,omitempty"`
	// Description is the task description (e.g., "implement the feature").
	Description string `json:"description,omitempty"`
	// ToolCalls is the number of tool calls made by the subagent.
	ToolCalls int `json:"tool_calls"`
	// CurrentTool is the tool currently being executed (e.g., "Bash npm test").
	// Extracted from the "└ {ToolName} {title}" line.
	CurrentTool string `json:"current_tool,omitempty"`
}

// BaseVerdict returns a Verdict pre-filled with common pane identity and
// timing fields. Callers set the remaining source-specific fields (Agent,
// Blocked, Reason, EvalSource, etc.) directly.
func BaseVerdict(pane Pane, start time.Time) Verdict {
	return Verdict{
		Target:      pane.Target,
		Session:     pane.Session,
		Window:      pane.Window,
		Pane:        pane.Pane,
		Command:     pane.Command,
		EvaluatedAt: time.Now().UTC(),
		DurationMs:  time.Since(start).Milliseconds(),
	}
}

// BuildProcessHeader returns a process metadata header prepended to pane
// content before evaluation. Provides context for parsers.
// Returns an empty string if no process info is available.
func BuildProcessHeader(pane Pane) string {
	if pane.PID <= 0 && len(pane.ProcessTree) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[Process Info]\n")
	fmt.Fprintf(&b, "Session: %s\n", pane.Session)
	fmt.Fprintf(&b, "Shell PID: %d\n", pane.PID)
	fmt.Fprintf(&b, "Shell command: %s\n", pane.Command)
	if len(pane.ProcessTree) > 0 {
		b.WriteString("Child processes:\n")
		for _, proc := range pane.ProcessTree {
			fmt.Fprintf(&b, "  %s\n", proc)
		}
	} else {
		b.WriteString("Child processes: (none)\n")
	}
	b.WriteString("\n[Terminal Content]\n")
	return b.String()
}
