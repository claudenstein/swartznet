package companion

// FormatVersion is the on-disk schema version of the
// CompanionIndex JSON document. Bumped on a backwards-
// incompatible change to the schema; subscribers MUST refuse
// any companion file whose version they do not recognise.
const FormatVersion = 1

// FormatFileName is the canonical filename inside the companion
// .torrent. Subscribers look for exactly this entry to extract
// the JSON payload. Keeping it stable across versions lets the
// subscriber reuse the same code path as new format versions
// land — only the inner JSON evolves.
const FormatFileName = "swartznet-content-index-v1.json.gz"

// FormatMagic is the leading byte sequence of an UNCOMPRESSED
// companion JSON document. Decode checks for it after gunzip so
// a corrupted or wrong-format file fails fast with a clear
// error rather than producing garbage records.
const FormatMagic = `{"version":1,"format":"swartznet-content-index"`

// CompanionIndex is the top-level JSON document. The Publisher
// field carries the publisher's ed25519 pubkey as 64-char hex,
// matching reputation.PubKeyHex; subscribers use it to
// attribute imported records and to maintain reputation
// against the publisher.
type CompanionIndex struct {
	// Version is FormatVersion at the time the index was written.
	// Subscribers refuse versions they do not recognise.
	Version int `json:"version"`
	// Format is a stable string identifying the schema. Always
	// "swartznet-content-index" for this package.
	Format string `json:"format"`
	// Publisher is the 64-char hex form of the publisher's
	// ed25519 public key. Empty for an anonymous companion
	// (uncommon — subscribers should be skeptical).
	Publisher string `json:"publisher,omitempty"`
	// GeneratedAt is the unix timestamp at which the index was
	// serialised. Used by subscribers to skip duplicates of an
	// older snapshot they have already imported.
	GeneratedAt int64 `json:"generated_at"`
	// Torrents is the list of torrents the publisher is
	// describing in this snapshot.
	Torrents []TorrentRecord `json:"torrents"`
}

// TorrentRecord is one entry in CompanionIndex.Torrents. It
// describes a torrent the publisher has indexed locally,
// optionally with the extracted file content for search.
type TorrentRecord struct {
	// InfoHash is the 40-char lowercase SHA-1 hex form.
	InfoHash string `json:"infohash"`
	// Name is the human-readable torrent name.
	Name string `json:"name"`
	// Size is the total bytes in the torrent (for size filters).
	Size int64 `json:"size,omitempty"`
	// AddedAt is the unix timestamp when the publisher added
	// this torrent to their local index. Useful for freshness
	// ranking on the subscriber side.
	AddedAt int64 `json:"added_at,omitempty"`
	// Files is the optional per-file detail. Empty when the
	// publisher chose to share only torrent-level metadata.
	Files []FileRecord `json:"files,omitempty"`
}

// FileRecord is one entry in TorrentRecord.Files. It describes
// a file the publisher has extracted text from, with optional
// content chunks for full-text search.
type FileRecord struct {
	// Index is the file's position in the torrent's
	// upverted file list, matching the M2.1 file tracker.
	Index int `json:"index"`
	// Path is the user-visible file path.
	Path string `json:"path"`
	// Size is the file's byte length.
	Size int64 `json:"size,omitempty"`
	// Mime is the best-known MIME type, e.g. "text/plain" or
	// "application/pdf".
	Mime string `json:"mime,omitempty"`
	// Extractor is the name of the M2.2/M2.3/M6 extractor that
	// produced the chunks below — useful for telemetry and for
	// the subscriber to decide whether to trust the content.
	Extractor string `json:"extractor,omitempty"`
	// Chunks is the list of extracted text chunks. May be
	// empty when the publisher only shares per-file metadata
	// (filename + size + mime) without the actual text.
	Chunks []ContentChunk `json:"chunks,omitempty"`
}

// ContentChunk is one paragraph-level extracted text fragment.
// Mirrors extractors.Chunk in the producer side and
// indexer.ContentDoc.Text on the consumer side.
type ContentChunk struct {
	// Text is the extracted text content of this chunk.
	Text string `json:"text"`
	// Offset is the byte offset of the chunk inside the
	// source file. Zero for whole-file extractions.
	Offset int64 `json:"offset,omitempty"`
}
