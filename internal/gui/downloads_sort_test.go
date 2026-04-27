package gui

import (
	"testing"

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
