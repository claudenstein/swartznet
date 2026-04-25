// Aggregate B-tree reader — the subscriber side of SPEC.md §1.7.
//
// The companion-index torrent file is a sequence of pages, each
// one BitTorrent piece wide. A reader fetches the trailer (last
// piece) first, verifies the publisher signature, then walks from
// the root (piece 0) down to whichever leaves overlap the query
// prefix. Only those pieces are pulled — for a well-shaped tree
// and a narrow prefix this is O(log N) root-to-leaf + a small
// number of leaf pieces.

package companion

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
)

// PageSource provides random-access reads into a torrent's pieces.
// The real subscriber implementation wraps an anacrolix
// torrent.Torrent; tests use BytesPageSource against an in-memory
// byte slice.
type PageSource interface {
	Piece(index int) ([]byte, error)
	NumPieces() int
}

// BytesPageSource is an in-memory PageSource for tests and
// for subscribers that have chosen to fully download and buffer
// the companion-index file before querying it.
type BytesPageSource struct {
	Data      []byte
	PieceSize int
}

// Piece returns the raw bytes of the page at index.
func (b *BytesPageSource) Piece(index int) ([]byte, error) {
	if index < 0 || index >= b.NumPieces() {
		return nil, fmt.Errorf("companion: piece %d out of range [0, %d)",
			index, b.NumPieces())
	}
	off := index * b.PieceSize
	return b.Data[off : off+b.PieceSize], nil
}

// NumPieces is the number of pieces in Data.
func (b *BytesPageSource) NumPieces() int {
	if b.PieceSize <= 0 {
		return 0
	}
	return len(b.Data) / b.PieceSize
}

// BTreeReader opens one Aggregate index-torrent tree for
// prefix queries.
type BTreeReader struct {
	src     PageSource
	trailer Trailer
}

// OpenBTree returns a reader rooted at the first piece of src,
// with the trailer signature verified. Callers MUST trust the
// tree only after OpenBTree returns without error — the trailer
// sig is the binding to the publisher's declared identity.
func OpenBTree(src PageSource) (*BTreeReader, error) {
	n := src.NumPieces()
	if n < 3 {
		return nil, fmt.Errorf("companion: tree has %d pages, need ≥3 (root+leaf+trailer)", n)
	}

	lastPage, err := src.Piece(n - 1)
	if err != nil {
		return nil, fmt.Errorf("companion: fetch trailer: %w", err)
	}
	trailer, err := DecodeTrailer(lastPage)
	if err != nil {
		return nil, fmt.Errorf("companion: decode trailer: %w", err)
	}
	if err := VerifyTrailerSig(trailer); err != nil {
		return nil, fmt.Errorf("companion: trailer signature invalid: %w", err)
	}
	if trailer.NumPages != uint32(n) {
		return nil, fmt.Errorf("companion: trailer claims %d pages, source has %d",
			trailer.NumPages, n)
	}
	if trailer.RootPieceIndex != 0 {
		return nil, fmt.Errorf("companion: trailer root piece = %d, want 0",
			trailer.RootPieceIndex)
	}

	return &BTreeReader{src: src, trailer: trailer}, nil
}

// Trailer returns the verified trailer metadata.
func (r *BTreeReader) Trailer() Trailer { return r.trailer }

// Find returns every record whose keyword starts with prefix.
// Each record's ed25519 signature is verified; records failing
// verification are silently dropped (we do not fail the whole
// query on one bad leaf — there is nothing a subscriber can do
// differently for specific bad records once the trailer sig has
// checked out).
//
// PoW enforcement is governed by the trailer's MinPoWBits: when
// non-zero, records whose PoW has fewer than MinPoWBits leading
// zero bits are also dropped. MinPoWBits==0 disables the check
// (test mode / v1.0 back-compat).
func (r *BTreeReader) Find(prefix string) ([]Record, error) {
	pLo := []byte(prefix)
	pHi := nextPrefix(pLo) // nil ⇒ +∞

	rootPage, err := r.src.Piece(0)
	if err != nil {
		return nil, fmt.Errorf("companion: fetch root: %w", err)
	}
	hdr, err := decodeHeader(rootPage)
	if err != nil {
		return nil, fmt.Errorf("companion: root header: %w", err)
	}
	if hdr.Kind != PageKindRoot {
		return nil, fmt.Errorf("companion: piece 0 kind = 0x%02x, want root", hdr.Kind)
	}

	leafPieces, err := r.walkToLeaves(0, pLo, pHi)
	if err != nil {
		return nil, err
	}

	var out []Record
	for _, idx := range leafPieces {
		page, err := r.src.Piece(idx)
		if err != nil {
			return nil, fmt.Errorf("companion: fetch leaf %d: %w", idx, err)
		}
		_, recs, err := DecodeLeaf(page)
		if err != nil {
			return nil, fmt.Errorf("companion: decode leaf %d: %w", idx, err)
		}
		for _, rec := range recs {
			if !bytes.HasPrefix([]byte(rec.Kw), pLo) {
				continue
			}
			if err := VerifyRecordSig(rec); err != nil {
				continue
			}
			if r.trailer.MinPoWBits > 0 {
				if err := VerifyRecordPoW(rec, r.trailer.MinPoWBits); err != nil {
					continue
				}
			}
			out = append(out, rec)
		}
	}
	return out, nil
}

