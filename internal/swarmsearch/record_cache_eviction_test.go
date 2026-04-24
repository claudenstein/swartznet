package swarmsearch

import (
	"testing"
)

// mkRec produces a LocalRecord that hashes to a distinct ID for
// each unique (kwIdx, ihIdx, ts) triple. Reuses pk so tests can
// vary just what matters.
func mkRec(kw string, ihByte byte, ts int64) LocalRecord {
	var r LocalRecord
	r.Pk[0] = 0x11
	r.Kw = kw
	r.Ih[0] = ihByte
	r.T = ts
	return r
}

// SetMaxRecords(0) leaves the cache unbounded — adding
// arbitrarily many records keeps them all.
func TestRecordCacheUnbounded(t *testing.T) {
	c := NewRecordCache()
	if c.MaxRecords() != 0 {
		t.Fatalf("default MaxRecords = %d, want 0", c.MaxRecords())
	}
	for i := 0; i < 100; i++ {
		c.Add(mkRec("k", byte(i), int64(i)))
	}
	if c.Len() != 100 {
		t.Errorf("Len = %d, want 100", c.Len())
	}
}

// Setting MaxRecords to N caps the cache at N. Adding N+k
// records evicts the oldest k.
func TestRecordCacheFIFOEviction(t *testing.T) {
	c := NewRecordCache()
	c.SetMaxRecords(3)

	r1 := mkRec("k", 0x01, 1)
	r2 := mkRec("k", 0x02, 2)
	r3 := mkRec("k", 0x03, 3)
	r4 := mkRec("k", 0x04, 4)

	c.Add(r1)
	c.Add(r2)
	c.Add(r3)
	if c.Len() != 3 {
		t.Fatalf("Len after 3 adds = %d, want 3", c.Len())
	}
	// Fourth add evicts r1 (oldest-inserted).
	c.Add(r4)
	if c.Len() != 3 {
		t.Fatalf("Len after 4th add = %d, want 3 (cap enforced)", c.Len())
	}

	if _, ok := c.Get(cacheRecordID(r1)); ok {
		t.Error("r1 should be evicted (oldest)")
	}
	for _, want := range []LocalRecord{r2, r3, r4} {
		if _, ok := c.Get(cacheRecordID(want)); !ok {
			t.Errorf("record %v missing after eviction round", want.Ih[0])
		}
	}
}

// Re-adding an existing record doesn't consume a new cap slot.
func TestRecordCacheIdempotentAddDoesntEvict(t *testing.T) {
	c := NewRecordCache()
	c.SetMaxRecords(2)

	r1 := mkRec("k", 0x01, 1)
	r2 := mkRec("k", 0x02, 2)

	c.Add(r1)
	c.Add(r2)
	// Re-add r1 — same ID, should NOT trigger eviction.
	c.Add(r1)
	if c.Len() != 2 {
		t.Errorf("Len after re-add = %d, want 2", c.Len())
	}
	if _, ok := c.Get(cacheRecordID(r2)); !ok {
		t.Error("r2 was incorrectly evicted by idempotent re-add of r1")
	}
}

// Lowering the cap below Len doesn't proactively evict — the
// next Add does. Documented behavior.
func TestRecordCacheSetMaxRecordsLazyEvict(t *testing.T) {
	c := NewRecordCache()

	for i := 0; i < 5; i++ {
		c.Add(mkRec("k", byte(i), int64(i)))
	}

	c.SetMaxRecords(2)
	if c.Len() != 5 {
		t.Errorf("lowering cap should not proactively evict; Len = %d, want 5", c.Len())
	}

	// Next Add triggers one eviction per slot over cap: we need
	// to drop 4 records to fit 5+1 into cap=2. Current impl
	// evicts one per Add call; adding one more drops one.
	c.Add(mkRec("k", 0xFF, 999))
	if c.Len() > 5 {
		t.Errorf("Len after one post-shrink Add = %d, shouldn't grow", c.Len())
	}
	// Cap is now 2 but after one Add we're at 5 (first eviction
	// made room for the new one, net same). Callers that want
	// eager eviction should use Remove directly.
}

// PruneOlderThan drops every record with T < since, returns the
// count dropped.
func TestRecordCachePruneOlderThan(t *testing.T) {
	c := NewRecordCache()
	c.Add(mkRec("k", 0x01, 100))
	c.Add(mkRec("k", 0x02, 200))
	c.Add(mkRec("k", 0x03, 300))
	c.Add(mkRec("k", 0x04, 400))

	dropped := c.PruneOlderThan(250)
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2 (T=100 and T=200)", dropped)
	}
	if c.Len() != 2 {
		t.Errorf("Len after prune = %d, want 2", c.Len())
	}

	// Idempotent — pruning again drops nothing.
	if dropped2 := c.PruneOlderThan(250); dropped2 != 0 {
		t.Errorf("second prune dropped %d, want 0", dropped2)
	}
}

// Prune + cap interaction: after pruning, the next Add refills
// cleanly without phantom eviction.
func TestRecordCachePruneThenAdd(t *testing.T) {
	c := NewRecordCache()
	c.SetMaxRecords(5)
	for i := 0; i < 5; i++ {
		c.Add(mkRec("k", byte(i), int64(i)))
	}

	c.PruneOlderThan(3) // drops records with T=0,1,2 → 2 left
	if c.Len() != 2 {
		t.Fatalf("Len after prune = %d, want 2", c.Len())
	}

	// Now we can Add 3 more without eviction because cap is 5.
	c.Add(mkRec("k", 0x10, 10))
	c.Add(mkRec("k", 0x11, 11))
	c.Add(mkRec("k", 0x12, 12))
	if c.Len() != 5 {
		t.Errorf("Len = %d, want 5 after prune+refill", c.Len())
	}

	// One more Add triggers cap-eviction.
	c.Add(mkRec("k", 0x13, 13))
	if c.Len() != 5 {
		t.Errorf("Len = %d after 6th add, want 5 (cap enforced)", c.Len())
	}
}
