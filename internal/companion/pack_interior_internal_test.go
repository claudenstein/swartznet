package companion

import (
	"strings"
	"testing"
)

// TestPackInteriorLevelEmptyChildrenRejected — the function is
// only reachable from BuildBTree, which feeds it the leaves
// slice. An empty slice means a logic bug upstream; the guard
// surfaces it as an error rather than producing an empty
// interior level. Direct test fills a small but real coverage
// gap (BuildBTree never reaches this branch on the happy path).
func TestPackInteriorLevelEmptyChildrenRejected(t *testing.T) {
	t.Parallel()
	if _, err := packInteriorLevel(nil, MinPieceSize); err == nil {
		t.Error("packInteriorLevel(nil, _) should error")
	}
	if _, err := packInteriorLevel([]pageBuild{}, MinPieceSize); err == nil {
		t.Error("packInteriorLevel([], _) should error")
	}
}

// TestPackInteriorLevelOverflowSplits — when adding the next
// child would push the trial-encoded interior page past the
// piece-size cap, packInteriorLevel must flush the current
// page and start a fresh one. Reaches the "len(cur) > 0 +
// ErrPageOverflow" branch.
//
// Synthesise enough children with non-empty separators so the
// trial encoding eventually overflows. Each child contributes
// a 1 KiB separator; with MinPieceSize=16 KiB and a small page
// header, ~14 children should fit before split.
func TestPackInteriorLevelOverflowSplits(t *testing.T) {
	t.Parallel()
	var cs []pageBuild
	for i := 0; i < 64; i++ {
		// Distinct 1 KiB minKeys ensure separators don't fold.
		key := []byte(strings.Repeat(string(rune('a'+i%26)), 1024))
		cs = append(cs, pageBuild{
			level:  0,
			minKey: key,
		})
	}
	pages, err := packInteriorLevel(cs, MinPieceSize)
	if err != nil {
		t.Fatalf("packInteriorLevel: %v", err)
	}
	if len(pages) < 2 {
		t.Errorf("expected ≥2 interior pages from 64 × 1 KiB-separator children, got %d", len(pages))
	}
}
