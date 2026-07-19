package companion

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/swartznet/swartznet/internal/indexer"
)

// BuildOptions controls what a build includes.
type BuildOptions struct {
	// IncludeContent pulls per-file extracted text chunks (default true).
	IncludeContent bool
	// IncludeTorrentNames carries the human-readable torrent name (default true).
	IncludeTorrentNames bool
	// MaxChunksPerFile caps chunks per file (0 = unlimited).
	MaxChunksPerFile int
	// MaxFilesPerTorrent caps files per torrent (0 = unlimited).
	MaxFilesPerTorrent int
}

// DefaultBuildOptions returns the production defaults: full content + names, no caps.
func DefaultBuildOptions() BuildOptions {
	return BuildOptions{IncludeContent: true, IncludeTorrentNames: true}
}

// BuildFromIndex walks the local corpus into a CompanionIndex. It holds no
// locks and makes no network calls. Version/Format are left zero here — Encode
// stamps them at serialization. One TorrentRecord per torrent doc, in the
// order the source returned them (not sorted). The source TorrentDoc.SignedBy
// is deliberately NOT propagated — the publisher's identity is carried only by
// the top-level Publisher field, and the subscriber stamps imports with the
// FOLLOWED pubkey.
func BuildFromIndex(src CorpusSource, publisherHex string, opts BuildOptions) (CompanionIndex, error) {
	if src == nil {
		return CompanionIndex{}, errors.New("companion: nil corpus source")
	}
	torrents, err := src.AllTorrentDocs()
	if err != nil {
		return CompanionIndex{}, fmt.Errorf("companion: list torrents: %w", err)
	}
	out := CompanionIndex{
		Publisher:   publisherHex,
		GeneratedAt: time.Now().Unix(),
		Torrents:    make([]TorrentRecord, 0, len(torrents)),
	}
	for _, t := range torrents {
		rec := TorrentRecord{InfoHash: strings.ToLower(t.InfoHash), Size: t.SizeBytes}
		if opts.IncludeTorrentNames {
			rec.Name = t.Name
		}
		if !t.AddedAt.IsZero() {
			rec.AddedAt = t.AddedAt.Unix()
		}
		if opts.IncludeContent {
			contentDocs, err := src.ContentDocsForInfoHash(t.InfoHash)
			if err != nil {
				return CompanionIndex{}, fmt.Errorf("companion: list content for %s: %w", t.InfoHash, err)
			}
			rec.Files = collectFileRecords(t.FilePaths, contentDocs, opts)
		}
		out.Torrents = append(out.Torrents, rec)
	}
	return out, nil
}

type fileBucket struct {
	path      string
	size      int64
	mime      string
	extractor string
	chunks    []ContentChunk
}

// collectFileRecords buckets content docs by file index, then emits one
// FileRecord per file path (so files with no extracted content are still
// listed — filename search still works). First-non-empty wins for path/mime/
// extractor; chunks accumulate in source order.
func collectFileRecords(filePaths []string, contentDocs []indexer.ContentDoc, opts BuildOptions) []FileRecord {
	byIndex := make(map[int]*fileBucket)
	for _, c := range contentDocs {
		b := byIndex[c.FileIndex]
		if b == nil {
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
		b.chunks = append(b.chunks, ContentChunk{Text: c.Text})
	}
	out := make([]FileRecord, 0, len(filePaths))
	for i, p := range filePaths {
		rec := FileRecord{Index: i, Path: p}
		if b := byIndex[i]; b != nil {
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
