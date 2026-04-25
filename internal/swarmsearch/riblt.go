// Rateless IBLT set-reconciliation for the sn_search sync protocol.
//
// SPEC.md §2 defines the wire format; this file implements the
// encode/decode primitives. The algorithm is a lightweight variant
// of Rateless IBLT (Yang et al., SIGCOMM 2024):
//
//   - Each element has a 32-byte ID plus a derived uint64 key.
//   - For every symbol index i, element e contributes iff a pure
//     deterministic function of (e.Key, i) selects it. We target
//     ~1/3 of elements per symbol, which is enough to peel an
//     O(d)-sized symmetric difference in O(d) symbols with
//     O(d log d) work.
//   - Symbols are {count, key_xor_sum, data_xor_sum}. Because
//     everything is a signed XOR / integer sum, differences are
//     commutative and invertible — the receiver subtracts their
//     own local symbol at index i from the sender's symbol to
//     obtain a *difference* symbol, then peels pure (|count|=1)
//     symbols iteratively.
//
// The implementation is deliberately simple: no priority queues,
// no external library. O(n) per encoded/consumed symbol and
// O(N²) worst-case peeling, which is fine for the SPEC §2.9
// default budget (≤2000 symbols, ≤50k records).

package swarmsearch

import (
	"errors"
)

// RIBLTElement is the 32-byte record ID used for set membership.
// Wire transport: SPEC §2.4 sets element_size = 32; the 8-byte
// key is the first 8 bytes of the ID interpreted as little-
// endian uint64 (SHA-256 output is already well-distributed so
// no further hashing is needed).
type RIBLTElement [32]byte

// Key is the derived 8-byte identity used in coded symbols.
//
// Must be a NONLINEAR function of the element bytes: a linear key
// (say, first 8 bytes interpreted as uint64) would make every
// symbol look like a "pure" decode target, because XOR over a
// linear function factors through XOR over the input bytes — and
// the receiver would hallucinate decodings that don't correspond
// to any real element. FNV-1a breaks that linearity.
func (e RIBLTElement) Key() uint64 {
	var h uint64 = 0xCBF29CE484222325 // FNV offset basis
	for _, b := range e {
		h ^= uint64(b)
		h *= 0x100000001B3 // FNV prime
	}
	return h
}

// RIBLTSymbol is one wire-level coded symbol.
//
// Count is signed because the receiver computes the *difference*
// symbol as (sender_count - local_count); an element exclusive to
// the receiver flips to negative count during peeling.
type RIBLTSymbol struct {
	Count   int32
	KeyXOR  uint64
	DataXOR [32]byte
}

// contributes is a pure function of (key, symbolIdx) that both
// sender and receiver run to decide element membership in each
// symbol.
//
// The rate varies across symbol positions via a 12-step cycle
// of moduli {2, 4, 8, …, 4096}. Peeling needs at least some
// "pure" (degree-1) symbols to bootstrap; a constant 1/3 rate
// would produce ~d/3 contributors per symbol for difference
// size d, so no pure symbols ever emerge for d ≥ ~5. By cycling
// through rates from 1/2 down to 1/4096, every twelve symbols
// cover a range that matches *some* plausible value of d — for
// d in [2, 4096] at least one cycle position produces expected
// degree ≈ 1, giving pure symbols that kickstart peeling.
func contributes(key uint64, symbolIdx uint64) bool {
	// SplitMix64 mix of (key, index).
	z := key + symbolIdx*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)

	// 12-step geometric cycle: 2, 4, 8, …, 4096.
	level := 1 + (symbolIdx % 12)
	mod := uint64(1) << level
	return z%mod == 0
}

// xorInto XORs src into dst in place, 8 bytes at a time.
func xorInto(dst, src *[32]byte) {
	for i := 0; i < 32; i++ {
		dst[i] ^= src[i]
	}
}

// RIBLTEncoder produces coded symbols for the local set.
type RIBLTEncoder struct {
	elems   []RIBLTElement
	nextIdx uint64
}

