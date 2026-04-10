package indexer

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/mapping"
)

// Document type constants. We use the special Bleve "_type" field so the
// same index can hold different kinds of documents (M2.2 will add a
// "content" type for extracted file text).
const (
	typeTorrent = "torrent"
)

// Field names. Kept as constants so tests and callers don't drift from
// whatever the schema says.
const (
	fieldType      = "type"      // document discriminator
	fieldInfoHash  = "infohash"  // 40-char hex, keyword analyzer
	fieldName      = "name"      // torrent name, standard analyzer
	fieldFilePaths = "files"     // concatenated file paths, standard analyzer
	fieldTrackers  = "trackers"  // tracker URLs, keyword analyzer
	fieldSizeBytes = "size_bytes"
	fieldAddedAt   = "added_at"
	fieldFileCount = "file_count"
)

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

	idx.AddDocumentMapping(typeTorrent, torrent)
	idx.TypeField = fieldType
	idx.DefaultType = typeTorrent

	return idx
}
