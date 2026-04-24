package swarmsearch

// ServiceBits is a 64-bit bitfield advertising the optional
// sub-features a node supports in its sn_search implementation.
// Modeled after Bitcoin Core's peer "services" field (BIP 9 +
// `src/protocol.h::NODE_*` bits). The v1 sn_search wire format
// treats the capability struct as 4 fixed int fields; a bitfield
// lets future features add new bits without touching the
// handshake schema.
//
// Invariant: **unknown bits must be ignored, never rejected**.
// That's the property that makes feature addition additive
// forever. Bitcoin's services field works because every client
// masks off the bits it cares about and ignores the rest.
//
// Bit assignments below are a numbered table that must never
// shrink. New features claim the next available bit.
type ServiceBits uint64

// Bit assignments. Named constants lock the numbering down.
// New features append; existing bits never move.
const (
	// BitShareLocal: node answers sn_search queries against
	// its full local Bleve index. Corresponds to the old
	// Capabilities.ShareLocal == 2 setting.
	BitShareLocal ServiceBits = 1 << 0

	// BitShareSwarm: node answers queries only for torrents
	// it's currently in a swarm for. Corresponds to the old
	// Capabilities.ShareLocal == 1 setting. Mutually
	// exclusive with BitShareLocal in practice, but both
	// bits exist so future policies can layer them.
	BitShareSwarm ServiceBits = 1 << 1

	// BitFileHits: node can return per-file matches (file
	// path matches, chunk offset). Corresponds to the old
	// Capabilities.FileHits.
	BitFileHits ServiceBits = 1 << 2

	// BitContentHits: node indexes extracted file contents
	// and returns content-level matches. Corresponds to the
	// old Capabilities.ContentHits.
	BitContentHits ServiceBits = 1 << 3

	// BitLayerDPublisher: node publishes keyword → infohash
	// entries to the BEP-44 DHT. Corresponds to the old
	// Capabilities.Publisher.
	BitLayerDPublisher ServiceBits = 1 << 4

	// BitCompanionPublisher: node publishes F3 companion
	// content-index torrents (M11c). v1.0.0+ feature.
	BitCompanionPublisher ServiceBits = 1 << 5

	// BitCompanionSubscriber: node follows one or more
	// companion publishers and ingests their indexes (M11d).
	BitCompanionSubscriber ServiceBits = 1 << 6

	// BitSnippetHighlight: node returns Bleve highlight
	// fragments in content hits (M12e).
	BitSnippetHighlight ServiceBits = 1 << 7

	// BitRegtest: node is running in regtest mode. PEER
	// ANNOUNCEMENT is LOUD so accidentally seeing a regtest
	// node on mainnet is obvious.
	BitRegtest ServiceBits = 1 << 8

	// BitSetReconciliation: node speaks the Aggregate
	// sn_search sync protocol (msg_types 4..8 per SPEC §2).
	// Peers without this bit set MUST NOT receive sync
	// frames; the handler rejects them with code 2 (unsupported
	// scope). Added v0.5.0 with the "Aggregate" redesign.
	BitSetReconciliation ServiceBits = 1 << 9

	// Bits 10-63 are reserved for future features. Always
	// allocate the NEXT available bit for a new feature;
	// never reuse an old bit for a different meaning.
)

// Has reports whether the given bit is set.
func (s ServiceBits) Has(bit ServiceBits) bool {
	return s&bit == bit
}

// With returns a copy of s with the given bit set.
func (s ServiceBits) With(bit ServiceBits) ServiceBits {
	return s | bit
}

// Without returns a copy of s with the given bit cleared.
func (s ServiceBits) Without(bit ServiceBits) ServiceBits {
	return s &^ bit
}

// ToCapabilities extracts the legacy 4-field Capabilities
// struct from this service bitfield. Used during the
// transition period while both representations coexist.
// Bits that don't map to a legacy field are dropped.
func (s ServiceBits) ToCapabilities() Capabilities {
	c := Capabilities{}
	switch {
	case s.Has(BitShareLocal):
		c.ShareLocal = 2
	case s.Has(BitShareSwarm):
		c.ShareLocal = 1
	}
	if s.Has(BitFileHits) {
		c.FileHits = 1
	}
	if s.Has(BitContentHits) {
		c.ContentHits = 1
	}
	if s.Has(BitLayerDPublisher) {
		c.Publisher = 1
	}
	return c
}

// ServicesFromCapabilities encodes a legacy Capabilities
// struct as a ServiceBits bitfield. Unknown legacy values
// map to zero bits, not errors.
func ServicesFromCapabilities(c Capabilities) ServiceBits {
	var s ServiceBits
	switch c.ShareLocal {
	case 2:
		s = s.With(BitShareLocal)
	case 1:
		s = s.With(BitShareSwarm)
	}
	if c.FileHits > 0 {
		s = s.With(BitFileHits)
	}
	if c.ContentHits > 0 {
		s = s.With(BitContentHits)
	}
	if c.Publisher > 0 {
		s = s.With(BitLayerDPublisher)
	}
	return s
}

// DefaultServices returns the service mask corresponding to
// DefaultCapabilities plus all the client-side features a
// current SwartzNet build implements. Future versions add
// more bits; old versions will see unknown bits and must
// ignore them.
func DefaultServices() ServiceBits {
	return BitShareLocal |
		BitFileHits |
		BitContentHits |
		BitCompanionPublisher |
		BitCompanionSubscriber |
		BitSnippetHighlight |
		BitSetReconciliation
}
