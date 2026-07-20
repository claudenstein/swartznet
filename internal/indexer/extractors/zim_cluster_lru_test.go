package extractors

import (
	"bytes"
	"testing"
)

// TestClusterLRUEvictsLeastRecentlyUsed pins the LRU contract that
// replaced the old random "drop half" eviction: when the cache is at
// capacity, inserting a new key evicts the genuinely least-recently
// *accessed* entry, not an arbitrary one.
func TestClusterLRUEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	c := newClusterLRU(2, 1<<30)
	c.put(1, []byte("one"))
	c.put(2, []byte("two"))

	// Touch key 1 so key 2 becomes the LRU victim.
	if _, ok := c.get(1); !ok {
		t.Fatal("key 1 should be present after put")
	}

	// Inserting key 3 must evict key 2 (least recently used), keep 1.
	c.put(3, []byte("three"))

	if _, ok := c.get(2); ok {
		t.Error("key 2 should have been evicted as least-recently-used")
	}
	if v, ok := c.get(1); !ok || !bytes.Equal(v, []byte("one")) {
		t.Errorf("key 1 should survive eviction, got %q ok=%v", v, ok)
	}
	if v, ok := c.get(3); !ok || !bytes.Equal(v, []byte("three")) {
		t.Errorf("key 3 should be present, got %q ok=%v", v, ok)
	}
}

// TestClusterLRURepeatKeyRefreshes verifies that re-putting an existing
// key updates its value and does not grow the cache or evict anything.
func TestClusterLRURepeatKeyRefreshes(t *testing.T) {
	t.Parallel()

	c := newClusterLRU(2, 1<<30)
	c.put(1, []byte("v1"))
	c.put(1, []byte("v2"))
	c.put(2, []byte("two"))

	if v, ok := c.get(1); !ok || !bytes.Equal(v, []byte("v2")) {
		t.Errorf("repeat put should refresh value, got %q ok=%v", v, ok)
	}
	if _, ok := c.get(2); !ok {
		t.Error("key 2 should be present — repeat put must not have evicted it")
	}
	if c.ll.Len() != 2 {
		t.Errorf("cache size = %d, want 2", c.ll.Len())
	}
}

// TestClusterLRUCapacityFloor guards the capacity<1 normalisation.
func TestClusterLRUCapacityFloor(t *testing.T) {
	t.Parallel()
	c := newClusterLRU(0, 1<<30)
	c.put(1, []byte("a"))
	if _, ok := c.get(1); !ok {
		t.Fatal("a cap-floored LRU should still hold one entry")
	}
}

// TestClusterLRUBoundsAggregateBytes pins the round-8 fix: eviction must respect
// an aggregate BYTE budget, not just the entry count. A crafted .zim with many
// large clusters previously pinned count×maxClusterBytes (32 × 64 MiB ≈ 2 GiB);
// the byte budget caps the total resident memory regardless of the count cap.
func TestClusterLRUBoundsAggregateBytes(t *testing.T) {
	t.Parallel()
	// High count cap (100) but a 10 MiB byte budget: 6 × 4 MiB clusters exceed it.
	const budget = 10 << 20
	c := newClusterLRU(100, budget)
	blob := make([]byte, 4<<20) // 4 MiB per cluster
	for k := uint32(0); k < 6; k++ {
		c.put(k, blob)
	}
	if c.curBytes > budget {
		t.Errorf("resident bytes %d exceed budget %d — aggregate not bounded", c.curBytes, budget)
	}
	// At most floor(10/4)=2 clusters resident.
	if c.ll.Len() > 2 {
		t.Errorf("cache holds %d clusters (%d MiB); byte budget should keep <=2", c.ll.Len(), c.curBytes>>20)
	}
	// The most-recently-put key survives.
	if _, ok := c.get(5); !ok {
		t.Error("most-recent cluster evicted")
	}
}

// TestClusterLRUAdmitsSingleOversizedEntry: an entry alone larger than the budget
// is still admitted (we need it for the current article) and does not loop.
func TestClusterLRUAdmitsSingleOversizedEntry(t *testing.T) {
	t.Parallel()
	c := newClusterLRU(32, 1<<20) // 1 MiB budget
	big := make([]byte, 4<<20)    // 4 MiB > budget
	c.put(1, big)
	if _, ok := c.get(1); !ok {
		t.Fatal("oversized-but-needed entry must still be cached")
	}
	if c.ll.Len() != 1 {
		t.Errorf("expected exactly 1 entry, got %d", c.ll.Len())
	}
}
