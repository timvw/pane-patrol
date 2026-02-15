package supervisor

import (
	"fmt"
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

	err := nudger.NudgePane("session:0.0", "y")
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

	err := nudger.NudgePane("session:0.0", "C-c")
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

	err := nudger.NudgePane("session:0.0", "yes")
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

	err := nudger.NudgePane("session:0.0", "y")
	if err == nil {
		t.Fatal("expected error when all Enter retries fail")
	}
	if !contains(err.Error(), "failed to send Enter after 3 attempts") {
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

	err := nudger.NudgePane("session:0.0", "y")
	if err == nil {
		t.Fatal("expected error when literal send fails")
	}
	if !contains(err.Error(), "send literal keys") {
		t.Errorf("unexpected error message: %v", err)
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
