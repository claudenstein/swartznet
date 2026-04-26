package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestPPTXExtractorEmptyInput covers the
// `if len(buf) == 0 { return nil, nil }` arm.
func TestPPTXExtractorEmptyInput(t *testing.T) {
	t.Parallel()
	chunks, err := NewPPTXExtractor().Extract(strings.NewReader(""), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got chunks for empty input, want nil")
	}
}

// TestPPTXExtractorBadZip covers the
// `zr, err := zip.NewReader(...); if err != nil { ... }` arm.
func TestPPTXExtractorBadZip(t *testing.T) {
	t.Parallel()
	if _, err := NewPPTXExtractor().Extract(strings.NewReader("not a zip"), 0); err == nil {
		t.Error("Extract should fail on non-zip input")
	}
}

// TestPPTXExtractorZipWithoutSlides covers the
// `if len(slides) == 0 { return nil, nil }` arm. A valid zip
// without any ppt/slides/*.xml entries — clearly not a deck.
func TestPPTXExtractorZipWithoutSlides(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("docProps/app.xml")
	w.Write([]byte("<Properties/>"))
	zw.Close()

	chunks, err := NewPPTXExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks for zip without slides, want nil", len(chunks))
	}
}

// TestPPTXExtractorEmptySlideText covers the
// `final := ...TrimSpace(...); if final == "" { return nil, nil }` arm.
// A deck whose slides have no a:t text runs yields empty output.
func TestPPTXExtractorEmptySlideText(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ppt/slides/slide1.xml")
	w.Write([]byte(`<?xml version="1.0"?><p:sld><p:cSld></p:cSld></p:sld>`))
	zw.Close()

	chunks, err := NewPPTXExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks for empty-text deck, want nil", len(chunks))
	}
}
