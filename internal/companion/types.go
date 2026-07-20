package companion

// FormatVersion is the on-disk schema version of the CompanionIndex JSON
// document. Bumped on a backwards-incompatible schema change; subscribers MUST
// refuse any companion file whose version they do not recognise.
const FormatVersion = 1

// FormatName is the stable schema identifier carried in every document.
const FormatName = "swartznet-content-index"

// FormatFileName is the generic filename used inside a companion .torrent when
// the publisher is anonymous (no pubkey). Real publishers tag the file with
// their pubkey prefix via CompanionFileName so each node's companion torrent
// has a distinguishable name in downloads lists.
const FormatFileName = "swartznet-content-index-v1.json.gz"

// CompanionFileName returns the on-wire filename for a CompanionIndex written
// by the publisher whose pubkey hex is pubkeyHex. Empty pubkeyHex → the generic
// FormatFileName; otherwise "swartznet-content-index-<first-12-hex>-v1.json.gz"
// (the prefix is the raw first 12 hex chars — short enough to stay readable,
// long enough to make collisions vanishingly unlikely).
func CompanionFileName(pubkeyHex string) string {
	if pubkeyHex == "" {
		return FormatFileName
	}
	prefix := pubkeyHex
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return "swartznet-content-index-" + prefix + "-v1.json.gz"
}

// CompanionIndex is the top-level JSON document. Publisher carries the ed25519
// pubkey as 64-char hex; subscribers use it to VERIFY the snapshot was authored
// by the publisher they follow and to stamp imported records with SignedBy.
type CompanionIndex struct {
	// Version is FormatVersion at write time. Subscribers refuse unknown versions.
	Version int `json:"version"`
	// Format is the stable schema id, always FormatName.
	Format string `json:"format"`
	// Publisher is the 64-char hex ed25519 public key. Empty for an anonymous
	// companion (uncommon; subscribers following a specific key reject a
	// mismatch).
	Publisher string `json:"publisher,omitempty"`
	// GeneratedAt is the unix timestamp at serialization. Subscribers dedup on
	// it — an unchanged snapshot is not re-imported.
	GeneratedAt int64 `json:"generated_at"`
	// Torrents is the list of torrents the publisher describes.
	Torrents []TorrentRecord `json:"torrents"`
}

// TorrentRecord is one entry in CompanionIndex.Torrents.
type TorrentRecord struct {
	InfoHash string       `json:"infohash"` // 40-char lowercase SHA-1 hex
	Name     string       `json:"name"`
	Size     int64        `json:"size,omitempty"`
	AddedAt  int64        `json:"added_at,omitempty"` // unix seconds
	Files    []FileRecord `json:"files,omitempty"`
}

// FileRecord is one file's extracted detail.
type FileRecord struct {
	Index     int            `json:"index"` // position in the torrent's file list
	Path      string         `json:"path"`
	Size      int64          `json:"size,omitempty"`
	Mime      string         `json:"mime,omitempty"`
	Extractor string         `json:"extractor,omitempty"`
	Chunks    []ContentChunk `json:"chunks,omitempty"`
}

// ContentChunk is one paragraph-level extracted text fragment.
type ContentChunk struct {
	Text   string `json:"text"`
	Offset int64  `json:"offset,omitempty"` // byte offset in the source file; 0 whole-file
}
