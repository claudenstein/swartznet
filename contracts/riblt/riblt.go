// Package riblt is the frozen Rateless IBLT set-reconciliation contract used by
// sn_search Aggregate sync (msg_types 4–8). It is a homegrown minimal
// implementation of Yang et al. (SIGCOMM 2024) with a graduated-degree cycle —
// NOT the yangl1996/riblt library. The element-key hash, the membership cycle,
// the coded-symbol layout, and the peel algorithm are all part of the wire
// contract: a second implementation must produce byte-identical coded symbols
// for the same element set. Stdlib-only, no crypto (element IDs are computed by
// contracts/record).
package riblt

// RIBLTElement is a 32-byte element ID (an SHA-256 record ID, computed
// elsewhere). RIBLT treats it as opaque bytes.
type RIBLTElement [32]byte

// Symbol is one coded symbol. Count is SIGNED: a decoder's diff = sender.Count
// − local.Count can go negative (an element the receiver has but the sender
// lacks).
type Symbol struct {
	Count   int32
	KeyXOR  uint64
	DataXOR [32]byte
}

// Key hashes the whole 32-byte element with FNV-1a-64. Hashing all 32 bytes
// (not a linear prefix) is load-bearing: a linear key would let the decoder
// hallucinate pure symbols from non-pure ones.
func (e RIBLTElement) Key() uint64 {
	var h uint64 = 0xCBF29CE484222325 // FNV offset basis
	for _, b := range e {
		h ^= uint64(b)
		h *= 0x100000001B3 // FNV prime
	}
	return h
}

// contributes reports whether the element with the given key participates in
// coded symbol number symbolIdx. It is a SplitMix64 finalizer of
// key + symbolIdx*golden, taken modulo 2^(1+idx%12) — a 12-step geometric-rate
// cycle (moduli 2,4,8,…,4096; average inclusion rate ≈ 0.083). FROZEN.
func contributes(key, symbolIdx uint64) bool {
	z := key + symbolIdx*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	z = z ^ (z >> 31)
	level := 1 + (symbolIdx % 12) // 1..12
	return z%(uint64(1)<<level) == 0
}

// Contributes is the exported membership test (for golden-vector tests and
// cross-impl verification).
func Contributes(key, symbolIdx uint64) bool { return contributes(key, symbolIdx) }

func xorInto(dst *[32]byte, e RIBLTElement) {
	for i := range dst {
		dst[i] ^= e[i]
	}
}

// Encoder streams coded symbols over a fixed element set. Two encoders over the
// same set (any order) produce identical streams.
type Encoder struct {
	elems   []RIBLTElement
	nextIdx uint64
}

// AddElement adds an element (the caller deduplicates; the encoder does not).
func (e *Encoder) AddElement(el RIBLTElement) { e.elems = append(e.elems, el) }

// Len is the element count (feeds sync_begin local_count).
func (e *Encoder) Len() int { return len(e.elems) }

// NextSymbolIndex is the stream position the next NextSymbol call will emit —
// captured into sync_symbols.index.
func (e *Encoder) NextSymbolIndex() uint64 { return e.nextIdx }

// NextSymbol emits the next coded symbol and advances the stream.
func (e *Encoder) NextSymbol() Symbol {
	var s Symbol
	for _, el := range e.elems {
		if contributes(el.Key(), e.nextIdx) {
			s.Count++
			s.KeyXOR ^= el.Key()
			xorInto(&s.DataXOR, el)
		}
	}
	e.nextIdx++
	return s
}

// Decoder peels the symmetric difference from a stream of the remote's coded
// symbols against the local element set.
type Decoder struct {
	local            []RIBLTElement
	diffSymbols      []Symbol
	decoded          map[RIBLTElement]int
	syntheticAdded   []RIBLTElement // decoded as sender-has / local-lacks (dir +1)
	syntheticRemoved []RIBLTElement // decoded as local-has / sender-lacks (dir −1)
}

