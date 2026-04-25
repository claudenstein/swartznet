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

// TestAdmitWithTrackerAttached — admit takes the tracker
// branch (line 386-398) when one is wired up. The current
// implementation calls tracker.RecordReturned(pub, 0), which
// is a no-op because RecordReturned guards against n<=0 —
// the comment in admit notes this is a placeholder for a
// future "set score" API. We don't assert any tracker state
// change here; this test exists to exercise the branch
// itself so coverage tools see it executed.
func TestAdmitWithTrackerAttached(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	tracker := reputation.NewTracker()
	b, _ := NewBootstrap(lookup, nil, nil, tracker, DefaultBootstrapOptions(), nil)

	pub := pubkeyBytes("admit-tracker")
	if !b.admit(pub, "test-label", "anchor") {
		t.Fatal("admit failed")
	}
	if !b.IsAdmitted(pub) {
		t.Error("admitted set should contain pub after admit")
	}
}

// TestAdmitAnchorReputationBranch — admit picks
// AnchorReputation when source=="anchor" and
// CandidateReputation otherwise. The internal `rep` value
// is currently discarded (it's part of the same future
// "set score" placeholder as above) but the branch itself
// is real and worth exercising for both sides.
func TestAdmitAnchorReputationBranch(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	tracker := reputation.NewTracker()
	b, _ := NewBootstrap(lookup, nil, nil, tracker, DefaultBootstrapOptions(), nil)

	a := pubkeyBytes("admit-anchor-source")
	c := pubkeyBytes("admit-crawl-source")
	if !b.admit(a, "lbl-a", "anchor") {
		t.Error("admit anchor source failed")
	}
	if !b.admit(c, "lbl-c", "bep51") {
		t.Error("admit bep51 source failed")
	}
}
