package reputation_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestTrackerSaveNoPathIsNoOp covers Tracker.Save's
// `if t.path == "" { return nil }` early-return arm. An
// in-memory tracker constructed via NewTracker() has no
// backing file; Save must not error and must not create
// anything on disk.
func TestTrackerSaveNoPathIsNoOp(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	if err := tr.Save(); err != nil {
		t.Errorf("Save on in-memory tracker should be a no-op, got %v", err)
	}
}
