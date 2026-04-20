package extractors

import (
	"encoding/binary"
	"testing"
)

func TestU16LEDecodes(t *testing.T) {
	t.Parallel()
	got := u16LE([]byte{0x01, 0x00, 0xff, 0xff, 0x34, 0x12})
	want := []uint16{1, 0xffff, 0x1234}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = 0x%04x, want 0x%04x", i, got[i], want[i])
		}
	}
}

func TestU16LEDropsTrailingByte(t *testing.T) {
	t.Parallel()
	// Odd-length input drops the dangling byte rather than panicking.
	got := u16LE([]byte{0x01, 0x00, 0x99})
	if len(got) != 1 || got[0] != 1 {
		t.Errorf("got = %v, want [1]", got)
	}
}

func TestU16BEDecodes(t *testing.T) {
	t.Parallel()
	got := u16BE([]byte{0x00, 0x01, 0xff, 0xff, 0x12, 0x34})
	want := []uint16{1, 0xffff, 0x1234}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d] = 0x%04x, want 0x%04x", i, got[i], want[i])
		}
	}
}

func TestFindDoubleNulFinds(t *testing.T) {
	t.Parallel()
	// UTF-16 codepoints: 'a' 'b' 0x0000 'c' — the first 00 00 pair
	// after a non-zero codepoint marks string end.
	in := []byte{'a', 0, 'b', 0, 0, 0, 'c', 0}
	if got := findDoubleNul(in); got != 4 {
		t.Errorf("findDoubleNul = %d, want 4", got)
	}
}

func TestFindDoubleNulOnlyMatchesAlignedPair(t *testing.T) {
	t.Parallel()
	// 0x00 at odd index does not start a UTF-16 NUL — the function
	// scans i += 2, so only even-aligned 0x00 0x00 pairs count.
	in := []byte{'a', 0x00, 0x00, 'b'}
	// Pair at positions (1,2) is misaligned; the loop checks (0,1)
	// then (2,3). Neither is {0,0}, so we get -1.
	if got := findDoubleNul(in); got != -1 {
		t.Errorf("findDoubleNul = %d, want -1 for misaligned pair", got)
	}
}

func TestFindDoubleNulNotFound(t *testing.T) {
	t.Parallel()
	if got := findDoubleNul([]byte{'a', 0, 'b', 0, 'c', 0}); got != -1 {
		t.Errorf("findDoubleNul = %d, want -1", got)
	}
}

func TestRationalBasic(t *testing.T) {
	t.Parallel()
	// 3/4 in big-endian.
	b := []byte{0, 0, 0, 3, 0, 0, 0, 4}
	if got := rational(b, binary.BigEndian); got != 0.75 {
		t.Errorf("rational = %v, want 0.75", got)
	}
}

func TestRationalDivByZero(t *testing.T) {
	t.Parallel()
	// Denominator zero is the documented degenerate case — must
	// return 0 instead of panicking.
	b := []byte{0, 0, 0, 5, 0, 0, 0, 0}
	if got := rational(b, binary.BigEndian); got != 0 {
		t.Errorf("rational = %v, want 0", got)
	}
}

func TestRationalLittleEndian(t *testing.T) {
	t.Parallel()
	// 10/2 = 5.0 in little-endian.
	b := []byte{10, 0, 0, 0, 2, 0, 0, 0}
	if got := rational(b, binary.LittleEndian); got != 5.0 {
		t.Errorf("rational = %v, want 5.0", got)
	}
}
