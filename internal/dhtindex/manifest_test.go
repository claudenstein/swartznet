package dhtindex_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestAddHitReplaceRunsEviction verifies that replacing an existing
// hit with a larger one still triggers the size-cap eviction loop.
// The previous implementation returned early on replacement and
// could leave the entry above MaxValueBytes.
func TestAddHitReplaceRunsEviction(t *testing.T) {
	t.Parallel()
	mf, _ := dhtindex.LoadOrCreateManifest("")

	// Pre-fill with enough hits that a single oversize replacement
	// would push the entry past the cap.
	for i := 0; i < 5; i++ {
		ih := bytes.Repeat([]byte{byte(i + 1)}, 20)
		if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{
			IH: ih,
			N:  "baseline-hit",
		}); err != nil {
			t.Fatalf("seed AddHit %d: %v", i, err)
		}
	}
	snap := mf.Snapshot()["ubuntu"]
	baselineCount := len(snap.Hits)
	if baselineCount != 5 {
		t.Fatalf("seed count = %d, want 5", baselineCount)
	}

	// Replace hit #0 with a much larger payload. A 900-byte name
	// plus surrounding metadata overshoots MaxValueBytes (1000)
	// on its own, so AddHit must evict older hits to make room.
	bigName := string(bytes.Repeat([]byte{'a'}, 900))
	if _, err := mf.AddHit("ubuntu", dhtindex.KeywordHit{
		IH: bytes.Repeat([]byte{0x01}, 20),
		N:  bigName,
	}); err != nil {
		t.Fatalf("replace AddHit: %v", err)
	}

	snap = mf.Snapshot()["ubuntu"]
	encoded := dhtindex.EstimateValueSize(dhtindex.KeywordValue{Hits: snap.Hits})
	if encoded > dhtindex.MaxValueBytes {
		t.Errorf("encoded size %d > MaxValueBytes %d — eviction did not run on replace",
			encoded, dhtindex.MaxValueBytes)
	}
	if len(snap.Hits) >= baselineCount {
		t.Errorf("hit count after oversize replace = %d, want < %d (at least one eviction)",
			len(snap.Hits), baselineCount)
	}
}

// TestLoadOrCreateManifestDropsNullEntries verifies that a
// hand-edited manifest whose JSON contains a null-valued entry
// (e.g. `{"entries": {"foo": null}}`) loads without panicking
// and with the null key dropped from the in-memory map.
func TestLoadOrCreateManifestDropsNullEntries(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "manifest.json")
	// Write a manifest with one valid entry + one null entry.
	raw := `{"entries":{"good":{"hits":[],"last_published":"0001-01-01T00:00:00Z","publish_count":0},"bad":null}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	mf, err := dhtindex.LoadOrCreateManifest(path)
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	snap := mf.Snapshot()
	if _, ok := snap["bad"]; ok {
		t.Error("nil entry 'bad' should have been dropped during load")
	}
	if _, ok := snap["good"]; !ok {
		t.Error("valid entry 'good' should remain after load")
	}

	// Subsequent AddHit / MarkPublished on the formerly-null key
	// must not nil-panic — they should behave as "no-op" for the
	// mutators, and AddHit should create a fresh entry.
	if _, err := mf.AddHit("bad", dhtindex.KeywordHit{
		IH: bytes.Repeat([]byte{0x02}, 20),
		N:  "recovered",
	}); err != nil {
		t.Fatalf("AddHit after null-load: %v", err)
	}
	if got := mf.Snapshot()["bad"]; got == nil || len(got.Hits) != 1 {
		t.Errorf("AddHit on formerly-null key did not create entry: %+v", got)
	}
}
