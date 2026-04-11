// Package companion implements SwartzNet's "companion content
// index" feature — the F3 distributed-content-search story from
// docs/05-integration-design.md §1.2 and §11.
//
// A companion index is a single gzipped JSON file containing a
// digest of one publisher's local Bleve content index: per-torrent
// records, per-file extracted text chunks, and basic metadata. The
// publisher serialises this file once per refresh interval, wraps
// it in a regular BitTorrent v1 .torrent file, adds the torrent to
// its own engine to seed it, and publishes a BEP-46-style pointer
// to the new infohash via the existing dhtindex Layer-D path.
//
// Other peers fetch the pointer, download the companion torrent,
// decode it, and import the records into their own local Bleve
// index — at which point those records become searchable through
// every existing search path (Layer L locally, Layer S over
// sn_search, even Layer D for the imported subset).
//
// File split:
//
//   - types.go     : the JSON schema (CompanionIndex / TorrentRecord
//     / FileRecord / ContentChunk)
//   - serialize.go : Encode / Decode with gzip framing
//   - build.go     : (M11b) BuildFromIndex helper that walks an
//     indexer.Index and produces a CompanionIndex
//   - torrent.go   : (M11b) Wrap a serialised companion file in a
//     v1 .torrent and return the metainfo
//   - import.go    : (M11d) Decode + import into a target Bleve
//     index, namespacing each record so the
//     subscriber knows which publisher contributed
//     which document
//
// M11a (this file's commit) implements just the schema and the
// encode/decode round trip. The build/import paths land in M11b
// and M11d respectively.
package companion
