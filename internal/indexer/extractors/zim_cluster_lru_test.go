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

	c := newClusterLRU(2)
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

	c := newClusterLRU(2)
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
	c := newClusterLRU(0)
	c.put(1, []byte("a"))
	if _, ok := c.get(1); !ok {
		t.Fatal("a cap-floored LRU should still hold one entry")
	}
}
