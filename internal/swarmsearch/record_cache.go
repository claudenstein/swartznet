// In-memory RecordCache — a ready-to-use RecordSource
// implementation the engine can populate as the local publisher
// signs new records. Filters queries by pubkeys / since / prefix
// so the sync responder doesn't have to over-share.

package swarmsearch

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"sync"
)

// RecordCache is a thread-safe set of LocalRecords keyed by
// their RIBLT element ID (SHA-256 over pk || kw || ih || t_LE).
// Implements the RecordSource interface so it can be attached via
// Protocol.SetRecordSource.
//
// Bounded-size eviction via SetMaxRecords: when the cap is
// reached, the oldest-inserted record is evicted FIFO-style
// before the new one lands. Zero means unlimited (default).
// Callers may also call PruneOlderThan periodically to trim
// records past a T threshold.
type RecordCache struct {
	mu    sync.RWMutex
	byID  map[[32]byte]LocalRecord
	order [][32]byte // insertion order for FIFO eviction
	max   int        // 0 = unlimited
}

// NewRecordCache returns an empty cache with no size cap.
func NewRecordCache() *RecordCache {
	return &RecordCache{
		byID: make(map[[32]byte]LocalRecord),
	}
}

// SetMaxRecords sets the soft cap for Add. Zero disables the
// cap; any positive value triggers FIFO eviction of the
// oldest-inserted record when Len would otherwise exceed cap.
//
// Setting a cap below Len DOES NOT proactively evict — the next
// Add does the eviction. Callers that need an immediate drain
// should call PruneOldestTo afterward.
func (c *RecordCache) SetMaxRecords(n int) {
	c.mu.Lock()
	c.max = n
	c.mu.Unlock()
}

// MaxRecords returns the current cap (0 = unlimited).
func (c *RecordCache) MaxRecords() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.max
}

// Add inserts a record. Idempotent: re-adding the same record
// (identical pk/kw/ih/t) is a no-op because the ID is deterministic.
// Re-adding under a new timestamp produces a new ID and a distinct
// cache entry. When MaxRecords is set and Len is at the cap, Add
// evicts the oldest-inserted record before writing the new one.
func (c *RecordCache) Add(r LocalRecord) {
	id := cacheRecordID(r)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.byID[id]; !exists {
		// New record — enforce cap before writing.
		if c.max > 0 && len(c.byID) >= c.max {
			c.evictOldestLocked()
		}
		c.order = append(c.order, id)
	}
	c.byID[id] = r
}

// evictOldestLocked drops the FIFO-head record. Caller must
// hold c.mu. The head may have already been removed by a prior
// Remove; skip ahead until we find a live entry or the queue
// empties.
func (c *RecordCache) evictOldestLocked() {
	for len(c.order) > 0 {
		head := c.order[0]
		c.order = c.order[1:]
		if _, ok := c.byID[head]; ok {
			delete(c.byID, head)
			return
		}
		// Already-removed entry in the queue — skip.
	}
}

// Remove deletes a record by its ID. No-op if absent.
func (c *RecordCache) Remove(id [32]byte) {
	c.mu.Lock()
	delete(c.byID, id)
	// Leave c.order alone — evictOldestLocked tolerates stale
	// queue entries. Periodically rebuilding the queue would
	// cost more than the occasional skip.
	c.mu.Unlock()
}

// PruneOlderThan removes every record whose T field is less
// than `since`. Returns the count of records dropped. Safe for
// concurrent callers.
func (c *RecordCache) PruneOlderThan(since int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	dropped := 0
	for id, r := range c.byID {
		if r.T < since {
			delete(c.byID, id)
			dropped++
		}
	}
	return dropped
}

// RemoveByRecord is a convenience: compute the ID for r and delete.
func (c *RecordCache) RemoveByRecord(r LocalRecord) {
	c.Remove(cacheRecordID(r))
}

// Len returns the current record count.
func (c *RecordCache) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byID)
}

// Get returns the record for the given ID, if present.
func (c *RecordCache) Get(id [32]byte) (LocalRecord, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.byID[id]
	return r, ok
}

// LocalRecords implements RecordSource. Returns the set of
// records matching the filter. A zero-value filter returns every
// record in the cache.
//
// Filter fields:
//   - Pubkeys: when non-empty, records MUST be authored by one
//     of the listed 32-byte pubkeys.
//   - Since: when > 0, records MUST have T >= since.
//   - Prefix: when non-empty, records MUST have Kw starting
//     with the UTF-8 prefix.
//
// All conditions conjunct. An error is never returned today —
// the signature exists so a future disk-backed implementation
// can surface I/O failures without an interface change.
func (c *RecordCache) LocalRecords(filter SyncFilter) ([]LocalRecord, error) {
	pubkeySet := toPubkeySet(filter.Pubkeys)

	c.mu.RLock()
	out := make([]LocalRecord, 0, len(c.byID))
	for _, r := range c.byID {
		if !matchFilter(r, pubkeySet, filter) {
			continue
		}
		out = append(out, r)
	}
	c.mu.RUnlock()
	return out, nil
}

// Snapshot is a lock-free iteration helper that returns every
// record currently in the cache. Prefer LocalRecords when a
// filter is available — Snapshot pays no filter cost so a large
// cache produces a large slice.
func (c *RecordCache) Snapshot() []LocalRecord {
	c.mu.RLock()
	out := make([]LocalRecord, 0, len(c.byID))
	for _, r := range c.byID {
		out = append(out, r)
	}
	c.mu.RUnlock()
	return out
}

// matchFilter applies a SyncFilter to a single record. Encapsulated
// so both LocalRecords and future streaming iterators share one
// canonical match rule.
func matchFilter(r LocalRecord, pubkeySet map[[32]byte]struct{}, f SyncFilter) bool {
	if len(pubkeySet) > 0 {
		if _, ok := pubkeySet[r.Pk]; !ok {
			return false
		}
	}
	if f.Since > 0 && r.T < f.Since {
		return false
	}
	if f.Prefix != "" && !strings.HasPrefix(r.Kw, f.Prefix) {
		return false
	}
	return true
}

// toPubkeySet decodes the filter's pubkey byte-slices into a
// lookup-friendly map. Invalid-length entries are silently
// skipped; the caller can't do anything useful with them anyway.
func toPubkeySet(pubkeys [][]byte) map[[32]byte]struct{} {
	if len(pubkeys) == 0 {
		return nil
	}
	out := make(map[[32]byte]struct{}, len(pubkeys))
	for _, pk := range pubkeys {
		if len(pk) != 32 {
			continue
		}
		var a [32]byte
		copy(a[:], pk)
		out[a] = struct{}{}
	}
	return out
}

// cacheRecordID derives the deterministic 32-byte key for a
// record. Matches localRecordID in sync_session.go — both must
// produce identical bytes for the same input or the same record
// would exist under two IDs, defeating deduplication. We
// duplicate the logic here instead of calling localRecordID to
// keep this file self-contained for future refactors.
func cacheRecordID(r LocalRecord) [32]byte {
	msg := make([]byte, 0, 32+len(r.Kw)+20+8)
	msg = append(msg, r.Pk[:]...)
	msg = append(msg, r.Kw...)
	msg = append(msg, r.Ih[:]...)
	var ts [8]byte
	binary.LittleEndian.PutUint64(ts[:], uint64(r.T))
	msg = append(msg, ts[:]...)
	return sha256.Sum256(msg)
}
