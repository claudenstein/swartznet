package indexer

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ContentDoc is the in-memory representation of a content-level index
// document: the extracted text of one chunk of one file inside a torrent.
// Linked back to its torrent via InfoHash; the file is identified by its
// index into the torrent's file list plus its human-readable path.
// Downstream code must not assume one file == one doc — large files chunk
// into multiple docs.
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
	// for large-file chunks. Encoded ONLY in the doc ID — schema v3 has
	// no stored chunk_index field, so reconstruction always yields 0.
	ChunkIndex int
	// PreserveExisting, when set, makes IndexContent skip the write if a doc for
	// this (infohash, file, chunk) already exists. The companion subscriber sets
	// it: content docs carry no provenance, so a followed publisher's snapshot
	// must never overwrite the node's OWN locally-extracted content for an
	// infohash it merely listed. Transient — never serialized.
	PreserveExisting bool
}

// docID keys a content doc by (infohash, file index, chunk index) so
// re-indexing the same triple overwrites rather than duplicates.
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

// IndexContent adds or updates a content-level document (put-or-replace
// on the doc ID). Empty Text is rejected: extractors signal "no text"
// with nil chunks and nil error, and the pipeline drops zero-chunk
// results as skipped before any index write.
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
	if doc.PreserveExisting {
		if d, err := i.bleve.Document(doc.docID()); err == nil && d != nil {
			// Already have content for this (infohash, file, chunk) — never let a
			// companion import clobber the node's own extraction.
			return nil
		}
	}
	return i.bleve.Index(doc.docID(), doc.toBleve())
}
