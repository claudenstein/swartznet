# SwartzNet

A BitTorrent client with **built-in distributed text search** — search not just
torrent names, but the files inside your torrents and, over time, torrents
published by other peers on the network — while remaining fully backwards
compatible with vanilla BitTorrent (BEP-3/5/9/10/44 on the mainline DHT).

This repository is in early development. See [docs/](docs/) for the full
research and design documents that motivate the architecture.

## Status

| Milestone | Status | What works |
|---|---|---|
| **Research & design** | ✅ Complete | Five reports in `docs/` totalling ~4,400 lines. |
| **M1 — Go scaffold + engine smoke test** | ✅ Complete | Minimal CLI wraps `anacrolix/torrent`, adds a magnet link, downloads and seeds. Engine wrapper exposes the extension hooks M2/M3 depend on. |
| **M2.0 — Torrent-level metadata index (Layer L start)** | ✅ Complete | Bleve full-text index auto-populated on torrent add; `swartznet search <query>` works over torrent names, file paths, and trackers. |
| **M2.1 — Piece-to-file completion tracker** | ✅ Complete | `FileCompleteEvent` stream synthesised from the piece-state subscription; handles resume (seeds pending counts from current piece state). Unit-tested on single-file, multi-file, and zero-length-file layouts. |
| **M2.2a — Extractor framework + plaintext extractor + ingestion pipeline** | ✅ Complete | Bleve schema gains a `content` document type; `Pipeline` worker consumes `FileCompleteEvent`, dispatches to the extractor registry, and writes content docs. Plaintext extractor handles .txt/.md/.html/.json/source code. `swartznet search` now returns both torrent-level and content-level hits, clearly labelled. |
| **M2.2b — Subtitle-aware extractor** | ✅ Complete | SRT/VTT parser strips timestamps, cue numbers, HTML/ASS markup, and WebVTT headers/NOTE blocks; only dialog text is indexed. Dialog is often the single most valuable text inside a movie/TV torrent. |
| **M2.2c — Chunker for large files** | ✅ Complete | Plaintext extractions larger than ~12 KiB are split into ~10 KiB chunks at paragraph boundaries (falling back to line boundaries, then arbitrary positions for minified inputs). Each chunk carries its source-byte offset for future snippet-highlight UI. |
| **M2.3 — PDF extractor** | ✅ Complete | Pure-Go PDF text extraction via `ledongthuc/pdf` (MIT-licensed fork of rsc/pdf). Buffered decode with a 256 MiB ceiling; panic-recovery around the parser; empty-text PDFs (scanned image-only) produce no ContentDocs rather than empty noise. |
| **M3a — sn_search LTEP registration + capability discovery** | ✅ Complete | New `internal/swarmsearch` package owns a `Protocol` that registers `sn_search` in every outbound LTEP handshake and observes remote handshakes to detect which peers speak the extension. Per-peer state tracked with their chosen extension id. No message handling yet. |
| **M3b — sn_search wire format + inbound query handler** | ✅ Complete | Bencoded query/result/reject messages (`internal/swarmsearch/wire.go`), plus a handler that answers inbound queries from the local Bleve index via an `indexerSearcher` adapter. Torrent-level and content-level hits are merged per infohash on the wire. |
| **M3c — Outbound Query fan-out + result aggregation** | ✅ Complete | `Protocol.Query()` generates a monotonic txid, fans the query out to every known search-capable peer via the `swarmSender`, collects responses on a per-query channel, and merges by infohash with per-peer source attribution. Honors context cancellation + per-query timeout. |
| **M3d — CLI `--swarm` flag + local HTTP API** | ✅ Complete | `swartznet add` starts a loopback-only HTTP API on `localhost:7654`; `swartznet search --swarm` POSTs to it to run combined local + swarm search against the running daemon. JSON and text output modes both supported. |
| **M4 — BEP-44 keyword publisher (Layer D)** | ✅ Complete | Persistent ed25519 publisher identity, keyword tokenizer, BEP-44 mutable-item put/get wrapper, publisher worker with on-disk shard manifest, parallel lookup fan-out across known indexer pubkeys, `swartznet search --dht`, and `swartznet status`. |
| **M5 — Spam resistance** | ✅ Complete | Persistent Bloom filter of known-good infohashes (1M items @ 1% FP, ~1.2 MB), Bayesian-smoothed per-pubkey reputation tracker, lookup auto-skips low-reputation indexers, Bloom-hit results boost to the top, auto-confirm on download completion, `swartznet flag/confirm` CLI commands. |
| **M6 — EPUB / DOCX / ODT extractors** | ✅ Complete | Three new binary-format text extractors. EPUB iterates XHTML chapters and strips HTML via golang.org/x/net/html. DOCX walks word/document.xml's `<w:t>` elements via stdlib encoding/xml. ODT does the same for content.xml's `<text:p>` elements while skipping `<office:automatic-styles>` noise. All zero-cgo, all use the existing chunker. |
| **M7 — Documentation polish for v1** | ✅ Complete | Two draft BEP specs (`docs/06-bep-sn_search-draft.md` and `docs/07-bep-dht-keyword-index-draft.md`), an operations guide (`docs/08-operations.md`), and a [CHANGELOG](CHANGELOG.md) covering every milestone. v1.0.0 release pending real-world swarm testing. |
| **M8 — Local web UI** | ✅ Complete | Static HTML/CSS/JS embedded into the binary via `go:embed` and served by the existing httpapi daemon. Browse to `http://localhost:7654/` to get a four-tab UI: Search (across all three layers), Add torrent, Status, and Sharing (with the per-instance `sn_search` capability toggles wired through new `GET`/`POST /capabilities` endpoints). Localhost-only by design — the GUI controls the daemon and is fundamentally separate from the per-peer search-result interfaces (`sn_search`, BEP-44). |
| **M9 — Per-hit source tracking + targeted flag** | ✅ Complete | LRU-bounded `reputation.SourceTracker` records which indexer pubkey returned which infohash during a Layer-D query; `POST /flag` uses that attribution to demote only the indexers actually responsible for a flagged hit instead of everyone returned in the last query. |
| **M10 — GUI download controls** | ✅ Complete | New `engine.TorrentSnapshots` + pause/resume/remove APIs, four new HTTP endpoints (`GET /torrents`, `POST /torrents/{ih}/{pause,resume}`, `DELETE /torrents/{ih}`), and a Downloads tab in the web UI with live progress bars, status pills, and per-torrent controls polling every 2 s. |
| **M11 — F3 companion content-index torrents** | ✅ Complete | SwartzNet's distributed content-search story. The daemon periodically serialises its local Bleve index to a gzipped JSON document, wraps it in a v1 `.torrent` metainfo, seeds it, and publishes a BEP-46-style mutable pointer at salt `_sn_content_index` (`companion.Publisher`). Subscribers follow publishers by their ed25519 pubkey; the `companion.SubscriberWorker` resolves each pointer, downloads the torrent, decodes the payload, and merges the records into the local index. New Companion tab in the web UI exposes the whole pipeline — publisher status, manual refresh, and an on-disk follow list managed through `/companion/{follow,unfollow,refresh}`. Closes the "distributed search" promise of the project's tagline without any new network protocol beyond the existing BitTorrent + BEP-44 stack. |

