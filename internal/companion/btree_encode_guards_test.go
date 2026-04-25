package companion

import (
	"strings"
	"testing"
)

// TestEncodeInteriorWrongKind — only PageKindInterior /
// PageKindRoot are accepted by EncodeInterior. Leaf or trailer
// kinds must be rejected before any payload is built.
func TestEncodeInteriorWrongKind(t *testing.T) {
	t.Parallel()
	_, err := EncodeInterior(PageKindLeaf, 0,
		[]InteriorChild{{ChildIndex: 1}}, MinPieceSize)
	if err == nil {
		t.Error("EncodeInterior should reject leaf-kind input")
	}
}

// TestEncodeInteriorEmptyChildren — every interior page must
// have at least one child; the zero-children guard fires before
// any byte is written.
func TestEncodeInteriorEmptyChildren(t *testing.T) {
	t.Parallel()
	_, err := EncodeInterior(PageKindInterior, 0, nil, MinPieceSize)
	if err == nil {
		t.Error("EncodeInterior should reject empty children slice")
	}
}

// TestDecodeHeaderShort — page below PageHeaderSize bytes must
// surface a clear "need N bytes for header" error rather than
// panicking on a bounds check.
func TestDecodeHeaderShort(t *testing.T) {
	t.Parallel()
	_, err := decodeHeader(make([]byte, PageHeaderSize-1))
	if err == nil {
		t.Error("decodeHeader should reject under-sized page")
	}
}

// TestEncodeLeafEmptyRecords — leaf pages need ≥1 record. The
// guard fires before any size calculation.
func TestEncodeLeafEmptyRecords(t *testing.T) {
	t.Parallel()
	_, err := EncodeLeaf(0, nil, MinPieceSize)
	if err == nil {
		t.Error("EncodeLeaf should reject empty record slice")
	}
}

// TestEncodeRecordOversizedKeyword — EncodeRecord rejects a
// keyword longer than MaxKeywordBytes before attempting to
// marshal. Same guard packLeaves uses, but exercised at the
// per-record level so future writers that bypass packLeaves
// can't slip an oversize keyword past EncodeRecord.
func TestEncodeRecordOversizedKeyword(t *testing.T) {
	t.Parallel()
	r := Record{Kw: strings.Repeat("k", MaxKeywordBytes+1)}
	if _, err := EncodeRecord(r); err == nil {
		t.Error("EncodeRecord should reject oversize keyword")
	}
}