// NewRIBLTEncoder returns an empty encoder.
func NewRIBLTEncoder() *RIBLTEncoder { return &RIBLTEncoder{} }

// AddElement adds an element to the encoder's set. Duplicates are
// not deduplicated by the encoder itself — callers should have
// already done that.
func (enc *RIBLTEncoder) AddElement(e RIBLTElement) {
	enc.elems = append(enc.elems, e)
}

// Len is the number of distinct elements the encoder would
// encode. Cheap for callers that want to populate local_count in
// the sync_begin message.
func (enc *RIBLTEncoder) Len() int { return len(enc.elems) }

// NextSymbol produces the next coded symbol in the stream. The
// returned symbol's effective index is (enc.NextSymbolIndex() - 1).
func (enc *RIBLTEncoder) NextSymbol() RIBLTSymbol {
	var s RIBLTSymbol
	for _, e := range enc.elems {
		if !contributes(e.Key(), enc.nextIdx) {
			continue
		}
		s.Count++
		s.KeyXOR ^= e.Key()
		xorInto(&s.DataXOR, (*[32]byte)(&e))
	}
	enc.nextIdx++
	return s
}

// NextSymbolIndex is the index the *next* call to NextSymbol will
// produce — useful for populating sync_symbols.index.
func (enc *RIBLTEncoder) NextSymbolIndex() uint64 { return enc.nextIdx }

// RIBLTDecoder reconciles a peer's set with the local set via
// streamed coded symbols. Usage:
//
//	dec := NewRIBLTDecoder()
//	for each local element: dec.AddLocalElement(e)
//	for each peer symbol s: dec.AddRemoteSymbol(s) // auto-peels
//	if dec.Converged() { ... dec.Added(), dec.Removed() ... }
type RIBLTDecoder struct {
	local       []RIBLTElement
	diffSymbols []RIBLTSymbol

	// decoded maps element → +1 (in sender, not in local) or -1
	// (in local, not in sender). Populated by peeling.
	decoded map[RIBLTElement]int

	// syntheticAdded accumulates elements we've decoded as "in
	// sender but not local" — future local-symbol computations
	// virtually include them so new incoming diff symbols don't
	// re-report the same element at every position it contributes
	// to. syntheticRemoved is the mirror: decoded "in local but
	// not sender" elements that future local computations must
	// virtually exclude.
	syntheticAdded   []RIBLTElement
	syntheticRemoved []RIBLTElement
}

// NewRIBLTDecoder returns a fresh decoder with no local elements.
func NewRIBLTDecoder() *RIBLTDecoder {
	return &RIBLTDecoder{decoded: make(map[RIBLTElement]int)}
}

// AddLocalElement records an element in the receiver's local set.
// Call before streaming remote symbols.
func (dec *RIBLTDecoder) AddLocalElement(e RIBLTElement) {
	dec.local = append(dec.local, e)
}

// AddRemoteSymbol consumes one coded symbol from the peer. The
// symbol's implicit index is the number of calls made so far
// (0-based). After ingest, peeling runs automatically; call
// Converged to check completion.
func (dec *RIBLTDecoder) AddRemoteSymbol(s RIBLTSymbol) {
	idx := uint64(len(dec.diffSymbols))
	ls := dec.effectiveLocalSymbol(idx)

	diff := RIBLTSymbol{
		Count:  s.Count - ls.Count,
		KeyXOR: s.KeyXOR ^ ls.KeyXOR,
	}
	for i := 0; i < 32; i++ {
		diff.DataXOR[i] = s.DataXOR[i] ^ ls.DataXOR[i]
	}
	dec.diffSymbols = append(dec.diffSymbols, diff)

	dec.peel()
}

