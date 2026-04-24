// Aggregate (B-tree) companion-index builder.
//
// Takes a slice of signed Records and a publisher identity,
// emits the complete file contents of an Aggregate index torrent:
//   - piece 0              : root page
//   - pieces 1..N-2        : interior / leaf pages (BFS order)
//   - piece  N-1           : trailer (signed)
//
// The output is deterministic: identical input Records + identical
// identity produce identical bytes, which is what lets two
// publishers arrive at the same PPMI commit fingerprint independently.
//
// Layout algorithm:
//
//   1. Sort records by RecordKey.
//   2. Greedy-pack leaves: keep adding records until the next one
//      would blow the page budget, then start a fresh leaf.
//   3. Build interior levels bottom-up by the same greedy-pack rule
//      applied to "separator + child_piece_index" entries.
//   4. Stop when a level collapses to one page → that's the root.
//   5. Assign piece indices: BFS top-down so level L's pages occupy
//      a contiguous slab immediately after level L-1's slab.
//   6. Rewrite interior/root pages with the now-known child indices.
//   7. Compute tree_fingerprint over the canonical record stream
//      (same bytes every publisher would compute for the same set).
//   8. Sign the trailer with the publisher's private ed25519 key.
//   9. Concatenate page bytes (zero-padded to pieceSize each) and
//      the trailer page.

package companion

import (
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"time"
)

// MinPieceSize is the smallest piece (= page) size we accept. Tiny
// pieces can't hold even a single record plus header, and force
// pathologically deep trees.
const MinPieceSize = 16384

// MaxPieceSize caps page byte budgets so a malformed input can't
// request a page too large to survive the encode path (uint16
// payload length). 4 MiB is the biggest piece length we expect in
// practice.
const MaxPieceSize = 4 * 1024 * 1024

// BuildBTreeInput parameterises the builder.
type BuildBTreeInput struct {
	// Records is the publisher's full record set for this
	// snapshot. Caller may pass unsorted; the builder sorts in
	// place (a shallow copy is made first so the caller's slice
	// order is preserved).
	Records []Record

	// PubKey is the publisher's ed25519 public key. Must match
	// the private key below.
	PubKey [32]byte

	// PrivKey signs the trailer. A nil PrivKey is allowed for
	// test/diagnostic builds where we only want to confirm the
	// layout; the resulting tree will fail VerifyTrailerSig.
	PrivKey ed25519.PrivateKey

	// Seq matches the PPMI sequence at publish time. Embedded in
	// the trailer so readers can sanity-check pointer ↔ tree.
	Seq uint64

	// PieceSize is the BitTorrent piece length. MUST be the same
	// value passed to the .torrent metainfo. Every page is exactly
	// this many bytes.
	PieceSize int

	// MinPoWBits is the hashcash difficulty readers will enforce.
	// Defaults to MinPoWBitsDefault when zero.
	MinPoWBits uint8

	// CreatedTs is the timestamp to embed in the trailer. Defaults
	// to time.Now() when zero.
	CreatedTs int64
}

// BuildBTreeOutput is everything a caller needs to wrap the bytes
// in a .torrent and publish a matching PPMI.
type BuildBTreeOutput struct {
	// Bytes is the full file contents, length = NumPages × PieceSize.
	Bytes []byte

	// NumPages is the total page count (includes trailer).
	NumPages int

	// NumRecords is the record count used.
	NumRecords int

	// TreeFingerprint is the SHA-256 that goes into the PPMI's
	// commit field. Readers verify companion-torrent integrity
	// by re-deriving this fingerprint from the leaf records and
	// matching against the PPMI.
	TreeFingerprint [32]byte
}

