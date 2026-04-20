package dhtindex_test

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAddIndexerReAddUpdatesLabel verifies the documented idempotent
// path: re-adding the same pubkey replaces the label but does NOT
// bump AddedAt.
func TestAddIndexerReAddUpdatesLabel(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(newScriptedGetter())
	pub := newPubkey(t)

	l.AddIndexer(pub, "first")
	first := l.Indexers()
	if len(first) != 1 {
		t.Fatalf("len(Indexers) = %d, want 1", len(first))
	}
	originalAddedAt := first[0].AddedAt

	l.AddIndexer(pub, "second")
	after := l.Indexers()
	if len(after) != 1 {
		t.Fatalf("len(Indexers) after re-add = %d, want still 1", len(after))
	}
	if after[0].Label != "second" {
		t.Errorf("Label = %q, want \"second\" (re-add did not update label)", after[0].Label)
	}
	if !after[0].AddedAt.Equal(originalAddedAt) {
		t.Errorf("AddedAt = %v, want unchanged %v (re-add must not bump AddedAt)",
			after[0].AddedAt, originalAddedAt)
	}
}

// TestAddIndexerHexInvalidHexBytes covers the
// previously-uncovered hex.DecodeString error branch in
// AddIndexerHex: a string of exactly 64 chars but non-hex bytes
// passes the length check and hits the decode call.
func TestAddIndexerHexInvalidHexBytes(t *testing.T) {
	t.Parallel()
	l := dhtindex.NewLookup(newScriptedGetter())

	// 64 chars exactly, but 'z' is not a hex digit.
	bad := strings.Repeat("z", 64)
	err := l.AddIndexerHex(bad, "should-not-add")
	if err == nil {
		t.Fatal("AddIndexerHex with non-hex bytes should error")
	}
	if len(l.Indexers()) != 0 {
		t.Errorf("indexer should not have been added; got %d", len(l.Indexers()))
	}
}
