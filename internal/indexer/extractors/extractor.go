package extractors

import (
	"io"
	"mime"
	"path/filepath"
	"strings"
	"sync"
)

// Chunk is a single text block produced by an Extractor.
type Chunk struct {
	// Text is the extracted text content of this chunk.
	Text string
	// Offset is the byte offset into the source file where this chunk
	// begins, for "jump to match" UI features. Zero for whole-file or
	// synthesized chunks.
	Offset int64
}

// Extractor is the interface every text-extractor backend implements.
//
// Extract reads from r (expected to return io.EOF at end of file) and
// returns the extracted text as a slice of Chunks. Implementations MUST:
//
//   - Bound how many bytes they read; maxBytes <= 0 selects the
//     per-extractor default budget.
//   - Return (nil, nil) — not an error — for genuinely empty files.
//   - Refuse input that is obviously not what they parse (binary
//     signature, bad magic) with an error rather than garbage chunks.
//
// Name() lands verbatim on the resulting ContentDoc's Extractor field so
// downstream analytics can tell which backend produced a document.
type Extractor interface {
	Name() string
	Extract(r io.Reader, maxBytes int64) ([]Chunk, error)
}

// maxDocTextBytes caps the extracted-text *output* of the
// container-based document extractors (DOCX/ODT/EPUB and the shared
// HTML walker). The per-extractor input caps bound the *compressed*
// bytes we buffer, but DEFLATE amplifies up to ~1032:1 once
// decompressed — so every zip-entry reader must be wrapped in
// io.LimitReader(rc, maxDocTextBytes) before it reaches an XML/HTML
// parser. Without that, a single oversized text node buffers the
// whole decompressed body inside one Token()/Next() call, an OOM
// that recover() cannot catch. 64 MiB of plain text is far beyond
// any real document.
const maxDocTextBytes = 64 * 1024 * 1024

// maxEpubTotalDecompress bounds the TOTAL decompressed bytes an EPUB extraction
// may read across ALL chapters, charged by bytes consumed (not output text). The
// per-chapter output budget cannot bound decompression work when a chapter emits
// no visible text (e.g. a giant <script> body), so a crafted multi-chapter EPUB
// could drive ~1000x total decompression without it. 256 MiB (4x the output cap)
// leaves ample headroom for legitimate markup while bounding a bomb.
const maxEpubTotalDecompress = 4 * maxDocTextBytes

// Candidate describes a file the dispatcher is considering.
type Candidate struct {
	// Path is the user-visible file path (for extension sniffing).
	Path string
	// MIME is the best-known MIME type; may be empty. The live pipeline
	// always leaves it empty, so dispatch is extension-driven in
	// practice; the field remains for tests and future sniffing.
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

// Register adds an extractor to the dispatch table. All registrations
// happen from the single ordered list in register.go so the dispatch
// order is explicit and deterministic.
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

// mimeFromPath guesses the MIME type from the file extension. The
// project-local extTypes override map is consulted FIRST — the stdlib
// table gets `.ts` wrong (MPEG-TS video, not TypeScript), knows no
// subtitle formats, and lacks even text/plain on systems without
// /etc/mime.types. Only extensions absent from the override map fall
// back to mime.TypeByExtension, with any "; charset=..." suffix
// stripped. Returns an empty string for unknown extensions; callers
// should treat that as "unknown, let specific extractors decide based
// on other signals".
func mimeFromPath(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return ""
	}
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
	// Plain text — not in Go's builtin mime map and therefore
	// empty on systems without /etc/mime.types (Alpine, scratch
	// containers, minimal base images). Without this override,
	// every .txt file silently skips extraction, which the Layer-B
	// Docker testbed hit head-on because the containers run
	// Alpine. Keep this list as the authoritative fallback for
	// common text suffixes so the indexer behaves the same across
	// deployment targets.
	".txt":      "text/plain",
	".text":     "text/plain",
	".md":       "text/markdown",
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
	".mobi":     "application/x-mobipocket-ebook",
	".azw":      "application/vnd.amazon.ebook",
	".azw3":     "application/vnd.amazon.ebook",
	".mp3":      "audio/mpeg",
	".jpg":      "image/jpeg",
	".jpeg":     "image/jpeg",
	".flac":     "audio/flac",
	".ogg":      "audio/ogg",
	".oga":      "audio/ogg",
	".opus":     "audio/opus",
	".mkv":      "video/x-matroska",
	".mka":      "video/x-matroska",
	".webm":     "video/webm",
	".mp4":      "video/mp4",
	".m4a":      "audio/mp4",
	".m4b":      "audio/mp4",
	".m4v":      "video/mp4",
	".zim":      "application/x-zim",
}
