package daemon

import (
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBootstrapIsPendingBranches walks all four IsPending
// states explicitly so a future refactor can't silently break
// the contract.
func TestBootstrapIsPendingBranches(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	endorsed := pubkeyBytes("endorsed-cand")
	observed := pubkeyBytes("observed-cand")
	admitted := pubkeyBytes("admitted-cand")
	unknown := pubkeyBytes("unknown-cand")
	endorser := pubkeyBytes("an-endorser")

	// Endorsement-only path.
	b.IngestEndorsement(endorser, endorsed)
	if !b.IsPending(endorsed) {
		t.Error("endorsed candidate must be pending")
	}

	// Observed-only path (sigValid=true but bloomPolicy is
	// false because no bloom/tracker, so it lands in observed
	// without being admitted).
	b.CandidateFromCrawl(observed, true)
	if !b.IsPending(observed) {
		t.Error("observed candidate must be pending")
	}

	// Admitted: the IsPending guard must short-circuit to false
	// even if the same pubkey is also in endorsements/observed.
	if !b.admit(admitted, "test-admit", "anchor") {
		t.Fatal("admit should succeed")
	}
	if b.IsPending(admitted) {
		t.Error("admitted candidate must NOT be pending")
	}

	// Unknown: never seen, never admitted.
	if b.IsPending(unknown) {
		t.Error("unknown candidate must NOT be pending")
	}
}

// TestCandidateFromCrawlAlreadyAdmittedShortCircuits — the
// hot path for re-receiving a CandidateFromCrawl signal for
// an already-admitted publisher: must return true without
// touching observed or running bloomPolicy again.
func TestCandidateFromCrawlAlreadyAdmittedShortCircuits(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	pub := pubkeyBytes("pre-admitted")
	if !b.admit(pub, "first-admit", "anchor") {
		t.Fatal("first admit failed")
	}
	if !b.CandidateFromCrawl(pub, true) {
		t.Error("CandidateFromCrawl should return true for already-admitted pubkey")
	}
	// Must NOT be added to observed (it's admitted, not pending).
	if b.IsPending(pub) {
		t.Error("already-admitted pubkey should not become pending")
	}
}

// TestCandidateFromCrawlAdmitsWhenBloomPolicyAllows — wires up
// a tracker so bloomPolicy returns true (defaultUnknownScore
// 0.5 ≥ 0.3 cutoff). The crawl observation is then admitted
// via the bep51 source label, exercising the
// "admit on bloomPolicy=true" branch.
func TestCandidateFromCrawlAdmitsWhenBloomPolicyAllows(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	bloom := reputation.NewBloomFilter(64, 0.01)
	tracker := reputation.NewTracker()
	b, _ := NewBootstrap(lookup, nil, bloom, tracker, DefaultBootstrapOptions(), nil)

	pub := pubkeyBytes("crawl-admit")
	if !b.CandidateFromCrawl(pub, true) {
		t.Error("CandidateFromCrawl should admit when bloomPolicy allows")
	}
	if !b.IsAdmitted(pub) {
		t.Error("post-CandidateFromCrawl: pub should be admitted")
	}
}

// TestAdmitAnchorMarksTrackerSeeded — admit with source="anchor"
// must MarkSeeded the pubkey on the tracker so the lookup's
// heavy-tail rule can identify trusted publishers and the
// scoreOf seeded-bonus branch starts the score near 1.0 (then
// decays organically over ~6 months).
func TestAdmitAnchorMarksTrackerSeeded(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	tracker := reputation.NewTracker()
	b, _ := NewBootstrap(lookup, nil, nil, tracker, DefaultBootstrapOptions(), nil)

	pub := pubkeyBytes("anchor-marks-seeded")
	if !b.admit(pub, "anchor-label", "anchor") {
		t.Fatal("admit failed")
	}
	pkHex := reputation.PubKey(pub)
	if !tracker.IsSeeded(pkHex) {
		t.Error("anchor admit must MarkSeeded the pubkey on the tracker")
	}
}

// TestAdmitCandidateDoesNotSeed — non-anchor sources (bep51,
// endorsement) must NOT be marked as seeded; they start at the
// neutral 0.5 default and earn/lose reputation through observed
// behavior, per the BootstrapOptions docstring.
func TestAdmitCandidateDoesNotSeed(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	tracker := reputation.NewTracker()
	b, _ := NewBootstrap(lookup, nil, nil, tracker, DefaultBootstrapOptions(), nil)

	for _, src := range []string{"bep51", "endorsement", "endorsed-bloom", "crawled"} {
		pub := pubkeyBytes("candidate-" + src)
		if !b.admit(pub, "lbl", src) {
			t.Errorf("admit src=%q failed", src)
			continue
		}
		if tracker.IsSeeded(reputation.PubKey(pub)) {
			t.Errorf("non-anchor source %q must NOT mark tracker as seeded", src)
		}
	}
}
