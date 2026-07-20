package engine

import (
	"github.com/swartznet/swartznet/contracts/ltepwire"
	"github.com/swartznet/swartznet/internal/swarmsearch"
)

// This file owns the engine's half of the sn_search capability mask (Slice 6).
// The engine is the single source of truth for the announced services bits:
// the HTTP readout (/capabilities, /aggregate) and — in a later slice — the
// outbound peer_announce both call ServicesMask, so a downgrade computed here
// reaches every consumer identically.

// Sharing returns the operator's current sn_search sharing prefs.
func (e *Engine) Sharing() ltepwire.Sharing {
	e.shareMu.Lock()
	defer e.shareMu.Unlock()
	return e.sharing
}

// SetSharing replaces the operator's sharing prefs (runtime-only; not
// persisted). ShareLocal is stored verbatim — clamping to 0..2 is the
// caller's (HTTP boundary) job, and Announced is total over any value.
func (e *Engine) SetSharing(s ltepwire.Sharing) {
	e.shareMu.Lock()
	e.sharing = s
	e.shareMu.Unlock()
}

// RuntimeFacts builds the daemon-owned half of the capability set from LIVE
// node state each call (never cached), so toggles like --no-index are honored
// immediately. Reconciliation (bit 9) is advertised now that the RIBLT sync
// bodies land in Slice 8 — the node both answers and initiates set-recon.
// CompanionPub/Sub stay advertised as build-feature bits (re-gated when the
// companion subsystem lands in Slice 10); SnippetHighlight already works.
func (e *Engine) RuntimeFacts() ltepwire.RuntimeFacts {
	return ltepwire.RuntimeFacts{
		Publishing:       e.publishingActive(),
		Reconciliation:   true,
		Regtest:          e.cfg.Regtest,
		CompanionPub:     true,
		CompanionSub:     true,
		SnippetHighlight: true,
	}
}

// publishingActive reports whether the node advertises the Layer-D publisher
// bit. It mirrors the legacy gate: publishing requires the index to be open
// (--no-index cascades to zero the bit) AND DHT publication not suppressed.
// Actual Layer-D publication is wired in a later slice; until then this is the
// capability-intent gate the readout reflects.
func (e *Engine) publishingActive() bool {
	return !e.cfg.NoIndex && !e.cfg.DisableDHTPublish
}

// ServicesMask is the single call site both the HTTP readout and the wire
// peer_announce use to obtain the live 64-bit services mask.
func (e *Engine) ServicesMask() uint64 {
	return ltepwire.Announced(e.Sharing(), e.RuntimeFacts())
}

// SwarmSearch returns the Layer-S sn_search protocol (never nil after New).
func (e *Engine) SwarmSearch() *swarmsearch.Protocol { return e.swarm }