// effectiveLocalSymbol returns what the receiver's symbol at idx
// looks like given the current known-decoded state. It is local
// ∪ syntheticAdded \ syntheticRemoved: past differences get
// factored in so the difference against new incoming sender
// symbols is zero for already-decoded elements.
func (dec *RIBLTDecoder) effectiveLocalSymbol(idx uint64) RIBLTSymbol {
	var s RIBLTSymbol
	for _, e := range dec.local {
		if contributes(e.Key(), idx) {
			s.Count++
			s.KeyXOR ^= e.Key()
			xorInto(&s.DataXOR, (*[32]byte)(&e))
		}
	}
	for _, e := range dec.syntheticAdded {
		if contributes(e.Key(), idx) {
			s.Count++
			s.KeyXOR ^= e.Key()
			xorInto(&s.DataXOR, (*[32]byte)(&e))
		}
	}
	for _, e := range dec.syntheticRemoved {
		if contributes(e.Key(), idx) {
			s.Count--
			s.KeyXOR ^= e.Key()
			xorInto(&s.DataXOR, (*[32]byte)(&e))
		}
	}
	return s
}

// peel is the iterative pure-symbol consumer. Runs until no pure
// symbol remains or no progress is made.
func (dec *RIBLTDecoder) peel() {
	changed := true
	for changed {
		changed = false
		for i := range dec.diffSymbols {
			s := dec.diffSymbols[i]
			if s.Count != 1 && s.Count != -1 {
				continue
			}
			// A pure symbol's DataXOR is a single element's ID
			// and its KeyXOR must be that element's key. Verify
			// self-consistency to filter ID collisions.
			var e RIBLTElement
			copy(e[:], s.DataXOR[:])
			if e.Key() != s.KeyXOR {
				continue
			}
			// Avoid double-decoding (should be impossible after
			// this symbol is zeroed out, but cheap to assert).
			if _, already := dec.decoded[e]; already {
				continue
			}

			direction := int(s.Count)
			dec.decoded[e] = direction
			// Record for future local-symbol computation: if
			// sender has it but receiver doesn't (dir>0), future
			// effective-local must include it. Mirror the other
			// direction.
			if direction > 0 {
				dec.syntheticAdded = append(dec.syntheticAdded, e)
			} else {
				dec.syntheticRemoved = append(dec.syntheticRemoved, e)
			}

			// Remove this element from every PAST symbol it
			// contributes to. Adjust count by the same direction
			// the pure symbol carried.
			for j := range dec.diffSymbols {
				if !contributes(e.Key(), uint64(j)) {
					continue
				}
				dec.diffSymbols[j].Count -= int32(direction)
				dec.diffSymbols[j].KeyXOR ^= e.Key()
				xorInto(&dec.diffSymbols[j].DataXOR, (*[32]byte)(&e))
			}
			changed = true
			break
		}
	}
}

// Converged reports whether every remaining difference symbol is
// zero. If true, Added / Removed describe the complete symmetric
// difference between the sender's and receiver's sets.
func (dec *RIBLTDecoder) Converged() bool {
	for _, s := range dec.diffSymbols {
		if s.Count != 0 || s.KeyXOR != 0 {
			return false
		}
		for _, b := range s.DataXOR {
			if b != 0 {
				return false
			}
		}
	}
	return true
}

// Added returns elements in the sender's set but not the
// receiver's. Valid after Converged() returns true.
func (dec *RIBLTDecoder) Added() []RIBLTElement {
	out := make([]RIBLTElement, 0)
	for e, dir := range dec.decoded {
		if dir > 0 {
			out = append(out, e)
		}
	}
	return out
}

// Removed returns elements the receiver has that the sender
// lacks. Valid after Converged() returns true.
func (dec *RIBLTDecoder) Removed() []RIBLTElement {
	out := make([]RIBLTElement, 0)
	for e, dir := range dec.decoded {
		if dir < 0 {
			out = append(out, e)
		}
	}
	return out
}

// SymbolsConsumed is the number of remote symbols ingested so far.
// Exposed mostly for regression gates.
func (dec *RIBLTDecoder) SymbolsConsumed() int { return len(dec.diffSymbols) }

// ErrSymbolBudgetExceeded signals that the receiver gave up before
// decoding converged. Returned by helpers that run the stream on
// behalf of callers; bare AddRemoteSymbol does not enforce a limit.
var ErrSymbolBudgetExceeded = errors.New("swarmsearch: RIBLT symbol budget exceeded")
