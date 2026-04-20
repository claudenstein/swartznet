package extractors

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

// TestReadFullPropagatesEOF covers the err branch of readFull —
// the underlying reader runs out before n bytes can be read.
func TestReadFullPropagatesEOF(t *testing.T) {
	t.Parallel()
	br := bufio.NewReader(bytes.NewReader([]byte("ABC")))
	got, err := readFull(br, 10)
	if err == nil {
		t.Fatalf("expected error, got %q", got)
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		t.Errorf("err = %v, want EOF/ErrUnexpectedEOF", err)
	}
}

// TestReadOGGPageBadCapturePattern covers the
// `string(header[:4]) != "OggS"` guard.
func TestReadOGGPageBadCapturePattern(t *testing.T) {
	t.Parallel()
	// 27 bytes of garbage not starting with "OggS".
	buf := make([]byte, 27)
	copy(buf, "NotOggSnippet")
	br := bufio.NewReader(bytes.NewReader(buf))
	if _, err := readOGGPage(br); err == nil {
		t.Error("readOGGPage should reject non-OggS header")
	}
}

// TestReadOGGPageHeaderEOF covers the very first error branch:
// readFull(br, 27) returning EOF.
func TestReadOGGPageHeaderEOF(t *testing.T) {
	t.Parallel()
	br := bufio.NewReader(bytes.NewReader([]byte{}))
	if _, err := readOGGPage(br); err == nil {
		t.Error("readOGGPage on empty reader should return error")
	}
}

// TestReadOGGPageSegTableEOF covers the segTable readFull error
// branch: a valid 27-byte header announcing segCount segments,
// but the reader has nothing left for the segment table.
func TestReadOGGPageSegTableEOF(t *testing.T) {
	t.Parallel()
	hdr := make([]byte, 27)
	copy(hdr[:4], "OggS")
	hdr[26] = 5 // segCount=5; reader will EOF trying to read 5 more bytes
	br := bufio.NewReader(bytes.NewReader(hdr))
	if _, err := readOGGPage(br); err == nil {
		t.Error("readOGGPage should error when segment table can't be read")
	}
}

// TestReadOGGPageBodyEOF covers the body readFull error branch:
// header + segTable read fine but the declared body size exceeds
// what's available.
func TestReadOGGPageBodyEOF(t *testing.T) {
	t.Parallel()
	hdr := make([]byte, 27)
	copy(hdr[:4], "OggS")
	hdr[26] = 1            // segCount=1
	body := []byte{0xFF}   // segTable byte → declares 255-byte segment
	full := append(hdr, body...)
	br := bufio.NewReader(bytes.NewReader(full))
	if _, err := readOGGPage(br); err == nil {
		t.Error("readOGGPage should error when body bytes can't be read")
	}
}

// TestReadOGGPageSuccessPath covers the happy path end-to-end —
// minimal Ogg page with segCount=1, one 4-byte segment.
func TestReadOGGPageSuccessPath(t *testing.T) {
	t.Parallel()
	hdr := make([]byte, 27)
	copy(hdr[:4], "OggS")
	hdr[26] = 1
	full := append(hdr, byte(4))      // segTable: one 4-byte segment
	full = append(full, []byte("ABCD")...) // body
	br := bufio.NewReader(bytes.NewReader(full))
	page, err := readOGGPage(br)
	if err != nil {
		t.Fatalf("readOGGPage: %v", err)
	}
	if string(page.data) != "ABCD" {
		t.Errorf("page.data = %q, want \"ABCD\"", page.data)
	}
}
