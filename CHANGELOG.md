# Changelog

All notable changes to SwartzNet are documented here. The
format follows [Keep a Changelog][kac]; the project follows
[Semantic Versioning][semver] starting from v1.

[kac]: https://keepachangelog.com/en/1.1.0/
[semver]: https://semver.org/spec/v2.0.0.html

## Rebuild (in progress — Phase 4)

The tree is being rebuilt from scratch against `SPEC.md` /
`ARCHITECTURE.md` / `PLAN.md`; the legacy implementation lives on the
`legacy-snapshot` branch. Entries here track rebuild slices; everything
below "Unreleased" describes the legacy line.

### Test environment + CLI fixes (2026-07-19)

A unified test harness (`scripts/run-all-tests.sh`) now runs the whole suite from
one entry point — both binaries, the race unit set, every per-slice DoD, the
web-client DoD, a new whole-CLI end-to-end (`scripts/e2e-cli.sh`), and the
CI-mirror gate. Building and running it caught three real bugs, now fixed:

- **CLI arg ordering.** `create`, `files`, `confirm`, and `flag` now accept their
  positional argument before or after flags (e.g. `swartznet files <ih>
  --api-addr X` and `swartznet create ./dir -o out.torrent`), matching what their
  usage strings show — previously the flag was silently ignored or the command
  errored.
- **`search` with a running daemon.** A plain `search <query>` no longer fails
  with "index locked" when a daemon is running; it transparently falls back to a
  local search routed through the daemon, so it works with or without one.
- **Companion status codes.** `follow`/`unfollow`/`refresh` now return HTTP 503
  (feature unavailable) instead of 500/429 when the companion subsystem is not
  wired (e.g. under `--no-dht`), so clients can disable the control rather than
  show a spurious error.

### Whitepaper (2026-07-19)

- Added `docs/whitepaper.md` — a concise (~2.5k words), Bitcoin-whitepaper-styled
  paper (abstract, numbered sections, conclusion, references) describing
  SwartzNet's architecture and innovations: mainline-invisible search over
  existing BEPs, the three isolated search layers (local / peer-wire / DHT),
  signed identity and records, RIBLT set reconciliation, the signed Aggregate
  B-tree + PPMI pointer, deny-by-default spam resistance, and the capability mask.

### Web client — full embedded SPA (2026-07-19)

The embedded web UI (served by the daemon at `/`) grows from a placeholder into a
**fully functional** single-page app at feature parity with the CLI and native GUI.

- Vanilla JS, native ES modules, **no build step / framework / CDN** — served via
  the existing `go:embed` contract. Five tabs (Downloads, Search, Status,
  Companion, Settings) matching the GUI; only the visible tab polls.
- **Downloads:** live torrent list with progress/status/peers/rates, add-magnet,
  pause/resume/remove (files on disk always kept; optional index-forget),
  indexing toggle, and a per-file priority drawer.
- **Search:** the three search layers (Local / Swarm / DHT) render as **three
  strictly-separate result groups** — never merged, sorted, or deduped — each with
  its own counters and score type; per-hit Confirm/Flag through the shared spam
  path; highlight snippets are safely escaped.
- **Status / Companion / Settings:** node health with honest degraded blocks
  (disabled subsystems shown as such), companion follow/unfollow/refresh, and
  bandwidth/queue/sharing settings with merge-PATCH saves (only changed fields
  sent; the clamped response is authoritative).
- Security model is the daemon's existing loopback bind + CSRF guard; the client
  sends no auth token and works same-origin. Verified by `scripts/smoke-web.sh`
  (21 live checks incl. the cross-origin 403 guard) and a Go embed-manifest test.

### Slice 13 — Hardening: CI merge gate + doc/license reconciliation (2026-07-19)

- The CI merge gate (`gofmt -s`, `go vet`, `go mod tidy`, `go test -race` with
  the Fyne build deps, timing-sensitive scenarios excluded) is confirmed to run
  the full deterministic wire-compat suite — all `contracts/*` golden vectors,
  future-service-bit tolerance, and reject-code-2 — and now passes green (the
  pass caught and fixed real formatting/vet breakers).
- Docs reconciled with code: `docs/07` now documents oldest-hit eviction (the
  shipping oversize handling) with DHT sharding as reserved scaffolding, not a
  requirement; `ledongthuc/pdf` is correctly attributed BSD-3-Clause.
- `cmd/dht-smoke` (live-DHT smoke tool) is rebuilt with a corrected exit
  contract: an all-failed `-stress` phase is now a hard failure (exit 1) instead
  of a swallowed warning, so a dead DHT path no longer reports PASS.

### Slice 12 — Aggregate index: frozen contracts + offline tooling (2026-07-19)

The signed SNAGG B-tree Aggregate index format and its DHT pointer are frozen,
and the offline builder/inspector/query tooling ships.

- New `contracts/snagg`: the byte-exact signed B-tree format (6-byte magic,
  bencoded records, MIN-KEY separators, 162-byte signed trailer, deterministic
  build, SHA-256 record-stream fingerprint, hostile-tree-guarded prefix query).
- New `contracts/dhtschema.PPMIValue` + `PPMISalt = SHA256("snet.index")`: the
  BEP-44 pointer to a publisher's merged index, with the tree fingerprint as its
  commit. Both contracts carry frozen golden vectors.
- New `swartznet aggregate build|inspect|find`: offline sign + pack a JSONL
  record set into a signed SNAGG file (mode 0644; `--pow-bits` refused above
  40), inspect its trailer (integrity gate), and prefix-query it (`--verify`
  re-derives the fingerprint).

The live aggregatePPMI/composite backends and DHT distribution are wired (see
below); admission seeds and the crawler remain the opt-in tail. All of it is off
by default — the ship default stays `LayerDMode=legacy`.

- **Aggregate DHT distribution (cross-publisher discovery).** In
  `aggregatePPMI`/`composite` mode a rebuilt SNAGG tree is now seeded as a
  companion torrent and advertised by a signed PPMI pointer on each refresh, and
  a lookup for another publisher resolves their pointer → fetches their tree →
  verifies it against the pointer's commit before returning hits. An unreachable
  or unknown publisher degrades to no hits — never a query error — so one
  offline publisher can't fail a search. Still off by default (`legacy`).
- **`swartznet crawl` — bounded BEP-51 DHT crawler.** A new ops command performs
  a bounded breadth-first crawl of the mainline DHT (over the standard
  `sample_infohashes` verb — no new verb/bit/port), sampling infohashes from the
  nodes it reaches and expanding its frontier from their neighbours. Bounded by
  `--workers`, `--max-infohashes`, `--max-nodes`, per-sample `--timeout-ms`, and
  an overall `--duration-ms`; `--json` for structured output. It only discovers
  and prints infohashes — it never fetches, indexes, or downloads. Exits non-zero
  if the crawl reaches no node (dead network / all seeds unreachable).

### Slice 11 — Native Fyne GUI (2026-07-19)

The first graphical build: `swartznet-gui` presents Downloads, Search, Status,
Companion, and Settings over the SAME daemon the CLI and web UI use — pure
presentation, no independent logic.

- New `internal/gui` (Fyne) + `cmd/swartznet-gui`. The Search tab renders
  per-layer Local / Swarm / DHT result cards from the shared search fan-out and
  never merges them; Confirm/Flag on a hit route through the daemon's shared
  spam path. The Downloads tab shows live download %/status and drives add /
  pause / resume / remove through the engine.
- The search fan-out is unified behind a new `Daemon.Search` that both the HTTP
  API and the GUI call, so their reconciliation can never drift.
- The About dialog now states Apache-2.0 for first-party code (correcting the
  legacy "MIT" claim) and notes the MPL-2.0 anacrolix engine dependency; the
  version and license come from one build-stamped source.
- Build with `./scripts/build-gui.sh dev` (requires CGo for Fyne/OpenGL).

### Slice 10 — Companion content-index publish/subscribe (2026-07-19)

A node now publishes a compact companion content-index (a gzip-JSON snapshot of
its indexed torrents + extracted text) as a single-file torrent advertised by a
BEP-46 pointer, and a follower imports a followed publisher's index into its own
local search — verified end-to-end between two real engines over BitTorrent.

- New `internal/companion`: the `CompanionIndex` gzip-JSON codec (bounded
  decompression, format/version refuse), the corpus builder, the single-file
  trackerless `.torrent` wrapper, the `Publisher` (rebuild+republish on a ≤1h
  ticker; an empty index is a failure; `lastRefresh` advances only on success),
  and the `Subscriber` + worker. The package talks to the DHT/engine only
  through narrow ports (it consumes the Slice-9 BEP-46 pointer primitive).
- The subscriber is fail-closed and closes three legacy defects: it **rejects a
  snapshot not authored by the followed publisher**, **stamps imported records
  with `SignedBy` = the publisher** (so `search --signed-by` attributes them),
  and **dedups on `GeneratedAt`** so an unchanged snapshot is not re-imported.
- The companion fetch (in the engine) is fail-closed on the untrusted infohash:
  exactly one file, ≤32 MiB checked before any piece, and a safe filename.
- Daemon wiring: independent publisher/subscriber legs, all failures non-fatal,
  teardown before the index; a persisted follow file (atomic, size-capped).
- New `GET /companion` + `POST /companion/{refresh,follow,unfollow}` HTTP routes
  and a new `swartznet companion <status|follow|unfollow|refresh>` CLI command
  (closing a legacy discoverability gap), plus a gated `add --regtest` flag.
- Config: `CompanionDir`, `CompanionFollowFile`.

### Slice 9 — Layer D: BEP-44 keyword index (2026-07-19)

A node now publishes its own torrents' name-keywords as signed BEP-44 mutable
items and resolves a query against a set of known indexer pubkeys — a real
2-node DHT cluster round-trips (node A publishes, node B searches and recovers
A's infohash) over nothing but standard mainline traffic (BEP-44/46/51 — no new
verb, reserved bit, or UDP port).

- New `contracts/dhtschema`: the frozen `KeywordValue` wire payload (bencoded
  via anacrolix `torrent/bencode` for byte-identity with the signing path), the
  ≤1000-byte cap enforced **before** unmarshal on decode, and the verbatim,
  never-truncated `SaltForKeyword`. Golden-vector pinned, with a `bep44` re-
  marshal-identity gate and a legacy-bytes read-back gate.
- New `internal/dhtindex`: the swappable `RecordBackend` seam
  (`Publish/Refresh/Retract/Lookup/Status/Close`) with the shipping
  `legacyKeyword` per-keyword backend; the `Publisher` worker (buffered submit +
  hourly refresh + retract) with a 55-minute per-keyword throttle and oldest-hit
  eviction; the `Lookup` read side (most-distinctive token, parallel per-indexer
  fan-out, reputation-gated, scored merge); the `AnacrolixPutter`/`Getter`
  sharing the fail-closed `checkPutStats` guard (a zero-node put surfaces as a
  failure, never a false success); the BEP-46 infohash-pointer primitive; and
  the BEP-51 `SampleInfohashes` primitive.
- Engine wiring: publish-on-`GotInfo` submits the torrent **name** keywords only
  (content tokens never reach the DHT) behind `--no-index` / `--no-dht-publish`;
  retract-on-removal; the read side stays alive leech-only while the write side
  is suppressed under those flags (the privacy cascade); the self-pubkey and
  `peer_announce`-gossiped pubkeys enter the lookup set.
- `searchmux` gains the third concurrent layer; `POST /search {"dht":true}`
  returns a `dht` block; a Layer-D error is surfaced inline (200), never a 5xx
  (§5.9). New `GET /publish` status route. New `swartznet crawl-probe` (a
  stateless BEP-51 diagnostic) and `--no-dht-publish` flag, both in `--help`.
- Config: `LayerDMode` (`legacy` only this release), `MinIndexerScore`,
  `PublisherPath`.

### Slice 8 — RIBLT sync + Aggregate record substrate (2026-07-19)

Two peers now reconcile their signed keyword→infohash record sets over
multi-batch Rateless-IBLT set reconciliation (the `sn_search` msg_types 4–8),
so differences larger than 100 symbols converge.

- New `contracts/riblt`: the frozen RIBLT math — FNV-1a-64 element key (over all
  32 bytes), the SplitMix64 12-step membership cycle, the streaming encoder, and
  the peel decoder with a self-consistency gate. Golden-vector pinned.
- New `contracts/record`: the signed keyword record. Its RIBLT `ElementID`
  (`SHA-256(pk‖kw‖ih‖LE64(t))`) **excludes** the proof-of-work and signature, so
  two valid signings of one semantic record dedupe; the signature message
  **includes** the PoW nonce. Golden-vector pinned.
- Extended `contracts/ltepwire`: the `sync_begin`/`symbols`/`need`/`records`/
  `end` codec with its per-message caps and `element_size == 32` / `kw ≤ 64`
  invariants. Golden-vector pinned.
- New `internal/swarmsearch` reconciliation: a FIFO-capped, filter-matched
  `RecordCache` (source + sink), the `SyncSession` state machine (budgets
  negotiate downward, byte accounting on the semantic record size, a
  symbol-budget overrun ends the session `limit_exceeded` penalty-free while
  other violations `aborted` + charge), the multi-batch responder pump, and the
  `StartSync`/`SendSyncNeed`/`CloseSync`/`WaitSyncConverged` initiator. It
  tolerates the reordering + timing of the async peer-wire dispatch (an
  out-of-order symbol batch is buffered; the initiator finalizes only past a
  symbol floor and exactly once).
- Engine: mints one signed record per torrent name-keyword on metadata arrival
  (even with `--no-index`, so a leech-only node still reconciles), wires the
  record cache with an age-prune loop, and takes the node identity via
  `SetSigner`. The `sn_search` reconciliation capability (services bit 9) is
  advertised again now that the sync bodies exist.
- `GET /aggregate` gains `cache_size` (records held) and `reconciliation`.
- `scripts/dod-slice8.sh`: 11 checks — the frozen golden vectors, a 250-record
  symmetric-difference converging to the union multi-batch, the budget /
  index-desync / capability guards, record minting per name-keyword, and the
  live `/aggregate` readout.

### Slice 7 — Layer S: `sn_search` peer-wire extension (2026-07-19)

The first wire slice. Two SwartzNet peers negotiate `sn_search` in the LTEP
`m` dict and answer scoped queries over an ordinary piece-transfer connection;
a vanilla BitTorrent client sees only an ignorable name in the `m` dict and
**receives zero `sn_search` frames**. No new reserved bit, no new DHT verb, no
new UDP port.

- New `contracts/ltepwire/wire.go`: the frozen query/result/reject/
  peer_announce envelope (msg_types 0–3; 4–8 reserved for RIBLT sync) with
  byte-exact golden vectors. The wire drops the year-1 zero timestamp,
  truncates hit names to 60 bytes (rune-safe, non-UTF-8 preserved), clamps
  rank, and forces an empty (never null) hit list.
- New `internal/swarmsearch`: the `Protocol` (LTEP negotiation, per-peer state,
  the token-gated transport seam), the inbound `Handler` (scope-checked answer
  or reject, fail-closed on `ShareLocal≠2`), the outbound query fan-out with
  **asked-set anti-spoof** (a result counts only from a peer we asked, one
  frame per peer), a Bitcoin-style **banman** (local, never gossiped) and a
  per-peer token-bucket rate limiter. It never imports Bleve (answers via an
  injected searcher) nor the torrent package.
- Engine LTEP transport seam: the `sn_search` extension is advertised on every
  outbound handshake; inbound frames are dispatched **off the read loop** with
  dual 256-slot semaphores and a payload copy; a `PeerToken` (mintable only from
  a recorded advertisement) makes "no send to a non-advertising peer" a
  compile-time property. `peer_announce.services` is produced by the single
  Slice-6 `Announced()` mask, so capability downgrades now reach the wire.
- `POST /search {"swarm":true}` fans in swarm hits concurrently with Layer L; a
  Layer-S failure is surfaced inline as `swarm.error` with a 200 (never a 5xx);
  `/status` reports the known/capable peer counts.
- `wirecompat` gains a raw-socket `MiniPeer` and two scenarios: a **vanilla
  peer sees zero `sn_search` frames** while a capable peer gets the
  `peer_announce`, and a peer queries the engine over the real wire and gets
  its indexed hits.
