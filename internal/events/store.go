package events

import (
	"sort"
	"sync"
	"time"
)

type Store struct {
	mu   sync.RWMutex
	ttl  time.Duration
	data map[string]Event
}

func NewStore(ttl time.Duration) *Store {
	return &Store{ttl: ttl, data: make(map[string]Event)}
}

func (s *Store) Upsert(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[e.Target] = e
}

func (s *Store) Snapshot(now time.Time) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(now, false)
}

func (s *Store) SnapshotAttention(now time.Time) []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(now, true)
}

func (s *Store) snapshotLocked(now time.Time, attentionOnly bool) []Event {
	if s.ttl > 0 {
		for target, e := range s.data {
			if now.Sub(e.TS) > s.ttl {
				delete(s.data, target)
			}
		}
	}
	result := make([]Event, 0, len(s.data))
	for _, e := range s.data {
		if attentionOnly && !IsAttentionState(e.State) {
			continue
		}
		result = append(result, e)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Target == result[j].Target {
			return result[i].TS.Before(result[j].TS)
		}
		return result[i].Target < result[j].Target
	})
	return result
}
