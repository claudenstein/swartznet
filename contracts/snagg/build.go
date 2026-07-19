package snagg

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"
	"time"
)

// BuildInput is the input to BuildBTree.
type BuildInput struct {
	Records    []Record
	PubKey     [32]byte
	PrivKey    ed25519.PrivateKey // nil ⇒ unsigned (fails VerifyTrailerSig; diagnostic only)
	Seq        uint64
	CreatedTs  int64 // 0 ⇒ time.Now() (breaks byte-determinism; pin for golden vectors)
	MinPoWBits uint8
	PieceSize  int
}

// BuiltTree is the output of BuildBTree.
type BuiltTree struct {
	Bytes       []byte
	NumPages    int
	NumRecords  int
	PieceSize   int
	Fingerprint [32]byte
}

// node is a build-time tree node (leaf or interior).
type node struct {
	records  []Record // leaf only
	children []*node  // interior only
	minKey   []byte
	piece    uint32
}

func (n *node) isLeaf() bool { return n.children == nil }

// BuildBTree packs records into a deterministic signed SNAGG file. Order:
// sort → pack leaves → interior levels bottom-up → top-down piece assignment →
// fingerprint → emit pages → signed trailer.
func BuildBTree(in BuildInput) (BuiltTree, error) {
	if in.PieceSize < MinPieceSize || in.PieceSize > MaxPieceSize {
		return BuiltTree{}, fmt.Errorf("snagg: PieceSize %d outside [%d, %d]", in.PieceSize, MinPieceSize, MaxPieceSize)
	}
	if len(in.Records) == 0 {
		return BuiltTree{}, fmt.Errorf("snagg: BuildBTree needs ≥1 record")
	}
	sorted := append([]Record(nil), in.Records...)
	for _, r := range sorted {
		if len(r.Kw) == 0 {
			return BuiltTree{}, fmt.Errorf("snagg: empty keyword in records")
		}
		if len(r.Kw) > MaxKeywordBytes {
			return BuiltTree{}, fmt.Errorf("snagg: keyword %q exceeds cap %d", r.Kw, MaxKeywordBytes)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return compareRecords(sorted[i], sorted[j]) < 0 })

	leaves, err := packLeaves(sorted, in.PieceSize)
	if err != nil {
		return BuiltTree{}, err
	}
	levels := [][]*node{leaves}
	cur := leaves
	level := 1
	for len(cur) > 1 {
		parents, err := packInteriorLevel(cur, in.PieceSize)
		if err != nil {
			return BuiltTree{}, err
		}
		levels = append(levels, parents)
		cur = parents
		level++
	}
	// Single-leaf tree still gets a synthetic one-child root so every tree is
	// root → … → leaves → trailer (≥3 pieces).
	if len(levels) == 1 {
		root, err := packInteriorLevel(leaves, in.PieceSize)
		if err != nil {
			return BuiltTree{}, err
		}
		levels = append(levels, root)
	}

	// Top-down piece assignment: root at piece 0, leaves last.
	piece := uint32(0)
	for l := len(levels) - 1; l >= 0; l-- {
		for _, nd := range levels[l] {
			nd.piece = piece
			piece++
		}
	}
	numDataPages := int(piece)

	fp, err := fingerprintSorted(sorted)
	if err != nil {
		return BuiltTree{}, err
	}

	pages := make([][]byte, numDataPages)
	for l := len(levels) - 1; l >= 0; l-- {
		isRootLevel := l == len(levels)-1
		for _, nd := range levels[l] {
			var page []byte
			if nd.isLeaf() {
				page, err = EncodeLeaf(l, nd.records, in.PieceSize)
			} else {
				kind := uint8(PageKindInterior)
				if isRootLevel {
					kind = PageKindRoot
				}
				page, err = EncodeInterior(kind, l, interiorChildrenOf(nd), in.PieceSize)
			}
			if err != nil {
				return BuiltTree{}, fmt.Errorf("snagg: encode page piece=%d: %w", nd.piece, err)
			}
			pages[nd.piece] = page
		}
	}

	createdTs := in.CreatedTs
	if createdTs == 0 {
		createdTs = time.Now().Unix()
	}
	tr := Trailer{
		Version:        TrailerVersion,
		PubKey:         in.PubKey,
		Seq:            in.Seq,
		CreatedTs:      uint64(createdTs),
		RootPieceIndex: 0,
		NumPages:       uint32(numDataPages + 1),
		NumRecords:     uint64(len(sorted)),
		MinPoWBits:     in.MinPoWBits,
		Fingerprint:    fp,
	}
	if in.PrivKey != nil {
		SignTrailer(&tr, in.PrivKey)
	}
	trailerPage, err := EncodeTrailer(tr, in.PieceSize)
	if err != nil {
		return BuiltTree{}, err
	}

	out := make([]byte, 0, (numDataPages+1)*in.PieceSize)
	for _, p := range pages {
		out = append(out, p...)
	}
	out = append(out, trailerPage...)

	return BuiltTree{
		Bytes:       out,
		NumPages:    numDataPages + 1,
		NumRecords:  len(sorted),
		PieceSize:   in.PieceSize,
		Fingerprint: fp,
	}, nil
}

