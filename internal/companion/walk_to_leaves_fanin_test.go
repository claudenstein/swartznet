package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"strings"
	"testing"
)

// budgetPageSource counts Piece calls and fails closed once the
// budget is exhausted. It keeps the fan-in regression test fast
// even against code without the shared visited-set, where the
// number of root-to-leaf paths (and hence Piece calls) grows
// Fibonacci-ally — Fib(40) ≈ 10⁸ for the layout below.
type budgetPageSource struct {
	inner  PageSource
	calls  int
	budget int
}

func (b *budgetPageSource) Piece(i int) ([]byte, error) {
	b.calls++
	if b.calls > b.budget {
		return nil, fmt.Errorf("test: piece budget %d exceeded", b.budget)
	}
	return b.inner.Piece(i)
}
func (b *budgetPageSource) NumPieces() int { return b.inner.NumPieces() }

// buildFanInSource hand-assembles a hostile companion file whose
// interior pages form a cross-page fan-in DAG: page i points at
// BOTH i+1 and i+2. Every child is strictly downward, in range and
// strictly increasing within its page, so the per-page child-index
// checks alone cannot reject it — only the shared visited-set can.
// The trailer is properly signed (interior structure is unsigned),
// so OpenBTree accepts the file.
func buildFanInSource(t *testing.T, treePages int) *BytesPageSource {
	t.Helper()
	if treePages < 3 {
		t.Fatalf("need ≥3 tree pages, got %d", treePages)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	n := treePages + 1 // + trailer
	leafIdx := treePages - 1
	var ih [20]byte
	rec := mkRecord(t, pub, priv, "kw", ih, 1712649600, 0)

	data := make([]byte, 0, n*MinPieceSize)
	for i := 0; i < treePages; i++ {
		var page []byte
		var err error
		if i == leafIdx {
			page, err = EncodeLeaf(0, []Record{rec}, MinPieceSize)
		} else {
			kind := PageKindInterior
			if i == 0 {
				kind = PageKindRoot
			}
			children := []InteriorChild{{Separator: nil, ChildIndex: uint32(i + 1)}}
			if i+2 <= leafIdx {
				children = append(children,
					InteriorChild{Separator: []byte{0x01}, ChildIndex: uint32(i + 2)})
			}
			page, err = EncodeInterior(kind, 1, children, MinPieceSize)
		}
		if err != nil {
			t.Fatalf("encode page %d: %v", i, err)
		}
		data = append(data, page...)
	}

	tr := Trailer{
		TrailerVersion: 0x01,
		Seq:            1,
		CreatedTs:      1712649600,
		RootPieceIndex: 0,
		NumPages:       uint32(n),
		NumRecords:     1,
	}
	copy(tr.PubKey[:], pub)
	copy(tr.PublisherSig[:], ed25519.Sign(priv, TrailerSigMessage(tr)))
	trailerPage, err := EncodeTrailer(tr, MinPieceSize)
	if err != nil {
		t.Fatalf("EncodeTrailer: %v", err)
	}
	data = append(data, trailerPage...)

	return &BytesPageSource{Data: data, PieceSize: MinPieceSize}
}

// TestWalkToLeavesRejectsCrossPageFanIn is the regression for the
// blocking exponential-walk DoS: per-page child checks pass a
// i → {i+1, i+2} layout, yet without a shared visited-set the walk
// re-enters shared subtrees once per path and Find("") (which
// defeats all range pruning) explodes to millions of Piece calls
// for a ~40-page file. With the visited-set the walk must instead
// fail closed on the first re-visit, after at most a small multiple
// of NumPieces page fetches.
func TestWalkToLeavesRejectsCrossPageFanIn(t *testing.T) {
	src := buildFanInSource(t, 40)
	r, err := OpenBTree(src)
	if err != nil {
		t.Fatalf("OpenBTree should accept a signed fan-in file: %v", err)
	}
	counter := &budgetPageSource{inner: src, budget: 3 * src.NumPieces()}
	r.src = counter

	_, err = r.Find("")
	if err == nil {
		t.Fatal("Find should fail closed on a cross-page fan-in DAG")
	}
	if !strings.Contains(err.Error(), "reached twice") {
		t.Errorf("error %v, want the visited-set re-visit rejection", err)
	}
	if counter.calls > 3*src.NumPieces() {
		t.Errorf("walk made %d Piece calls for a %d-piece tree, want ≤ %d",
			counter.calls, src.NumPieces(), 3*src.NumPieces())
	}
}

// TestCheckLeafIndices covers the defense-in-depth guard that
// backs Find's leaf loop: duplicate leaf indices or more leaves
// than tree pages must fail closed rather than re-verify and
// re-append the same records.
func TestCheckLeafIndices(t *testing.T) {
	cases := []struct {
		name      string
		leaves    []int
		numPieces int
		wantErr   bool
	}{
		{"empty", nil, 3, false},
		{"distinct", []int{1, 2, 4}, 6, false},
		{"duplicate leaf", []int{1, 2, 1}, 6, true},
		{"too many leaves", []int{0, 1, 2}, 3, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkLeafIndices(c.leaves, c.numPieces)
			if (err != nil) != c.wantErr {
				t.Errorf("checkLeafIndices(%v, %d) err = %v, wantErr %v",
					c.leaves, c.numPieces, err, c.wantErr)
			}
		})
	}
}
