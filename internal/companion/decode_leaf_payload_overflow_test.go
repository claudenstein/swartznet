package companion

import "testing"

// TestDecodeLeafPayloadOverflow covers DecodeLeaf's
// `if hdr.PayloadLength + PageHeaderSize > len(page)` arm at
// lines 400-402. Build a small page whose header claims a
// payload that overflows the page bytes.
func TestDecodeLeafPayloadOverflow(t *testing.T) {
	t.Parallel()
	hdr := PageHeader{
		Version:       BTreeVersion,
		Kind:          PageKindLeaf,
		PayloadLength: 1024, // way more than the 16-byte page below
	}
	page := encodeHeader(hdr) // exactly PageHeaderSize bytes; no payload written
	if _, _, err := DecodeLeaf(page); err == nil {
		t.Error("DecodeLeaf should reject a header claiming payload beyond the page")
	}
}
