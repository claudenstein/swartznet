package indexer

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

// schemaSentinelKey is the Bleve SetInternal key under which the schema
// version an index was created with is recorded. Internal metadata lives
// outside the searchable document store, so the sentinel never appears in
// search results or DocCount. The first Open on a fresh path writes it;
// every subsequent Open verifies it; a mismatch triggers a clean rebuild.
var schemaSentinelKey = []byte("_swartznet_schema_version")

// Index is SwartzNet's local full-text search index. It wraps a Bleve
// index on disk and exposes a narrow, intention-revealing API.
//
// Concurrency: all methods are safe for concurrent use; every method
// serializes on one global mutex.
type Index struct {
	path string
	log  *slog.Logger

	mu    sync.Mutex
	bleve bleve.Index
}

// Open opens (or creates) a Bleve index at path. An existing index whose
// stored schema version does not match SchemaVersion is removed wholesale
// and recreated empty — lost documents regenerate when torrents re-index.
func Open(path string) (*Index, error) {
	return OpenWithLogger(path, nil)
}

// OpenWithLogger is like Open but lets the caller supply a slog.Logger for
// schema-rebuild and recovery diagnostics. A nil logger falls back to a
// warn-level stderr handler.
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

	// For a freshly-created index the sentinel is always absent and is
	// simply written below. For an existing index the sentinel decides
	// whether a rebuild is needed.
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
// filepath.Join keeps the marker lookup correct on Windows.
func indexDirExists(path string) bool {
	_, err := os.Stat(filepath.Join(path, "index_meta.json"))
	return err == nil
}

// openOrCreate opens an existing Bleve index at path or creates a new one
// with the current schema when the directory is missing. It does NOT
// check the schema version; callers must do that separately.
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
// metadata store. Missing, empty, or unparseable sentinels yield 0, which
// triggers a rebuild.
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
// metadata, outside the search document store.
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
// document: what callers hand to IndexTorrent and what walks reconstruct.
type TorrentDoc struct {
	InfoHash  string    // 40-char lowercase hex
	Name      string    // torrent name as shown to the user
	FilePaths []string  // all file paths inside the torrent
	Trackers  []string  // tracker URLs
	SizeBytes int64     // total torrent size in bytes
	FileCount int       // cached len(FilePaths) for faceting
	AddedAt   time.Time // when this was added to the index
	// SignedBy is the 64-char hex ed25519 pubkey of whoever signed this
	// torrent's .torrent file, or empty for unsigned torrents. Keyword
	// field, so the search-by-publisher facet matches it exactly.
	SignedBy string
	// PreserveExistingSigner, when set, forbids a non-empty SignedBy from
	// OVERWRITING a different existing attribution. The companion subscriber
	// sets it: its snapshot proves the publisher authored the LIST, not that it
	// signed each torrent (there is no per-torrent signature), so it must never
	// hijack a torrent the node already attributes to another publisher.
	// Transient — never serialized.
	PreserveExistingSigner bool
}

// docID keys a torrent doc by infohash so re-indexing the same torrent is
// a pure update, never a duplicate.
func (d TorrentDoc) docID() string {
	return "t:" + strings.ToLower(d.InfoHash)
}

// toBleve converts the public TorrentDoc into the map form Bleve expects.
// FilePaths are joined with "\n" into the single-string files field and
// split back on "\n" during reconstruction — a path containing a newline
// corrupts the round-trip (frozen legacy behavior, SPEC §5.5).
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
		fieldSignedBy:  strings.ToLower(d.SignedBy),
	}
}

// ErrForeignTorrent is returned by IndexTorrent when a PreserveExistingSigner
// write targets a torrent already attributed to a DIFFERENT publisher. It is a
// benign skip signal, not a failure: the companion subscriber uses it to skip
// that torrent's content docs too, so a followed publisher cannot hijack a
// torrent the node holds.
var ErrForeignTorrent = errors.New("indexer: torrent already attributed to a different publisher")

