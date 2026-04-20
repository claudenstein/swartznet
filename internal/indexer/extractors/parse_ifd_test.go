package extractors

import (
	"encoding/binary"
	"testing"
)

// buildIFD assembles a TIFF block with one IFD entry at the
// usual offset 8. Returns the byte slice the parser can consume.
//
// The IFD layout is:
//   - byte 0..3:  byte-order marker + magic ("II*\x00" little-endian)
//   - byte 4..7:  uint32 IFD offset (always 8)
//   - byte 8..9:  uint16 entry count
//   - byte 10..21: 12-byte entry (tag/type/count/valSlot)
//   - byte 22..25: uint32 next-IFD offset (zero terminates the list)
//   - byte 26+:   any out-of-line value bytes
func buildIFD(tag, typ uint16, cnt uint32, inlineValSlot [4]byte, outOfLine []byte) []byte {
	bo := binary.LittleEndian
	hdr := []byte{'I', 'I', 0x2a, 0x00}
	ifdOffset := make([]byte, 4)
	bo.PutUint32(ifdOffset, 8)

	entry := make([]byte, 12)
	bo.PutUint16(entry[0:2], tag)
	bo.PutUint16(entry[2:4], typ)
	bo.PutUint32(entry[4:8], cnt)
	copy(entry[8:12], inlineValSlot[:])

	count := make([]byte, 2)
	bo.PutUint16(count, 1) // one IFD entry

	nextIFD := []byte{0, 0, 0, 0}

	out := append([]byte{}, hdr...)
	out = append(out, ifdOffset...)
	out = append(out, count...)
	out = append(out, entry...)
	out = append(out, nextIFD...)
	if outOfLine != nil {
		out = append(out, outOfLine...)
	}
	return out
}

// TestParseIFDByteUndefinedInline covers the type-1/7 (BYTE /
// UNDEFINED) inline branch. cnt <= 4, so the value lives in
// the slot itself.
func TestParseIFDByteUndefinedInline(t *testing.T) {
	t.Parallel()
	// Tag 0x9000 (ExifVersion is type 7), cnt=4, slot = "0231"
	// (the typical ExifVersion).
	tiff := buildIFD(0x9000, 7, 4, [4]byte{'0', '2', '3', '1'}, nil)
	out, err := parseTIFF(tiff)
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0x9000]; got != "0231" {
		t.Errorf("tag 0x9000 = %q, want \"0231\"", got)
	}
}

// TestParseIFDRationalGPSCoordinate covers the type-5 RATIONAL
// branch with cnt==3 (the GPS lat/lon D/M/S triple). The slot
// holds an out-of-line offset to the 24 bytes of rational data.
func TestParseIFDRationalGPSCoordinate(t *testing.T) {
	t.Parallel()
	bo := binary.LittleEndian
	// Three rationals: 51/1, 30/1, 0/1 → 51 + 30/60 + 0 = 51.5
	rationals := make([]byte, 24)
	bo.PutUint32(rationals[0:4], 51)
	bo.PutUint32(rationals[4:8], 1)
	bo.PutUint32(rationals[8:12], 30)
	bo.PutUint32(rationals[12:16], 1)
	bo.PutUint32(rationals[16:20], 0)
	bo.PutUint32(rationals[20:24], 1)

	// The slot points at offset 26 (after the IFD).
	slot := [4]byte{}
	bo.PutUint32(slot[:], 26)

	tiff := buildIFD(0x0002, 5, 3, slot, rationals)
	out, err := parseTIFF(tiff)
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0x0002]; got != "51.5000" {
		t.Errorf("tag 0x0002 = %q, want \"51.5000\"", got)
	}
}

// TestParseIFDRationalSingleValue covers the cnt==1 RATIONAL
// branch (single rational, e.g. EXIF FocalLength).
func TestParseIFDRationalSingleValue(t *testing.T) {
	t.Parallel()
	bo := binary.LittleEndian
	// 5/2 = 2.5 (e.g. focal length)
	r := make([]byte, 8)
	bo.PutUint32(r[0:4], 5)
	bo.PutUint32(r[4:8], 2)

	slot := [4]byte{}
	bo.PutUint32(slot[:], 26)

	tiff := buildIFD(0x920A, 5, 1, slot, r)
	out, err := parseTIFF(tiff)
	if err != nil {
		t.Fatal(err)
	}
	if got := out[0x920A]; got != "2.5000" {
		t.Errorf("tag 0x920A = %q, want \"2.5000\"", got)
	}
}

// TestParseTIFFGPSIFDExpansion exercises the GPS IFD pointer
// expansion in parseTIFF: a tag 0x8825 in IFD0 points at a
// secondary IFD whose tags 0x0001..0x0004 hold lat/latRef/lon/
// lonRef. The combined "GPS" entry should appear in the result.
//
// We can't easily construct this with the single-entry buildIFD
// helper; this test pins the simpler "no GPS" case (0x8825
// absent, no expansion) and proves the GPS-expansion code path
// at least short-circuits cleanly. The full GPS expansion is
// indirectly exercised by TestEXIFExtractorReadsCameraMakeModel
// and the BuildJPEG fixture.
func TestParseTIFFGPSAbsent(t *testing.T) {
	t.Parallel()
	tiff := buildIFD(0x010F, 2, 4, [4]byte{'A', 'B', 'C', 0}, nil)
	out, err := parseTIFF(tiff)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out[0x8825]; ok {
		t.Error("0x8825 (GPS) should not appear in the result")
	}
	if got := out[0x010F]; got != "ABC" {
		t.Errorf("tag 0x010F = %q, want \"ABC\"", got)
	}
}
