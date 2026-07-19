package extractors

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// buildEpubBomb streams a valid EPUB with nChapters, each of which emits a tiny
// visible MARKERnnn but hides scriptBytes of highly-compressible padding inside a
// <script> (which yields NO visible text). The chapters are written streaming, so
// the fixture stays small even for a large total decompressed size.
func buildEpubBomb(t *testing.T, nChapters, scriptBytes int) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)

	mh, err := w.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	mh.Write([]byte("application/epub+zip"))
	if err := writeZipEntry(w, "META-INF/container.xml", "<container/>"); err != nil {
		t.Fatal(err)
	}
	if err := writeZipEntry(w, "content.opf", "<package/>"); err != nil {
		t.Fatal(err)
	}

	chunk := bytes.Repeat([]byte("A"), 64*1024)
	for i := 0; i < nChapters; i++ {
		f, err := w.Create(fmt.Sprintf("ch%03d.xhtml", i))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(f, "<html><body><p>MARKER%03d</p><script>", i)
		for written := 0; written < scriptBytes; written += len(chunk) {
			if _, err := f.Write(chunk); err != nil {
				t.Fatal(err)
			}
		}
		io.WriteString(f, "</script></body></html>")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestEPUBTotalDecompressionBounded pins the round-8 fix: a crafted EPUB whose
// chapters decompress far more than the total budget — each emitting NO visible
// text (a <script> body) so the OUTPUT budget never shrinks — must not decompress
// them all. The shared total-decompression budget, charged by bytes CONSUMED,
// stops extraction, so the last chapters are never reached.
func TestEPUBTotalDecompressionBounded(t *testing.T) {
	const nCh = 16
	const scriptBytes = 24 << 20 // 24 MiB/chapter → 384 MiB total if unbounded (budget is 256 MiB)

	data := buildEpubBomb(t, nCh, scriptBytes)
	if len(data) > 8<<20 {
		t.Fatalf("bomb EPUB is %d bytes; expected small (repetitive padding compresses)", len(data))
	}

	chunks, err := NewEPUBExtractor().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var joined strings.Builder
	for _, c := range chunks {
		joined.WriteString(c.Text)
	}
	got := joined.String()

	if !strings.Contains(got, "MARKER000") {
		t.Error("first chapter's text missing — extraction did not start")
	}
	// The total-decompression budget (256 MiB) is exhausted after ~10 of the 16
	// 24-MiB chapters, so the last chapter is never processed. Without the fix,
	// every chapter's marker would appear.
	if last := fmt.Sprintf("MARKER%03d", nCh-1); strings.Contains(got, last) {
		t.Errorf("%s present — total decompression not bounded (all %d chapters decompressed)", last, nCh)
	}
}
