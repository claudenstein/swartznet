package snagg

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
)

// PageSource abstracts the piece store a reader walks.
type PageSource interface {
	Piece(index int) ([]byte, error)
	NumPieces() int
}

// BytesPageSource is an in-memory PageSource over a contiguous SNAGG file.
type BytesPageSource struct {
	Data      []byte
	PieceSize int
}

func (b BytesPageSource) NumPieces() int {
	if b.PieceSize <= 0 { // guard against an integer divide-by-zero on a bad piece size
		return 0
	}
	return len(b.Data) / b.PieceSize
}
func (b BytesPageSource) Piece(index int) ([]byte, error) {
	n := b.NumPieces()
	if index < 0 || index >= n {
		return nil, fmt.Errorf("snagg: piece %d out of range [0, %d)", index, n)
	}
	off := index * b.PieceSize
	return b.Data[off : off+b.PieceSize], nil
}

// Tree is an opened, trailer-verified SNAGG tree.
type Tree struct {
	src     PageSource
	Trailer Trailer
}

// OpenBTree opens and validates a tree: ≥3 pieces, decode + verify trailer
// signature, and the page-count / root-piece invariants. Nothing is trusted
// until this returns cleanly.
func OpenBTree(src PageSource) (*Tree, error) {
	n := src.NumPieces()
	if n < 3 {
		return nil, fmt.Errorf("snagg: tree has %d pages, need ≥3 (root+leaf+trailer)", n)
	}
	last, err := src.Piece(n - 1)
	if err != nil {
		return nil, fmt.Errorf("snagg: fetch trailer: %w", err)
	}
	tr, err := DecodeTrailer(last)
	if err != nil {
		return nil, fmt.Errorf("snagg: decode trailer: %w", err)
	}
	if err := VerifyTrailerSig(tr); err != nil {
		return nil, fmt.Errorf("snagg: trailer signature invalid: %w", err)
	}
	if int(tr.NumPages) != n {
		return nil, fmt.Errorf("snagg: trailer claims %d pages, source has %d", tr.NumPages, n)
	}
	if tr.RootPieceIndex != 0 {
		return nil, fmt.Errorf("snagg: trailer root piece = %d, want 0", tr.RootPieceIndex)
	}
	return &Tree{src: src, Trailer: tr}, nil
}

