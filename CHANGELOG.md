# Changelog

All notable changes to SwartzNet are documented here. The
format follows [Keep a Changelog][kac]; the project follows
[Semantic Versioning][semver] starting from v1.

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/spec/v2.0.0.html

## Unreleased

Targeting **v1.0.0** — first numbered release. The roadmap
through M7 is feature-complete on `main`. v1 will land once
operational testing in real swarms catches any remaining
issues.

## M7 — Documentation polish

- **M7a**: Draft BEP-style spec for the `sn_search` peer-wire
  extension (`docs/06-bep-sn_search-draft.md`).
- **M7b**: Draft BEP-style spec for the BEP-44 keyword index
  (`docs/07-bep-dht-keyword-index-draft.md`).
- **M7c**: Operations guide
  (`docs/08-operations.md`), this CHANGELOG, and README
  polish.

## M6 — Office-document extractors

- **M6a**: EPUB extractor with shared HTML-text helper
  (`internal/indexer/extractors/htmltext.go`,
  `epub.go`). Pure stdlib + `golang.org/x/net/html`.
- **M6b**: DOCX and ODT extractors via stdlib
  `archive/zip` + `encoding/xml`.

## M5 — Spam resistance

- **M5a**: `internal/reputation/bloom.go` — pure-Go Bloom
  filter (1M items @ 1% FP, ~1.2 MB) with custom on-disk
  format and double-hashing trick.
- **M5b**: `internal/reputation/reputation.go` —
  per-pubkey reputation tracker with Bayesian-smoothed
  scoring.
- **M5c**: Lookup path now consults both. Low-reputation
  indexers are skipped before any DHT traversal; Bloom-hit
  results sort to the top with a +0.25 score boost.
- **M5d**: `swartznet flag` and `swartznet confirm`
  CLI commands; auto-confirm on torrent download
  completion via `Torrent.Complete().On()`.

## M4 — BEP-44 keyword publisher (Layer D)

- **M4a**: `internal/identity` — persistent ed25519
  publisher keypair with 0600 permissions enforcement.
- **M4b**: `internal/dhtindex/tokenize.go` — torrent name
  → keyword tokenisation with stop-word and extension
  filtering.
- **M4c**: `internal/dhtindex/{schema,dht}.go` — BEP-44
  mutable-item put/get wrapper around
  `anacrolix/dht/v2/exts/getput`, plus an in-memory
  test double. Includes a race fix in `httpapi`.
- **M4d**: `internal/dhtindex/{manifest,publisher}.go` —
  long-running publisher worker with on-disk shard
  manifest and 1h refresh ticker. Engine wiring loads the
  identity, builds the publisher, and feeds new torrents
  to it on add.
- **M4e**: `internal/dhtindex/lookup.go` — parallel BEP-44
  get fan-out across known indexer pubkeys, merging by
  infohash with per-source attribution.
- **M4f**: `swartznet search --dht` CLI flag,
  `swartznet status` command, HTTP API plumbing for both.

## M3 — Peer-wire `sn_search` extension (Layer S)

- **M3a**: `internal/swarmsearch/protocol.go` — registers
  `sn_search` in the LTEP `m` dictionary via the
  `PeerConnAdded` callback, observes remote handshakes
  via `ReadExtendedHandshake` to discover capable peers.
- **M3b**: `internal/swarmsearch/{wire,handler}.go` —
  bencoded query/result/reject messages, inbound query
  handler that runs against the local Bleve index via an
  adapter.
- **M3c**: `internal/swarmsearch/query.go` — outbound
  `Query()` fan-out with txid routing and merge-by-infohash.
- **M3d**: `internal/httpapi/server.go` — loopback HTTP
  daemon with POST /search and GET /healthz endpoints;
  `swartznet search --swarm` CLI flag.

## M2 — Local Bleve index (Layer L)

- **M2.0**: `internal/indexer/{indexer,schema}.go` — Bleve
  full-text index over torrent metadata; `swartznet
  search <query>` command.
- **M2.1**: `internal/engine/{filemap,file_tracker}.go` —
  piece-to-file completion tracker that synthesises
  `FileCompleteEvent`s from the piece-state subscription.
- **M2.2a**: `internal/indexer/{content,pipeline}.go`
  + `internal/indexer/extractors/{extractor,plaintext}.go`
  — extractor framework, plain-text extractor, ingestion
  pipeline that consumes file-complete events and writes
  content docs.
- **M2.2b**: SRT and WebVTT subtitle extractor
  (`internal/indexer/extractors/subtitle.go`).
- **M2.2c**: Paragraph-boundary chunker
  (`internal/indexer/extractors/chunker.go`).
- **M2.3**: PDF extractor via `github.com/ledongthuc/pdf`
  (`internal/indexer/extractors/pdf.go`).

## M1 — Foundation

- **M1**: `cmd/swartznet` + `internal/{config,engine}` —
  minimal CLI built on `anacrolix/torrent` v1.61.0 with
  the extension hooks the later milestones depend on.

## Pre-history

Five research / design documents in `docs/01-…05-` covering
the comparison of torrent-client implementations, prior art
for distributed search, the relevant BEPs, and the
SwartzNet integration design.
