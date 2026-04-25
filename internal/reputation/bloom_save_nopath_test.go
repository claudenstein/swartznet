package reputation_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBloomSaveNoPathIsNoOp — a freshly-constructed in-memory
// filter (no Path set) must Save cleanly without touching disk.
// LoadOrCreateBloom is the only public constructor that wires
// up a path; NewBloomFilter leaves it empty so callers that just
// want a memory-only filter for tests / experiments can Save()
// idempotently.
func TestBloomSaveNoPathIsNoOp(t *testing.T) {
	t.Parallel()
	bf := reputation.NewBloomFilter(64, 0.01)
	bf.Add([]byte("memory-only"))

	if err := bf.Save(); err != nil {
		t.Errorf("Save on path-less filter should be no-op, got %v", err)
	}
	// Confirm the filter still contains the item — Save must
	// not mutate state when there's nothing to persist.
	if !bf.Test([]byte("memory-only")) {
		t.Error("Save mutated the filter's contents")
	}
}
