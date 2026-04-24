package swarmsearch

import (
	"sync"
	"testing"
)

// mkCacheRecord builds a LocalRecord for cache tests.
func mkCacheRecord(pkByte byte, kw string, ihByte byte, ts int64) LocalRecord {
	var r LocalRecord
	r.Pk[0] = pkByte
	r.Kw = kw
	r.Ih[0] = ihByte
	r.T = ts
	return r
}

// cacheIDMatchesSessionID: the RecordCache must derive the same
// 32-byte key as sync_session.localRecordID, otherwise a record
// added to the cache would live under one ID and the session
// would look it up under another.
func TestCacheIDMatchesSessionID(t *testing.T) {
	r := mkCacheRecord(0xAA, "linux", 0x11, 1712649600)
	if cacheRecordID(r) != localRecordID(r) {
		t.Fatal("cacheRecordID / localRecordID diverged — de-dup would break")
	}
}

func TestRecordCacheAddGet(t *testing.T) {
	c := NewRecordCache()
	if c.Len() != 0 {
		t.Fatalf("new cache Len = %d, want 0", c.Len())
	}

	r := mkCacheRecord(0x01, "linux", 0x10, 1)
	c.Add(r)
	if c.Len() != 1 {
		t.Fatalf("after Add, Len = %d, want 1", c.Len())
	}

	got, ok := c.Get(cacheRecordID(r))
	if !ok {
		t.Fatal("Get missed a record we just stored")
	}
	if got.Kw != r.Kw || got.T != r.T {
		t.Errorf("Get returned mismatched record: %+v", got)
	}
}

func TestRecordCacheAddIdempotent(t *testing.T) {
	c := NewRecordCache()
	r := mkCacheRecord(0x01, "linux", 0x10, 1)
	c.Add(r)
	c.Add(r)
	c.Add(r)
	if c.Len() != 1 {
		t.Errorf("Add idempotency: Len = %d, want 1", c.Len())
	}
}

func TestRecordCacheRemove(t *testing.T) {
	c := NewRecordCache()
	r := mkCacheRecord(0x01, "linux", 0x10, 1)
	c.Add(r)
	c.Remove(cacheRecordID(r))
	if c.Len() != 0 {
		t.Errorf("after Remove, Len = %d, want 0", c.Len())
	}
	if _, ok := c.Get(cacheRecordID(r)); ok {
		t.Error("Get after Remove should miss")
	}
}

func TestRecordCacheRemoveByRecord(t *testing.T) {
	c := NewRecordCache()
	r := mkCacheRecord(0x02, "ubuntu", 0x20, 5)
	c.Add(r)
	c.RemoveByRecord(r)
	if c.Len() != 0 {
		t.Errorf("Len after RemoveByRecord = %d, want 0", c.Len())
	}
}

// LocalRecords with an empty filter returns every record.
func TestLocalRecordsEmptyFilter(t *testing.T) {
	c := NewRecordCache()
	for i := 0; i < 5; i++ {
		c.Add(mkCacheRecord(0x01, "linux", byte(i), int64(i)))
	}

	got, err := c.LocalRecords(SyncFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Errorf("len = %d, want 5", len(got))
	}
}

// Filter.Pubkeys narrows to exactly the listed publishers.
func TestLocalRecordsPubkeyFilter(t *testing.T) {
	c := NewRecordCache()
	c.Add(mkCacheRecord(0x01, "linux", 0x10, 1))
	c.Add(mkCacheRecord(0x02, "linux", 0x11, 2))
	c.Add(mkCacheRecord(0x02, "linux", 0x12, 3))

	var pkWanted [32]byte
	pkWanted[0] = 0x02
	got, err := c.LocalRecords(SyncFilter{Pubkeys: [][]byte{pkWanted[:]}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (only pk=0x02)", len(got))
	}
	for _, r := range got {
		if r.Pk[0] != 0x02 {
			t.Errorf("wrong pubkey in result: %x", r.Pk[0])
		}
	}
}

// Filter.Since drops records below the threshold.
func TestLocalRecordsSinceFilter(t *testing.T) {
	c := NewRecordCache()
	c.Add(mkCacheRecord(0x01, "a", 0x10, 100))
	c.Add(mkCacheRecord(0x01, "a", 0x11, 200))
	c.Add(mkCacheRecord(0x01, "a", 0x12, 300))

	got, _ := c.LocalRecords(SyncFilter{Since: 200})
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (T >= 200)", len(got))
	}
}

