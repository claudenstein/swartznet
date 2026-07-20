package extractors

import (
	"strings"
	"testing"
)

// TestDOCXOutputCapBoundsDecompressedText is the regression for the
// zip-bomb amplification fix: even though a DOCX caps its *compressed*
// input, the decompressed <w:t> text would otherwise accumulate into
// an unbounded strings.Builder. With the output guard, a small
// maxBytes budget caps the extracted text regardless of how much body
// text the document contains.
func TestDOCXOutputCapBoundsDecompressedText(t *testing.T) {
	t.Parallel()

	// Many large paragraphs => lots of decompressed text from a small
	// (highly compressible) zip.
	para := strings.Repeat("alpha bravo charlie ", 2000) // ~40 KiB each
	paragraphs := make([]string, 200)                    // ~8 MiB of text total
	for i := range paragraphs {
		paragraphs[i] = para
	}
	docx := buildMinimalDOCX(t, paragraphs)

	const budget = 256 * 1024
	chunks, err := NewDOCXExtractor().Extract(strings.NewReader(string(docx)), budget)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var total int
	for _, c := range chunks {
		total += len(c.Text)
	}
	// The guard breaks once out.Len() crosses the budget; the final
	// in-flight CharData token can overshoot slightly, so allow a
	// modest multiple of the budget but require it well under the
	// ~8 MiB of total body text.
	if total > 2*1024*1024 {
		t.Fatalf("extracted %d bytes from a %d-byte budget — output cap not bounding zip-bomb amplification", total, budget)
	}
	if total == 0 {
		t.Fatal("expected some extracted text up to the cap, got none")
	}
}
