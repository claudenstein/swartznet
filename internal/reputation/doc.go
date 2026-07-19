// Package reputation implements SwartzNet's spam-resistance layer:
// a store of two complementary signals with no Bleve, DHT, or HTTP
// dependencies. Ranking that folds these signals together lives in
// the Layer-D consumer, not here.
//
// The two mechanisms:
//
//  1. A Bloom filter of "known-good" infohashes — torrents the user
//     downloaded successfully or explicitly confirmed. A lookup
//     result whose infohash hits the filter is boosted; a miss is
//     demoted, not dropped. The on-disk format is a frozen golden
//     format (see bloom.go): existing known-good.bloom files must
//     stay byte-compatible.
//
//  2. A per-publisher reputation table. For every indexer pubkey we
//     record (hits_returned, hits_confirmed, hits_flagged) and derive
//     a single Bayesian-smoothed score. Indexers below a configurable
//     threshold are demoted or skipped by the lookup fan-out.
//
// A companion in-memory SourceTracker (sources.go) attributes each
// recently-seen infohash to the indexers that returned it, so the
// flag path demotes only the responsible pubkeys.
//
// Files:
//
//   - bloom.go      : Bloom filter type with on-disk persistence.
//   - reputation.go : per-pubkey reputation tracker + seed list.
//   - sources.go    : bounded LRU infohash → source-pubkey attribution.
package reputation
