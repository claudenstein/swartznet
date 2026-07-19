package engine

import (
	"time"

	"github.com/swartznet/swartznet/internal/config"
	"github.com/swartznet/swartznet/internal/reputation"
	"github.com/swartznet/swartznet/internal/trust"
)

// loadSpamResistance loads the Bloom filter, reputation tracker, and trust
// allowlist from their configured paths. Every load failure is a warning —
// the field stays nil and the node runs without that signal. An empty path
// is feature-off (nil), consistent with the rest of the config.
func (e *Engine) loadSpamResistance(cfg config.Config) {
	if cfg.BloomPath != "" {
		bloom, err := reputation.LoadOrCreateBloom(cfg.BloomPath)
		if err != nil {
			e.log.Warn("engine.bloom_load_err", "err", err)
		} else {
			e.bloom = bloom
			e.log.Info("engine.bloom_loaded", "path", cfg.BloomPath, "estimated_items", bloom.EstimatedItems())
		}
	}
	if cfg.ReputationPath != "" {
		tracker, err := reputation.LoadOrCreateTracker(cfg.ReputationPath)
		if err != nil {
			e.log.Warn("engine.reputation_load_err", "err", err)
		} else {
			e.tracker = tracker
			e.log.Info("engine.reputation_loaded", "path", cfg.ReputationPath)
			if cfg.SeedListPath != "" {
				n, errs := tracker.LoadSeedList(cfg.SeedListPath)
				for _, err := range errs {
					e.log.Warn("engine.seed_list_err", "err", err)
				}
				if n > 0 {
					e.log.Info("engine.seed_list_loaded", "path", cfg.SeedListPath, "imported", n)
				}
			}
		}
	}
	if cfg.TrustPath != "" {
		store, err := trust.LoadOrCreate(cfg.TrustPath)
		if err != nil {
			// Trust was CONFIGURED but could not be loaded (corrupt/
			// unreadable file). Mark the store degraded so FlagHit fails
			// closed — a nil store must not be mistaken for "trust off",
			// which would silently demote trusted publishers.
			e.trustFailed = true
			e.log.Warn("engine.trust_load_err", "err", err)
		} else {
			e.trust = store
			e.log.Info("engine.trust_loaded", "path", cfg.TrustPath)
		}
	}
}

// KnownGoodBloom returns the known-good Bloom filter (nil when off).
func (e *Engine) KnownGoodBloom() *reputation.BloomFilter {
	e.repMu.Lock()
	defer e.repMu.Unlock()
	return e.bloom
}

// ReputationTracker returns the reputation tracker (nil when off).
func (e *Engine) ReputationTracker() *reputation.Tracker {
	e.repMu.Lock()
	defer e.repMu.Unlock()
	return e.tracker
}

// SourceTracker returns the per-hit attribution tracker (always non-nil).
func (e *Engine) SourceTracker() *reputation.SourceTracker { return e.sources }

// TrustStore returns the publisher allowlist (nil when off).
func (e *Engine) TrustStore() *trust.Store {
	e.repMu.Lock()
	defer e.repMu.Unlock()
	return e.trust
}

// trustDegraded reports whether trust was configured but failed to load, so
// the trusted-publisher exemption cannot be checked. Callers that would
// demote must fail closed.
func (e *Engine) trustDegraded() bool {
	e.repMu.Lock()
	defer e.repMu.Unlock()
	return e.trustFailed
}

// Checkpoint flushes the Bloom and reputation tracker atomically. Each Save
// is tmp+rename, a no-op for in-memory (empty-path) instances, so a
// checkpoint racing Close leaves a consistent file either way. This is
// deterministic code owning the state transition (no LLM, no ambiguous
// limbo) — the crash-safety fix (D23).
func (e *Engine) Checkpoint() {
	// Serialize so the ticker, confirm/flag, completion, and Close never
	// overlap their saves — each Checkpoint's bloom+tracker pair is written
	// as one uninterrupted unit, and the last one to run wins cleanly.
	e.ckptMu.Lock()
	defer e.ckptMu.Unlock()
	bloom, tracker := e.KnownGoodBloom(), e.ReputationTracker()
	if bloom != nil {
		if err := bloom.Save(); err != nil {
			e.log.Warn("engine.bloom_save_err", "err", err)
		}
	}
	if tracker != nil {
		if err := tracker.Save(); err != nil {
			e.log.Warn("engine.reputation_save_err", "err", err)
		}
	}
}

// runReputationCheckpoint flushes Bloom+reputation on a bounded interval so a
// crash loses at most one interval of spam-resistance signal (the legacy
// saved only at clean Close). Auto-confirm and Confirm/Flag additionally
// flush immediately.
func (e *Engine) runReputationCheckpoint() {
	defer e.ckptWG.Done()
	ticker := time.NewTicker(e.checkpointInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.bgCtx.Done():
			return
		case <-ticker.C:
			e.Checkpoint()
		}
	}
}

// ConfirmHit is the confirm half of the one shared path: add the infohash to
// the known-good Bloom and boost the reputation of its attributed indexers
// (an explicit user signal — completion never does this, D22). A nil Bloom
// makes it a no-op that reports notConfigured so the caller answers 503.
func (e *Engine) ConfirmHit(ih [20]byte, ihHex string) (confirmed int, ok bool) {
	bloom := e.KnownGoodBloom()
	if bloom == nil {
		return 0, false
	}
	bloom.Add(ih[:])
	if tracker := e.ReputationTracker(); tracker != nil {
		if pks := e.sources.Sources(ihHex); len(pks) > 0 {
			tracker.RecordConfirmed(pks...)
			confirmed = len(pks)
		}
	}
	e.Checkpoint()
	return confirmed, true
}

// FlagResult reports the outcome of a flag through the shared path.
type FlagResult struct {
	Flagged     int    // indexers actually demoted
	Attribution string // targeted | trusted-exempt | trust-unavailable | none
}

// FlagHit is the flag half of the one shared path: demote ONLY the attributed
// non-trusted indexers, then forget the attribution so a second flag can't
// double-dock. Zero attribution demotes nobody (fail-closed — never the old
// demote-every-indexer fallback). A nil tracker reports notConfigured (503).
func (e *Engine) FlagHit(ihHex string) (FlagResult, bool) {
	tracker := e.ReputationTracker()
	if tracker == nil {
		return FlagResult{}, false
	}
	// Trust was configured but failed to load: we cannot check the
	// exemption, so demoting anyone risks silently demoting a trusted
	// publisher. Fail CLOSED — demote nobody and say so honestly. The
	// attribution is preserved (no Forget) so a retry after the operator
	// fixes trust.json can still act on it.
	if e.trustDegraded() {
		return FlagResult{Attribution: "trust-unavailable"}, true
	}
	pks := e.sources.Sources(ihHex)
	if len(pks) == 0 {
		return FlagResult{Attribution: "none"}, true
	}
	store := e.TrustStore()
	demote := pks[:0:0]
	trustedSkipped := false
	for _, pk := range pks {
		if store != nil && store.IsTrusted(string(pk)) {
			trustedSkipped = true
			continue
		}
		demote = append(demote, pk)
	}
	if len(demote) > 0 {
		tracker.RecordFlagged(demote...)
	}
	e.sources.Forget(ihHex) // no double-dock
	e.Checkpoint()
	attribution := "targeted"
	if len(demote) == 0 && trustedSkipped {
		attribution = "trusted-exempt"
	}
	return FlagResult{Flagged: len(demote), Attribution: attribution}, true
}
