package admission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// pubkeyBytes derives a distinct 32-byte pubkey from a label via
// SHA-256 (ported from the legacy bootstrap test helper).
func pubkeyBytes(label string) [32]byte { return sha256.Sum256([]byte(label)) }

// fakeRep is an in-memory ReputationView. Unknown pubkeys score the
// neutral UnknownPrior, faithfully reproducing the reputation.Tracker
// behavior the legacy defect leaned on, without importing reputation.
type fakeRep struct {
	mu     sync.Mutex
	scores map[[32]byte]float64
	seeded map[[32]byte]bool
}

func newFakeRep() *fakeRep {
	return &fakeRep{scores: map[[32]byte]float64{}, seeded: map[[32]byte]bool{}}
}

func (f *fakeRep) Score(pub [32]byte) float64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.scores[pub]; ok {
		return s
	}
	return UnknownPrior
}

func (f *fakeRep) IsSeeded(pub [32]byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seeded[pub]
}

func (f *fakeRep) MarkSeeded(pub [32]byte, _ string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seeded[pub] = true
}

func (f *fakeRep) setScore(pub [32]byte, s float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scores[pub] = s
}

func newEngine(t *testing.T, policy Policy, rep ReputationView) *AdmissionEngine {
	t.Helper()
	e, err := NewEngine(policy, rep, nil, discardLog())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
}

// TestUnknownCandidateDenied — the headline inversion. An unknown
// 0.5-scored crawl candidate must NOT be admitted under DefaultPolicy.
// This is the exact case the legacy `Score >= 0.3` bloomPolicy admitted.
func TestUnknownCandidateDenied(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	cand := pubkeyBytes("unknown-crawl")

	if e.CandidateFromCrawl(cand, true) {
		t.Fatal("unknown 0.5-scored candidate must be DENIED under DefaultPolicy")
	}
	if e.IsAdmitted(cand) {
		t.Error("denied candidate must not be admitted")
	}
	if !e.IsPending(cand) {
		t.Error("crawl-observed candidate must be pending")
	}
	if got := e.AdmittedCount(); got != 0 {
		t.Errorf("AdmittedCount = %d, want 0", got)
	}
}

// TestUnknownCandidateDeniedNilRep — with no reputation view attached
// there is no earned signal at all, so the gate stays closed.
func TestUnknownCandidateDeniedNilRep(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), nil)
	if e.CandidateFromCrawl(pubkeyBytes("nil-rep-cand"), true) {
		t.Fatal("candidate must be denied when no reputation view is attached")
	}
}

// TestThreeFreshEndorsersDoNotClearBar — three freshly-generated
// unknown (0.5-scored) endorsers must NOT clear EndorsementThreshold.
// The legacy `Score >= 0.5` endorser count admitted them for free.
func TestThreeFreshEndorsersDoNotClearBar(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	cand := pubkeyBytes("sybil-target")

	for i, name := range []string{"sybil-1", "sybil-2", "sybil-3"} {
		endorser := pubkeyBytes(name)
		if e.IngestEndorsement(endorser, cand) {
			t.Fatalf("endorsement %d by a fresh 0.5 endorser must NOT admit", i+1)
		}
	}
	if e.IsAdmitted(cand) {
		t.Error("candidate backed only by fresh Sybil endorsers must stay denied")
	}
	if !e.IsPending(cand) {
		t.Error("endorsed-but-denied candidate must be pending")
	}
}

// TestStrongEndorsersClearBar — three STRONG endorsers (earned score
// strictly above the prior) admit the candidate on endorsements alone.
func TestStrongEndorsersClearBar(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, DefaultPolicy(), rep)
	cand := pubkeyBytes("well-vouched")

	for i, name := range []string{"strong-1", "strong-2", "strong-3"} {
		endorser := pubkeyBytes(name)
		rep.setScore(endorser, 0.9)
		admitted := e.IngestEndorsement(endorser, cand)
		// Only the third distinct strong endorsement crosses threshold.
		if i < 2 && admitted {
			t.Fatalf("endorsement %d should not yet admit (threshold 3)", i+1)
		}
		if i == 2 && !admitted {
			t.Fatal("third strong endorsement must admit")
		}
	}
	if !e.IsAdmitted(cand) {
		t.Error("candidate with 3 strong endorsers must be admitted")
	}
	if e.IsPending(cand) {
		t.Error("admitted candidate must not be pending")
	}
}

