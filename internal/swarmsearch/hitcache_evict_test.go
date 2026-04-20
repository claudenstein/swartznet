package swarmsearch_test

import (
	"fmt"
	"testing"

	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// TestHitCacheStoreEvictsAtCapacity drives Store past the
// configured cap so evictOldest fires. With cap=2 and 3 distinct
// infohashes stored in order, the cache size is 2 and the
// oldest entry (the first stored) is gone.
func TestHitCacheStoreEvictsAtCapacity(t *testing.T) {
	t.Parallel()
	hc := swarmsearch.NewHitCache(2)

	mk := func(i int) swarmsearch.MergedHit {
		return swarmsearch.MergedHit{
			InfoHash: fmt.Sprintf("%040d", i),
			Name:     fmt.Sprintf("torrent-%d", i),
		}
	}
	hc.Store(mk(1))
	hc.Store(mk(2))
	hc.Store(mk(3)) // evict-oldest fires; entry 1 dropped

	if got := hc.Size(); got != 2 {
		t.Errorf("Size = %d, want 2 (cache cap)", got)
	}
	if _, ok := hc.Lookup(fmt.Sprintf("%040d", 1)); ok {
		t.Error("oldest entry should be evicted by Store at capacity")
	}
	if _, ok := hc.Lookup(fmt.Sprintf("%040d", 2)); !ok {
		t.Error("entry 2 should still be present")
	}
	if _, ok := hc.Lookup(fmt.Sprintf("%040d", 3)); !ok {
		t.Error("entry 3 should still be present")
	}
}
