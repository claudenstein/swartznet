package gui

import (
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/engine"
)

// TestSetAllPrioritiesEmptyFiles covers setAllPriorities at
// files_dialog.go:219-239 when fd.files is empty. The
// synchronous body snapshots an empty index list under
// lock, then spawns a goroutine whose for-loop body never
// executes, so fd.d.Eng.SetFilePriority is never called —
// daemon-free path. We sleep briefly to let the goroutine
// run and exit before the test returns.
func TestSetAllPrioritiesEmptyFiles(t *testing.T) {
	t.Parallel()
	fd := &filesDialog{
		// d intentionally nil — goroutine never dereferences it
		// because indices is empty and the for-loop body skips.
	}
	fd.setAllPriorities(engine.FilePriorityNormal)
	// Give the goroutine a moment to run-and-return.
	time.Sleep(50 * time.Millisecond)
}
