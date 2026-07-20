package extractors

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestDOCXEmptyDocumentReturnsNoChunks covers the
// `if strings.TrimSpace(text) == "" { return nil, nil }` arm in
// DOCXExtractor.Extract. A document.xml whose body holds no
// printable runs (just whitespace and structural elements) yields
// an empty extracted string; the extractor must return nil chunks
// rather than ship an empty Chunk to the indexer.
func TestDOCXEmptyDocumentReturnsNoChunks(t *testing.T) {
	t.Parallel()
	// Body has a paragraph with only whitespace inside its text run.
	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` +
		`<w:body><w:p><w:r><w:t>   </w:t></w:r></w:p></w:body>` +
		`</w:document>`
	data := buildMinimalDOCXRaw(t, docXML)

	e := NewDOCXExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("got %d chunks, want nil for whitespace-only doc", len(chunks))
	}
}

// TestDOCXMalformedDocumentXMLPropagatesError covers the
// `extractDocumentText err != nil → return nil, err` arm. The
// document.xml is broken at the token level, which the strict-off
// XML decoder still rejects.
func TestDOCXMalformedDocumentXMLPropagatesError(t *testing.T) {
	t.Parallel()
	// A broken tag — opening bracket without closing produces a
	// token-level decode error even with Strict=false.
	docXML := `<?xml version="1.0"?><<<w:body</w:document>`
	data := buildMinimalDOCXRaw(t, docXML)

	e := NewDOCXExtractor()
	_, err := e.Extract(bytes.NewReader(data), 0)
	if err == nil {
		t.Error("expected XML decode error for malformed document.xml")
	}
}

// buildMinimalDOCXRaw is buildMinimalDOCX but takes a raw
// document.xml string instead of synthesising one from paragraphs.
func buildMinimalDOCXRaw(t *testing.T, documentXML string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types/>`},
		{"_rels/.rels", `<?xml version="1.0"?><Relationships/>`},
		{"word/document.xml", documentXML},
	} {
		f, err := w.Create(e.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
