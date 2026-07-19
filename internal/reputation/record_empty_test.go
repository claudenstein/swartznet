package reputation_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestRecordConfirmedEmptyArgsNoop and TestRecordFlaggedEmptyArgsNoop
// pin the documented "no args, no work" fast-return on both
// counter-bumping helpers.
func TestRecordConfirmedEmptyArgsNoop(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()

	tr.RecordConfirmed()                            // no varargs
	tr.RecordConfirmed([]reputation.PubKeyHex{}...) // empty slice spread

	// Should not have created any phantom records.
	snap := tr.Snapshot()
	if len(snap) != 0 {
		t.Errorf("Snapshot has %d entries after empty-args RecordConfirmed; want 0", len(snap))
	}
}

func TestRecordFlaggedEmptyArgsNoop(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()

	tr.RecordFlagged()
	tr.RecordFlagged([]reputation.PubKeyHex{}...)

	snap := tr.Snapshot()
	if len(snap) != 0 {
		t.Errorf("Snapshot has %d entries after empty-args RecordFlagged; want 0", len(snap))
	}
}
