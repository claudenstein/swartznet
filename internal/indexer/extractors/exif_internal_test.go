package extractors

import (
	"encoding/binary"
	"testing"
)

func TestParseTIFFTooShort(t *testing.T) {
	t.Parallel()
	if _, err := parseTIFF([]byte{0x49, 0x49, 0x2a, 0x00}); err == nil {
		t.Error("parseTIFF should error on input shorter than 8 bytes")
	}
}

func TestParseTIFFBadByteOrderMarker(t *testing.T) {
	t.Parallel()
	// Valid 8-byte length but neither "II" nor "MM" prefix.
	tiff := []byte{0x58, 0x58, 0x2a, 0x00, 0x08, 0x00, 0x00, 0x00}
	if _, err := parseTIFF(tiff); err == nil {
		t.Error("parseTIFF should reject unknown byte-order marker")
	}
}

// TestFindExifTIFFFallthroughNoExif covers findExifTIFF's
// `return nil, false` arm at line 132 when the loop completes
// without finding the APP1 EXIF segment. We hand it a JPEG-like
// buffer with a non-EXIF APP segment that consumes the entire
// payload.
func TestFindExifTIFFFallthroughNoExif(t *testing.T) {
	t.Parallel()
	// 0xFF 0xE0 (APP0, JFIF marker) | seg-len 4 | one byte of payload
	// Then 0xFF 0xFF (fill byte) | end of buffer.
	jpeg := []byte{
		0xFF, 0xE0, 0x00, 0x04, 0x00, 0x00, // APP0 (not EXIF), len=4
		0xFF, 0xFF, // fill bytes — loop continues but i goes past buffer
	}
	got, ok := findExifTIFF(jpeg)
	if ok || got != nil {
		t.Errorf("findExifTIFF on non-EXIF JPEG = (%v, %v), want (nil, false)", got, ok)
	}
}

// TestParseTIFFBigEndianHeader covers the `case "MM": bo = binary.BigEndian`
// arm. A minimal TIFF header with the "MM" byte-order marker and
// a 4-byte IFD offset of 8 (just past the header) followed by an
// IFD with zero entries — parseTIFF should accept it and return
// no tags rather than error.
func TestParseTIFFBigEndianHeader(t *testing.T) {
	t.Parallel()
	tiff := []byte{
		'M', 'M', 0x00, 0x2a, // big-endian magic 0x002a
		0x00, 0x00, 0x00, 0x08, // IFD offset = 8 (BE)
		0x00, 0x00, // IFD entry count = 0 (BE)
	}
	tags, err := parseTIFF(tiff)
	if err != nil {
		t.Fatalf("parseTIFF MM: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected 0 tags from empty IFD, got %d", len(tags))
	}
}

func TestParseTIFFIFDOffsetOutOfRange(t *testing.T) {
	t.Parallel()
	// "II" header + a 4-byte IFD offset past end-of-buffer.
	tiff := []byte{
		'I', 'I', 0x2a, 0x00,
		0xff, 0xff, 0xff, 0xff, // IFD offset = 0xFFFFFFFF (way past end)
	}
	if _, err := parseTIFF(tiff); err == nil {
		t.Error("parseTIFF should reject out-of-range IFD offset")
	}
}

func TestReadValueBytesInlineFitsInSlot(t *testing.T) {
	t.Parallel()
	// size <= 4 → returned slice is the slot's first `size` bytes.
	slot := []byte{'A', 'B', 'C', 'D'}
	got := readValueBytes(nil, slot, 3, binary.LittleEndian)
	if string(got) != "ABC" {
		t.Errorf("readValueBytes inline = %q, want \"ABC\"", got)
	}
}

func TestReadValueBytesOutOfLineSuccess(t *testing.T) {
	t.Parallel()
	// size > 4 → slot encodes a uint32 offset into t. Build a t
	// whose bytes 16..23 are the payload, encode offset 16 in slot.
	t1 := make([]byte, 32)
	copy(t1[16:], []byte("HELLO_WORLD"))
	slot := make([]byte, 4)
	binary.LittleEndian.PutUint32(slot, 16)

	got := readValueBytes(t1, slot, 11, binary.LittleEndian)
	if string(got) != "HELLO_WORLD" {
		t.Errorf("readValueBytes out-of-line = %q, want \"HELLO_WORLD\"", got)
	}
}

// TestReadValueBytesOutOfLineOffsetTooLarge covers the "off+size >
// len(t)" guard — the function must return nil rather than slice
// past the end of the buffer.
func TestReadValueBytesOutOfLineOffsetTooLarge(t *testing.T) {
	t.Parallel()
	t1 := make([]byte, 16)
	slot := make([]byte, 4)
	binary.LittleEndian.PutUint32(slot, 12) // 12 + 8 = 20 > 16

	if got := readValueBytes(t1, slot, 8, binary.LittleEndian); got != nil {
		t.Errorf("readValueBytes with OOR offset = %v, want nil", got)
	}
}
