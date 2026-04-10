package indexer

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// Index is SwartzNet's local full-text search index. It wraps a Bleve index
// on disk and exposes a narrow, intention-revealing API to callers.
//
// Concurrency: all methods are safe for concurrent use.
type Index struct {
	path string

	mu    sync.Mutex
	bleve bleve.Index
}

// Open opens (or creates) a Bleve index at path. If path already contains a
// Bleve index, it is opened read-write; otherwise a new index is created
// with the SwartzNet schema.
func Open(path string) (*Index, error) {
	if path == "" {
		return nil, errors.New("indexer: path must not be empty")
	}

	// Bleve stores the index as a directory of files. Detect existence by
	// looking for the "index_meta.json" file Bleve writes at init time.
	var (
		bi  bleve.Index
		err error
	)
	if _, statErr := os.Stat(path + "/index_meta.json"); statErr == nil {
		bi, err = bleve.Open(path)
	} else {
		bi, err = bleve.New(path, buildMapping())
	}
	if err != nil {
		return nil, fmt.Errorf("indexer: open %q: %w", path, err)
	}

	return &Index{path: path, bleve: bi}, nil
}

// Close flushes and closes the underlying Bleve index. Idempotent.
func (i *Index) Close() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil
	}
	err := i.bleve.Close()
	i.bleve = nil
	return err
}

// TorrentDoc is the in-memory representation of a torrent-level index
// document. It is what callers hand to IndexTorrent and what Search returns
// in hit previews.
type TorrentDoc struct {
	InfoHash  string    // 40-char lowercase hex
	Name      string    // torrent name as shown to the user
	FilePaths []string  // all file paths inside the torrent
	Trackers  []string  // tracker URLs
	SizeBytes int64     // total torrent size in bytes
	FileCount int       // cached len(FilePaths) for faceting
	AddedAt   time.Time // when this was added to the index
}

// docID returns the Bleve document ID for a torrent. We use the infohash
// directly so re-indexing the same torrent is a pure update (no duplicates).
func (d TorrentDoc) docID() string {
	return "t:" + strings.ToLower(d.InfoHash)
}

// toBleve converts the public TorrentDoc into the map form Bleve expects.
// Multi-value fields (files, trackers) are joined with a separator that
// the standard analyzer will tokenise cleanly.
func (d TorrentDoc) toBleve() map[string]any {
	if d.FileCount == 0 {
		d.FileCount = len(d.FilePaths)
	}
	if d.AddedAt.IsZero() {
		d.AddedAt = time.Now().UTC()
	}
	return map[string]any{
		fieldType:      typeTorrent,
		fieldInfoHash:  strings.ToLower(d.InfoHash),
		fieldName:      d.Name,
		fieldFilePaths: strings.Join(d.FilePaths, "\n"),
		fieldTrackers:  d.Trackers,
		fieldSizeBytes: d.SizeBytes,
		fieldAddedAt:   d.AddedAt,
		fieldFileCount: d.FileCount,
	}
}

// IndexTorrent adds or updates a torrent document. Safe to call on the same
// infohash multiple times; later calls overwrite earlier ones (Bleve's
// Index() semantics are put-or-replace on the document ID).
func (i *Index) IndexTorrent(doc TorrentDoc) error {
	if doc.InfoHash == "" {
		return errors.New("indexer: TorrentDoc.InfoHash must not be empty")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return errors.New("indexer: closed")
	}
	return i.bleve.Index(doc.docID(), doc.toBleve())
}

// DeleteTorrent removes a torrent document from the index. Not an error if
// the infohash is not present.
func (i *Index) DeleteTorrent(infoHash string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return errors.New("indexer: closed")
	}
	return i.bleve.Delete("t:" + strings.ToLower(infoHash))
}

// DocCount returns the number of documents currently in the index. Useful
// for smoke tests and debug output.
func (i *Index) DocCount() (uint64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return 0, errors.New("indexer: closed")
	}
	return i.bleve.DocCount()
}

// SearchRequest describes a query. Only Query is required.
type SearchRequest struct {
	Query string // free-form text; matches against name and file paths
	Limit int    // max hits to return; defaults to 20 if zero
}

// SearchHit is a single result row returned by Search.
type SearchHit struct {
	InfoHash  string   // 40-char lowercase hex
	Name      string   // torrent name
	SizeBytes int64    // total bytes
	FileCount int      // cached file count
	Trackers  []string // tracker URLs (may be empty)
	Score     float64  // Bleve relevance score
}

// SearchResponse is the result envelope for a Search call.
type SearchResponse struct {
	Total uint64      // total hit count across the whole index
	Hits  []SearchHit // hits, up to Limit, ordered by descending Score
	Took  time.Duration
}

// Search runs a query against the index and returns a SearchResponse.
// For M2.0 the query is always a Bleve QueryString (supports "word1 word2",
// "exact phrase", fielded queries like "name:ubuntu", and boolean ops).
func (i *Index) Search(req SearchRequest) (*SearchResponse, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil, errors.New("indexer: closed")
	}
	if req.Query == "" {
		return nil, errors.New("indexer: empty query")
	}
	if req.Limit <= 0 {
		req.Limit = 20
	}

	q := bleve.NewQueryStringQuery(req.Query)
	sr := bleve.NewSearchRequestOptions(q, req.Limit, 0, false)
	sr.Fields = []string{fieldInfoHash, fieldName, fieldSizeBytes, fieldFileCount, fieldTrackers}

	res, err := i.bleve.Search(sr)
	if err != nil {
		return nil, fmt.Errorf("indexer: search: %w", err)
	}

	out := &SearchResponse{
		Total: res.Total,
		Hits:  make([]SearchHit, 0, len(res.Hits)),
		Took:  res.Took,
	}
	for _, h := range res.Hits {
		hit := SearchHit{
			Score: h.Score,
		}
		if v, ok := h.Fields[fieldInfoHash].(string); ok {
			hit.InfoHash = v
		}
		if v, ok := h.Fields[fieldName].(string); ok {
			hit.Name = v
		}
		if v, ok := h.Fields[fieldSizeBytes].(float64); ok {
			hit.SizeBytes = int64(v)
		}
		if v, ok := h.Fields[fieldFileCount].(float64); ok {
			hit.FileCount = int(v)
		}
		switch v := h.Fields[fieldTrackers].(type) {
		case string:
			hit.Trackers = []string{v}
		case []any:
			for _, t := range v {
				if s, ok := t.(string); ok {
					hit.Trackers = append(hit.Trackers, s)
				}
			}
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}
