package events

import (
	"testing"
	"time"
)

func TestStore_UpsertAndSnapshot(t *testing.T) {
	now := time.Now().UTC()
	s := NewStore(5 * time.Minute)
	s.Upsert(Event{Assistant: "claude", State: StateWaitingInput, Target: "s:0.1", TS: now})

	got := s.Snapshot(now)
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].State != StateWaitingInput {
		t.Fatalf("expected state waiting_input, got %s", got[0].State)
	}
}

func TestStore_UpsertOverwritesSameTarget(t *testing.T) {
	now := time.Now().UTC()
	s := NewStore(5 * time.Minute)
	s.Upsert(Event{Assistant: "claude", State: StateRunning, Target: "s:0.1", TS: now})
	s.Upsert(Event{Assistant: "claude", State: StateWaitingApproval, Target: "s:0.1", TS: now.Add(1 * time.Second)})

	got := s.Snapshot(now.Add(1 * time.Second))
	if len(got) != 1 {
		t.Fatalf("expected 1 event, got %d", len(got))
	}
	if got[0].State != StateWaitingApproval {
		t.Fatalf("expected overwritten state waiting_approval, got %s", got[0].State)
	}
}

func TestStore_SnapshotAttentionOnly(t *testing.T) {
	now := time.Now().UTC()
	s := NewStore(5 * time.Minute)
	s.Upsert(Event{Assistant: "claude", State: StateRunning, Target: "s:0.1", TS: now})
	s.Upsert(Event{Assistant: "opencode", State: StateWaitingInput, Target: "s:0.2", TS: now})

	got := s.SnapshotAttention(now)
	if len(got) != 1 {
		t.Fatalf("expected 1 attention event, got %d", len(got))
	}
	if got[0].Target != "s:0.2" {
		t.Fatalf("expected target s:0.2, got %s", got[0].Target)
	}
}

func TestStore_ExpiresStaleEntries(t *testing.T) {
	now := time.Now().UTC()
	s := NewStore(2 * time.Minute)
	s.Upsert(Event{Assistant: "claude", State: StateWaitingInput, Target: "s:0.1", TS: now})

	got := s.Snapshot(now.Add(3 * time.Minute))
	if len(got) != 0 {
		t.Fatalf("expected 0 events after ttl expiry, got %d", len(got))
	}
}
