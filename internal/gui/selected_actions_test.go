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
func TestSelectedActionsEarlyReturns(t *testing.T) {
	t.Parallel()
	dl := &downloadsTab{selected: -1}

	dl.showFilesForSelected()
	dl.toggleIndexSelected()
	dl.pauseSelected()
	dl.resumeSelected()
	dl.removeSelected()
	// Each must return silently without dereferencing dl.d.
}
