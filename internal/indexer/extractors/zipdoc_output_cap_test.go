package extractors

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// buildStoredZip assembles a zip archive with the given entries
// using Method: Store, so building the multi-MiB bomb payloads
// below doesn't burn test time deflating them. The extractors'
// per-entry readers behave identically either way — the point is
// that the DECOMPRESSED entry stream handed to the XML/HTML
// parsers is bounded, regardless of how the entry was packed.
func buildStoredZip(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestZipDocExtractorsBoundDecompressedEntryStream is the
// regression test for the decompression-bomb finding: every
// ZIP-container document extractor must wrap the zip-entry reader
// in io.LimitReader before handing it to the XML/HTML parser.
// Without the limit a single oversized text node buffers the whole
// decompressed body inside one Token()/Next() call — here that
// would surface as the full bomb-sized text coming back as chunks.
//
// Truncation at the budget may surface as a decode error (the XML
// walkers fail closed mid-CharData) or as gracefully truncated
// text (the EPUB/HTML tokenizer); both are acceptable. What must
// never happen is err == nil with more than maxDocTextBytes of
// output.
func TestZipDocExtractorsBoundDecompressedEntryStream(t *testing.T) {
	// One text node 1 MiB past the output budget. Big enough that
	// an unbounded path provably exceeds the cap, small enough to
	// keep the test quick.
	bomb := strings.Repeat("a", maxDocTextBytes+1<<20)

	cases := []struct {
		name  string
		ex    Extractor
		entry string
		body  string
	}{
		// Each body is WELL-FORMED: against an unbounded path the
		// parse succeeds and the full bomb comes back as chunks
		// (which this test rejects); against the bounded code the
		// budget truncates before the closing tags, surfacing as a
		// decode error or graceful truncation.
		{
			name:  "docx",
			ex:    NewDOCXExtractor(),
			entry: "word/document.xml",
			body:  `<?xml version="1.0"?><document><body><p><t>` + bomb + `</t></p></body></document>`,
		},
		{
			name:  "odt",
			ex:    NewODTExtractor(),
			entry: "content.xml",
			body:  `<?xml version="1.0"?><office><body><text><p>` + bomb + `</p></text></body></office>`,
		},
		{
			name:  "epub",
			ex:    NewEPUBExtractor(),
			entry: "OEBPS/ch1.xhtml",
			body:  `<html><body><p>` + bomb + `</p></body></html>`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := buildStoredZip(t, map[string]string{tc.entry: tc.body})
			chunks, err := tc.ex.Extract(bytes.NewReader(data), 0)
			if err != nil {
				// Fail-closed truncation error: acceptable, and
				// inherently bounded.
				return
			}
			var total int
			for _, c := range chunks {
				total += len(c.Text)
			}
			if total > maxDocTextBytes {
				t.Fatalf("%s: extracted %d bytes of text, exceeds the %d output budget — decompressed entry stream is unbounded",
					tc.name, total, maxDocTextBytes)
			}
		})
	}
}
