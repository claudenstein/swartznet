# Changelog

All notable changes to SwartzNet are documented here. The
format follows [Keep a Changelog][kac]; the project follows
[Semantic Versioning][semver] starting from v1.

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/spec/v2.0.0.html

## Unreleased

Targeting **v1.0.0** — first GA release. v0.2.0 adds the local
web UI and validates the BEP-44 publish path against the live
mainline DHT. v1.0.0 still wants real-world data for the
reputation prior weight and at least one second client
implementing `sn_search` (the BEP-1 requirement to take a
draft to Final).

### M9 — Per-hit source tracking + targeted flag

- New `internal/reputation.SourceTracker`: an LRU-bounded map of
  infohash → set of indexer pubkeys that returned that infohash
  in a Layer-D query. Always non-nil after `engine.New`; no
  on-disk persistence (it repopulates naturally as the user
  searches).
- `dhtindex.Lookup` now records source attributions after
  merging hits, and `httpapi.handleFlag` uses the tracker to
  demote only the indexers that actually returned the flagged
  infohash, falling back to "demote everyone we saw recently"
  if the tracker has no record.

### M10 — GUI download controls

- **M10a**: New `engine.TorrentSnapshot` plus
  `Engine.TorrentSnapshots`, `PauseTorrent`, `ResumeTorrent`,
  `RemoveTorrent`, `IsPaused`. The pause state is mirrored on
  the `Handle` because anacrolix's internal flag is private.
  `snapshotOf` carries a nil-Info guard so torrents that haven't
  fetched metadata yet do not crash the API.
- **M10b**: Four new HTTP endpoints — `GET /torrents`,
  `POST /torrents/{infohash}/pause`, `POST /torrents/{infohash}/resume`,
  `DELETE /torrents/{infohash}`. Bridged through a new
  `httpapi.TorrentController` interface and a small adapter in
  `cmd/swartznet/torrent_controller.go`.
- **M10c**: New "Downloads" tab in the web UI with per-torrent
  progress bars, status pills, and pause/resume/remove buttons.
  Polls `/torrents` every 2 s while the tab is active.

### M11 — F3 companion content-index torrents (in progress)

- **M11a**: New `internal/companion` package with the on-disk
  schema for SwartzNet's distributed content index. Top-level
  `CompanionIndex` carrying `TorrentRecord`/`FileRecord`/
  `ContentChunk` types, gzip+JSON `Encode`/`Decode` with a
  format-magic guard and a 1 GiB safety cap. Format constants:
  `FormatVersion=1`, `FormatFileName="swartznet-content-index-v1.json.gz"`.
- **M11b**: `companion.BuildFromIndex` walks the local Bleve
  index (via two new `indexer.AllTorrentDocs` /
  `indexer.ContentDocsForInfoHash` paginated MatchAll queries)
  and produces a CompanionIndex. `companion.WriteCompanionFiles`
  serialises it and wraps the bytes in a v1 .torrent metainfo
  with a 256 KiB piece length, written atomically to the
  publisher's companion directory.
- **M11c**: New `companion.Publisher` worker — every hour
  (configurable) it builds the index, seeds the wrapping torrent
  through the engine, and publishes a BEP-46-style mutable
  pointer at salt `_sn_content_index` whose value is the new
  infohash. Manual `RefreshNow` is throttled by `MinInterval`.
  `dhtindex.AnacrolixPutter.PutInfohashPointer` and
  `AnacrolixGetter.GetInfohashPointer` are the new BEP-44
  primitives. `engine.AddTorrentMetaInfo` lets the publisher
  hand the in-memory metainfo back to the engine for seeding,
  and `engine.PointerPutter` exposes the shared
  `*dhtindex.AnacrolixPutter`. `cmd_add.go` constructs and
  starts the companion publisher after `SetIndex` whenever the
  daemon has both an index and an identity. New
  `config.CompanionDir` (default `~/.local/share/swartznet/companion`)
  controls the on-disk artefacts directory.
