// Cold-subscriber bootstrap for the Aggregate redesign.
//
// SPEC.md §3 defines three independent channels a fresh
// subscriber runs in parallel on first launch:
//
//	Channel A — hardcoded anchor pubkeys → PPMI fetch.
//	Channel B — BEP-51 sample_infohashes crawl → inspect
//	             metainfo for snet.pubkey → admit via policy.
//	Channel C — sn_search peer_announce.endorsed gossip →
//	             admit via policy.
//
// This file implements the testable orchestration layer:
// anchor fetch, admission policy, endorsement ingest.
// The BEP-51 crawler is a pluggable hook (CandidateFromCrawl)
// so tests don't need a live DHT; the HTTPS last-ditch
// fallback lives in P5.2.

package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/swartznet/swartznet/internal/dhtindex"
	"github.com/swartznet/swartznet/internal/reputation"
)

// DefaultAnchorPubkeys are the SwartzNet project's hardcoded
// trust anchors, used as EigenTrust-style reputation seeds per
// PROPOSAL.md §2.2. The list is deliberately short — 5 is the
// target — and MUST be populated with real operator keys before
// a production release. For v0.5.x development builds the list
// is empty; operators can inject their own via
// BootstrapOptions.AnchorHexes or a CLI flag.
//
// A zero-anchor bootstrap still works: channels B and C still
// harvest candidate publishers, but the admission policy's
// "at-least-1-anchor-match OR 2-Bloom-hits" rule means a
// bootstrap with no anchors admits only Bloom-matched candidates.
var DefaultAnchorPubkeys = []string{
	// Populated in production releases with trust-anchor pubkeys
	// controlled by the SwartzNet project.
}

// BootstrapOptions tunes the cold-start behavior.
type BootstrapOptions struct {
	// AnchorHexes overrides DefaultAnchorPubkeys with 64-char hex
	// pubkeys supplied by the caller. Empty strings are ignored.
	AnchorHexes []string

	// MaxTrackedPublishers caps how many candidate publishers the
	// bootstrap will admit to the Lookup set. Default 100.
	MaxTrackedPublishers int

	// AnchorReputation is the starting score assigned to every
	// anchor pubkey on first admission. Default 0.8 — high
	// enough to rank their hits above un-vouched publishers
	// without pinning reputation forever.
	AnchorReputation float64

	// CandidateReputation is the starting score for pubkeys
	// admitted via channel B/C. Default 0.1 — low; they'll
	// earn or lose reputation through observed behavior.
	CandidateReputation float64

	// EndorsementThreshold is the number of distinct endorsing
	// peers (with non-trivial reputation) required to bypass the
	// Bloom-filter admission gate. Default 3.
	EndorsementThreshold int
}

// DefaultBootstrapOptions returns the production defaults.
func DefaultBootstrapOptions() BootstrapOptions {
	return BootstrapOptions{
		AnchorHexes:          append([]string(nil), DefaultAnchorPubkeys...),
		MaxTrackedPublishers: 100,
		AnchorReputation:     0.8,
		CandidateReputation:  0.1,
		EndorsementThreshold: 3,
	}
}

// Bootstrap owns the cold-start state machine. Safe for
// concurrent use; internal locking is fine-grained around the
// mutable admission tables.
type Bootstrap struct {
	log     *slog.Logger
	lookup  *dhtindex.Lookup
	ppmi    dhtindex.PPMIGetter
	bloom   *reputation.BloomFilter
	tracker *reputation.Tracker

	opts BootstrapOptions

	mu             sync.Mutex
	anchorKeys     [][32]byte
	admitted       map[[32]byte]struct{}
	endorsements   map[[32]byte]map[[32]byte]struct{} // candidate → endorsers
	observed       map[[32]byte]struct{}              // pubkeys from channel B that didn't clear bloomPolicy
	candidateQueue []candidate
}

// candidate is one publisher observed via channel B/C, waiting
// in line for admission. We queue them rather than admit
// immediately so admission stays rate-limited per the
// MaxTrackedPublishers cap.
type candidate struct {
	Pub    [32]byte
	Label  string
	Source string // "bep51", "endorsement", "anchor"
}