// TestSeededEndorserIsStrong — a seeded (but merely 0.5-scored)
// endorser counts as strong. Three of them clear the bar.
func TestSeededEndorserIsStrong(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, DefaultPolicy(), rep)
	cand := pubkeyBytes("seeded-vouched")

	for _, name := range []string{"seed-e1", "seed-e2", "seed-e3"} {
		endorser := pubkeyBytes(name)
		rep.MarkSeeded(endorser, name)
		e.IngestEndorsement(endorser, cand)
	}
	if !e.IsAdmitted(cand) {
		t.Error("candidate with 3 seeded endorsers must be admitted")
	}
}

// TestEarnedCandidateAdmitted — a crawl candidate whose own earned
// score is strictly above the prior is admitted directly.
func TestEarnedCandidateAdmitted(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, DefaultPolicy(), rep)
	cand := pubkeyBytes("earned-pub")
	rep.setScore(cand, 0.75)

	if !e.CandidateFromCrawl(cand, true) {
		t.Fatal("candidate with earned Score>0.5 must be admitted")
	}
	if !e.IsAdmitted(cand) {
		t.Error("earned candidate must be admitted")
	}
}

// TestExactPriorCandidateDenied — a candidate scoring EXACTLY the prior
// is denied: the bar is strict (>), not >=.
func TestExactPriorCandidateDenied(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, DefaultPolicy(), rep)
	cand := pubkeyBytes("exactly-half")
	rep.setScore(cand, UnknownPrior)

	if e.CandidateFromCrawl(cand, true) {
		t.Fatal("candidate scoring exactly the prior must be denied (strict >)")
	}
}

// TestSeededCandidateAdmitted — a crawl candidate that is itself seeded
// (anchor/seed membership) is admitted even at the 0.5 prior.
func TestSeededCandidateAdmitted(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, DefaultPolicy(), rep)
	cand := pubkeyBytes("seeded-crawl")
	rep.MarkSeeded(cand, "seed")

	if !e.CandidateFromCrawl(cand, true) {
		t.Fatal("seeded candidate must be admitted")
	}
}

// TestCrawlRequiresValidSig — an invalid-signature crawl candidate is
// rejected before the policy runs, and is not even observed.
func TestCrawlRequiresValidSig(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, DefaultPolicy(), rep)
	cand := pubkeyBytes("bad-sig")
	rep.setScore(cand, 0.99) // would otherwise be admitted

	if e.CandidateFromCrawl(cand, false) {
		t.Fatal("crawl candidate with sigValid=false must be rejected")
	}
	if e.IsAdmitted(cand) || e.IsPending(cand) {
		t.Error("invalid-sig candidate must be neither admitted nor pending")
	}
}

// TestAnchorAdmittedSeededAndCapExempt — admitting an anchor admits it,
// marks it seeded on the reputation view, and is exempt from the cap.
func TestAnchorAdmittedSeededAndCapExempt(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	policy := DefaultPolicy()
	policy.MaxTrackedPublishers = 1
	e := newEngine(t, policy, rep)

	// Fill the non-anchor cap with an earned candidate.
	cand := pubkeyBytes("cap-filler")
	rep.setScore(cand, 0.8)
	if !e.CandidateFromCrawl(cand, true) {
		t.Fatal("earned candidate should fill the cap")
	}

	anchor := pubkeyBytes("the-anchor")
	if !e.AdmitAnchor(anchor, "anchor-label") {
		t.Fatal("anchor must be admitted even with the candidate cap full")
	}
	if !e.IsAdmitted(anchor) {
		t.Error("anchor must be admitted")
	}
	if !rep.IsSeeded(anchor) {
		t.Error("anchor admit must MarkSeeded the pubkey on the reputation view")
	}
	// A second anchor also admits (cap-exempt); a further candidate is
	// still refused, proving anchors do not consume candidate slots.
	if !e.AdmitAnchor(pubkeyBytes("anchor-2"), "a2") {
		t.Error("second anchor must admit (anchors are cap-exempt)")
	}
	cand2 := pubkeyBytes("cap-filler-2")
	rep.setScore(cand2, 0.8)
	if e.CandidateFromCrawl(cand2, true) {
		t.Error("second earned candidate must be refused — cap excludes anchors but stays enforced")
	}
	if got := e.AdmittedCount(); got != 3 {
		t.Errorf("AdmittedCount = %d, want 3 (1 candidate + 2 anchors)", got)
	}
}

