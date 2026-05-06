package gui

import (
	"testing"
)

// TestSelectedActionsEarlyReturns covers the
// `ih := dl.selectedInfoHash(); if ih == "" { return }` early-return
// arms in showFilesForSelected, toggleIndexSelected, pauseSelected,
// resumeSelected, and removeSelected. With selected = -1 (no row
// chosen) selectedInfoHash returns "" and each method exits before
// touching dl.d, so a daemon-less downloadsTab is sufficient.
//
// Asserts the *observable side effect* of the early return: the
// selectedInfoHash() guard reads the same lock the action paths
// would take, and selected stays unchanged. A regression that
// fell through to dereference dl.d would crash with a nil-pointer
// panic (which the t.Run + recover below would catch). The
// dl.snaps emptiness assertion locks in that no action mutated
// state.
func TestSelectedActionsEarlyReturns(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{selected: -1}

	if got := dl.selectedInfoHash(); got != "" {
		t.Fatalf("selectedInfoHash with selected=-1: got %q, want empty", got)
	}

	for _, action := range []struct {
		name string
		fn   func()
	}{
		{"showFilesForSelected", dl.showFilesForSelected},
		{"toggleIndexSelected", dl.toggleIndexSelected},
		{"pauseSelected", dl.pauseSelected},
		{"resumeSelected", dl.resumeSelected},
		{"removeSelected", dl.removeSelected},
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: panicked with selected=-1, dl.d=nil: %v", action.name, r)
				}
			}()
			action.fn()
		}()
	}

	if dl.selected != -1 {
		t.Fatalf("selected mutated: got %d, want -1", dl.selected)
	}
	if len(dl.snaps) != 0 {
		t.Fatalf("dl.snaps mutated: got %d entries, want 0", len(dl.snaps))
	}
}