// IndexTorrent adds or updates a torrent document (put-or-replace on the
// doc ID). Non-empty stored signed_by is sticky (DECISIONS §7-Q37): a
// later upsert with an empty SignedBy — a local magnet re-add, a
// companion import — carries the stored signer forward instead of
// blanking it. The point read and the write happen under the same mutex
// hold, so the carry-forward is atomic for every writer crossing this
// seam.
func (i *Index) IndexTorrent(doc TorrentDoc) error {
	if doc.InfoHash == "" {
		return errors.New("indexer: TorrentDoc.InfoHash must not be empty")
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return errors.New("indexer: closed")
	}
	if doc.SignedBy == "" {
		if stored := i.storedSignedByLocked(doc.docID()); stored != "" {
			doc.SignedBy = stored
		}
	} else if doc.PreserveExistingSigner {
		// A companion import must not hijack a torrent the node ALREADY HOLDS: the
		// snapshot proves the publisher authored the LIST, not that it owns this
		// torrent. If a doc already exists under a DIFFERENT signer — including an
		// UNSIGNED local torrent (stored ""), the common case — skip the ENTIRE
		// write (Name/FilePaths/Size + attribution) and signal the caller to skip
		// its content too. A new torrent (no existing doc) or one already
		// attributed to the SAME publisher is stamped normally.
		if d, err := i.bleve.Document(doc.docID()); err == nil && d != nil {
			if stored := i.storedSignedByLocked(doc.docID()); stored != strings.ToLower(doc.SignedBy) {
				return ErrForeignTorrent
			}
		}
	}
	return i.bleve.Index(doc.docID(), doc.toBleve())
}

// storedSignedByLocked returns the stored signed_by field of the document
// with the given ID, or "" when the document is absent or the read fails
// (a failed read must not block indexing). Caller must hold i.mu.
func (i *Index) storedSignedByLocked(docID string) string {
	q := bleve.NewDocIDQuery([]string{docID})
	sr := bleve.NewSearchRequestOptions(q, 1, 0, false)
	sr.Fields = []string{fieldSignedBy}
	res, err := i.bleve.Search(sr)
	if err != nil || len(res.Hits) == 0 {
		return ""
	}
	v, _ := res.Hits[0].Fields[fieldSignedBy].(string)
	return v
}

// DeleteTorrent removes a torrent document from the index. Not an error
// if the infohash is not present.
func (i *Index) DeleteTorrent(infoHash string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return errors.New("indexer: closed")
	}
	return i.bleve.Delete("t:" + strings.ToLower(infoHash))
}

// DeleteContentForTorrent removes every content-level document belonging
// to the given infohash and returns how many were deleted. Selection uses
// exact-match term queries (never a QueryString) so a hostile or
// malformed infohash cannot inject query syntax.
func (i *Index) DeleteContentForTorrent(infoHash string) (int, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return 0, errors.New("indexer: closed")
	}
	return i.deleteByQueryLocked(contentForInfoHashQuery(infoHash))
}

// DocCount returns the number of documents currently in the index. The
// schema sentinel is internal metadata, not a document, so DocCount is
// exactly torrent docs + content docs.
func (i *Index) DocCount() (uint64, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return 0, errors.New("indexer: closed")
	}
	return i.bleve.DocCount()
}

