package indexer

import (
	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/keyword"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
)

// Document type constants. Both kinds live in one index under the "type"
// discriminator so a single search naturally interleaves torrent-level and
// content-level matches.
const (
	typeTorrent = "torrent"
	typeContent = "content"
)

// Field names. Wire-frozen: they appear in the QueryString syntax users
// type ("name:ubuntu", "infohash:<hex>"), so renaming any of them breaks
// the public search grammar.
const (
	fieldType      = "type"     // document discriminator
	fieldInfoHash  = "infohash" // 40-char hex, keyword analyzer
	fieldName      = "name"     // torrent name, standard analyzer
	fieldFilePaths = "files"    // '\n'-joined file paths, standard analyzer
	fieldTrackers  = "trackers" // tracker URLs, keyword analyzer, multi-value
	fieldSizeBytes = "size_bytes"
	fieldAddedAt   = "added_at"
	fieldFileCount = "file_count"
	fieldSignedBy  = "signed_by" // 64-char hex pubkey of the .torrent signer (v0.7+)

	// Content document fields.
	fieldFileIndex = "file_index" // position in the torrent's file list
	fieldFilePath  = "file_path"  // single file path (keyword)
	fieldFileSize  = "file_size"  // bytes
	fieldMime      = "mime"       // MIME type string (keyword)
	fieldText      = "text"       // the extracted text body (standard analyzer)
	fieldExtractor = "extractor"  // name of the extractor that produced this doc
	fieldIndexedAt = "indexed_at"
)

// SchemaVersion is bumped whenever the Bleve mapping changes incompatibly
// with indexes created under an earlier version. Open writes it as an
// internal-store sentinel on creation and checks it on reopen; a mismatch
// wipes the directory and rebuilds from scratch (torrent docs regenerate
// via autoIndex on session restore; content docs on re-extraction).
//
// v3 (2026-04-13): TorrentDoc gains the signed_by keyword field.
const SchemaVersion = 3

// buildMapping constructs the SwartzNet Bleve mapping. Keyword fields
// store the exact (lowercased-at-write for infohash/signed_by) value;
// full-text fields carry term vectors so highlight fragments work; all
// other mapping properties keep the bleve v2 defaults — disabling any of
// them changes the "_all" behavior unqualified query terms rely on.
func buildMapping() *mapping.IndexMappingImpl {
	idx := bleve.NewIndexMapping()

	// Keyword fields — stored exactly as given, no tokenisation.
	kw := bleve.NewTextFieldMapping()
	kw.Analyzer = keyword.Name
	kw.Store = true
	kw.Index = true

	// Full-text fields — standard analyzer: lowercasing plus unicode
	// tokenisation. Term vectors enable snippet highlighting.
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
	torrent.AddFieldMappingsAt(fieldSignedBy, kw)

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
