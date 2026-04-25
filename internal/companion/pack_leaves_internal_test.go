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