- `scripts/dod-slice7.sh`: 11 checks — the wire gates (codec goldens, §6 fixes,
  scope-reject-2, fail-closed, anti-spoof, vanilla silence, real-wire query,
  compile-time no-bare-send) plus the HTTP swarm surface (§5.9 inline error,
  `/status` counts, index-independent swarm).

### Slice 6 — capability mask: the single services-bit producer (2026-07-19)

- New `contracts/ltepwire`: the frozen 64-bit `sn_search` services bitfield
  (bits 0–9, append-only; unknown bits ignored never rejected) and the SINGLE
  pure producer `Announced(Sharing, RuntimeFacts) uint64`, pinned by a golden
  vector table. `Announced` derives every bit purely from its inputs — there is
  no static default floor — so an operator downgrade actually clears its bit.
- **Type split** (`Sharing` = operator prefs, bits 0–3; `RuntimeFacts` = daemon
  facts, bits 4–9). The Publisher bit is a daemon-owned `RuntimeFact`; the
  `PATCH /capabilities` body has no publisher field, so a partial "save sharing"
  can never clobber it.
- Engine: `Sharing`/`SetSharing` (runtime-mutable, seeded from config),
  `RuntimeFacts` (computed live — `Publishing = !--no-index && !--no-dht-publish`,
  so `--no-index` zeroes the Publisher bit), and `ServicesMask()` — the one call
  site both the HTTP readout and (later) the wire announce use.
- HTTP: `GET /capabilities` (sharing prefs + read-only `publisher` + live
  `services`), `PATCH /capabilities` (preserve-unset merge, clamps
  `share_local` 0..2) with a `POST` alias, and `GET /aggregate` `services` now
  the **live** mask (16 lowercase hex, big-endian) instead of the static
  `0x2ED`. New config fields: `share_local` (0..2), `share_file_hits`,
  `share_content_hits`.
- Fixes three legacy §6 defects: the static `/aggregate` mask and the
  Publisher-bit clobber are fully closed; capability downgrades now reach the
  readout (the wire half lands in Slice 7). Regtest (bit 8) is now actually
  advertised when in regtest mode — the "loud" announce the legacy never set.
- `scripts/dod-slice6.sh`: 17 checks — live `0x2FD`/`0x2E0` masks, the
  Publisher-bit-untouched PATCH, clamp, POST alias, CSRF, and the `--no-index`
  `→ 0x2ED` cascade.

### Slice 5 — trust, reputation, Bloom, confirm/flag (2026-07-18)

- New `internal/reputation`: the frozen FNV-64a Kirsch-Mitzenmacher
  **known-good Bloom filter** (`SBLM` v1 on-disk format, pinned by a golden
  byte-vector and a checked-in `testdata/known-good.bloom`), the
  **Bayesian-smoothed per-publisher reputation tracker** (prior weight 5.0,
  neutral 0.5, decaying seed bonus; score-sorted `Snapshot`), and the
  source-attribution LRU that records which indexer returned each hit.
- New `internal/trust`: the persistent publisher **allowlist** (`trust.json`,
  pretty JSON, atomic write, 64-hex validation).
- New `internal/admission`: a **deny-by-default** admission engine (inverts
  the legacy's permissive §6 admission; the permissive policy survives only
  for tests).
- The **one shared confirm/flag path** (`daemon.Confirm`/`daemon.Flag` →
  `engine.ConfirmHit`/`engine.FlagHit`): deterministic code owns the Bloom/
  reputation state transition. **Confirm** adds the infohash to the Bloom and
  boosts its attributed indexers; **flag** demotes ONLY attributed,
  non-trusted indexers and **fails closed** on zero attribution — fixing the
  legacy §6 dishonest-success defect (it never claims a demotion it did not
  perform; `indexers_flagged` is not `omitempty`, `attribution` is explicit).
- Two Bloom auto-confirm paths, both **add-only** (no reputation
  self-reinforcement): a **trusted publisher** is confirmed at metadata
  arrival; a **completed** torrent is confirmed unconditionally — and
  `watchCompletion` promotes the next queued slot *before* the Bloom
  checkpoint so promotion never waits on disk I/O.
- Crash-safety: a bounded **periodic checkpoint** (~5 min) plus a
  save-on-close flush; each save writes a **unique tempfile** and the
  checkpoint is serialized, so a checkpoint racing a confirm/flag can never
  tear `reputation.json`. A corrupt/unreadable but configured `trust.json`
  **fails closed** (`attribution=trust-unavailable`, demotes nobody) rather
  than silently disabling the trusted-publisher exemption.
- HTTP: `POST /confirm`, `POST /flag`, `GET /aggregate` (known-indexer /
  bootstrap counts, distinguishing a starved node from a quiet one), and
  `/status` gains `bloom` + `reputation` blocks. CLI: `swartznet trust`
  (offline `list`/`add`/`remove`), `swartznet confirm`, and `swartznet flag`
  (honest "no reputations changed …" reporting).
- `scripts/dod-slice5.sh`: 22 checks — offline trust management, golden Bloom
  load, trusted-publisher auto-confirm at metadata, the shared confirm/flag
  path, `kill -9` checkpoint durability, `/aggregate` shape, and the
  fail-closed corrupt-trust path.

### Slice 4 — Layer L: local full-text search (2026-07-18)

- New `internal/indexer`: the Bleve (scorch) schema v3, the single-worker
  extraction pipeline with a 60 s watchdog + panic-recover, the 2 KiB
  chunker (paragraph→line→hard-split), `t:`/`c:` doc IDs, exact-match
  `TermQuery` for `signed_by`/`infohash`, the `<mark>` HTML highlighter,
  and the schema-sentinel rebuild. `IndexTorrent` honors the §7-Q37
  sticky-SignedBy rule.
- New `internal/indexer/extractors`: the first-claim-wins MIME registry
  (full `extTypes` override table) with plaintext (tags-in `.html`),
  subtitle, PDF, EPUB, DOCX, ODT, and ZIM extractors, all hardening
  bounds preserved.
- New `contracts/token`: frozen `Tokenize` + `MostDistinctive` (the only
  lookup-token chooser — the §6 first-token defect is unrepresentable);
  golden vectors.
- New `internal/searchmux`: the local-only fan-out (native response
  types, no merged hit type), shared by the HTTP adapter and (later) GUI.
- Engine wiring: `SetIndex`, `autoIndex`, `ingestFileEvents`, per-torrent
  indexing toggle, snapshot index counters, a reachable **Forget** (docs
  deleted, files kept — §6/F36), and an **hourly rescan** that recovers
  dropped file-complete events (rebuild-only; the legacy had none).
- **ZIM works against the live pipeline** (the §6 defect): the engine
  wraps its torrent reader in a size-bounded `ReadSeekerAt` shim so
  `io.ReaderAt`-requiring extractors work — and the size bound fixes a
  real over-read where anacrolix's `File.NewReader` read a large buffer
  past the file into the next file's bytes, mis-indexing every earlier
  file in a multi-file torrent.
- HTTP: `POST /search` (Layer-L block; nil index = 200-empty, not 503),
  `GET /index/stats`, `POST /torrents/{ih}/indexing`, `DELETE
  /torrents/{ih}?forget=1`, `/status local.doc_count`. CLI: `swartznet
  search` (direct-Bleve or daemon-routed) and `swartznet index`
  (stats / per-torrent toggle).
- `scripts/dod-slice4.sh`: 18 checks — PDF+ZIM+plaintext indexed through
  the live pipeline, `<mark>` highlights, exact-TermQuery `--signed-by`,
  Forget, and the NoIndex degraded surface.

### Slice 3 — create + infohash-preserving signing (2026-07-18)

- New `contracts/sign`: the frozen 34-byte signing payload
  (`"SN-TORRENT-V1|" ‖ SHA1(info)`), the ed25519 primitives, the
  `Signature` type, and the three-way verification taxonomy —
  `ErrNotSigned` (benign), `ErrBadSignature` (tamper, Signature still
  populated), and plain errors for bad lengths that are neither sentinel.
  A deterministic golden vector pins the derivation byte-for-byte.
- New `internal/signing`: `Sign`/`Verify` over raw .torrent bytes via the
  contracts codecs — the info dict never round-trips a typed struct, so
  signed and unsigned twins share one infohash. Re-signing replaces.
- `swartznet create <path> -o <out>` with `--sign`, `--seed`, `--name`,
  `--piece-kib` (now genuinely validated: power of two ≥ 16 KiB — the
  legacy documented but never enforced it), trackers/webseeds/private/
  comment. A no-seed create builds no engine at all; `--seed` seeds in
  place daemonlessly.
- Verify-at-add: signed torrents populate `SignedBy` end to end (handle →
  session → `/torrents` → status), bad signatures add anyway with empty
  `SignedBy` and a logged rejection (D20), and a verified duplicate add
  upgrades an unsigned handle stickily (§7-Q37).
- Fixed the legacy defect where `create --sign --seed` never showed the
  creator's own signature: the signed bytes now flow to the engine via
  `AddTorrentBytesSeedFrom`, so the publisher's own node badges itself.
- `scripts/dod-slice3.sh`: 17 checks — twin infohash equality at the byte
  level, taxonomy over the wire, D20 add-anyway, the J1 fix, and the
  fail-closed identity paths.

### Slice 2 — add + download + seed (2026-07-18)

The first slice that is a usable BitTorrent client.

- New `internal/engine`: the anacrolix wrapper (v1.61.0, extension APIs
  only) with the full SPEC §5.4 quirk catalog honored — positive
  rate-limiter burst floor, shared `PeerStore` for BEP-5 write tokens,
  `Exp=2h` BEP-44 pin, per-file Normal-priority activation (never
  `DownloadAll`), seed-in-place `FilePathMaker` on the real basename,
  background `VerifyData` on metainfo add and restore, and the
  magnet→metainfo upgrade guard. Frozen `session.json` v1 format with
  byte-exact `.torrent` copies; per-entry restore that survives corrupt
  rows; exactly-once file-complete events with a 64-slot replay buffer.
- Three §6 defects fixed by construction: queue promotion on completion
  is unconditional (never Bloom-gated); `countActiveDownloads` inspects
  file priorities so an all-`none` torrent frees its slot;
  `/config/rate-limit` uses pointer-field merge semantics (PATCH+POST) so
  a partial update can't zero the other cap.
- New `contracts/bencode`: raw-bytes-preserving metainfo codec — the
  infohash is always SHA1 of the original info bytes, never a typed
  round-trip; signed/unsigned twins share one infohash (golden-pinned).
- `swartznet add <magnet|.torrent|40-hex|->` IS the daemon (the Slice-0
  `serve` scaffold is deleted); bare 40-hex is an infohash add; `-` reads
  .torrent bytes from stdin. New `swartznet files` command; `status`
  gains a Downloads section with real percentages.
- HTTP API: `POST /torrent`, `GET /torrents`, files listing/priority,
  pause/resume/remove, `/config/rate-limit`, `/config/queue` — all via
  locally-declared interfaces (httpapi still imports zero subsystems).
- New `internal/wirecompat` in-process multi-engine harness; the
  timing-sensitive two-engine transfer lives in `wirecompat/scenarios`,
  excluded from CI by a non-end-anchored grep (fixing the legacy footgun).
- `scripts/dod-slice2.sh`: 15 checks driving TWO real binaries through a
  loopback magnet transfer, session restore percentages, and the
  rate-limit merge.

### Slice 1 — persistent identity (2026-07-18)

- New `internal/identity`: raw 64-byte ed25519 `identity.key` (frozen legacy
  format), created at mode exactly 0600 with the legacy validation gates and
  error strings preserved byte-for-byte (exact-0600 — 0400 rejected too;
  size; seed→pubkey re-derivation; directory-at-path). A present-but-invalid
  key is never overwritten or regenerated (invariant #2).
- Auto-create happens **only at the default XDG path**: the daemon compares
  the configured path against `config.Default().IdentityPath` and
  `identity.Load`'s create branch is the single enforcement site (also
  closing the legacy stat-then-create TOCTOU). Parent-dir creation moved to
  the create arm, so a refused load has no side effects.
- `daemon.New` loads identity before all subsystems and — fixing the §6
  defect — wires `httpapi.Options.PublisherPubKey` from
  `identity.PublicKeyHex`, un-nested from any publisher collaborator:
  `/status` now reports `publisher.pubkey` in every real daemon.
- `serve --identity <path>`: explicit paths are load-only; a failed explicit
  load exits 1, while a default-path failure degrades per SPEC §2.8 (warn +
  publisher-less). New log events `daemon.identity_loaded` /
  `daemon.identity_load_err` (attrs `pubkey`/`err` kept from legacy).
- `scripts/dod-slice1.sh` (19 checks) covers the whole DoD against the real
  binary; `dod-slice0.sh` gained hermetic `XDG_DATA_HOME` isolation.

### Slice 0 — walking skeleton (2026-07-17)

- New `internal/config`, `internal/daemon`, `internal/httpapi` (+ embedded
  web stub) and `cmd/swartznet` with `serve` (temporary scaffold, folds
  into `add` in Slice 2), thin-HTTP-client `status`, `version`, `help`.
- `GET /status` (frozen full JSON shape, honestly degraded) and
  `GET /healthz`; CSRF/DNS-rebind guard and 1 MiB body cap preserved
  byte-for-byte from legacy; non-loopback binds warn loudly
  ("API is UNAUTHENTICATED") but are honored.
- Reverse-order teardown is now observable in logs
  (`daemon.close_begin` → `daemon.bg_joined` → `httpapi.stopped` →
  `daemon.close_done`); SIGINT/SIGTERM exit 130; `Daemon.Close` is
  idempotent.
- §6 defect fixes shipped from day one: **one** unsafe gate
  (`SWARTZNET_UNSAFE=1` or a test binary; `SWARTZNET_ALLOW_REGTEST` is
  gone), and `SWARTZNET_LOG` (`debug|info|warn|error`, default info) is
  documented in `swartznet help`, with a warning on unrecognized values.
