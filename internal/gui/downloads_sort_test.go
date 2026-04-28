package gui

import (
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSortSnapsLockedNegativeColIsNoop covers the
// `if dl.sortCol < 0 { return }` early-return at downloads.go:434.
func TestSortSnapsLockedNegativeColIsNoop(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{
		sortCol: -1,
		snaps: []engine.TorrentSnapshot{
			{Name: "b"}, {Name: "a"}, {Name: "c"},
		},
	}
	dl.sortSnapsLocked()
	// sortCol < 0 → no sort, original order preserved.
	if dl.snaps[0].Name != "b" || dl.snaps[2].Name != "c" {
		t.Errorf("sortSnapsLocked changed order with negative sortCol: %v", dl.snaps)
	}
}

// TestSortSnapsLockedAppliesColumnLess covers the success arm at
// downloads.go:437-438. Sort by column 0 (Name) ascending and
// verify alphabetical order.
func TestSortSnapsLockedAppliesColumnLess(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{
		sortCol:  0,
		sortDesc: false,
		snaps: []engine.TorrentSnapshot{
			{Name: "ubuntu"}, {Name: "alpine"}, {Name: "fedora"},
		},
	}
	dl.sortSnapsLocked()
	if dl.snaps[0].Name != "alpine" {
		t.Errorf("first after sort = %q, want %q", dl.snaps[0].Name, "alpine")
	}
}

// TestToggleSortStateMachine covers toggleSort's three-state
// cycle (asc → desc → cleared) plus the cross-column transition
// and the out-of-range early-return at downloads.go:412-413.
func TestToggleSortStateMachine(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()

	// Provide minimal table callbacks so Refresh doesn't log
	// "missing CreateCell/UpdateCell" warnings during the test.
	dl := &downloadsTab{
		sortCol: -1,
		table: widget.NewTable(
			func() (int, int) { return 0, 0 },
			func() fyne.CanvasObject { return widget.NewLabel("") },
			func(widget.TableCellID, fyne.CanvasObject) {},
		),
	}
	dl.toggleSort(0) // first click → asc on col 0
	if dl.sortCol != 0 || dl.sortDesc {
		t.Errorf("after first click: sortCol=%d sortDesc=%v, want 0/false", dl.sortCol, dl.sortDesc)
	}
	dl.toggleSort(0) // same col → desc
	if dl.sortCol != 0 || !dl.sortDesc {
		t.Errorf("after second click: sortCol=%d sortDesc=%v, want 0/true", dl.sortCol, dl.sortDesc)
	}
	dl.toggleSort(0) // same col desc → cleared
	if dl.sortCol != -1 {
		t.Errorf("after third click: sortCol=%d, want -1 (cleared)", dl.sortCol)
	}
	dl.toggleSort(1) // new col → asc
	if dl.sortCol != 1 || dl.sortDesc {
		t.Errorf("after click on different col: sortCol=%d sortDesc=%v, want 1/false", dl.sortCol, dl.sortDesc)
	}

	// Out-of-range → no-op (state preserved).
	dl.toggleSort(-1)
	if dl.sortCol != 1 {
		t.Errorf("toggleSort(-1) should be no-op; sortCol changed to %d", dl.sortCol)
	}
	dl.toggleSort(len(dlColumns) + 5)
	if dl.sortCol != 1 {
		t.Errorf("toggleSort(big) should be no-op; sortCol changed to %d", dl.sortCol)
	}
}