The full roadmap and per-milestone rationale is in
[`docs/05-integration-design.md`](docs/05-integration-design.md) §12.
See [CHANGELOG.md](CHANGELOG.md) for the per-commit release notes.

## Design in one paragraph

SwartzNet embeds [`anacrolix/torrent`](https://github.com/anacrolix/torrent)
(Go, MPL-2.0) as its BitTorrent engine. We hook its piece-completion callback
to feed downloaded file contents into a local [Bleve](https://github.com/blevesearch/bleve)
full-text index (**Layer L**). We register a custom `sn_search` extension via
the BEP-10 Extension Protocol so that two SwartzNet peers already talking
BitTorrent can ask each other keyword queries on the same TCP connection
(**Layer S**). We publish keyword → infohash mappings on the existing mainline
DHT using BEP-44 mutable items, keyed by per-publisher ed25519 pubkey with the
keyword as salt (**Layer D**). Nothing we add requires a new reserved bit, a
new DHT verb, or a second UDP port — vanilla clients see a normal peer speaking
BEP-3/5/9/10/44.

## Documentation

### Research (completed)

- [`docs/01-torrent-clients-comparison.md`](docs/01-torrent-clients-comparison.md) —
  libtorrent, Transmission, anacrolix/torrent, rqbit, WebTorrent compared on
  extension API, piece-verify hooks, DHT extensibility, and license. Winner:
  anacrolix/torrent.
- [`docs/02-tribler-deep-dive.md`](docs/02-tribler-deep-dive.md) — Tribler is
  the closest prior art (BitTorrent + keyword search since 2006). Its search
  architecture, limitations, and what we reuse vs. replace.
- [`docs/03-p2p-search-protocols.md`](docs/03-p2p-search-protocols.md) — survey
  of aMule/Kad, GNUnet, Gnutella, RetroShare, YaCy, Freenet. Identifies
  aMule's keyword-hash DHT as the most directly applicable pattern.
- [`docs/04-bep-extension-points.md`](docs/04-bep-extension-points.md) — every
  BEP relevant to our design, with byte-level walkthroughs of the LTEP
  handshake and BEP-44 mutable-item publish/get flow.
- [`docs/05-integration-design.md`](docs/05-integration-design.md) — the
  synthesis: three-layer architecture, the `sn_search` wire format spec,
  ingestion pipeline, threat model, backwards-compatibility test matrix,
  ordered roadmap.

### Specs and operations (M7)

- [`docs/06-bep-sn_search-draft.md`](docs/06-bep-sn_search-draft.md) —
  draft BEP-style specification for the `sn_search` peer-wire extension.
  Anyone reimplementing the extension in another client should be able
  to read this document end-to-end and produce a wire-compatible peer.
- [`docs/07-bep-dht-keyword-index-draft.md`](docs/07-bep-dht-keyword-index-draft.md) —
  matching draft spec for the Layer-D BEP-44 mutable-item keyword index.
- [`docs/08-operations.md`](docs/08-operations.md) — file layout, what
  to back up, useful commands, and a troubleshooting guide.
- [`CHANGELOG.md`](CHANGELOG.md) — milestone-by-milestone change log
  for every commit on `main`.

## Building

Requires Go 1.22 or later. Go 1.24+ is recommended.

```bash
go build ./cmd/swartznet
./swartznet --help
```

## Running

```bash
# Add and download a torrent from a magnet link, seed on completion.
# The torrent's metadata (name, files, trackers) is automatically written
# to the local Bleve index as soon as it arrives from the swarm, and
# text content from completed files (PDFs, subtitles, source code, etc.)
# is extracted and indexed as each file finishes.
#
# While this daemon is running it also exposes an HTTP API on
# localhost:7654 that the `search --swarm` subcommand uses to issue
# distributed swarm-wide queries over sn_search. Open the same URL
# in any browser to use the bundled web UI:
#
#   http://localhost:7654/
./swartznet add "magnet:?xt=urn:btih:..."

# Search the local index only (works without a running daemon).
# Supports Bleve's query-string syntax:
#     ubuntu              -- bag-of-words match
#     "ubuntu 24.04"      -- phrase match
#     name:debian         -- fielded query
#     ubuntu -server      -- boolean exclusion
./swartznet search ubuntu
./swartznet search --json --limit 50 "ubuntu 24.04"

# Combined local + swarm search. Requires a running `swartznet add`
# daemon with peers connected; asks every peer that advertises the
# sn_search BEP-10 extension and merges the results.
./swartznet search --swarm ubuntu

# Combined local + swarm + DHT search. Adds a parallel BEP-44 mutable-item
# lookup against every known indexer pubkey alongside the swarm path.
./swartznet search --swarm --dht ubuntu

# Or use the browser-based web UI for everything above:
#   http://localhost:7654/
# (the same daemon serves both the JSON API and the embedded UI)

# Snapshot of the running daemon's index, peer set, DHT publisher,
# Bloom filter population, and per-pubkey reputation table.
./swartznet status

# Mark an infohash as known-good. Boosts it in future DHT lookups.
# (Auto-confirm runs on every successful download — usually you do
# not need to call this manually.)
./swartznet confirm <40-char-hex-infohash>

# Mark an infohash as spam. Lowers the reputation of every indexer
# pubkey that has been associated with it.
./swartznet flag <40-char-hex-infohash>

# Print the version.
./swartznet version
```

Defaults:
- Downloaded data:  `~/.local/share/swartznet/data`
- Local search index: `~/.local/share/swartznet/index`
- Both honour `$XDG_DATA_HOME`; both can be overridden with `--data-dir` / `--index-dir`.

More commands will be added as milestones land.

## Backwards compatibility

A vanilla BitTorrent client connecting to SwartzNet sees a standard BEP-3
handshake with the LTEP bit set, a standard LTEP handshake advertising
`ut_metadata`, `ut_pex`, and (for SwartzNet peers only) `sn_search`, and
standard BEP-5 DHT traffic. The `sn_search` extension is strictly opt-in: if
the other side doesn't advertise it in their LTEP `m` dictionary, we never
send them a search message. Everything we store in the DHT is a standard
BEP-44 mutable item — vanilla nodes serve it under the same rules they serve
any other BEP-44 item.

See [`docs/05-integration-design.md`](docs/05-integration-design.md) §8 for the
explicit backwards-compatibility test matrix.

## Explicit non-goals

- **Anonymity / traffic analysis resistance.** SwartzNet is not Tor. Users who
  need network-level anonymity should layer a VPN or Tor transport under their
  BitTorrent traffic.
- **Legal content filtering.** SwartzNet does not enforce copyright or
  jurisdictional content restrictions. Users are responsible for what they
  search and download, exactly as with every existing BitTorrent client.
- **Global content-level distributed index.** The local-first content index
  (Layer L) makes files you already have searchable on your machine. Sharing
  content-level indexes across the network is an explicit v2 feature, not v1.

See [`docs/05-integration-design.md`](docs/05-integration-design.md) §9 for the
full threat model.

## Contributing

SwartzNet is in early development and the internal APIs are changing rapidly.
Please open an issue before sending large patches so we can align on the
approach. The roadmap in `docs/05-integration-design.md` §12 is authoritative
for what's in scope for each milestone.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The upstream dependency `anacrolix/torrent` is licensed MPL 2.0; any
modifications we make inside that library are published under MPL 2.0. Our own
code in this repository is Apache 2.0.
