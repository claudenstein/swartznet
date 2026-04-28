package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSortFilesLockedDispatches covers each case of
// sortFilesLocked's `switch fd.sortBy` plus the priority rank
// table. Construct a filesDialog with synthetic FileSnapshots
// and verify the requested sort produces the expected ordering.
func TestSortFilesLockedDispatches(t *testing.T) {
	t.Parallel()
	mk := func(idx int, path string, length int64, prog float64, prio string) engine.FileSnapshot {
		return engine.FileSnapshot{
			Index: idx, DisplayPath: path, Length: length,
			Progress: prog, Priority: prio,
		}
	}
	src := []engine.FileSnapshot{
		mk(2, "c.txt", 300, 0.50, "high"),
		mk(0, "a.txt", 100, 0.25, "none"),
		mk(1, "b.txt", 200, 0.75, "normal"),
		mk(3, "d.txt", 50, 1.00, "weird"), // unknown priority → rank 1
	}

	cases := []struct {
		sort  string
		first int // expected Index of first element after sort
	}{
		{"path", 0},     // a.txt
		{"size", 3},     // size 50
		{"progress", 0}, // 0.25
		{"priority", 0}, // "none" → rank 0
		{"index", 0},    // index 0
		{"unknown", 0},  // default → index sort, index 0 first
	}
	for _, c := range cases {
		fd := &filesDialog{sortBy: c.sort}
		fd.files = append([]engine.FileSnapshot{}, src...)
		fd.sortFilesLocked()
		if got := fd.files[0].Index; got != c.first {
			t.Errorf("sortBy=%q first.Index = %d, want %d (files: %+v)",
				c.sort, got, c.first, fd.files)
		}
	}
}
