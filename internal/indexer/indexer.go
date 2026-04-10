package indexer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
)

// schemaSentinelKey is the Bleve Index.SetInternal key under which we
// record the schema version an index was created with. Internal metadata
// lives outside the searchable document store, so the sentinel never
// appears in search results. The first call to Open on a fresh path writes
// it; every subsequent Open verifies it. A mismatch triggers a clean
// rebuild.
var schemaSentinelKey = []byte("_swartznet_schema_version")

// Index is SwartzNet's local full-text search index. It wraps a Bleve index
// on disk and exposes a narrow, intention-revealing API to callers.
//
// Concurrency: all methods are safe for concurrent use.
type Index struct {
	path string
	log  *slog.Logger

	mu    sync.Mutex
	bleve bleve.Index
}

// Open opens (or creates) a Bleve index at path. If path already contains a
// Bleve index with a matching SchemaVersion, it is opened read-write;
// otherwise a new index is created with the SwartzNet schema. If an
// existing index is found but its schema version does not match, the old
// directory is removed and a fresh index is created — this is safe because
// any lost documents will be rebuilt from re-adding torrents.
//
// The logger is optional; pass nil to silence schema-rebuild messages.
func Open(path string) (*Index, error) {
	return OpenWithLogger(path, nil)
}

// OpenWithLogger is like Open but lets the caller supply a slog.Logger for
// schema-rebuild and recovery diagnostics.
func OpenWithLogger(path string, log *slog.Logger) (*Index, error) {
	if path == "" {
		return nil, errors.New("indexer: path must not be empty")
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	existed := indexDirExists(path)

	bi, err := openOrCreate(path)
	if err != nil {
		return nil, err
	}

	// For a freshly-created index the sentinel is always absent and we
	// simply write it. For an existing index the sentinel tells us whether
	// a rebuild is needed.
	if existed {
		stored := readSchemaVersion(bi)
		if stored != SchemaVersion {
			log.Warn("indexer.schema_rebuild",
				"path", path,
				"stored_version", stored,
				"wanted_version", SchemaVersion,
			)
			if err := bi.Close(); err != nil {
				return nil, fmt.Errorf("indexer: close before rebuild: %w", err)
			}
			if err := os.RemoveAll(path); err != nil {
				return nil, fmt.Errorf("indexer: remove stale index: %w", err)
			}
			bi, err = openOrCreate(path)
			if err != nil {
				return nil, err
			}
		}
	}

	// Ensure the sentinel is present. Idempotent: SetInternal overwrites.
	if err := writeSchemaVersion(bi, SchemaVersion); err != nil {
		return nil, fmt.Errorf("indexer: write schema sentinel: %w", err)
	}

	return &Index{path: path, bleve: bi, log: log}, nil
}

// indexDirExists reports whether path already holds a Bleve index.
func indexDirExists(path string) bool {
	_, err := os.Stat(path + "/index_meta.json")
	return err == nil
}

// openOrCreate opens an existing Bleve index at path or creates a new one
// with the current schema if the directory is missing. It does NOT check
// the schema version; callers must do that separately.
func openOrCreate(path string) (bleve.Index, error) {
	if indexDirExists(path) {
		bi, err := bleve.Open(path)
		if err != nil {
			return nil, fmt.Errorf("indexer: open %q: %w", path, err)
		}
		return bi, nil
	}
	bi, err := bleve.New(path, buildMapping())
	if err != nil {
		return nil, fmt.Errorf("indexer: create %q: %w", path, err)
	}
	return bi, nil
}

// readSchemaVersion reads the sentinel from an open Bleve index's internal
// metadata store. Returns 0 if the sentinel is missing or unparseable,
// which triggers a rebuild.
func readSchemaVersion(bi bleve.Index) int {
	val, err := bi.GetInternal(schemaSentinelKey)
	if err != nil || len(val) == 0 {
		return 0
	}
	v, err := strconv.Atoi(string(val))
	if err != nil {
		return 0
	}
	return v
}

// writeSchemaVersion persists the schema version integer as internal
// metadata. It lives outside the search document store, so it does not
// pollute search results or doc counts.
func writeSchemaVersion(bi bleve.Index, v int) error {
	return bi.SetInternal(schemaSentinelKey, []byte(strconv.Itoa(v)))
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

// SearchHit is a single result row returned by Search. Fields marked
// "torrent" are populated when the hit is a torrent-level document; fields
// marked "content" are populated for content-level hits. A single call to
// Search can return both kinds interleaved — check DocType to tell them
// apart.
type SearchHit struct {
	DocType  string  // "torrent" or "content"
	InfoHash string  // 40-char lowercase hex
	Score    float64 // Bleve relevance score

	// Torrent-level fields.
	Name      string   // torrent name
	SizeBytes int64    // total torrent bytes
	FileCount int      // cached file count
	Trackers  []string // tracker URLs (may be empty)

	// Content-level fields.
	FileIndex int    // position in torrent's file list
	FilePath  string // user-visible file path
	FileSize  int64  // file bytes on disk
	Mime      string // MIME type
	Extractor string // producer extractor name
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
	sr.Fields = []string{
		fieldType,
		fieldInfoHash,
		// torrent fields
		fieldName, fieldSizeBytes, fieldFileCount, fieldTrackers,
		// content fields
		fieldFileIndex, fieldFilePath, fieldFileSize, fieldMime, fieldExtractor,
	}

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
		hit := SearchHit{Score: h.Score}
		if v, ok := h.Fields[fieldType].(string); ok {
			hit.DocType = v
		}
		if v, ok := h.Fields[fieldInfoHash].(string); ok {
			hit.InfoHash = v
		}
		// Torrent-level fields.
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
		// Content-level fields.
		if v, ok := h.Fields[fieldFileIndex].(float64); ok {
			hit.FileIndex = int(v)
		}
		if v, ok := h.Fields[fieldFilePath].(string); ok {
			hit.FilePath = v
		}
		if v, ok := h.Fields[fieldFileSize].(float64); ok {
			hit.FileSize = int64(v)
		}
		if v, ok := h.Fields[fieldMime].(string); ok {
			hit.Mime = v
		}
		if v, ok := h.Fields[fieldExtractor].(string); ok {
			hit.Extractor = v
		}
		out.Hits = append(out.Hits, hit)
	}
	return out, nil
}

// deleteByQueryLocked deletes every document matching the given query
// string. Caller must hold i.mu. Returns the number of documents deleted.
//
// Bleve 2.x does not ship a public DeleteByQuery, so we fetch IDs in
// batches and delete them one by one. For the sizes we care about
// (~thousands of content docs per removed torrent) this is acceptable.
func (i *Index) deleteByQueryLocked(queryString string) (int, error) {
	const batchSize = 1000
	q := bleve.NewQueryStringQuery(queryString)
	sr := bleve.NewSearchRequestOptions(q, batchSize, 0, false)
	// We only need IDs for deletion; no field projection.
	sr.Fields = nil

	var deleted int
	for {
		res, err := i.bleve.Search(sr)
		if err != nil {
			return deleted, fmt.Errorf("indexer: deleteByQuery search: %w", err)
		}
		if len(res.Hits) == 0 {
			return deleted, nil
		}
		batch := i.bleve.NewBatch()
		for _, h := range res.Hits {
			batch.Delete(h.ID)
		}
		if err := i.bleve.Batch(batch); err != nil {
			return deleted, fmt.Errorf("indexer: deleteByQuery batch: %w", err)
		}
		deleted += len(res.Hits)
		if uint64(len(res.Hits)) < uint64(batchSize) {
			return deleted, nil
		}
	}
}
