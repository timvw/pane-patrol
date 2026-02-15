package supervisor

import (
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	"github.com/timvw/pane-patrol/internal/model"
)

// VerdictCache caches LLM verdicts keyed by pane content hash.
// When pane content hasn't changed since the last scan, the cached verdict
// is reused — saving an expensive LLM API call (~10-15s per pane).
//
// Cache entries have a TTL. After expiry, the pane is re-evaluated even if
// content is identical. This ensures we don't miss frozen/stuck agents where
// the content hasn't changed because the agent is truly stuck.
type VerdictCache struct {
	mu      sync.RWMutex
	entries map[string]*cacheEntry // keyed by pane target
	ttl     time.Duration
}

type cacheEntry struct {
	contentHash string
	verdict     model.Verdict
	cachedAt    time.Time
	hitCount    int
}

// NewVerdictCache creates a cache with the given TTL.
// A TTL of 0 disables caching.
func NewVerdictCache(ttl time.Duration) *VerdictCache {
	return &VerdictCache{
		entries: make(map[string]*cacheEntry),
		ttl:     ttl,
	}
}

// Lookup checks if we have a valid cached verdict for the given target and content.
// Returns the cached verdict and true if found and valid, nil and false otherwise.
func (c *VerdictCache) Lookup(target, content string) (*model.Verdict, bool) {
	if c.ttl <= 0 {
		return nil, false
	}

	hash := hashContent(content)

	c.mu.RLock()
	entry, ok := c.entries[target]
	c.mu.RUnlock()

	if !ok {
		return nil, false
	}

	// Content changed — cache miss
	if entry.contentHash != hash {
		return nil, false
	}

	// TTL expired — cache miss (forces re-evaluation of stale content)
	if time.Since(entry.cachedAt) > c.ttl {
		return nil, false
	}

	// Cache hit — increment counter and return
	c.mu.Lock()
	entry.hitCount++
	c.mu.Unlock()

	v := entry.verdict
	return &v, true
}

// Store saves a verdict in the cache for the given target and content.
func (c *VerdictCache) Store(target, content string, verdict model.Verdict) {
	if c.ttl <= 0 {
		return
	}

	hash := hashContent(content)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[target] = &cacheEntry{
		contentHash: hash,
		verdict:     verdict,
		cachedAt:    time.Now(),
	}
}

// Invalidate removes the cache entry for the given target, forcing
// re-evaluation on the next scan regardless of content.
func (c *VerdictCache) Invalidate(target string) {
	c.mu.Lock()
	delete(c.entries, target)
	c.mu.Unlock()
}

// Stats returns cache statistics.
type CacheStats struct {
	Entries int
	Hits    int64
	Misses  int64
}

// hashContent returns a hex-encoded SHA256 hash of the content.
func hashContent(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}
