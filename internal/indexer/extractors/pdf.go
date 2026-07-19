package extractors

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// PDFExtractor uses github.com/ledongthuc/pdf (a pure-Go BSD-3-Clause
// fork of Russ Cox's rsc/pdf, (c) The Go Authors) to pull the plain-text
// stream out of PDF documents.
//
// Limitations:
//   - PDFs with embedded images only (non-searchable scanned PDFs) yield
//     no text. We don't attempt OCR.
//   - Heavily encoded / DRM-encrypted PDFs error out; the pipeline
//     swallows those errors so a single bad PDF doesn't poison a whole
//     torrent's extraction.
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
// decodes the PDF, and returns the plain text chunked via chunkText.
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

	// Bound the decoded text page-by-page. reader.GetPlainText() accumulates the
	// WHOLE document's text into an in-memory buffer BEFORE returning, so a
	// LimitReader on its result runs after the allocation already happened — a
	// flate-amplified content stream (up to ~1032:1) decompresses to gigabytes of
	// text-show operators and OOM-crashes the daemon (which recover() cannot
	// catch). Instead we replicate the per-page loop with a running byte budget
	// AND skip any page whose DECOMPRESSED content stream exceeds the budget (a
	// bomb), so peak memory is bounded to ~maxBytes.
	text := extractBoundedText(reader, maxBytes)

	if len(strings.TrimSpace(text)) == 0 {
		// Common case for scanned (image-only) PDFs: the text layer is
		// empty. Return nil rather than indexing an empty document.
		return nil, nil
	}

	return chunkText(text, DefaultChunkTargetBytes), nil
}

// extractBoundedText replicates Reader.GetPlainText but bounds memory: it walks
// pages only while the accumulated text is under maxBytes, SKIPS any page whose
// decompressed content stream(s) exceed maxBytes (a flate bomb — extracting it
// would OOM), and truncates the final page to the remaining budget. Per-page
// panics from the library are contained so one hostile page cannot abort the doc.
func extractBoundedText(reader *pdf.Reader, maxBytes int64) string {
	var out strings.Builder
	fonts := make(map[string]*pdf.Font)
	pages := reader.NumPage()
	for i := 1; i <= pages && int64(out.Len()) < maxBytes; i++ {
		p := reader.Page(i)
		if pageContentExceeds(p, maxBytes) {
			continue // flate-bomb page: skip rather than materialize gigabytes
		}
		txt := pageText(p, fonts)
		if txt == "" {
			continue
		}
		if remaining := maxBytes - int64(out.Len()); int64(len(txt)) > remaining {
			txt = txt[:remaining]
		}
		out.WriteString(txt)
	}
	return out.String()
}

// pageText extracts one page's plain text, containing any library panic so a
// single malformed page yields "" instead of aborting the whole document.
func pageText(p pdf.Page, fonts map[string]*pdf.Font) (txt string) {
	defer func() {
		if recover() != nil {
			txt = ""
		}
	}()
	for _, name := range p.Fonts() { // cache fonts (mirrors Reader.GetPlainText)
		if _, ok := fonts[name]; !ok {
			f := p.Font(name)
			fonts[name] = &f
		}
	}
	s, err := p.GetPlainText(fonts)
	if err != nil {
		return ""
	}
	return s
}

// pageContentExceeds reports whether a page's DECOMPRESSED content stream(s)
// exceed cap bytes, reading through an io.LimitReader (to io.Discard) so a flate
// bomb is DETECTED without ever materializing it. An unreadable/hostile content
// stream (the library panics on unsupported filters) counts as exceeding, so it
// is skipped conservatively.
func pageContentExceeds(p pdf.Page, cap int64) (exceeds bool) {
	defer func() {
		if recover() != nil {
			exceeds = true
		}
	}()
	if p.V.IsNull() {
		return false
	}
	contents := p.V.Key("Contents")
	var streams []pdf.Value
	switch contents.Kind() {
	case pdf.Stream:
		streams = []pdf.Value{contents}
	case pdf.Array:
		for i := 0; i < contents.Len(); i++ {
			streams = append(streams, contents.Index(i))
		}
	default:
		return false
	}
	var total int64
	for _, s := range streams {
		rc := s.Reader()
		// Read at most one byte past the remaining budget: if the stream yields
		// that many, the decompressed content is over cap → bomb.
		n, _ := io.Copy(io.Discard, io.LimitReader(rc, cap-total+1))
		rc.Close()
		if total += n; total > cap {
			return true
		}
	}
	return false
}

// claimsPDF claims application/pdf by MIME type; application/x-pdf (the
// legacy Microsoft alias) is included for tolerance. No size gate.
func claimsPDF(mime string, c Candidate) bool {
	switch mime {
	case "application/pdf", "application/x-pdf":
		return true
	}
	return false
}