// TestCandidateSourcesDoNotSeed — non-anchor admits must NOT mark the
// reputation view as seeded; they start at the prior and must earn.
func TestCandidateSourcesDoNotSeed(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	e := newEngine(t, PermissivePolicyForTest(), rep)

	crawl := pubkeyBytes("permissive-crawl")
	if !e.CandidateFromCrawl(crawl, true) {
		t.Fatal("permissive crawl admit failed")
	}
	if rep.IsSeeded(crawl) {
		t.Error("crawl source must NOT mark the reputation view as seeded")
	}

	endorser := pubkeyBytes("permissive-endorser")
	endorsed := pubkeyBytes("permissive-endorsed")
	if !e.IngestEndorsement(endorser, endorsed) {
		t.Fatal("permissive endorsement admit failed")
	}
	if rep.IsSeeded(endorsed) {
		t.Error("endorsement source must NOT mark the reputation view as seeded")
	}
}

// TestPermissivePolicyAdmitsAll — the test-only permissive policy
// reproduces the legacy always-admit behavior: unknown 0.5 candidates
// and single fresh endorsers are admitted.
func TestPermissivePolicyAdmitsAll(t *testing.T) {
	t.Parallel()
	e := newEngine(t, PermissivePolicyForTest(), newFakeRep())

	if !e.CandidateFromCrawl(pubkeyBytes("perm-unknown"), true) {
		t.Error("permissive policy must admit an unknown crawl candidate")
	}
	if !e.IngestEndorsement(pubkeyBytes("perm-e"), pubkeyBytes("perm-c")) {
		t.Error("permissive policy must admit on a single fresh endorsement")
	}
	if !e.AdmitAnchor(pubkeyBytes("perm-anchor"), "a") {
		t.Error("permissive policy must admit an anchor")
	}
}

// TestAdmitIdempotent — re-admitting the same pubkey returns true
// without growing the admitted set.
func TestAdmitIdempotent(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	anchor := pubkeyBytes("idempotent")

	if !e.AdmitAnchor(anchor, "first") {
		t.Fatal("first admit failed")
	}
	if !e.AdmitAnchor(anchor, "second") {
		t.Error("re-admit must return true")
	}
	if got := e.AdmittedCount(); got != 1 {
		t.Errorf("AdmittedCount = %d, want 1", got)
	}
}

// TestCandidateFromCrawlAlreadyAdmittedShortCircuits — re-receiving a
// crawl signal for an already-admitted pubkey returns true without
// making it pending.
func TestCandidateFromCrawlAlreadyAdmittedShortCircuits(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	pub := pubkeyBytes("pre-admitted")
	if !e.AdmitAnchor(pub, "seed-anchor") {
		t.Fatal("anchor admit failed")
	}
	if !e.CandidateFromCrawl(pub, true) {
		t.Error("CandidateFromCrawl should return true for an already-admitted pubkey")
	}
	if e.IsPending(pub) {
		t.Error("already-admitted pubkey must not become pending")
	}
}

// TestCapRefusalLogged — refusing a candidate at the cap emits the
// admission.admit_capped warn line carrying the refused source, so a
// starved node is not silent.
func TestCapRefusalLogged(t *testing.T) {
	t.Parallel()
	rep := newFakeRep()
	policy := PermissivePolicyForTest()
	policy.MaxTrackedPublishers = 1
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	e, err := NewEngine(policy, rep, nil, log)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if !e.IngestEndorsement(pubkeyBytes("e1"), pubkeyBytes("cand-1")) {
		t.Fatal("first candidate should admit under permissive policy")
	}
	if e.IngestEndorsement(pubkeyBytes("e2"), pubkeyBytes("cand-2")) {
		t.Fatal("second candidate should be refused at cap=1")
	}
	out := buf.String()
	if !strings.Contains(out, "admit_capped") {
		t.Errorf("cap refusal must log admit_capped, got %q", out)
	}
	if !strings.Contains(out, "endorsement") {
		t.Errorf("cap refusal log must carry the refused source, got %q", out)
	}
}

// TestIsPendingBranches walks every IsPending state explicitly.
func TestIsPendingBranches(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())

	endorsed := pubkeyBytes("branch-endorsed")
	observed := pubkeyBytes("branch-observed")
	admitted := pubkeyBytes("branch-admitted")
	unknown := pubkeyBytes("branch-unknown")

	e.IngestEndorsement(pubkeyBytes("branch-endorser"), endorsed)
	if !e.IsPending(endorsed) {
		t.Error("endorsed candidate must be pending")
	}
	e.CandidateFromCrawl(observed, true)
	if !e.IsPending(observed) {
		t.Error("observed candidate must be pending")
	}
	if !e.AdmitAnchor(admitted, "a") {
		t.Fatal("anchor admit failed")
	}
	if e.IsPending(admitted) {
		t.Error("admitted candidate must NOT be pending")
	}
	if e.IsPending(unknown) {
		t.Error("never-seen candidate must NOT be pending")
	}
}

