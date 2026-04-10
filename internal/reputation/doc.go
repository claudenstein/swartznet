// Package reputation implements SwartzNet's spam-resistance layer
// (M5 in docs/05-integration-design.md §4.3 / §9).
//
// There are two complementary mechanisms:
//
//  1. A Bloom filter of "known-good" infohashes — torrents that
//     either the user has downloaded successfully or that the user
//     explicitly confirmed via `swartznet confirm`. Lookup results
//     whose infohash hits the filter get a score boost; results
//     that miss it are not dropped, just demoted. Bloom filters give
//     us a fixed-size, fast O(1) "have I seen this?" probe with a
//     tunable false-positive rate.
//
//  2. A per-publisher reputation table. For every indexer pubkey we
//     have ever queried, we record (hits_returned,
//     hits_downloaded_ok, hits_flagged_spam) and derive a single
//     score from those counters. Indexers below a configurable
//     threshold are demoted in the lookup fan-out (low-quality)
//     or skipped entirely (proven malicious).
//
// Both stores are independent of the rest of the codebase: the M5c
// commit wires them into the Layer-D lookup path, but a future M6
// could equally well consult them from the Layer-S sn_search
// inbound query handler.
//
// Files:
//
//   - bloom.go    : Bloom filter type with on-disk persistence.
//   - reputation.go : per-pubkey reputation tracker (M5b).
//   - filter.go     : helper that combines both into a single
//                     "is this hit good?" decision (M5c).
package reputation
