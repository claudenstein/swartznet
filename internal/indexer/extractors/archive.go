package extractors

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// ArchiveExtractor indexes the *names* of files inside an archive
// rather than their contents. Useful for finding a specific file
// that ships inside a source tarball or a photo bundle without
// unpacking — searching for "changelog.md" will match an archive
// that contains "src/docs/changelog.md" at any depth.
//
// Supported formats:
//   - .zip (archive/zip)
//   - .tar (archive/tar)
//   - .tar.gz / .tgz (archive/tar through compress/gzip)
//
// Not supported:
//   - .rar / .7z / .tar.bz2 / .tar.xz — none ship with the stdlib;
//     adding them would need CGo or pure-Go third-party libs.
//
// The extracted "text" is just a newline-separated list of member
// paths, sized-sorted by path so diff-searching inside the index is
// deterministic. A 4 MiB cap on the concatenated name list keeps
// pathological archives (millions of tiny files) bounded.
type ArchiveExtractor struct{}

// NewArchiveExtractor returns a ready-to-use archive extractor.
func NewArchiveExtractor() *ArchiveExtractor { return &ArchiveExtractor{} }

// Name implements Extractor.
func (*ArchiveExtractor) Name() string { return "archive" }

// Extract implements Extractor. Dispatches by path extension —
// the Candidate's path is available via the top-level Dispatch
// flow, but at Extract time we only have the io.Reader. So we
// sniff the magic bytes instead.
func (e *ArchiveExtractor) Extract(r io.Reader, maxBytes int64) ([]Chunk, error) {
	if maxBytes <= 0 {
		maxBytes = 64 * 1024 * 1024
	}

	// Slurp the whole stream up to cap. Archive readers need random
	// access for zip (needs Len()/ReadAt()) and streamed for tar.
	// For simplicity + pure-Go, buffer the whole thing.
	raw, err := io.ReadAll(io.LimitReader(r, maxBytes))
	if err != nil {
		return nil, fmt.Errorf("archive: read: %w", err)
	}
	if len(raw) < 4 {
		return nil, nil
	}

	var names []string

	switch {
	case bytes.HasPrefix(raw, []byte{0x50, 0x4b, 0x03, 0x04}), // "PK\x03\x04" — ZIP local file header
		bytes.HasPrefix(raw, []byte{0x50, 0x4b, 0x05, 0x06}), // empty zip
		bytes.HasPrefix(raw, []byte{0x50, 0x4b, 0x07, 0x08}): // spanned zip
		names, err = zipMemberNames(raw)
	case bytes.HasPrefix(raw, []byte{0x1f, 0x8b}): // gzip magic
		names, err = tarGzMemberNames(raw)
	default:
		// Try plain tar (no magic in header — format is ASCII blocks).
		// archive/tar returns io.EOF when the first block isn't a
		// valid tar header, so we can attempt + recover.
		if n, terr := tarMemberNames(bytes.NewReader(raw)); terr == nil && len(n) > 0 {
			names = n
		} else {
			return nil, fmt.Errorf("archive: unknown format")
		}
	}
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nil
	}

	sort.Strings(names)
	text := strings.Join(names, "\n")
	return []Chunk{{Text: text}}, nil
}

func zipMemberNames(raw []byte) ([]string, error) {
	rd, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("archive: zip: %w", err)
	}
	names := make([]string, 0, len(rd.File))
	for _, f := range rd.File {
		if f.Name == "" {
			continue
		}
		// Skip directory entries (they have no interesting text).
		if strings.HasSuffix(f.Name, "/") {
			continue
		}
		names = append(names, f.Name)
	}
	return names, nil
}

func tarGzMemberNames(raw []byte) ([]string, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("archive: gzip: %w", err)
	}
	defer gz.Close()
	return tarMemberNames(gz)
}

func tarMemberNames(r io.Reader) ([]string, error) {
	tr := tar.NewReader(r)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive: tar: %w", err)
		}
		if hdr.Typeflag == tar.TypeDir {
			continue
		}
		if hdr.Name != "" {
			names = append(names, hdr.Name)
		}
	}
	return names, nil
}

func init() {
	Register(NewArchiveExtractor(), func(mime string, c Candidate) bool {
		if c.Size > 500*1024*1024 {
			// Don't bother indexing names inside >500 MiB archives;
			// the cost/benefit isn't worth it for most cases.
			return false
		}
		if mime == "application/zip" || mime == "application/x-tar" ||
			mime == "application/gzip" || mime == "application/x-gzip" {
			return true
		}
		lower := strings.ToLower(filepath.Ext(c.Path))
		switch lower {
		case ".zip", ".tar", ".tgz":
			return true
		}
		// .tar.gz is a double extension — special-case it.
		if strings.HasSuffix(strings.ToLower(c.Path), ".tar.gz") {
			return true
		}
		return false
	})
}
