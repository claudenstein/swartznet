package extractors

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestFLACExtractorEmptyComments covers the `text == "" → nil,
// nil` branch: a valid VORBIS_COMMENT block with no user fields.
func TestFLACExtractorEmptyComments(t *testing.T) {
	t.Parallel()
	// Vorbis body with vendor + zero comments.
	var body bytes.Buffer
	vendor := "swartznet"
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(vendor)))
	body.WriteString(vendor)
	_ = binary.Write(&body, binary.LittleEndian, uint32(0))

	var out bytes.Buffer
	out.WriteString("fLaC")
	ln := body.Len()
	// isLast=1, type=4 → 0x84.
	out.Write([]byte{0x84, byte(ln >> 16), byte(ln >> 8), byte(ln)})
	out.Write(body.Bytes())

	chunks, err := NewFLACExtractor().Extract(bytes.NewReader(out.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestFLACExtractorNoVorbisBlock covers the main-loop `isLast →
// break` exit path with no VORBIS_COMMENT ever seen.
func TestFLACExtractorNoVorbisBlock(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	out.WriteString("fLaC")
	// One STREAMINFO block (type=0), isLast=1 → 0x80. Body 34 bytes.
	out.Write([]byte{0x80, 0x00, 0x00, 0x22})
	out.Write(make([]byte, 34))

	chunks, err := NewFLACExtractor().Extract(bytes.NewReader(out.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

// TestFLACExtractorBadSignatureTruncated covers the sig-read error
// branch — input shorter than the 4-byte magic.
func TestFLACExtractorBadSignatureTruncated(t *testing.T) {
	t.Parallel()
	_, err := NewFLACExtractor().Extract(bytes.NewReader([]byte("fL")), 0)
	if err == nil {
		t.Error("expected error on short signature")
	}
}

// TestFLACExtractorBlockHeaderEOF covers the per-iteration header
// read error branch — valid magic but no block header follows.
func TestFLACExtractorBlockHeaderEOF(t *testing.T) {
	t.Parallel()
	_, err := NewFLACExtractor().Extract(bytes.NewReader([]byte("fLaC")), 0)
	if err == nil {
		t.Error("expected error on missing block header")
	}
}
