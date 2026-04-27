package companion

import (
	"bytes"
	"testing"
)

// TestEncodeInteriorPayloadExceedsUint16 covers EncodeInterior's
// `if payload.Len() > 65535 → err` arm. One child with a
// 65536-byte separator drives the encoded payload past the
// uint16 cap (separator + 3-byte uvarint length prefix + 4-byte
// child index ≈ 65543 bytes).
func TestEncodeInteriorPayloadExceedsUint16(t *testing.T) {
	t.Parallel()
	children := []InteriorChild{{
		Separator:  bytes.Repeat([]byte{'x'}, 65536),
		ChildIndex: 0,
	}}
	if _, err := EncodeInterior(PageKindInterior, 0, children, 1<<20); err == nil {
		t.Error("EncodeInterior should reject payload > uint16")
	}
}

// TestPackInteriorLevelPropagatesNonOverflowErr covers
// packInteriorLevel's `return nil, err` arm at line 415 — the
// fallthrough for EncodeInterior errors that aren't
// ErrPageOverflow. A child with a 65536-byte minKey makes the
// trial payload exceed uint16 inside EncodeInterior; the
// 1 MiB pieceSize keeps the overflow guard from firing first.
func TestPackInteriorLevelPropagatesNonOverflowErr(t *testing.T) {
	t.Parallel()
	children := []pageBuild{
		{minKey: nil}, // 1st: empty separator
		{minKey: bytes.Repeat([]byte{'x'}, 65536)}, // 2nd: huge separator
	}
	if _, err := packInteriorLevel(children, 1<<20); err == nil {
		t.Error("packInteriorLevel should propagate EncodeInterior payload-overflow err")
	}
}

// TestPackLeavesPropagatesNonOverflowErr covers packLeaves'
// `return nil, err` arm at line 364 — EncodeLeaf can return
// errors other than ErrPageOverflow (e.g. payload>uint16),
// which packLeaves must surface directly without trying to
// flush-and-restart.
//
// Strategy: large pieceSize (1 MiB) so ErrPageOverflow won't
// fire, plus 600 records → payload > 65535 → EncodeLeaf returns
// "leaf payload exceeds uint16" err, packLeaves propagates it.
func TestPackLeavesPropagatesNonOverflowErr(t *testing.T) {
	t.Parallel()
	records := make([]Record, 600)
	for i := range records {
		records[i].Kw = "k"
	}
	if _, err := packLeaves(records, 1<<20); err == nil {
		t.Error("packLeaves should propagate EncodeLeaf payload-overflow err")
	}
}

// TestEncodeLeafPayloadExceedsUint16 covers EncodeLeaf's
// `if payload.Len() > 65535 → err` arm. 600 trivial records
// encode to ~120 bytes each, easily clearing the uint16 cap.
func TestEncodeLeafPayloadExceedsUint16(t *testing.T) {
	t.Parallel()
	records := make([]Record, 600)
	for i := range records {
		records[i].Kw = "k"
	}
	if _, err := EncodeLeaf(0, records, 1<<20); err == nil {
		t.Error("EncodeLeaf should reject payload > uint16")
	}
}

// TestDecodeLeafPayloadOverflow covers DecodeLeaf's
// `if hdr.PayloadLength + PageHeaderSize > len(page)` arm at
// lines 400-402. Build a small page whose header claims a
// payload that overflows the page bytes.
func TestDecodeLeafPayloadOverflow(t *testing.T) {
	t.Parallel()
	hdr := PageHeader{
		Version:       BTreeVersion,
		Kind:          PageKindLeaf,
		PayloadLength: 1024, // way more than the 16-byte page below
	}
	page := encodeHeader(hdr) // exactly PageHeaderSize bytes; no payload written
	if _, _, err := DecodeLeaf(page); err == nil {
		t.Error("DecodeLeaf should reject a header claiming payload beyond the page")
	}
}
