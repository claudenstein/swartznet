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

func TestRemoveHitClearsLastHit(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")
	if _, err := mf.AddHit("solo", dhtindex.KeywordHit{IH: ihBytes(7), N: "only"}); err != nil {
		t.Fatal(err)
	}

	mf.RemoveHit("solo", ihBytes(7))

	// The entry stays in the map (RemoveHit only edits Hits), but its
	// Hits slice must be empty now.
	if got := len(mf.Snapshot()["solo"].Hits); got != 0 {
		t.Errorf("hits after removing the only hit = %d, want 0", got)
	}
}
