package model

import "time"

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
	// Command is the current command running in the pane (e.g., "node", "bash").
	Command string `json:"command"`
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

	// Agent is the detected agent name (e.g., "claude-code", "opencode", "not_an_agent").
	// Determined by the LLM, not by Go code (ZFC compliance).
	Agent string `json:"agent"`
	// Blocked indicates whether the pane is waiting for human input.
	Blocked bool `json:"blocked"`
	// Reason is a one-line summary of the verdict.
	Reason string `json:"reason"`
	// Reasoning is the LLM's detailed step-by-step analysis.
	Reasoning string `json:"reasoning"`

	// Content is the raw pane capture. Only populated when verbose mode is enabled.
	Content string `json:"content,omitempty"`

	// Model is the LLM model that produced this verdict.
	Model string `json:"model"`
	// Provider is the LLM provider used (e.g., "anthropic", "openai").
	Provider string `json:"provider"`
	// EvaluatedAt is the timestamp when the evaluation was performed.
	EvaluatedAt time.Time `json:"evaluated_at"`
}

// LLMVerdict is the JSON structure returned by the LLM.
// This is parsed from the LLM's response text.
type LLMVerdict struct {
	Agent     string `json:"agent"`
	Blocked   bool   `json:"blocked"`
	Reason    string `json:"reason"`
	Reasoning string `json:"reasoning"`
}
