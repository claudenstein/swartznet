package extractors

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// buildFlateBombPDF returns a small valid PDF whose single page's /Contents is a
// FlateDecode stream that decompresses to decompressedSize bytes of text-show
// operators. The stream is compressed by streaming, so the test never holds the
// decompressed content in memory.
func buildFlateBombPDF(t *testing.T, decompressedSize int) []byte {
	t.Helper()
	var compressed bytes.Buffer
	zw := zlib.NewWriter(&compressed)
	chunk := []byte(strings.Repeat("(AAAA)Tj\n", 512)) // ~4.6 KiB of show-text ops
	for written := 0; written < decompressedSize; written += len(chunk) {
		if _, err := zw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	comp := compressed.Bytes()

	var buf bytes.Buffer
	var offsets []int
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	writeObj := func(id int, body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}
	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
		"/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")
	// Object 4: the FlateDecode content stream (binary — write raw for byte fidelity).
	offsets = append(offsets, buf.Len())
	fmt.Fprintf(&buf, "4 0 obj\n<< /Filter /FlateDecode /Length %d >>\nstream\n", len(comp))
	buf.Write(comp)
	buf.WriteString("\nendstream\nendobj\n")
	writeObj(5, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	xrefOff := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n", len(offsets)+1)
	buf.WriteString("0000000000 65535 f \n")
	for _, off := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(offsets)+1, xrefOff)
	return buf.Bytes()
}

// TestPDFFlateBombIsBounded pins the round-7 HIGH fix: a small PDF whose page
// decompresses to a huge text stream must NOT be materialized (the old
// GetPlainText + post-hoc LimitReader OOM-crashed the daemon). The page's
// decompressed content exceeds the extract cap, so it is skipped, and peak
// allocation stays bounded.
func TestPDFFlateBombIsBounded(t *testing.T) {
	const decompressed = 256 << 20 // 256 MiB of text operators
	const extractCap = 2 << 20     // 2 MiB extract budget (> the PDF, < the bomb)

	data := buildFlateBombPDF(t, decompressed)
	if len(data) > extractCap {
		t.Fatalf("bomb PDF is %d bytes; must be under the extract cap %d to read fully", len(data), extractCap)
	}

	e := NewPDFExtractor()
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	chunks, err := e.Extract(bytes.NewReader(data), extractCap)
	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("bomb extraction errored (should skip the page + return empty): %v", err)
	}
	if len(chunks) != 0 {
		t.Errorf("expected no chunks from a skipped bomb page, got %d", len(chunks))
	}
	const allocCap = 48 << 20 // far below 256 MiB, comfortably above bounded work
	if grew := after.TotalAlloc - before.TotalAlloc; grew > allocCap {
		t.Fatalf("PDF extraction allocated %d bytes for a %d-byte cap — the bomb was materialized", grew, extractCap)
	}
}

// TestPDFStillExtractsAfterBombGuard guards against over-tightening: a normal
// (small-content) PDF still extracts its text through the bounded path.
func TestPDFStillExtractsAfterBombGuard(t *testing.T) {
	data := buildMinimalPDF("BoundedPathStillWorks")
	chunks, err := e0().Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	joined := ""
	for _, c := range chunks {
		joined += c.Text
	}
	if !strings.Contains(joined, "BoundedPathStillWorks") {
		t.Errorf("bounded path lost the text: %q", joined)
	}
}

func e0() *PDFExtractor { return NewPDFExtractor() }
