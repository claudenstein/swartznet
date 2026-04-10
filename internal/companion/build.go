package companion

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// BuildOptions tunes how BuildFromIndex assembles its
// CompanionIndex. The defaults produce a "share everything"
// snapshot suitable for the publisher's own corpus.
type BuildOptions struct {
	// IncludeContent, when false, omits the per-file content
	// chunks from each TorrentRecord. The publisher might set
	// this to false for an opt-out content-sharing setup
	// (still publishes the file list and torrent metadata).
	// Default: true.
	IncludeContent bool

	// MaxChunksPerFile bounds how many extracted text chunks
	// the publisher will share for a single file. Set to 0 for
	// no limit. Default: 0 (no limit).
	MaxChunksPerFile int

	// MaxFilesPerTorrent bounds how many file records the
	// publisher will share for a single torrent. Set to 0 for
	// no limit. Default: 0 (no limit).
	MaxFilesPerTorrent int

	// IncludeTorrentNames, when false, omits the torrent name
	// from the published record. Currently always true; reserved
	// for a future "anonymous-torrent" mode.
	IncludeTorrentNames bool
}

// DefaultBuildOptions returns the safe default — share
// everything. Override fields on the returned value as needed.
func DefaultBuildOptions() BuildOptions {
	return BuildOptions{
		IncludeContent:      true,
		MaxChunksPerFile:    0,
		MaxFilesPerTorrent:  0,
		IncludeTorrentNames: true,
	}
}

// BuildFromIndex walks the publisher's local Bleve index and
// returns a CompanionIndex ready to be Encoded. publisherHex is
// the publisher's 64-char ed25519 pubkey hex (or empty for an
// anonymous index, which subscribers should be skeptical of).
//
// The function holds no locks and makes no network calls — it
// is safe to call from a periodic refresh worker without
// affecting query latency.
func BuildFromIndex(idx *indexer.Index, publisherHex string, opts BuildOptions) (CompanionIndex, error) {
	if idx == nil {
		return CompanionIndex{}, errors.New("companion: nil index")
	}
	torrents, err := idx.AllTorrentDocs()
	if err != nil {
		return CompanionIndex{}, fmt.Errorf("companion: list torrents: %w", err)
	}

	out := CompanionIndex{
		Publisher:   publisherHex,
		GeneratedAt: time.Now().Unix(),
		Torrents:    make([]TorrentRecord, 0, len(torrents)),
	}

	for _, t := range torrents {
		rec := TorrentRecord{
			InfoHash: strings.ToLower(t.InfoHash),
			Size:     t.SizeBytes,
		}
		if opts.IncludeTorrentNames {
			rec.Name = t.Name
		}
		if !t.AddedAt.IsZero() {
			rec.AddedAt = t.AddedAt.Unix()
		}

		if opts.IncludeContent {
			contentDocs, err := idx.ContentDocsForInfoHash(t.InfoHash)
			if err != nil {
				return CompanionIndex{}, fmt.Errorf("companion: list content for %s: %w", t.InfoHash, err)
			}
			rec.Files = collectFileRecords(t.FilePaths, contentDocs, opts)
		}

		out.Torrents = append(out.Torrents, rec)
	}

	return out, nil
}

// collectFileRecords groups content documents by file index and
// emits one FileRecord per file with its chunks attached. Files
// with no extracted content are still represented (so the
// subscriber can search by filename even when the publisher
// did not have a working extractor for that format).
func collectFileRecords(filePaths []string, contentDocs []indexer.ContentDoc, opts BuildOptions) []FileRecord {
	// Group content docs by file index. The pipeline writes
	// one ContentDoc per chunk, with monotonic ChunkIndex.
	type fileBucket struct {
		path      string
		size      int64
		mime      string
		extractor string
		chunks    []ContentChunk
	}
	byIndex := make(map[int]*fileBucket)
	for _, c := range contentDocs {
		b, ok := byIndex[c.FileIndex]
		if !ok {
			b = &fileBucket{path: c.FilePath, size: c.FileSize}
			byIndex[c.FileIndex] = b
		}
		if b.path == "" && c.FilePath != "" {
			b.path = c.FilePath
		}
		if b.mime == "" {
			b.mime = c.Mime
		}
		if b.extractor == "" {
			b.extractor = c.Extractor
		}
		b.chunks = append(b.chunks, ContentChunk{
			Text: c.Text,
		})
	}

	// Emit a FileRecord for each file in the torrent. Even
	// without content docs, the file path itself is useful
	// for filename search.
	out := make([]FileRecord, 0, len(filePaths))
	for i, p := range filePaths {
		rec := FileRecord{Index: i, Path: p}
		if b, ok := byIndex[i]; ok {
			rec.Size = b.size
			rec.Mime = b.mime
			rec.Extractor = b.extractor
			rec.Chunks = b.chunks
			if opts.MaxChunksPerFile > 0 && len(rec.Chunks) > opts.MaxChunksPerFile {
				rec.Chunks = rec.Chunks[:opts.MaxChunksPerFile]
			}
		}
		out = append(out, rec)
		if opts.MaxFilesPerTorrent > 0 && len(out) >= opts.MaxFilesPerTorrent {
			break
		}
	}
	return out
}
