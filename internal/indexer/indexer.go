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

// Stats is the per-index snapshot returned by Stats(). Exposed by
// the HTTP /index/stats endpoint and the GUI Status tab. All byte
// counts are "as seen by the file system" — DirSizeBytes is the
// sum of every regular file under Index.path, so it measures what
// Bleve's scorch backend actually costs on disk.
//
// CorpusTextBytes is the sum of every ContentDoc.Text length in
// the index — the "raw text we fed to Bleve" number that can be
// divided into DirSizeBytes to get the text-to-index inflation
// ratio. This is the measurement that the v1.0.0 open question
// "how big is Bleve's index per TB of indexed text" wants.
type Stats struct {
	// DirBytes is the total on-disk size of the Bleve directory
	// (the sum of every regular file under Index.path).
	DirBytes int64 `json:"dir_bytes"`
	// DocCount is the total number of Bleve documents (torrent +
	// content, plus the schema sentinel).
	DocCount uint64 `json:"doc_count"`
	// TorrentCount is the number of torrent-level documents.
	TorrentCount uint64 `json:"torrent_count"`
	// ContentCount is the number of content-level documents
	// (one per file-chunk extraction).
	ContentCount uint64 `json:"content_count"`
	// CorpusTextBytes is the sum of every ContentDoc.Text field
	// currently in the index. Divide DirBytes by this to get the
	// index inflation ratio. Zero if the index has no content
	// docs yet.
	CorpusTextBytes int64 `json:"corpus_text_bytes"`
	// InflationRatio is DirBytes / CorpusTextBytes, for when the
	// corpus is non-empty. Zero otherwise. Useful as a one-number
	// summary for the GUI.
	InflationRatio float64 `json:"inflation_ratio"`
}

// Stats returns a Stats snapshot. Cheap-ish — the corpus-bytes
// sum requires scanning every content doc via a paginated
// MatchAll query, so callers should poll at human cadences (e.g.
// once every few seconds in the GUI), not in tight loops.
//
// Used by the HTTP API /index/stats endpoint that exposes the
// v1.0.0 index-size measurement.
func (i *Index) Stats() (Stats, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return Stats{}, errors.New("indexer: closed")
	}

	var out Stats

	// Total doc count is straight off the index.
	total, err := i.bleve.DocCount()
	if err != nil {
		return Stats{}, fmt.Errorf("indexer: Stats doc count: %w", err)
	}
	out.DocCount = total

	// Per-type counts via two cheap MatchAll queries that only
	// ask for the type field. We use Size=0 and read Total from
	// the response envelope so Bleve never has to materialise the
	// hit list — it's the cheapest "how many docs match" call.
	for _, tt := range []struct {
		ty  string
		dst *uint64
	}{
		{typeTorrent, &out.TorrentCount},
		{typeContent, &out.ContentCount},
	} {
		q := bleve.NewQueryStringQuery("+" + fieldType + ":" + tt.ty)
		sr := bleve.NewSearchRequestOptions(q, 0, 0, false)
		res, err := i.bleve.Search(sr)
		if err != nil {
			return Stats{}, fmt.Errorf("indexer: Stats %s count: %w", tt.ty, err)
		}
		*tt.dst = res.Total
	}

	// Corpus text bytes: walk every content doc in batches and
	// sum the length of the stored Text field. We ask Bleve to
	// project only the text field to keep the response payload
	// small.
	q := bleve.NewQueryStringQuery("+" + fieldType + ":" + typeContent)
	const batch = 1000
	var (
		from     = 0
		textSum  int64
		guardTTL = 64 // bound the loop defensively
	)
	for guardTTL > 0 {
		guardTTL--
		sr := bleve.NewSearchRequestOptions(q, batch, from, false)
		sr.Fields = []string{fieldText}
		res, err := i.bleve.Search(sr)
		if err != nil {
			return Stats{}, fmt.Errorf("indexer: Stats text scan: %w", err)
		}
		if len(res.Hits) == 0 {
			break
		}
		for _, h := range res.Hits {
			if v, ok := h.Fields[fieldText].(string); ok {
				textSum += int64(len(v))
			}
		}
		if len(res.Hits) < batch {
			break
		}
		from += batch
	}
	out.CorpusTextBytes = textSum

	// On-disk size: sum every regular file under Index.path.
	// Follows symlinks but does not descend into them (Bleve
	// doesn't use symlinks internally).
	if size, err := dirBytes(i.path); err == nil {
		out.DirBytes = size
	}

	if out.CorpusTextBytes > 0 {
		out.InflationRatio = float64(out.DirBytes) / float64(out.CorpusTextBytes)
	}
	return out, nil
}

// dirBytes sums the size of every regular file under root. Does
// not recurse into symlinked directories. Returns 0 for a
// missing or unreadable root so Stats() can degrade gracefully.
func dirBytes(root string) (int64, error) {
	var total int64
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, _ := dirBytes(root + "/" + e.Name())
			total += sub
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		total += info.Size()
	}
	return total, nil
}

// AllTorrentDocs returns every torrent-level document in the
// index, reconstructed from the stored fields. Used by the M11
// companion-index publisher to walk the local index when
// generating its serialised digest.
//
// Pagination is handled internally — Bleve's MatchAllQuery is
// run in batches of 1000 docs each. The returned slice is
// freshly allocated and is safe to retain after the index is
// closed.
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
	for {
		sr := bleve.NewSearchRequestOptions(q, batch, from, false)
		sr.Fields = []string{
			fieldInfoHash, fieldName, fieldFilePaths, fieldTrackers,
			fieldSizeBytes, fieldFileCount, fieldAddedAt,
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

// ContentDocsForInfoHash returns every content-level document
// stored under the given infohash, reconstructed from the
// stored fields. Used by the M11 companion-index publisher to
// pair each torrent record with its extracted text.
func (i *Index) ContentDocsForInfoHash(infoHash string) ([]ContentDoc, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.bleve == nil {
		return nil, errors.New("indexer: closed")
	}
	infoHash = strings.ToLower(infoHash)
	q := bleve.NewQueryStringQuery("+" + fieldType + ":" + typeContent +
		" +" + fieldInfoHash + ":" + infoHash)
	const batch = 1000
	var (
		out  []ContentDoc
		from = 0
	)
	for {
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

// torrentDocFromFields reconstructs a TorrentDoc from the
// projection map Bleve returns in SearchHit.Fields.
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
		// Bleve stores datetimes as RFC3339-formatted strings.
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			doc.AddedAt = t
		}
	}
	return doc
}

// contentDocFromFields reconstructs a ContentDoc from the
// projection map Bleve returns in SearchHit.Fields.
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
//
// The query is a Bleve QueryString, which supports the following
// syntax end-users can type directly into the search box:
//
//   - `word1 word2` — any document containing any term
//     (Bleve's default is SHOULD, not MUST).
//   - `+required` — prefix with `+` to make a term required.
//   - `-excluded` — prefix with `-` to exclude docs matching it.
//   - `"exact phrase"` — double quotes for phrase match.
//   - `name:ubuntu` — restrict a term to a specific field.
//     Text-analyzed fields (`name`, `files`, `text`) take any
//     tokenized term. Keyword-analyzed fields (`infohash`,
//     `trackers`, `file_path`, `mime`, `extractor`) match the
//     exact stored value only.
//   - `word~1` — fuzzy match with edit distance 1.
//   - `word^2` — boost a term.
//
// These are all locked down by TestSearchQueryOperators in
// indexer_test.go. The Layer-S swarm search and Layer-D DHT
// lookup both pass the raw query string through this same path,
// so the syntax is consistent across all three layers.
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
