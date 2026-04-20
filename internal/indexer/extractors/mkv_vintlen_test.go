package extractors

import "testing"

// TestVintLenAllBranches drives every documented branch of
// vintLen, the EBML VINT length-prefix decoder. Previously only
// 40% of the switch was covered (lengths 1–2 from the canned MKV
// fixture). Pin all eight bit-positions plus the all-zero
// "invalid" fall-through.
func TestVintLenAllBranches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		first byte
		want  int
	}{
		// Top bit set → length 1.
		{0x80, 1},
		{0xFF, 1},
		// Bit 6 set, bit 7 clear → length 2.
		{0x40, 2},
		{0x7F, 2},
		// Bit 5 → length 3.
		{0x20, 3},
		// Bit 4 → length 4.
		{0x10, 4},
		// Bit 3 → length 5.
		{0x08, 5},
		// Bit 2 → length 6.
		{0x04, 6},
		// Bit 1 → length 7.
		{0x02, 7},
		// Bit 0 → length 8.
		{0x01, 8},
		// All zero → invalid (sentinel 0).
		{0x00, 0},
	}
	for _, c := range cases {
		if got := vintLen(c.first); got != c.want {
			t.Errorf("vintLen(0x%02x) = %d, want %d", c.first, got, c.want)
		}
	}
}
