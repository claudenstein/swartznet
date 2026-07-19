package snagg

import (
	"encoding/binary"
	"runtime"
	"testing"
)

// TestDecodeLeafBoundsPrealloc pins the round-5 LOW fix: a structurally-
// unauthenticated leaf page declaring a huge record count (uint16 max) with NO
// record bytes must not pre-allocate ~10 MB before the decode loop errors. The
// count is capped against the remaining payload length.
func TestDecodeLeafBoundsPrealloc(t *testing.T) {
	page := make([]byte, PageHeaderSize+2)
	encodeHeader(page, pageHeader{kind: PageKindLeaf, level: 0, payload: 2})
	binary.LittleEndian.PutUint16(page[PageHeaderSize:], 0xFFFF) // count=65535, zero records

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := DecodeLeaf(page)
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("malicious leaf (count=65535, no records) decoded without error")
	}
	const cap = 2 << 20 // 2 MiB — far above a bounded decode, far below ~10 MiB
	if grew := after.TotalAlloc - before.TotalAlloc; grew > cap {
		t.Fatalf("DecodeLeaf pre-allocated %d bytes for a 2-byte payload — count not bounded", grew)
	}
}

// TestDecodeInteriorBoundsPrealloc covers the interior/root decoder the same way.
func TestDecodeInteriorBoundsPrealloc(t *testing.T) {
	page := make([]byte, PageHeaderSize+2)
	encodeHeader(page, pageHeader{kind: PageKindRoot, level: 1, payload: 2})
	binary.LittleEndian.PutUint16(page[PageHeaderSize:], 0xFFFF)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := DecodeInterior(page)
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("malicious interior (count=65535, no children) decoded without error")
	}
	const cap = 2 << 20
	if grew := after.TotalAlloc - before.TotalAlloc; grew > cap {
		t.Fatalf("DecodeInterior pre-allocated %d bytes for a 2-byte payload — count not bounded", grew)
	}
}

// TestCapHint is a small unit check on the bound helper.
func TestCapHint(t *testing.T) {
	for _, tc := range []struct{ n, rem, want int }{
		{0, 100, 0}, {5, 100, 5}, {65535, 0, 0}, {65535, 10, 10}, {10, 10, 10},
	} {
		if got := capHint(tc.n, tc.rem); got != tc.want {
			t.Errorf("capHint(%d,%d)=%d, want %d", tc.n, tc.rem, got, tc.want)
		}
	}
}
