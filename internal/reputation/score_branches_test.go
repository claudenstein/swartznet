package reputation_test

import (
	"strings"
	"testing"
	"time"

	"github.com/swartznet/swartznet/internal/reputation"
)

// makeTrackerWith returns a fresh tracker with a single record
// at the given pubkey. Bypasses the touch path so tests can pin
// every Counters field exactly.
func makeTrackerWith(t *testing.T, pk reputation.PubKeyHex, c reputation.Counters) *reputation.Tracker {
	t.Helper()
	tr := reputation.NewTracker()
	tr.Records[pk] = &c
	return tr
}

// TestScoreOfBranches drives every documented branch of scoreOf
// (via Score, which is just `scoreOf(touch(pk))` with the
// tracker lock):
//
//   - all-zero counters → defaultUnknownScore (0.5)
//   - hits_returned > 0, hits_confirmed = 0 → discounted bayesian
//   - hits_flagged > hits_confirmed → numerator-clamped-to-0 path
//   - SeededAt set, age = 0 → bonus added
//   - SeededAt set, age > 0 → exponentially-decayed bonus
//   - SeededAt set in the future → age clamped to 0
//   - organic > 1 → clamped to 1
func TestScoreOfBranches(t *testing.T) {
	t.Parallel()

	// All-zero counters → defaultUnknownScore (0.5).
	pkA := reputation.PubKeyHex(strings.Repeat("a", 64))
	tr := makeTrackerWith(t, pkA, reputation.Counters{})
	if got := tr.Score(pkA); got < 0.49 || got > 0.51 {
		t.Errorf("zero counters Score = %v, want ~0.5", got)
	}

	// HitsReturned > 0, HitsConfirmed = 0 → discounted (< 0.5).
	pkB := reputation.PubKeyHex(strings.Repeat("b", 64))
	tr = makeTrackerWith(t, pkB, reputation.Counters{HitsReturned: 100})
	if got := tr.Score(pkB); got >= 0.5 {
		t.Errorf("returned-only Score = %v, want < 0.5", got)
	}

	// HitsFlagged > HitsConfirmed → numerator clamps to 0.
	pkC := reputation.PubKeyHex(strings.Repeat("c", 64))
	tr = makeTrackerWith(t, pkC, reputation.Counters{
		HitsReturned:  10,
		HitsConfirmed: 1,
		HitsFlagged:   9,
	})
	if got := tr.Score(pkC); got < 0 || got > 0.5 {
		t.Errorf("flagged-dominant Score = %v, want in [0, 0.5]", got)
	}

	// SeededAt right now → bonus near SeedBonus.
	pkD := reputation.PubKeyHex(strings.Repeat("d", 64))
	tr = makeTrackerWith(t, pkD, reputation.Counters{SeededAt: time.Now()})
	if got := tr.Score(pkD); got < 0.5+reputation.SeedBonus*0.95 {
		t.Errorf("fresh-seed Score = %v, want >= 0.5 + ~SeedBonus", got)
	}

	// SeededAt in the past → decayed bonus, still above neutral.
	pkE := reputation.PubKeyHex(strings.Repeat("e", 64))
	tr = makeTrackerWith(t, pkE, reputation.Counters{
		SeededAt: time.Now().Add(-365 * 24 * time.Hour),
	})
	if got := tr.Score(pkE); got <= 0.5 || got >= 0.5+reputation.SeedBonus {
		t.Errorf("old-seed Score = %v, want in (0.5, 0.5+SeedBonus)", got)
	}

	// SeededAt in the future → age clamped to 0, full bonus.
	pkF := reputation.PubKeyHex(strings.Repeat("f", 64))
	tr = makeTrackerWith(t, pkF, reputation.Counters{
		SeededAt: time.Now().Add(24 * time.Hour),
	})
	if got := tr.Score(pkF); got < 0.5+reputation.SeedBonus*0.95 {
		t.Errorf("future-seed Score = %v, want full SeedBonus (age clamped to 0)", got)
	}

	// Confirmed-dominant + seeded → organic > 1 path clamps to 1.
	pkG := reputation.PubKeyHex("0000000000000000000000000000000000000000000000000000000000000007")
	tr = makeTrackerWith(t, pkG, reputation.Counters{
		HitsReturned:  100,
		HitsConfirmed: 100,
		SeededAt:      time.Now(),
	})
	if got := tr.Score(pkG); got < 0.99 || got > 1.0 {
		t.Errorf("confirmed+seed Score = %v, want clamped to 1.0", got)
	}
}
