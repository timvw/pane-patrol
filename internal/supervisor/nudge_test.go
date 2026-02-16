package supervisor

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// sendKeysCall records a single call to the mock sendKeys function.
type sendKeysCall struct {
	paneID string
	flag   string
	keys   string
}

func TestNudger_LiteralText(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {}, // no-op sleep for fast tests
	}

	err := nudger.NudgePane("session:0.0", "y", false)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	// Gastown pattern: literal "y" → Escape → Enter
	if len(calls) != 3 {
		t.Fatalf("expected 3 send-keys calls (literal, escape, enter), got %d", len(calls))
	}

	// 1. Literal text with -l flag
	if calls[0].flag != "-l" || calls[0].keys != "y" {
		t.Errorf("call 1: got flag=%q keys=%q, want flag=\"-l\" keys=\"y\"", calls[0].flag, calls[0].keys)
	}

	// 2. Escape (no flag)
	if calls[1].flag != "" || calls[1].keys != "Escape" {
		t.Errorf("call 2: got flag=%q keys=%q, want flag=\"\" keys=\"Escape\"", calls[1].flag, calls[1].keys)
	}

	// 3. Enter (no flag)
	if calls[2].flag != "" || calls[2].keys != "Enter" {
		t.Errorf("call 3: got flag=%q keys=%q, want flag=\"\" keys=\"Enter\"", calls[2].flag, calls[2].keys)
	}

	// All calls should target the right pane
	for i, c := range calls {
		if c.paneID != "session:0.0" {
			t.Errorf("call %d: paneID got %q, want %q", i+1, c.paneID, "session:0.0")
		}
	}
}

func TestNudger_ControlSequence(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "C-c", false)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	// Control sequences are sent raw — single call, no literal flag, no Enter
	if len(calls) != 1 {
		t.Fatalf("expected 1 send-keys call for control sequence, got %d", len(calls))
	}
	if calls[0].flag != "" || calls[0].keys != "C-c" {
		t.Errorf("got flag=%q keys=%q, want flag=\"\" keys=\"C-c\"", calls[0].flag, calls[0].keys)
	}
}

func TestNudger_EnterRetryOnFailure(t *testing.T) {
	enterAttempts := 0
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			if keys == "Enter" {
				enterAttempts++
				if enterAttempts < 3 {
					return fmt.Errorf("tmux busy")
				}
			}
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "yes", false)
	if err != nil {
		t.Fatalf("NudgePane() should succeed on 3rd Enter attempt: %v", err)
	}
	if enterAttempts != 3 {
		t.Errorf("expected 3 Enter attempts, got %d", enterAttempts)
	}
}

func TestNudger_EnterAllRetriesFail(t *testing.T) {
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			if keys == "Enter" {
				return fmt.Errorf("tmux not responding")
			}
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "y", false)
	if err == nil {
		t.Fatal("expected error when all Enter retries fail")
	}
	if !strings.Contains(err.Error(), "failed to send Enter after 3 attempts") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestNudger_LiteralSendFails(t *testing.T) {
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			if flag == "-l" {
				return fmt.Errorf("send failed")
			}
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "y", false)
	if err == nil {
		t.Fatal("expected error when literal send fails")
	}
	if !strings.Contains(err.Error(), "send literal keys") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestNudger_RawMode verifies that raw=true sends a single keypress
// with no Escape or Enter appended (for TUIs like Claude Code).
func TestNudger_RawMode(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	// "y" with raw=true should send "y" with -l flag — no Escape, no Enter
	err := nudger.NudgePane("session:0.0", "y", true)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 send-keys call for raw mode, got %d: %v", len(calls), calls)
	}
	if calls[0].flag != "-l" {
		t.Errorf("raw literal should use -l flag, got %q", calls[0].flag)
	}
	if calls[0].keys != "y" {
		t.Errorf("got keys=%q, want %q", calls[0].keys, "y")
	}
}

// TestNudger_RawMultiKeySequence verifies "Down Enter" is split into
// two separate raw send-keys calls.
func TestNudger_RawMultiKeySequence(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "Down Enter", true)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 send-keys calls for 'Down Enter', got %d: %v", len(calls), calls)
	}
	if calls[0].keys != "Down" {
		t.Errorf("call 1: got keys=%q, want %q", calls[0].keys, "Down")
	}
	if calls[1].keys != "Enter" {
		t.Errorf("call 2: got keys=%q, want %q", calls[1].keys, "Enter")
	}
}

