package extractors

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestODTExtractorEmptyContentReturnsNil covers the
// `if strings.TrimSpace(text) == "" { return nil, nil }` arm.
// content.xml has only structural elements with no character
// data, so extracted text is empty.
func TestODTExtractorEmptyContentReturnsNil(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	mh, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	mh.Write([]byte("application/vnd.oasis.opendocument.text"))

	contentXML := `<?xml version="1.0"?>` +
		`<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0">` +
		`<office:body><office:text></office:text></office:body>` +
		`</office:document-content>`
	cf, err := w.Create("content.xml")
	if err != nil {
		t.Fatal(err)
	}
	cf.Write([]byte(contentXML))
	w.Close()

	chunks, err := NewODTExtractor().Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for empty-text ODT", len(chunks))
	}
}

// TestODTExtractorMalformedContentXML covers the
// `text, err := extractODTText(...); if err != nil { return ... }`
// arm. Plant invalid XML in content.xml; the strict-off decoder
// still rejects token-level garbage.
func TestODTExtractorMalformedContentXML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	mh, _ := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	mh.Write([]byte("application/vnd.oasis.opendocument.text"))

	cf, _ := w.Create("content.xml")
	// "<<<" produces a token-level decode error even with
	// Strict=false.
	cf.Write([]byte("<<<garbage"))
	w.Close()

	if _, err := NewODTExtractor().Extract(bytes.NewReader(buf.Bytes()), 0); err == nil {
		t.Error("Extract should fail on malformed content.xml")
	}
}
