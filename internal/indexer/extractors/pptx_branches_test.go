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

// TestPPTXExtractorBreakInSlideAddsNewline covers
// extractDrawingMLText's `case "br": out.WriteByte('\n')` arm.
// A slide with a <a:br/> element triggers an inline newline in
// the extracted text.
func TestPPTXExtractorBreakInSlideAddsNewline(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ppt/slides/slide1.xml")
	w.Write([]byte(`<?xml version="1.0"?>` +
		`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody>` +
		`<a:p><a:r><a:t>before</a:t></a:r><a:br/><a:r><a:t>after</a:t></a:r></a:p>` +
		`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`))
	zw.Close()

	chunks, err := NewPPTXExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	got := chunks[0].Text
	if !strings.Contains(got, "before") || !strings.Contains(got, "after") {
		t.Errorf("missing text in:\n%s", got)
	}
	if !strings.Contains(got, "before\nafter") {
		t.Errorf("expected newline between 'before' and 'after' from <a:br/>:\n%q", got)
	}
}

// TestPPTXExtractorMalformedSlideXML covers extractDrawingMLText's
// `if err != nil { return ..., fmt.Errorf(...) }` arm. A slide
// with token-level XML garbage triggers a decode error.
func TestPPTXExtractorMalformedSlideXML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("ppt/slides/slide1.xml")
	w.Write([]byte("<<<garbage"))
	zw.Close()

	if _, err := NewPPTXExtractor().Extract(bytes.NewReader(buf.Bytes()), 0); err == nil {
		t.Error("Extract should fail on malformed slide XML")
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
