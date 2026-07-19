// Package companion implements the companion content-index (BEP-46 pointer
// pattern): a publisher builds a compact gzip-JSON CompanionIndex describing
// its locally-indexed torrents + extracted text, wraps it as a single-file
// .torrent, seeds it, and publishes a BEP-46 mutable pointer at the well-known
// salt "_sn_content_index" under its identity; a follower resolves the pointer,
// fetches the .torrent fail-closed, verifies the snapshot was authored by the
// followed publisher, dedups by GeneratedAt, and imports the records into its
// local Bleve stamped with SignedBy = the publisher's pubkey.
//
// The package depends on the DHT and the engine ONLY through the narrow port
// interfaces below — it never imports internal/dhtindex or internal/engine.
// The BEP-46 pointer primitives it consumes (PutInfohashPointer /
// GetInfohashPointer at the salt "_sn_content_index") already exist in
// internal/dhtindex (Slice 9). It does import internal/indexer for the corpus
// document types (the source it reads and the sink it writes), which is the
// acyclic search-layer dependency, not an engine/DHT edge.
package companion

import (
	"context"

	"github.com/anacrolix/torrent/metainfo"

	"github.com/swartznet/swartznet/internal/indexer"
)

// SaltContentIndex is the well-known BEP-44 salt under which every publisher
// puts its companion-index pointer. The target is SHA1(pubkey || salt),
// computed inside the dhtindex putter. Stable constant — never change without
// bumping FormatVersion.
const SaltContentIndex = "_sn_content_index"

// PointerPutter is the BEP-46 write primitive (satisfied by
// *dhtindex.AnacrolixPutter). The publisher passes []byte(SaltContentIndex).
type PointerPutter interface {
	PutInfohashPointer(ctx context.Context, salt []byte, infohash [20]byte) error
}

// PointerGetter is the BEP-46 read primitive (satisfied by
// *dhtindex.AnacrolixGetter). The subscriber uses only the non-ts variant —
// it never trusts the pointer's own timestamp for dedup (it dedups on the
// signed CompanionIndex.GeneratedAt instead).
type PointerGetter interface {
	GetInfohashPointer(ctx context.Context, pubkey [32]byte, salt []byte) ([20]byte, error)
}

// TorrentSeeder seeds the freshly built companion .torrent, serving the single
// file from contentPath (where WriteCompanionFiles wrote it — the companion dir
// is separate from the engine's data dir), and drops a superseded companion
// seed by infohash so they do not accumulate. The engine satisfies it.
type TorrentSeeder interface {
	SeedMetaInfo(mi *metainfo.MetaInfo, contentPath string) error
	DropTorrent(infohash [20]byte) error
}

// CompanionFetcher downloads a companion .torrent FAIL-CLOSED (exactly 1 file,
// ≤32 MiB, safe filename — the bounds live in the engine) and returns the
// on-disk path. Satisfied by *engine.Engine.
type CompanionFetcher interface {
	FetchCompanionTorrent(ctx context.Context, infohash [20]byte) (path string, err error)
}

// CorpusSource is the read side (CorpusExport): the local Bleve index the
// publisher walks to build a CompanionIndex. *indexer.Index satisfies it.
type CorpusSource interface {
	AllTorrentDocs() ([]indexer.TorrentDoc, error)
	ContentDocsForInfoHash(infoHash string) ([]indexer.ContentDoc, error)
}

// Ingester is the write side (CorpusImport): the local Bleve index the
// subscriber writes imported records into. *indexer.Index satisfies it.
type Ingester interface {
	IndexTorrent(doc indexer.TorrentDoc) error
	IndexContent(doc indexer.ContentDoc) error
}
