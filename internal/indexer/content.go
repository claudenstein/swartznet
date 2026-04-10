package indexer

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContentDoc is the in-memory representation of a content-level index
// document: the extracted text from one file (or one chunk of one file)
// inside a torrent.
//
// Content docs are linked back to their torrent via InfoHash. The file is
// identified by its index into the torrent's upverted file list plus its
// human-readable path (kept as a separate field so we don't have to join
// back through the torrent doc at query time for result snippets).
//
// M2.2a stores one document per file; later milestones will chunk very
// large files into multiple docs at ~10 KB each, so downstream code should
// not assume one file == one doc.
type ContentDoc struct {
	InfoHash  string    // 40-char lowercase hex infohash
	FileIndex int       // index in the torrent's file list
	FilePath  string    // user-visible path, e.g. "Some Book/chapter3.txt"
	FileSize  int64     // bytes on disk
	Mime      string    // best-guess MIME type, e.g. "text/plain"
	Text      string    // extracted text body
	Extractor string    // name of the extractor that produced this doc
	IndexedAt time.Time // when this extraction was written to the index
	// ChunkIndex is 0 for the only-chunk / entire-file case, incrementing
	// for large-file chunks. Part of the doc ID so chunks are independent.
	ChunkIndex int
}

// docID returns the Bleve document ID for a content document. The ID
// includes the infohash, file index, and chunk index so that
// re-indexing the same (infohash, file) overwrites rather than duplicates.
func (d ContentDoc) docID() string {
	return fmt.Sprintf("c:%s:%d:%d", strings.ToLower(d.InfoHash), d.FileIndex, d.ChunkIndex)
}

// toBleve converts the public ContentDoc into the map form Bleve expects.
func (d ContentDoc) toBleve() map[string]any {
	if d.IndexedAt.IsZero() {
		d.IndexedAt = time.Now().UTC()
	}
	return map[string]any{
		fieldType:      typeContent,
		fieldInfoHash:  strings.ToLower(d.InfoHash),
		fieldFileIndex: d.FileIndex,
		fieldFilePath:  d.FilePath,
		fieldFileSize:  d.FileSize,
		fieldMime:      d.Mime,
		fieldText:      d.Text,
		fieldExtractor: d.Extractor,
		fieldIndexedAt: d.IndexedAt,
	}
}

// IndexContent adds or updates a content-level document. Safe to call on
// the same (InfoHash, FileIndex, ChunkIndex) multiple times; later calls
// overwrite earlier ones.
func (i *Index) IndexContent(doc ContentDoc) error {
	if doc.InfoHash == "" {
		return errors.New("indexer: ContentDoc.InfoHash must not be empty")
	}
	if doc.Text == "" {
		return errors.New("indexer: ContentDoc.Text must not be empty")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return errors.New("indexer: closed")
	}
	return i.bleve.Index(doc.docID(), doc.toBleve())
}

// DeleteContentForTorrent removes all content-level documents belonging to
// a given infohash. Used when a torrent is removed from the engine so its
// extracted-text footprint is not left behind in the index.
//
// Implementation note: Bleve has no native "delete by query" primitive in
// the public API, so we query for all matching docs first and then delete
// them by ID. For small indexes this is fine; for large indexes we will
// switch to an internal reader-based deletion in a later milestone.
func (i *Index) DeleteContentForTorrent(infoHash string) (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return 0, errors.New("indexer: closed")
	}

	// Build a query that selects only content docs for this infohash.
	// Using the QueryString form keeps this simple and mirrors how the
	// real Search path issues queries.
	q := fmt.Sprintf("+%s:%s +%s:%s",
		fieldType, typeContent,
		fieldInfoHash, strings.ToLower(infoHash),
	)
	return i.deleteByQueryLocked(q)
}
