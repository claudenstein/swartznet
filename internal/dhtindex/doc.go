// Package dhtindex is Layer D: the BEP-44 mutable-item keyword index. A node
// publishes its own torrents' name-keywords as signed mutable items at
// SHA1(pubkey||salt) targets (the write side, driven by Publisher), and
// resolves a query against a set of known indexer pubkeys (the read side,
// Lookup). The wire payload is the frozen contracts/dhtschema.KeywordValue; the
// package rides only standard BEP-44 (mutable put/get with salt), BEP-46
// (infohash pointer), and BEP-51 (sample_infohashes) — no new DHT verb,
// reserved bit, UDP port, or classifiable marker, so a vanilla client sees
// only ordinary mainline traffic.
//
// The RecordBackend seam (backend.go) is the swap point for the Slice-12
// Aggregate index: the legacy per-keyword CAS backend (legacyKeyword) is the
// v1 default, selected by config.LayerDMode=="legacy". Putter/Getter and the
// fail-closed checkPutStats guard live BENEATH the port, shared by every
// backend so the guard cannot drift; the manifest/throttle/eviction sit inside
// the backend as format-specific state, while the indexer-set / reputation /
// scoring orchestration sits above the port in Lookup.
package dhtindex
