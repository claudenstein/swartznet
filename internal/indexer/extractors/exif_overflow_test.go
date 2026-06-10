package extractors

import (
	"encoding/binary"
	"testing"
)

// TestParseIFDHugeCountOverflowSafe is the regression test for the
// 32-bit integer-overflow finding: cnt is an attacker-controlled
// uint32, and the old `int(cnt)` / `cnt*8` math could go negative or
// wrap on 32-bit targets, slipping past the bounds checks and
// panicking on the slice. The arithmetic now runs in int64, so a
// huge count must simply produce no entry — on every GOARCH.
func TestParseIFDHugeCountOverflowSafe(t *testing.T) {
	t.Parallel()
	// The value slot points near the top of the uint32 range so that
	// wrapped 32-bit `off+size` math would pass the old check.
	var slot [4]byte
	binary.LittleEndian.PutUint32(slot[:], 0x7FFFFFFF)

	cases := []struct {
		name string
		typ  uint16
		cnt  uint32
	}{
		// type 2 (ASCII): int(0xFFFFFFFF) is -1 on 32-bit, and
		// valSlot[:-1] panics before any offset check runs.
		{"ascii cnt=0xFFFFFFFF", 2, 0xFFFFFFFF},
		// type 5 (RATIONAL): cnt*8 = 2³²+8 wraps a 32-bit int to 8,
		// passing the size check with a wild out-of-line offset.
		{"rational cnt*8 wraps", 5, 0x20000001},
		// type 1 (BYTE): negative-on-32-bit cnt must not slice the
		// inline slot.
		{"byte cnt=0xFFFFFFFF", 1, 0xFFFFFFFF},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tiff := buildIFD(0x010E, tc.typ, tc.cnt, slot, nil)
			out, err := parseTIFF(tiff)
			if err != nil {
				t.Fatal(err)
			}
			if v, ok := out[0x010E]; ok {
				t.Errorf("overflowing count produced entry %q, want none", v)
			}
		})
	}
}

// TestReadValueBytesOverflowSafe exercises readValueBytes directly
// with the size/offset combinations whose old int math wrapped on
// 32-bit targets.
func TestReadValueBytesOverflowSafe(t *testing.T) {
	t.Parallel()
	t1 := make([]byte, 64)
	var slot [4]byte
	binary.LittleEndian.PutUint32(slot[:], 0x7FFFFFFF)

	// off (0x7FFFFFFF) + size (8) wraps negative in 32-bit int math
	// and used to pass the `> len(t)` check.
	if got := readValueBytes(t1, slot[:], 8, binary.LittleEndian); got != nil {
		t.Errorf("near-MaxInt32 offset = %v, want nil", got)
	}
	// A size beyond the uint32 range can only appear via cnt*8; it
	// must be rejected, not truncated.
	if got := readValueBytes(t1, slot[:], 1<<32+8, binary.LittleEndian); got != nil {
		t.Errorf("size beyond uint32 range = %v, want nil", got)
	}
	// Defensive negative-size guard.
	if got := readValueBytes(t1, slot[:], -1, binary.LittleEndian); got != nil {
		t.Errorf("negative size = %v, want nil", got)
	}
}

// TestParseTIFFHugeIFDOffsetOverflowSafe covers the first-IFD
// offset: 0xFFFFFFFF is -1 as a 32-bit int, which used to dodge the
// `>= len(t)` rejection and index t[-1:] inside parseIFD.
func TestParseTIFFHugeIFDOffsetOverflowSafe(t *testing.T) {
	t.Parallel()
	tiff := []byte{
		'I', 'I', 0x2a, 0x00, // little-endian TIFF header
		0xFF, 0xFF, 0xFF, 0xFF, // IFD offset = 0xFFFFFFFF
	}
	if _, err := parseTIFF(tiff); err == nil {
		t.Error("parseTIFF should reject an out-of-range IFD offset")
	}
}
