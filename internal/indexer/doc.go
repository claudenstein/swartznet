// Package indexer owns SwartzNet's local full-text search index — Layer L.
//
// One Bleve scorch index (SchemaVersion 3) holds two document types keyed
// by the "type" field: "torrent" (one per torrent: name, file list,
// trackers, signer) and "content" (one per extracted-text chunk, linked to
// its torrent via the infohash field). Doc IDs are deterministic
// ("t:<infohash>" / "c:<infohash>:<fileIdx>:<chunkIdx>") so re-indexing is
// always a pure replace, never a duplicate.
//
// The package never touches the wire and never learns which peer or HTTP
// request triggered a query: Layer S consumes SearchResponse through its
// own adapter, and the HTTP API talks to the daemon's collaborator seam,
// not to this package directly.
//
// Concurrency: Index is safe for concurrent use by many goroutines; every
// method serializes on one global mutex. Open returns a single handle;
// close it exactly once via Close (idempotent).
package indexer