// TestNudger_RawTripleKeySequence verifies "Down Down Enter" is sent as 3 calls.
func TestNudger_RawTripleKeySequence(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "Down Down Enter", true)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	if len(calls) != 3 {
		t.Fatalf("expected 3 send-keys calls, got %d: %v", len(calls), calls)
	}
	expected := []string{"Down", "Down", "Enter"}
	for i, want := range expected {
		if calls[i].keys != want {
			t.Errorf("call %d: got keys=%q, want %q", i+1, calls[i].keys, want)
		}
	}
}

// TestNudger_RawMixedSequence verifies "Down y" is split into two calls:
// "Down" sent as raw control sequence, "y" sent with -l literal flag.
// This is the Claude Code "approve and don't ask again" action.
func TestNudger_RawMixedSequence(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "Down y", true)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	if len(calls) != 2 {
		t.Fatalf("expected 2 send-keys calls for 'Down y', got %d: %v", len(calls), calls)
	}
	// "Down" is a control sequence — no -l flag
	if calls[0].flag != "" {
		t.Errorf("call 1 (Down): got flag=%q, want \"\"", calls[0].flag)
	}
	if calls[0].keys != "Down" {
		t.Errorf("call 1: got keys=%q, want %q", calls[0].keys, "Down")
	}
	// "y" is a literal character — needs -l flag
	if calls[1].flag != "-l" {
		t.Errorf("call 2 (y): got flag=%q, want \"-l\"", calls[1].flag)
	}
	if calls[1].keys != "y" {
		t.Errorf("call 2: got keys=%q, want %q", calls[1].keys, "y")
	}
}

// TestNudger_RawControlOnly verifies that pure control sequences in raw mode
// are sent without the -l flag.
func TestNudger_RawControlOnly(t *testing.T) {
	var calls []sendKeysCall
	nudger := &Nudger{
		SendKeys: func(paneID, flag, keys string) error {
			calls = append(calls, sendKeysCall{paneID, flag, keys})
			return nil
		},
		Sleep: func(d time.Duration) {},
	}

	err := nudger.NudgePane("session:0.0", "Enter", true)
	if err != nil {
		t.Fatalf("NudgePane() error: %v", err)
	}

	if len(calls) != 1 {
		t.Fatalf("expected 1 send-keys call, got %d: %v", len(calls), calls)
	}
	if calls[0].flag != "" {
		t.Errorf("control sequence should not use -l flag, got %q", calls[0].flag)
	}
	if calls[0].keys != "Enter" {
		t.Errorf("got keys=%q, want %q", calls[0].keys, "Enter")
	}
}

func TestSplitKeySequence(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"Enter", []string{"Enter"}},
		{"Down Enter", []string{"Down", "Enter"}},
		{"Down Down Enter", []string{"Down", "Down", "Enter"}},
		{"C-c", []string{"C-c"}},
		// Mixed control + literal: always split
		{"hello world", []string{"hello", "world"}},
		{"Down y", []string{"Down", "y"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitKeySequence(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("splitKeySequence(%q) = %v (len %d), want %v (len %d)",
					tt.input, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("splitKeySequence(%q)[%d] = %q, want %q",
						tt.input, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestIsControlSequence(t *testing.T) {
	tests := []struct {
		keys string
		want bool
	}{
		// Named keys
		{"Enter", true},
		{"Escape", true},
		{"Up", true},
		{"Down", true},
		{"Left", true},
		{"Right", true},
		{"Tab", true},
		{"BTab", true},
		{"Space", true},
		{"BSpace", true},
		{"DC", true},

		// Ctrl+key patterns
		{"C-c", true},
		{"C-z", true},
		{"C-a", true},
		{"C-d", true},

		// Meta/Alt+key patterns
		{"M-x", true},
		{"M-a", true},

		// Literal text (should NOT be control sequences)
		{"y", false},
		{"yes", false},
		{"continue", false},
		{"Y", false},
		{"n", false},
		{"q", false},
		{"", false},

		// Edge cases
		{"C-", false},    // incomplete Ctrl pattern (len 2, not 3)
		{"M-", false},    // incomplete Meta pattern (len 2, not 3)
		{"C-cc", false},  // too long for Ctrl pattern
		{"CC-c", false},  // wrong prefix position
		{"enter", false}, // lowercase (tmux keys are case-sensitive)
	}

	for _, tt := range tests {
		t.Run(tt.keys, func(t *testing.T) {
			got := isControlSequence(tt.keys)
			if got != tt.want {
				t.Errorf("isControlSequence(%q) = %v, want %v", tt.keys, got, tt.want)
			}
		})
	}
}
