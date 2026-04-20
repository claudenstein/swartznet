package extractors

import "testing"

// TestFindExifTIFFStopsAtSOS pins the SOS-marker (0xDA) early-
// out: image data follows, no more APP segments to find.
func TestFindExifTIFFStopsAtSOS(t *testing.T) {
	t.Parallel()
	// FF D8 (SOI), then FF DA (SOS) immediately. No APP1 EXIF.
	b := []byte{0xFF, 0xD8, 0xFF, 0xDA, 0x00, 0x00}
	if _, ok := findExifTIFF(b[2:]); ok {
		t.Error("findExifTIFF should return false when SOS reached without APP1")
	}
}

// TestFindExifTIFFSkipsFFPadding covers the
// `marker == 0 || marker == 0xFF` continue branch — JPEG fill
// bytes between segments.
func TestFindExifTIFFSkipsFFPadding(t *testing.T) {
	t.Parallel()
	// Sequence after SOI: 0xFF 0xFF (skip), 0xFF 0xDA (SOS terminates).
	b := []byte{0xFF, 0xFF, 0xFF, 0xDA}
	if _, ok := findExifTIFF(b); ok {
		t.Error("findExifTIFF should not find EXIF in pure-padding payload")
	}
}

// TestFindExifTIFFTruncatedSegmentLength covers the
// `i+2 > len(b)` bounds check: the marker says a segment
// follows but there's no room to read the 2-byte length.
func TestFindExifTIFFTruncatedSegmentLength(t *testing.T) {
	t.Parallel()
	b := []byte{0xFF, 0xE1} // APP1 marker but no length bytes
	if _, ok := findExifTIFF(b); ok {
		t.Error("findExifTIFF on truncated segment-length should return false")
	}
}

// TestFindExifTIFFInvalidSegLength covers the `segLen < 2`
// guard. A segLen of 1 is illegal because it implies the length
// field itself doesn't fit; a 0 also fails.
func TestFindExifTIFFInvalidSegLength(t *testing.T) {
	t.Parallel()
	for _, segLen := range []byte{0x00, 0x01} {
		b := []byte{0xFF, 0xE1, 0x00, segLen}
		if _, ok := findExifTIFF(b); ok {
			t.Errorf("findExifTIFF with segLen=%d should return false", segLen)
		}
	}
}

// TestFindExifTIFFOversizedSegment covers `bodyEnd > len(b)`:
// the length field claims a longer body than the buffer holds.
func TestFindExifTIFFOversizedSegment(t *testing.T) {
	t.Parallel()
	// APP1 (0xE1) + segLen 0x00FF (255 bytes) but only 4 bytes
	// follow.
	b := []byte{0xFF, 0xE1, 0x00, 0xFF, 'a', 'b', 'c', 'd'}
	if _, ok := findExifTIFF(b); ok {
		t.Error("findExifTIFF on oversized segment should return false")
	}
}
