package extractors

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
)

// EPUBExtractor extracts text from EPUB ebooks. EPUB is a ZIP
// archive containing XHTML files plus a small amount of metadata
// (OPF spine + NCX/nav table of contents). For full-text search we
// only need the XHTML body content of every chapter.
//
// The implementation deliberately ignores the OPF spine and just
// iterates every .xhtml/.html/.htm file in the archive in lexical
// order. Reading order is wrong for human consumption (chapters
// might be out of sequence) but completely irrelevant to a
// keyword index — and avoiding the OPF parser keeps the extractor
// to ~80 lines of pure stdlib + golang.org/x/net/html.
//
// Encrypted DRM-protected EPUBs (Adobe DE, B&N, Kindle/MOBI) are
// not supported and will produce empty or garbage text. The
// extractor handles them by returning nil chunks rather than
// erroring.
type EPUBExtractor struct{}

// NewEPUBExtractor returns a ready-to-use EPUBExtractor.
func NewEPUBExtractor() *EPUBExtractor { return &EPUBExtractor{} }

// Name implements Extractor.
func (*EPUBExtractor) Name() string { return "epub" }

// epubMaxInputBytes is the safety cap on how much of the source
// file we buffer into memory before parsing. 256 MiB is enough for
// even pathologically-large multi-volume omnibus editions and
// keeps us well below typical container RAM limits.
const epubMaxInputBytes = 256 * 1024 * 1024

// Extract implements Extractor. It buffers the input (zip needs
// random access via io.ReaderAt), iterates the archive, extracts
// text from each XHTML chapter, concatenates with paragraph
// boundaries between chapters, and runs the result through the
// shared chunker.
func (e *EPUBExtractor) Extract(r io.Reader, maxBytes int64) (chunks []Chunk, err error) {
	if maxBytes <= 0 || maxBytes > epubMaxInputBytes {
		maxBytes = epubMaxInputBytes
	}

	// Recover from any panic in golang.org/x/net/html on weird
	// input. One bad EPUB must never crash the pipeline worker.
	defer func() {
		if rec := recover(); rec != nil {
			chunks = nil
			err = fmt.Errorf("epub: panic during extraction: %v", rec)
		}
	}()

	buf, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("epub: read input: %w", err)
	}
	if len(buf) == 0 {
		return nil, nil
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("epub: open zip: %w", err)
	}

	// Collect XHTML chapter file names; iterate in lexical order
	// for deterministic output.
	var chapters []*zip.File
	for _, f := range zr.File {
		if isXHTMLChapter(f.Name) {
			chapters = append(chapters, f)
		}
	}
	if len(chapters) == 0 {
		return nil, nil
	}
	sort.Slice(chapters, func(i, j int) bool {
		return chapters[i].Name < chapters[j].Name
	})

	var combined strings.Builder
	for _, f := range chapters {
		text, err := extractChapter(f)
		if err != nil {
			// One bad chapter does not poison the rest of the book.
			continue
		}
		if combined.Len() > 0 {
			combined.WriteString("\n\n")
		}
		combined.WriteString(text)
	}

	if strings.TrimSpace(combined.String()) == "" {
		return nil, nil
	}

	return chunkText(combined.String(), DefaultChunkTargetBytes), nil
}

// extractChapter opens one zip entry, runs it through the shared
// HTML text extractor, and returns the visible text.
func extractChapter(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()
	return extractHTMLText(rc)
}

// isXHTMLChapter reports whether a zip entry is a candidate
// chapter file. We accept .xhtml, .html, .htm in any subdirectory.
// META-INF and the .opf / .ncx / .nav files are excluded since
// they only contain navigation metadata, not body text.
func isXHTMLChapter(name string) bool {
	if name == "" || strings.HasSuffix(name, "/") {
		return false
	}
	lower := strings.ToLower(name)
	if strings.HasPrefix(lower, "meta-inf/") {
		return false
	}
	ext := strings.ToLower(path.Ext(lower))
	switch ext {
	case ".xhtml", ".html", ".htm":
		return true
	}
	return false
}

// init registers the EPUB extractor in the dispatch table for the
// canonical EPUB MIME type. Two .epub MIME aliases are accepted in
// the wild; we cover both.
func init() {
	Register(NewEPUBExtractor(), func(mime string, c Candidate) bool {
		switch mime {
		case "application/epub+zip", "application/epub":
			return true
		}
		return false
	})
}
