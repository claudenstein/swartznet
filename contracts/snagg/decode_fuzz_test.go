package snagg

import (
	"encoding/binary"
	"testing"
)

// TestDecodeInteriorRejectsOverflowSeparator is the regression for the sl+4
// varint overflow: a separator length near 2^64 must be rejected with an error,
// never allowed to reach p[:sl] (which panicked with a bounds-out-of-range on
// untrusted network data — a DoS in the aggregate/companion query path).
func TestDecodeInteriorRejectsOverflowSeparator(t *testing.T) {
	page := make([]byte, MinPieceSize)
	var buf []byte
	buf = binary.LittleEndian.AppendUint16(buf, 1) // child count = 1
	var v [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(v[:], ^uint64(0)) // separator length = 2^64-1
	buf = append(buf, v[:n]...)
	buf = append(buf, 1, 2, 3, 4) // >=4 trailing bytes so the wrapped check would be skipped
	encodeHeader(page, pageHeader{kind: PageKindRoot, level: 1, payload: uint16(len(buf))})
	copy(page[PageHeaderSize:], buf)

	if _, err := DecodeInterior(page); err == nil {
		t.Fatal("DecodeInterior accepted a 2^64-1 separator length (would panic on p[:sl])")
	}
}

// TestDecodeTrailerRejectsShortPage is the regression for the missing length
// guard: a page that declares trailer payload=162 but is shorter than 178 bytes
// must error, not panic on page[16:178].
func TestDecodeTrailerRejectsShortPage(t *testing.T) {
	page := make([]byte, 100) // < PageHeaderSize + TrailerPayloadSize (178)
	encodeHeader(page, pageHeader{kind: PageKindTrailer, level: 0, payload: TrailerPayloadSize})
	if _, err := DecodeTrailer(page); err == nil {
		t.Fatal("DecodeTrailer accepted a 100-byte page declaring a 162-byte payload (would panic)")
	}
}

// TestOpenBTreeRejectsZeroPieceSize is the regression for the integer
// divide-by-zero: a BytesPageSource with PieceSize 0 (or negative) must error,
// not panic in NumPieces (reachable via `aggregate inspect --piece-size 0`).
func TestOpenBTreeRejectsZeroPieceSize(t *testing.T) {
	data := make([]byte, 100000)
	for _, ps := range []int{0, -1} {
		if _, err := OpenBTree(BytesPageSource{Data: data, PieceSize: ps}); err == nil {
			t.Errorf("OpenBTree accepted PieceSize %d (would divide by zero)", ps)
		}
	}
}

// TestDecodeInteriorRejectsSeparatorPastEnd covers the ordinary (non-overflow)
// short-buffer case too, so the overflow-safe rewrite didn't regress it.
func TestDecodeInteriorRejectsSeparatorPastEnd(t *testing.T) {
	page := make([]byte, MinPieceSize)
	var buf []byte
	buf = binary.LittleEndian.AppendUint16(buf, 1)
	var v [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(v[:], 9999) // separator longer than the remaining payload
	buf = append(buf, v[:n]...)
	buf = append(buf, 1, 2, 3, 4)
	encodeHeader(page, pageHeader{kind: PageKindRoot, level: 1, payload: uint16(len(buf))})
	copy(page[PageHeaderSize:], buf)
	if _, err := DecodeInterior(page); err == nil {
		t.Fatal("DecodeInterior accepted a separator longer than its payload")
	}
}
