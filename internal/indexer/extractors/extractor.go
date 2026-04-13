package extractors

import (
	"io"
	"mime"
	"path/filepath"
	"strings"
	"sync"
)

// Chunk is a single text block produced by an Extractor. Most extractors
// will emit exactly one chunk per file for M2.2a; later milestones split
// large files into ~10 KB chunks so the Bleve index can highlight
// paragraph-level matches without storing entire books in a single doc.
type Chunk struct {
	// Text is the extracted text content of this chunk.
	Text string
	// Offset is the byte offset into the source file where this chunk
	// begins, for future "jump to match" UI features. Zero for
	// whole-file chunks.
	Offset int64
}

// Extractor is the interface every text-extractor backend implements.
//
// Extract reads from r (expected to return io.EOF at end of file) and
// returns the extracted text as a slice of Chunks. Implementations SHOULD:
//
//   - Stop early and return a partial result if the file is obviously not
//     text (binary signature, NUL bytes in the first 1 KiB, etc.).
//   - Respect a reasonable byte cap to avoid pulling entire terabytes of
//     text into RAM.
//   - Return an empty slice (not an error) for genuinely empty files.
//
// Name() is used as the `extractor` field on the resulting ContentDoc so
// we can track which backend produced what in the index.
type Extractor interface {
	Name() string
	Extract(r io.Reader, maxBytes int64) ([]Chunk, error)
}

// Candidate describes a file the dispatcher is considering.
type Candidate struct {
	// Path is the user-visible file path (for extension sniffing).
	Path string
	// MIME is the best-known MIME type; may be empty.
	MIME string
	// Size is the file size in bytes. Extractors may refuse files above
	// their own size caps.
	Size int64
}

// Dispatch picks an Extractor for the given candidate, or returns nil if
// no registered extractor claims the file. The second return value is the
// detected or pass-through MIME type, so the caller can persist it on the
// ContentDoc regardless of which extractor (if any) handled the file.
func Dispatch(c Candidate) (Extractor, string) {
	mime := c.MIME
	if mime == "" {
		mime = mimeFromPath(c.Path)
	}

	reg := registry()
	reg.mu.RLock()
	defer reg.mu.RUnlock()

	for _, e := range reg.extractors {
		if e.claims(mime, c) {
			return e.impl, mime
		}
	}
	return nil, mime
}

// Register adds an extractor to the dispatch table. Called by each
// extractor implementation's init() so the table is built at program
// start.
//
// claims is called in registration order; the first extractor that
// returns true handles the file. Put more specific extractors first.
func Register(impl Extractor, claims func(mime string, c Candidate) bool) {
	r := registry()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.extractors = append(r.extractors, registeredExtractor{
		impl:   impl,
		claims: claims,
	})
}

type registeredExtractor struct {
	impl   Extractor
	claims func(mime string, c Candidate) bool
}

type extractorRegistry struct {
	mu         sync.RWMutex
	extractors []registeredExtractor
}

var (
	registryOnce sync.Once
	regInstance  *extractorRegistry
)

func registry() *extractorRegistry {
	registryOnce.Do(func() {
		regInstance = &extractorRegistry{}
	})
	return regInstance
}

// mimeFromPath guesses the MIME type from the file extension. It consults
// the stdlib mime.TypeByExtension table first (which covers html/json/xml/
// etc.), then falls back to the SwartzNet-specific extTypes map for types
// the stdlib table does not know about (.srt, .vtt, source code).
// Returns an empty string for unknown extensions; callers should treat
// that as "unknown, let specific extractors decide based on other
// signals".
func mimeFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}
	// Check our override map first — the stdlib table gets `.ts` wrong
	// (it thinks it's MPEG-TS video, not TypeScript) and doesn't know
	// about subtitle formats at all.
	if m, ok := extTypes[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension(ext); m != "" {
		// Strip any charset suffix; consumers only care about the type/subtype.
		if i := strings.Index(m, ";"); i >= 0 {
			m = strings.TrimSpace(m[:i])
		}
		return m
	}
	return ""
}

// extTypes is SwartzNet's override map for file extensions the stdlib mime
// package gets wrong (or doesn't know). Entries here take precedence over
// mime.TypeByExtension.
var extTypes = map[string]string{
	".srt":      "application/x-subrip",
	".vtt":      "text/vtt",
	".ass":      "text/x-ssa",
	".ssa":      "text/x-ssa",
	".log":      "text/plain",
	".go":       "text/x-go",
	".py":       "text/x-python",
	".js":       "text/javascript",
	".ts":       "text/x-typescript", // stdlib thinks .ts is MPEG-TS video
	".rs":       "text/x-rust",
	".c":        "text/x-c",
	".cc":       "text/x-c++",
	".cpp":      "text/x-c++",
	".h":        "text/x-c",
	".hpp":      "text/x-c++",
	".java":     "text/x-java",
	".rb":       "text/x-ruby",
	".sh":       "text/x-shellscript",
	".yaml":     "text/x-yaml",
	".yml":      "text/x-yaml",
	".toml":     "text/x-toml",
	".markdown": "text/markdown",
	".epub":     "application/epub+zip",
	".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".odt":      "application/vnd.oasis.opendocument.text",
	".rtf":      "application/rtf",
	".tar":      "application/x-tar",
	".tgz":      "application/gzip",
	".fb2":      "application/x-fictionbook+xml",
	".pptx":     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odp":      "application/vnd.oasis.opendocument.presentation",
}