// BuildBTree assembles an Aggregate companion-index file from a
// record set + publisher identity. Caller is responsible for
// wrapping the result in a .torrent via anacrolix's
// metainfo.Info.BuildFromFilePath (after writing the bytes to
// disk) or equivalent.
func BuildBTree(in BuildBTreeInput) (BuildBTreeOutput, error) {
	var out BuildBTreeOutput
	if in.PieceSize < MinPieceSize || in.PieceSize > MaxPieceSize {
		return out, fmt.Errorf("companion: PieceSize %d outside [%d, %d]",
			in.PieceSize, MinPieceSize, MaxPieceSize)
	}
	if len(in.Records) == 0 {
		return out, errors.New("companion: BuildBTree needs ≥1 record")
	}

	// Defensive copy so callers can reuse their slices. We sort
	// by RecordKey below which would mutate their order otherwise.
	records := make([]Record, len(in.Records))
	copy(records, in.Records)
	sort.Slice(records, func(i, j int) bool {
		return compareRecords(records[i], records[j]) < 0
	})

	powBits := in.MinPoWBits
	if powBits == 0 {
		powBits = MinPoWBitsDefault
	}

	// Step 1: greedy-pack leaves.
	leafGroups, err := packLeaves(records, in.PieceSize)
	if err != nil {
		return out, err
	}

	// Step 2: build interior levels bottom-up. "level 0" is the
	// leaves; each new level is constructed from the previous one.
	// When a level reduces to a single page it becomes the root.
	//
	// Special case: a single-leaf tree still needs a root page
	// because SPEC §1.3 makes root (kind 0x00) distinct from leaf
	// (kind 0x02). Wrap with a trivial one-child root so every
	// tree has the same shape: root → … → leaves → trailer.
	levels := [][]pageBuild{leafPageBuilds(leafGroups)}
	for len(levels[len(levels)-1]) > 1 {
		prev := levels[len(levels)-1]
		next, err := packInteriorLevel(prev, in.PieceSize)
		if err != nil {
			return out, err
		}
		levels = append(levels, next)
	}
	if len(levels) == 1 {
		leaf := levels[0][0]
		root := pageBuild{
			level:    1,
			isRoot:   true,
			children: []InteriorChild{{Separator: nil, ChildIndex: 0}},
			minKey:   leaf.minKey,
		}
		levels = append(levels, []pageBuild{root})
	}

	// Step 3: BFS piece assignment. We lay out levels in order:
	// root first, then level L-2, then L-3, ..., ending with
	// leaves. Trailer goes last.
	totalDataPages := 0
	for _, lv := range levels {
		totalDataPages += len(lv)
	}

	// The deepest level is levels[0] (leaves); the shallowest is
	// levels[N-1] (single-page root). BFS layout: emit root first,
	// then each level below it. Reverse levels to iterate from
	// top down.
	topDown := make([][]pageBuild, len(levels))
	for i, lv := range levels {
		topDown[len(levels)-1-i] = lv
	}

	// Assign piece indices in top-down order.
	pieceIdx := 0
	for l, lv := range topDown {
		for i := range lv {
			lv[i].pieceIndex = pieceIdx
			pieceIdx++
		}
		_ = l
	}

	// Step 4: rewrite interior/root pages with correct child
	// piece indices. For each page at level L (L>0), its children
	// are the consecutive pages at level L-1 consumed in order
	// across all pages at level L. Track a running cursor.
	for l := 0; l < len(topDown)-1; l++ {
		childCursor := 0
		for i := range topDown[l] {
			page := &topDown[l][i]
			for j := range page.children {
				childPage := topDown[l+1][childCursor]
				page.children[j].ChildIndex = uint32(childPage.pieceIndex)
				childCursor++
			}
		}
		if childCursor != len(topDown[l+1]) {
			return out, fmt.Errorf("companion: layout mismatch at level %d: consumed %d children, have %d",
				l, childCursor, len(topDown[l+1]))
		}
	}

	// Step 5: compute tree fingerprint over canonical record stream.
	//
	// Canonical form = concatenation of EncodeRecord(r) for each
	// r in sorted order. This is independent of page layout — two
	// publishers with identical records produce identical
	// fingerprints even if their pieceSize choices differ.
	h := sha256.New()
	for _, r := range records {
		enc, err := EncodeRecord(r)
		if err != nil {
			return out, err
		}
		h.Write(enc)
	}
	var fingerprint [32]byte
	copy(fingerprint[:], h.Sum(nil))

	// Step 6: emit all pages in piece-index order, then the trailer.
	numPages := totalDataPages + 1 // +1 for trailer
	fileBytes := make([]byte, numPages*in.PieceSize)

	// Build a flat slice of pages indexed by pieceIndex for easy
	// lookup during encoding.
	pageOrder := make([]*pageBuild, totalDataPages)
	for l := range topDown {
		for i := range topDown[l] {
			pg := &topDown[l][i]
			pageOrder[pg.pieceIndex] = pg
		}
	}

	for idx, pg := range pageOrder {
		var pageBytes []byte
		var err error
		if pg.isLeaf {
			pageBytes, err = EncodeLeaf(uint8(pg.level), pg.records, in.PieceSize)
		} else {
			kind := PageKindInterior
			if pg.isRoot {
				kind = PageKindRoot
			}
			pageBytes, err = EncodeInterior(kind, uint8(pg.level), pg.children, in.PieceSize)
		}
		if err != nil {
			return out, fmt.Errorf("companion: encode page piece=%d: %w", idx, err)
		}
		off := idx * in.PieceSize
		copy(fileBytes[off:off+in.PieceSize], pageBytes)
	}

	// Step 7: trailer.
	createdTs := in.CreatedTs
	if createdTs == 0 {
		createdTs = time.Now().Unix()
	}
	trailer := Trailer{
		TrailerVersion:  0x01,
		PubKey:          in.PubKey,
		Seq:             in.Seq,
		CreatedTs:       uint64(createdTs),
		RootPieceIndex:  0, // invariant per SPEC §1.2
		NumPages:        uint32(numPages),
		NumRecords:      uint64(len(records)),
		MinPoWBits:      powBits,
		TreeFingerprint: fingerprint,
	}
	if in.PrivKey != nil {
		sig := ed25519.Sign(in.PrivKey, TrailerSigMessage(trailer))
		copy(trailer.PublisherSig[:], sig)
	}
	trailerPage, err := EncodeTrailer(trailer, in.PieceSize)
	if err != nil {
		return out, err
	}
	off := (numPages - 1) * in.PieceSize
	copy(fileBytes[off:off+in.PieceSize], trailerPage)

	out.Bytes = fileBytes
	out.NumPages = numPages
	out.NumRecords = len(records)
	out.TreeFingerprint = fingerprint
	return out, nil
}

