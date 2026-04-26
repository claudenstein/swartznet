package companion

import (
	"strings"
	"testing"
)

// TestPackLeavesRejectsEmptyKeyword — packLeaves validates at
// pack time so a downstream EncodeRecord doesn't have to fail
// with the same error per-page.
func TestPackLeavesRejectsEmptyKeyword(t *testing.T) {
	t.Parallel()
	r := Record{Kw: ""} // empty keyword
	if _, err := packLeaves([]Record{r}, MinPieceSize); err == nil {
		t.Error("packLeaves should reject empty keyword")
	}
}

// TestPackLeavesRejectsOversizeKeyword — same point, larger
// length validation.
func TestPackLeavesRejectsOversizeKeyword(t *testing.T) {
	t.Parallel()
	r := Record{Kw: strings.Repeat("x", MaxKeywordBytes+1)}
	if _, err := packLeaves([]Record{r}, MinPieceSize); err == nil {
		t.Error("packLeaves should reject oversize keyword")
	}
}

// TestPackLeavesSingleRecordTooLargeForPage covers the
// `if len(cur) == 0 { return error }` arm of packLeaves.
// Pass a tiny pieceSize so even a single normally-sized
// record can't fit; packLeaves must surface the
// "record too large" error rather than silently truncating.
func TestPackLeavesSingleRecordTooLargeForPage(t *testing.T) {
	t.Parallel()
	r := Record{Kw: "ubuntu"}
	// 100 bytes is well below the ~270 bytes a record encodes
	// to, plus the 16-byte page header.
	if _, err := packLeaves([]Record{r}, 100); err == nil {
		t.Error("packLeaves should reject single oversized record")
	}
}