- **M11d**: New `companion.Subscriber` and
  `companion.SubscriberWorker` — the read side of the F3 story.
  `Subscriber.Sync` resolves a publisher's BEP-46 pointer,
  fetches the underlying torrent, decodes the gzipped JSON, and
  ingests every record into the local Bleve index.
  `SubscriberWorker` runs the same pipeline against a follow
  list every hour. Narrow `PointerGetter`, `CompanionFetcher`,
  and `Ingester` interfaces keep the package decoupled from
  internal/engine and internal/dhtindex.
  `engine.AddInfoHash` adds a torrent by raw 20-byte infohash;
  `engine.FetchCompanionTorrent` orchestrates add → wait for
  metadata → wait for download → return the on-disk path, and
  satisfies `companion.CompanionFetcher`. `engine.PointerGetter`
  exposes the shared `*dhtindex.AnacrolixGetter`. New
  `config.CompanionFollowFile` (default
  `~/.local/share/swartznet/companion-follows.json`) is the JSON
  array of `{pubkey, label}` rows the subscriber follows on
  startup; the file is loaded by a tiny `cmd/swartznet/companion_follows.go`
  helper. `cmd_add.go` starts the subscriber worker after the
  publisher whenever the daemon has both an index and a DHT
  getter. Six new tests covering happy path, pointer/fetcher
  failures, partial-ingest failure, the worker lifecycle, and
  IngestReader. All pass under `-race`.
### M12 — v1.0.0 preparation

Everything below is work toward answering the six "open questions
that block v1" in `docs/05-integration-design.md` §13, plus
tactical post-v1 items from §12 that were already clear enough to
ship without research.

- **M12a — README status table refresh**: Backfilled M9, M10, and
  M11 into the top-of-README milestone table, removed the stale
  "Planned" rows for M2.3 / M3 / M4 / M5 / M6 that had been
  complete since earlier releases.
- **M12b — index-size measurement tooling**: New `indexer.Stats()`
  method + `GET /index/stats` endpoint reporting on-disk directory
  bytes, per-type document counts (torrents vs. content chunks),
  sum of every stored `ContentDoc.Text` byte, and the resulting
  inflation ratio. The Status tab in the web UI now shows these as
  part of the Local Index card, so anyone running the daemon for a
  day can produce the data that answers v1 open question #1 ("how
  big is Bleve's index per TB of indexed text"). `TestIndexStats`
  pins down every field against a seeded index.
- **M12c — dht-smoke concurrent-publish stress test**: Added
  `-stress N`, `-stress-concurrent`, and `-stress-timeout` flags
  to `cmd/dht-smoke`. After the single-put smoke, the stress phase
  publishes `N` distinct BEP-44 mutable items against the live
  mainline DHT with bounded concurrency and reports per-put
  latency (min / p50 / p95 / max), total success rate, wall-clock
  elapsed, post-run DHT routing stats, and a round-trip Get from
  one successful keyword. Answers v1 open question #2 ("how many
  concurrent BEP-44 publishes can anacrolix/dht/v2 sustain"). Fails
  the exit status only when every put fails — a partial failure is
  the interesting measurement.
- **M12e — search result snippet highlighting**: `SearchRequest`
  gains a `Highlight` bool; when true, Bleve's HTML highlighter
  runs on the `name` / `files` / `text` fields and returns
  matched-text fragments wrapped in `<mark>...</mark>`. Fragments
  flow through to `LocalHit.Fragments` on `POST /search` and are
  rendered in the web UI as a small indented snippet block under
  each hit. `TestSearchHighlight` covers both the nil-when-off
  case and the marked-fragment-when-on case. The CLI still omits
  `Highlight` (its output is plain text), so this is strictly a
  GUI enhancement.
- **M12d — multi-word + boolean query support**: Bleve's
  `QueryStringQuery` already supported `+required` / `-excluded` /
  `"phrase"` / `field:term` / `fuzzy~1` — this commit just pins
  those guarantees down behind `TestSearchQueryOperators` (8
  sub-cases covering each operator), rewrites the `Index.Search`
  docstring to enumerate the supported syntax, and adds a one-line
  hint under the web UI's search box so end-users can discover the
  advanced operators.