// AllTorrentDocs returns every torrent-level document, reconstructed from
// stored fields, paginated internally in 1000-doc batches. The walk holds
// the global mutex, so a count-scaled page ceiling bounds it against an
// index that never returns a short page; hitting the ceiling logs
// indexer.all_torrent_docs_truncated and truncates rather than spinning.
func (i *Index) AllTorrentDocs() ([]TorrentDoc, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil, errors.New("indexer: closed")
	}
	q := bleve.NewQueryStringQuery("+" + fieldType + ":" + typeTorrent)
	const batch = 1000
	var (
		out  []TorrentDoc
		from = 0
	)
	countReq := bleve.NewSearchRequestOptions(q, 0, 0, false)
	countRes, err := i.bleve.Search(countReq)
	if err != nil {
		return nil, fmt.Errorf("indexer: AllTorrentDocs count: %w", err)
	}
	maxPages := int(countRes.Total/batch) + 2
	for page := 0; ; page++ {
		if page >= maxPages {
			i.log.Warn("indexer.all_torrent_docs_truncated",
				"pages", page, "scanned", from, "torrent_count", countRes.Total)
			break
		}
		sr := bleve.NewSearchRequestOptions(q, batch, from, false)
		sr.Fields = []string{
			fieldInfoHash, fieldName, fieldFilePaths, fieldTrackers,
			fieldSizeBytes, fieldFileCount, fieldAddedAt, fieldSignedBy,
		}
		res, err := i.bleve.Search(sr)
		if err != nil {
			return nil, fmt.Errorf("indexer: AllTorrentDocs: %w", err)
		}
		if len(res.Hits) == 0 {
			break
		}
		for _, h := range res.Hits {
			out = append(out, torrentDocFromFields(h.Fields))
		}
		if len(res.Hits) < batch {
			break
		}
		from += batch
	}
	return out, nil
}

// ContentDocsForInfoHash returns every content-level document stored
// under the given infohash, reconstructed from stored fields. Same
// pagination and count-scaled ceiling as AllTorrentDocs; truncation logs
// indexer.content_docs_truncated.
func (i *Index) ContentDocsForInfoHash(infoHash string) ([]ContentDoc, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil, errors.New("indexer: closed")
	}
	q := contentForInfoHashQuery(infoHash)
	const batch = 1000
	var (
		out  []ContentDoc
		from = 0
	)
	countReq := bleve.NewSearchRequestOptions(q, 0, 0, false)
	countRes, err := i.bleve.Search(countReq)
	if err != nil {
		return nil, fmt.Errorf("indexer: ContentDocsForInfoHash count: %w", err)
	}
	maxPages := int(countRes.Total/batch) + 2
	for page := 0; ; page++ {
		if page >= maxPages {
			i.log.Warn("indexer.content_docs_truncated",
				"pages", page, "scanned", from,
				"info_hash", infoHash, "content_count", countRes.Total)
			break
		}
		sr := bleve.NewSearchRequestOptions(q, batch, from, true)
		sr.Fields = []string{
			fieldInfoHash, fieldFileIndex, fieldFilePath, fieldFileSize,
			fieldMime, fieldText, fieldExtractor, fieldIndexedAt,
		}
		res, err := i.bleve.Search(sr)
		if err != nil {
			return nil, fmt.Errorf("indexer: ContentDocsForInfoHash: %w", err)
		}
		if len(res.Hits) == 0 {
			break
		}
		for _, h := range res.Hits {
			out = append(out, contentDocFromFields(h.Fields))
		}
		if len(res.Hits) < batch {
			break
		}
		from += batch
	}
	return out, nil
}

// torrentDocFromFields reconstructs a TorrentDoc from the projection map
// Bleve returns in SearchHit.Fields. Multi-value keyword fields project
// as string for one value and []any for several; both branches must be
// handled. Datetimes round-trip as RFC3339 strings.
func torrentDocFromFields(fields map[string]any) TorrentDoc {
	doc := TorrentDoc{}
	if v, ok := fields[fieldInfoHash].(string); ok {
		doc.InfoHash = v
	}
	if v, ok := fields[fieldName].(string); ok {
		doc.Name = v
	}
	if v, ok := fields[fieldFilePaths].(string); ok && v != "" {
		doc.FilePaths = strings.Split(v, "\n")
	}
	switch v := fields[fieldTrackers].(type) {
	case string:
		if v != "" {
			doc.Trackers = []string{v}
		}
	case []any:
		for _, t := range v {
			if s, ok := t.(string); ok {
				doc.Trackers = append(doc.Trackers, s)
			}
		}
	}
	if v, ok := fields[fieldSizeBytes].(float64); ok {
		doc.SizeBytes = int64(v)
	}
	if v, ok := fields[fieldFileCount].(float64); ok {
		doc.FileCount = int(v)
	}
	if v, ok := fields[fieldAddedAt].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			doc.AddedAt = t
		}
	}
	if v, ok := fields[fieldSignedBy].(string); ok {
		doc.SignedBy = v
	}
	return doc
}

