package admission

import (
	"encoding/hex"
	"fmt"
	"log/slog"
)

// AdmissionEngine is the deny-by-default publisher-admission gate. It
// composes a Policy over the reputation view and owns the admitted /
// pending / anchor state. Deterministic code owns every state
// transition; no admission is the result of prompt-following behavior.
// Safe for concurrent use.
type AdmissionEngine struct {
	policy Policy
	rep    ReputationView // may be nil: no earned signal, gate stays closed
	log    *slog.Logger
	state  *State
}

// NewEngine constructs an admission engine. anchorHexes are the
// configured trust anchors (64-char hex ed25519 pubkeys); each is
// validated and empty strings are ignored. rep may be nil (no earned
// signal). A nil log falls back to slog.Default.
func NewEngine(policy Policy, rep ReputationView, anchorHexes []string, log *slog.Logger) (*AdmissionEngine, error) {
	if log == nil {
		log = slog.Default()
	}
	if policy.MaxTrackedPublishers <= 0 {
		policy.MaxTrackedPublishers = MaxTrackedPublishers
	}
	if policy.EndorsementThreshold <= 0 {
		policy.EndorsementThreshold = EndorsementThreshold
	}

	var anchorKeys [][32]byte
	for _, s := range anchorHexes {
		if s == "" {
			continue
		}
		raw, err := hex.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("admission: anchor %q not hex: %w", s, err)
		}
		if len(raw) != 32 {
			return nil, fmt.Errorf("admission: anchor %q has %d bytes, want 32", s, len(raw))
		}
		var pub [32]byte
		copy(pub[:], raw)
		anchorKeys = append(anchorKeys, pub)
	}

	return &AdmissionEngine{
		policy: policy,
		rep:    rep,
		log:    log,
		state:  newState(anchorKeys),
	}, nil
}

// AdmitAnchor admits a configured trust anchor (channel A). Anchors are
// exempt from the publisher cap and are marked seeded on the reputation
// view so they carry the decaying seed bonus. Idempotent. Slice 12's
// anchor PPMI loop calls this per successfully-fetched anchor.
func (e *AdmissionEngine) AdmitAnchor(pub [32]byte, label string) bool {
	return e.admit(Candidate{Pub: pub, Label: label, Source: SourceAnchor})
}

// CandidateFromCrawl processes a publisher discovered via a BEP-51
// crawl (channel B). sigValid MUST be true — the caller verifies the
// torrent's snet.sig before admission. Candidates that do not clear the
// deny-by-default crawl rule are recorded as observed so IsPending
// reflects them and a later endorsement/earned round can promote them.
// Slice 12's crawler calls this per observed publisher.
func (e *AdmissionEngine) CandidateFromCrawl(cand [32]byte, sigValid bool) bool {
	if !sigValid {
		return false
	}
	s := e.state
	s.mu.Lock()
	if _, ok := s.admitted[cand]; ok {
		s.mu.Unlock()
		return true
	}
	s.observed[cand] = struct{}{}
	s.mu.Unlock()
	return e.admit(Candidate{Pub: cand, Label: "crawled", Source: SourceBEP51})
}

// IngestEndorsement processes one endorsement (channel C): endorser
// vouches for cand. An already-admitted candidate stays admitted and
// records the endorser for stats. Otherwise the endorser is recorded
// and the deny-by-default endorsement rule re-evaluated — admission
// fires only with EndorsementThreshold distinct STRONG endorsers or the
// candidate's own earned signal. Slice 12's gossip path calls this.
func (e *AdmissionEngine) IngestEndorsement(endorser, cand [32]byte) bool {
	s := e.state
	s.mu.Lock()
	if _, ok := s.admitted[cand]; ok {
		e.recordEndorserLocked(cand, endorser)
		s.mu.Unlock()
		return true
	}
	e.recordEndorserLocked(cand, endorser)
	s.mu.Unlock()
	return e.admit(Candidate{Pub: cand, Label: "endorsed", Source: SourceEndorsement})
}

