package engine

import "testing"

// TestTorrentSnapshotsDeterministicOrder pins the round-8 fix (the root cause of
// the GUI wrong-torrent HIGH): TorrentSnapshots ranges a map (randomized
// iteration order), so it must sort deterministically — otherwise the list
// reshuffles every poll and an index-based consumer targets the wrong torrent.
func TestTorrentSnapshotsDeterministicOrder(t *testing.T) {
	e := testEngine(t)
	for i := byte(1); i <= 6; i++ {
		var h [20]byte
		for j := range h {
			h[j] = i
		}
		if _, err := e.AddInfoHash(h); err != nil {
			t.Fatal(err)
		}
	}

	first := e.TorrentSnapshots()
	if len(first) != 6 {
		t.Fatalf("got %d snapshots, want 6", len(first))
	}
	// Sorted ascending by infohash.
	for i := 1; i < len(first); i++ {
		if first[i-1].InfoHash > first[i].InfoHash {
			t.Fatalf("snapshots not sorted: %s before %s", first[i-1].InfoHash, first[i].InfoHash)
		}
	}
	// Stable across repeated calls despite map-iteration randomization.
	for k := 0; k < 8; k++ {
		next := e.TorrentSnapshots()
		if len(next) != len(first) {
			t.Fatalf("snapshot count changed: %d vs %d", len(next), len(first))
		}
		for i := range next {
			if next[i].InfoHash != first[i].InfoHash {
				t.Fatalf("order changed between calls at index %d: %s vs %s", i, next[i].InfoHash, first[i].InfoHash)
			}
		}
	}
}
