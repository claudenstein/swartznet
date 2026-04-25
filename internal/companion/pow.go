// Hashcash proof-of-work for Aggregate records.
//
// SPEC.md §1.5: each Record carries a `pow` nonce such that
// SHA256(RecordSigMessage(r)) has at least D leading zero bits,
// where D is the publisher-chosen minimum (typically 20). Readers
// enforce the threshold from Trailer.MinPoWBits; publishers mint
// each record's nonce once at build time.
//
// The purpose is cost-to-publish, not cost-to-verify:
//   mint cost  ≈ 2^D SHA256 ops     (~20 ms at D=20 on a laptop)
//   verify cost = 1 SHA256 op       (constant)
//
// Together with BEP-44 signature requirements and per-IP DHT
// rate limits, D=20 makes 10⁶ spam records cost $0.10-$0.50 of
// cloud CPU versus $0 today.

package companion

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// MineRecordPoW finds the smallest `Pow` nonce (starting from
// minNonce) such that SHA256(RecordSigMessage(r)) has at least
// bits leading zero bits. The returned Record has r.Pow set.
//
// Does NOT produce a signature — after mining, callers MUST sign
// the message (RecordSigMessage uses the freshly-set Pow) and
// copy the result into r.Sig. The ordering matters: signing before
// mining would invalidate the signature for any nonce other than
// the one signed over.
//
// maxIterations bounds brute-force effort. Pass 0 for "no limit"
// (the expected cost is 2^bits which is modest for bits ≤ 24).
func MineRecordPoW(r Record, bits uint8, maxIterations uint64) (Record, error) {
	if bits == 0 {
		return r, nil
	}
	if bits > 40 {
		return r, fmt.Errorf("companion: MineRecordPoW refuses bits=%d (cost prohibitive)", bits)
	}

	// Iterate through candidate nonces until the hash preimage
	// hits the leading-zero-bits threshold. We reuse the buffer
	// produced by recordPreimage to avoid re-allocating on every
	// iteration — the only bytes that change are the nonce varint
	// tail, so we rewrite it in place.
	for iter := uint64(0); maxIterations == 0 || iter < maxIterations; iter++ {
		r.Pow = iter
		sum := sha256.Sum256(RecordSigMessage(r))
		if leadingZeroBitsOfByteSlice(sum[:]) >= int(bits) {
			return r, nil
		}
	}
	return r, ErrPoWExhausted
}

// SignAndMineRecord prepares a Record for publication: fills Pk
// from pub, mines a PoW nonce at `bits` difficulty, then signs
// with priv. The caller supplies everything except Sig and Pow.
//
// On success, the returned record is fully valid: sig verifies
// and PoW holds. On error (including PoW exhaustion), the partial
// record is returned unsigned so callers can diagnose.
func SignAndMineRecord(priv ed25519.PrivateKey, pub ed25519.PublicKey, kw string, ih [20]byte, ts int64, bits uint8) (Record, error) {
	if len(pub) != 32 {
		return Record{}, fmt.Errorf("companion: SignAndMineRecord pub %d bytes, want 32", len(pub))
	}
	var r Record
	copy(r.Pk[:], pub)
	r.Kw = kw
	r.Ih = ih
	r.T = ts

	mined, err := MineRecordPoW(r, bits, 0)
	if err != nil {
		return mined, fmt.Errorf("companion: mine record: %w", err)
	}

	sig := ed25519.Sign(priv, RecordSigMessage(mined))
	copy(mined.Sig[:], sig)
	return mined, nil
}

// ErrPoWExhausted signals MineRecordPoW hit maxIterations without
// finding a valid nonce. Only returned when the caller imposes
// an iteration cap; the default (maxIterations==0) never returns
// this error.
var ErrPoWExhausted = errors.New("companion: PoW iteration budget exhausted")

// leadingZeroBitsOfByteSlice is a duplicate of the helper in
// read_btree.go, kept here so pow.go is a self-contained unit. A
// future refactor could move the shared helper into a new file,
// but keeping them side-by-side is less intrusive today.
func leadingZeroBitsOfByteSlice(b []byte) int {
	n := 0
	for _, x := range b {
		if x == 0 {
			n += 8
			continue
		}
		for mask := byte(0x80); mask != 0; mask >>= 1 {
			if x&mask != 0 {
				return n
			}
			n++
		}
		return n
	}
	return n
}

// recordPreimage returns the bytes a PoW solver feeds to SHA-256.
// Same fields as RecordSigMessage — we keep them semantically
// identical so a verifier doesn't have to recompute the signing
// message separately. The helper is unexported; callers use
// RecordSigMessage for the public API.
func recordPreimage(r Record) []byte {
	buf := make([]byte, 0, 32+len(r.Kw)+20+8+binary.MaxVarintLen64)
	buf = append(buf, r.Pk[:]...)
	buf = append(buf, r.Kw...)
	buf = append(buf, r.Ih[:]...)
	var ts [8]byte
	binary.LittleEndian.PutUint64(ts[:], uint64(r.T))
	buf = append(buf, ts[:]...)
	var nonce [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(nonce[:], r.Pow)
	buf = append(buf, nonce[:n]...)
	return buf
}
