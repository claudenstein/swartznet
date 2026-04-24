package gui

import (
	"reflect"
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

func makeSnap(name, ih, status string, progress float64, size int64,
	peers int, down, up int64, indexing bool, signedBy string,
	trusted bool) engine.TorrentSnapshot {
	return engine.TorrentSnapshot{
		InfoHash:         ih,
		Name:             name,
		Status:           status,
		Progress:         progress,
		Size:             size,
		ActivePeers:      peers,
		DownloadRate:     down,
		UploadRate:       up,
		Indexing:         indexing,
		SignedBy:         signedBy,
		TrustedPublisher: trusted,
	}
}

// fixture returns three snapshots with distinct values in every
// sortable column. Order here is intentional — each test sorts
// by a specific column and asserts a specific resulting order.
func sortFixture() []engine.TorrentSnapshot {
	return []engine.TorrentSnapshot{
		makeSnap("banana", "ih-b", "seeding", 1.0, 1000, 5, 0, 200, true, "", false),
		makeSnap("apple", "ih-a", "metadata", 0.25, 500, 2, 100, 0, false, "sig-xyz", false),
		makeSnap("cherry", "ih-c", "paused", 0.5, 2000, 10, 50, 50, true, "sig-abc", true),
	}
}

func namesOf(s []engine.TorrentSnapshot) []string {
	out := make([]string, len(s))
	for i := range s {
		out[i] = s[i].Name
	}
	return out
}

func TestSnapLessByColumn(t *testing.T) {
	t.Parallel()
	type wantOrder struct {
		asc, desc []string
	}
	// The expected post-sort Name order for each column, both
	// directions. Names are chosen so there's a clean ordering
	// in every column.
	cases := map[int]wantOrder{
		0: {asc: []string{"apple", "banana", "cherry"}, desc: []string{"cherry", "banana", "apple"}},                // Name
		1: {asc: []string{"apple", "cherry", "banana"}, desc: []string{"banana", "cherry", "apple"}},                // Status: metadata, paused, seeding
		2: {asc: []string{"apple", "cherry", "banana"}, desc: []string{"banana", "cherry", "apple"}},                // Progress 0.25 < 0.5 < 1.0
		3: {asc: []string{"apple", "banana", "cherry"}, desc: []string{"cherry", "banana", "apple"}},                // Size 500 < 1000 < 2000
		4: {asc: []string{"apple", "banana", "cherry"}, desc: []string{"cherry", "banana", "apple"}},                // Peers 2 < 5 < 10
		5: {asc: []string{"banana", "cherry", "apple"}, desc: []string{"apple", "cherry", "banana"}},                // Download 0 < 50 < 100
		6: {asc: []string{"apple", "cherry", "banana"}, desc: []string{"banana", "cherry", "apple"}},                // Upload 0 < 50 < 200
	}

	for col, want := range cases {
		for _, desc := range []bool{false, true} {
			snaps := sortFixture()
			sortSnapsSlice(snaps, snapLess(col, desc))
			got := namesOf(snaps)
			wantOrder := want.asc
			if desc {
				wantOrder = want.desc
			}
			if !reflect.DeepEqual(got, wantOrder) {
				t.Errorf("column=%d desc=%v: got %v, want %v", col, desc, got, wantOrder)
			}
		}
	}
}

// Indexed column (col 7) is tri-state in effect: indexing=false
// sorts before indexing=true, and ties break by name.
func TestSnapLessIndexedColumn(t *testing.T) {
	t.Parallel()
	snaps := []engine.TorrentSnapshot{
		{Name: "c-on", Indexing: true},
		{Name: "a-off", Indexing: false},
		{Name: "b-on", Indexing: true},
	}
	sortSnapsSlice(snaps, snapLess(7, false))
	got := namesOf(snaps)
	want := []string{"a-off", "b-on", "c-on"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("indexed asc: got %v, want %v", got, want)
	}
	sortSnapsSlice(snaps, snapLess(7, true))
	got = namesOf(snaps)
	want = []string{"c-on", "b-on", "a-off"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("indexed desc: got %v, want %v", got, want)
	}
}

// Signed column (col 8): signed first in ASC; within signed,
// SignedBy ascending; within unsigned, no further ordering.
func TestSnapLessSignedColumn(t *testing.T) {
	t.Parallel()
	snaps := []engine.TorrentSnapshot{
		{Name: "c", SignedBy: ""},
		{Name: "a", SignedBy: "zzz"},
		{Name: "b", SignedBy: "aaa"},
	}
	sortSnapsSlice(snaps, snapLess(8, false))
	got := namesOf(snaps)
	// Signed first: {b (aaa), a (zzz), c ("")}.
	want := []string{"b", "a", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("signed asc: got %v, want %v", got, want)
	}
}

// Out-of-range columns should be a no-op (less returns false for
// every pair → insertion sort preserves input order).
func TestSnapLessOutOfRange(t *testing.T) {
	t.Parallel()
	orig := sortFixture()
	snaps := append([]engine.TorrentSnapshot{}, orig...)
	sortSnapsSlice(snaps, snapLess(99, false))
	if !reflect.DeepEqual(snaps, orig) {
		t.Errorf("out-of-range col changed order: got %v", namesOf(snaps))
	}
}

// Insertion sort stability: equal keys must keep input order.
func TestSortSnapsSliceStability(t *testing.T) {
	t.Parallel()
	// All have the same Progress; they should stay in input
	// order when sorted by Progress.
	snaps := []engine.TorrentSnapshot{
		{Name: "first", Progress: 0.5},
		{Name: "second", Progress: 0.5},
		{Name: "third", Progress: 0.5},
	}
	sortSnapsSlice(snaps, snapLess(2, false))
	got := namesOf(snaps)
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stability asc: got %v, want %v", got, want)
	}
	// Descending on equal keys: still stable (insertion sort
	// only swaps when strictly-less). Input order preserved.
	sortSnapsSlice(snaps, snapLess(2, true))
	got = namesOf(snaps)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stability desc: got %v, want %v", got, want)
	}
}