- `Validate()` now runs all rejections before creating any directory
  (SPEC §7-Q48 resolved; create-set unchanged: DataDir 0755 +
  IndexDir's parent only).

## Unreleased

Targeting **v1.0.0** — first GA release. v1.0.0 still wants
real-world data for the reputation prior weight and at least
one second client implementing `sn_search` (the BEP-1
requirement to take a draft to Final). Both require
engagement from actual users of the v0.x prereleases.

### Fixed — Create-and-seed shows "downloading 0%" instead of "seeding"

A freshly-created torrent could sit at `downloading` / 0% instead
of flipping to `seeding` / 100%, even though every byte was
already on disk. Two paths were affected:

- **CLI `create --seed`** called the plain `AddTorrentMetaInfo`,
  which roots anacrolix storage at `cfg.DataDir`. Unless the user
  passed `--data-dir <parent-of-root>` (documented but
  unenforced), the post-add `VerifyData` rehashed an empty
  directory and reported 0%. It now seeds **in place** from the
  positional `<root>` via `AddTorrentMetaInfoSeedFrom`, matching
  the GUI; `--data-dir` no longer needs to point at the content.

- **Renamed torrents (GUI *and* CLI `--name`)** stayed at 0% even
  on the seed-from path: anacrolix's default file storage resolves
  every file under `<base>/<info.Name>/…`, so once the display
  name differed from the on-disk basename (the GUI's Create dialog
  auto-fills an editable name and defaults the seed checkbox on,
  making this the common case) it looked for the bytes under the
  wrong name and found nothing.

The engine's per-torrent seed storage now keys file paths on the
**real on-disk basename** (`storage.NewFileOpts` + a custom
`FilePathMaker`) instead of `info.Name`, so a renamed torrent
seeds from its real location while downloaders still see the
chosen name. The basename is persisted in the session manifest
(new `content_name` field) so a restart restores the correct
storage; legacy entries fall back to `info.Name`. Regression
tests cover single-file and multi-file renames plus the
restart/restore path, and the GUI create-seed test now asserts the
renamed torrent reaches 0 missing bytes. Wire-compat is untouched
(local storage wiring only; the infohash and `.torrent` bytes are
produced identically).

### Fixed — Second whole-codebase review pass (32 findings: 3 blocking, 11 important, 18 nits)

A fresh review + regression-check against the prior 55-finding
audit confirmed every prior fix holds, found two of them
incomplete, and surfaced new hardening work. All 32 confirmed
findings are fixed here, each with a regression test. No on-wire
bytes changed (the swarmsearch budget work *implements*
already-specified `limit_exceeded` behavior) and Layer L/S/D
isolation is preserved, so the mainline-compat matrix stays green.

**Blocking (remote OOM/DoS that `recover()` cannot catch):**

- **extractors:** the ZIP-container document extractors
  (DOCX/ODT/ODP/PPTX/EPUB) fed the *decompressed* entry stream to
  the XML parser unbounded — the between-token output guard never
  runs inside a single giant `Token()` call, so one deflate-bomb
  text node (~1032:1 amplification) buffered the whole decompressed
  body. Every zip-entry reader is now wrapped in
  `io.LimitReader(rc, maxDocTextBytes)` (64 MiB text budget; PPTX
  slides and EPUB chapters share a shrinking budget). The HTML
  tokenizer now sets `SetMaxBuf` (default was unlimited) and treats
  buffer/budget exhaustion as graceful truncation. Also: archive
  name-list extraction enforces its documented 4 MiB cap on the
  tar.gz branch (plus a 1 GiB decompression-walk bound), and EXIF
  IFD parsing is integer-overflow-safe on 32-bit targets.
- **companion:** the B-tree walker's per-page child checks could
  not see *cross-page* fan-in — pages laid out `i → {i+1, i+2}`
  grow root-to-leaf paths Fibonacci-ally (reproduced: 5M+ piece
  fetches for a 40-piece file). This was the incomplete half of the
  prior audit's fix. The walk now shares a visited-set across the
  entire traversal and fails closed the moment any page is reached
  twice, capping the walk at `NumPieces` fetches; `Find`
  additionally rejects duplicate leaf indices as defense-in-depth.

**Important:**

- **dhtindex:** all three BEP-44 put paths (keyword, BEP-46
  companion pointer, PPMI) now share one `checkPutStats` guard and
  fail closed when the put traversal reaches zero DHT nodes — the
  prior audit hardened only the keyword path, so a companion
  pointer that never landed still recorded success and showed a
  green refresh. Pointer values fetched from the DHT are size-capped
  at the BEP-44 1000-byte limit before decoding; PPMI seqs use the
  overflow-clamping `nextSeq`.
- **swarmsearch:** the per-session sync `max_bytes` budget was
  declared and echoed on the wire but never enforced (dead code).
  `ApplyRecords` now phase-guards, accounts every frame into
  `bytes_in`, and aborts with the documented `limit_exceeded` once
  over budget; `sync_symbols`/`sync_records` protocol violations now
  tear the session down (terminal `sync_end`, release, misbehavior
  charge) instead of logging at Debug; query `result`/`reject`
  frames from peers outside the query's fan-out set are dropped and
  charged.
- **indexer pipeline:** the extract reader is now closed by the
  extracting goroutine strictly after `Extract` returns — never by
  the worker on the watchdog-timeout path — fixing a
  Read-after-Close data race on anacrolix `torrent.Reader` when an
  extractor wedges.
- **engine:** companion-index torrent downloads are capped at
  32 MiB (`maxCompanionBytes`) — an attacker-controlled BEP-46
  pointer can no longer fill the disk; the fetch fails closed before
  any piece is requested. `sn_search` reply writers are gated by a
  bounded semaphore; `autoDownload`/`autoIndex`/magnet-upgrade
  metadata waits now honor `bgCtx` so Close doesn't leak them.
- **reputation:** corrupt/truncated `known-good.bloom` files are
  rejected at load time (exact bitset-length match) instead of
  panicking the daemon on the first `Add`/`Test`.
- **httpapi + gui:** flagging an infohash with no recorded source
  attribution no longer demotes *every* known indexer's reputation
  (an attacker-weaponizable fan-out); both sides now fail closed
  with zero demotions, and `FlagResponse` gained an additive
  `indexers_flagged` field. GUI: the Downloads primary selection is
  infohash-keyed so Remove/Pause can't hit the wrong torrent after
  the 2 s background re-sort; copied magnet links URL-escape the
  display name (closing a magnet-parameter-injection vector from
  remote-sourced names).
- **cmd:** `create --seed` now keeps DHT enabled so trackerless
  seeds are actually discoverable; `--identity` without `--sign` is
  a usage error instead of silently ignored.
- **daemon:** the anchor-fetch loop re-runs when the HTTPS
  bootstrap fallback adds anchors after startup (previously a dead
  cold-start path); the companion follow file is capped at 1 MiB
  (rejected wholesale over the cap); anchors no longer consume
  `MaxTrackedPublishers` slots and cap refusals are logged.
- Plus input-validation nits across httpapi (search query length
  cap, strict file-index parsing), cmd, gui (strict search-limit
  parsing, notification-map pruning), indexer (bounded doc-walk
  pagination, SignedBy-only search now accepted as documented), and
  engine (untrusted-name validation on companion/session paths).

### Fixed — `internal/gui` is race-clean under `-race`

The GUI tests raced under the Fyne *test* driver: a background
worker goroutine routes its UI update through `fyne.Do`, which the
test driver (unlike the real GLFW driver) runs inline on the
spawned goroutine. That render then touched Fyne's process-global,
unsynchronized font/SVG cache concurrently with other
test-goroutine rendering — `DATA RACE` reports, all inside Fyne
internals. Production behavior is unchanged and was never racy:
with the real driver, `fyne.Do` serializes every callback onto the
single UI thread, so exactly one goroutine ever touches those
caches.

The fix is test-only. Each async UI flow that a test exercises now
exposes an unexported, nil-in-production seam invoked at the very
end of its goroutine (after `fyne.Do` returns); tests set the seam
and deterministically join the goroutine before any further
rendering or teardown, restoring the single-UI-thread invariant
under the test driver and replacing the prior `time.Sleep` drains.
Seams added: `afterCreateTorrent` (create.go), `afterRunSearch`
(search.go), `afterRefreshPublisher` (companion.go),
`afterSetAllPriorities` (files_dialog.go), and `afterRemoveSelected`
/ `afterAddMagnet` (downloads.go). No production logic, signatures,
or off-thread behavior changed. Verified across 38+ full-suite
`-race` iterations with zero `DATA RACE` reports.

Also widened a handful of tight test wall-clock budgets that could
flake under a fully-saturated parallel `-race` sweep (CPU starvation,
not a code defect): the regtest Layer-D refresh scenario
(`TestLayerDPublisherRefreshKeepsItemFresh`, 12 s → 30 s) and four
`cmd/swartznet` "did this near-instant event happen?" assertions —
the two `signalContext` cancellation tests (1 s → 5 s) and the two
`progressLoop` exit tests (2 s → 5 s). Each assertion's meaning is
unchanged; a genuinely hung loop still fails.

### Fixed — Whole-codebase review hardening pass (55 findings)

A multi-package review swept every subsystem and produced 55
confirmed, adversarially-verified findings; all are fixed here,
each with a regression test (except pure-comment nits). No
on-wire bytes changed and Layer L/S/D isolation is preserved,
so the mainline-compat matrix stays green.

Untrusted-input availability (remote DoS, were unauthenticated):

  - `engine`: reject a zero infohash decoded from an untrusted
    BEP-46 pointer before it reaches `AddTorrentInfoHash`
    (`panicif.Zero` crash); the add path now recovers like
    `AddMagnetURI`.
  - `indexer/extractors`: the subtitle extractor now honours
    `maxBytes` (was ignored → multi-GB `.srt` OOM); MKV element
    sizes are bounded before allocation (untrusted EBML VINT →
    multi-GB `make`); `readFull` enforces a 64 MiB ceiling.
  - `indexer/extractors`: ZIP/XML/PDF extractors (docx, odt,
    odp, pptx, epub, pdf, htmltext) now cap *decompressed*
    output, not just compressed input, closing zip-bomb
    amplification; ID3 `tagSize` is clamped to the read budget.
  - `companion`: the B-tree reader requires strictly-downward,
    increasing, in-range child indices plus a depth budget,
    visited-set and leaf cap — a self/back-pointing or
    DAG-shaped page no longer recurses into stack overflow.
  - `swarmsearch`: responder sync sessions are bounded per peer
    with a staleness reaper that emits `sync_end` aborted, so an
    abandoned session reaches a terminal state instead of
    lingering forever (unbounded-memory DoS).
  - `dhtindex` / `indexer`: DHT value decode and infohash→query
    paths are size-bounded / structured (no `QueryString`
    interpolation), matching the encode-side caps.

Fail-open on state-changing steps (deterministic-layer rules):

  - `dhtindex`: a publish that reached zero DHT nodes no longer
    counts as success (the rate-limiter then suppressed retry
    for ~55m); `MarkPublished`/`LastPublished` require ≥1
    confirmed node. Manifest size estimates reserve the
    timestamp width so a near-cap entry can actually publish.
  - `cli`: explicit `--key`/`--identity` paths are load-only and
    error on a missing file instead of silently minting a new
    identity; `defaultIdentityPath` honours `XDG_DATA_HOME`;
    `--dht-insecure`/`--regtest` are gated; `config.Validate`
    fails closed on those flags outside tests.
  - `daemon`: the companion publisher writes to `CompanionDir`
    (was `DataDir`, silently ignoring the setting); the
    anchor-fetch goroutine is cancel-tracked and joined on
    `Close`; HTTPS bootstrap rejects non-`https` URLs.
  - `engine`: a restored *paused* torrent no longer flips its
    files to Normal priority on activation.

Concurrency, correctness & identity:

  - `indexer`: the extract watchdog now runs `Extract` against a
    hard deadline so a wedged extractor fails the file instead
    of pinning a worker forever; `deleteByQueryLocked` is
    bounded; `Stats.CorpusTextBytes` no longer truncates past
    64k content docs; `AllTorrentDocs` preserves `SignedBy`.
  - `swarmsearch`: RIBLT symbol `Index` is validated (a dropped
    frame aborts instead of silently desyncing); `ShareLocal==1`
    fails closed; `StartFeeler` is idempotent; misbehavior
    scores are charged; `mergeResponses` dedups per peer.
  - `security`: seed-list pubkeys are normalised (uppercase hex
    seeds now get the bonus); a corrupt Bloom header with
    `m==0` is rejected instead of panicking; Bloom double-hash
    forces an odd stride (Kirsch-Mitzenmacher); a loaded key's
    public half is re-derived from its seed and verified.
  - `gui`: follow rows are sorted deterministically and the
    context menu keys off pubkey, not row index, so Unfollow no
    longer targets the wrong publisher; the file-priority
    `Select` no longer re-fires `OnChanged` on recycle; startup
    no longer fires false "Download complete" toasts for
    already-seeding torrents.
  - `httpapi`: mutating endpoints reject cross-origin / non-loopback
    `Origin`/`Referer` and validate `Host` (CSRF / DNS-rebinding
    defense); non-loopback binds are refused without an explicit
    opt-in; client-supplied search timeouts are clamped;
    `handleStatus` populates the publisher pubkey.

### Added — "Aggregate" distributed-layer redesign

A multi-commit series inverts Layer-D from per-keyword BEP-44
items into per-publisher pointers, moves the real keyword index
into a piece-aligned B-tree inside a regular BitTorrent
"companion index torrent", and reconciles updates between peers
via a new Rateless IBLT set-sync session layered on `sn_search`.
Design and rationale in
[`docs/research/PROPOSAL.md`](docs/research/PROPOSAL.md);
byte-level spec in [`docs/research/SPEC.md`](docs/research/SPEC.md);
phase-by-phase tracking in
[`docs/research/ROADMAP.md`](docs/research/ROADMAP.md).

Shipped components:

  - `internal/companion/btree.go` — B-tree page layout
    (magic `SNAGG\0`, interior/leaf/trailer kinds) with
    deterministic build, BFS piece assignment, ed25519-signed
    trailer binding the tree to a publisher.
  - `internal/companion/read_btree.go` — prefix-query walker
    with trailer-sig verification and per-record sig re-check
    on every returned hit.
  - `internal/companion/pow.go` — hashcash mint +
    `SignAndMineRecord` convenience (D=20 default).
  - `internal/dhtindex/ppmi.go` — Publisher Pointer Mutable
    Item. One BEP-44 item per publisher at the fixed
    double-hashed salt `SHA256("snet.index")`. Collapses DHT
    cost from O(publishers × keywords) to O(publishers).
  - `internal/dhtindex/lookup.go` — `Lookup.Query` resolves
    PPMIs first, falls back to legacy per-keyword for
    publishers that haven't migrated. Dual-read migration.
  - `internal/swarmsearch/riblt.go` — rateless IBLT encoder/
    decoder (SIGCOMM 2024) with graduated-degree cycle.
    Converges in ~3d symbols for symmetric difference d.
  - `internal/swarmsearch/sync_{wire,session}.go` — msg_types
    4-8 wire format and in-process session state machine.
  - `internal/swarmsearch/handler.go` — LTEP dispatch for
    sync msg_types behind the `BitSetReconciliation` (services
    bit 9) capability gate. Peers without the bit receive
    `reject code 2`.
  - `internal/daemon/bootstrap.go` + `bootstrap_https.go` —
    three-channel cold-start (anchors + BEP-51 candidates +
    `peer_announce` endorsements) plus last-ditch HTTPS anchor
    fallback.

Test coverage: ~130 new test functions across the four
packages, including the capstone `TestAggregateEndToEnd` that
exercises publisher → PPMI → subscriber → prefix-query in one
pass.

Status of the BEP-51 crawler track: the primitives now land
incrementally — `dhtindex.SampleInfohashes` (raw BEP-51 query),
`dhtindex.PublisherFromMetainfo` (signature classifier), and
`dhtindex.CrawlOnce` (one-tick glue: sample + fetch + classify
+ sink). A `swartznet crawl-probe --addr <host:port>` ops
command exercises the primitive against a live peer.

The remaining gap is a *production* MetainfoFetcher: BEP-9
ut_metadata only transports the info dict, while
`snet.pubkey` / `snet.sig` are top-level metainfo fields, so
a BEP-9-only fetcher can't promote crawled infohashes to the
"observed publishers" set without an additional signature-
exchange channel. Closing the gap needs either a new LTEP
extension (e.g. `ut_signature`) or a tracker / HTTP mirror
convention. Documented in
[`docs/research/MILESTONE-v0.5.0.md`](docs/research/MILESTONE-v0.5.0.md).
LocalRecord sync was wired up in earlier commits so nodes do
share records over the responder path; the engine attaches a
RecordCache as both source and sink in `engine.New`.

### Changed — Indexer pipeline hardening (global cap + watchdog)

Two extractor-pipeline robustness improvements following the
whole-codebase review:

  - `internal/engine/engine.go` — pipeline construction now
    passes a 100 MiB global per-file extract cap instead of 0
    ("each extractor's own default"). The archive extractor
    enforced 64 MiB but PDF / EPUB / DOCX / FB2 silently
    buffered whatever the file claimed; a torrent of
    pathological PDFs could blow up resident memory before
    any per-extractor limit kicked in. 100 MiB is comfortably
    larger than any real-world textual document while still
    bounding the worst case.
  - `internal/indexer/pipeline.go` — `safeExtract` now arms a
    soft 60-second watchdog that emits a
    `pipeline.extract_slow` warning when an extract exceeds
    its budget. Soft because Go can't terminate goroutines
    externally — the watchdog observes and reports, the
    worker keeps going. Real protection still comes from
    `Pipeline.maxFileBytes` plus per-extractor
    `io.LimitReader` plus the panic recovery; the watchdog
    surfaces hangs in logs before the pipeline channel backs
    up.

The Extractor interface itself still doesn't take a
context.Context — plumbing one through every extractor is a
larger refactor that's not justified by current threat
modelling. The watchdog + size cap combination addresses the
practical concern (memory blowups, silent hangs) without the
churn.

### Added — BEP-46 pointer carries publisher timestamp

Companion-index pointers now include a `ts` field (publisher
wall-clock Unix-seconds) alongside the existing `ih`
infohash. Subscribers can use the timestamp to detect
publishers that have gone silent (pointer hasn't been
re-published in days/weeks) and apply policy — log a warning,
deprioritize, drop the follow — instead of silently chasing a
stale infohash.

  - `internal/dhtindex/dht.go` — `bep46Pointer.TS int64`
    field with `bencode:"ts,omitempty"`. PutInfohashPointer
    sets it from `time.Now().Unix()`. New
    `AnacrolixGetter.GetInfohashPointerInfo` returns
    `PointerInfo{InfoHash, TS}`; the legacy
    `GetInfohashPointer` is now a thin wrapper around it so
    every existing caller (companion subscriber, all the
    fakes in the test suite) keeps working without churn.
  - `internal/dhtindex/pointer_ts_test.go` — round-trip,
    omitempty-on-zero, legacy-decode tolerance, and
    forward-compat (unknown-extra-key) tests lock in the wire
    contract.

Wire-compat is preserved by bencode's extension model:
publishers without ts emit `{ih: ...}` (decoders treat TS=0
as "unknown freshness, accept"); subscribers without ts
support ignore the extra dict entry. No coordinated upgrade
required; no BEP-44/46 spec violation.

### Fixed — `--no-index` cascades to disable Layer-D publishing

`--no-index` was documented to "prevent Bleve from opening at
all (and cascade to disable Layer D publishing)" but only the
first half held: the daemon skipped `indexer.Open` while
`engine.startPublisher` still launched the BEP-44 keyword
publisher under the user's identity. A user running
`swartznet add --no-index` was still announcing keyword
pointers to the DHT — a privacy regression that contradicted
the documented global opt-out.

  - `internal/config/config.go` — new `Config.NoIndex` field
    mirrors the daemon-level flag down to the engine.
  - `internal/daemon/daemon.go` — `daemon.New` copies
    `Options.NoIndex` into `Cfg.NoIndex` before calling
    `engine.New` so the engine's publisher gating sees a
    consistent view.
  - `internal/engine/engine.go` — `startPublisher` now skips
    the keyword Publisher worker on either `cfg.NoIndex` or
    `cfg.DisableDHTPublish`, and keeps the `sn_search`
    Publisher capability bit at 0 in both cases (a node that
    isn't pushing entries shouldn't gossip itself as an
    indexer).
  - `internal/daemon/daemon_test.go` —
    `TestDaemonNoIndexCascadesToPublisher` regression-locks
    that `Engine.Publisher()` is nil when `NoIndex` is true.

The lookup / pointer-getter path stays intact, so a
`--no-index` node still subscribes to other publishers and
fetches companion indexes — it just contributes nothing to
the search-side network.

### Fixed — Determinism follow-ups from production-architecture audit

  - `internal/dhtindex/manifest.go` — `RemoveHit` now drops a
    keyword entry from the manifest entirely once its last hit
    is removed, and a new `RemoveAllHits(infohash)` scrubs an
    infohash from every keyword entry in one pass. Prevents the
    manifest from growing unbounded over the lifetime of a
    long-running publisher.
  - `internal/dhtindex/publisher.go` — new `Publisher.Retract`
    method wraps `RemoveAllHits` and persists the manifest.
  - `internal/engine/engine.go` — `Engine.RemoveTorrent` now
    calls `publisher.Retract` so a removed torrent's keyword
    hits stop being re-announced on the next refresh tick.
    Without this, peers kept discovering torrents the publisher
    no longer hosted.
  - `internal/daemon/follows.go` — `LoadFollowFile` now returns
    `(int, error)` so corrupted or unreadable follow files
    surface through the structured logger instead of being
    swallowed when stderr is `io.Discard`. Behaviour stays
    fail-closed: an unreadable file leaves the subscriber with
    an empty list (follows can still be added via the HTTP API).

### Changed — Create Torrent dialog auto-fills the Output path

The native GUI's Create Torrent dialog now pre-populates the
`Output .torrent path` field with `<root>.torrent` whenever the
user browses for a root file/folder or types into the Root entry.
Same edit-survives policy as the existing Name auto-fill: the
output is only overwritten while the user has not typed anything
custom into it. Eliminates the `Output path required` error users
hit when clicking Create after picking a root but before picking
an output. The submit handler also defensively re-derives the
output path from the root if the entry is empty at click time, so
any flow where OnChanged didn't fire (paste-without-edit, focus
oddities) still produces a working .torrent without surfacing the
modal error.

