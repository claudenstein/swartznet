package companion

import (
	"encoding/binary"
	"testing"
)

// makeInteriorPage builds a synthetic page with a fully formed
// header (kind / payload-length) and the supplied payload bytes
// copied in after the header. Pads to totalSize.
func makeInteriorPage(t *testing.T, kind PageKind, payload []byte, totalSize int) []byte {
	t.Helper()
	hdr := encodeHeader(PageHeader{Kind: kind, PayloadLength: uint16(len(payload))})
	page := make([]byte, totalSize)
	copy(page, hdr)
	copy(page[PageHeaderSize:], payload)
	return page
}

// TestDecodeInteriorBadKind — only PageKindInterior /
// PageKindRoot are accepted; a leaf page or trailer page
// must be rejected by DecodeInterior.
func TestDecodeInteriorBadKind(t *testing.T) {
	t.Parallel()
	body := make([]byte, 2)
	binary.LittleEndian.PutUint16(body, 0) // 0 children
	page := makeInteriorPage(t, PageKindLeaf, body, MinPieceSize)
	if _, _, err := DecodeInterior(page); err == nil {
		t.Error("DecodeInterior should reject leaf-kind page")
	}
}

// TestDecodeInteriorPayloadTooLong — claim a payload length
// longer than the page itself; DecodeInterior must reject.
// Build the header by hand (encodeHeader clamps to uint16 so
// we can claim 65535 even though the page is 24 bytes).
func TestDecodeInteriorPayloadTooLong(t *testing.T) {
	t.Parallel()
	page := make([]byte, PageHeaderSize+8)
	hdr := encodeHeader(PageHeader{Kind: PageKindInterior, PayloadLength: 60000})
	copy(page, hdr)
	if _, _, err := DecodeInterior(page); err == nil {
		t.Error("DecodeInterior should reject impossible payload length")
	}
}

// TestDecodeInteriorPayloadTooShort — payload of 1 byte (less
// than the 2-byte child-count header) must error.
func TestDecodeInteriorPayloadTooShort(t *testing.T) {
	t.Parallel()
	page := makeInteriorPage(t, PageKindInterior, []byte{0xAA}, MinPieceSize)
	if _, _, err := DecodeInterior(page); err == nil {
		t.Error("DecodeInterior should reject 1-byte payload")
	}
}

// TestDecodeInteriorShortSeparator — claim 1 child with a
// huge sep length, but provide too few body bytes. The
// "short separator or child index" guard must fire.
func TestDecodeInteriorShortSeparator(t *testing.T) {
	t.Parallel()
	body := make([]byte, 0, 16)
	body = binary.LittleEndian.AppendUint16(body, 1) // 1 child
	varint := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(varint, 1000) // huge separator length
	body = append(body, varint[:n]...)
	// no separator bytes follow → short
	page := makeInteriorPage(t, PageKindInterior, body, MinPieceSize)
	if _, _, err := DecodeInterior(page); err == nil {
		t.Error("DecodeInterior should reject short separator body")
	}
}