// NewDecoder builds an empty decoder.
func NewDecoder() *Decoder { return &Decoder{decoded: make(map[RIBLTElement]int)} }

// AddLocalElement seeds a receiver-side element.
func (d *Decoder) AddLocalElement(el RIBLTElement) { d.local = append(d.local, el) }

// SymbolsIn is how many remote symbols have been applied (feeds the wire
// index-match check).
func (d *Decoder) SymbolsIn() int { return len(d.diffSymbols) }

// AddRemoteSymbol folds one remote coded symbol into the diff at the implicit
// next index and peels.
func (d *Decoder) AddRemoteSymbol(s Symbol) {
	idx := uint64(len(d.diffSymbols))
	ls := d.effectiveLocalSymbol(idx)
	diff := Symbol{Count: s.Count - ls.Count, KeyXOR: s.KeyXOR ^ ls.KeyXOR}
	for i := range diff.DataXOR {
		diff.DataXOR[i] = s.DataXOR[i] ^ ls.DataXOR[i]
	}
	d.diffSymbols = append(d.diffSymbols, diff)
	d.peel()
}

// effectiveLocalSymbol recomputes the local coded symbol at idx over
// local ∪ syntheticAdded \ syntheticRemoved, so already-decoded elements cancel
// against future frames.
func (d *Decoder) effectiveLocalSymbol(idx uint64) Symbol {
	var s Symbol
	add := func(el RIBLTElement, sign int32) {
		if contributes(el.Key(), idx) {
			s.Count += sign
			s.KeyXOR ^= el.Key()
			for i := range s.DataXOR {
				s.DataXOR[i] ^= el[i]
			}
		}
	}
	for _, el := range d.local {
		add(el, 1)
	}
	for _, el := range d.syntheticAdded {
		add(el, 1)
	}
	for _, el := range d.syntheticRemoved {
		add(el, -1)
	}
	return s
}

func (d *Decoder) peel() {
	for {
		changed := false
		for j := range d.diffSymbols {
			s := d.diffSymbols[j]
			if s.Count != 1 && s.Count != -1 {
				continue
			}
			var e RIBLTElement
			copy(e[:], s.DataXOR[:]) // for a pure symbol, DataXOR IS the element ID
			if e.Key() != s.KeyXOR {
				continue // self-consistency gate: rejects ID collisions
			}
			if _, done := d.decoded[e]; done {
				continue
			}
			direction := int(s.Count)
			d.decoded[e] = direction
			if direction > 0 {
				d.syntheticAdded = append(d.syntheticAdded, e)
			} else {
				d.syntheticRemoved = append(d.syntheticRemoved, e)
			}
			// Subtract e out of every symbol it contributes to.
			for k := range d.diffSymbols {
				if contributes(e.Key(), uint64(k)) {
					d.diffSymbols[k].Count -= int32(direction)
					d.diffSymbols[k].KeyXOR ^= e.Key()
					for i := range d.diffSymbols[k].DataXOR {
						d.diffSymbols[k].DataXOR[i] ^= e[i]
					}
				}
			}
			changed = true
			break
		}
		if !changed {
			break
		}
	}
}

// Converged reports whether every residual symbol is fully zero.
func (d *Decoder) Converged() bool {
	var zero [32]byte
	for _, s := range d.diffSymbols {
		if s.Count != 0 || s.KeyXOR != 0 || s.DataXOR != zero {
			return false
		}
	}
	return true
}

// Added returns elements the sender has and the receiver lacks (→ sync_need).
func (d *Decoder) Added() []RIBLTElement {
	out := make([]RIBLTElement, 0)
	for e, dir := range d.decoded {
		if dir > 0 {
			out = append(out, e)
		}
	}
	return out
}

// Removed returns elements the receiver has and the sender lacks (→ push).
func (d *Decoder) Removed() []RIBLTElement {
	out := make([]RIBLTElement, 0)
	for e, dir := range d.decoded {
		if dir < 0 {
			out = append(out, e)
		}
	}
	return out
}