### Added — Downloads multi-select + bulk control

Each click on a torrent row toggles its membership in a
multi-selection set; the toolbar shows `(N selected)` and
the right-click menu pluralises every action label so the
user can confirm the bulk operation visually. Pause / Resume /
Remove / Toggle Index now operate on every selected torrent in
display order, falling back to the single primary selection when
the set is empty. Two new toolbar buttons — `Select All` and
`Clear` — give explicit set-management without keyboard
modifiers (Fyne's table widget does not surface modifier state
through `OnSelected`, so a Ctrl-click toggle would have shipped
half-broken). Selection is keyed by infohash so it survives the
2-second poll refresh and any sort change.

### Added — Right-click menus on Search and Companion

Each Search-results card (Local, Swarm, DHT) now wraps in a
right-click capture exposing **Add to downloads**, **Copy
magnet link**, **Copy infohash**, **Confirm**, **Flag**, and
(when the hit is signed) **Copy publisher pubkey**. The
Companion tab's followed-publishers list gains a right-click
menu with **Copy public key**, **Copy label**, and **Unfollow**
for the focused row.

### Added — Companion tab shows the full publisher pubkey

`Companion → Companion Publisher → Public Key` was previously
truncated to 16 chars + "...", which made it useless for the
"share my pubkey so my peers can follow me" flow. The label is
now selectable, monospace, wraps within its row, and gains an
inline `Copy` button next to the pubkey value so users get the
full 64-char hex into their clipboard in one click.

### Changed — Companion-index torrent uses publisher-tagged file name

`WriteCompanionFiles` now writes the gzipped JSON payload to
`<dir>/swartznet-content-index-<pubkey-prefix-12>-v1.json.gz`,
and the wrapping single-file torrent's `info.name` follows. The
previous generic name made every node's companion torrent look
identical in the Downloads list, which was confusing for users
running multiple SwartzNet instances or following several
publishers. Subscribers locate the payload by the path returned
from `engine.FetchCompanionTorrent` and never key on the
filename, so the rename is end-to-end transparent.

### Added — Settings: editable Data / Index directories

The Settings tab gains a `Storage Paths` card with text
entries + Browse buttons for `DataDir` and `IndexDir`, a Save
button, and a "Reset to defaults" shortcut. Edits persist to
`<share-root>/config.json` (loaded by `swartznet-gui` on next
launch) and a dialog explains the change applies on restart —
hot-swapping these paths under a running engine + indexer is
not safe, so we deliberately avoid pretending it is. CLI flags
still win against the saved file for operators who need to
override.

### Fixed — Downloads table column overflow

Torrent rows with very long names previously rendered the trailing
characters past the Name column boundary into the Status / Progress
cells. Cell labels now use `TextTruncateEllipsis` so long names
show "Project Hail Mary 2026 1080p WEB Lin..." and stop cleanly at
the column edge.

### Fixed — Create Torrent seeds from the source path

`Engine.AddTorrentMetaInfo` was relying on anacrolix's default
storage backend, which roots every torrent at `cfg.DataDir`.
The Create Torrent flow hashes whatever path the user picked
(typically `~/Documents/...`), so the freshly-seeded torrent's
`info.Name` resolved to a non-existent file under DataDir and
the post-add VerifyData found zero bytes — the row sat at 0%
even though the source content was already on disk. New
`Engine.AddTorrentMetaInfoSeedFrom(mi, dataParent)` adds the
torrent with a per-torrent `storage.NewFile(dataParent)` so
anacrolix locates the real bytes at `dataParent + info.Name`.
The GUI's `runCreateTorrent` calls the new method with
`filepath.Dir(opts.Root)`, and the value is persisted as
`sessionEntry.DataPath` so RestoreSession reapplies the same
override on the next launch instead of bouncing the row back
to 0%. Regression tests:
`TestAddTorrentMetaInfoSeedFromExternalPath` and
`TestAddTorrentMetaInfoSeedFromSurvivesRestart`.

### Fixed — Restored torrents resume at their real progress

Two engine bugs combined to make every restart appear to wipe
download progress: `AddTorrentMetaInfo` was the only `Add*` path
that did not call `persistAdd`, so torrents created via the GUI's
`Create Torrent` button (or seeded by the companion publisher) were
not recorded in the session manifest at all and silently vanished
on the next launch; and `RestoreSession` did not run `VerifyData`
on the re-added handles, so anacrolix had no opportunity to discover
the on-disk pieces — `BytesCompleted` stayed at 0 until a peer
asked for a piece, which never happens for a freshly-restarted
client with no peers yet. `AddTorrentMetaInfo` now marshals the
metainfo, writes it to `<DataDir>/torrents/<infohash>.torrent`, and
calls `persistAdd` with `addedVia="metainfo"`; `RestoreSession`
spawns `verifyOnRestore` for every entry which waits for `GotInfo`
(no-op for file/metainfo entries) and then calls
`VerifyDataContext`. Regression test:
`TestRestoreSessionRehashesOnDiskData`.

### Added — About dialog shows build date

`Help → About` gains a `Built` row alongside `Version`, populated
from a new `main.BuildDate` ldflag injected by `scripts/build-gui.sh`
as the UTC build timestamp. `go run` / IDE launches that don't go
through the build script render the row as `(dev build)` so the
user can tell at a glance whether they're on an official binary.

### Added — `swartznet crawl-probe` ops command

One-shot CLI that issues a single BEP-51 `sample_infohashes`
query against a DHT address and prints the response (samples,
interval, num, closest nodes). Pure ops tooling — no running
daemon needed. Useful for validating that a peer supports
BEP-51 and for hand-inspecting the samples it volunteers
during Channel-B crawler development. Text + JSON output.

### Added — Engine periodic RecordCache prune

`engine.DefaultRecordCacheMaxAge` (30 days) +
`DefaultRecordCachePruneInterval` (1 hour). On startup
`engine.New` launches a goroutine that calls
`RecordCache.PruneOlderThan` on a ticker so the Aggregate
cache's TTL is enforced automatically without operator
intervention. Regtest mode swaps in 200 ms / 500 ms so
scenario tests can observe the prune cycle.

### Fixed

  - **`upgradeMagnetSession` race in `Engine.AddTorrentFile`**. The
    metainfo-arrival upgrade goroutine was spawned for every
    `registerLocked` caller, including `AddTorrentFile` and
    `AddTorrentMetaInfo`. For those paths, the goroutine raced
    `writeTorrentCopy` for the same `<hex>.torrent.tmp` target
    file: when the goroutine won the rename, the saved bytes
    contained a re-marshalled metainfo (anacrolix's
    `Torrent.Metainfo()` synthesises CreationDate / CreatedBy
    fields) that no longer byte-matched the original. On the
    next `RestoreSession`, `metainfo.Load` rejected the file
    with "error after decoding metainfo: expected EOF". Symptom:
    intermittent flake under heavy parallel race testing where
    `eng2.TorrentSnapshots()` returned 1 torrent instead of 2.
    Fix moves the goroutine spawn out of `registerLocked` and
    into `AddMagnet` / `AddInfoHash` only — the two paths that
    genuinely benefit from a post-fetch metainfo upgrade. Same
    gate added in `restoreEntry` so file/metainfo restores skip
    the upgrade. Regression-gated by
    `TestAddTorrentFileDoesNotRemarshalCopy` (deterministic:
    fails reproducibly with original buggy code).

  - **Anchor-source admit no-op**. `daemon.Bootstrap.admit`
    contained a documented placeholder that called
    `tracker.RecordReturned(pub, 0)`, which `RecordReturned`
    silently ignores (`if n <= 0 { return }`). Anchor pubkeys
    therefore never landed on the tracker, breaking the
    intent of `opts.AnchorReputation` and leaving the lookup's
    heavy-tail rule unable to identify trusted publishers.
    Replaced with `tracker.MarkSeeded(pub, label)` for
    `source == "anchor"` only — gives anchors the high
    starting score the seeded-bonus branch of `scoreOf` is
    designed to express. Candidate sources stay un-seeded
    (default 0.5; earn or lose reputation organically).

  - **Layer-D BEP-44 put/get round-trip**. anacrolix/torrent's
    `NewAnacrolixDhtServer` builds a `dht.ServerConfig` from
    scratch and does NOT copy `dht.NewDefaultServerConfig`'s
    `Exp=2h` default, so `sc.Exp` landed at 0. `bep44.Wrapper`
    then treated every stored item as instantly expired
    (`i.created.Add(0).After(now)` = false for any clock read
    after the store), deleted it on the next get, and returned
    `ErrItemNotFound`. Symptom: BEP-44 put succeeds (valid-token
    expvar increments, put handler replies OK) yet an
    immediately-following get returns "value not found" — the
    load-bearing failure in testbed scenario s12 and every
    downstream Layer-D keyword lookup. Fixed in the engine's
    `ConfigureAnacrolixDhtServer` callback by pinning
    `sc.Exp = 2*time.Hour` if upstream didn't set one, matching
    `dht.NewDefaultServerConfig`. Regression-gated by two new
    tests: `internal/engine.TestDHTEnginePutReceive` (raw
    anacrolix probe put → engine DHT store → probe get) and
    `internal/testlab.TestLayerDDHTClusterRoundTrip` (full
    engine-hosted 6-node cluster on loopback). The Layer-B s12
    scenario now asserts `indexers_responded >= 1` and that the
    fixture infohash appears in `dht.hits`.