// Find runs a prefix query, returning records whose keyword has the prefix and
// whose per-record signature (and PoW, when the trailer requires it) verifies.
// It guarantees AUTHENTICITY of what it returns, never COMPLETENESS — interior
// structure is unauthenticated, so a hostile tree can steer a query away from
// legitimate leaves. Callers needing completeness run VerifyFingerprint.
func (t *Tree) Find(prefix string) ([]Record, error) {
	pLo := []byte(prefix)
	pHi := nextPrefix(pLo)

	root, err := t.src.Piece(0)
	if err != nil {
		return nil, fmt.Errorf("snagg: fetch root: %w", err)
	}
	rh, err := decodeHeader(root)
	if err != nil {
		return nil, fmt.Errorf("snagg: root header: %w", err)
	}
	if rh.kind != PageKindRoot {
		return nil, fmt.Errorf("snagg: piece 0 kind = 0x%02x, want root", rh.kind)
	}

	visited := map[int]bool{}
	var leaves []int
	if err := t.walkToLeaves(0, pLo, pHi, visited, &leaves); err != nil {
		return nil, err
	}
	if len(leaves) >= t.src.NumPieces() {
		return nil, fmt.Errorf("snagg: walk returned %d leaves for a %d-piece tree", len(leaves), t.src.NumPieces())
	}
	seen := map[int]bool{}
	for _, li := range leaves {
		if seen[li] {
			return nil, fmt.Errorf("snagg: leaf piece %d returned twice by walk", li)
		}
		seen[li] = true
	}

	var out []Record
	for _, li := range leaves {
		page, err := t.src.Piece(li)
		if err != nil {
			return nil, fmt.Errorf("snagg: fetch leaf %d: %w", li, err)
		}
		recs, err := DecodeLeaf(page)
		if err != nil {
			return nil, fmt.Errorf("snagg: decode leaf %d: %w", li, err)
		}
		for _, r := range recs {
			if !strings.HasPrefix(r.Kw, prefix) {
				continue
			}
			if r.Verify() != nil {
				continue
			}
			if t.Trailer.MinPoWBits > 0 && r.VerifyPoW(int(t.Trailer.MinPoWBits)) != nil {
				continue
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// walkToLeaves DFS-collects leaf pieces whose [lower,upper) overlaps the prefix
// range. A shared visited set fails closed on any re-reached piece (cycle /
// DAG fan-in), and child indices must be strictly increasing and below the
// trailer — bounding a hostile walk to NumPieces fetches.
func (t *Tree) walkToLeaves(pieceIdx int, pLo, pHi []byte, visited map[int]bool, out *[]int) error {
	if visited[pieceIdx] {
		return fmt.Errorf("snagg: piece %d reached twice (cycle or fan-in in interior pages)", pieceIdx)
	}
	visited[pieceIdx] = true
	page, err := t.src.Piece(pieceIdx)
	if err != nil {
		return fmt.Errorf("snagg: fetch piece %d: %w", pieceIdx, err)
	}
	h, err := decodeHeader(page)
	if err != nil {
		return fmt.Errorf("snagg: piece %d header: %w", pieceIdx, err)
	}
	switch h.kind {
	case PageKindLeaf:
		*out = append(*out, pieceIdx)
		return nil
	case PageKindRoot, PageKindInterior:
		children, err := DecodeInterior(page)
		if err != nil {
			return err
		}
		n := t.src.NumPieces()
		prevIdx := pieceIdx
		for i, c := range children {
			ci := int(c.ChildIndex)
			if ci <= prevIdx || ci >= n-1 {
				return fmt.Errorf("snagg: piece %d child %d index %d out of range (must be in (%d, %d))", pieceIdx, i, ci, prevIdx, n-1)
			}
			prevIdx = ci
			lower := c.Separator // nil ⇒ −∞
			var upper []byte     // nil ⇒ +∞
			if i+1 < len(children) {
				upper = children[i+1].Separator
			}
			if rangeOverlapsPrefix(lower, upper, pLo, pHi) {
				if err := t.walkToLeaves(ci, pLo, pHi, visited, out); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return fmt.Errorf("snagg: piece %d unexpected kind 0x%02x", pieceIdx, h.kind)
	}
}

// VerifyFingerprint reconstructs the record stream from every leaf (in stored
// order) and checks it against the trailer fingerprint + record count. This is
// the optional full-integrity pass; it authenticates COMPLETENESS.
func (t *Tree) VerifyFingerprint() error {
	h := sha256.New()
	n := t.src.NumPieces()
	count := uint64(0)
	for i := 0; i < n-1; i++ {
		page, err := t.src.Piece(i)
		if err != nil {
			return err
		}
		hdr, err := decodeHeader(page)
		if err != nil {
			return err
		}
		if hdr.kind != PageKindLeaf {
			continue
		}
		recs, err := DecodeLeaf(page)
		if err != nil {
			return err
		}
		for _, r := range recs {
			if count >= t.Trailer.NumRecords {
				return fmt.Errorf("snagg: more than %d records, trailer claim exceeded", t.Trailer.NumRecords)
			}
			enc, err := EncodeRecord(r)
			if err != nil {
				return err
			}
			h.Write(enc)
			count++
		}
	}
	if count != t.Trailer.NumRecords {
		return fmt.Errorf("snagg: read %d records, trailer claims %d", count, t.Trailer.NumRecords)
	}
	var got [32]byte
	copy(got[:], h.Sum(nil))
	if got != t.Trailer.Fingerprint {
		return fmt.Errorf("snagg: reconstructed fingerprint mismatches trailer")
	}
	return nil
}

// nextPrefix returns the smallest byte slice strictly greater than every slice
// beginning with p (for the half-open [p, nextPrefix(p)) range). All-0xFF ⇒ nil
// (meaning +∞).
func nextPrefix(p []byte) []byte {
	out := append([]byte(nil), p...)
	for len(out) > 0 {
		if out[len(out)-1] < 0xFF {
			out[len(out)-1]++
			return out
		}
		out = out[:len(out)-1]
	}
	return nil
}

// rangeOverlapsPrefix reports whether [lower,upper) overlaps [pLo,pHi). nil
// bounds are ±∞.
func rangeOverlapsPrefix(lower, upper, pLo, pHi []byte) bool {
	// No overlap if upper ≤ pLo, or lower ≥ pHi.
	if upper != nil && bytes.Compare(upper, pLo) <= 0 {
		return false
	}
	if pHi != nil && lower != nil && bytes.Compare(lower, pHi) >= 0 {
		return false
	}
	return true
}
