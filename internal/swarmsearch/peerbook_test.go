package swarmsearch

import (
	"testing"
)

func TestPeerBookAddNewAndPromote(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(10, 100)

	b.AddNew("peer:1")
	b.AddNew("peer:2")
	if b.NewCount() != 2 {
		t.Errorf("NewCount = %d, want 2", b.NewCount())
	}
	if b.TriedCount() != 0 {
		t.Errorf("TriedCount = %d, want 0", b.TriedCount())
	}

	// Promote peer:1 to tried.
	b.Promote("peer:1")
	if b.NewCount() != 1 {
		t.Errorf("NewCount after promote = %d, want 1", b.NewCount())
	}
	if b.TriedCount() != 1 {
		t.Errorf("TriedCount after promote = %d, want 1", b.TriedCount())
	}
	if !b.IsTried("peer:1") {
		t.Error("peer:1 should be tried")
	}
	if b.IsTried("peer:2") {
		t.Error("peer:2 should still be new")
	}
}

func TestPeerBookPromoteIncrements(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(10, 100)
	b.AddNew("peer:1")
	b.Promote("peer:1")
	b.Promote("peer:1") // second call on an already-tried peer
	// Should still be tried (not duplicated).
	if b.TriedCount() != 1 {
		t.Errorf("TriedCount = %d after double promote, want 1", b.TriedCount())
	}
}

func TestPeerBookAddNewIdempotent(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(10, 100)
	b.AddNew("peer:1")
	b.AddNew("peer:1") // no-op
	if b.NewCount() != 1 {
		t.Errorf("NewCount = %d, want 1 (idempotent add)", b.NewCount())
	}

	b.Promote("peer:1")
	b.AddNew("peer:1") // should not demote from tried
	if !b.IsTried("peer:1") {
		t.Error("AddNew on a tried peer should not demote it")
	}
}

func TestPeerBookEvictionNew(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(10, 3) // tiny new table
	b.AddNew("a:1")
	b.AddNew("b:2")
	b.AddNew("c:3")
	b.AddNew("d:4") // should evict oldest = a:1
	if b.NewCount() != 3 {
		t.Errorf("NewCount = %d, want 3 after eviction", b.NewCount())
	}
	addrs := b.NewAddrs()
	for _, a := range addrs {
		if a == "a:1" {
			t.Error("oldest entry a:1 should have been evicted")
		}
	}
}

func TestPeerBookEvictionTried(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(2, 100) // tiny tried table
	b.AddNew("a:1")
	b.AddNew("b:2")
	b.AddNew("c:3")
	b.Promote("a:1")
	b.Promote("b:2")
	b.Promote("c:3") // should evict LRQ from tried
	if b.TriedCount() != 2 {
		t.Errorf("TriedCount = %d, want 2 after eviction", b.TriedCount())
	}
}

func TestPeerBookRemove(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(10, 100)
	b.AddNew("peer:1")
	b.Promote("peer:1")
	b.AddNew("peer:2")
	b.Remove("peer:1")
	b.Remove("peer:2")
	if b.TriedCount() != 0 || b.NewCount() != 0 {
		t.Errorf("tried=%d new=%d after remove, want 0/0",
			b.TriedCount(), b.NewCount())
	}
}

func TestPeerBookRecordFailure(t *testing.T) {
	t.Parallel()
	b := NewPeerBook(10, 100)
	b.AddNew("peer:1")
	b.RecordFailure("peer:1")
	// v1 doesn't demote on failure — just records the counter.
	// Assert the entry still exists.
	if b.NewCount() != 1 {
		t.Errorf("RecordFailure removed the entry")
	}
}
