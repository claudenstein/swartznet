package daemon

import (
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBootstrapAdmitCapEnforced — admit must reject the
// (MaxTrackedPublishers+1)-th candidate to keep the lookup
// set bounded. We use a low cap so the test stays cheap.
func TestBootstrapAdmitCapEnforced(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	opts.MaxTrackedPublishers = 2
	b, err := NewBootstrap(lookup, nil, nil, nil, opts, nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	c1 := pubkeyBytes("admit-cap-1")
	c2 := pubkeyBytes("admit-cap-2")
	c3 := pubkeyBytes("admit-cap-3")

	if !b.admit(c1, "label1", "anchor") {
		t.Fatal("admit c1 should succeed")
	}
	if !b.admit(c2, "label2", "anchor") {
		t.Fatal("admit c2 should succeed (still below cap)")
	}
	if b.admit(c3, "label3", "anchor") {
		t.Error("admit c3 should fail — cap=2 reached")
	}
	if got := b.AdmittedCount(); got != 2 {
		t.Errorf("AdmittedCount = %d, want 2", got)
	}
}

// TestBootstrapAdmitIdempotent — re-admitting the same
// pubkey returns true without growing the admitted set.
func TestBootstrapAdmitIdempotent(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	b, _ := NewBootstrap(lookup, nil, nil, nil, DefaultBootstrapOptions(), nil)

	pub := pubkeyBytes("idempotent-key")
	if !b.admit(pub, "first", "anchor") {
		t.Fatal("first admit failed")
	}
	if !b.admit(pub, "second", "anchor") {
		t.Error("re-admit must return true")
	}
	if got := b.AdmittedCount(); got != 1 {
		t.Errorf("AdmittedCount = %d, want 1", got)
	}
}

// TestBootstrapBloomPolicyTrackerKnowsPublisher — when the
// tracker is wired up at all, bloomPolicy admits any pubkey
// whose Score is ≥ 0.3. defaultUnknownScore is 0.5, so even
// fresh pubkeys clear the bar; this exercises the tracker-
// threshold branch (the cell line that says
// `tracker.Threshold(reputation.PubKey(cand), 0.3)` returns
// true).
func TestBootstrapBloomPolicyTrackerKnowsPublisher(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	bloom := reputation.NewBloomFilter(64, 0.01)
	tracker := reputation.NewTracker()

	b, err := NewBootstrap(lookup, nil, bloom, tracker, DefaultBootstrapOptions(), nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	pub := pubkeyBytes("any-publisher")
	if !b.bloomPolicy(pub) {
		t.Error("bloomPolicy should admit when tracker is wired (defaultUnknownScore=0.5 ≥ 0.3)")
	}
}

// TestBootstrapBloomPolicyBloomOnlyNoTracker — non-nil bloom
// but nil tracker reaches the second guard, which short-
// circuits to false (no tracker → can't evaluate threshold).
func TestBootstrapBloomPolicyBloomOnlyNoTracker(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	bloom := reputation.NewBloomFilter(64, 0.01)

	b, _ := NewBootstrap(lookup, nil, bloom, nil, DefaultBootstrapOptions(), nil)
	pub := pubkeyBytes("any-publisher")
	if b.bloomPolicy(pub) {
		t.Error("bloomPolicy with bloom but no tracker should return false")
	}
}

// TestBootstrapCountStrongEndorsersWithTracker — only
// endorsers whose tracker score crosses 0.5 contribute. We
// register two endorsers, score one high and the other low,
// and verify the count is 1.
func TestBootstrapCountStrongEndorsersWithTracker(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	tracker := reputation.NewTracker()
	b, _ := NewBootstrap(lookup, nil, nil, tracker, DefaultBootstrapOptions(), nil)

	cand := pubkeyBytes("c-cand")
	strong := pubkeyBytes("e-strong")
	weak := pubkeyBytes("e-weak")

	// Push strong endorser well above 0.5 by accumulating both
	// Returned and Confirmed counters (the score formula needs
	// good/returned to swing the Bayesian-smoothed mean above
	// the unknown prior of 0.5).
	tracker.RecordReturned(reputation.PubKey(strong), 200)
	confirmedKey := reputation.PubKey(strong)
	for i := 0; i < 200; i++ {
		tracker.RecordConfirmed(confirmedKey)
	}
	// Push weak endorser well below 0.5: lots of returned, lots
	// of flagged.
	tracker.RecordReturned(reputation.PubKey(weak), 100)
	for i := 0; i < 100; i++ {
		tracker.RecordFlagged(reputation.PubKey(weak))
	}

	b.IngestEndorsement(strong, cand)
	b.IngestEndorsement(weak, cand)

	got := b.countStrongEndorsers(cand)
	if got != 1 {
		t.Errorf("countStrongEndorsers = %d, want 1 (only the strong one ≥ 0.5)", got)
	}
}
