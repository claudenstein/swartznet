package companion

import (
	"strings"
	"testing"
)

// encodeChildrenPage builds a valid root/interior page in-memory for
// the cycle/DAG regression tests. The interior structure is NOT
// signed, so a malicious publisher can produce arbitrary
// ChildIndex values here while still passing OpenBTree's trailer
// checks.
func encodeChildrenPage(t *testing.T, kind PageKind, children []InteriorChild) []byte {
	t.Helper()
	page, err := EncodeInterior(kind, 0, children, MinPieceSize)
	if err != nil {
		t.Fatalf("EncodeInterior: %v", err)
	}
	return page
}

// TestWalkToLeavesRejectsSelfReferentialRoot is the regression for
// the blocking stack-overflow DoS: a root whose single child points
// at itself (ChildIndex=0) used to recurse forever and crash the
// process with "fatal error: stack overflow". With the
// strictly-downward child-index guard, Find must instead return an
// error.
func TestWalkToLeavesRejectsSelfReferentialRoot(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 30, []string{"alpha", "beta"}, MinPieceSize)
	// Root (piece 0) with one child pointing back at piece 0.
	page := encodeChildrenPage(t, PageKindRoot, []InteriorChild{
		{Separator: nil, ChildIndex: 0},
	})
	r.src = &constPieceSource{inner: r.src, idx: 0, payload: page}

	_, err := r.Find("alpha")
	if err == nil {
		t.Fatal("Find should reject a self-referential root, not recurse")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("unexpected error %v, want a child-index range rejection", err)
	}
}

// TestWalkToLeavesRejectsBackPointer covers a child that points
// upward/backward (ChildIndex <= pieceIdx) on a deeper tree. We
// craft a root whose child points at a higher interior page, then
// make that interior page point back at the root.
func TestWalkToLeavesRejectsBackPointer(t *testing.T) {
	// A tree large enough to have at least one interior level below
	// the root. With many distinct keywords and the minimum piece
	// size the builder is forced to split into multiple leaves +
	// interior pages.
	kws := make([]string, 0, 64)
	for i := 0; i < 64; i++ {
		kws = append(kws, string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+itoa(i))
	}
	r, _, _, _ := buildTestTree(t, 400, kws, MinPieceSize)

	if r.src.NumPieces() < 4 {
		t.Skip("tree too small to exercise a back-pointer at piece 1")
	}
	// Replace piece 1 (an interior or leaf below the root) with an
	// interior page whose child points back at the root (piece 0).
	page := encodeChildrenPage(t, PageKindInterior, []InteriorChild{
		{Separator: nil, ChildIndex: 0},
	})
	r.src = &constPieceSource{inner: r.src, idx: 1, payload: page}

	if _, err := r.Find(""); err == nil {
		t.Fatal("Find should reject an interior page whose child points back at the root")
	}
}

// TestWalkToLeavesRejectsNonIncreasingChildren covers the
// strictly-increasing within-a-page check: two children in the same
// page that are not strictly increasing must be rejected, since an
// honest top-down BFS layout never produces them and a DAG (shared
// subtree) relies on exactly this.
func TestWalkToLeavesRejectsNonIncreasingChildren(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 30, []string{"alpha", "beta"}, MinPieceSize)
	// Root with two children both pointing at piece 1 (a DAG edge).
	page := encodeChildrenPage(t, PageKindRoot, []InteriorChild{
		{Separator: nil, ChildIndex: 1},
		{Separator: []byte("m"), ChildIndex: 1},
	})
	r.src = &constPieceSource{inner: r.src, idx: 0, payload: page}

	if _, err := r.Find("alpha"); err == nil {
		t.Fatal("Find should reject a page with non-strictly-increasing child indices (DAG edge)")
	}
}

// TestWalkToLeavesRejectsChildIntoTrailer covers the upper bound:
// a child that points at the trailer piece (NumPieces-1) or beyond
// must be rejected — the trailer is not a tree page.
func TestWalkToLeavesRejectsChildIntoTrailer(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 30, []string{"alpha"}, MinPieceSize)
	trailerIdx := uint32(r.src.NumPieces() - 1)
	page := encodeChildrenPage(t, PageKindRoot, []InteriorChild{
		{Separator: nil, ChildIndex: trailerIdx},
	})
	r.src = &constPieceSource{inner: r.src, idx: 0, payload: page}

	if _, err := r.Find("alpha"); err == nil {
		t.Fatal("Find should reject a child pointing at the trailer piece")
	}
}

// TestWalkToLeavesHonestTreeStillWorks is a guard against the
// child-index invariant being too strict: a normally-built tree
// must still return all matching records.
func TestWalkToLeavesHonestTreeStillWorks(t *testing.T) {
	r, recs, _, _ := buildTestTree(t, 200, []string{"linux", "ubuntu", "debian"}, MinPieceSize)
	want := 0
	for _, rec := range recs {
		if rec.Kw == "linux" {
			want++
		}
	}
	got, err := r.Find("linux")
	if err != nil {
		t.Fatalf("Find on honest tree: %v", err)
	}
	if len(got) != want {
		t.Fatalf("Find returned %d records, want %d", len(got), want)
	}
}

// itoa is a tiny base-10 helper so the test file does not need
// strconv just for building keyword variety.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
