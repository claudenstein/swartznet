package gui

import (
	"testing"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSetAllPrioritiesEmptyFiles covers setAllPriorities at
// files_dialog.go:219-239 when fd.files is empty. The
// synchronous body snapshots an empty index list under
// lock, then spawns a goroutine whose for-loop body never
// executes, so fd.d.Eng.SetFilePriority is never called —
// daemon-free path. We join the goroutine deterministically via
// the afterSetAllPriorities seam instead of sleeping.
func TestSetAllPrioritiesEmptyFiles(t *testing.T) {
	wait := joinSetAllPriorities(t, 1)

	fd := &filesDialog{
		// d intentionally nil — goroutine never dereferences it
		// because indices is empty and the for-loop body skips.
	}
	fd.setAllPriorities(engine.FilePriorityNormal)
	wait()
}
