package extractors

import "testing"

// TestDecodeID3BytesUTF16ShortAfterBOM covers the
// `if len(b) < 2 { return "" }` arm in case 1 (UTF-16) — a
// 1-byte body can't carry a BOM, so decode returns empty.
func TestDecodeID3BytesUTF16ShortAfterBOM(t *testing.T) {
	t.Parallel()
	if got := decodeID3Bytes(1, []byte{0x41}); got != "" {
		t.Errorf("decodeID3Bytes(utf-16, short) = %q, want empty", got)
	}
}

// TestDecodeID3BytesUTF16NoBOMDefaultsToLE covers the
// `default: src = u16LE(b)` arm when no BOM is present at the
// start of the body.
func TestDecodeID3BytesUTF16NoBOMDefaultsToLE(t *testing.T) {
	t.Parallel()
	// 'A' + 'X' in UTF-16 LE with non-NUL high bytes so TrimRight
	// doesn't strip the second char's low byte: 0x41 0x01 (= U+0141)
	// then 0x42 0x01 (= U+0142). Both characters are non-NUL.
	got := decodeID3Bytes(1, []byte{0x41, 0x01, 0x42, 0x01})
	if got == "" {
		t.Errorf("decodeID3Bytes(utf-16, no BOM) returned empty; expected 2 chars")
	}
}

// TestDecodeID3BytesUnknownEncodingDefaults covers the trailing
// `return strings.TrimSpace(string(b))` arm — encoding values
// outside 0-3 fall through to the raw-bytes default.
func TestDecodeID3BytesUnknownEncodingDefaults(t *testing.T) {
	t.Parallel()
	got := decodeID3Bytes(99, []byte("plain"))
	if got != "plain" {
		t.Errorf("decodeID3Bytes(unknown enc) = %q, want plain", got)
	}
}