// admit runs the composed policy gate then the capacity gate for one
// candidate and records the outcome. A true return means the candidate
// is in the admitted set (newly or already). A false return is EITHER a
// policy denial OR a capacity refusal; the two are distinguished only
// by the admission.admit_capped warn log, so a starved node is never
// silent.
func (e *AdmissionEngine) admit(c Candidate) bool {
	s := e.state
	s.mu.Lock()

	if _, ok := s.admitted[c.Pub]; ok {
		s.mu.Unlock()
		return true
	}

	c.StrongEndorsers = e.strongEndorsersLocked(c.Pub)

	rule := e.policy.ruleFor(c.Source)
	if rule == nil || !rule.Admit(c, e.rep) {
		s.mu.Unlock()
		return false
	}

	if c.Source != SourceAnchor {
		if len(s.admitted)-len(s.anchorsAdmitted) >= e.policy.MaxTrackedPublishers {
			s.mu.Unlock()
			e.log.Warn("admission.admit_capped",
				"pubkey", hex.EncodeToString(c.Pub[:8]),
				"source", string(c.Source),
				"cap", e.policy.MaxTrackedPublishers)
			return false
		}
	}

	s.admitted[c.Pub] = struct{}{}
	if c.Source == SourceAnchor {
		s.anchorsAdmitted[c.Pub] = struct{}{}
	}
	s.mu.Unlock()

	// Anchor seeding is the only reputation write admission performs; it
	// gives anchors the decaying seed bonus. Non-anchor sources start at
	// the unknown prior and must earn reputation elsewhere.
	if c.Source == SourceAnchor && e.rep != nil {
		e.rep.MarkSeeded(c.Pub, c.Label)
	}
	return true
}

// recordEndorserLocked adds endorser to cand's endorser set. Caller
// must hold e.state.mu.
func (e *AdmissionEngine) recordEndorserLocked(cand, endorser [32]byte) {
	set, ok := e.state.endorsements[cand]
	if !ok {
		set = make(map[[32]byte]struct{})
		e.state.endorsements[cand] = set
	}
	set[endorser] = struct{}{}
}

// strongEndorsersLocked counts cand's distinct STRONG endorsers: those
// with earned signal (Score strictly above the unknown prior, or
// seeded). Zero when no reputation view is attached — the fail-closed
// inversion of the legacy "no tracker counts every endorser" behavior
// that let fresh Sybils clear the bar. Caller must hold e.state.mu.
func (e *AdmissionEngine) strongEndorsersLocked(cand [32]byte) int {
	endorsers := e.state.endorsements[cand]
	if len(endorsers) == 0 || e.rep == nil {
		return 0
	}
	n := 0
	for ep := range endorsers {
		if e.rep.Score(ep) > UnknownPrior || e.rep.IsSeeded(ep) {
			n++
		}
	}
	return n
}

// IsAdmitted reports whether the pubkey has been admitted.
func (e *AdmissionEngine) IsAdmitted(pub [32]byte) bool { return e.state.IsAdmitted(pub) }

// IsPending reports whether the pubkey is seen but not admitted.
func (e *AdmissionEngine) IsPending(pub [32]byte) bool { return e.state.IsPending(pub) }

// AnchorCount, AdmittedCount, and PendingCount satisfy the httpapi
// /aggregate probe interface structurally (no import either way).

// AnchorCount returns the configured trust-anchor count.
func (e *AdmissionEngine) AnchorCount() int { return e.state.AnchorCount() }

// AdmittedCount returns the total admitted publisher count.
func (e *AdmissionEngine) AdmittedCount() int { return e.state.AdmittedCount() }

// PendingCount returns the distinct observed-but-not-admitted count.
func (e *AdmissionEngine) PendingCount() int { return e.state.PendingCount() }

// Anchors, Admitted, and Pending are the short-named count accessors
// the /aggregate probe reads; they alias the *Count methods.

// Anchors returns the configured trust-anchor count.
func (e *AdmissionEngine) Anchors() int { return e.state.AnchorCount() }

// Admitted returns the total admitted publisher count.
func (e *AdmissionEngine) Admitted() int { return e.state.AdmittedCount() }

// Pending returns the distinct observed-but-not-admitted count.
func (e *AdmissionEngine) Pending() int { return e.state.PendingCount() }
