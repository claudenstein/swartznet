package dhtindex_test

import (
	"bytes"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// ihBytes returns a 20-byte slice filled with v — handy for
// constructing distinguishable infohashes in tests.
func ihBytes(v byte) []byte { return bytes.Repeat([]byte{v}, 20) }

func TestRemoveHitDropsExisting(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	for i := 1; i <= 3; i++ {
		if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{IH: ihBytes(byte(i)), N: "x"}); err != nil {
			t.Fatalf("seed AddHit %d: %v", i, err)
		}
	}

	mf.RemoveHit("ubuntu", ihBytes(2))

	got := mf.Snapshot()["ubuntu"].Hits
	if len(got) != 2 {
		t.Fatalf("hits len = %d, want 2", len(got))
	}
	for _, h := range got {
		if bytes.Equal(h.IH, ihBytes(2)) {
			t.Errorf("RemoveHit did not drop infohash 0x02")
		}
	}
}

func TestRemoveHitUnknownKeywordNoop(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	// No panic, no state change.
	mf.RemoveHit("never-added", ihBytes(1))

	if got := len(mf.Snapshot()); got != 0 {
		t.Errorf("snapshot has %d entries after no-op remove, want 0", got)
	}
}

func TestRemoveHitUnknownInfohashNoop(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err != nil {
		t.Fatal(err)
	}

	mf.RemoveHit("ubuntu", ihBytes(99))

	got := mf.Snapshot()["ubuntu"].Hits
	if len(got) != 1 || !bytes.Equal(got[0].IH, ihBytes(1)) {
		t.Errorf("hits = %+v, want unchanged single 0x01 hit", got)
	}
}

func TestRemoveAllHitsScrubsEveryKeyword(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	// Two torrents tagged under three keywords. Removing torrent A
	// must scrub it from every entry it appears in, drop entries
	// where it was the only hit, and leave torrent B alone.
	a := dhtindex.KeywordHit{IH: ihBytes(0xAA), N: "a"}
	b := dhtindex.KeywordHit{IH: ihBytes(0xBB), N: "b"}
	for _, kw := range []string{"linux", "ubuntu"} {
		if _, err := mf.AddHit(kw, a); err != nil {
			t.Fatalf("seed AddHit(%s, A): %v", kw, err)
		}
		if _, err := mf.AddHit(kw, b); err != nil {
			t.Fatalf("seed AddHit(%s, B): %v", kw, err)
		}
	}
	if _, err := mf.AddHit("solo-a", a); err != nil {
		t.Fatalf("seed AddHit(solo-a, A): %v", err)
	}

	got := mf.RemoveAllHits(ihBytes(0xAA))
	if got != 3 {
		t.Errorf("touched = %d, want 3", got)
	}

	snap := mf.Snapshot()
	if _, ok := snap["solo-a"]; ok {
		t.Errorf("solo-a should have been dropped (now empty)")
	}
	for _, kw := range []string{"linux", "ubuntu"} {
		entry, ok := snap[kw]
		if !ok {
			t.Errorf("%s missing; A removed but B should remain", kw)
			continue
		}
		if len(entry.Hits) != 1 {
			t.Errorf("%s has %d hits, want 1 (only B)", kw, len(entry.Hits))
			continue
		}
		if !bytes.Equal(entry.Hits[0].IH, ihBytes(0xBB)) {
			t.Errorf("%s remaining hit IH = %x, want B (0xBB...)", kw, entry.Hits[0].IH)
		}
	}
}

func TestRemoveAllHitsEmptyInfohashNoop(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{IH: ihBytes(1), N: "x"}); err != nil {
		t.Fatal(err)
	}

	if got := mf.RemoveAllHits(nil); got != 0 {
		t.Errorf("empty-infohash touched = %d, want 0", got)
	}
	if got := len(mf.Snapshot()["ubuntu"].Hits); got != 1 {
		t.Errorf("entry was mutated by no-op; hits = %d, want 1", got)
	}
}

func TestRemoveHitClearsLastHit(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	if _, err := mf.AddHit("solo", dhtindex.KeywordHit{IH: ihBytes(7), N: "only"}); err != nil {
		t.Fatal(err)
	}

	mf.RemoveHit("solo", ihBytes(7))

	// Removing the last hit must drop the keyword from the manifest
	// entirely so refreshAll() doesn't keep re-publishing an empty
	// value forever, and the manifest can't grow unbounded over the
	// lifetime of a long-running publisher.
	snap := mf.Snapshot()
	if _, ok := snap["solo"]; ok {
		t.Errorf("emptied entry still present in manifest, want it dropped")
	}
}
