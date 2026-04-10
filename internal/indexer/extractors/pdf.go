package extractors

import (
	"bytes"
	"fmt"
	"io"

	"github.com/ledongthuc/pdf"
)

// PDFExtractor uses github.com/ledongthuc/pdf (a pure-Go MIT-licensed
// fork of Russ Cox's rsc/pdf) to pull the plain-text stream out of PDF
// documents. It gives us full-text search over ebooks, scanned-and-OCRed
// PDFs (if someone else already ran OCR), academic papers, scanned
// receipts, and the miscellaneous PDFs that end up in book torrent packs.
//
// Limitations of M2.3 (may be revisited later):
//   - PDFs with embedded images only (non-searchable scanned PDFs) yield
//     no text. We don't attempt OCR.
//   - Heavily encoded / DRM-encrypted PDFs will error out. We swallow
//     those errors in the pipeline so a single bad PDF doesn't poison
//     a whole torrent's extraction.
//   - The underlying library occasionally panics on malformed PDFs; we
//     recover from panics in Extract so the pipeline worker survives.
type PDFExtractor struct{}

// NewPDFExtractor returns a ready-to-use PDFExtractor.
func NewPDFExtractor() *PDFExtractor { return &PDFExtractor{} }

// Name implements Extractor.
func (*PDFExtractor) Name() string { return "pdf" }

// pdfMaxInputBytes caps how much of an input file we buffer into memory
// to feed pdf.NewReader. A 256 MiB ceiling is enough for even very
// large books and still leaves the pipeline RAM-safe on small hosts.
const pdfMaxInputBytes = 256 * 1024 * 1024

// Extract implements Extractor. It buffers the input into memory (the
// underlying library needs random access, not a streaming read),
// decodes the PDF, and returns the plain text as a series of ~10 KiB
// chunks split at paragraph boundaries.
func (e *PDFExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 || maxBytes > pdfMaxInputBytes {
		maxBytes = pdfMaxInputBytes
	}

	// The pdf package sometimes panics on malformed input; recover so
	// the pipeline worker does not crash. Convert to an error so the
	// caller can log it normally.
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("pdf: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("pdf: read input: %w", err)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	reader, err := pdf.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("pdf: parse: %w", err)
	}

	plain, err := reader.GetPlainText()
	if err != nil {
		return nil, fmt.Errorf("pdf: get plain text: %w", err)
	}
	text, err := io.ReadAll(plain)
	if err != nil {
		return nil, fmt.Errorf("pdf: read plain text: %w", err)
	}

	if len(bytes.TrimSpace(text)) == 0 {
		// Common case for scanned (image-only) PDFs: the text layer is
		// empty. Return nil rather than indexing an empty document.
		return nil, nil
	}

	return chunkText(string(text), DefaultChunkTargetBytes), nil
}

// init registers the PDF extractor. It claims application/pdf by MIME
// type; application/x-pdf (the legacy Microsoft alias) is included for
// tolerance.
func init() {
	Register(NewPDFExtractor(), func(mime string, c Candidate) bool {
		switch mime {
		case "application/pdf", "application/x-pdf":
			return true
		}
		return false
	})
}
