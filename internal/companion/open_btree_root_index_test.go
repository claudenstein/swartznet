package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
)

// TestOpenBTreeRejectsNonZeroRootIndex covers OpenBTree's
// `if trailer.RootPieceIndex != 0 { return nil, ... }` guard.
// Build a real, signed tree, then re-encode the trailer with
// RootPieceIndex set to 1 (and re-sign), splice it back into
// the bytes, and assert OpenBTree rejects it.
func TestOpenBTreeRejectsNonZeroRootIndex(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	build, err := BuildBTree(BuildBTreeInput{
		Records:   makeRecords(t, pub, priv, 6, []string{"linux"}),
		PubKey:    pk,
		PrivKey:   priv,
		Seq:       1,
		PieceSize: MinPieceSize,
		CreatedTs: 1,
	})
	if err != nil {
		t.Fatalf("BuildBTree: %v", err)
	}

	// Decode the existing trailer, mutate the root pointer, re-sign.
	src := &BytesPageSource{Data: build.Bytes, PieceSize: MinPieceSize}
	last := src.NumPieces() - 1
	page, err := src.Piece(last)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := DecodeTrailer(page)
	if err != nil {
		t.Fatal(err)
	}
	tr.RootPieceIndex = 1 // poison
	sig := ed25519.Sign(priv, TrailerSigMessage(tr))
	copy(tr.PublisherSig[:], sig)

	doctored, err := EncodeTrailer(tr, MinPieceSize)
	if err != nil {
		t.Fatal(err)
	}
	off := last * MinPieceSize
	copy(build.Bytes[off:off+MinPieceSize], doctored)

	if _, err := OpenBTree(&BytesPageSource{Data: build.Bytes, PieceSize: MinPieceSize}); err == nil {
		t.Error("OpenBTree should reject trailer whose RootPieceIndex != 0")
	}
}
