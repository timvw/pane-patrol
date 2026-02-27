package events

import (
	"testing"
	"time"
)

func TestValidate_MinimalValidEvent(t *testing.T) {
	e := Event{Assistant: "claude", State: StateWaitingInput, Target: "s:0.1", TS: time.Now().UTC()}
	if err := e.Validate(); err != nil {
		t.Fatalf("expected valid event, got %v", err)
	}
}

func TestValidate_MissingAssistant(t *testing.T) {
	e := Event{State: StateWaitingInput, Target: "s:0.1", TS: time.Now().UTC()}
	if err := e.Validate(); err == nil {
		t.Fatalf("expected missing assistant validation error")
	}
}

func TestValidate_InvalidState(t *testing.T) {
	e := Event{Assistant: "claude", State: "blocked-ish", Target: "s:0.1", TS: time.Now().UTC()}
	if err := e.Validate(); err == nil {
		t.Fatalf("expected invalid state validation error")
	}
}

func TestValidate_InvalidTarget(t *testing.T) {
	e := Event{Assistant: "claude", State: StateWaitingInput, Target: "not-a-target", TS: time.Now().UTC()}
	if err := e.Validate(); err == nil {
		t.Fatalf("expected invalid target validation error")
	}
}

func TestValidate_MissingTimestamp(t *testing.T) {
	e := Event{Assistant: "claude", State: StateWaitingInput, Target: "s:0.1"}
	if err := e.Validate(); err == nil {
		t.Fatalf("expected missing timestamp validation error")
	}
}

func TestIsAttentionState(t *testing.T) {
	if !IsAttentionState(StateWaitingInput) {
		t.Fatalf("waiting_input should be attention state")
	}
	if !IsAttentionState(StateWaitingApproval) {
		t.Fatalf("waiting_approval should be attention state")
	}
	if IsAttentionState(StateRunning) {
		t.Fatalf("running should not be attention state")
	}
}