### Added

  - **Layer-A DHT cluster harness** (`internal/testlab.NewDHTCluster`).
    In-process multi-node engine harness with the mainline DHT
    enabled and every node chain-bootstrapped to its siblings
    on loopback. Mirrors testbed scenario s12's topology (2
    seeds + N leeches, `NoSecurity=true`, explicit
    `DHTBootstrapAddrs`) without any docker dependency. Purpose:
    isolate whether a Layer-D failure lives in the engine's DHT
    wiring or in the containerised-network path. First
    application: `internal/testlab.TestLayerDDHTClusterRoundTrip`
    and `TestDHTClusterPointerRoundTrip` originally reproduced the
    s12 BEP-44 get-returns-"value not found" bug in pure Go;
    after the `sc.Exp` fix landed they became the regression gate
    for the fix. `TestLayerDDHTClusterRoundTrip` further asserts
    that every seed answers the lookup and each seed's distinct
    fixture infohash appears in the merged hit list (exercises
    Lookup.Query's fan-out + merge).
  - **`internal/testlab.TestDHTClusterWireMeshConverges`** — gate
    for the DHT-cluster's sn_search mesh contract. Documents
    that `NewDHTCluster` on its own does NOT converge a
    peer-wire mesh (the shared testlab infohash has no real
    torrent so no node announces_peer for it), and asserts that
    calling `Cluster.WireMesh` explicitly gets all n-1 peers
    handshaked within 15 s.
  - **`internal/testlab.TestLayerDGossipEndToEnd`** — strictly
    stronger than the round-trip test. Exercises the full
    in-process equivalent of testbed s12: DHT cluster +
    WireMesh + Publisher.Submit + sn_search PeerAnnounce `pk`
    gossip → auto-AddIndexer on the leech → real BEP-44
    Lookup.Query. No manual `AddIndexer` call anywhere. If the
    gossip cross-registration path regresses, the leech's
    Lookup.Indexers() never gets the seed pubkeys and this
    test fails before the DHT query runs.
  - **`internal/testlab.TestLayerDPublisherRefreshKeepsItemFresh`** —
    regression gate for the Publisher's refresh ticker. Asserts
    LastPublished advances past the initial timestamp within
    12 s under regtest (RefreshInterval=5 s) AND that a
    post-refresh Lookup.Query still resolves the keyword to
    the fixture infohash. Catches any bug where the refresh
    loop stops firing — which would silently let BEP-44 items
    expire in production (2h TTL) without being re-published.
  - **Engine DHT introspection**: `Engine.DHTRoutingTableSize()
    returns (good, total)` exposes the embedded anacrolix DHT
    server's routing-table occupancy. Intended for diagnostic
    dashboards and for tests that need to distinguish "put
    traversal found no neighbours" from "put traversal fanned
    out but nothing answered" — a load-bearing distinction when
    debugging the s12 family of failures.
  - **`GET /status.dht`** (HTTP API): new JSON block
    `{"good_nodes": N, "nodes": M}` exposing the daemon's DHT
    routing-table occupancy. Daemon wires
    `engine.DHTRoutingTableSize` into the new
    `httpapi.Options.DHTStats` probe. Omitted entirely (nil
    field) when the daemon ran with `DisableDHT=true`, so
    callers can distinguish "DHT disabled" from "DHT enabled
    but empty". Lets operators and Layer-B scenarios surface
    DHT health without reaching into internal types.
  - **`config.ListenHost`**: when non-empty, binds every
    BitTorrent/DHT listener to the given interface (e.g.
    `127.0.0.1`). Default empty = anacrolix's default =
    `0.0.0.0`. Operators rarely need this, but the `testlab`
    DHT cluster sets it to `127.0.0.1` so the DHT's BEP-44
    token validator (which SHA1s the query's source IP) sees a
    deterministic source across sibling queries.
  - **`config.DisableIPv6`**: passes straight through to
    `torrent.ClientConfig.DisableIPv6`. Forces the embedded
    client onto a single address family, avoiding the latent
    "two DHT servers per node, Publisher drives only one" trap
    and the cross-family `[::ffff:v4]` routing-table entries
    that can't round-trip through a v4-only Put. Useful for
    any deployment on a network without functional IPv6
    (corporate LANs, most docker bridges).

### Changed

  - **GUI: more design polish — About dialog + Add Magnet
    retry.** Follow-up to the first design pass:
    - *About dialog — copyable values*: the identity pubkey,
      BitTorrent port, and HTTP API address each now have an
      inline Copy icon-button next to them. Previously the
      dialog rendered these as plain `widget.NewLabel`, which
      Fyne does not let users select or copy — so the 64-char
      ed25519 pubkey was effectively trapped in the dialog.
      The placeholder values ("unknown", "disabled") and the
      empty-string case don't get the Copy button; we don't
      want to invite users to copy nothing. Covered by a new
      `TestCopyableValue` unit test.
    - *Add Magnet — preserve URI on error*: validation failures
      and engine rejections used to close the dialog; the user
      had to reopen it and re-paste the whole magnet URI to
      fix a single-character typo. Now the error dialog's
      onClosed callback reopens the Add Magnet form with the
      bad URI pre-filled and the "Index" checkbox state
      preserved, so the user edits in place.
  - **GUI: design polish across Downloads, Search, Companion, and
    the main menu.** Specifically:
    - *Downloads — confirm before Remove*: the Remove toolbar
      button, context-menu item, and Delete keyboard shortcut
      now all trigger a `dialog.ShowConfirm` that names the
      torrent ("Remove \"Ubuntu 24.04 Desktop\" from the
      download list and stop seeding/leeching?") and clarifies
      that downloaded files are kept. The previous version of
      this confirm dialog claimed it also deleted on-disk data,
      which was inaccurate — `engine.RemoveTorrent` calls
      `anacrolix.Torrent.Drop()` and removes the session-
      manifest entry, but leaves both `/data` and the Bleve
      index untouched.
    - *Downloads — friendlier Add Magnet errors*: client-side
      validation fires instantly for two common paste mistakes
      (a non-magnet URL, or a magnet missing the `xt=urn:btih:`
      parameter) with explicit guidance; engine-side errors are
      rewritten from anacrolix's internal phrasing
      ("engine: magnet has zero infohash") into user-facing
      text ("the magnet URI's infohash is all zeros — it needs
      a real 40-character btih value"). Unknown errors pass
      through unchanged. Unit tests cover both the validator
      and the rewriter.
    - *Search — empty-state hint*: before any search runs, the
      result panel now shows a centred "Search across your
      local index, the swarm, and the DHT" prompt explaining
      what the tab does and how to broaden the query. Hidden
      once results arrive; re-shown when a search returns zero
      hits from every enabled layer.
    - *Companion — followed-publishers empty state*: the
      Followed Publishers card previously rendered an empty
      rectangle when you had no follows. It now overlays a
      "No publishers followed yet. Paste a 64-char public key
      above and press Follow to start syncing a remote Bleve
      index." hint that hides once the first follow lands.
    - *Main menu — discoverable shortcuts*: a proper `File`
      menu now sits alongside `Help`, with `Add Magnet...`,
      `Find in Search`, and `Quit` each carrying their
      registered `desktop.CustomShortcut` so Fyne renders the
      accelerator next to the label. The `Quit` item also sets
      `IsQuit` so platform integrations (macOS app menu) can
      route it correctly.
  - **GUI: consolidated four duplicated `win()` helpers into a
    single `windowForObject`** (`internal/gui/window.go`). Each
    tab (Downloads, Search, Settings, Companion) previously
    carried a near-identical 15-line method that walked the
    Fyne driver's window list to find the hosting window for a
    canvas object; they now all delegate to the shared helper.
    The new helper also handles the no-Fyne-app case without
    panicking (covered by a regression test), which makes the
    GUI package test-friendly for the first time.

### Added

  - **GUI: first unit tests** (`internal/gui/helpers_test.go`,
    `internal/gui/sort_test.go`). Lifts package coverage from
    0.0 % to 7.4 % by exercising the pure helpers (`humanBytes`,
    `rateStr`, `parseKiB`, `kibStr`, `limitDisplay`, `boolInt`,
    `boolStr`, `portStr`, `pieceLengthFromLabel`, `splitLines`,
    `torrentFilter.Matches`, `swartzTheme.Color/Font/Icon/Size`)
    and the Downloads-tab sort state machine (`snapLess` across
    all 9 columns and both directions, `sortSnapsSlice`
    stability on equal keys and edge cases, `toggleSort` cycle
    none → asc → desc → none, `selectedInfoHash` bounds
    checks). The larger widget-construction paths still need a
    real Fyne runtime to exercise end-to-end; these tests cover
    every piece of state logic the widget code calls into.

### Fixed

  - **Engine: outbound PeerAnnounce frames now actually carry
    the `pk` field when Layer-D publisher is running**
    (`internal/engine/engine.go:startPublisher`). Wire-compat
    matrix row 8.4-C introduced pubkey gossip in sn_search
    PeerAnnounce — but the gossip path in
    `swarmsearch.Protocol.onRemoteHandshake` gates on
    `caps.Publisher > 0`, and the engine never bumped caps
    after starting the publisher. `SetPublisherPubkey` set the
    pubkey in the protocol state but the frame-encode path
    silently skipped it because caps.Publisher was still 0.
    Net effect: every running SwartzNet node had pubkey gossip
    disabled, so Layer-D cross-registration via sn_search
    handshake never fired in practice. `startPublisher` now
    reads the current capabilities, flips Publisher=1, and
    calls `SetCapabilities` — but only when
    `DisableDHTPublish` is false (leech-only DHT mode keeps
    gossip off on purpose). Surfaced by building the
    DHT-enabled Layer-B testbed stack: leech-1's /search
    --dht reported `indexers_asked=0` until the fix went in;
    now reports `indexers_asked=6` (all nodes cross-
    registered).

  - **`fileTracker.Subscribe` now replays events for files that
    were already complete at registration time**
    (`internal/engine/file_tracker.go`). Previously, on a seed
    where every piece was verified-complete before the
    `ingestFileEvents` goroutine had a chance to register its
    subscription, the fileTracker's initial dispatch fired into
    an empty subscriber list and the content-ingest pipeline
    never saw those files — so seeded content never got
    indexed. `Subscribe()` now copies the existing `doneReplay`
    buffer into the new subscriber channel before returning,
    eliminating the race. Regression coverage:
    `internal/engine/file_tracker_replay_test.go`
    (`TestSubscribeReplaysPreDispatchedEvents`,
    `TestSubscribeReplayDoesNotDoubleDeliver`). This was a
    pre-existing bug that only showed up once Layer-B actually
    ran transfers to completion against pre-populated seeds; the
    Layer-A in-process harness has always had per-test subscribe
    ordering that avoided the race.

  - **`.txt`, `.text`, and `.md` now have explicit MIME overrides
    in `internal/indexer/extractors/extractor.go`**. Go's builtin
    `mime.TypeByExtension` table does NOT include `.txt`, so on
    systems without `/etc/mime.types` (Alpine base images,
    scratch containers, minimal CI runners) `.txt` files were
    returned as empty-string MIME and the plaintext extractor
    refused to claim them. The Layer-B Docker testbed hit this
    head-on once it grew real multi-KiB text fixtures. Moving
    the entries into `extTypes` makes behavior deterministic
    across deployment targets. Existing
    `internal/indexer/extractors/mime_test.go` covers the
    override path.

### Changed

  - **`sn_search` now rejects queries for unsupported scopes instead
    of silently downgrading them** (`internal/swarmsearch/handler.go`):
    a C1 querier that asks a C0 responder for scope `"c"` (or `"f"` on
    an F0 node) now receives a `Reject` frame with code
    `RejectUnsupportedScope` (2), matching the wire-compat matrix row
    8.4-B in `docs/05-integration-design.md`. Empty scope and scope
    `"n"` are always served. Coverage:
    `internal/swarmsearch/scope_reject_test.go` (unit) and
    `internal/testlab/scope_reject_scenario_test.go` (full
    BT → LTEP → extension message round-trip, including a hit-cache
    pollution check that proves the reject fires before the local
    search runs).

### Added

  - **`swartznet add --dht-insecure`** (testing-only)
    (`cmd/swartznet/cmd_add.go`, `internal/config/config.go`,
    `internal/engine/engine.go`). Disables BEP-42 node-ID
    security enforcement on the anacrolix DHT server (maps to
    `dht.ServerConfig.NoSecurity`). Required for private DHTs
    on docker bridges / k8s cluster IPs where container IPs
    never produce a "secure" ID under BEP-42's rules —
    anacrolix would otherwise silently filter every peer as
    "not secure" and BEP-44 puts time out on every target.
    Must stay off on mainline: flipping it in production
    weakens Sybil resistance.
  - **Testbed: s12 asserts up to the full Layer-D put stage**
    (`testbed/scenarios/s12-swarm-dht.sh`,
    `testbed/docker-compose.dht.yml`, back in
    `run-testbed.sh all`). Concretely verifies: 6-node DHT
    formation, seed publisher warm-up, every node has
    `capable_peers >= 1` (sn_search LTEP handshake
    converged), at least one BEP-44 put emitted, AND
    `leech-1.dht.indexers_asked >= 2` — proves pubkey-gossip
    cross-registration via sn_search is actually working end-
    to-end. The final hop (leech gets non-empty `hits` from
    the Layer-D lookup) is intentionally NOT asserted yet;
    header comment in the scenario documents the deferred
    investigation into why `getput.Get` returns "value not
    found" in docker when a matching 6-node in-process test
    (same bootstrap topology, NoSecurity=true) completes in
    <1s. Incorrect keyword ("aethergram") was another bug
    the investigation caught — dhtindex.Publisher publishes
    only `Tokenize(torrent.Name)` keywords, not content
    tokens. Scenario now queries "corpus".
  - **`swartznet add --dht-bootstrap=HOST:PORT`** (repeatable)
    (`cmd/swartznet/cmd_add.go`, `internal/config/config.go`,
    `internal/engine/engine.go`). Threads a user-provided list
    of bootstrap addresses into
    `dht.ServerConfig.StartingNodes` instead of anacrolix's
    default public mainline hosts. Required for any isolated-
    network DHT scenario (docker bridge, k8s cluster-local
    testing) where public hosts are unreachable. Empty value
    preserves the previous behaviour.

  - **Testbed: DHT-enabled 6-node stack (s12, WIP)**
    (`testbed/docker-compose.dht.yml`,
    `testbed/scenarios/s12-swarm-dht.sh`). 6-node swarm with
    DHT enabled, static IPs in a new 172.29.0.0/24 subnet,
    cross-bootstrapped seeds. The scenario is deliberately
    NOT in `scripts/run-testbed.sh all`: gossip-based indexer
    cross-registration now works end-to-end
    (`indexers_asked=6` post-fix, up from 0), but BEP-44 puts
    on the private DHT time out consistently in anacrolix's
    getput extension (`transaction timed out` warnings on
    every attempt). Raw KRPC ping/pong between containers
    works, ruling out basic UDP. Root cause is still under
    investigation — left in-tree for a future loop to pick
    up so the `--dht-bootstrap` flag and the gossip-caps fix
    have a real end-to-end target.

  - **Testbed driver: per-scenario timing + JSON output**
    (`scripts/run-testbed.sh`). The scoreboard now shows a
    DURATION column alongside SCENARIO and RESULT, so slow
    trends and flake signals are visible at a glance. A new
    `--json=<path>` flag writes a structured summary
    (`{started_at, finished_at, total_wall_clock_s,
    overall_exit, scenarios:[{name, result, duration_s,
    netem_profile}]}`) suitable for CI status bots and
    perf-regression dashboards. JSON is produced via a
    python3 heredoc (already a hard dependency of every
    scenario script) rather than hand-quoted bash — bash's
    `printf %q` produces shell-safe escapes, not JSON-safe
    ones. Stress-tested: `scripts/run-testbed.sh all` ran
    4 times back-to-back (3 flake-detection loops + 1 final
    verify) all PASS, 115-125s each.
  - **s11 — vanilla BT client interop at the wire level**
    (`testbed/scenarios/s11-vanilla-interop.sh`,
    `testbed/Dockerfile.vanilla`,
    `testbed/docker-compose.swarm.yml` gains a `vanilla-leech`
    service at 172.28.0.9 under compose profile `vanilla`).
    Runs the upstream `anacrolix/torrent/cmd/torrent` CLI — the
    same library family our engine wraps, but built without any
    SwartzNet extension-protocol registrations — on the swarm
    bridge and requires it to download the 4-MiB fixture from
    SwartzNet peers using `--no-dht --no-pex --no-seed`. That
    leaves only the BEP-9 `x.pe=` peer hints + BEP-3/10 handshake
    as the peer bootstrap path. Successful download proves the
    SwartzNet seeds are emitting nothing that violates
    CLAUDE.md's "vanilla client must see nothing but
    BEP-3/5/9/10/44" constraint; a regression anywhere in our
    LTEP handshake, reserved-bit usage, or peer-wire framing
    would trip this test. First passing run: the vanilla binary
    downloaded + verified + exited in 2 s. Bytes SHA-256 checked
    via `docker cp` because the binary exits cleanly on
    completion (no persistent container to `docker exec` into).
    Two other BT implementations were tried first — aria2c and
    transmission-cli — but both silently failed to honor
    `x.pe=` over the isolated docker bridge; the scenario's
    DEPLOYMENT NOTES in the script documents why anacrolix's
    CLI was chosen.
  - **s10 — mid-transfer seed churn**
    (`testbed/scenarios/s10-swarm-churn.sh`). Only scenario in
    the matrix that kills a seed *while* transfers are in flight
    (s8 runs to completion under loss without churn; s9 kills
    seeds only after leeches finish). Runs the 6-node swarm
    under `/netem/lossy.sh` to stretch the transfer window to
    ~15-20s, waits for `leech-1.progress >= 0.3`, `docker stop
    sn-swarm-seed-1`, asserts all 4 leeches converge within 300s
    via seed-2 + mutual exchange, and byte-checks leech-1's
    on-disk fixture. First passing run converged 14s post-churn.
    Gracefully short-circuits if the fixture is too small / the
    netem profile too gentle to produce a mid-transfer window.
  - **s9 — pass-along + late-joiner resilience scenario**
    (`testbed/scenarios/s9-swarm-late-joiner.sh`,
    `testbed/docker-compose.swarm.yml` gains a `leech-5`
    service gated behind the `late-joiner` docker compose
    profile). The shape: (1) wait for the four original leeches
    to reach `progress=1.0` via the original seeds, (2) `docker
    stop` both seeds so the swarm has only ex-leeches with the
    content, (3) bring up `leech-5` (172.28.0.8, profile
    `late-joiner`) whose magnet URI includes every node's IP,
    (4) assert leech-5 reaches `progress=1.0` entirely from
    ex-leech pass-along, (5) assert bytes match fixture
    byte-for-byte. Budget 60 s for the pass-along transfer.
    First passing run completed in under 10 s wall clock; the
    assertion is structural, so timing flakes would only
    surface as a correctness regression (ex-leeches not
    seeding, PEX not falling back, or `x.pe=` multi-address
    resolution silently picking only dead hints). Driver wires
    `--profile '*'` into the teardown path so compose-profile-
    gated services don't leak and hold the bridge network
    open.
  - **Layer-B 3-node testbed is now portable across UFW-DROP hosts**
    (`testbed/docker-compose.yml`, `testbed/scenarios/s1-s5`).
    Previously the scenarios probed `http://localhost:17654-17656`
    and relied on host-side port forwarding; on stock Ubuntu
    (`DEFAULT_FORWARD_POLICY="DROP"`) the docker-proxy's forward
    leg is blocked and the scenarios RST'd without ever running
    their assertions. And the leech magnet URI's `x.pe=` hints
    were hostname-based (`seed-1:42069`), which anacrolix's
    `StringAddr` dialer has been observed to silently drop. Both
    are now fixed: `docker-compose.yml` pins static IPs in a
    `172.27.0.0/24` subnet (distinct from the swarm stack's
    `172.28.0.0/24`), the magnet URI uses dotted-quad IPs, and
    s1-s5 probe the API via `docker exec sn-seed-1 curl
    http://<hostname>:7654/…` over the internal bridge. The
    three-node testbed now runs green on hosts with the default
    UFW profile. Full matrix wall clock: 70s.
  - **s8 — 6-node swarm under a lossy link**
    (`testbed/scenarios/s8-swarm-lossy.sh`). Runs the
    `docker-compose.swarm.yml` stack with the existing
    `/netem/lossy.sh` profile (5% loss + 150ms RTT). Asserts
    all 4 leeches converge within 300s despite retransmits and
    leech-1's on-disk bytes match the fixture byte-for-byte
    (smoke check that retransmits don't corrupt the wire).
    Drop-in-addition to s6/s7 so `scripts/run-testbed.sh all`
    now executes the full matrix: s1..s5 → swarm → s8.
  - **Layer-B testbed now includes a 6-node small-swarm scenario**
    (`testbed/docker-compose.swarm.yml`, scenarios
    `s6-swarm-transfer.sh` and `s7-swarm-search.sh`). Two seeds
    plus four leeches share a 4-MiB deterministic fixture
    generated by `scripts/gen-swarm-fixture.sh` (infohash
    `9564c13e1f67f40ec14bf0a2e54a86dea69ccebd`). The topology is
    a full mesh bootstrapped via `x.pe=` peer-address hints with
    **static IPv4 assignments** from a dedicated
    `172.28.0.0/24` IPAM subnet — hostnames don't reliably round
    -trip through anacrolix's `StringAddr` dialer for `x.pe=`
    hints, so the testbed pins `seed-1` through `leech-4` to
    `172.28.0.2-172.28.0.7`. s6 asserts all four leeches reach
    `progress=1.0`, their on-disk SHA-256 matches the fixture
    byte-for-byte, and peak `active_peers` during the transfer
    window reaches ≥ 2 (evidence that PEX + hint propagation
    actually built the swarm graph). s7 fires a `swarm:true`
    `/search` for `aethergram` from `leech-1` and asserts the
    Layer-S fan-out reaches the fixture infohash through at least
    one remote peer's local index. The new scenarios are driven
    by `scripts/run-testbed.sh swarm` (alias that runs both
    assertion scripts against one compose lifecycle), or
    individually via `s6` / `s7`. All API probes in these
    scenarios run via `docker exec … curl` over the internal
    bridge so the test is independent of the host's UFW
    `DEFAULT_FORWARD_POLICY` — stock Ubuntu workstations with
    `DROP` would otherwise see host-side port forwards RST'd by
    the kernel FORWARD chain.
  - **Layer-B testbed now transfers real content** (closes the
    "placeholder infohash" gap documented in the previous
    `testbed/README.md:92-100`): `testbed/fixture/` carries a
    small deterministic multi-file fixture
    (`content/testbed-fixture-book/`, ~3 KiB total) plus a
    pre-generated `.torrent` with a committed infohash
    (`c4405d27af8462e3d5e03c30c542f66e170fe4f8`). The Dockerfile
    copies `testbed/fixture/` into `/fixture/`; the entrypoint
    honours a new `ROLE` env var to pre-populate `/data` with the
    content on seed containers, so anacrolix's piece-verify pass
    marks the torrent complete on startup with zero network.
    The leech's magnet URI carries `x.pe=seed-1:42069&x.pe=seed-2:42069`
    peer-address hints (BEP-9) so it bootstraps without DHT or
    trackers. A new scenario
    `testbed/scenarios/s5-piece-transfer.sh` asserts the leech
    reaches `progress=1.0` within 90 s and its on-disk bytes
    match the fixture SHA-256 byte-for-byte; `s4-home-dsl-search.sh`
    now asserts the seeds actually return hits for the fixture
    marker `aethergram` (no more structural-only search check).
  - **Gossip-discovered publisher pubkeys across `sn_search`
    handshakes** (wire-compat matrix row 8.4-C, closes the last
    `WEAK` cell in the §8 matrix): nodes running the Layer-D
    publisher (`caps.Publisher == 1`) now gossip their 32-byte
    ed25519 identity in the `pk` field of every outbound
    `PeerAnnounce`. Receivers stash it on the peer's
    `PeerState.PublisherPubkey` and forward it to an attached
    `IndexerSink`, so the next `search --dht` fan-out automatically
    covers peers learned through peer-wire handshakes without a
    separate registration round-trip. Non-publishers (the default
    `caps.Publisher == 0`) suppress `pk`, so pure subscribers can
    never pollute a peer's indexer set. The engine wires a default
    sink into `*dhtindex.Lookup` (`engine.go:startPublisher`).
    Coverage: `internal/swarmsearch/gossip_pubkey_test.go` (unit,
    including wrong-length and missing-pk negative paths) and
    `internal/testlab/gossip_pubkey_scenario_test.go` (two-node
    cluster proves bidirectional delivery and subscriber-side
    suppression).

  - **Full-round-trip vanilla BEP-44 wire-compat test**
    (`internal/dhtindex/vanilla_bep44_test.go`): complements the
    signature-math-only `vanilla_bep44_wirecompat_test.go` with a
    two-server loopback DHT scenario. SwartzNet's `AnacrolixPutter`
    publishes a keyword entry, a plain `anacrolix/dht/v2` server
    issues `getput.Get` against the BEP-44 target, and the
    retrieved value decodes as a valid `KeywordValue`.
    Signature verification runs inside the anacrolix get path, so
    a passing test proves our BEP-44 items are
    bit-compatible with any stock client.

  - **Vanilla-client wire-compat scenarios**
    (`internal/testlab/vanilla_download_scenario_test.go`,
    `internal/testlab/vanilla_peer_scenario_test.go`,
    `internal/dhtindex/vanilla_bep44_wirecompat_test.go`): end-to-end
    proofs of rows 8.1-A/B and 8.3-D of the wire-compat matrix. A real
    `anacrolix torrent.Client` with no SwartzNet extensions downloads
    a seeded payload by magnet (BEP-9 metadata → BEP-3 pieces); a
    MiniPeer acting as a mainline client connects without advertising
    `sn_search` and verifies that the engine never pushes an
    `sn_search` extension frame at it; and a stock BEP-44 reader
    decodes a SwartzNet-published keyword item and verifies its
    signature without any SwartzNet code. `DialVanillaMiniPeer` in
    `internal/testlab/minipeer.go` exposes the empty-`m`-dict
    handshake path used by these tests.

### Fixed

  - **`sn_search` gossip accepted the all-zero 32-byte pubkey**
    (`internal/swarmsearch/handler.go`): the 8.4-C gossip path
    treated any 32-byte `pk` as a valid publisher identity, so a
    misbehaving peer advertising `pk = 0x00…00` would silently add
    a useless entry to every receiver's `Lookup.Indexers()` set on
    each reconnect, wasting the next DHT keyword GET fan-out on a
    key that cannot correspond to a real ed25519 identity. The
    handler now rejects the zero value at the sink boundary while
    still processing the rest of the `PeerAnnounce` fields
    (`Services`, `Version`). Regression coverage:
    `TestPeerAnnounceZeroPubkeyRejected` in
    `internal/swarmsearch/gossip_pubkey_test.go`.

  - **Ingest pipeline silently dropped files when two goroutines
    read `Handle.FileEvents()`** (`internal/engine/file_tracker.go`,
    `internal/engine/engine.go`, `cmd/swartznet/cmd_add.go`): the
    CLI's `swartznet add` progressLoop drained the same
    `FileCompleteEvent` channel that `Engine.ingestFileEvents` was
    using to feed the extractor pipeline. Go's single-receiver
    semantics split each event to exactly one reader, so a random
    fraction of files never reached the indexer and
    `TorrentSnapshot.IndexedFiles` stalled at ~11–13/15 for the
    15-file multi-peer fixture. The `fileTracker` now fans each
    event out to every subscriber independently via per-caller
    buffered channels, and `Handle.SubscribeFileEvents()` exposes
    that contract explicitly. The CLI's `progressLoop` and
    `Engine.ingestFileEvents` each take their own subscription, so
    display progress and pipeline progress can no longer cannibalise
    each other. Regression coverage lives in
    `internal/engine/file_tracker_dispatch_test.go` (unit fan-out
    checks) and
    `internal/testlab/file_events_fanout_scenario_test.go` (real
    seed→leech run that asserts `IndexedFiles == Files` while a
    second consumer runs in parallel).
  - **`swartznet add` daemon silently tearing down the HTTP API
    on malformed magnets** (`internal/engine/engine.go`): when
    the CLI was invoked with a magnet whose v1 infohash decoded
    to all-zero bytes (a common foot-gun in smoke tests and
    tooling glue), anacrolix/torrent's `AddTorrentOpt` panicked
    via `panicif.Zero`. The CLI's deferred `daemon.Close`
    unwound the stack, tearing down the HTTP API listener
    seconds after it started — from the outside the daemon
    stayed alive but the web UI, `status`, and `search --swarm`
    all got "connection refused". `Engine.AddMagnet` now parses
    the magnet URI itself via `metainfo.ParseMagnetUri`, rejects
    zero-infohash and malformed URIs with a descriptive error,
    and never lets the anacrolix panic fire. Regression tests
    live in `internal/engine/add_magnet_invalid_test.go` and
    `internal/daemon/http_reachability_test.go` (the latter
    boots a full `daemon.Daemon` and asserts `/healthz` stays
    reachable for a few seconds of idle lifetime, which the
    previous test suite never covered).
  - **`CreateTorrentOptions.Name` override silently ignored**
    (`internal/engine/create.go`): the documented "Name overrides
    info.name" behaviour did not take effect because the override
    was applied before `info.BuildFromFilePath`, which then
    overwrote `info.Name` with `filepath.Base(opts.Root)`. The
    override is now applied after `BuildFromFilePath`, matching
    the documented contract. Tests in
    `internal/engine/create_options_test.go` pin the new
    behaviour.
  - **Newly-created torrents stuck at 0% progress**
    (`internal/engine/engine.go`): when the Create Torrent
    flow (Fyne GUI or `swartznet create --seed`) built a
    torrent from local content and handed it straight to the
    engine, the download progress bar stayed at 0% forever.
    anacrolix/torrent does not verify existing on-disk pieces
    eagerly — it waits for a peer request to trigger a read,
    and a brand-new infohash with no other seeders in the
    swarm generates no peer requests. `AddTorrentMetaInfo`
    now kicks off `t.VerifyData()` in a background goroutine,
    so the piece-state subscription fires as each local
    piece is rehashed and both the download progress bar
    *and* the new indexing progress bar advance the moment
    the torrent appears in the GUI. Applies to the companion
    publisher path too, which seeds a freshly-written JSON
    index via the same code path.

### Changed

  - **Create Torrent dialog (Fyne)** — the **Torrent name**
    field now auto-populates with the basename of the chosen
    file or folder as soon as you browse to it, so the field
    is obviously editable rather than an empty afterthought.
    The label also spells out that this becomes the
    top-level folder name downloaders see, and the
    placeholder reads "Torrent display name — edit to
    rename". User-typed edits survive re-browsing; only the
    auto-fill from a previous browse is overwritten.

### Added

  - **Per-torrent indexing progress bar** (Web UI + API):
    the extraction pipeline
    (`internal/indexer/pipeline.go`) now keeps atomic
    per-infohash counters of every file it has processed,
    partitioned into `extracted` / `skipped` / `failed`, and
    exposes them via `Pipeline.Stats(infohash)`. The engine's
    `TorrentSnapshot` carries the counts through as
    `IndexedFiles` + `IndexExtracted`; the `/torrents` HTTP
    response surfaces them as `indexed_files` and
    `index_extracted`. The Downloads tab renders a second,
    thinner bar under each torrent card that advances from 0
    to the torrent's file count with a live "🔍 indexed N /
    M (X%)" label, so users can watch the pipeline chew
    through huge text-heavy corpora (e.g. a 450,000-file
    Project Gutenberg mirror where extraction runs orders of
    magnitude longer than the download itself) and tell at a
    glance that it's still making forward progress. One new
    test, `TestPipelineStatsCounters`.

  - **Session persistence**
    (`internal/engine/session.go`): the engine now records every
    open torrent in `<DataDir>/session.json` and writes a copy of
    each `.torrent` file to `<DataDir>/torrents/<infohash>.torrent`
    so the user's download list survives daemon restarts. Magnet
    adds are recorded with their original URI; once metadata
    arrives the magnet entry is upgraded to a file entry so future
    restarts skip the ut_metadata round trip. Per-torrent
    `paused`, `indexing`, and `queue_order` are part of the
    manifest and survive restart. `engine.RestoreSession` is
    invoked from `daemon.New` right after engine init so the GUI
    sees the previous list the moment the window opens. Two new
    integration tests (`TestSessionRoundTrip`,
    `TestSessionRemoveDeletesCopy`).

    Fixes the "all torrents disappear after a GUI restart" bug.

  - **ZIM extractor** (`internal/indexer/extractors/zim.go`):
    SwartzNet now indexes the article text inside OpenZIM
    files — the format Kiwix ships for offline Wikipedia,
    Project Gutenberg, Stack Exchange, and similar corpora.
    Uses random-access I/O (the engine's anacrolix file
    reader satisfies `io.ReaderAt`) so a 70 GiB ZIM doesn't
    have to be slurped into RAM. Handles uncompressed
    (cluster type 1) and zstd (type 5) clusters; XZ/LZMA2
    (type 4, pre-2021 ZIMs) is detected and skipped with a
    clean error. Bounded at 5,000 articles and 32 MiB of
    cumulative emitted text per file by default. HTML
    articles go through the existing `extractHTMLText`
    helper so block-level paragraph boundaries survive into
    the chunker. Five new tests including a synthesized
    in-memory ZIM with an uncompressed single-cluster
    layout. **Total extractors: 19.**

  - New dep: `github.com/klauspost/compress` (BSD-3-Clause)
    for zstd cluster decompression.

## v0.7.0 — 2026-04-13

**Headline: search by publisher.** Every torrent indexed since
v0.4.0 carries an optional ed25519 publisher signature on its
`.torrent` file. Bleve now stores that signature on each
TorrentDoc, exposes it as a search hit field, and accepts a
`SignedBy` filter that restricts results to a specific
publisher.

### Index schema bump (v2 → v3)

  - `TorrentDoc.SignedBy string` (64-char hex) added,
    persisted as a Bleve keyword field for exact-match
    filtering.
  - `SearchRequest.SignedBy string` filter — when set, the
    query is conjuncted with a term match on the
    `signed_by` field.
  - `SearchHit.SignedBy string` returned on every hit so
    UIs can render a badge.
  - Existing indexes auto-rebuild on first open under v0.7.0
    (the `SchemaVersion` sentinel mismatch triggers a clean
    rebuild — same code path as v0.2 → v0.3 of the index).

Two new tests: `TestSearchSignedByFilter` (3 docs split
across two pubkeys, each pubkey reachable via the filter)
and `TestSearchSignedByPersistsThroughIndex` (round-trip the
signed_by field through Bleve).

### CLI

  - `swartznet search --signed-by <pubkey>` flag passes
    through to the search API. Combine with `--swarm` /
    `--dht` as needed; the filter only applies to local
    Layer-L hits today.

### HTTP API

  - `POST /search` body gains `signed_by string`.
  - `GET /search` `local.hits[].signed_by` populated for
    torrent-level matches.

### Web UI

  - New `publisher` text input next to the search options.
    Accepts a 64-char hex pubkey; `pattern` validates client-
    side.
  - Search hits show a `✓ <pubkey-prefix>` badge for signed
    torrents. Clicking the badge sets the publisher filter
    and re-runs the search — fast pivot to "everything else
    by this publisher".

### Native GUI

  - Search hit subtitle gains `✓ signed by <pubkey-prefix>`
    when the local hit's SignedBy is non-empty.
  - Trust-aware rendering (gold ★ for trusted) deferred to
    v0.8 when the SearchHit type carries TrustedPublisher
    natively without an engine round-trip.

## v0.6.1 — 2026-04-13

Web UI polish patch on top of v0.6.0.

  - **Status panel**: new "Torrents" card at the top of the
    grid showing total + counts by status (downloading /
    seeding / queued / paused) + aggregate ↓/↑ throughput +
    signed/trusted counts. Computed from the same /torrents
    poll the Downloads tab uses, so no extra round trip.
  - **Keyboard shortcut**: pressing `/` (or `Ctrl/Cmd+K`)
    anywhere on the page switches to the Search tab and
    focuses the query input. Standard convention used by
    GitHub, Slack, Discord, Linear. Suppressed while the
    user is already typing in another input.

Search-result signed-publisher badges deferred to v0.7.0
where they'll land alongside a search-by-publisher filter
that requires persisting `signed_by` on each Bleve
TorrentDoc.

## v0.6.0 — 2026-04-13

Two focused additions: **four media metadata extractors**
(FLAC, OGG, MKV, MP4) and **web-UI parity with the native
GUI** for the features that landed between v0.3.0 and v0.5.0.

### New media metadata extractors

Pure-Go, stdlib-only implementations. Each extracts the
human-visible metadata from the container's header / tag
blocks; audio/video frame data is intentionally skipped.

  - **FLAC** (`internal/indexer/extractors/flac.go`) — walks
    the metadata-block chain to find the VORBIS_COMMENT block
    and decodes title, artist, album, date, genre, track,
    composer, label, ISRC, copyright.
  - **OGG Vorbis / Opus** (`internal/indexer/extractors/ogg.go`)
    — parses Ogg pages, locates the comment packet (prefixed
    with either `\x03vorbis` or `OpusTags`), and reuses the
    FLAC Vorbis-comment parser.
  - **MKV / WebM** (`internal/indexer/extractors/mkv.go`) —
    EBML walker bounded to Info / Tracks / Chapters / Tags
    elements. Extracts title, muxer, writer, per-track name,
    language, and per-chapter ChapString. Bounded at 16 MiB
    of read because MKV metadata always lives near the start.
  - **MP4 / M4A / M4B / M4V** (`internal/indexer/extractors/mp4.go`)
    — QuickTime atom walker limited to moov / udta / meta /
    ilst. Extracts the full iTunes-style tag set: title,
    artist, album artist, album, date, genre, composer,
    encoder, comment, description, grouping, copyright,
    track, disc.

12 new extractor tests synthesize the relevant container
format in-memory so tests don't depend on external files.

**Total extractors: 18** (plaintext, subtitle, PDF, EPUB,
DOCX, ODT, RTF, archive, FB2, PPTX, ODP, MOBI, ID3, EXIF,
FLAC, OGG, MKV, MP4).

### Web-UI parity

The embedded HTML/CSS/JS web UI at `http://localhost:7654/`
now shows the same Downloads-tab information the native GUI
does, plus the same Settings controls.

Downloads panel:

  - Transfer speed (↓/↑ B/s) in the meta line when any rate
    is non-zero.
  - Signed-publisher badge: ★ for trusted, ✓ for signed-
    untrusted. Tooltip shows the full pubkey.
  - Per-row buttons for **files** and **index: on/off**.
  - Files dialog (modal) with per-file progress + priority
    (none/normal/high) buttons + Select All / Deselect All
    bulk actions.

Settings panel gains two new fieldsets:

  - **Bandwidth limits** — download/upload KiB/s entries
    backed by new endpoints `GET/POST /config/rate-limit`.
  - **Queue** — max-active-downloads entry backed by
    `GET/POST /config/queue`.

New HTTP endpoints:

  - `GET /config/rate-limit` / `POST /config/rate-limit`
    `{upload_bps, download_bps}` — both 0 = unlimited.
  - `GET /config/queue` / `POST /config/queue`
    `{max_active_downloads}` — 0 = unlimited.

CSS additions: `.signed` / `.signed-trusted` badges, modal
overlay + dialog, files-table with path / size / progress /
priority columns.

## v0.5.0 — 2026-04-13

**Highlight: publisher trust.** Builds on v0.4.0's signed
torrents with a user-managed whitelist: `.torrent` files
signed by a trusted publisher are auto-confirmed to the
known-good Bloom filter as soon as their metadata arrives,
surfaced with a star badge in the Downloads table, and
available via a new context menu for one-click trust /
revoke.

Also in this release: three new metadata extractors (MOBI
ebooks, MP3 ID3 tags, JPEG EXIF) pushing the total to 14,
plus a polished Verify Signature dialog.

### Publisher trust — `internal/trust` package

Persistent JSON trust list (default:
`~/.local/share/swartznet/trust.json`). Atomic tempfile+rename
writes. Load at daemon startup, mutate through the Store API.

New API:

  - `trust.LoadOrCreate(path)` returns a `*Store`.
  - `Store.Add/Remove/IsTrusted/Label/List`.
  - `engine.Engine.TrustStore()` accessor.
  - `TorrentSnapshot.TrustedPublisher bool`.

Engine behaviour: when `autoIndex` sees a handle whose
`SignedBy()` is non-empty AND the pubkey is in the trust
store, the torrent's infohash is added to the known-good
Bloom filter immediately — no waiting for download
completion.

CLI:

  - `swartznet trust list [--json] [--file path]`
  - `swartznet trust add <pubkey> [<label>]`
  - `swartznet trust remove <pubkey>`

GUI:

  - Downloads "Signed" column shows ★ prefix for trusted
    publishers, ✓ prefix for signed-but-not-trusted, — for
    unsigned.
  - Right-click on a signed torrent: "Verify signature..."
    opens a form dialog with the full pubkey, trust status,
    trust label, and signed infohash. A separate menu item
    toggles trust ("Trust this publisher" /
    "Revoke trust for this publisher").

HTTP API: `GET /torrents` items gain optional
`trusted_publisher bool`.

Five trust-store tests; the signature-dialog integration is
covered by the existing engine signing round-trip tests.

### New metadata extractors

  - **MOBI** (`internal/indexer/extractors/mobi.go`) —
    pure-Go reader for Amazon Kindle .mobi / .azw / .azw3
    metadata. Walks the PalmDB + MOBI header, extracts the
    full-title field, parses EXTH records for author,
    publisher, description, ISBN, subject, published date,
    language. 3 tests with a synthetic MOBI byte stream.
  - **ID3** (`internal/indexer/extractors/id3.go`) — ID3v2.3
    and ID3v2.4 tag reader for MP3 files. Surfaces TIT2
    title, TPE1 artist, TALB album, TDRC/TYER year, TCON
    genre, TRCK track, TPUB publisher, COMM comment, USLT
    lyrics. Handles all four ID3 encodings (ISO-8859-1,
    UTF-16 BOM, UTF-16 BE, UTF-8). 4 tests.
  - **EXIF** (`internal/indexer/extractors/exif.go`) — JPEG
    APP1 EXIF reader. Walks the TIFF header + IFD0,
    extracts camera make/model, software, artist,
    description, copyright, date taken, and GPS coordinates
    (if present). 3 tests.

The existing `TestDispatchRefusesBinary` updated: .jpg now
dispatches to the EXIF extractor (correctly — for metadata,
not pixel data). .mkv still returns nil.

**Total extractors now: 14** — plaintext, subtitle, PDF,
EPUB, DOCX, ODT, RTF, archive, FB2, PPTX, ODP, MOBI, ID3,
EXIF.

## v0.4.0 — 2026-04-13

**Highlight: signed torrents.** SwartzNet can now sign and
verify `.torrent` files with ed25519 publisher signatures.
Downloads coming from a trusted publisher (anyone whose pubkey
you know) can be attributed with cryptographic certainty;
vanilla BitTorrent clients ignore the signature fields and
treat the `.torrent` as any other — full wire compatibility.

Also in this release: three new content extractors pushing the
total to 11 (FB2, PPTX, ODP join plaintext, subtitle, PDF,
EPUB, DOCX, ODT, RTF, archive).

### Signed torrents — `internal/signing` package

Two new optional top-level fields are added to the .torrent
metainfo dictionary:

  - `snet.pubkey`  32-byte ed25519 public key
  - `snet.sig`     64-byte ed25519 signature

The signature payload is `"SN-TORRENT-V1|" || <infohash>`.
The domain prefix prevents signature reuse across future
uses of the same key; the infohash binds the signature to
the content.

Compatibility: every other BitTorrent client (qBittorrent,
Transmission, libtorrent, anacrolix/torrent itself, etc.)
already ignores unknown top-level metainfo fields. A signed
`.torrent` downloads normally in every client; only SwartzNet
reads the signing fields.

New API:

  - `internal/signing.SignBytes(raw, priv) ([]byte, error)`
  - `internal/signing.VerifyBytes(raw) (Signature, error)`
  - `internal/signing.SignFile(path, priv) error`
  - `internal/signing.VerifyFile(path) (Signature, error)`
  - `engine.CreateTorrentOptions.SignWith ed25519.PrivateKey`
  - `engine.Handle.SignedBy() string`
  - `engine.TorrentSnapshot.SignedBy string` (hex pubkey)

CLI:

  - `swartznet create --sign` signs with the node's identity
    (loaded from `~/.local/share/swartznet/identity.key` by
    default; override with `--identity <path>`).

GUI:

  - Create Torrent dialog gains a "Sign with my ed25519
    identity" checkbox (enabled by default).
  - Downloads table gains a "Signed" column showing "✓ <prefix>"
    for verified signatures or "—" for unsigned.
  - Right-click context menu gains a "Copy publisher pubkey"
    action when the torrent is signed.

HTTP API:

  - `GET /torrents` response items gain an optional
    `signed_by` field containing the 64-char hex pubkey.

Five signing tests plus two engine integration tests cover
round-trip, unsigned files, tampered content, file-on-disk
round-trip, and the pubkey-hex encoder.

### New content extractors

  - **FB2** (`internal/indexer/extractors/fb2.go`) — pure-Go
    XML walker for FictionBook 2.x ebooks. Extracts body
    paragraphs + titles, skips `<binary>` cover art and
    `<stylesheet>`. Permissive charset handling so
    windows-1251-declared documents don't bounce out.
  - **PPTX** (`internal/indexer/extractors/pptx.go`) —
    PowerPoint presentations. Iterates
    `ppt/slides/slideN.xml` in numeric order, pulls text
    from every `<a:t>` element. Mirrors the DOCX design.
  - **ODP** (`internal/indexer/extractors/odp.go`) —
    LibreOffice Impress presentations. Reuses the ODT
    extractor's XML walker (same `<text:p>` / `<text:h>` /
    `<text:span>` shape).

Seven new extractor tests covering all three formats plus
dispatch-by-extension checks.

## v0.3.3 — 2026-04-13

GUI polish pass #2 plus two new text extractors — shipped the
same day as v0.3.2.

### GUI polish

- **Files dialog sort:** new "Sort by" dropdown (index /
  path / size / progress / priority) in the per-torrent Files
  modal. Persists across the 2-second poll refresh.
- **Search in-flight indicator:** `ProgressBarInfinite`
  appears under the status label while any of the Local /
  Swarm / DHT layers is running. Visible feedback that the
  query is in progress, especially important for swarm and
  DHT queries that can take seconds.
- **Torrents card on Status tab:** new card at the top of
  the Status grid showing total count, counts broken down by
  status (downloading / seeding / queued / paused), plus
  aggregate download and upload throughput.

### New content extractors

- **RTF** (`internal/indexer/extractors/rtf.go`): pure-Go
  parser for the subset of RTF used by every mainstream
  generator (Word, LibreOffice, Apple TextEdit, Pages export).
  Strips control words, groups, and common destinations
  (fonttbl / stylesheet / colortbl / info / pict / bin /
  header / footer / ...); decodes `\uN` Unicode escapes and
  `\'XX` hex escapes; emits `\par` as newline, `\tab` as tab.
  Claims by MIME (application/rtf, text/rtf) or `.rtf`
  extension. 4 tests.
- **Archive** (`internal/indexer/extractors/archive.go`):
  indexes the *file names* inside ZIP / TAR / TAR.GZ / TGZ
  archives, sorted and newline-joined. Lets searches match
  "changelog.md" inside a source tarball without unpacking.
  Pure-Go stdlib (archive/zip, archive/tar, compress/gzip).
  Detects format via magic bytes. 4 tests.

## v0.3.2 — 2026-04-13

Quality-of-life point release on top of v0.3.1. Every item is
small in isolation but the cumulative effect is that the GUI
now feels like a real desktop app rather than a proof of
concept.

### GUI polish

- **Transfer speed:** new ↓/↑ columns in the Downloads table
  showing per-torrent bytes/sec. Window title shows aggregate
  throughput when transfers are active.
- **Sortable columns:** click any Downloads column header to
  sort ascending; click again for descending; a third click
  clears the sort. An arrow (▲/▼) marks the active column.
- **Keyboard shortcuts:** `Ctrl+N` opens the Add Magnet
  dialog, `Ctrl+F` switches to Search and focuses the query
  entry, `Ctrl+Q` quits, `Delete` removes the selected
  torrent.
- **Persistent window size:** Fyne Preferences stores the
  window dimensions on close and restores them on next
  launch.
- **Empty state:** "No torrents yet" message replaces the
  previously blank Downloads table for new installs.
- **Row-header bug fix:** Fyne's `NewTableWithHeaders`
  exposes both a column-header row and a row-header column.
  The row-header cells used to show the CreateHeader
  placeholder text "Header"; they now render blank.
- **Resizable window fix:** the GUI advertised a 1110×1216
  minimum size to the window manager (the sum of every tab's
  content minimum size). On a 1366×768 laptop screen, the WM
  correctly refused to shrink below that minimum, which users
  reported as "the resize cursor appears but nothing
  happens". Fix: wrap each tab's content in a scroll
  container. Minimum drops to 209×33; scroll bars appear
  automatically when the content is bigger than the window.

### New GUI flags

- `--torrent <path.torrent>` — repeatable, loads the given
  `.torrent` file at startup. Useful for demos, scripted
  reproductions, and screenshots.
- `--tab <downloads|search|status|companion|settings>` —
  opens on a specific tab instead of the default Downloads.

### README overhaul

README is now product-focused: hero paragraph, feature
bullets, install (pre-built binaries + build-from-source),
quick-start (add/search/create/GUI), three-frontends table,
documentation pointers, configuration, non-goals, dev/license.
The previous 24-row milestone matrix moved to
[docs/MILESTONES.md](docs/MILESTONES.md). New
[docs/README.md](docs/README.md) indexes every document by
audience. Three native GUI screenshots under
`docs/screenshots/` illustrate the Downloads, Status, and
Settings tabs.

### CI/CD

- `.github/workflows/test.yml` runs `go mod tidy` check,
  `gofmt -l -s`, `go vet`, and `go test -race` on every
  push to main and every PR. Excludes `internal/testlab/`
  (integration tests that are flaky on shared runners).
- `.github/workflows/release.yml` fires on every `v*` tag,
  builds five CLI binaries (linux amd64/arm64, darwin
  amd64/arm64, windows amd64) and four GUI binaries (linux,
  darwin amd64 via cross-compile from arm64, darwin arm64,
  windows), regenerates SHA256SUMS, extracts the matching
  CHANGELOG section into release notes, and publishes the
  GitHub Release. Prerelease flag is automatic for v0.* tags.

## v0.3.1 — 2026-04-13

Feature-packed point release on top of v0.3.0. Six new user-
facing capabilities, a product-focused documentation rewrite,
and a full GitHub Actions CI/CD pipeline so future tags build
cross-platform binaries automatically.

**Highlights:** file selection for multi-file torrents,
bandwidth rate limits, download queue with concurrency cap and
reorderable queue positions, CLI parity for the v0.3.0 features
(`create` / `index` / `files`), right-click context menu in the
Downloads tab, and — for the first time in any SwartzNet
release — pre-built GUI binaries for **macOS (Intel + Apple
Silicon) and Windows** alongside the existing Linux GUI binary.

### File selection for multi-file torrents

- New `Engine.TorrentFiles(infoHashHex) ([]FileSnapshot, error)`
  returns a per-file view (path, size, bytes completed, progress,
  priority).
- New `Engine.SetFilePriority(ih, fileIndex, priority)` flips a
  single file between "none", "normal", and "high". Takes effect
  immediately even on an already-downloading torrent.
- New `Engine.autoDownload` goroutine: after metadata arrives,
  every file is set to Normal priority so the GUI flow matches
  CLI behaviour. The CLI's existing `DownloadAll()` call stays
  as a harmless duplicate.
- New HTTP endpoints:
  - `GET /torrents/{infohash}/files` — per-file snapshot list.
  - `POST /torrents/{infohash}/files/{index}/priority` —
    `{"priority": "none"|"normal"|"high"}`.
- GUI Downloads toolbar gains a "Files..." button that opens a
  modal with a live-updating list of every file in the selected
  torrent: path, size, progress bar, and a per-file priority
  dropdown. "Select All" / "Deselect All" bulk actions at the
  top. Polls every 2 s while open.
- Two new engine tests:
  `TestTorrentFilesAndSetPriority`,
  `TestTorrentFilesUnknownInfohash`.

### Bandwidth rate limits

- The Engine now installs mutable `*rate.Limiter` instances
  (from `golang.org/x/time/rate`) on the anacrolix client's
  `UploadRateLimiter` / `DownloadRateLimiter` fields. Defaults
  to `rate.Inf` (unlimited). Users can tune limits at runtime
  without restarting the client.
- New `Engine.SetUploadLimitBytesPerSec(bps)`,
  `SetDownloadLimitBytesPerSec(bps)`,
  `UploadLimitBytesPerSec() int64`,
  `DownloadLimitBytesPerSec() int64`. Zero or negative bps
  disables the cap.
- GUI Settings tab gains a new "Bandwidth Limits" card with
  two numeric entries (KiB/s) and an Apply button. Current
  limits are read on tab open; Apply updates the limiter in
  place so every active peer connection sees the new rate.
- Two new engine tests: `TestRateLimitDefaultsUnlimited`,
  `TestRateLimitSetAndGet`.

### CLI parity for v0.3.0 features

Three new `swartznet` subcommands so scripting against the
daemon doesn't need the GUI:

- `swartznet create <path> -o out.torrent [flags]` — build a
  new `.torrent` from local content. Spins up a headless
  engine (no DHT, no upload unless `--seed`) just long enough
  to hash pieces and write the file. Flags: `--tracker URL`
  (repeat), `--webseed URL` (repeat), `--piece-kib N`,
  `--private`, `--comment STR`, `--name STR`, `--seed`,
  `--data-dir PATH`.
- `swartznet index <infohash> on|off [--api-addr]` — flips
  the per-torrent indexing toggle on a running daemon via
  `POST /torrents/{ih}/indexing`.
- `swartznet files <infohash> [--json] [--api-addr]` and
  `swartznet files <infohash> <index> <priority>` — lists
  every file in a torrent with priority + progress (table or
  JSON), or flips a single file's priority to none/normal/high.

### Right-click context menu in Downloads

GUI right-click on any row in the Downloads table opens a
context menu operating on the selected row:

  - Files…
  - Pause / Resume (contextual, based on current state)
  - Remove
  - Stop / Start indexing (contextual)
  - Copy magnet link (to system clipboard)
  - Copy infohash

The menu is implemented via a small `SecondaryTappable` wrapper
around the table (Fyne doesn't expose per-cell secondary-tap
events directly).

### Queue management

anacrolix/torrent has no built-in "max N active downloads"
concept. SwartzNet now layers a simple FIFO queue on top so
users can cap concurrency (matching qBittorrent's behaviour):

- `Engine.MaxActiveDownloads() int` / `SetMaxActiveDownloads(n)`.
  Zero = unlimited (the default and previous behaviour).
- New `Handle.IsQueued()` and `TorrentSnapshot.Queued bool`
  surface the "waiting for a slot" state.
- Queued torrents still fetch metadata and run the indexing
  pipeline; only the file-priority flip (to PiecePriorityNormal)
  is deferred.
- Pause / remove / completion hooks call `promoteQueuedLocked`
  to fill the freed slot with the oldest queued torrent.
- Raising the cap at runtime immediately promotes everything
  that was waiting.
- GUI Settings tab gains a new "Queue Management" card with a
  single numeric entry + Apply button. Current cap is read on
  tab open.
- Four new engine tests:
  `TestMaxActiveDownloadsDefaultsUnlimited`,
  `TestMaxActiveDownloadsClampsNegative`,
  `TestQueueOrderThirdTorrentQueuedUnderCap2`,
  `TestQueueRaisingCapPromotesQueued`.

Still pending:

- **Cross-platform GUI release** (darwin + windows GUI
  binaries via `fyne-cross` once Docker is available on the
  build machine).

## v0.3.0 — 2026-04-12

**Highlight:** SwartzNet now ships a **native Fyne GUI** as a
third frontend alongside the CLI and the web UI. All GUI code
is Go — no HTML/CSS/JS. The same daemon (engine, indexer,
companion, HTTP API) powers all three. Two other big features
land in this release: **per-torrent indexing control** and
**torrent creation from local content**.

### G0–G7 — Native Fyne GUI (v0.3.0)

- **G0**: Extracted engine+indexer+companion+httpapi wiring from
  `cmd/swartznet/cmd_add.go` into a new `internal/daemon/`
  package. `Daemon` struct with `New(ctx, opts)` / `Close()` is
  now shared by both CLI and GUI. `controllerAdapter` and
  `companionAdapter` moved from `cmd/swartznet/` to
  `internal/daemon/adapters.go`. Three new tests
  (`TestDaemonStartStop`, `TestDaemonNoIndex`, `TestDaemonWithAPI`)
  all pass under `-race`. CLI behavior unchanged.
- **G1**: New `cmd/swartznet-gui` entry point and
  `internal/gui/` package. Fyne v2.7.3 (BSD 3-Clause) chosen over
  Wails/Gio because it is pure Go for the UI layer. Window with
  AppTabs layout. Downloads tab: `widget.Table` polling
  `engine.TorrentSnapshots` every 2 s via `fyne.Do()`. Add
  magnet dialog; file picker for `.torrent`. Pause/resume/remove
  buttons.
- **G2**: Search tab with Local / Swarm / DHT checkboxes, all
  three layers fanned out in parallel via goroutines. Results as
  cards with Confirm / Flag buttons using the same source
  attribution logic as the HTTP API handler.
- **G3**: Status tab — adaptive grid of Card widgets
  (local index, swarm peers, DHT publisher, Bloom filter) plus a
  reputation list. 4-second refresh, matches web UI cadence.
- **G4**: Companion tab — publisher status card with Refresh
  Now button, plus a follow-list List widget and a pubkey +
  label form.
- **G5**: Settings tab — sharing-level RadioGroup (L0 / L1 / L2),
  file/content hit Checks, save button calls
  `swarmsearch.Protocol.SetCapabilities`.
- **G6**: System tray via `desktop.App` assertion. Tray menu:
  Show, Add Magnet, About, Quit. Close intercept minimises to
  tray when available, otherwise quits. Download-complete OS
  notifications via `app.SendNotification`. About dialog shows
  version, identity pubkey, listen port, HTTP API address.
  `//go:embed` PNG icon.
- **G7**: `scripts/build-gui.sh` for native builds with CGo +
  trimpath + stripped symbols (~46 MB). Docs in
  `docs/08-operations.md#native-gui-v030` covering
  dependencies and the `fyne-cross` Docker path for release
  builds on all 5 platforms (linux-amd64/arm64,
  darwin-amd64/arm64, windows-amd64).

**Trade-off accepted:** Fyne needs a CGo toolchain, so the GUI
binary is not statically linked and can't be cross-compiled from
a vanilla Go toolchain. The CLI continues to build with
`CGO_ENABLED=0` and stays ~40 MB with the existing
`build-release.sh` pipeline.

### G8 — Per-torrent indexing control

- New `Handle.IsIndexing() bool` and `Engine.SetTorrentIndexing(
  hex, enabled) error` let the user decide, per torrent, whether
  file downloads feed the extraction pipeline and whether the
  torrent-level document is written to Bleve. Default remains on
  — existing behaviour is preserved.
- `TorrentSnapshot` gains an `Indexing bool` field, mirrored
  through the httpapi struct as `"indexing":bool` so the web UI
  can surface it in future work.
- New HTTP endpoint `POST /torrents/{infohash}/indexing` with
  body `{"enabled": true|false}`.
- GUI Downloads tab gains a new "Indexed" column (yes/no) and a
  "Toggle Index" toolbar button. The Add Magnet dialog gains an
  "Index this torrent's files after download" checkbox (default
  on). The `.torrent` file picker keeps indexing on by default;
  the user can toggle afterwards via the toolbar button.
- Two new tests: `TestSetTorrentIndexingUnknownInfohash`,
  `TestSetTorrentIndexingReflectedInSnapshot`.

### G9 — Create Torrent

- New `Engine.CreateTorrent(CreateTorrentOptions) (*metainfo.MetaInfo, error)`
  and `Engine.CreateTorrentFile(opts, outPath) (infohash, mi, error)`
  wrap `metainfo.Info.BuildFromFilePath` plus bencode serialization
  and atomic tempfile+rename for the on-disk variant.
  `CreateTorrentOptions` exposes: Root (file or folder),
  Name override, PieceLength (0 = Auto via
  `metainfo.ChoosePieceLength`), Trackers, WebSeeds (BEP-19),
  Private (BEP-27), Comment, CreatedBy.
- GUI Downloads toolbar gains a "Create Torrent" button that
  walks the user through every field, with file/folder pickers
  for Root and a Save As… picker for output. "Start seeding
  immediately" calls `AddTorrentMetaInfo` right after the file
  is written.
- Piece hashing runs in a background goroutine; a
  `ProgressBarInfinite` modal stays up until completion so the
  UI never blocks.
- Four new tests: `TestCreateTorrentSingleFile`,
  `TestCreateTorrentMultiFile`, `TestCreateTorrentFileWritesValid`,
  `TestCreateTorrentMissingRoot`.

### G10 — Documentation refresh

- `docs/05-integration-design.md` §2 updated with the v0.3.0
  architecture diagram (three frontends + `internal/daemon`
  layer), plus new sections on per-torrent indexing control and
  torrent creation.
- `docs/08-operations.md` gains "Creating a new torrent" and
  "Per-torrent indexing control" subsections explaining every
  dialog field and the two paths for opting a torrent out.
- `README.md` gains a "Three frontends" table and status-matrix
  entries for G0-G9.

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
### M13 — v1.0.0 blocker research follow-through

The research pass over the six v1-blocking open questions in
`docs/05-integration-design.md` §13 produced a report
(`docs/09-v1-blocker-research.md`) plus a set of concrete action
items. Nothing turned out to require a protocol redesign; the
below commits are the straightforward follow-through.

- **M13a — `THIRD_PARTY_LICENSES` + PDF attribution fix**: Audit
  of the extractor dependency tree confirmed the v1 hot path is
  license-clean (BSD-3-Clause for `ledongthuc/pdf` and
  `golang.org/x/net`, stdlib for everything else). The only
  finding was a mis-attribution — the project docs called
  `ledongthuc/pdf` MIT-licensed; the upstream `LICENSE` file is
  actually BSD-3-Clause (© The Go Authors, inherited from
  `rsc/pdf`). New `THIRD_PARTY_LICENSES` file lists every heavy
  dependency with its full notice text; `README.md` and
  `internal/indexer/extractors/pdf.go` updated to call it
  BSD-3-Clause.
- **M13b — publisher `MinPutInterval` hard cap**: v1 blocker 2
  research noted `anacrolix/dht/v2` has no default rate cap on
  concurrent mutable-item puts, so the client must enforce its
  own per-keyword budget to avoid self-DoS'ing the publisher.
  `dhtindex.PublisherOptions` gains a `MinPutInterval` field
  (default 55 minutes), and `publishOne` now short-circuits if
  the keyword was published less than `MinPutInterval` ago,
  regardless of whether the trigger was a `Submit()` or a
  refresh tick. `TestPublisherMinPutIntervalThrottles` covers
  the new path.
- **M13e — chunker shrink (10 KiB → 2 KiB)**: v1 blocker 1
  research converges on 0.5–4 KiB for content-chunk targets
  (Elastic's docs default to ~250 words ≈ 1.25 KiB; production
  RAG/BM25 stacks sit at 1–2 KiB). SwartzNet was using 10 KiB,
  an order of magnitude above the sweet spot. Shrinking
  `DefaultChunkTargetBytes` improves BM25 relevance per-hit and
  tightens highlight fragments at a small index-size cost.
- **M13c — seed reputation list + decaying bonus**: v1 blocker 4
  research recommended a signed/versioned seed list of ~20
  curated pubkeys with an exponentially-decaying score boost
  (90-day half-life) so organic reputation dominates after one
  quarter. `reputation.Counters` gains `SeededAt` + `SeedLabel`;
  `scoreOf` adds `SeedBonus × 2^(-age/SeedHalfLife)` on top of
  the organic Bayesian score. New `MarkSeeded`, `IsSeeded`,
  `AnySeeded`, and `LoadSeedList` methods; new `SeedList` JSON
  schema with `version` gate. A fresh seed scores ~0.95, well
  above any reasonable `MinIndexerScore` cutoff, so the existing
  `Threshold` pre-filter in `dhtindex.Lookup` gets heavy-tail
  semantics for free (a bootstrap node with zero traffic still
  passes the cutoff if it's in the seed list). New
  `config.SeedListPath` (default `~/.local/share/swartznet/seeds.json`)
  is loaded by `engine.New` after `LoadOrCreateTracker`. Three
  new tests cover the bypass, the 90-day decay via backdated
  `SeededAt`, and the JSON loader (including malformed
  entries). The actual shipping seed list file is not bundled —
  distribution is a post-v1 operational decision.

- **M13d — privacy & threat model (blocker 6)**: The original
  research recommendation was "SOCKS5 for the put path", but
  BEP-44 is UDP and SOCKS5/Tor don't cleanly carry UDP — so
  the v1 response is the honest subset of that plan instead.
  New "Privacy and threat model" section in
  `docs/08-operations.md` enumerates exactly what's visible
  (IP → ~8 closest DHT nodes; stable pubkey; hourly timing
  fingerprint; BEP-42 geographic bias) and exactly what isn't
  (downloads, local queries, companion contents, web UI).
  Shipping mitigations: `--no-dht` (full disable) and the new
  `--no-dht-publish` / `cfg.DisableDHTPublish` leech-only mode
  that keeps the node on the DHT for gets + companion
  pointers but skips every outbound `put`.
  `dhtindex.KeywordValue` gains an optional `NextPubKey`
  bencode field — v1.0.0 ships the field on the wire but
  never populates it; the rotation logic is scheduled for
  v1.1 so future clients can adopt it without a format bump.
  For real publisher anonymity, users layer their own Tor /
  VPN / i2p on top, as documented.

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
- **M12f — per-peer sn_search rate limiter**: Design doc §5.4
  asks for rate limiting on inbound `sn_search` queries. A noisy
  peer used to be able to DoS the Bleve query path; this commit
  adds a token-bucket per peer (default 5 q/s steady, burst 10)
  that gets a `RejectRateLimited` reply when over quota. Runtime
  configurable via `Protocol.SetRateLimit`. Per-peer buckets are
  evicted in `OnPeerClosed` so long-running daemons don't leak.
  Six unit tests on the bucket math plus one end-to-end
  `TestHandleInboundRateLimit` through the full `HandleMessage`
  path (including peer isolation). All pass under `-race`.
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
