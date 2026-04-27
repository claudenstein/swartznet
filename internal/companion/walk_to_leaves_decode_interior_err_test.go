package companion

import (
	"testing"
)

// TestWalkToLeavesDecodeInteriorError covers walkToLeaves's
// `_, children, err := DecodeInterior(page); if err != nil { return nil, err }`
// arm. Build a real tree, then on Find swap piece 0 (root) for
// a page whose header decodes as a valid root but whose payload
// is too short to contain an interior body. DecodeInterior
// rejects with the wrapped 'short' error.
func TestWalkToLeavesDecodeInteriorError(t *testing.T) {
	r, _, _, _ := buildTestTree(t, 30, []string{"alpha", "beta", "gamma"}, MinPieceSize)
	// Header-only payload claims kind=Root + PayloadLength=1 so
	// decodeHeader succeeds but DecodeInterior fails on the
	// undersized body.
	page := make([]byte, MinPieceSize)
	hdr := encodeHeader(PageHeader{
		Version:       BTreeVersion,
		Kind:          PageKindRoot,
		PayloadLength: 1,
	})
	copy(page, hdr)
	// Body is a single 0x00 byte — not a valid interior dump.
	r.src = &constPieceSource{inner: r.src, idx: 0, payload: page}

	if _, err := r.Find("alpha"); err == nil {
		t.Error("Find should fail when DecodeInterior on root rejects")
	}
}
