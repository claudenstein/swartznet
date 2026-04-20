package reputation_test

import (
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestSnapshotPubKeyTiebreak covers the
// `out[i].Score == out[j].Score → PubKey < PubKey` tiebreaker
// branch of Snapshot. Seed two records whose derived score
// collides (empty counters yield the default unknown-score
// for all of them) and assert PubKey-lexical ordering.
func TestSnapshotPubKeyTiebreak(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	a := reputation.PubKeyHex(strings.Repeat("a", 64))
	b := reputation.PubKeyHex(strings.Repeat("b", 64))
	// Insert in reverse order to guarantee the sort is actually
	// consulted.
	tr.Records[b] = &reputation.Counters{}
	tr.Records[a] = &reputation.Counters{}

	snap := tr.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len(snap) = %d, want 2", len(snap))
	}
	if snap[0].Score != snap[1].Score {
		t.Fatalf("scores not equal: %v vs %v; test premise broken",
			snap[0].Score, snap[1].Score)
	}
	if snap[0].PubKey != a || snap[1].PubKey != b {
		t.Errorf("order = [%q, %q], want [%q, %q] (lex tiebreaker)",
			snap[0].PubKey, snap[1].PubKey, a, b)
	}
}
