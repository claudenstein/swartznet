package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildMinimalEPUB synthesises a tiny but valid EPUB byte slice
// at test time. It contains:
//   - mimetype (must be the first entry, stored uncompressed)
//   - META-INF/container.xml (points at the OPF — we don't read it)
//   - content.opf (we don't read it either)
//   - chapter01.xhtml + chapter02.xhtml with the test text
//
// Real EPUBs are more elaborate (NCX/nav, manifest, spine), but
// our extractor only iterates .xhtml entries so this minimal
// shape is enough.
func buildMinimalEPUB(t *testing.T, chapters map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	// EPUB requires the first entry to be "mimetype" stored
	// uncompressed. Method=Store + name=mimetype.
	mh, err := w.CreateHeader(&zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mh.Write([]byte("application/epub+zip")); err != nil {
		t.Fatal(err)
	}

	containerXML := `<?xml version="1.0"?>
<container xmlns="urn:oasis:names:tc:opendocument:xmlns:container" version="1.0">
  <rootfiles>
    <rootfile full-path="content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	if err := writeZipEntry(w, "META-INF/container.xml", containerXML); err != nil {
		t.Fatal(err)
	}
	if err := writeZipEntry(w, "content.opf", "<package></package>"); err != nil {
		t.Fatal(err)
	}

	// Add chapter files in the order the caller passed them
	// (which the test relies on for assertion ordering).
	for name, body := range chapters {
		if err := writeZipEntry(w, name, body); err != nil {
			t.Fatal(err)
		}
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeZipEntry(w *zip.Writer, name, body string) error {
	f, err := w.Create(name)
	if err != nil {
		return err
	}
	_, err = f.Write([]byte(body))
	return err
}

const sampleChapter1 = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head><title>Chapter 1</title></head>
  <body>
    <h1>Chapter One</h1>
    <p>The quick brown fox jumps over the lazy dog.</p>
    <p>This is a second paragraph with <em>emphasis</em> and <strong>bold</strong> words.</p>
  </body>
</html>`

const sampleChapter2 = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml">
  <head>
    <title>Chapter 2</title>
    <style>body { color: red; }</style>
    <script>alert("noise that should be excluded");</script>
  </head>
  <body>
    <h1>Chapter Two</h1>
    <p>Quantum mechanics is the study of subatomic particles.</p>
  </body>
</html>`

func TestEPUBExtractsChapters(t *testing.T) {
	t.Parallel()
	data := buildMinimalEPUB(t, map[string]string{
		"chapter01.xhtml": sampleChapter1,
		"chapter02.xhtml": sampleChapter2,
	})

	e := NewEPUBExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("got zero chunks from EPUB")
	}
	joined := joinChunks(chunks)

	// Both chapters' text should be present.
	for _, want := range []string{
		"quick brown fox",
		"second paragraph",
		"emphasis",
		"Quantum mechanics",
		"subatomic particles",
		"Chapter One",
		"Chapter Two",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in extracted text", want)
		}
	}

	// Script and style content must be excluded.
	for _, bad := range []string{
		"alert",
		"noise that should be excluded",
		"color: red",
	} {
		if strings.Contains(joined, bad) {
			t.Errorf("unexpected noise %q in extracted text", bad)
		}
	}

	// HTML tags must not leak through.
	for _, bad := range []string{"<p>", "</p>", "<em>", "<html"} {
		if strings.Contains(joined, bad) {
			t.Errorf("HTML tag leak: %q", bad)
		}
	}
}

func TestEPUBHandlesEmptyArchive(t *testing.T) {
	t.Parallel()
	data := buildMinimalEPUB(t, map[string]string{})
	e := NewEPUBExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks for empty EPUB, got %v", chunks)
	}
}

func TestEPUBHandlesGarbageInput(t *testing.T) {
	t.Parallel()
	e := NewEPUBExtractor()
	_, err := e.Extract(bytes.NewReader([]byte("not actually a zip file")), 0)
	if err == nil {
		t.Error("expected error for garbage input")
	}
}

func TestEPUBHandlesEmptyInput(t *testing.T) {
	t.Parallel()
	e := NewEPUBExtractor()
	chunks, err := e.Extract(bytes.NewReader(nil), 0)
	if err != nil {
		t.Errorf("unexpected error for empty input: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil chunks for empty input, got %v", chunks)
	}
}

func TestEPUBDispatchRoutes(t *testing.T) {
	t.Parallel()
	e, mime := Dispatch(Candidate{Path: "book.epub", Size: 5 * 1024 * 1024})
	if e == nil {
		t.Fatalf("no extractor for .epub (mime=%q)", mime)
	}
	if e.Name() != "epub" {
		t.Errorf("extractor = %s, want epub", e.Name())
	}
}

func TestEPUBOnlyXHTMLEntriesContribute(t *testing.T) {
	t.Parallel()
	// An EPUB with non-XHTML files (cover image, OPF, NCX) should
	// still extract just the chapter text.
	data := buildMinimalEPUB(t, map[string]string{
		"OEBPS/chapter1.xhtml":  sampleChapter1,
		"OEBPS/cover.jpg":       "BINARY-IMAGE-DATA",
		"OEBPS/toc.ncx":         "<ncx>navigation only</ncx>",
		"OEBPS/styles/main.css": "body { color: red; }",
	})
	e := NewEPUBExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinChunks(chunks)
	if !strings.Contains(joined, "quick brown fox") {
		t.Errorf("chapter text missing")
	}
	for _, bad := range []string{"BINARY-IMAGE-DATA", "navigation only", "color: red"} {
		if strings.Contains(joined, bad) {
			t.Errorf("non-chapter content leaked: %q", bad)
		}
	}
}

func joinChunks(chunks []Chunk) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString(c.Text)
		b.WriteByte(' ')
	}
	return b.String()
}