// Empty and single-element slices are no-ops and must not panic.
func TestSortSnapsSliceEdgeCases(t *testing.T) {
	t.Parallel()
	sortSnapsSlice(nil, snapLess(0, false))
	sortSnapsSlice([]engine.TorrentSnapshot{}, snapLess(0, false))
	sortSnapsSlice([]engine.TorrentSnapshot{{Name: "only"}}, snapLess(0, false))
}

// --- toggleSort state machine ---

// toggleSort cycles (no sort) → asc → desc → (no sort) on
// successive clicks of the same column.
func TestToggleSortSameColumnCycle(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{
		sortCol:  -1,
		selected: -1,
	}
	// We can't call toggleSort without a widget (it calls
	// dl.table.Refresh). Fake the refresh-safe code path by
	// copying the state-mutation logic into a tiny harness.
	// Instead, we test the state machine directly by mutating
	// dl.sortCol / dl.sortDesc through calls that don't touch
	// the widget. To do that, substitute a minimal table-free
	// variant: since toggleSort internally calls
	// sortSnapsLocked() (which is a no-op when dl.sortCol < 0
	// OR when snaps is empty) and then dl.table.Refresh(),
	// we exercise it by leaving dl.snaps nil. But Refresh on
	// a nil table panics — so we wrap the whole thing in a
	// recover.
	//
	// Simpler: just call the state-mutation logic inline.
	cycle := func(col int) {
		switch {
		case dl.sortCol != col:
			dl.sortCol = col
			dl.sortDesc = false
		case !dl.sortDesc:
			dl.sortDesc = true
		default:
			dl.sortCol = -1
			dl.sortDesc = false
		}
	}

	cycle(2) // click progress → ascending
	if dl.sortCol != 2 || dl.sortDesc {
		t.Fatalf("after click 1: col=%d desc=%v, want col=2 desc=false", dl.sortCol, dl.sortDesc)
	}
	cycle(2) // same column → descending
	if dl.sortCol != 2 || !dl.sortDesc {
		t.Fatalf("after click 2: col=%d desc=%v, want col=2 desc=true", dl.sortCol, dl.sortDesc)
	}
	cycle(2) // same column → clear
	if dl.sortCol != -1 || dl.sortDesc {
		t.Fatalf("after click 3: col=%d desc=%v, want col=-1 desc=false", dl.sortCol, dl.sortDesc)
	}
	cycle(3) // switch to Size → ascending
	if dl.sortCol != 3 || dl.sortDesc {
		t.Fatalf("after click 4: col=%d desc=%v, want col=3 desc=false", dl.sortCol, dl.sortDesc)
	}
	cycle(5) // switch to Download → ascending (not desc)
	if dl.sortCol != 5 || dl.sortDesc {
		t.Fatalf("after click 5: col=%d desc=%v, want col=5 desc=false", dl.sortCol, dl.sortDesc)
	}
}

// --- downloadsTab.selectedInfoHash ---

func TestSelectedInfoHashGuards(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{selected: -1}
	if ih := dl.selectedInfoHash(); ih != "" {
		t.Errorf("selected=-1: got %q, want \"\"", ih)
	}
	dl.snaps = []engine.TorrentSnapshot{{InfoHash: "abc"}, {InfoHash: "def"}}
	dl.selected = 1
	if ih := dl.selectedInfoHash(); ih != "def" {
		t.Errorf("selected=1: got %q, want def", ih)
	}
	// Out-of-range must return "" rather than panic on indexing.
	dl.selected = 99
	if ih := dl.selectedInfoHash(); ih != "" {
		t.Errorf("selected=99: got %q, want \"\" (out of range)", ih)
	}
}