// pageBuild is the builder's internal representation of one page,
// pre-encode. We hold children and records here separately because
// an interior page's child indices aren't known until after the
// top-down BFS layout pass.
type pageBuild struct {
	level      int  // 0 = leaf; larger = closer to root
	isLeaf     bool // true iff records != nil
	isRoot     bool // true iff single page at the highest level

	// Only for leaves:
	records []Record

	// Only for interior/root pages:
	children []InteriorChild

	// minKey is the smallest key in this subtree. Used by the
	// parent level to derive its separators.
	minKey []byte

	// pieceIndex is assigned during BFS layout (step 3 above).
	pieceIndex int
}

// leafPageBuilds converts leaf record groups into pageBuild entries.
func leafPageBuilds(groups [][]Record) []pageBuild {
	out := make([]pageBuild, len(groups))
	for i, g := range groups {
		out[i] = pageBuild{
			level:   0,
			isLeaf:  true,
			records: g,
			minKey:  RecordKey(g[0]),
		}
	}
	return out
}

// packLeaves greedy-packs sorted records into pages that each
// fit in one piece. Returns the groups in the same record order.
func packLeaves(records []Record, pieceSize int) ([][]Record, error) {
	var groups [][]Record
	cur := make([]Record, 0, 64)
	for _, r := range records {
		// Validate at pack time — keyword length & record size.
		if len(r.Kw) == 0 {
			return nil, errors.New("companion: empty keyword in records")
		}
		if len(r.Kw) > MaxKeywordBytes {
			return nil, fmt.Errorf("companion: keyword %q exceeds cap %d",
				r.Kw, MaxKeywordBytes)
		}

		trial := append(cur, r) //nolint:gocritic
		if _, err := EncodeLeaf(0, trial, pieceSize); err != nil {
			if errors.Is(err, ErrPageOverflow) {
				if len(cur) == 0 {
					// Single record doesn't fit — oversized.
					enc, _ := EncodeRecord(r)
					return nil, fmt.Errorf("companion: record of %d bytes too large for page %d",
						len(enc), pieceSize)
				}
				groups = append(groups, cur)
				cur = []Record{r}
				continue
			}
			return nil, err
		}
		cur = trial
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}
	return groups, nil
}

// packInteriorLevel builds the layer of interior pages that
// indexes the given layer of child pages. Children are
// greedy-packed into interior pages by byte budget.
//
// The first child of each interior page gets an empty separator
// (records < second child's separator land here); subsequent
// children carry their minKey.
func packInteriorLevel(children []pageBuild, pieceSize int) ([]pageBuild, error) {
	if len(children) == 0 {
		return nil, errors.New("companion: packInteriorLevel empty children")
	}
	var pages []pageBuild
	cur := make([]InteriorChild, 0, 64)
	curMin := []byte(nil)
	for i, ch := range children {
		sep := ch.minKey
		if len(cur) == 0 {
			// First child of a fresh page has empty separator.
			sep = nil
			curMin = ch.minKey
		}
		trial := append(cur, InteriorChild{Separator: sep, ChildIndex: 0}) //nolint:gocritic
		if _, err := EncodeInterior(PageKindInterior, 0, trial, pieceSize); err != nil {
			if errors.Is(err, ErrPageOverflow) {
				if len(cur) == 0 {
					// Even a single child's separator doesn't fit —
					// suggests keyword is extreme or pieceSize is far
					// too small. packLeaves should have rejected that
					// first, but defend anyway.
					return nil, fmt.Errorf("companion: child separator too large for interior page %d", pieceSize)
				}
				pages = append(pages, pageBuild{
					level:    children[0].level + 1,
					children: cur,
					minKey:   curMin,
				})
				// Restart with this child as the page's first.
				cur = []InteriorChild{{Separator: nil, ChildIndex: 0}}
				curMin = ch.minKey
				continue
			}
			return nil, err
		}
		cur = trial
		_ = i
	}
	if len(cur) > 0 {
		pages = append(pages, pageBuild{
			level:    children[0].level + 1,
			children: cur,
			minKey:   curMin,
		})
	}
	// If this level is a single page, it's the root.
	if len(pages) == 1 {
		pages[0].isRoot = true
	}
	return pages, nil
}

// compareRecords returns -1/0/+1 by RecordKey byte order.
func compareRecords(a, b Record) int {
	ka := RecordKey(a)
	kb := RecordKey(b)
	for i := 0; i < len(ka) && i < len(kb); i++ {
		if ka[i] < kb[i] {
			return -1
		}
		if ka[i] > kb[i] {
			return 1
		}
	}
	if len(ka) < len(kb) {
		return -1
	}
	if len(ka) > len(kb) {
		return 1
	}
	return 0
}
