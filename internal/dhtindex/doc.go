// Package dhtindex implements SwartzNet's BEP-44 keyword publisher
// (Layer D, M4) and the matching lookup path.
//
// Layer D is the third and most ambitious of the three search layers
// described in docs/05-integration-design.md §4. Where Layer L
// (local Bleve, M2) is "search what you've already downloaded" and
// Layer S (peer-wire sn_search, M3) is "ask the peers you happen to
// be talking to right now," Layer D is "ask the entire mainline DHT,"
// using BEP-44 mutable items as a transport for keyword → infohash
// mappings.
//
// Each (publisher_pubkey, keyword) pair lives at a deterministic DHT
// target computed as SHA1(pubkey || keyword). The value is a small
// bencoded dict listing the infohashes that publisher claims for the
// keyword. Anyone who knows the publisher's pubkey and the keyword
// string can recompute the target and fetch the value with a
// standard BEP-44 get.
//
// This package is split across files by responsibility:
//
//   - tokenize.go    : torrent-name → []string keyword extraction
//   - schema.go      : the on-the-wire bencode schema for the value
//   - manifest.go    : per-keyword shard manifest persisted on disk
//   - dht.go         : thin wrapper around anacrolix/dht/v2/exts/getput
//   - publisher.go   : worker that drains a queue and publishes
//   - lookup.go      : GET fan-out across known indexer pubkeys
//
// M4 lands them in order across several commits. The publisher and
// the lookup path are independent of each other; either can ship
// before the other.
package dhtindex