// contentDocFromFields reconstructs a ContentDoc from the projection map
// Bleve returns in SearchHit.Fields. ChunkIndex is encoded only in the
// doc ID — there is no stored chunk_index field in schema v3 — so every
// reconstructed doc carries ChunkIndex 0 (frozen for byte-compat).
func contentDocFromFields(fields map[string]any) ContentDoc {
	doc := ContentDoc{}
	if v, ok := fields[fieldInfoHash].(string); ok {
		doc.InfoHash = v
	}
	if v, ok := fields[fieldFileIndex].(float64); ok {
		doc.FileIndex = int(v)
	}
	if v, ok := fields[fieldFilePath].(string); ok {
		doc.FilePath = v
	}
	if v, ok := fields[fieldFileSize].(float64); ok {
		doc.FileSize = int64(v)
	}
	if v, ok := fields[fieldMime].(string); ok {
		doc.Mime = v
	}
	if v, ok := fields[fieldText].(string); ok {
		doc.Text = v
	}
	if v, ok := fields[fieldExtractor].(string); ok {
		doc.Extractor = v
	}
	if v, ok := fields[fieldIndexedAt].(string); ok && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			doc.IndexedAt = t
		}
	}
	return doc
}

// contentForInfoHashQuery builds the exact-match conjunction selecting
// every content doc for one infohash. Term queries (not a QueryString)
// mean the infohash is never re-parsed by Bleve's query grammar, so a
// hostile or malformed infohash cannot inject query syntax. Mirrors the
// SignedBy filter path in Search.
func contentForInfoHashQuery(infoHash string) query.Query {
	typeQ := bleve.NewTermQuery(typeContent)
	typeQ.SetField(fieldType)
	ihQ := bleve.NewTermQuery(strings.ToLower(infoHash))
	ihQ.SetField(fieldInfoHash)
	return bleve.NewConjunctionQuery(typeQ, ihQ)
}

// deleteByQueryLocked deletes every document matching q. Caller must hold
// i.mu. Returns the number of documents deleted.
//
// Bleve 2.x has no public DeleteByQuery, so IDs are fetched in batches
// and deleted one by one. The loop is bounded off the matching-set size
// reported by the first search: a working delete shrinks that set each
// batch, so exceeding the bound means deletes are silently ineffective —
// the loop fails closed with an error rather than spinning forever under
// the global mutex.
func (i *Index) deleteByQueryLocked(q query.Query) (int, error) {
	const batchSize = 1000
	sr := bleve.NewSearchRequestOptions(q, batchSize, 0, false)
	// Only IDs are needed for deletion; no field projection.
	sr.Fields = nil

	var (
		deleted  int
		maxIters int // computed from the first search's Total
	)
	for iter := 0; ; iter++ {
		res, err := i.bleve.Search(sr)
		if err != nil {
			return deleted, fmt.Errorf("indexer: deleteByQuery search: %w", err)
		}
		if iter == 0 {
			// (Total/batchSize)+1 batches suffice when each delete
			// takes effect; 2x slack + a floor absorbs index churn
			// without ever becoming unbounded.
			maxIters = int(res.Total/batchSize)*2 + 4
		}
		if iter >= maxIters {
			return deleted, fmt.Errorf(
				"indexer: deleteByQuery made no progress (%d deleted, %d iterations) — deletes ineffective",
				deleted, iter)
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
