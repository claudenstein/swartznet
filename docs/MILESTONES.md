# Milestone history

This document records every milestone that shaped the current
codebase. It exists to preserve the "why" behind past decisions
for anyone digging through the git history. For release-level
change tracking, see [CHANGELOG.md](../CHANGELOG.md). For the
up-to-date architecture, see
[`05-integration-design.md`](05-integration-design.md).

| Milestone | What landed |
|---|---|
| **Research & design** | Five reports in `docs/` totalling ~4,400 lines. |
| **M1 — Go scaffold + engine smoke test** | Minimal CLI wraps `anacrolix/torrent`, adds a magnet link, downloads and seeds. Engine wrapper exposes the extension hooks M2/M3 depend on. |
| **M2.0 — Torrent-level metadata index (Layer L start)** | Bleve full-text index auto-populated on torrent add; `swartznet search <query>` works over torrent names, file paths, and trackers. |
| **M2.1 — Piece-to-file completion tracker** | `FileCompleteEvent` stream synthesised from the piece-state subscription; handles resume. Unit-tested on single-file, multi-file, and zero-length-file layouts. |
| **M2.2a — Extractor framework + plaintext + pipeline** | Bleve schema gains a `content` document type; `Pipeline` worker consumes `FileCompleteEvent`, dispatches to the extractor registry, writes content docs. Plaintext handles .txt/.md/.html/.json/source code. |
| **M2.2b — Subtitle-aware extractor** | SRT/VTT parser strips timestamps, cue numbers, HTML/ASS markup, WebVTT headers/NOTE blocks; only dialog text is indexed. |
| **M2.2c — Chunker for large files** | Extractions larger than ~12 KiB are split into ~10 KiB chunks at paragraph boundaries (falling back to line boundaries, then arbitrary positions). |
| **M2.3 — PDF extractor** | Pure-Go PDF text extraction via `ledongthuc/pdf` (BSD-3-Clause fork of rsc/pdf). Buffered decode with a 256 MiB ceiling; panic-recovery; empty-text PDFs produce no ContentDocs. |
| **M3a — sn_search LTEP registration + capability discovery** | `internal/swarmsearch.Protocol` registers `sn_search` in every outbound LTEP handshake and observes remote handshakes to detect capable peers. |
| **M3b — sn_search wire format + inbound query handler** | Bencoded query/result/reject messages; handler answers inbound queries from the local Bleve index. Torrent-level and content-level hits merged per infohash on the wire. |
| **M3c — Outbound Query fan-out + result aggregation** | `Protocol.Query()` fans to every search-capable peer, collects responses on a per-query channel, merges by infohash with per-peer source attribution. |
| **M3d — CLI `--swarm` flag + local HTTP API** | `swartznet add` starts a loopback HTTP API on `localhost:7654`; `swartznet search --swarm` talks to it for distributed queries. |
| **M4 — BEP-44 keyword publisher (Layer D)** | Persistent ed25519 identity, keyword tokenizer, BEP-44 mutable-item put/get, publisher worker, parallel lookup fan-out, `swartznet search --dht`, `swartznet status`. |
| **M5 — Spam resistance** | Persistent Bloom filter of known-good infohashes (1M @ 1% FP), Bayesian-smoothed per-pubkey reputation tracker, Bloom-hit boost, auto-confirm on completion, `swartznet flag/confirm`. |
| **M6 — EPUB / DOCX / ODT extractors** | Three binary-format extractors. All zero-cgo via `golang.org/x/net/html` and `encoding/xml`. |
| **M7 — Documentation polish for v1** | Two draft BEP specs (`06-bep-sn_search-draft.md`, `07-bep-dht-keyword-index-draft.md`), operations guide (`08-operations.md`), CHANGELOG. |
| **M8 — Local web UI** | HTML/CSS/JS embedded via `go:embed`, served by the existing httpapi daemon at `http://localhost:7654/`. Four tabs: Search, Add, Status, Sharing. |
| **M9 — Per-hit source tracking + targeted flag** | `reputation.SourceTracker` records which indexer pubkey returned which infohash; `POST /flag` demotes only the indexers actually responsible. |
| **M10 — GUI download controls** | `engine.TorrentSnapshots` + pause/resume/remove, four HTTP endpoints, Downloads tab in the web UI with live progress + controls. |
| **M11 — F3 companion content-index torrents** | The daemon serialises its local Bleve index to a gzipped JSON document, wraps it in a `.torrent`, seeds it, and publishes a BEP-46 mutable pointer. Subscribers follow publishers by ed25519 pubkey; the worker resolves each pointer, downloads, decodes, and merges. Companion tab in the web UI manages the pipeline. |
| **v0.3.0 G0 — `internal/daemon/` extraction** | Engine+indexer+companion+httpapi wiring extracted from `cmd_add.go` into a shared `Daemon` struct used by both CLI and GUI. |
| **v0.3.0 G1–G6 — Native Fyne GUI** | `cmd/swartznet-gui`: cross-platform desktop app with five tabs (Downloads / Search / Status / Companion / Settings), system tray with minimise-to-tray and download-complete notifications, dark theme. |
| **v0.3.0 G7 — GUI build script** | `scripts/build-gui.sh` for native CGo build; `fyne-cross` documented for cross-platform targets. |
| **v0.3.0 G8 — Per-torrent indexing opt-out** | `Engine.SetTorrentIndexing(ih, enabled)`. GUI checkbox in Add Magnet, "Indexed" column, "Toggle Index" toolbar. HTTP: `POST /torrents/{ih}/indexing`. |
| **v0.3.0 G9 — Create Torrent** | `Engine.CreateTorrent`, `CreateTorrentFile`. GUI modal walks through root/name/piece-length/trackers/webseeds/comment/private/output + optional immediate seeding. |
| **v0.3.0 G10 — Documentation refresh** | New three-frontend architecture diagram, new sections on indexing control and torrent creation. |
| **Post-v0.3.0 — File selection** | `Engine.TorrentFiles` + `SetFilePriority` (none/normal/high). New `autoDownload` goroutine flips files to Normal via `File.SetPriority` so File.Priority surfaces in the UI. GUI "Files..." modal with per-file progress + priority dropdowns + bulk Select/Deselect. HTTP: `GET/POST /torrents/{ih}/files/...`. |
| **Post-v0.3.0 — Bandwidth rate limiting** | Engine installs mutable `*rate.Limiter` on `UploadRateLimiter`/`DownloadRateLimiter`. `Set/Get{Upload,Download}LimitBytesPerSec`. GUI Settings "Bandwidth Limits" card. Zero = unlimited. |
| **Post-v0.3.0 — CLI parity** | `swartznet create`, `swartznet index on\|off`, `swartznet files`. |
| **Post-v0.3.0 — Right-click context menu** | Fyne `SecondaryTappable` wrapper around the Downloads table. Context menu: Files / Pause|Resume / Remove / Toggle Index / Copy magnet / Copy infohash. |
| **Post-v0.3.0 — Queue management** | `Engine.MaxActiveDownloads()` / `SetMaxActiveDownloads`. New `Handle.queued` state surfaced in `TorrentSnapshot.Queued` and Status. Hooks in pause/remove/completion to promote the next queued torrent. |
| **v0.4.0 — Signed `.torrent` files** | New `internal/signing` package adds optional `snet.pubkey` (32 B) + `snet.sig` (64 B) top-level fields to the metainfo dict, signing `"SN-TORRENT-V1\|" \|\| infohash` with the node's ed25519 identity. `swartznet create --sign`; GUI Create Torrent dialog gains "Sign with my ed25519 identity" checkbox. Three new content extractors (FB2, PPTX, ODP) — total 11. Wire-compatible (vanilla clients ignore the new keys). See `docs/11-signing-protocol.md` for the formal spec. |
| **v0.5.0 — Publisher trust list** | New `internal/trust` package: JSON-persisted whitelist of trusted ed25519 pubkeys (`~/.local/share/swartznet/trust.json`). Trusted-publisher torrents auto-confirm to the known-good Bloom filter the moment metadata arrives. CLI: `swartznet trust list/add/remove`. GUI Downloads gets a Signed column (★ trusted, ✓ signed, — unsigned), right-click "Verify signature..." and "Trust this publisher". HTTP `GET /torrents` items gain `trusted_publisher`. Three new metadata extractors (MOBI, ID3, EXIF) — total 14. |
| **v0.6.0 — Media metadata + web UI parity** | Four new metadata extractors (FLAC, OGG Vorbis/Opus, MKV/WebM, MP4/M4A/M4B/M4V) — total 18. Web UI Downloads tab gains transfer-speed display, signed-publisher badges, per-row Files / index-toggle buttons, and a Files modal with per-file priority controls. New endpoints `GET/POST /config/rate-limit` and `GET/POST /config/queue` back new Bandwidth Limits + Queue fieldsets in the Settings tab. |
| **v0.6.1 — Web UI status polish** | New "Torrents" card on the Status tab (totals + status breakdown + aggregate throughput + signed/trusted counts). Keyboard shortcut `/` (or `Ctrl/Cmd+K`) jumps to Search and focuses the query input. |
| **v0.7.0 — Search by publisher** | Bleve schema bumps v2 → v3 to persist `signed_by` on every TorrentDoc. New `SearchRequest.SignedBy` filter conjuncts a term match on the `signed_by` field. CLI `swartznet search --signed-by <pubkey>`; HTTP `POST /search {signed_by}`; web UI gains a publisher input and clickable badges that pivot the search. Native GUI shows `✓ signed by <prefix>` on local hits. |
