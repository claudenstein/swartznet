package extractors

import (
	"bytes"
	"testing"
)

// TestMKVExtractorEBMLBodyTruncated covers the
// `if _, err := br.Discard(int(size)); err != nil` arm in the
// EBML-body skip block. We hand-build EBML header that claims a
// 50-byte body but only supplies 5 — Discard fails.
func TestMKVExtractorEBMLBodyTruncated(t *testing.T) {
	t.Parallel()
	// EBML id + claimed size 50 + only 5 bytes of body.
	hdr := append(ebmlID(ebmlIDEBML), ebmlSize(50)...)
	hdr = append(hdr, []byte("short")...)
	if _, err := NewMKVExtractor().Extract(bytes.NewReader(hdr), 0); err == nil {
		t.Error("Extract should fail when EBML body is shorter than declared")
	}
}

// TestMKVExtractorSegmentHeaderMissing covers the
// `id, _, err = readEBMLHeader(br); if err != nil` arm. Provide a
// well-formed EBML element but nothing after it — the second
// readEBMLHeader call hits EOF before any bytes.
func TestMKVExtractorSegmentHeaderMissing(t *testing.T) {
	t.Parallel()
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))
	// No segment, no anything after EBML.
	if _, err := NewMKVExtractor().Extract(bytes.NewReader(ebmlHeader), 0); err == nil {
		t.Error("Extract should fail when no Segment header follows EBML")
	}
}

// TestMKVExtractorChildBodyTruncated covers the
// `body, err := readFull(br, int(size)); if err != nil` arm.
// Build a Segment whose Info child claims 100 bytes of body but
// supplies 5.
func TestMKVExtractorChildBodyTruncated(t *testing.T) {
	t.Parallel()
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))

	// Hand-craft an Info element whose declared size is 100 but body is "short".
	infoHdr := append(ebmlID(ebmlIDInfo), ebmlSize(100)...)
	infoHdr = append(infoHdr, []byte("short")...)

	// Segment whose body is just this truncated Info child. Use a
	// declared size that matches what we actually have (5+hdrlen)
	// so readEBMLHeader on the segment succeeds; readFull on the
	// Info child then fails when it tries to pull 100 bytes out.
	segment := append(ebmlID(ebmlIDSegment), ebmlSize(len(infoHdr))...)
	segment = append(segment, infoHdr...)

	file := append(ebmlHeader, segment...)
	if _, err := NewMKVExtractor().Extract(bytes.NewReader(file), 0); err == nil {
		t.Error("Extract should fail when an Info child body is truncated")
	}
}

// TestMKVExtractorUnknownChildDiscardTruncated covers the
// `if _, err := br.Discard(int(size)); err != nil` arm in the
// default-case (unknown id). Build a segment containing one
// unknown child whose declared size exceeds available bytes.
func TestMKVExtractorUnknownChildDiscardTruncated(t *testing.T) {
	t.Parallel()
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))

	// Cluster (0x1F43B675) is in the default branch; declare 100 bytes
	// but supply 5.
	unknownHdr := append(ebmlID(0x1F43B675), ebmlSize(100)...)
	unknownHdr = append(unknownHdr, []byte("short")...)

	segment := append(ebmlID(ebmlIDSegment), ebmlSize(len(unknownHdr))...)
	segment = append(segment, unknownHdr...)

	file := append(ebmlHeader, segment...)
	if _, err := NewMKVExtractor().Extract(bytes.NewReader(file), 0); err == nil {
		t.Error("Extract should fail when an unknown child's discard runs short")
	}
}
