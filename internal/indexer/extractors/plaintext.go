package extractors

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// PlaintextExtractor reads UTF-8 (or ASCII-safe) text and returns it as a
// single whole-file Chunk. It refuses files that look binary by checking
// for NUL bytes in the first few KiB — a heuristic that works reliably for
// source code, subtitles, logs, and common text formats.
//
// Later milestones will extend this with:
//   - Per-paragraph chunking for large documents.
//   - Charset detection (chardet) for non-UTF-8 files.
//   - Newline normalisation and whitespace collapse.
//
// Name returns "plaintext" — this is the string that will land on the
// ContentDoc's Extractor field so downstream analytics can tell how a
// given document was produced.
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
// the sniff prefix, and returns the whole content as a single Chunk at
// offset 0. UTF-8 validation is performed; files that fail UTF-8
// validation are returned anyway with their invalid bytes replaced by
// the Unicode replacement character, on the assumption that being able
// to search the readable parts is better than failing entirely.
func (e *PlaintextExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	if maxBytes <= 0 {
		maxBytes = 32 * 1024 * 1024 // safety ceiling: 32 MiB
	}

	br := bufio.NewReaderSize(r, 32*1024)

	// Sniff: peek at the first e.sniffBytes bytes (or however many exist)
	// and bail out if any NUL shows up. A trailing UTF-8 BOM is also
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
	return []Chunk{{Text: out, Offset: 0}}, nil
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

// Register the plaintext extractor in the dispatch table. This init
// fires at package load time, so callers of extractors.Dispatch() just
// work without any explicit setup.
func init() {
	Register(NewPlaintextExtractor(), func(mime string, c Candidate) bool {
		// Claim anything that looks like text and is not obviously too
		// large. The 32 MiB upper bound matches the Extract default; the
		// indexer pipeline may impose a tighter cap for M2.2a.
		if c.Size > 100*1024*1024 {
			return false
		}
		switch mime {
		case "text/plain",
			"text/markdown",
			"text/html",
			"text/xml",
			"text/csv",
			"text/vtt",
			"text/javascript",
			"application/json",
			"application/x-subrip",
			"application/xml":
			return true
		}
		return strings.HasPrefix(mime, "text/")
	})
}
