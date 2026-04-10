package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildMinimalODT synthesises a minimal but valid .odt at test
// time. The real OpenDocument shape includes mimetype, manifest,
// styles, settings, and meta files; the extractor only reads
// content.xml so this minimal version is enough to exercise the
// real code path.
func buildMinimalODT(t *testing.T, paragraphs []string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// mimetype must come first and be stored uncompressed,
	// matching the OpenDocument packaging spec.
	mh, err := w.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mh.Write([]byte("application/vnd.oasis.opendocument.text")); err != nil {
		t.Fatal(err)
	}

	for _, e := range []struct{ name, body string }{
		{"META-INF/manifest.xml", `<?xml version="1.0"?><manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"/>`},
		{"content.xml", buildODTContentXML(paragraphs)},
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

// buildODTContentXML returns a minimal OpenDocument content.xml
// whose body contains one <text:p> per input paragraph plus an
// <office:automatic-styles> block we deliberately fill with noise
// the extractor must not include in its output.
func buildODTContentXML(paragraphs []string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	b.WriteString(`<office:document-content `)
	b.WriteString(`xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" `)
	b.WriteString(`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" `)
	b.WriteString(`xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0">`)
	b.WriteString(`<office:automatic-styles>`)
	b.WriteString(`<style:style style:name="P1">noise that should be excluded</style:style>`)
	b.WriteString(`</office:automatic-styles>`)
	b.WriteString(`<office:body><office:text>`)
	for _, p := range paragraphs {
		b.WriteString(`<text:p text:style-name="P1">`)
		b.WriteString(xmlEscape(p))
		b.WriteString(`</text:p>`)
	}
	b.WriteString(`</office:text></office:body>`)
	b.WriteString(`</office:document-content>`)
	return b.String()
}

func TestODTExtractsParagraphs(t *testing.T) {
	t.Parallel()
	data := buildMinimalODT(t, []string{
		"The quick brown fox jumps over the lazy dog.",
		"Quantum mechanics describes subatomic particles.",
	})

	e := NewODTExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("got zero chunks from minimal ODT")
	}
	joined := joinChunks(chunks)
	for _, want := range []string{
		"quick brown fox",
		"Quantum mechanics",
		"subatomic particles",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in extracted text:\n%s", want, joined)
		}
	}
	// The automatic-styles noise must NOT appear in the output.
	if strings.Contains(joined, "noise that should be excluded") {
		t.Errorf("style noise leaked into extracted text:\n%s", joined)
	}
	// And no XML markup should leak through.
	for _, bad := range []string{"<text:p>", "</text:p>", "<office", "xmlns"} {
		if strings.Contains(joined, bad) {
			t.Errorf("XML tag leak: %q", bad)
		}
	}
}

func TestODTHandlesGarbageInput(t *testing.T) {
	t.Parallel()
	e := NewODTExtractor()
	_, err := e.Extract(bytes.NewReader([]byte("not actually a zip")), 0)
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

func TestODTHandlesArchiveWithoutContentXML(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, _ := w.Create("META-INF/manifest.xml")
	f.Write([]byte("<?xml version='1.0'?><manifest/>"))
	w.Close()

	e := NewODTExtractor()
	chunks, err := e.Extract(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}

func TestODTDispatchRoutes(t *testing.T) {
	t.Parallel()
	e, mime := Dispatch(Candidate{Path: "report.odt", Size: 75 * 1024})
	if e == nil {
		t.Fatalf("no extractor for .odt (mime=%q)", mime)
	}
	if e.Name() != "odt" {
		t.Errorf("extractor = %s, want odt", e.Name())
	}
}

func TestODTEmptyInput(t *testing.T) {
	t.Parallel()
	e := NewODTExtractor()
	chunks, err := e.Extract(bytes.NewReader(nil), 0)
	if err != nil {
		t.Errorf("unexpected error for empty input: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks, got %v", chunks)
	}
}
