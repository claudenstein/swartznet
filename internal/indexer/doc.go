// Package indexer owns SwartzNet's local full-text search index (Layer L
// in the design doc).
//
// M2.0 — the current scope — indexes torrent-level metadata only: torrent
// name, infohash, file list (paths + sizes), trackers, and bookkeeping. It
// gives the user an immediate "search what I've added" capability and
// validates the Bleve integration before M2.1 adds piece-to-file completion
// tracking and M2.2 layers on text extractors for actual file content.
//
// The schema is intentionally open-ended so that M2.2 can add nested
// Content documents without rebuilding the index. See docs/05-integration-design.md
// §4.1 for the design rationale and §6 for the ingestion pipeline the
// indexer will eventually plug into.
//
// Concurrency: Index is safe for concurrent use by many goroutines. Open
// returns a single handle; close it exactly once via Close.
package indexer
