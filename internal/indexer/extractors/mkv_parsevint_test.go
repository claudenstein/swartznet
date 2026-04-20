package extractors

import "testing"

// TestParseVINTGuards covers the two early-return branches of
// parseVINT: empty input and invalid/truncated VINT prefix.
func TestParseVINTGuards(t *testing.T) {
	t.Parallel()

	// Empty input: n/0 length → (0, 0).
	if v, n := parseVINT(nil, false); v != 0 || n != 0 {
		t.Errorf("parseVINT(nil) = (%d, %d), want (0, 0)", v, n)
	}
	if v, n := parseVINT([]byte{}, true); v != 0 || n != 0 {
		t.Errorf("parseVINT(empty) = (%d, %d), want (0, 0)", v, n)
	}

	// First byte all zero → vintLen returns 0 → invalid VINT.
	if v, n := parseVINT([]byte{0x00, 0x01, 0x02}, false); v != 0 || n != 0 {
		t.Errorf("parseVINT(zero first byte) = (%d, %d), want (0, 0)", v, n)
	}

	// First byte indicates length 4 but only 2 bytes available → n > len(b).
	if v, n := parseVINT([]byte{0x10, 0xAA}, false); v != 0 || n != 0 {
		t.Errorf("parseVINT(truncated) = (%d, %d), want (0, 0)", v, n)
	}

	// Sanity: a valid 1-byte VINT round-trips.
	if v, n := parseVINT([]byte{0x82}, false); v != 0x02 || n != 1 {
		t.Errorf("parseVINT(0x82) = (%d, %d), want (2, 1)", v, n)
	}
}
