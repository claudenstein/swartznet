package swarmsearch

import (
	"sync"
)

// HitCache is an LRU cache of recently-seen MergedHits keyed by
// infohash. Inspired by BIP-152's compact block relay insight:
// "assume the receiver already has most of the data." In the
// sn_search context, when a query fans out to 10 peers and 8
// of them return the same popular torrent, the merge step can
// skip the full metadata comparison for hits that are already
// in the cache and just increment the source count.
//
// v1 implementation: local-only merge speedup. The cache is
// queried during Protocol.Query's merge phase. It does NOT
// affect the wire format — full hits are still sent on the
// wire. The v1.1 compact result encoding (where the responder
// sends SipHash short IDs for hits the querier has already
// seen) builds on this cache as its state store.
//
// Concurrent-safe.
type HitCache struct {
	mu      sync.Mutex
	entries map[string]*cachedHit // keyed by infohash hex (40 chars)
	order   []string              // LRU order: oldest at [0]
	maxSize int
}

type cachedHit struct {
	hit      MergedHit
	hitCount int // how many times this infohash has been seen across queries
}

// DefaultHitCacheSize is the maximum number of distinct
// infohashes kept in the cache. 4096 covers the working set
// of even an aggressive search session without using
// significant memory (~4K × ~200 bytes per MergedHit ≈ 800 KB).
const DefaultHitCacheSize = 4096

// NewHitCache creates an empty cache with the given max size.
func NewHitCache(maxSize int) *HitCache {
	if maxSize <= 0 {
		maxSize = DefaultHitCacheSize
	}
	return &HitCache{
		entries: make(map[string]*cachedHit),
		maxSize: maxSize,
	}
}

// Lookup checks whether infohash (40-char hex) is in the cache.
// Returns the cached MergedHit and true if found, or zero and
// false if not. A hit bumps the entry to the front of the LRU.
func (c *HitCache) Lookup(infohash string) (MergedHit, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[infohash]
	if !ok {
		return MergedHit{}, false
	}
	e.hitCount++
	c.promote(infohash)
	return e.hit, true
}

// Store adds or updates a MergedHit in the cache. If the cache
// is at capacity, the least-recently-used entry is evicted.
func (c *HitCache) Store(hit MergedHit) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ih := hit.InfoHash
	if _, ok := c.entries[ih]; ok {
		c.entries[ih].hit = hit
		c.promote(ih)
		return
	}
	if len(c.entries) >= c.maxSize {
		c.evictOldest()
	}
	c.entries[ih] = &cachedHit{hit: hit, hitCount: 1}
	c.order = append(c.order, ih)
}

// Size returns the number of entries currently in the cache.
func (c *HitCache) Size() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// promote moves infohash to the end of the order slice (MRU).
func (c *HitCache) promote(infohash string) {
	for i, ih := range c.order {
		if ih == infohash {
			c.order = append(c.order[:i], c.order[i+1:]...)
			c.order = append(c.order, infohash)
			return
		}
	}
}

// evictOldest removes the oldest entry from the cache.
func (c *HitCache) evictOldest() {
	if len(c.order) == 0 {
		return
	}
	oldest := c.order[0]
	c.order = c.order[1:]
	delete(c.entries, oldest)
}