// TestPendingCountDedup — a pubkey that is both endorsed and observed
// counts once; admitted pubkeys drop out of the pending count.
func TestPendingCountDedup(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	both := pubkeyBytes("endorsed-and-observed")

	e.IngestEndorsement(pubkeyBytes("dedup-endorser"), both)
	e.CandidateFromCrawl(both, true)
	if got := e.PendingCount(); got != 1 {
		t.Errorf("PendingCount = %d, want 1 (dedup across endorsement+observed)", got)
	}

	// A second, distinct observed candidate bumps the count to 2.
	e.CandidateFromCrawl(pubkeyBytes("other-observed"), true)
	if got := e.PendingCount(); got != 2 {
		t.Errorf("PendingCount = %d, want 2", got)
	}
}

// TestStarvedNodeZeros — a fresh engine with the empty default anchor
// set renders 0/0/0, distinguishable from an unwired (quiet) node only
// by the block's presence. The count aliases agree with the *Count
// accessors.
func TestStarvedNodeZeros(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	if e.AnchorCount() != 0 || e.AdmittedCount() != 0 || e.PendingCount() != 0 {
		t.Errorf("fresh engine counts = (%d,%d,%d), want (0,0,0)",
			e.AnchorCount(), e.AdmittedCount(), e.PendingCount())
	}
	if e.Anchors() != e.AnchorCount() || e.Admitted() != e.AdmittedCount() || e.Pending() != e.PendingCount() {
		t.Error("short-named accessors must agree with the *Count methods")
	}
}

// TestConfiguredAnchorsCounted — operator-supplied anchor hexes are
// validated and reflected by AnchorCount even before any are admitted.
func TestConfiguredAnchorsCounted(t *testing.T) {
	t.Parallel()
	a1 := hex.EncodeToString(pubkeyToSlice(pubkeyBytes("configured-1")))
	a2 := hex.EncodeToString(pubkeyToSlice(pubkeyBytes("configured-2")))
	e, err := NewEngine(DefaultPolicy(), newFakeRep(), []string{a1, "", a2}, discardLog())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if got := e.AnchorCount(); got != 2 {
		t.Errorf("AnchorCount = %d, want 2 (empty hex ignored)", got)
	}
	if got := e.AdmittedCount(); got != 0 {
		t.Errorf("configured anchors are not admitted until fetched; AdmittedCount = %d, want 0", got)
	}
}

func pubkeyToSlice(pub [32]byte) []byte { return pub[:] }

// TestNewEngineRejectsBadAnchors — non-hex and wrong-length anchor
// strings are rejected at construction.
func TestNewEngineRejectsBadAnchors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		anchors []string
	}{
		{"non-hex", []string{"not-hex"}},
		{"wrong-length", []string{"abcd"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewEngine(DefaultPolicy(), newFakeRep(), tc.anchors, discardLog()); err == nil {
				t.Errorf("expected error for anchors %v", tc.anchors)
			}
		})
	}
}

// TestDefaultAnchorPubkeysEmpty — the shipped anchor list is empty: a
// curated set is a release prerequisite, not code.
func TestDefaultAnchorPubkeysEmpty(t *testing.T) {
	t.Parallel()
	if len(DefaultAnchorPubkeys) != 0 {
		t.Errorf("DefaultAnchorPubkeys must ship empty, got %d entries", len(DefaultAnchorPubkeys))
	}
}

// TestNewEngineDefaultsClampPolicy — non-positive policy tunables fall
// back to the package defaults.
func TestNewEngineDefaultsClampPolicy(t *testing.T) {
	t.Parallel()
	policy := DefaultPolicy()
	policy.MaxTrackedPublishers = 0
	policy.EndorsementThreshold = -1
	e := newEngine(t, policy, newFakeRep())
	if e.policy.MaxTrackedPublishers != MaxTrackedPublishers {
		t.Errorf("MaxTrackedPublishers = %d, want %d", e.policy.MaxTrackedPublishers, MaxTrackedPublishers)
	}
	if e.policy.EndorsementThreshold != EndorsementThreshold {
		t.Errorf("EndorsementThreshold = %d, want %d", e.policy.EndorsementThreshold, EndorsementThreshold)
	}
}

// TestUnknownSourceDenied — a candidate with a source no rule governs
// is denied by default.
func TestUnknownSourceDenied(t *testing.T) {
	t.Parallel()
	e := newEngine(t, DefaultPolicy(), newFakeRep())
	if e.admit(Candidate{Pub: pubkeyBytes("weird"), Source: Source("mystery")}) {
		t.Error("candidate with an unknown source must be denied")
	}
}
