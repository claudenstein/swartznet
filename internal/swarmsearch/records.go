package swarmsearch

import (
	"strings"
	"sync"

	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/contracts/record"
)

// LocalRecord is a signed keyword→infohash record. It aliases the frozen
// contracts/record.Record so the ElementID/signature contracts have a single
// source of truth (swarmsearch imports the stdlib-only contract, not the
// engine/indexer/companion packages).
type LocalRecord = record.Record

// RecordSource supplies the local records a sync responder reconciles against
// (snapshotted once per sync_begin). nil = no records.
type RecordSource interface {
	LocalRecords(filter ltepwire.SyncFilter) ([]LocalRecord, error)
}

// RecordSink absorbs records decoded from a peer during sync.
type RecordSink interface {
	Add(r LocalRecord)
}

// PublisherObserver is notified once per distinct publisher pubkey seen in a
// valid synced record (feeds Layer-D indexer discovery in a later slice).
type PublisherObserver interface {
	NotePublisherSeen(pubkey [32]byte)
}

// RecordCache is an in-memory, FIFO-capped store of signed records. It serves
// as both the RecordSource and RecordSink. Safe for concurrent use.
type RecordCache struct {
	mu    sync.Mutex
	m     map[[32]byte]LocalRecord // keyed by record.ElementID
	order [][32]byte               // FIFO insertion order (may hold stale ids)
	max   int                      // 0 = unlimited
}

// NewRecordCache builds an empty cache (unlimited until SetMaxRecords).
func NewRecordCache() *RecordCache { return &RecordCache{m: make(map[[32]byte]LocalRecord)} }

// SetMaxRecords sets the cap (0 = unlimited). Lowering the cap does not
// proactively evict; the next Add drains toward the cap.
func (c *RecordCache) SetMaxRecords(n int) {
	c.mu.Lock()
	c.max = n
	c.mu.Unlock()
}

// MaxRecords returns the cap.
func (c *RecordCache) MaxRecords() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.max
}

// Len returns the record count.
func (c *RecordCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.m)
}

// Add inserts or overwrites a record. Idempotent on ElementID (a re-signed
// record replaces without a new order entry). At cap, evicts the FIFO head.
func (c *RecordCache) Add(r LocalRecord) {
	id := r.ElementID()
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.m[id]; exists {
		c.m[id] = r
		return
	}
	if c.max > 0 && len(c.m) >= c.max {
		for len(c.order) > 0 {
			head := c.order[0]
			c.order = c.order[1:]
			if _, live := c.m[head]; live {
				delete(c.m, head)
				break
			}
		}
	}
	c.m[id] = r
	c.order = append(c.order, id)
}

// Get returns a record by ElementID.
func (c *RecordCache) Get(id [32]byte) (LocalRecord, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.m[id]
	return r, ok
}

// Remove drops a record by ElementID.
func (c *RecordCache) Remove(id [32]byte) {
	c.mu.Lock()
	delete(c.m, id)
	c.mu.Unlock()
}

// PruneOlderThan drops every record with T strictly less than cutoff, returning
// how many were removed.
func (c *RecordCache) PruneOlderThan(cutoff int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for id, r := range c.m {
		if r.T < cutoff {
			delete(c.m, id)
			n++
		}
	}
	// Compact the FIFO order slice so it does not grow unbounded across
	// long-lived age-pruning (stale ids are otherwise only skipped at cap).
	if n > 0 {
		kept := c.order[:0]
		for _, id := range c.order {
			if _, live := c.m[id]; live {
				kept = append(kept, id)
			}
		}
		c.order = kept
	}
	return n
}

// Snapshot returns a copy of all records.
func (c *RecordCache) Snapshot() []LocalRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LocalRecord, 0, len(c.m))
	for _, r := range c.m {
		out = append(out, r)
	}
	return out
}

// LocalRecords returns the records matching filter (conjunctive; zero filter =
// all).
func (c *RecordCache) LocalRecords(filter ltepwire.SyncFilter) ([]LocalRecord, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LocalRecord, 0)
	for _, r := range c.m {
		if matchFilter(r, filter) {
			out = append(out, r)
		}
	}
	return out, nil
}

func matchFilter(r LocalRecord, f ltepwire.SyncFilter) bool {
	if len(f.Pubkeys) > 0 {
		found := false
		for _, pk := range f.Pubkeys {
			if len(pk) == 32 && [32]byte(pk) == r.Pk {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if f.Since > 0 && r.T < f.Since { // inclusive floor
		return false
	}
	if f.Prefix != "" && !strings.HasPrefix(r.Kw, f.Prefix) {
		return false
	}
	return true
}

// syncRecordWireSize is the SEMANTIC byte size used for budget accounting (NOT
// the bencoded size): 32(pk)+len(kw)+20(ih)+8(t)+8(pow)+64(sig).
func syncRecordWireSize(r LocalRecord) int { return 32 + len(r.Kw) + 20 + 8 + 8 + 64 }
