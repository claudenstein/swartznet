package indexer

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
)

// Document type constants. We use the special Bleve "_type" field so the
// same index can hold different kinds of documents:
//
//   - "torrent" — one per torrent, holds name, file list, trackers. M2.0.
//   - "content" — one per extracted-text chunk of a file inside a torrent.
//     M2.2a. Linked back to its torrent via the infohash field.
//
// Keeping both in one index means `swartznet search foo` naturally returns
// both torrent-level matches and content-level matches in a single result
// set, which is exactly the UX the integration design calls for.
const (
	typeTorrent = "torrent"
	typeContent = "content"
)

// Field names. Kept as constants so tests and callers don't drift from
// whatever the schema says.
const (
	fieldType      = "type"     // document discriminator
	fieldInfoHash  = "infohash" // 40-char hex, keyword analyzer
	fieldName      = "name"     // torrent name, standard analyzer
	fieldFilePaths = "files"    // concatenated file paths, standard analyzer
	fieldTrackers  = "trackers" // tracker URLs, keyword analyzer
	fieldSizeBytes = "size_bytes"
	fieldAddedAt   = "added_at"
	fieldFileCount = "file_count"

	// Content document fields.
	fieldFileIndex = "file_index" // position in the torrent's file list
	fieldFilePath  = "file_path"  // single file path (keyword)
	fieldFileSize  = "file_size"  // bytes
	fieldMime      = "mime"       // MIME type string (keyword)
	fieldText      = "text"       // the extracted text body (standard analyzer)
	fieldExtractor = "extractor"  // name of the extractor that produced this doc
	fieldIndexedAt = "indexed_at"
)

// SchemaVersion is bumped whenever the Bleve mapping changes in a way that
// is not backwards compatible with indexes created under an earlier version.
// The Index.Open path writes this as a sentinel document on first creation
// and checks it on reopen; a mismatch triggers a clean rebuild.
const SchemaVersion = 2

// buildMapping constructs the Bleve index mapping for SwartzNet. M2.0
// ships a single document type ("torrent"); M2.2 will add a "content"
// type for extracted file text, which is why the mapping is per-type
// rather than using the DefaultMapping.
func buildMapping() *mapping.IndexMappingImpl {
	idx := bleve.NewIndexMapping()

	// Keyword fields — stored exactly as given, no tokenisation.
	kw := bleve.NewTextFieldMapping()
	kw.Analyzer = keyword.Name
	kw.Store = true
	kw.Index = true

	// Full-text fields — standard analyzer gives us lowercasing, unicode
	// tokenisation, and (implicitly) stop-word removal for common languages.
	// M5 will replace this with a language-aware analyzer chain once
	// lingua-go is integrated.
	ft := bleve.NewTextFieldMapping()
	ft.Analyzer = standard.Name
	ft.Store = true
	ft.Index = true
	ft.IncludeInAll = true
	ft.IncludeTermVectors = true

	num := bleve.NewNumericFieldMapping()
	num.Store = true
	num.Index = true

	dt := bleve.NewDateTimeFieldMapping()
	dt.Store = true
	dt.Index = true

	torrent := bleve.NewDocumentMapping()
	torrent.AddFieldMappingsAt(fieldInfoHash, kw)
	torrent.AddFieldMappingsAt(fieldName, ft)
	torrent.AddFieldMappingsAt(fieldFilePaths, ft)
	torrent.AddFieldMappingsAt(fieldTrackers, kw)
	torrent.AddFieldMappingsAt(fieldSizeBytes, num)
	torrent.AddFieldMappingsAt(fieldAddedAt, dt)
	torrent.AddFieldMappingsAt(fieldFileCount, num)

	content := bleve.NewDocumentMapping()
	content.AddFieldMappingsAt(fieldInfoHash, kw)
	content.AddFieldMappingsAt(fieldFileIndex, num)
	content.AddFieldMappingsAt(fieldFilePath, kw)
	content.AddFieldMappingsAt(fieldFileSize, num)
	content.AddFieldMappingsAt(fieldMime, kw)
	content.AddFieldMappingsAt(fieldText, ft) // the main searchable body
	content.AddFieldMappingsAt(fieldExtractor, kw)
	content.AddFieldMappingsAt(fieldIndexedAt, dt)

	idx.AddDocumentMapping(typeTorrent, torrent)
	idx.AddDocumentMapping(typeContent, content)
	idx.TypeField = fieldType
	idx.DefaultType = typeTorrent

	return idx
}
