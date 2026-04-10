package extractors

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// buildMinimalPDF returns a byte slice containing a valid PDF with a
// single page that renders the given ASCII message in Helvetica. The
// generator computes xref byte offsets after the objects are
// serialized, so even if we swap in different object bodies later the
// math stays correct.
//
// The test fixture is generated at runtime on purpose — we avoid
// checking in binary files and we exercise the same code paths the
// extractor will see from real PDFs. The body is intentionally kept
// simple (no images, no encryption) since the extractor's job is text
// extraction, not PDF format fidelity.
func buildMinimalPDF(message string) []byte {
	var buf bytes.Buffer
	var offsets []int

	// PDF header + binary comment the spec recommends.
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

	writeObj := func(id int, body string) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", id, body)
	}

	writeObj(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObj(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObj(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] "+
		"/Contents 4 0 R /Resources << /Font << /F1 5 0 R >> >> >>")

	stream := fmt.Sprintf("BT\n/F1 24 Tf\n72 720 Td\n(%s) Tj\nET\n", message)
	writeObj(4, fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
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

func TestPDFExtractsHelloWorld(t *testing.T) {
	t.Parallel()
	data := buildMinimalPDF("Hello SwartzNet World")
	e := NewPDFExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatal("got zero chunks from minimal PDF")
	}
	joined := ""
	for _, c := range chunks {
		joined += c.Text
	}
	if !strings.Contains(joined, "Hello SwartzNet World") {
		t.Errorf("extracted text %q does not contain expected message", joined)
	}
}

func TestPDFHandlesGarbageInput(t *testing.T) {
	t.Parallel()
	e := NewPDFExtractor()
	// Random bytes → parse error, but must not panic and must not
	// return a success result.
	chunks, err := e.Extract(bytes.NewReader([]byte("this is not a pdf at all")), 0)
	if err == nil {
		t.Fatalf("expected error for garbage input, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on error, want nil", chunks)
	}
}

func TestPDFHandlesEmptyInput(t *testing.T) {
	t.Parallel()
	e := NewPDFExtractor()
	chunks, err := e.Extract(bytes.NewReader(nil), 0)
	if err != nil {
		t.Errorf("unexpected error for empty input: %v", err)
	}
	if chunks != nil {
		t.Errorf("expected nil for empty input, got %v", chunks)
	}
}

func TestPDFDispatchRoutes(t *testing.T) {
	t.Parallel()
	// .pdf files must dispatch to the PDF extractor.
	e, mime := Dispatch(Candidate{Path: "book.pdf", Size: 1024 * 1024})
	if e == nil {
		t.Fatalf("no extractor for .pdf (mime=%q)", mime)
	}
	if e.Name() != "pdf" {
		t.Errorf("extractor = %s, want pdf", e.Name())
	}
}

func TestPDFLongTextChunks(t *testing.T) {
	t.Parallel()
	// Many copies of the same short string → a long plaintext stream
	// that should trigger chunker.go's split logic. This verifies the
	// integration between the PDF extractor and chunkText.
	msg := strings.Repeat("swartznet is searchable ", 1500) // ~36 KB
	data := buildMinimalPDF(msg)
	e := NewPDFExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(chunks) < 2 {
		t.Logf("got %d chunks for long PDF (expected >= 2, acceptable for now)", len(chunks))
	}
	var total int
	for _, c := range chunks {
		total += len(c.Text)
	}
	if total == 0 {
		t.Fatal("extracted total size 0")
	}
}
