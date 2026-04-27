package extractors

import (
	"bytes"
	"testing"
)

// TestMKVExtractorTooShort covers the
// `id, size, err := readEBMLHeader(br); if err != nil { ... }`
// arm. A near-empty input fails the first VINT read.
func TestMKVExtractorTooShort(t *testing.T) {
	t.Parallel()
	if _, err := NewMKVExtractor().Extract(bytes.NewReader([]byte{0x00}), 0); err == nil {
		t.Error("Extract should fail on near-empty input")
	}
}

// TestMKVExtractorMissingSegment covers the
// `if id != ebmlIDSegment { return nil, ... }` arm. Build an
// EBML header followed by an arbitrary non-Segment element.
func TestMKVExtractorMissingSegment(t *testing.T) {
	t.Parallel()
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))
	// Use ebmlIDInfo (0x1549A966) as the second element instead
	// of a Segment.
	bogus := ebmlElem(ebmlIDInfo, []byte{})
	file := append(ebmlHeader, bogus...)
	if _, err := NewMKVExtractor().Extract(bytes.NewReader(file), 0); err == nil {
		t.Error("Extract should fail when EBML is not followed by Segment")
	}
}

// TestMKVExtractorEmptySegmentReturnsNil covers the
// `if text == "" { return nil, nil }` arm. A Segment with no
// recognized child elements yields no extracted text.
func TestMKVExtractorEmptySegmentReturnsNil(t *testing.T) {
	t.Parallel()
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))
	// Segment containing nothing — no Info, Tracks, Chapters or Tags.
	segment := ebmlElem(ebmlIDSegment, []byte{})
	file := append(ebmlHeader, segment...)

	chunks, err := NewMKVExtractor().Extract(bytes.NewReader(file), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for empty segment", len(chunks))
	}
}

// TestMKVExtractorDiscardsUnknownChildren covers the `default`
// arm in the segment-walk switch — children whose id isn't
// Info/Tracks/Chapters/Tags are skipped via br.Discard. Mix
// one unknown child into a segment that also has Info+Title
// so we know extraction still produces output.
func TestMKVExtractorDiscardsUnknownChildren(t *testing.T) {
	t.Parallel()
	ebmlHeader := ebmlElem(ebmlIDEBML, []byte("dummy"))
	info := ebmlElem(ebmlIDInfo, ebmlElem(ebmlIDTitle, []byte("Real Title")))
	// Cluster (0x1F43B675) is a known unknown — it's not in our
	// switch but it's a real MKV element so the writer doesn't
	// reject it.
	cluster := ebmlElem(0x1F43B675, []byte("ignore me"))
	segment := ebmlElem(ebmlIDSegment, append(cluster, info...))
	file := append(ebmlHeader, segment...)

	chunks, err := NewMKVExtractor().Extract(bytes.NewReader(file), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected one chunk")
	}
}
