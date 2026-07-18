package extractors

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// PlaintextExtractor reads UTF-8 (or ASCII-safe) text and returns it
// chunked at paragraph boundaries. It refuses files that look binary by
// checking for NUL bytes in the first few KiB — a heuristic that works
// reliably for source code, logs, and common text formats.
//
// Raw .html/.htm files are handled here WITH TAGS LEFT IN (text/html is
// in the accept list); the tag-stripping HTML walker runs only inside
// the EPUB and ZIM extractors. There is no standalone HTML extractor.
type PlaintextExtractor struct {
	// sniffBytes is the prefix length we look at to decide whether a file
	// is actually text. 4 KiB is plenty in practice and catches almost
	// all binary files by their first NUL.
	sniffBytes int
}

// NewPlaintextExtractor returns a ready-to-use PlaintextExtractor.
func NewPlaintextExtractor() *PlaintextExtractor {
	return &PlaintextExtractor{sniffBytes: 4096}
}

// Name implements Extractor.
func (*PlaintextExtractor) Name() string { return "plaintext" }

// Extract implements Extractor.
//
// It reads up to maxBytes of r, refuses files that contain NUL bytes in
// the sniff prefix, and returns the content chunked via chunkText.
// UTF-8 validation is performed; files that fail UTF-8 validation are
// returned anyway with their invalid bytes replaced by the Unicode
// replacement character, on the assumption that being able to search
// the readable parts is better than failing entirely.
func (e *PlaintextExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024 // safety ceiling: 32 MiB
	}

	br := bufio.NewReaderSize(r, 32*1024)

	// Sniff: peek at the first e.sniffBytes bytes (or however many exist)
	// and bail out if any NUL shows up. A leading UTF-8 BOM is also
	// consumed here if present.
	sniff, _ := br.Peek(e.sniffBytes)
	if bytes.IndexByte(sniff, 0) != -1 {
		return nil, fmt.Errorf("plaintext: binary signature (NUL byte) detected in first %d bytes", len(sniff))
	}
	if bytes.HasPrefix(sniff, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = br.Discard(3)
	}

	limited := io.LimitReader(br, maxBytes)
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("plaintext: read: %w", err)
	}

	// Ensure UTF-8 validity; invalid sequences become U+FFFD.
	var out string
	if utf8.Valid(raw) {
		out = string(raw)
	} else {
		out = sanitizeUTF8(raw)
	}

	// An empty file is not an error. Return an empty chunk slice so the
	// caller can skip indexing.
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}
	return chunkText(out, DefaultChunkTargetBytes), nil
}

// sanitizeUTF8 walks a byte slice and replaces every invalid UTF-8
// sequence with the Unicode replacement character. It is used for files
// that claim to be text but contain a stray non-UTF-8 byte here and there
// (common in mixed-encoding archives).
func sanitizeUTF8(b []byte) string {
	var sb strings.Builder
	sb.Grow(len(b))
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		if r == utf8.RuneError && size == 1 {
			sb.WriteRune('\uFFFD')
			b = b[1:]
			continue
		}
		sb.WriteRune(r)
		b = b[size:]
	}
	return sb.String()
}

// claimsPlaintext claims anything that looks like text and is not
// obviously too large. Subtitle formats (.srt/.vtt/.ass) are deliberately
// declined so the more specific SubtitleExtractor claims them — that one
// strips timestamps and only indexes the dialog text.
func claimsPlaintext(mime string, c Candidate) bool {
	if c.Size > 100*1024*1024 {
		return false
	}
	switch mime {
	case "application/x-subrip", "text/vtt", "text/x-ssa":
		// Subtitle formats are handled by SubtitleExtractor.
		return false
	case "text/plain",
		"text/markdown",
		"text/html",
		"text/xml",
		"text/csv",
		"text/javascript",
		"application/json",
		"application/xml":
		return true
	}
	return strings.HasPrefix(mime, "text/")
}
