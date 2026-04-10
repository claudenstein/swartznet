package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildMinimalDOCX synthesises a tiny but valid .docx file at
// test time. It contains:
//   - [Content_Types].xml (must be present, we don't read it)
//   - _rels/.rels (relationship descriptor — also not read)
//   - word/document.xml with the test paragraphs
//
// Real DOCX files have many more parts (styles, fonts, theme,
// settings, web settings, footnotes, etc.). The extractor only
// touches word/document.xml so this minimal shape exercises the
// real code path without committing binary fixtures.
func buildMinimalDOCX(t *testing.T, paragraphs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	for _, e := range []struct{ name, body string }{
		{"[Content_Types].xml", `<?xml version="1.0"?><Types/>`},
		{"_rels/.rels", `<?xml version="1.0"?><Relationships/>`},
		{"word/document.xml", buildDocumentXML(paragraphs)},
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

// buildDocumentXML returns a minimal WordprocessingML body whose
// <w:body> contains one <w:p> per input paragraph, with a single
// <w:t> text run per paragraph holding the supplied string.
func buildDocumentXML(paragraphs []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	b.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	b.WriteString(`<w:body>`)
	for _, p := range paragraphs {
		b.WriteString(`<w:p><w:r><w:t>`)
		b.WriteString(xmlEscape(p))
		b.WriteString(`</w:t></w:r></w:p>`)
	}
	b.WriteString(`</w:body></w:document>`)
	return b.String()
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

func TestDOCXExtractsParagraphs(t *testing.T) {
	t.Parallel()
	data := buildMinimalDOCX(t, []string{
		"The quick brown fox jumps over the lazy dog.",
		"This is the second paragraph of the test document.",
		"Quantum mechanics describes subatomic particles.",
	})

	e := NewDOCXExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("got zero chunks from minimal DOCX")
	}
	joined := joinChunks(chunks)
	for _, want := range []string{
		"quick brown fox",
		"second paragraph",
		"Quantum mechanics",
		"subatomic particles",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in extracted text:\n%s", want, joined)
		}
	}
	// XML markup must not leak through.
	for _, bad := range []string{"<w:p>", "<w:t>", "</w:body>", "xmlns"} {
		if strings.Contains(joined, bad) {
			t.Errorf("XML tag leak: %q", bad)
		}
	}
}

func TestDOCXHandlesGarbageInput(t *testing.T) {
	t.Parallel()
	e := NewDOCXExtractor()
	_, err := e.Extract(bytes.NewReader([]byte("not a zip at all")), 0)
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

func TestDOCXHandlesEmptyInput(t *testing.T) {
	t.Parallel()
	e := NewDOCXExtractor()
	chunks, err := e.Extract(bytes.NewReader(nil), 0)
	if err != nil {
		t.Errorf("unexpected error for empty input: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks for empty input, got %v", chunks)
	}
}

func TestDOCXHandlesArchiveWithoutDocumentXML(t *testing.T) {
	t.Parallel()
	// Build a zip that has the .docx envelope shape but no
	// word/document.xml inside — could be an .xlsx or .pptx that
	// reused the same MIME by mistake. Should yield nil chunks
	// rather than an error.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("[Content_Types].xml")
	f.Write([]byte("<?xml version='1.0'?><Types/>"))
	w.Close()

	e := NewDOCXExtractor()
	chunks, err := e.Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

func TestDOCXDispatchRoutes(t *testing.T) {
	t.Parallel()
	e, mime := Dispatch(Candidate{Path: "report.docx", Size: 100 * 1024})
	if e == nil {
		t.Fatalf("no extractor for .docx (mime=%q)", mime)
	}
	if e.Name() != "docx" {
		t.Errorf("extractor = %s, want docx", e.Name())
	}
}

func TestDOCXHandlesParagraphBoundaries(t *testing.T) {
	t.Parallel()
	// Two paragraphs should produce a paragraph boundary (i.e.
	// the chunker can see them as distinct). Verify by checking
	// that the extracted text contains a "\n\n" between them.
	data := buildMinimalDOCX(t, []string{
		"First paragraph.",
		"Second paragraph.",
	})
	e := NewDOCXExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinChunks(chunks)
	if !strings.Contains(joined, "First paragraph.") {
		t.Errorf("first paragraph missing")
	}
	if !strings.Contains(joined, "Second paragraph.") {
		t.Errorf("second paragraph missing")
	}
}

func TestDOCXEscapedXMLEntities(t *testing.T) {
	t.Parallel()
	// Make sure the decoder unescapes &amp; / &lt; / &gt; / &apos;
	// in body text.
	data := buildMinimalDOCX(t, []string{
		"This <test> & 'document' has \"special\" chars.",
	})
	e := NewDOCXExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinChunks(chunks)
	if !strings.Contains(joined, "<test>") {
		t.Errorf("&lt;test&gt; not unescaped: %q", joined)
	}
	if !strings.Contains(joined, "& 'document'") {
		t.Errorf("&amp; / &apos; not unescaped: %q", joined)
	}
}
