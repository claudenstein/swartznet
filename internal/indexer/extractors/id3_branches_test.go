package extractors

import (
	"bytes"
	"testing"
)

// TestID3ExtractorTooShort covers the
// `header, err := br.Peek(10); if err != nil || len(header) < 10 { ... }`
// arm.
func TestID3ExtractorTooShort(t *testing.T) {
	t.Parallel()
	if _, err := NewID3Extractor().Extract(bytes.NewReader([]byte("ID")), 0); err == nil {
		t.Error("Extract should fail on input shorter than 10 bytes")
	}
}

// TestID3ExtractorMissingHeader covers the
// `if string(header[:3]) != "ID3" { ... }` arm.
func TestID3ExtractorMissingHeader(t *testing.T) {
	t.Parallel()
	bad := bytes.Repeat([]byte("X"), 50)
	if _, err := NewID3Extractor().Extract(bytes.NewReader(bad), 0); err == nil {
		t.Error("Extract should fail when header is not ID3")
	}
}

// TestID3ExtractorUnsupportedV22 covers the
// `if majorVer != 3 && majorVer != 4 { ... }` arm by setting
// majorVer = 2 (v2.2).
func TestID3ExtractorUnsupportedV22(t *testing.T) {
	t.Parallel()
	tag := []byte{
		'I', 'D', '3',
		2, // major version 2.2
		0, // revision
		0, // flags
		0, 0, 0, 0, // tag size (syncsafe, zero)
	}
	if _, err := NewID3Extractor().Extract(bytes.NewReader(tag), 0); err == nil {
		t.Error("Extract should fail for v2.2 tag")
	}
}

// TestID3ExtractorReadTagError covers the
// `if _, err := io.ReadFull(br, raw); err != nil { ... }` arm
// — the header advertises a 100-byte tag but only 10 bytes
// follow, so ReadFull returns ErrUnexpectedEOF.
func TestID3ExtractorReadTagError(t *testing.T) {
	t.Parallel()
	tag := []byte{
		'I', 'D', '3',
		4, 0, 0,
		0, 0, 0, 0x64, // syncsafe size = 100
	}
	if _, err := NewID3Extractor().Extract(bytes.NewReader(tag), 0); err == nil {
		t.Error("Extract should fail when tag size exceeds input")
	}
}

// TestID3ExtractorPaddingOnly covers two arms:
//   - frame ID first byte == 0: break (padding area)
//   - final == "": return nil, nil
//
// A tag with no frames (just padding) trips both.
func TestID3ExtractorPaddingOnly(t *testing.T) {
	t.Parallel()
	// 100-byte tag of padding (all zeros).
	tag := []byte{
		'I', 'D', '3',
		4, 0, 0,
		0, 0, 0, 0x64, // 100-byte tag size
	}
	tag = append(tag, make([]byte, 100)...)

	chunks, err := NewID3Extractor().Extract(bytes.NewReader(tag), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for padding-only tag", len(chunks))
	}
}

// TestID3ExtractorOversizeFrame covers the
// `if frameSize < 0 || 10+frameSize > len(frames) { break }` arm.
// Build a tag whose first frame claims a body bigger than the
// surrounding tag.
func TestID3ExtractorOversizeFrame(t *testing.T) {
	t.Parallel()
	// Tag header
	tag := []byte{
		'I', 'D', '3',
		4, 0, 0,
		0, 0, 0, 30, // 30-byte tag
	}
	// Frame: TIT2 with frameSize claimed = 200 (way past tag end)
	frame := []byte{'T', 'I', 'T', '2'}
	// syncsafe size = 200
	frame = append(frame, []byte{0, 0, 1, 0x48}...)
	frame = append(frame, []byte{0, 0}...) // flags
	tag = append(tag, frame...)
	// Pad to 30 bytes after header
	tag = append(tag, make([]byte, 30-len(frame))...)

	chunks, err := NewID3Extractor().Extract(bytes.NewReader(tag), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil — frame should have been rejected", len(chunks))
	}
}
