package testlab_test

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/testlab"
)

// TestScenarioCacheHitAcrossQueries verifies the M16c BIP-152-
// style hit cache over real peer-wire connections. The first
// sn_search query for "ubuntu" populates the Protocol's
// HitCache; the second query for the same keyword should find
// the infohash already cached (cache.Size() unchanged or
// incremented only for genuinely new hits).
//
// This validates that the cache Store() call in Query's collect
// path fires correctly through the full Protocol → merge →
// HitCache pipeline.
func TestScenarioCacheHitAcrossQueries(t *testing.T) {
	c := testlab.NewCluster(t, 2)
	answerer := c.Nodes[1]
	querier := c.Nodes[0]

	// Seed the answerer with content.
	answerer.IndexTorrent(t, 0x01, "ubuntu 24.04 desktop amd64 iso")

	c.WireMesh(t)
	c.WaitAllHandshaked(t, 10*time.Second)
	time.Sleep(100 * time.Millisecond)

	cache := querier.Eng.SwarmSearch().HitCache()

	// Before any query: cache should be empty.
	if sz := cache.Size(); sz != 0 {
		t.Errorf("pre-query cache size = %d, want 0", sz)
	}

	// First query: populates the cache.
	resp1 := querier.SwarmQuery(t, "ubuntu")
	if resp1.Responded == 0 || len(resp1.Hits) == 0 {
		t.Fatalf("first query: responded=%d hits=%d, want >=1",
			resp1.Responded, len(resp1.Hits))
	}
	sizeAfter1 := cache.Size()
	if sizeAfter1 == 0 {
		t.Fatal("cache still empty after first query — Store not wired")
	}
	t.Logf("first query: %d hits, cache size %d", len(resp1.Hits), sizeAfter1)

	// Second query for the same keyword: same result set, but
	// the cache should already contain the infohashes.
	resp2 := querier.SwarmQuery(t, "ubuntu")
	if len(resp2.Hits) != len(resp1.Hits) {
		t.Errorf("second query: %d hits, want %d (same as first)",
			len(resp2.Hits), len(resp1.Hits))
	}
	sizeAfter2 := cache.Size()
	if sizeAfter2 != sizeAfter1 {
		t.Errorf("cache grew from %d to %d on second query — expected same (cache hit path)",
			sizeAfter1, sizeAfter2)
	}
	t.Logf("second query: %d hits, cache size %d (unchanged = cache hit)", len(resp2.Hits), sizeAfter2)

	// Third query for a DIFFERENT keyword: no match → cache
	// should NOT grow (no new hits to store).
	resp3 := querier.SwarmQuery(t, "nonexistent keyword xyz")
	sizeAfter3 := cache.Size()
	if len(resp3.Hits) != 0 {
		t.Errorf("third query (nonexistent): %d hits, want 0", len(resp3.Hits))
	}
	if sizeAfter3 != sizeAfter2 {
		t.Errorf("cache grew from %d to %d on empty-result query",
			sizeAfter2, sizeAfter3)
	}
}
