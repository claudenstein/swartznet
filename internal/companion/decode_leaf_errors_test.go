package companion

import (
	"encoding/binary"
	"testing"
)

// makeLeafPage builds a synthetic leaf page like makeInteriorPage
// but with kind = PageKindLeaf.
func makeLeafPage(t *testing.T, payload []byte, totalSize int) []byte {
	t.Helper()
	return makeInteriorPage(t, PageKindLeaf, payload, totalSize)
}

// TestDecodeLeafBadKind — root/interior pages must be rejected.
func TestDecodeLeafBadKind(t *testing.T) {
	t.Parallel()
	body := make([]byte, 2)
	page := makeInteriorPage(t, PageKindRoot, body, MinPieceSize)
	if _, _, err := DecodeLeaf(page); err == nil {
		t.Error("DecodeLeaf should reject root-kind page")
	}
}

// TestDecodeLeafPayloadTooShort — leaf payload of 1 byte (less
// than the 2-byte record-count header) must error.
func TestDecodeLeafPayloadTooShort(t *testing.T) {
	t.Parallel()
	page := makeLeafPage(t, []byte{0xAA}, MinPieceSize)
	if _, _, err := DecodeLeaf(page); err == nil {
		t.Error("DecodeLeaf should reject 1-byte payload")
	}
}

// TestDecodeLeafBadRecordVarint — claim 1 record but supply
// only the leading bit of the varint (continuation pattern with
// no terminator). binary.Uvarint returns n<=0.
func TestDecodeLeafBadRecordVarint(t *testing.T) {
	t.Parallel()
	body := make([]byte, 0, 16)
	body = binary.LittleEndian.AppendUint16(body, 1) // 1 record
	// 4 continuation bytes with no terminator: bad varint.
	body = append(body, 0x80, 0x80, 0x80, 0x80)
	page := makeLeafPage(t, body, MinPieceSize)
	if _, _, err := DecodeLeaf(page); err == nil {
		t.Error("DecodeLeaf should reject malformed record varint")
	}
}

// TestDecodeLeafShortRecord — varint claims a 1000-byte record
// but the body has fewer bytes. "short record bytes" guard.
func TestDecodeLeafShortRecord(t *testing.T) {
	t.Parallel()
	body := make([]byte, 0, 16)
	body = binary.LittleEndian.AppendUint16(body, 1) // 1 record
	varint := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(varint, 1000)
	body = append(body, varint[:n]...)
	// no record bytes follow → short
	page := makeLeafPage(t, body, MinPieceSize)
	if _, _, err := DecodeLeaf(page); err == nil {
		t.Error("DecodeLeaf should reject short record body")
	}
}

// TestEncodeTrailerPageSizeTooSmall — pageSize must accommodate
// header + payload; below that, EncodeTrailer rejects.
func TestEncodeTrailerPageSizeTooSmall(t *testing.T) {
	t.Parallel()
	tr := Trailer{TrailerVersion: 0x01}
	if _, err := EncodeTrailer(tr, 32); err == nil {
		t.Error("EncodeTrailer should reject pageSize=32 (too small)")
	}
}

// TestEncodeTrailerBadVersion — only TrailerVersion=0x01 is
// accepted; anything else is a future-version write that this
// build doesn't understand.
func TestEncodeTrailerBadVersion(t *testing.T) {
	t.Parallel()
	tr := Trailer{TrailerVersion: 0x02}
	if _, err := EncodeTrailer(tr, MinPieceSize); err == nil {
		t.Error("EncodeTrailer should reject TrailerVersion=2")
	}
}
