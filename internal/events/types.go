package events

import (
	"fmt"
	"strings"
	"time"
)

const (
	StateWaitingInput    = "waiting_input"
	StateWaitingApproval = "waiting_approval"
	StateRunning         = "running"
	StateCompleted       = "completed"
	StateError           = "error"
	StateIdle            = "idle"
)

// Event is the normalized hook payload consumed by pane-patrol.
type Event struct {
	Assistant string    `json:"assistant"`
	State     string    `json:"state"`
	Target    string    `json:"target"`
	TS        time.Time `json:"ts"`
	Message   string    `json:"message,omitempty"`
}

func (e Event) Validate() error {
	if strings.TrimSpace(e.Assistant) == "" {
		return fmt.Errorf("assistant is required")
	}
	if !isValidState(e.State) {
		return fmt.Errorf("invalid state %q", e.State)
	}
	if !isValidTarget(e.Target) {
		return fmt.Errorf("invalid target %q", e.Target)
	}
	if e.TS.IsZero() {
		return fmt.Errorf("ts is required")
	}
	return nil
}

func IsAttentionState(state string) bool {
	return state == StateWaitingInput || state == StateWaitingApproval
}

func isValidState(state string) bool {
	switch state {
	case StateWaitingInput, StateWaitingApproval, StateRunning, StateCompleted, StateError, StateIdle:
		return true
	default:
		return false
	}
}

// isValidTarget checks for tmux target format: session:window.pane
func isValidTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	colon := strings.LastIndex(target, ":")
	if colon <= 0 || colon == len(target)-1 {
		return false
	}
	rest := target[colon+1:]
	dot := strings.LastIndex(rest, ".")
	if dot <= 0 || dot == len(rest)-1 {
		return false
	}
	return true
}
