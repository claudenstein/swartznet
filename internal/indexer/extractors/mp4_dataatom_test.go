package extractors

import (
	"encoding/binary"
	"testing"
)

// makeAtom builds an MP4-style atom: 4-byte BE size + 4-byte
// type + body.
func makeAtom(atomType string, body []byte) []byte {
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(body)))
	copy(out[4:8], atomType)
	copy(out[8:], body)
	return out
}

// TestExtractDataAtomFindsDataPayload covers the success path:
// `data` atom found inside the parent body, with the standard
// 8-byte data-header (version+flags+reserved) preceding the
// UTF-8 payload.
func TestExtractDataAtomFindsDataPayload(t *testing.T) {
	t.Parallel()
	// data atom body: 1-byte version + 3-byte flags (1 = UTF-8)
	// + 4-byte reserved + payload + trailing null bytes (which
	// TrimRight strips) and surrounding whitespace (TrimSpace strips).
	body := []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	body = append(body, []byte("  Hello World  \x00\x00")...)
	parent := makeAtom("data", body)

	if got := extractDataAtom(parent); got != "Hello World" {
		t.Errorf("extractDataAtom = %q, want \"Hello World\"", got)
	}
}

// TestExtractDataAtomSkipsNonDataAtom covers the
// "skip past atomType != 'data'" branch — a leading non-data
// child atom should be hopped over to find the real data atom.
func TestExtractDataAtomSkipsNonDataAtom(t *testing.T) {
	t.Parallel()
	// First child: free atom whose declared size must satisfy the
	// `size >= 16` guard, so its body needs at least 8 bytes.
	first := makeAtom("free", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07})
	// Second child: actual data atom.
	dataBody := []byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00}
	dataBody = append(dataBody, []byte("payload-text")...)
	second := makeAtom("data", dataBody)

	combined := append(first, second...)
	if got := extractDataAtom(combined); got != "payload-text" {
		t.Errorf("extractDataAtom = %q, want \"payload-text\"", got)
	}
}

// TestExtractDataAtomSizeTooSmall covers the `size < 16` guard:
// a child atom whose declared size is below the minimum
// (8-byte atom header + 8-byte data header) returns "".
func TestExtractDataAtomSizeTooSmall(t *testing.T) {
	t.Parallel()
	bad := make([]byte, 16)
	binary.BigEndian.PutUint32(bad[0:4], 8) // size=8 (less than 16)
	copy(bad[4:8], "data")

	if got := extractDataAtom(bad); got != "" {
		t.Errorf("extractDataAtom on size<16 = %q, want \"\"", got)
	}
}

// TestExtractDataAtomSizePastEnd covers the `p+size > len(b)`
// guard — declared size larger than buffer.
func TestExtractDataAtomSizePastEnd(t *testing.T) {
	t.Parallel()
	bad := make([]byte, 16)
	binary.BigEndian.PutUint32(bad[0:4], 999) // size way past buffer
	copy(bad[4:8], "data")

	if got := extractDataAtom(bad); got != "" {
		t.Errorf("extractDataAtom on size>len = %q, want \"\"", got)
	}
}

// TestExtractDataAtomNoDataAtom covers the loop-completes-without-finding
// path: no `data` child anywhere in the parent body.
func TestExtractDataAtomNoDataAtom(t *testing.T) {
	t.Parallel()
	first := makeAtom("free", []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08})
	if got := extractDataAtom(first); got != "" {
		t.Errorf("extractDataAtom with no data atom = %q, want \"\"", got)
	}
}

// TestExtractDataAtomShortBuffer covers the `p+16 <= len(b)`
// loop-condition false-on-entry case (buffer < 16 bytes).
func TestExtractDataAtomShortBuffer(t *testing.T) {
	t.Parallel()
	if got := extractDataAtom([]byte{0x00, 0x01, 0x02}); got != "" {
		t.Errorf("extractDataAtom on tiny buffer = %q, want \"\"", got)
	}
}
