package supervisor

import (
	"sync"
	"testing"
	"time"

	"github.com/timvw/pane-patrol/internal/model"
)

func TestVerdictCache_StoreAndLookup(t *testing.T) {
	cache := NewVerdictCache(5 * time.Minute)

	verdict := model.Verdict{
		Target:  "session:0.0",
		Agent:   "opencode",
		Blocked: true,
		Reason:  "waiting for confirmation",
	}

	// Store
	cache.Store("session:0.0", "pane content here", verdict)

	// Lookup with same content — cache hit
	got, ok := cache.Lookup("session:0.0", "pane content here")
	if !ok {
		t.Fatal("expected cache hit, got miss")
	}
	if got.Agent != "opencode" {
		t.Errorf("Agent: got %q, want %q", got.Agent, "opencode")
	}
	if !got.Blocked {
		t.Error("Blocked: got false, want true")
	}
}

func TestVerdictCache_ContentChanged(t *testing.T) {
	cache := NewVerdictCache(5 * time.Minute)

	verdict := model.Verdict{
		Target:  "session:0.0",
		Agent:   "opencode",
		Blocked: true,
	}

	cache.Store("session:0.0", "old content", verdict)

	// Lookup with different content — cache miss
	_, ok := cache.Lookup("session:0.0", "new content")
	if ok {
		t.Error("expected cache miss when content changed, got hit")
	}
}

func TestVerdictCache_TTLExpiry(t *testing.T) {
	// Use a very short TTL
	cache := NewVerdictCache(1 * time.Millisecond)

	verdict := model.Verdict{
		Target:  "session:0.0",
		Agent:   "opencode",
		Blocked: true,
	}

	cache.Store("session:0.0", "content", verdict)

	// Wait for TTL to expire
	time.Sleep(5 * time.Millisecond)

	// Lookup — should be expired
	_, ok := cache.Lookup("session:0.0", "content")
	if ok {
		t.Error("expected cache miss after TTL expiry, got hit")
	}
}

func TestVerdictCache_Invalidate(t *testing.T) {
	cache := NewVerdictCache(5 * time.Minute)

	verdict := model.Verdict{
		Target:  "session:0.0",
		Agent:   "opencode",
		Blocked: true,
	}

	cache.Store("session:0.0", "content", verdict)

	// Verify it's there
	_, ok := cache.Lookup("session:0.0", "content")
	if !ok {
		t.Fatal("expected cache hit before invalidation")
	}

	// Invalidate
	cache.Invalidate("session:0.0")

	// Lookup — should miss
	_, ok = cache.Lookup("session:0.0", "content")
	if ok {
		t.Error("expected cache miss after invalidation, got hit")
	}
}

func TestVerdictCache_ZeroTTLDisables(t *testing.T) {
	cache := NewVerdictCache(0)

	verdict := model.Verdict{
		Target:  "session:0.0",
		Agent:   "opencode",
		Blocked: true,
	}

	// Store should be a no-op
	cache.Store("session:0.0", "content", verdict)

	// Lookup should always miss
	_, ok := cache.Lookup("session:0.0", "content")
	if ok {
		t.Error("expected cache miss with zero TTL, got hit")
	}
}

func TestVerdictCache_MultiplePanes(t *testing.T) {
	cache := NewVerdictCache(5 * time.Minute)

	v1 := model.Verdict{Target: "session:0.0", Agent: "opencode", Blocked: true}
	v2 := model.Verdict{Target: "session:0.1", Agent: "not_an_agent", Blocked: false}

	cache.Store("session:0.0", "content-a", v1)
	cache.Store("session:0.1", "content-b", v2)

	got1, ok1 := cache.Lookup("session:0.0", "content-a")
	got2, ok2 := cache.Lookup("session:0.1", "content-b")

	if !ok1 || !ok2 {
		t.Fatalf("expected both cache hits: ok1=%v ok2=%v", ok1, ok2)
	}
	if got1.Agent != "opencode" {
		t.Errorf("pane 0.0: got agent %q, want %q", got1.Agent, "opencode")
	}
	if got2.Agent != "not_an_agent" {
		t.Errorf("pane 0.1: got agent %q, want %q", got2.Agent, "not_an_agent")
	}
}

func TestVerdictCache_LookupReturnsCopy(t *testing.T) {
	cache := NewVerdictCache(5 * time.Minute)

	verdict := model.Verdict{Target: "session:0.0", Agent: "opencode", Blocked: true}
	cache.Store("session:0.0", "content", verdict)

	// Get a reference and modify it
	got, _ := cache.Lookup("session:0.0", "content")
	got.Agent = "mutated"

	// Lookup again — should still be the original
	got2, _ := cache.Lookup("session:0.0", "content")
	if got2.Agent != "opencode" {
		t.Errorf("cache returned a reference instead of a copy: got %q after mutation", got2.Agent)
	}
}

func TestHashContent(t *testing.T) {
	// Same content produces same hash
	h1 := hashContent("hello world")
	h2 := hashContent("hello world")
	if h1 != h2 {
		t.Error("same content should produce same hash")
	}

	// Different content produces different hash
	h3 := hashContent("different")
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}

	// Hash is hex-encoded SHA256 (64 chars)
	if len(h1) != 64 {
		t.Errorf("hash length: got %d, want 64", len(h1))
	}
}

func TestVerdictCache_TTLExpiryDeletesEntry(t *testing.T) {
	cache := NewVerdictCache(1 * time.Millisecond)

	verdict := model.Verdict{Target: "session:0.0", Agent: "opencode", Blocked: true}
	cache.Store("session:0.0", "content", verdict)

	time.Sleep(5 * time.Millisecond)

	// Lookup should miss AND delete the stale entry
	_, ok := cache.Lookup("session:0.0", "content")
	if ok {
		t.Error("expected cache miss after TTL expiry")
	}

	// Verify the entry was actually deleted
	cache.mu.RLock()
	_, exists := cache.entries["session:0.0"]
	cache.mu.RUnlock()
	if exists {
		t.Error("expired entry should be deleted from map on TTL miss")
	}
}

func TestVerdictCache_ConcurrentAccess(t *testing.T) {
	// This test validates thread-safety under -race.
	cache := NewVerdictCache(5 * time.Minute)
	const goroutines = 50

	var wg sync.WaitGroup

	// Concurrent stores
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			v := model.Verdict{Target: "session:0.0", Agent: "opencode", Blocked: true}
			cache.Store("session:0.0", "content", v)
		}(i)
	}

	// Concurrent lookups
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			cache.Lookup("session:0.0", "content")
		}(i)
	}

	// Concurrent invalidates
	for i := 0; i < goroutines/5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Invalidate("session:0.0")
		}()
	}

	wg.Wait()
	// If we get here without -race complaints, the locking is correct
}
