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

// TestDecodeRecordBoundsAlloc pins the round-6 fix: a tiny hostile record blob
// declaring a huge inner string (kw length near the ~128 MiB bencode default)
// must not drive that allocation before the field-width checks run. The decode
// is bounded to len(b).
func TestDecodeRecordBoundsAlloc(t *testing.T) {
	// d{kw: <bencode string declaring 134217727 bytes, no body>}
	blob := []byte("d2:kw134217727:e")

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	_, err := DecodeRecord(blob)
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("hostile record (kw declares ~128 MiB, no body) decoded without error")
	}
	const cap = 8 << 20 // 8 MiB — far above a bounded decode, far below ~128 MiB
	if grew := after.TotalAlloc - before.TotalAlloc; grew > cap {
		t.Fatalf("DecodeRecord allocated %d bytes for a %d-byte blob — not bounded", grew, len(blob))
	}
}

// TestDecodeRecordStillRoundTrips guards against over-tightening: a normal
// encoded record still decodes.
func TestDecodeRecordStillRoundTrips(t *testing.T) {
	r := Record{Kw: "ubuntu", Pow: 7, T: 12345}
	for i := range r.Ih {
		r.Ih[i] = byte(i)
	}
	for i := range r.Pk {
		r.Pk[i] = byte(i + 1)
	}
	for i := range r.Sig {
		r.Sig[i] = byte(i + 2)
	}
	enc, err := EncodeRecord(r)
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeRecord(enc)
	if err != nil {
		t.Fatalf("bounded decode rejected a valid record: %v", err)
	}
	if back.Kw != r.Kw || back.Pow != r.Pow || back.T != r.T || back.Ih != r.Ih {
		t.Errorf("round-trip mismatch: %+v vs %+v", back, r)
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
