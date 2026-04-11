package reputation_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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
	tr.RecordReturned(pk(0x30), 1)            // unknown-ish

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

// TestTrackerSeededBypassesThreshold covers the M13c cold-start
// heavy-tail rule: a freshly-seeded pubkey with zero traffic
// should pass a MinIndexerScore threshold that a neutral unknown
// pubkey would fail.
func TestTrackerSeededBypassesThreshold(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()

	unknown := pk(0x70)
	seeded := pk(0x71)
	tr.MarkSeeded(seeded, "test-seed")

	// Unknown pubkey sits at the neutral prior (~0.5).
	if score := tr.Score(unknown); score < 0.4 || score > 0.6 {
		t.Errorf("unknown score = %.3f, want ~0.5", score)
	}
	// Fresh seed jumps to ~0.5 + SeedBonus = ~0.95.
	seededScore := tr.Score(seeded)
	if seededScore < 0.9 {
		t.Errorf("fresh seed score = %.3f, want ≥0.9", seededScore)
	}

	// With a 0.7 threshold cutoff, the seed must pass and the
	// unknown must fail.
	if tr.Threshold(unknown, 0.7) {
		t.Errorf("unknown incorrectly passed 0.7 threshold (score=%.3f)", tr.Score(unknown))
	}
	if !tr.Threshold(seeded, 0.7) {
		t.Errorf("seed failed 0.7 threshold (score=%.3f)", seededScore)
	}

	// IsSeeded and AnySeeded helpers.
	if !tr.IsSeeded(seeded) {
		t.Error("IsSeeded(seeded) returned false")
	}
	if tr.IsSeeded(unknown) {
		t.Error("IsSeeded(unknown) returned true")
	}
	if !tr.AnySeeded([]reputation.PubKeyHex{unknown, seeded}) {
		t.Error("AnySeeded with one seed returned false")
	}
	if tr.AnySeeded([]reputation.PubKeyHex{unknown}) {
		t.Error("AnySeeded with no seeds returned true")
	}
}

// TestTrackerSeedBonusDecays covers the 90-day exponential
// half-life: a seed imported long enough ago should converge
// toward its organic score rather than staying pinned at ~0.95.
// We can't sleep 90 days in a test, so we reach under the hood
// via a direct Records mutation to backdate SeededAt.
func TestTrackerSeedBonusDecays(t *testing.T) {
	t.Parallel()
	tr := reputation.NewTracker()
	seeded := pk(0x72)
	tr.MarkSeeded(seeded, "old-seed")

	// Fresh seed: score ≥ 0.9.
	if fresh := tr.Score(seeded); fresh < 0.9 {
		t.Errorf("fresh seed score = %.3f, want ≥0.9", fresh)
	}

	// Backdate SeededAt to 180 days ago (two half-lives).
	r := tr.Records[seeded]
	r.SeededAt = time.Now().Add(-2 * reputation.SeedHalfLife)

	// After two half-lives the bonus is 0.25× of SeedBonus ≈
	// 0.11, so organic (~0.5) + bonus (~0.11) = ~0.61.
	decayed := tr.Score(seeded)
	if decayed > 0.75 {
		t.Errorf("decayed seed score = %.3f, want ≤0.75 after 2 half-lives", decayed)
	}
	if decayed < 0.5 {
		t.Errorf("decayed seed score = %.3f, want still ≥0.5 (neutral floor)", decayed)
	}
}

// TestTrackerLoadSeedList exercises the JSON seed-list loader.
func TestTrackerLoadSeedList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "seeds.json")

	body := []byte(`{
		"version": 1,
		"seeds": [
			{"pubkey": "0101010101010101010101010101010101010101010101010101010101010101", "label": "alice"},
			{"pubkey": "0202020202020202020202020202020202020202020202020202020202020202", "label": "bob"},
			{"pubkey": "not-hex", "label": "junk"},
			{"pubkey": "03030303", "label": "too-short"}
		]
	}`)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	tr := reputation.NewTracker()
	n, errs := tr.LoadSeedList(path)
	if n != 2 {
		t.Errorf("imported = %d, want 2", n)
	}
	if len(errs) != 2 {
		t.Errorf("errs = %d, want 2 (bad hex + too-short)", len(errs))
	}

	if !tr.IsSeeded(reputation.PubKeyHex("0101010101010101010101010101010101010101010101010101010101010101")) {
		t.Error("alice not seeded after load")
	}

	// Missing file is not an error.
	n2, errs2 := tr.LoadSeedList(filepath.Join(dir, "does-not-exist.json"))
	if n2 != 0 || len(errs2) != 0 {
		t.Errorf("missing file: imported=%d errs=%v, want 0/nil", n2, errs2)
	}
}
