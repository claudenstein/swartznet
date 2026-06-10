package daemon

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/swartznet/swartznet/internal/reputation"
)

// TestBootstrapAdmitCapEnforced — admit must reject the
// (MaxTrackedPublishers+1)-th candidate to keep the lookup
// set bounded. We use a low cap so the test stays cheap.
// Candidates use non-anchor sources because anchors are exempt
// from the cap (see TestBootstrapAdmitAnchorsExemptFromCap).
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

	if !b.admit(c1, "label1", "bep51") {
		t.Fatal("admit c1 should succeed")
	}
	if !b.admit(c2, "label2", "endorsement") {
		t.Fatal("admit c2 should succeed (still below cap)")
	}
	if b.admit(c3, "label3", "bep51") {
		t.Error("admit c3 should fail — cap=2 reached")
	}
	if got := b.AdmittedCount(); got != 2 {
		t.Errorf("AdmittedCount = %d, want 2", got)
	}
}

// TestBootstrapAdmitAnchorsExemptFromCap — anchors are the trust
// seeds the admission policy leans on, so they must neither be
// refused by MaxTrackedPublishers nor occupy candidate slots.
// Before the exemption, a node whose cap had been filled by
// crawl/endorsement candidates would silently drop a late-arriving
// anchor (and vice versa: anchors admitted first silently shrank
// the candidate budget).
func TestBootstrapAdmitAnchorsExemptFromCap(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	opts.MaxTrackedPublishers = 1
	b, err := NewBootstrap(lookup, nil, nil, nil, opts, nil)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	// Fill the candidate cap.
	if !b.admit(pubkeyBytes("exempt-cand-1"), "cand1", "bep51") {
		t.Fatal("first candidate should fill cap=1")
	}
	// Anchors still admit past the full cap.
	if !b.admit(pubkeyBytes("exempt-anchor-1"), "anchor1", "anchor") {
		t.Error("anchor must be admitted even when the candidate cap is full")
	}
	if !b.admit(pubkeyBytes("exempt-anchor-2"), "anchor2", "anchor") {
		t.Error("second anchor must also be admitted (anchors don't count toward cap)")
	}
	// And anchors don't consume candidate slots: a further
	// candidate is still refused (cap of 1 already used by cand1),
	// proving the count excludes anchors rather than racing them.
	if b.admit(pubkeyBytes("exempt-cand-2"), "cand2", "endorsement") {
		t.Error("second candidate should still be refused — cap excludes anchors but stays enforced")
	}
	if got := b.AdmittedCount(); got != 3 {
		t.Errorf("AdmittedCount = %d, want 3 (1 candidate + 2 anchors)", got)
	}
}

// TestBootstrapAdmitCapRefusalLogged — refusing a candidate at the
// cap must not be silent: operators need to distinguish a starved
// node from a quiet network. We capture the slog output and assert
// the warn line fires with the refused source.
func TestBootstrapAdmitCapRefusalLogged(t *testing.T) {
	t.Parallel()
	lookup := newTestLookup()
	opts := DefaultBootstrapOptions()
	opts.MaxTrackedPublishers = 1
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	b, err := NewBootstrap(lookup, nil, nil, nil, opts, log)
	if err != nil {
		t.Fatalf("NewBootstrap: %v", err)
	}

	if !b.admit(pubkeyBytes("logged-cand-1"), "cand1", "bep51") {
		t.Fatal("first candidate should admit")
	}
	if b.admit(pubkeyBytes("logged-cand-2"), "cand2", "endorsement") {
		t.Fatal("second candidate should be refused at cap=1")
	}
	out := buf.String()
	if !strings.Contains(out, "admit_capped") {
		t.Errorf("cap refusal should log admit_capped, got %q", out)
	}
	if !strings.Contains(out, "endorsement") {
		t.Errorf("cap refusal log should carry the refused source, got %q", out)
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
