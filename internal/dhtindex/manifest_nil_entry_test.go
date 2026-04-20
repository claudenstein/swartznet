package dhtindex_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/dhtindex"
)

// TestSnapshotSkipsNilEntries covers the defensive
// `if v == nil { continue }` branch in Snapshot. Production
// LoadOrCreateManifest normalises nil entries away, but the
// invariant is enforced defensively at every consumer because a
// hand-edited / truncated on-disk manifest can drop one in.
// Inject one directly via the exported Entries field and assert
// Snapshot returns the surviving real entry without panicking.
func TestSnapshotSkipsNilEntries(t *testing.T) {
	t.Parallel()
	m, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	if _, err := m.AddHit("real", dhtindex.KeywordHit{IH: ihBytes(7), N: "x"}); err != nil {
		t.Fatal(err)
	}
	// Hand-inject a nil entry the way a truncated on-disk manifest
	// would surface after Unmarshal.
	m.Entries["null-entry"] = nil

	snap := m.Snapshot()
	if _, ok := snap["null-entry"]; ok {
		t.Errorf("Snapshot leaked a nil-entry key into the output")
	}
	if _, ok := snap["real"]; !ok {
		t.Errorf("Snapshot dropped the real entry")
	}
}

// TestAddHitOnNilEntryRebuilds covers AddHit's
// `!ok || entry == nil` defensive branch. The entry exists in
// the map but its value is nil; AddHit must rebuild the entry
// rather than nil-panic.
func TestAddHitOnNilEntryRebuilds(t *testing.T) {
	t.Parallel()
	m, err := dhtindex.LoadOrCreateManifest("")
	if err != nil {
		t.Fatalf("LoadOrCreateManifest: %v", err)
	}
	m.Entries["broken"] = nil

	total, err := m.AddHit("broken", dhtindex.KeywordHit{IH: ihBytes(8), N: "y"})
	if err != nil {
		t.Fatalf("AddHit on nil entry: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 after rebuilding nil entry", total)
	}
	if m.Entries["broken"] == nil {
		t.Errorf("entry should have been rebuilt to a non-nil pointer")
	}
}