// NewBootstrap constructs a cold-start orchestrator. Required
// deps: a Lookup (to admit publishers into) and a PPMIGetter
// (to fetch anchor PPMIs). bloom and tracker are optional but
// the admission policy leans on them — without a bloom filter
// the "2-hit" rule can't fire; without a tracker the
// endorsement threshold can't weight endorsers.
func NewBootstrap(lookup *dhtindex.Lookup, ppmi dhtindex.PPMIGetter, bloom *reputation.BloomFilter, tracker *reputation.Tracker, opts BootstrapOptions, log *slog.Logger) (*Bootstrap, error) {
	if lookup == nil {
		return nil, errors.New("daemon: bootstrap needs a Lookup")
	}
	if log == nil {
		log = slog.Default()
	}
	if opts.MaxTrackedPublishers <= 0 {
		opts.MaxTrackedPublishers = 100
	}
	if opts.AnchorReputation == 0 {
		opts.AnchorReputation = 0.8
	}
	if opts.CandidateReputation == 0 {
		opts.CandidateReputation = 0.1
	}
	if opts.EndorsementThreshold <= 0 {
		opts.EndorsementThreshold = 3
	}

	b := &Bootstrap{
		log:          log,
		lookup:       lookup,
		ppmi:         ppmi,
		bloom:        bloom,
		tracker:      tracker,
		opts:         opts,
		admitted:     make(map[[32]byte]struct{}),
		endorsements: make(map[[32]byte]map[[32]byte]struct{}),
		observed:     make(map[[32]byte]struct{}),
	}

	for _, s := range opts.AnchorHexes {
		if s == "" {
			continue
		}
		raw, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("daemon: anchor %q not hex: %w", s, err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("daemon: anchor %q has %d bytes, want 32", s, len(raw))
		}
		var pub [32]byte
		copy(pub[:], raw)
		b.anchorKeys = append(b.anchorKeys, pub)
	}
	return b, nil
}

// AnchorKeys returns the validated anchor set as [32]byte
// arrays. Used by tests; also by operators inspecting state via
// a /status endpoint (future).
func (b *Bootstrap) AnchorKeys() [][32]byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][32]byte, len(b.anchorKeys))
	copy(out, b.anchorKeys)
	return out
}

// RunAnchors executes channel A: for each anchor pubkey, fetch
// its PPMI and, if successful, admit it to the Lookup with the
// AnchorReputation seed. Returns the count of successfully
// fetched anchors and a slice of per-anchor errors.
func (b *Bootstrap) RunAnchors(ctx context.Context) (int, []error) {
	if b.ppmi == nil {
		return 0, []error{errors.New("daemon: bootstrap has no PPMIGetter")}
	}
	b.mu.Lock()
	anchors := append([][32]byte(nil), b.anchorKeys...)
	b.mu.Unlock()

	type result struct {
		pub [32]byte
		err error
	}
	results := make(chan result, len(anchors))
	var wg sync.WaitGroup
	for _, pub := range anchors {
		wg.Add(1)
		go func(p [32]byte) {
			defer wg.Done()
			_, err := b.ppmi.GetPPMI(ctx, p)
			results <- result{pub: p, err: err}
		}(pub)
	}
	wg.Wait()
	close(results)

	succeeded := 0
	var errs []error
	for r := range results {
		if r.err != nil {
			errs = append(errs, fmt.Errorf("anchor %x: %w", r.pub[:8], r.err))
			continue
		}
		if ok := b.admit(r.pub, "anchor-"+hex.EncodeToString(r.pub[:4]), "anchor"); ok {
			succeeded++
		}
	}
	return succeeded, errs
}

// IngestEndorsement processes one entry from a peer_announce
// endorsement list (channel C). `endorser` is the peer that sent
// the announce; `candidate` is the pubkey they're vouching for.
// Admission fires when:
//
//   - The candidate is already admitted: we add to the
//     endorsement count and return admitted=true.
//   - The candidate has EndorsementThreshold distinct endorsers
//     whose reputation ≥ 0.5 each.
//   - OR the Bloom filter + reputation heuristic from the
//     admission policy accepts.
func (b *Bootstrap) IngestEndorsement(endorser, cand [32]byte) bool {
	b.mu.Lock()
	if _, ok := b.admitted[cand]; ok {
		// Already admitted — just track the endorser for stats.
		if _, ok := b.endorsements[cand]; !ok {
			b.endorsements[cand] = make(map[[32]byte]struct{})
		}
		b.endorsements[cand][endorser] = struct{}{}
		b.mu.Unlock()
		return true
	}
	endorsers, ok := b.endorsements[cand]
	if !ok {
		endorsers = make(map[[32]byte]struct{})
		b.endorsements[cand] = endorsers
	}
	endorsers[endorser] = struct{}{}
	b.mu.Unlock()

	// Check endorsement threshold.
	if b.countStrongEndorsers(cand) >= b.opts.EndorsementThreshold {
		return b.admit(cand, "endorsed", "endorsement")
	}
	// Fall back to bloom policy.
	if b.bloomPolicy(cand) {
		return b.admit(cand, "endorsed-bloom", "endorsement")
	}
	return false
}

