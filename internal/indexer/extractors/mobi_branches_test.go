package extractors

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// TestMOBIExtractorTooShort covers the
// `if len(buf) < 78+8 { ... }` arm.
func TestMOBIExtractorTooShort(t *testing.T) {
	t.Parallel()
	if _, err := NewMOBIExtractor().Extract(bytes.NewReader(make([]byte, 50)), 0); err == nil {
		t.Error("Extract should fail on input shorter than 86 bytes")
	}
}

// TestMOBIExtractorZeroRecords covers the
// `if numRecords == 0 { return nil, errors.New(...) }` arm.
// Build a 86-byte buffer with numRecords field = 0.
func TestMOBIExtractorZeroRecords(t *testing.T) {
	t.Parallel()
	buf := make([]byte, 86)
	// numRecords at bytes 76-78 = 0 (already zero from make).
	if _, err := NewMOBIExtractor().Extract(bytes.NewReader(buf), 0); err == nil {
		t.Error("Extract should fail when numRecords is 0")
	}
}

// TestMOBIExtractorRec0OffsetOutOfRange covers the
// `if rec0Offset >= len(buf) { ... }` arm. numRecords=1 but
// the offset for record 0 is past the buffer end.
func TestMOBIExtractorRec0OffsetOutOfRange(t *testing.T) {
	t.Parallel()
	buf := make([]byte, 100)
	binary.BigEndian.PutUint16(buf[76:78], 1)        // 1 record
	binary.BigEndian.PutUint32(buf[78:82], 0xFFFFFF) // offset way past EOF
	if _, err := NewMOBIExtractor().Extract(bytes.NewReader(buf), 0); err == nil {
		t.Error("Extract should fail when record 0 offset is past EOF")
	}
}

// TestMOBIExtractorRec0TooShort covers the
// `if len(rec0) < 16+88 { ... }` arm.
func TestMOBIExtractorRec0TooShort(t *testing.T) {
	t.Parallel()
	buf := make([]byte, 200)
	binary.BigEndian.PutUint16(buf[76:78], 1)
	// Point record 0 at offset 198 — only 2 bytes follow.
	binary.BigEndian.PutUint32(buf[78:82], 198)
	if _, err := NewMOBIExtractor().Extract(bytes.NewReader(buf), 0); err == nil {
		t.Error("Extract should fail when record 0 is too short")
	}
}

// TestMOBIExtractorMissingMOBISignature covers the
// `if string(mobiHeader[:4]) != "MOBI" { ... }` arm. Build a
// record 0 of sufficient size but without the MOBI signature.
func TestMOBIExtractorMissingMOBISignature(t *testing.T) {
	t.Parallel()
	// Total: 78 (palmdb) + 8 (rec info) + record body
	rec0Offset := 86
	rec0Len := 16 + 88 + 100 // PalmDOC + MOBI + slack
	buf := make([]byte, rec0Offset+rec0Len)
	binary.BigEndian.PutUint16(buf[76:78], 1)
	binary.BigEndian.PutUint32(buf[78:82], uint32(rec0Offset))
	// MOBI header at rec0Offset+16 = "XXXX..." — not "MOBI".
	copy(buf[rec0Offset+16:], []byte("XXXX"))

	if _, err := NewMOBIExtractor().Extract(bytes.NewReader(buf), 0); err == nil {
		t.Error("Extract should fail when MOBI signature is missing")
	}
}

// TestMOBIExtractorBadHeaderLength covers the
// `if mobiHeaderLen < 16 || int(mobiHeaderLen) > len(mobiHeader) { ... }`
// arm.
func TestMOBIExtractorBadHeaderLength(t *testing.T) {
	t.Parallel()
	rec0Offset := 86
	rec0Len := 16 + 88 + 100
	buf := make([]byte, rec0Offset+rec0Len)
	binary.BigEndian.PutUint16(buf[76:78], 1)
	binary.BigEndian.PutUint32(buf[78:82], uint32(rec0Offset))
	mobiHeaderStart := rec0Offset + 16
	copy(buf[mobiHeaderStart:], []byte("MOBI"))
	// Set mobiHeaderLen to 8 (< 16) → bad.
	binary.BigEndian.PutUint32(buf[mobiHeaderStart+4:mobiHeaderStart+8], 8)

	if _, err := NewMOBIExtractor().Extract(bytes.NewReader(buf), 0); err == nil {
		t.Error("Extract should fail with too-small header length")
	}
}

// TestMOBIExtractorMinimalEmptyText covers the
// `if text == "" { return nil, nil }` arm. Build a valid MOBI
// record 0 with no title and no EXTH so out.String() is empty.
func TestMOBIExtractorMinimalEmptyText(t *testing.T) {
	t.Parallel()
	rec0Offset := 86
	mobiHeaderLen := uint32(132)
	rec0Len := 16 + int(mobiHeaderLen) + 100
	buf := make([]byte, rec0Offset+rec0Len)
	binary.BigEndian.PutUint16(buf[76:78], 1)
	binary.BigEndian.PutUint32(buf[78:82], uint32(rec0Offset))
	mobiHeaderStart := rec0Offset + 16
	copy(buf[mobiHeaderStart:], []byte("MOBI"))
	binary.BigEndian.PutUint32(buf[mobiHeaderStart+4:mobiHeaderStart+8], mobiHeaderLen)
	binary.BigEndian.PutUint32(buf[mobiHeaderStart+12:mobiHeaderStart+16], 1252)
	// titleOffset & titleLen at mobiHeader[84:92] — zero out:
	// already zeros from make. titleLen=0 means no title is written.
	// EXTH flags at mobiHeader[128:132] = 0 → no EXTH.

	chunks, err := NewMOBIExtractor().Extract(bytes.NewReader(buf), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for title-less MOBI without EXTH", len(chunks))
	}
}
