package engine

import (
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestGossipIndexerSinkRoutesToLookup is the production sink
// path: a peer_announce frame carrying a publisher pubkey gets
// routed via the swarmsearch IndexerSink → gossipIndexerSink →
// dhtindex.Lookup.AddIndexer. Without this routing, gossiped
// publishers never enter the lookup set and Layer-D queries
// silently miss them.
func TestGossipIndexerSinkRoutesToLookup(t *testing.T) {
	t.Parallel()
	lookup := dhtindex.NewLookup(nil)
	sink := &gossipIndexerSink{lookup: lookup}

	pub := [32]byte{0xAB, 0xCD}
	sink.NoteGossipIndexer(pub, "gossip:127.0.0.1:1234")

	indexers := lookup.Indexers()
	if len(indexers) != 1 {
		t.Fatalf("Indexers len = %d, want 1", len(indexers))
	}
	if indexers[0].PubKey != pub {
		t.Errorf("PubKey = %x, want %x", indexers[0].PubKey, pub)
	}
	if indexers[0].Label != "gossip:127.0.0.1:1234" {
		t.Errorf("Label = %q, want gossip:127.0.0.1:1234", indexers[0].Label)
	}

	// Re-call with a different label — AddIndexer is idempotent
	// and updates the label without bumping AddedAt or duplicating
	// the entry.
	sink.NoteGossipIndexer(pub, "gossip:10.0.0.1:99")
	indexers = lookup.Indexers()
	if len(indexers) != 1 {
		t.Errorf("after re-add Indexers len = %d, want 1 (idempotent)", len(indexers))
	}
	if indexers[0].Label != "gossip:10.0.0.1:99" {
		t.Errorf("after re-add Label = %q, want updated value", indexers[0].Label)
	}
}

// TestGossipIndexerSinkNilSafe — both nil-receiver and nil-lookup
// must short-circuit cleanly. Engine wiring guarantees neither
// happens in production, but a noisy peer message should never
// be able to reach a panic via this sink.
func TestGossipIndexerSinkNilSafe(t *testing.T) {
	t.Parallel()
	pub := [32]byte{0x01}

	// Nil receiver: must not panic.
	var nilSink *gossipIndexerSink
	nilSink.NoteGossipIndexer(pub, "x")

	// Nil lookup: same — short-circuit.
	sink := &gossipIndexerSink{lookup: nil}
	sink.NoteGossipIndexer(pub, "x")
}