// walkToLeaves does a DFS from the given interior/root page,
// collecting leaf piece indices whose subtree overlaps [pLo, pHi).
// A nil pHi is treated as +∞.
func (r *BTreeReader) walkToLeaves(pieceIdx int, pLo, pHi []byte) ([]int, error) {
	page, err := r.src.Piece(pieceIdx)
	if err != nil {
		return nil, fmt.Errorf("companion: fetch piece %d: %w", pieceIdx, err)
	}
	hdr, err := decodeHeader(page)
	if err != nil {
		return nil, fmt.Errorf("companion: piece %d header: %w", pieceIdx, err)
	}

	if hdr.Kind == PageKindLeaf {
		return []int{pieceIdx}, nil
	}
	if hdr.Kind != PageKindInterior && hdr.Kind != PageKindRoot {
		return nil, fmt.Errorf("companion: piece %d unexpected kind 0x%02x",
			pieceIdx, hdr.Kind)
	}

	_, children, err := DecodeInterior(page)
	if err != nil {
		return nil, err
	}
	// Make a safe copy of each separator — DecodeInterior aliases
	// the input page buffer, and we recursively fetch other
	// pages that will mutate that buffer on reuse.
	copied := make([]InteriorChild, len(children))
	for i, c := range children {
		sep := make([]byte, len(c.Separator))
		copy(sep, c.Separator)
		copied[i] = InteriorChild{Separator: sep, ChildIndex: c.ChildIndex}
	}

	var out []int
	for i, ch := range copied {
		// Compute effective [lower, upper) range for this child.
		// First child (i==0) has effective lower = -∞ (nil); all
		// subsequent children use the preceding child's separator
		// to determine their lower bound. Hmm — actually our
		// encoding stores sep[i] as the minKey of child i itself,
		// not as the separator between i-1 and i. Reading back:
		//   - ch.Separator is the min key of ch's subtree.
		//   - First child's Separator is empty (= -∞).
		// So upper bound of child i = lower bound of child i+1
		// (or +∞ for the last child).
		var lower []byte = ch.Separator // might be nil for first
		var upper []byte
		if i+1 < len(copied) {
			upper = copied[i+1].Separator
		}
		if !rangeOverlapsPrefix(lower, upper, pLo, pHi) {
			continue
		}
		leaves, err := r.walkToLeaves(int(ch.ChildIndex), pLo, pHi)
		if err != nil {
			return nil, err
		}
		out = append(out, leaves...)
	}
	return out, nil
}

// VerifyFingerprint re-derives the canonical fingerprint by
// reading every leaf in piece-index order and hashing each
// record's canonical bytes. Returns nil iff the derived
// fingerprint matches the trailer's TreeFingerprint.
//
// This is the "full integrity check" a cautious subscriber runs
// after fetching the entire file. Everyday prefix queries skip it
// because per-record signatures already prevent tampered leaves
// from polluting the results.
func (r *BTreeReader) VerifyFingerprint() error {
	h := sha256.New()
	count := 0
	for i := 0; i < r.src.NumPieces()-1; i++ {
		page, err := r.src.Piece(i)
		if err != nil {
			return fmt.Errorf("companion: fetch piece %d: %w", i, err)
		}
		hdr, err := decodeHeader(page)
		if err != nil {
			return err
		}
		if hdr.Kind != PageKindLeaf {
			continue
		}
		_, recs, err := DecodeLeaf(page)
		if err != nil {
			return err
		}
		for _, rec := range recs {
			enc, err := EncodeRecord(rec)
			if err != nil {
				return err
			}
			h.Write(enc)
			count++
		}
	}
	if uint64(count) != r.trailer.NumRecords {
		return fmt.Errorf("companion: read %d records, trailer claims %d",
			count, r.trailer.NumRecords)
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != r.trailer.TreeFingerprint {
		return errors.New("companion: reconstructed fingerprint mismatches trailer")
	}
	return nil
}

// nextPrefix returns the smallest byte slice strictly greater
// than every slice starting with p. For "ubu" that's "ubv"; for
// "ub\xFF" it's "uc"; for "\xFF\xFF\xFF" it returns nil (there is
// no such finite slice, i.e. +∞).
func nextPrefix(p []byte) []byte {
	out := make([]byte, len(p))
	copy(out, p)
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] < 0xFF {
			out[i]++
			return out[:i+1]
		}
	}
	return nil
}

// rangeOverlapsPrefix reports whether the half-open child range
// [lower, upper) has any overlap with the prefix range [pLo, pHi).
// lower==nil means -∞; upper==nil or pHi==nil means +∞.
func rangeOverlapsPrefix(lower, upper, pLo, pHi []byte) bool {
	// upper ≤ pLo  →  child entirely below prefix range
	if upper != nil && bytes.Compare(upper, pLo) <= 0 {
		return false
	}
	// lower ≥ pHi  →  child entirely above prefix range
	if pHi != nil && lower != nil && bytes.Compare(lower, pHi) >= 0 {
		return false
	}
	return true
}

// VerifyRecordPoW returns nil iff SHA256(RecordSigMessage(rec))
// has at least minBits leading zero bits. Used by the reader to
// reject un-minted records when the trailer declares a non-zero
// minimum. Lives in this file because P5.1 has not yet introduced
// its own pow.go; once it does, both files can share.
func VerifyRecordPoW(rec Record, minBits uint8) error {
	if minBits == 0 {
		return nil
	}
	sum := sha256.Sum256(RecordSigMessage(rec))
	if leadingZeroBits(sum[:]) < int(minBits) {
		return fmt.Errorf("companion: record PoW %d bits < required %d",
			leadingZeroBits(sum[:]), minBits)
	}
	return nil
}

// leadingZeroBits counts the leading zero bits in b, MSB first.
func leadingZeroBits(b []byte) int {
	n := 0
	for _, x := range b {
		if x == 0 {
			n += 8
			continue
		}
		// Table-free count-leading-zeros on a byte.
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