// interiorChildrenOf builds the wire child list: separator = child subtree min
// key (EncodeInterior forces the first empty), ChildIndex = assigned piece.
func interiorChildrenOf(nd *node) []InteriorChild {
	out := make([]InteriorChild, len(nd.children))
	for i, c := range nd.children {
		out[i] = InteriorChild{Separator: c.minKey, ChildIndex: c.piece}
	}
	return out
}

func packLeaves(sorted []Record, pieceSize int) ([]*node, error) {
	var leaves []*node
	var cur []Record
	for _, r := range sorted {
		cand := append(append([]Record(nil), cur...), r)
		if _, err := EncodeLeaf(0, cand, pieceSize); err != nil {
			if !errors.Is(err, ErrPageOverflow) {
				return nil, err
			}
			if len(cur) == 0 {
				enc, _ := EncodeRecord(r)
				return nil, fmt.Errorf("snagg: record of %d bytes too large for page %d", len(enc), pieceSize)
			}
			leaves = append(leaves, mkLeaf(cur))
			cur = []Record{r}
			if _, err := EncodeLeaf(0, cur, pieceSize); err != nil {
				enc, _ := EncodeRecord(r)
				return nil, fmt.Errorf("snagg: record of %d bytes too large for page %d", len(enc), pieceSize)
			}
		} else {
			cur = cand
		}
	}
	if len(cur) > 0 {
		leaves = append(leaves, mkLeaf(cur))
	}
	return leaves, nil
}

func packInteriorLevel(children []*node, pieceSize int) ([]*node, error) {
	if len(children) == 0 {
		return nil, fmt.Errorf("snagg: packInteriorLevel empty children")
	}
	var parents []*node
	var cur []*node
	for _, ch := range children {
		cand := append(append([]*node(nil), cur...), ch)
		if _, err := EncodeInterior(PageKindInterior, 1, interiorChildrenOfSlice(cand), pieceSize); err != nil {
			if !errors.Is(err, ErrPageOverflow) {
				return nil, err
			}
			if len(cur) == 0 {
				return nil, fmt.Errorf("snagg: child separator too large for interior page %d", pieceSize)
			}
			parents = append(parents, mkInterior(cur))
			cur = []*node{ch}
			if _, err := EncodeInterior(PageKindInterior, 1, interiorChildrenOfSlice(cur), pieceSize); err != nil {
				return nil, fmt.Errorf("snagg: child separator too large for interior page %d", pieceSize)
			}
		} else {
			cur = cand
		}
	}
	if len(cur) > 0 {
		parents = append(parents, mkInterior(cur))
	}
	return parents, nil
}

func interiorChildrenOfSlice(children []*node) []InteriorChild {
	out := make([]InteriorChild, len(children))
	for i, c := range children {
		out[i] = InteriorChild{Separator: c.minKey, ChildIndex: 0}
	}
	return out
}

func mkLeaf(records []Record) *node {
	rs := append([]Record(nil), records...)
	return &node{records: rs, minKey: RecordKey(rs[0])}
}

func mkInterior(children []*node) *node {
	cs := append([]*node(nil), children...)
	return &node{children: cs, minKey: cs[0].minKey}
}
