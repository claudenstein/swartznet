package reputation_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

func TestSourceTrackerRecordAndQuery(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)

	st.Record("AABBCC", pk(0x10))
	st.Record("AABBCC", pk(0x20))

	got := st.Sources("aabbcc")
	if len(got) != 2 {
		t.Fatalf("len(sources) = %d, want 2", len(got))
	}
	if !slices.Contains(got, pk(0x10)) || !slices.Contains(got, pk(0x20)) {
		t.Errorf("sources = %v, want both pk(0x10) and pk(0x20)", got)
	}
}

func TestSourceTrackerCaseInsensitive(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)
	st.Record("AABBCC", pk(0x33))
	if st.Sources("aabbcc") == nil {
		t.Errorf("lookup with lowercase failed")
	}
	if st.Sources("AABBCC") == nil {
		t.Errorf("lookup with uppercase failed")
	}
}

func TestSourceTrackerDeduplicates(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)
	for i := 0; i < 5; i++ {
		st.Record("aaaa", pk(0x42))
	}
	got := st.Sources("aaaa")
	if len(got) != 1 {
		t.Errorf("expected 1 unique source, got %d: %v", len(got), got)
	}
}

func TestSourceTrackerRecordMany(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)
	st.RecordMany("xxx", []reputation.PubKeyHex{pk(0x01), pk(0x02), pk(0x03)})
	got := st.Sources("xxx")
	if len(got) != 3 {
		t.Errorf("len = %d, want 3", len(got))
	}
}

func TestSourceTrackerSourcesAbsent(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)
	if got := st.Sources("never-seen"); got != nil {
		t.Errorf("expected nil for absent infohash, got %v", got)
	}
}

func TestSourceTrackerForget(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)
	st.Record("ihaa", pk(0x01))
	st.Forget("ihaa")
	if got := st.Sources("ihaa"); got != nil {
		t.Errorf("Sources after Forget = %v, want nil", got)
	}
}

func TestSourceTrackerLRUEviction(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(3)
	st.Record("aaa", pk(0x01))
	st.Record("bbb", pk(0x02))
	st.Record("ccc", pk(0x03))
	if st.Len() != 3 {
		t.Fatalf("Len = %d, want 3", st.Len())
	}
	// Inserting a fourth should evict the oldest ("aaa") because
	// it has not been accessed since insertion.
	st.Record("ddd", pk(0x04))
	if st.Len() != 3 {
		t.Errorf("Len after eviction = %d, want 3", st.Len())
	}
	if st.Sources("aaa") != nil {
		t.Errorf("aaa should have been evicted")
	}
	for _, ih := range []string{"bbb", "ccc", "ddd"} {
		if st.Sources(ih) == nil {
			t.Errorf("%s should still be present", ih)
		}
	}
}

func TestSourceTrackerLRUTouchOnQuery(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(3)
	st.Record("aaa", pk(0x01))
	st.Record("bbb", pk(0x02))
	st.Record("ccc", pk(0x03))
	// Touching aaa via Sources should make it the MRU, so the next
	// insert should evict bbb instead.
	_ = st.Sources("aaa")
	st.Record("ddd", pk(0x04))
	if st.Sources("aaa") == nil {
		t.Errorf("aaa was evicted despite being touched")
	}
	if st.Sources("bbb") != nil {
		t.Errorf("bbb should have been evicted as the new oldest")
	}
}

func TestSourceTrackerEmptyArgs(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(0)
	st.Record("", pk(0x01)) // empty infohash → no-op
	st.Record("aaa", "")    // empty pubkey → no-op
	st.RecordMany("", nil)
	st.Forget("")
	if st.Sources("") != nil {
		t.Errorf("Sources of empty key should return nil")
	}
	if st.Len() != 0 {
		t.Errorf("Len = %d, want 0 after empty-arg ops", st.Len())
	}
}

func TestSourceTrackerLargeWorkload(t *testing.T) {
	t.Parallel()
	st := reputation.NewSourceTracker(100)
	const n = 1000
	for i := 0; i < n; i++ {
		ih := strings.Repeat("0", 30) + intToHex(i)
		st.Record(ih, pk(byte(i%5)))
	}
	if st.Len() > 100 {
		t.Errorf("Len = %d, exceeds capacity 100", st.Len())
	}
	// The last 100 inserts should still be present.
	for i := n - 100; i < n; i++ {
		ih := strings.Repeat("0", 30) + intToHex(i)
		if st.Sources(ih) == nil {
			t.Errorf("recent insert %d evicted unexpectedly", i)
		}
	}
}

func intToHex(i int) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 10)
	for j := 9; j >= 0; j-- {
		out[j] = hexdigits[i&0xf]
		i >>= 4
	}
	return string(out)
}
