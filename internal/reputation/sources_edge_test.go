package reputation_test

import (
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestSourceTrackerRecordEmptyArgsNoop covers the documented
// "empty pubkey or empty infohash is silently ignored" guard.
func TestSourceTrackerRecordEmptyArgsNoop(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(8)

	st.Record("", "abcd") // empty infohash
	st.Record("1234", "") // empty pubkey
	st.Record("", "")     // both empty

	if got := st.Len(); got != 0 {
		t.Errorf("Len = %d, want 0 after empty-args Records", got)
	}
}

// TestSourceTrackerForgetEmptyAndUnknownNoop covers the two
// no-op branches of Forget: empty infohash + infohash not
// previously recorded.
func TestSourceTrackerForgetEmptyAndUnknownNoop(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(8)
	st.Record("1111", "abcd") // seed something

	st.Forget("")     // empty
	st.Forget("9999") // never recorded

	if got := st.Len(); got != 1 {
		t.Errorf("Len = %d, want 1 (the seed should still be there)", got)
	}

	// Now forget the real one for completeness.
	st.Forget("1111")
	if got := st.Len(); got != 0 {
		t.Errorf("Len = %d, want 0 after Forget('1111')", got)
	}
}
