package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
)

// failOnPagePageSource wraps a real BytesPageSource but returns
// an error when Piece(failIdx) is called. Lets Find pass past
// OpenBTree's root + trailer fetches, then trip the leaf-fetch
// error arm at piece failIdx.
type failOnPagePageSource struct {
	inner   *BytesPageSource
	failIdx int
}

func (f failOnPagePageSource) NumPieces() int { return f.inner.NumPieces() }
func (f failOnPagePageSource) Piece(idx int) ([]byte, error) {
	if idx == f.failIdx {
		return nil, errors.New("test: simulated leaf-fetch failure")
	}
	return f.inner.Piece(idx)
}

// TestFindLeafFetchError — when src.Piece fails for a leaf
// index Find must wrap the underlying error rather than panic
// on a nil page.
func TestFindLeafFetchError(t *testing.T) {
	t.Parallel()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	var pk [32]byte
	copy(pk[:], pub)
	out, err := BuildBTree(BuildBTreeInput{
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

	src := &BytesPageSource{Data: out.Bytes, PieceSize: MinPieceSize}
	// Open via the real src so OpenBTree's piece(0) + trailer
	// fetches succeed.
	r, err := OpenBTree(src)
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}
	// Then swap in the failing wrapper for the actual Find call.
	// Leaf pages start at index 1 (root=0, trailer=last); fail
	// the first leaf so Find aborts on the very first iteration.
	r.src = failOnPagePageSource{inner: src, failIdx: 1}

	if _, err := r.Find("linux"); err == nil {
		t.Error("Find should propagate the leaf-fetch error")
	}
}

// TestFindLeafDecodeError — same idea but the page bytes
// returned for the leaf are corrupted so DecodeLeaf rejects.
func TestFindLeafDecodeError(t *testing.T) {
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

	src := &BytesPageSource{Data: build.Bytes, PieceSize: MinPieceSize}
	r, err := OpenBTree(src)
	if err != nil {
		t.Fatalf("OpenBTree: %v", err)
	}
	// Corrupt the magic bytes of leaf piece index 1 so
	// DecodeLeaf rejects (after walkToLeaves succeeded).
	leafOff := 1 * MinPieceSize
	for i := 0; i < 6; i++ {
		src.Data[leafOff+i] = 0
	}

	if _, err := r.Find("linux"); err == nil {
		t.Error("Find should reject corrupt leaf page")
	}
}
