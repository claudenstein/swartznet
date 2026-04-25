package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// errPageSource is a PageSource that always errors on Piece.
// Used to drive OpenBTree's src.Piece(n-1) error branch.
type errPageSource struct {
	pieceCount int
}

func (e errPageSource) NumPieces() int { return e.pieceCount }
func (e errPageSource) Piece(int) ([]byte, error) {
	return nil, errors.New("test: simulated fetch failure")
}

// TestOpenBTreeFetchTrailerError — when src.Piece(n-1) returns
// an error, OpenBTree must wrap it as "fetch trailer" rather
// than panic. Reaches the second error branch in OpenBTree.
func TestOpenBTreeFetchTrailerError(t *testing.T) {
	t.Parallel()
	if _, err := OpenBTree(errPageSource{pieceCount: 5}); err == nil {
		t.Error("expected error from failing Piece fetch")
	}
}

// TestOpenBTreeDecodeTrailerError — corrupt the magic bytes of
// the last page so decodeHeader returns an error before sig
// verification can run. Reaches the DecodeTrailer error branch.
func TestOpenBTreeDecodeTrailerError(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	out, err := BuildBTree(BuildBTreeInput{
		Records:   makeRecords(t, pub, priv, 3, []string{"k"}),
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}
	src := &BytesPageSource{Data: out.Bytes, PieceSize: MinPieceSize}
	// Zero the first 6 bytes of the trailer page → magic check fails.
	lastOff := (src.NumPieces() - 1) * src.PieceSize
	for i := 0; i < 6; i++ {
		src.Data[lastOff+i] = 0
	}
	if _, err := OpenBTree(src); err == nil {
		t.Error("OpenBTree should reject trailer with mangled magic")
	}
}

// TestOpenBTreeNumPagesMismatch — extend the source by one zero
// page so the trailer's NumPages claim doesn't match the actual
// piece count. The trailer signature is still valid, so we get
// past VerifyTrailerSig but trip the NumPages check.
func TestOpenBTreeNumPagesMismatch(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	out, err := BuildBTree(BuildBTreeInput{
		Records:   makeRecords(t, pub, priv, 3, []string{"k"}),
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	// Move the trailer page out by one slot so the source has
	// (claimed pages + 1) pieces. Insert a zero-filled page in
	// the middle.
	originalSize := len(out.Bytes)
	extended := make([]byte, originalSize+MinPieceSize)
	// copy all but the trailer (keep at original positions).
	copy(extended, out.Bytes[:originalSize-MinPieceSize])
	// extended[size-MinPieceSize:size] is zero (the inserted page).
	// trailer goes back at the end.
	copy(extended[originalSize:], out.Bytes[originalSize-MinPieceSize:])

	src := &BytesPageSource{Data: extended, PieceSize: MinPieceSize}
	if _, err := OpenBTree(src); err == nil {
		t.Error("OpenBTree should reject trailer NumPages mismatch")
	}
}
