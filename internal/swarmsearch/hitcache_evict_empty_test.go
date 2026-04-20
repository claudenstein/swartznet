package swarmsearch

import "testing"

// TestEvictOldestOnEmptyCache covers the
// `len(c.order) == 0 → return` early-exit branch of evictOldest.
// In production this is unreachable because Store gates eviction
// behind a capacity check, but the helper is meant to be safe to
// call defensively. Construct an empty HitCache and call directly.
func TestEvictOldestOnEmptyCache(t *testing.T) {
	t.Parallel()
	c := NewHitCache(8)
	// Must not panic; nothing to assert beyond "no panic".
	c.evictOldest()
	c.evictOldest() // and again — still safe
}
