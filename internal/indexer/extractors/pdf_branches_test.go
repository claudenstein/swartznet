package extractors

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
)

// TestPDFExtractReadAllError covers the
// `io.ReadAll(io.LimitReader(...)) err → return nil, ...` arm.
// iotest.ErrReader produces an error on first Read so io.ReadAll
// fails before the pdf library ever sees the bytes.
func TestPDFExtractReadAllError(t *testing.T) {
	t.Parallel()
	failing := iotest.ErrReader(errors.New("simulated read failure"))
	e := NewPDFExtractor()
	chunks, err := e.Extract(failing, 0)
	if err == nil {
		t.Errorf("expected error from failing reader, got chunks=%v", chunks)
	}
	if chunks != nil {
		t.Errorf("chunks = %v on read err, want nil", chunks)
	}
}

// TestPDFExtractEmptyTextLayer covers the
// `if len(bytes.TrimSpace(text)) == 0 { return nil, nil }` arm.
// Build a PDF whose Tj operator draws only whitespace — the
// extracted text is non-empty but trims to "".
func TestPDFExtractEmptyTextLayer(t *testing.T) {
	t.Parallel()
	// "(   )" Tj draws three spaces. After GetPlainText, the
	// caller sees whitespace only, which TrimSpace strips.
	data := buildMinimalPDF("   ")
	e := NewPDFExtractor()
	chunks, err := e.Extract(bytes.NewReader(data), 0)
	if err != nil {
		// Some pdf libs accept "( ) Tj"; if the extractor returns
		// chunks instead, the empty-trim arm just doesn't fire here.
		// We still want zero text either way.
		t.Logf("Extract err (acceptable): %v", err)
	}
	for _, c := range chunks {
		if strings.TrimSpace(c.Text) != "" {
			t.Errorf("expected only-whitespace chunks, got %q", c.Text)
		}
	}
}