// Filter.Prefix drops records whose keyword doesn't start with it.
func TestLocalRecordsPrefixFilter(t *testing.T) {
	c := NewRecordCache()
	c.Add(mkCacheRecord(0x01, "linux", 0x10, 1))
	c.Add(mkCacheRecord(0x01, "ubuntu", 0x11, 2))
	c.Add(mkCacheRecord(0x01, "ubuntu-lts", 0x12, 3))

	got, _ := c.LocalRecords(SyncFilter{Prefix: "ubu"})
	if len(got) != 2 {
		t.Errorf("len = %d, want 2 (ubuntu, ubuntu-lts)", len(got))
	}
	for _, r := range got {
		if r.Kw != "ubuntu" && r.Kw != "ubuntu-lts" {
			t.Errorf("unexpected kw %q passed prefix filter", r.Kw)
		}
	}
}

// Multiple filters conjunct correctly.
func TestLocalRecordsCombinedFilters(t *testing.T) {
	c := NewRecordCache()
	var pkA, pkB [32]byte
	pkA[0] = 0x01
	pkB[0] = 0x02

	c.Add(LocalRecord{Pk: pkA, Kw: "linux", T: 100})
	c.Add(LocalRecord{Pk: pkA, Kw: "linux", T: 200, Ih: [20]byte{1}})
	c.Add(LocalRecord{Pk: pkB, Kw: "linux", T: 200, Ih: [20]byte{2}})
	c.Add(LocalRecord{Pk: pkA, Kw: "ubuntu", T: 200, Ih: [20]byte{3}})

	// Only pkA AND T >= 150 AND kw="linux*" → the middle record.
	got, _ := c.LocalRecords(SyncFilter{
		Pubkeys: [][]byte{pkA[:]},
		Since:   150,
		Prefix:  "lin",
	})
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1; got %+v", len(got), got)
	}
	if got[0].T != 200 || got[0].Kw != "linux" {
		t.Errorf("wrong record: %+v", got[0])
	}
}

// RecordCache must implement the RecordSource interface.
func TestRecordCacheImplementsRecordSource(t *testing.T) {
	var _ RecordSource = (*RecordCache)(nil)
}

// Invalid-length pubkeys in the filter are silently ignored; the
// caller-intended filter still applies to valid entries.
func TestLocalRecordsPubkeyFilterIgnoresShortEntries(t *testing.T) {
	c := NewRecordCache()
	c.Add(mkCacheRecord(0x01, "x", 0, 0))

	var pk [32]byte
	pk[0] = 0x01
	got, _ := c.LocalRecords(SyncFilter{
		Pubkeys: [][]byte{
			{0x01}, // too short — ignored
			pk[:],  // valid
		},
	})
	if len(got) != 1 {
		t.Errorf("len = %d, want 1 (short entry should be silently ignored)", len(got))
	}
}

// Concurrent reads while writes are happening must not race.
// go test -race will flag any data race on the internal map.
func TestRecordCacheConcurrency(t *testing.T) {
	c := NewRecordCache()
	const N = 100
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			c.Add(mkCacheRecord(0x01, "linux", byte(i), int64(i)))
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < N; i++ {
			_, _ = c.LocalRecords(SyncFilter{Prefix: "lin"})
			_ = c.Snapshot()
		}
	}()
	wg.Wait()
	if c.Len() != N {
		t.Errorf("after concurrent Add/Read, Len = %d, want %d", c.Len(), N)
	}
}

// Snapshot returns an independent slice — mutating the result
// must not affect subsequent calls.
func TestSnapshotIndependent(t *testing.T) {
	c := NewRecordCache()
	c.Add(mkCacheRecord(0x01, "linux", 0x10, 1))

	snap1 := c.Snapshot()
	if len(snap1) == 0 {
		t.Fatal("Snapshot returned empty slice")
	}
	snap1[0].Kw = "mutated"

	snap2 := c.Snapshot()
	if snap2[0].Kw == "mutated" {
		t.Error("mutation of Snapshot leaked back into the cache")
	}
}

// End-to-end: RecordCache attached as RecordSource drives a real
// sync_begin through the handler, confirming that records added
// via cache.Add become visible to sync peers.
func TestRecordCacheDrivesSyncBegin(t *testing.T) {
	p := New(nil)
	registerPeerWithServices(t, p, "peer-cache", BitSetReconciliation)

	cache := NewRecordCache()
	cache.Add(mkCacheRecord(0x11, "ubuntu", 0x01, 1))
	cache.Add(mkCacheRecord(0x11, "ubuntu", 0x02, 2))
	p.SetRecordSource(cache)

	begin := SyncBegin{TxID: 17}
	raw, _ := EncodeSyncBegin(begin)
	reply, last := captureReply()
	p.HandleMessage("peer-cache", raw, reply)

	body := last()
	hdr, _ := peekHeader(body)
	// With records in the cache, handler should reply SyncSymbols
	// (not the zero-record SyncEnd path).
	if hdr.MsgType != MsgTypeSyncSymbols {
		t.Errorf("msg_type = %d, want SyncSymbols — cache should have produced a batch", hdr.MsgType)
	}
}
