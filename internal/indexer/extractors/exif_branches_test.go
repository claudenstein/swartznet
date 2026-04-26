package extractors

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestEXIFExtractorJPEGWithoutEXIFReturnsNil covers the
// `tiff, found := findExifTIFF(...); if !found { return nil, nil }`
// arm in EXIFExtractor.Extract. A JPEG with SOI + EOI and no APP1
// segment must surface as (nil, nil), not as an error.
func TestEXIFExtractorJPEGWithoutEXIFReturnsNil(t *testing.T) {
	t.Parallel()
	// Bare-minimum JPEG: SOI followed immediately by EOI.
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xD9}
	chunks, err := NewEXIFExtractor().Extract(bytes.NewReader(jpeg), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for JPEG without EXIF segment", len(chunks))
	}
}

// TestEXIFExtractorBadTIFFByteOrderMarker covers the
// `parseTIFF err != nil → return nil, err` arm. The APP1 EXIF
// segment is well-formed but the TIFF block has neither II nor
// MM at the start, so parseTIFF fails on the byte-order check.
func TestEXIFExtractorBadTIFFByteOrderMarker(t *testing.T) {
	t.Parallel()
	// 8 bytes of garbage where the TIFF header should be.
	tiff := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}

	var seg bytes.Buffer
	seg.WriteString("Exif\x00\x00")
	seg.Write(tiff)

	var jpeg bytes.Buffer
	jpeg.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	_ = binary.Write(&jpeg, binary.BigEndian, uint16(2+seg.Len()))
	jpeg.Write(seg.Bytes())
	jpeg.Write([]byte{0xFF, 0xD9})

	if _, err := NewEXIFExtractor().Extract(bytes.NewReader(jpeg.Bytes()), 0); err == nil {
		t.Error("expected parseTIFF error for invalid byte-order marker")
	}
}

// TestEXIFExtractorTIFFWithNoKnownTagsYieldsNoChunks covers the
// `if text == "" { return nil, nil }` arm in Extract. A valid
// TIFF with zero entries (count=0) means no tags in
// exifTagOrder are populated, so the assembled text is empty.
func TestEXIFExtractorTIFFWithNoKnownTagsYieldsNoChunks(t *testing.T) {
	t.Parallel()
	// Build a minimal TIFF with count=0, no entries.
	var tiff bytes.Buffer
	tiff.WriteString("II")
	tiff.Write([]byte{0x2A, 0x00})                          // magic
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(8)) // IFD offset
	_ = binary.Write(&tiff, binary.LittleEndian, uint16(0)) // entry count = 0
	_ = binary.Write(&tiff, binary.LittleEndian, uint32(0)) // next IFD = 0

	var seg bytes.Buffer
	seg.WriteString("Exif\x00\x00")
	seg.Write(tiff.Bytes())

	var jpeg bytes.Buffer
	jpeg.Write([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	_ = binary.Write(&jpeg, binary.BigEndian, uint16(2+seg.Len()))
	jpeg.Write(seg.Bytes())
	jpeg.Write([]byte{0xFF, 0xD9})

	chunks, err := NewEXIFExtractor().Extract(bytes.NewReader(jpeg.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for empty TIFF", len(chunks))
	}
}
