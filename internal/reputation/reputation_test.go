package reputation_test

import (
	"path/filepath"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

func pk(seed byte) reputation.PubKeyHex {
	var arr [32]byte
	for i := range arr {
		arr[i] = seed
	}
	return reputation.PubKey(arr)
}

func TestTrackerUnknownScoreIsNeutral(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	score := tr.Score(pk(0xaa))
	if score < 0.4 || score > 0.6 {
		t.Errorf("unknown score = %.3f, want ~0.5", score)
	}
}

func TestTrackerConfirmedRaisesScore(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	indexer := pk(0x01)
	tr.RecordReturned(indexer, 100)
	tr.RecordConfirmed(repeat(indexer, 80)...)
	score := tr.Score(indexer)
	// 80 out of 100 → ~0.8 raw, smoothed slightly down by the prior.
	if score < 0.7 || score > 0.85 {
		t.Errorf("good indexer score = %.3f, want ~0.75-0.82", score)
	}
}

func TestTrackerFlaggedLowersScore(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	indexer := pk(0x02)
	tr.RecordReturned(indexer, 100)
	tr.RecordFlagged(repeat(indexer, 50)...)
	score := tr.Score(indexer)
	// 0 confirmed, 50 flagged → raw 0, smoothed up toward 0.5.
	if score >= 0.5 {
		t.Errorf("bad indexer score = %.3f, want < 0.5", score)
	}
	if score >= 0.3 {
		t.Errorf("bad indexer score = %.3f, want clearly below 0.3", score)
	}
}

func TestTrackerLowVolumeStaysNearPrior(t *testing.T) {
	t.Parallel()
	// 1 returned + 1 confirmed should NOT shoot all the way to 1.0;
	// the Bayesian smoothing keeps it close to the neutral prior.
	tr := reputation.NewTracker()
	indexer := pk(0x03)
	tr.RecordReturned(indexer, 1)
	tr.RecordConfirmed(indexer)
	score := tr.Score(indexer)
	if score < 0.5 || score > 0.7 {
		t.Errorf("low-volume score = %.3f, want 0.5-0.7", score)
	}
}

func TestTrackerHighVolumePerfectScore(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	indexer := pk(0x04)
	tr.RecordReturned(indexer, 1000)
	tr.RecordConfirmed(repeat(indexer, 1000)...)
	score := tr.Score(indexer)
	if score < 0.95 {
		t.Errorf("high-volume perfect score = %.3f, want > 0.95", score)
	}
}

func TestTrackerSnapshotSortsByScore(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	tr.RecordReturned(pk(0x10), 50)
	tr.RecordConfirmed(repeat(pk(0x10), 40)...) // good
	tr.RecordReturned(pk(0x20), 50)
	tr.RecordFlagged(repeat(pk(0x20), 30)...) // bad
	tr.RecordReturned(pk(0x30), 1) // unknown-ish

	snap := tr.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("snap len = %d, want 3", len(snap))
	}
	for i := 1; i < len(snap); i++ {
		if snap[i].Score > snap[i-1].Score {
			t.Errorf("snapshot not sorted by score desc: %.3f > %.3f at %d",
				snap[i].Score, snap[i-1].Score, i)
		}
	}
	if snap[0].PubKey != pk(0x10) {
		t.Errorf("top entry pubkey = %s, want pk(0x10)", snap[0].PubKey)
	}
}

func TestTrackerThreshold(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	good := pk(0x40)
	bad := pk(0x41)
	tr.RecordReturned(good, 100)
	tr.RecordConfirmed(repeat(good, 90)...)
	tr.RecordReturned(bad, 100)
	tr.RecordFlagged(repeat(bad, 90)...)

	if !tr.Threshold(good, 0.7) {
		t.Errorf("good indexer below 0.7 threshold (score=%.3f)", tr.Score(good))
	}
	if tr.Threshold(bad, 0.5) {
		t.Errorf("bad indexer above 0.5 threshold (score=%.3f)", tr.Score(bad))
	}
	// cutoff 0 always passes
	if !tr.Threshold(bad, 0) {
		t.Error("cutoff 0 should always pass")
	}
}

func TestTrackerPersistRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "reputation.json")

	tr, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatal(err)
	}
	indexer := pk(0x50)
	tr.RecordReturned(indexer, 10)
	tr.RecordConfirmed(repeat(indexer, 8)...)
	if err := tr.Save(); err != nil {
		t.Fatal(err)
	}

	reopened, err := reputation.LoadOrCreateTracker(path)
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.Score(indexer)
	want := tr.Score(indexer)
	if got != want {
		t.Errorf("reopened score = %.5f, want %.5f", got, want)
	}
}

func TestTrackerRecordReturnedZeroNoOp(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	tr.RecordReturned(pk(0x60), 0)
	if len(tr.Snapshot()) != 0 {
		t.Errorf("expected empty tracker after zero-n RecordReturned")
	}
}

// repeat returns a slice with pk repeated n times. Used to make
// the test bodies read more like English ("flag this hit 80 times").
func repeat(pk reputation.PubKeyHex, n int) []reputation.PubKeyHex {
	out := make([]reputation.PubKeyHex, n)
	for i := range out {
		out[i] = pk
	}
	return out
}
