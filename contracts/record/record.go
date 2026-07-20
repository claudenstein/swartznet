// Package record is the frozen signed-keyword-record substrate for the
// sn_search Aggregate index. A record maps a keyword to an infohash, signed by
// a publisher. The ElementID (RIBLT reconciliation key), the signature message,
// and the hashcash PoW are cross-implementation wire/hash contracts: a second
// implementation must produce byte-identical output. Deliberately EXCLUDES pow
// and sig from the ElementID so two valid signings of one semantic record
// dedupe. Stdlib crypto only.
package record

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math/bits"
)

// MaxKeywordBytes caps a record's keyword.
const MaxKeywordBytes = 64

// maxPoWBits refuses absurd mining targets.
const maxPoWBits = 40

// Record is one signed keyword→infohash mapping.
type Record struct {
	Pk  [32]byte // publisher ed25519 public key
	Kw  string   // lowercased UTF-8 keyword, 1..MaxKeywordBytes bytes
	Ih  [20]byte // SHA-1 infohash
	T   int64    // unix seconds
	Pow uint64   // hashcash nonce
	Sig [64]byte // ed25519 signature over SigMessage
}

var (
	// ErrKeyword is returned for an empty or oversized keyword.
	ErrKeyword = errors.New("record: keyword must be 1..64 bytes")
	// ErrPoWTarget is returned when a mining target exceeds maxPoWBits.
	ErrPoWTarget = errors.New("record: PoW target too high")
	// ErrBadSig is returned by Verify on a signature mismatch.
	ErrBadSig = errors.New("record: signature verification failed")
	// ErrInsufficientPoW is returned by VerifyPoW below the target.
	ErrInsufficientPoW = errors.New("record: insufficient proof-of-work")
)

// idPreimage is pk || kw || ih || LE64(T) — shared by ElementID and SigMessage.
func (r Record) idPreimage() []byte {
	b := make([]byte, 0, 32+len(r.Kw)+20+8)
	b = append(b, r.Pk[:]...)
	b = append(b, r.Kw...)
	b = append(b, r.Ih[:]...)
	var t [8]byte
	binary.LittleEndian.PutUint64(t[:], uint64(r.T))
	b = append(b, t[:]...)
	return b
}

// ElementID is the RIBLT reconciliation key: SHA-256(pk || kw || ih || LE64(T)).
// It EXCLUDES Pow and Sig, so re-signing (a new nonce/signature of the same
// semantic record) yields the same ID and dedupes.
func (r Record) ElementID() [32]byte {
	return sha256.Sum256(r.idPreimage())
}

// SigMessage is the signed/PoW'd preimage: the ElementID preimage plus
// uvarint(Pow). Pow is INSIDE the signature so a record's PoW cannot be
// stripped or replaced without invalidating the signature.
func (r Record) SigMessage() []byte {
	pre := r.idPreimage()
	var nonce [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(nonce[:], r.Pow)
	return append(pre, nonce[:n]...)
}

// Verify checks the ed25519 signature over SigMessage.
func (r Record) Verify() error {
	if !ed25519.Verify(ed25519.PublicKey(r.Pk[:]), r.SigMessage(), r.Sig[:]) {
		return ErrBadSig
	}
	return nil
}

// leadingZeroBits counts the leading zero bits (MSB-first) of the hash.
func leadingZeroBits(h [32]byte) int {
	n := 0
	for _, b := range h {
		if b == 0 {
			n += 8
			continue
		}
		n += bits.LeadingZeros8(b)
		break
	}
	return n
}

// VerifyPoW checks that SHA-256(SigMessage) has at least minBits leading zero
// bits. minBits == 0 skips the check.
func (r Record) VerifyPoW(minBits int) error {
	if minBits <= 0 {
		return nil
	}
	if leadingZeroBits(sha256.Sum256(r.SigMessage())) < minBits {
		return ErrInsufficientPoW
	}
	return nil
}

// MinePoW finds the lowest nonce whose SigMessage hash has >= bits leading zero
// bits, setting r.Pow. bits == 0 is a no-op; bits > maxPoWBits errors.
func MinePoW(r *Record, bits int) error {
	if bits <= 0 {
		r.Pow = 0
		return nil
	}
	if bits > maxPoWBits {
		return ErrPoWTarget
	}
	for nonce := uint64(0); ; nonce++ {
		r.Pow = nonce
		if leadingZeroBits(sha256.Sum256(r.SigMessage())) >= bits {
			return nil
		}
	}
}

// SignAndMine builds a record, mines the PoW to `bits`, THEN signs (signing
// first would invalidate every nonce but the signed one). Returns the fully
// populated record.
func SignAndMine(priv ed25519.PrivateKey, pub [32]byte, kw string, ih [20]byte, t int64, bits int) (Record, error) {
	if len(kw) < 1 || len(kw) > MaxKeywordBytes {
		return Record{}, ErrKeyword
	}
	r := Record{Pk: pub, Kw: kw, Ih: ih, T: t}
	if err := MinePoW(&r, bits); err != nil {
		return Record{}, err
	}
	sig := ed25519.Sign(priv, r.SigMessage())
	copy(r.Sig[:], sig)
	return r, nil
}
