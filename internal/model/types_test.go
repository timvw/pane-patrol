package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildProcessHeader(t *testing.T) {
	tests := []struct {
		name      string
		pane      Pane
		wantEmpty bool
		contains  []string
	}{
		{
			name:      "no PID and no process tree",
			pane:      Pane{Session: "test", PID: 0, ProcessTree: nil},
			wantEmpty: true,
		},
		{
			name: "with PID and process tree",
			pane: Pane{
				Session:     "my-session",
				PID:         12345,
				Command:     "bash",
				ProcessTree: []string{"opencode --model gpt-4o"},
			},
			contains: []string{
				"[Process Info]",
				"Session: my-session",
				"Shell PID: 12345",
				"Shell command: bash",
				"Child processes:",
				"  opencode --model gpt-4o",
				"[Terminal Content]",
			},
		},
		{
			name: "with PID but no children",
			pane: Pane{
				Session: "idle",
				PID:     999,
				Command: "zsh",
			},
			contains: []string{
				"Shell PID: 999",
				"Child processes: (none)",
			},
		},
		{
			name: "zero PID but has process tree",
			pane: Pane{
				ProcessTree: []string{"node server.js"},
			},
			contains: []string{
				"[Process Info]",
				"  node server.js",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := BuildProcessHeader(tt.pane)
			if tt.wantEmpty {
				if got != "" {
					t.Errorf("expected empty string, got %q", got)
				}
				return
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("BuildProcessHeader() missing %q in:\n%s", want, got)
				}
			}
		})
	}
}

func TestVerdict_RecommendedZeroInJSON(t *testing.T) {
	// Recommended=0 is a valid index (first action). It must appear in JSON
	// output, not be omitted by omitempty.
	v := Verdict{
		Agent:   "opencode",
		Blocked: true,
		Reason:  "permission dialog",
		Actions: []Action{
			{Keys: "Enter", Label: "approve", Risk: "medium"},
			{Keys: "Escape", Label: "reject", Risk: "low"},
		},
		Recommended: 0,
	}

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	// The JSON must contain "recommended":0
	if !strings.Contains(string(data), `"recommended":0`) {
		t.Errorf("JSON output missing \"recommended\":0, got: %s", string(data))
	}
}

func TestVerdict_WaitingForInJSON(t *testing.T) {
	v := Verdict{
		Agent:      "opencode",
		Blocked:    true,
		Reason:     "permission dialog",
		WaitingFor: "△ Permission required\n$ git diff HEAD",
	}

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal error: %v", err)
	}

	if !strings.Contains(string(data), `"waiting_for"`) {
		t.Errorf("JSON output missing waiting_for field, got: %s", string(data))
	}
	if !strings.Contains(string(data), "Permission required") {
		t.Errorf("JSON output missing waiting_for content, got: %s", string(data))
	}
}