- **M11e**: GUI integration. New `httpapi.CompanionController`
  interface and four endpoints: `GET /companion` (status of
  publisher + every followed publisher), `POST /companion/refresh`
  (proxies to `Publisher.RefreshNow`, returns 429 on throttle),
  `POST /companion/follow {pubkey, label}` (adds to the follow
  list AND persists to disk), `POST /companion/unfollow {pubkey}`
  (removes + persists). The `cmd/swartznet/companion_controller.go`
  adapter bridges the running publisher and subscriber worker
  to the controller and owns the on-disk follow file
  (atomic-rename writes). New "Companion" tab in the web UI
  showing publisher status (last refresh, infohash, count), a
  manual refresh button, the follow form, and one card per
  followed publisher with last-sync stats and an unfollow
  button. Six new httpapi tests using a fake controller —
  status, refresh happy path, refresh-too-soon (429), follow,
  follow with bad pubkey (400), and unfollow.

## v0.2.0 — 2026-04-10

Second preview release. Adds a complete browser-based GUI on
top of the existing CLI + JSON API, validates the Layer-D
publish path against the live mainline DHT, and ships
release tooling so future cuts are one command.

### M8 — Local web UI

- **M8a+b**: HTML/CSS/JS embedded into the binary via go:embed
  and served from the existing httpapi daemon at `GET /` and
  `GET /static/*`. Four tabs (Search, Add torrent, Status,
  Sharing) using the same JSON endpoints the CLI uses. No build
  step, no JavaScript bundler, no external dependencies. Lives
  at `internal/httpapi/web/{embed.go, index.html,
  static/style.css, static/app.js}`. `/healthz` now reports the
  build version so the UI badge can show the running version.
- **M8c**: Three new HTTP endpoints to round out the UI's
  functionality:
  - `POST /torrent {uri}` adds a magnet via the new
    `httpapi.TorrentAdder` interface (`engine.AddMagnetURI`
    satisfies it). Includes a `recover()` guard so a malformed
    magnet returns a clean 400 instead of crashing the daemon.
  - `GET /capabilities` reports the current `sn_search`
    `share_local` / `file_hits` / `content_hits` / `publisher`
    flags from `swarmsearch.Protocol`.
  - `POST /capabilities` updates them with input clamping.

The web UI is **localhost-only by design** because it controls
the daemon and is fundamentally separate from the per-peer
search-result interfaces (sn_search peer wire, BEP-44 DHT)
which the user controls via capability flags and the
`--no-dht` flag at startup.

### Release tooling and validation

- **`scripts/build-release.sh`**: one-command cross-compile for
  linux/amd64+arm64, darwin/amd64+arm64, windows/amd64. Pure-Go,
  CGO-disabled, fully static binaries with stripped symbols and
  trimpath. Generates a SHA256SUMS file alongside.
- **`cmd/dht-smoke`**: one-shot live mainline DHT smoke test for
  the BEP-44 publisher path. Joins the real DHT, runs an
  AnacrolixPutter Put + AnacrolixGetter Get round trip with an
  ephemeral keypair so the user's real publisher identity is
  never touched. Run with `go run ./cmd/dht-smoke`.
- **Validation**: the `dht-smoke` target was run against the live
  mainline DHT on 2026-04-10. 25 good DHT nodes after bootstrap,
  Put accepted by 7 of 8 closest nodes in ~10s (1 timeout, normal
  network reality), Get round-tripped the signed payload back in
  ~7s with the synthetic Hit data unchanged. **The "BEP-44
  publish path not yet validated against the live mainline
  DHT" caveat from v0.1.0 is now retired.**

All nine packages pass under `go test -race ./...`.

## v0.1.0 — 2026-04-10

First numbered preview release. M1-M7 feature-complete in 26
commits. Five cross-platform release binaries (Linux x64+arm64,
macOS x64+arm64, Windows x64) attached to the GitHub Release at
<https://github.com/claudenstein/swartznet/releases/tag/v0.1.0>.

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
