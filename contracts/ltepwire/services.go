// Package ltepwire is the frozen sn_search capability-negotiation contract:
// the 64-bit `services` bitfield carried in the LTEP peer_announce (msg_type
// 3) and reported by the HTTP /aggregate + /capabilities readouts. It is the
// single source of truth for the announced mask, so a second implementation
// must produce byte-identical output for the same inputs.
//
// The bit numbering is FROZEN and append-only: never renumber a bit, never
// reuse a retired bit, always allocate the next free bit. A receiver honors
// every bit it defines and IGNORES unknown/higher bits — it never rejects a
// frame for them (the additive-forever forward-compat contract). Absence of a
// peer_announce means services 0, which is NOT an error: the peer is still
// answered.
//
// Stdlib-only; the package knows nothing of the engine, the HTTP layer, or
// the wire codec — those consume Announced and FormatHex.
package ltepwire

import "fmt"

// ServiceBits is the 64-bit sn_search services bitfield. Numbering is frozen
// and append-only (bits 10..63 reserved).
type ServiceBits uint64

const (
	// BitShareLocal (bit 0) — answers queries against the full local index
	// (operator ShareLocal level 2).
	BitShareLocal ServiceBits = 1 << 0 // 0x001
	// BitShareSwarm (bit 1) — answers only for torrents currently in-swarm
	// (operator ShareLocal level 1). Mutually exclusive with BitShareLocal.
	BitShareSwarm ServiceBits = 1 << 1 // 0x002
	// BitFileHits (bit 2) — returns per-file matches, not just torrent name.
	BitFileHits ServiceBits = 1 << 2 // 0x004
	// BitContentHits (bit 3) — indexes and returns extracted-content matches.
	BitContentHits ServiceBits = 1 << 3 // 0x008
	// BitLayerDPublisher (bit 4) — publishes keyword→infohash records to the
	// BEP-44 DHT (Layer D). Daemon-owned; tracks live publishing state.
	BitLayerDPublisher ServiceBits = 1 << 4 // 0x010
	// BitCompanionPublisher (bit 5) — publishes companion content-index
	// torrents.
	BitCompanionPublisher ServiceBits = 1 << 5 // 0x020
	// BitCompanionSubscriber (bit 6) — follows companion publishers and
	// ingests their indexes.
	BitCompanionSubscriber ServiceBits = 1 << 6 // 0x040
	// BitSnippetHighlight (bit 7) — returns highlighted snippet fragments.
	BitSnippetHighlight ServiceBits = 1 << 7 // 0x080
	// BitRegtest (bit 8) — regtest mode; deliberately "loud" so accidental
	// regtest↔mainnet cross-connections are obvious in peer state.
	BitRegtest ServiceBits = 1 << 8 // 0x100
	// BitSetReconciliation (bit 9) — speaks the Aggregate RIBLT set-recon
	// sync protocol (required to receive sync frames).
	BitSetReconciliation ServiceBits = 1 << 9 // 0x200
	// BitPeerGossip (bit 10) — speaks sn_peers PEX (msg_type 9) AND consents to
	// having its own address shared onward. A node advertises this to opt into
	// the capable-peer overlay; peers only gossip addresses of nodes that set it.
	BitPeerGossip ServiceBits = 1 << 10 // 0x400

	// Bits 11..63 are reserved. Allocate the next free bit; never reuse.
)

// Has reports whether every bit of `bit` is set in s.
func (s ServiceBits) Has(bit ServiceBits) bool { return s&bit == bit }

// With returns s with `bit` set.
func (s ServiceBits) With(bit ServiceBits) ServiceBits { return s | bit }

// Without returns s with `bit` cleared.
func (s ServiceBits) Without(bit ServiceBits) ServiceBits { return s &^ bit }

// Sharing is the operator-controlled half of the capability set. These map
// directly to the low bits; a downgrade here MUST clear its bit — there is no
// static default floor (that was the legacy §6 defect where downgrades never
// reached the wire).
type Sharing struct {
	// ShareLocal is a tri-state: 2 → BitShareLocal, 1 → BitShareSwarm,
	// 0 (or any other value) → neither. Clamping to 0..2 is an HTTP/config
	// boundary concern; Announced is total over all values.
	ShareLocal uint8
	// FileHits → BitFileHits.
	FileHits bool
	// ContentHits → BitContentHits.
	ContentHits bool
}

// RuntimeFacts is the daemon-owned half of the capability set: bits derived
// from live node state, never settable by the operator's sharing prefs. Keeping
// these separate from Sharing is what makes a "save sharing" that omits the
// Publisher bit unable to clobber it (the legacy §6 defect).
type RuntimeFacts struct {
	// Publishing → BitLayerDPublisher. True iff the node is actively
	// publishing Layer-D records (index open AND DHT publish enabled).
	Publishing bool
	// Reconciliation → BitSetReconciliation.
	Reconciliation bool
	// Regtest → BitRegtest. True iff the node runs in regtest mode.
	Regtest bool
	// CompanionPub → BitCompanionPublisher.
	CompanionPub bool
	// CompanionSub → BitCompanionSubscriber.
	CompanionSub bool
	// SnippetHighlight → BitSnippetHighlight.
	SnippetHighlight bool
	// PeerGossip → BitPeerGossip. True iff the node participates in sn_peers PEX
	// (and thus consents to having its address shared onward).
	PeerGossip bool
}

// Announced is the SINGLE pure producer of the 64-bit services mask,
// consumed identically by the HTTP readout and (in a later slice) the
// outbound peer_announce. It derives every bit straight from its inputs with
// NO static default floor, so an operator downgrade clears its bit. Total over
// all ShareLocal values (∉{1,2} → neither bit 0 nor bit 1).
func Announced(s Sharing, f RuntimeFacts) uint64 {
	var m ServiceBits
	switch s.ShareLocal {
	case 2:
		m |= BitShareLocal // bit 0
	case 1:
		m |= BitShareSwarm // bit 1
	}
	if s.FileHits {
		m |= BitFileHits // bit 2
	}
	if s.ContentHits {
		m |= BitContentHits // bit 3
	}
	if f.Publishing {
		m |= BitLayerDPublisher // bit 4
	}
	if f.CompanionPub {
		m |= BitCompanionPublisher // bit 5
	}
	if f.CompanionSub {
		m |= BitCompanionSubscriber // bit 6
	}
	if f.SnippetHighlight {
		m |= BitSnippetHighlight // bit 7
	}
	if f.Regtest {
		m |= BitRegtest // bit 8
	}
	if f.Reconciliation {
		m |= BitSetReconciliation // bit 9
	}
	if f.PeerGossip {
		m |= BitPeerGossip // bit 10
	}
	return uint64(m)
}

// FormatHex renders a services mask as exactly 16 lowercase hex characters,
// big-endian (most-significant byte first), zero-padded, with no "0x" prefix.
// This is the wire/readout rendering; it is byte-identical to the legacy
// 8-byte-buffer encoder.
func FormatHex(v uint64) string { return fmt.Sprintf("%016x", v) }