// CandidateFromCrawl processes one publisher pubkey discovered
// via channel B (BEP-51 sample_infohashes + metainfo inspection,
// or sync-record ingestion via PublisherObserver). The caller is
// responsible for verifying the torrent's snet.sig (or record's
// sig) before passing the pubkey in. Admission is subject to
// bloom + endorsement policy.
//
// Pubkeys that don't clear the admission gate are recorded in
// the observed set so IsPending returns true. They can be
// admitted later if endorsements accumulate or if the Bloom
// filter starts matching their hits.
func (b *Bootstrap) CandidateFromCrawl(cand [32]byte, sigValid bool) bool {
	if !sigValid {
		// SPEC §3.2 requires a valid snet.sig before admission.
		return false
	}
	b.mu.Lock()
	if _, ok := b.admitted[cand]; ok {
		b.mu.Unlock()
		return true
	}
	b.observed[cand] = struct{}{}
	b.mu.Unlock()
	if b.bloomPolicy(cand) {
		return b.admit(cand, "crawled", "bep51")
	}
	// No immediate admission — observed entry stays so
	// IsPending reflects reality, and a future endorsement round
	// or Bloom update can promote the candidate.
	return false
}

// IsAdmitted returns whether a given pubkey has been admitted
// to the Lookup set by this bootstrap.
func (b *Bootstrap) IsAdmitted(cand [32]byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.admitted[cand]
	return ok
}

// IsPending returns whether a candidate has been seen (via
// channel B or C) but not yet admitted. Useful for operator
// diagnostics and tests. A candidate is "pending" if it has
// ANY endorsement OR has been observed via a crawl/sync signal
// but hasn't cleared the admission gate.
func (b *Bootstrap) IsPending(cand [32]byte) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.admitted[cand]; ok {
		return false
	}
	if _, ok := b.endorsements[cand]; ok {
		return true
	}
	_, ok := b.observed[cand]
	return ok
}

// AdmittedCount returns the total number of publishers the
// bootstrap has brought into the Lookup set.
func (b *Bootstrap) AdmittedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.admitted)
}

// AnchorCount returns the number of hardcoded+HTTPS-fetched
// anchor pubkeys currently in the bootstrap set. Used by the
// httpapi /aggregate endpoint via the BootstrapProbe interface.
func (b *Bootstrap) AnchorCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.anchorKeys)
}

// admit performs the actual Lookup registration + reputation
// seeding + admitted bookkeeping. Returns false if we've already
// admitted this pubkey or hit the MaxTrackedPublishers cap.
func (b *Bootstrap) admit(pub [32]byte, label, source string) bool {
	b.mu.Lock()
	if _, ok := b.admitted[pub]; ok {
		b.mu.Unlock()
		return true
	}
	if len(b.admitted) >= b.opts.MaxTrackedPublishers {
		b.mu.Unlock()
		return false
	}
	b.admitted[pub] = struct{}{}
	b.mu.Unlock()

	b.lookup.AddIndexer(pub, label)
	if b.tracker != nil {
		rep := b.opts.CandidateReputation
		if source == "anchor" {
			rep = b.opts.AnchorReputation
		}
		// Tracker doesn't have a direct "set score" API; we
		// instead record enough Hits-Returned events to cause
		// the Bayesian-smoothed score to land at approximately
		// the seed. For now we just call RecordReturned once to
		// put the pubkey on the tracker's radar; a later commit
		// can seed more precisely.
		b.tracker.RecordReturned(reputation.PubKey(pub), 0)
		_ = rep
	}
	return true
}

// bloomPolicy returns true if the candidate should be admitted
// based on the Bloom filter + reputation heuristic: at least two
// of the candidate's (hypothetical) hits appear in the known-good
// Bloom filter OR at least one overlaps an anchor's index.
//
// Today we don't yet know the candidate's hits (they live in
// the companion torrent, fetched only post-admission). So this
// check is a placeholder that returns true when the bloom or
// tracker disposition is "not clearly bad": the conservative
// default is false; tests set it to true via the test-only
// BloomOverride helper.
func (b *Bootstrap) bloomPolicy(cand [32]byte) bool {
	// Without a Bloom filter, default-deny for crawl candidates
	// (channel C still admits via endorsements). This keeps
	// channel B quiet on a cold cache.
	if b.bloom == nil {
		return false
	}
	// If the tracker already knows this pubkey with a
	// reasonable score, admit.
	if b.tracker != nil && b.tracker.Threshold(reputation.PubKey(cand), 0.3) {
		return true
	}
	return false
}

// countStrongEndorsers returns the number of distinct endorsers
// with tracker-reported reputation ≥ 0.5. Zero when no tracker
// is attached.
func (b *Bootstrap) countStrongEndorsers(cand [32]byte) int {
	b.mu.Lock()
	endorsers := make([][32]byte, 0, len(b.endorsements[cand]))
	for e := range b.endorsements[cand] {
		endorsers = append(endorsers, e)
	}
	b.mu.Unlock()

	if b.tracker == nil {
		// Without a tracker, any endorsers count; this is
		// permissive but safe because we still gate on
		// EndorsementThreshold distinct IPs via the peer layer.
		return len(endorsers)
	}
	strong := 0
	for _, e := range endorsers {
		if b.tracker.Threshold(reputation.PubKey(e), 0.5) {
			strong++
		}
	}
	return strong
}
