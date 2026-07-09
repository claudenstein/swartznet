# SwartzNet Rebuild Plan

> Companion to `ARCHITECTURE.md`. This is the build order: a sequence of vertical slices, each of which ships a **working, observable capability** and is independently testable. The plan is the operational contract for the rebuild; where it and `ARCHITECTURE.md` disagree, the architecture wins and this plan is corrected.

---

## How to use this plan

Work **one slice at a time**, tests-first, and **stop for review between the numbered slices** (each slice is a natural PR boundary). The loop for every slice is:

1. **Read the behavioral references first.** For any behavior that must match legacy, open the named `legacy-snapshot` file and extract the *observable* contract (inputs → outputs, wire/disk bytes, error taxonomy, ordering). Preserve the observable behavior; **write clean new code** against the module boundaries in `ARCHITECTURE.md` — never copy legacy structure, imports, or duplicated derivations.
2. **Write the frozen contract / golden vectors before the logic** for any slice that touches `contracts/*`. A second-language implementation must be reproducible from the contract package + its fixtures alone.
3. **Build the slice to its Definition of Done** — an actual command or wire observation, not "tests pass."
4. **Assert the negative:** every slice lists the SPEC §6 defects it must *not* reproduce and the SPEC §5 rules it must *preserve*; add a test that fails if the defect returns.
5. **Rebuild both binaries** (`dist/swartznet`, `dist/swartznet-gui-dev-linux-amd64`) and run `go test ./... -count=1 -short` then `go test -race ./...` before declaring the slice done.

Where a slice exhausts, pivot to the next unblocked slice rather than pausing.

---

## Milestones (ordered slices)

Legend for **Behavioral references**: SPEC §§ are the source of truth; `legacy:` paths are read-only behavioral references on branch `legacy-snapshot`.

---

### Slice 0 — Walking skeleton: one daemon, one frontend, saying hello

- **Goal:** the lifecycle spine everything grows from. SPEC §5.10 has **no `serve`/`daemon` subcommand** — `swartznet add` *is* the daemon — so Slice 0 stands up that spine behind a **temporary `serve` scaffold** (there is no engine to `add` yet): `serve` constructs a `daemon.New(ctx, Options)`, binds `localhost:7654`, serves `GET /status` (JSON) + a stub embedded web page, and shuts down in reverse order on Ctrl-C (exit 130). `swartznet status` is a **thin HTTP client** from day one — it GETs `/status` from a running daemon and prints it; it does **not** start its own daemon (matching ARCHITECTURE assumption I51, add-is-daemon). **Slice 2 deletes `serve` and folds the spine into `add`.** Nothing torrents yet.
- **Modules touched:** `daemon`, `config`, `httpapi` (minimal: `/status`, embed skeleton), `cmd/swartznet` (dispatch + thin-client `status` + the **temporary** `serve` scaffold, removed in Slice 2), logging (`slog`→stderr, `SWARTZNET_LOG`).
- **Depends on:** nothing.
- **Behavioral references:** SPEC §2.1 (daemon lifecycle, config/paths), §2.9 (`/status`), §0 invariant #3 (loopback-only, no auth) and #4 (one constructor). `legacy: internal/daemon/daemon.go`, `internal/config/`, `internal/httpapi/server.go`, `cmd/swartznet/main.go`.
- **Definition of done:** `dist/swartznet serve &` (temporary scaffold) then `curl -s localhost:7654/status | jq .` returns valid JSON, and `swartznet status` (thin HTTP client, not its own daemon) prints the same; `curl` with a spoofed `Origin: http://evil.example` is rejected by the CSRF/DNS-rebind guard while `Origin: http://localhost:7654` passes; binding to a **non-loopback** address still **binds but emits a loud one-time "API is UNAUTHENTICATED" warning** (loopback-default + CSRF is the security model, not a hard refusal — SPEC §2.9/§5.9); `SIGINT` produces reverse-order teardown in logs and exit 130; `SWARTZNET_LOG=debug` changes verbosity.
- **§6 defects it must NOT reproduce:** the two-name undocumented unsafe gate — ship the single `SWARTZNET_UNSAFE=1` + `testing.Testing()` gate from day one; undocumented `SWARTZNET_LOG` — document it.
- **§5 rules it must preserve:** empty-path = feature-off; `Validate()` creates only `DataDir` (0755) and `IndexDir`'s parent; `httpapi` imports **zero** subsystems (all collaborators are locally-declared interfaces; nil ⇒ 503); `Close` cancels+joins background context **before** subsystem teardown.

---

### Slice 1 — Persistent identity

- **Goal:** the daemon loads-or-creates `identity.key`, `/status` now reports the real publisher pubkey hex, and the key survives restart.
- **Modules touched:** `identity`, `daemon` (wire `Signer` + set `Options.PublisherPubKey`), `httpapi` (`/status` pubkey field).
- **Depends on:** Slice 0.
- **Behavioral references:** SPEC §2.8 (identity persistence), §5.6 (exact-0600, seed→pubkey re-derivation), §0 invariant #2. `legacy: internal/identity/`.
- **Definition of done:** first `serve` creates `~/.local/share/swartznet/identity.key` at mode **exactly 0600**; a file at 0400 is **rejected** (not silently accepted); `chmod 0644` then restart is rejected; passing `--identity /path` load-only refuses to create; `/status` shows the same pubkey across restarts; deleting the key and restarting creates a *new* pubkey only at the default path.
- **§6 defects it must NOT reproduce:** `/status` publisher.pubkey never populated — set `Options.PublisherPubKey` from `identity.PublicKeyHex()`.
- **§5 rules it must preserve:** never regenerate implicitly; auto-create **only** at the default XDG path (guarded by the `IsDefaultPath` flag from `Load`); public half re-derived from seed and checked on load.

---

### Slice 2 — Add + download + seed (a usable BitTorrent client)

- **Goal:** `swartznet add <magnet|.torrent|40-hex|->` downloads to `DataDir`, `swartznet status` shows real percentages, and a completed torrent seeds. This is the first slice that is a *usable client*.
- **Modules touched:** `engine` (anacrolix `Client`+DHT via extension APIs only, `AddService`, queue, session persist/restore, `fileTracker`, per-file priority, rate limits, `VerifyData`, `FilePathMaker`), `contracts/bencode` (raw-bytes metainfo parse for `.torrent` add), `daemon` (engine wiring + adapters), `httpapi` (`/add`, `/downloads`, `/config/rate-limit`, `/config/queue`), `cmd/swartznet` (`add` — now the daemon, **folding in and deleting the Slice-0 `serve` scaffold**; thin-client `status`, `files`).
- **Depends on:** Slices 0–1.
- **Behavioral references:** SPEC §2.2 (engine), §5.4 (anacrolix quirks: positive burst floor, `PeerStore` write tokens, `Exp=2h` pin, per-file-priority activation, seed-in-place `FilePathMaker` on real basename, background `VerifyData`, magnet→metainfo upgrade guard), §5.5 (event fan-out). `legacy: internal/engine/engine.go`, `engine_session.go`, `engine_files.go`, `cmd/swartznet/cmd_add.go`, `cmd_status.go`, `cmd_files.go`.
- **Definition of done:** in the in-process multi-engine harness, engine A creates+seeds a fixture and engine B `add`s the magnet and completes; `swartznet add` of a bare 40-hex string is treated as an infohash add (not a file path); `status` shows a non-zero percentage on a fresh seed *before any peer connects* (proves `VerifyData`); a restart restores the session and resumes; `PATCH /config/rate-limit` with only `down` set leaves `up` intact; an all-`none`-priority torrent frees its queue slot.
- **§6 defects it must NOT reproduce:** queue promotion coupled to the Bloom gate (a nil Bloom must not strand a completed slot — promotion is decoupled here even though Bloom arrives in Slice 5); `countActiveDownloads` ignoring file priorities; `/config/rate-limit` zeroing the unset field; "downloading 0%" from seeding under the wrong basename (seed-in-place on the real basename).
- **§5 rules it must preserve:** integrate anacrolix **only** through extension APIs — never patch the vendored lib (MPL); per-file Normal-priority flip on `GotInfo` (not `DownloadAll`); exactly-one file-complete event, fanned out via the 64-slot replay buffer; attacker-controlled infohash adds wrapped in `recover`; magnet→metainfo upgrade guard.

---

### Slice 3 — Create + infohash-preserving signing

- **Goal:** `swartznet create <path>` writes a `.torrent`; `swartznet trust`-signed torrents carry top-level `snet.pubkey`/`snet.sig`; a signed and unsigned twin of the same content share **one infohash**.
- **Modules touched:** `signing`, `contracts/sign`, `contracts/bencode` (extend), `engine` (create path + verify-at-add), `cmd/swartznet` (`create`), `daemon` (surface `SignedBy` on the handle).
- **Depends on:** Slices 1–2.
- **Behavioral references:** SPEC §2.3 (signing model), §5.6 (`ErrNotSigned` vs `ErrBadSignature`), §7-D20 (bad-signature add-anyway). `legacy: internal/signing/`, `internal/engine/engine_create.go`.
- **Definition of done:** `create` then `create --sign`; both `.torrent`s hash to the same infohash (compare `SHA1(info)`); `Verify` returns the pubkey on the signed one, `ErrNotSigned` on the plain one, `ErrBadSignature` on a byte-flipped one; adding a bad-signature torrent still adds the torrent with **empty `SignedBy`** and logs `ErrBadSignature`; the info dict round-trips byte-identical (proves `map[string]bencode.Bytes` pass-through).
- **§6 defects it must NOT reproduce:** none new; establishes the taxonomy other slices depend on.
- **§5 rules it must preserve:** never round-trip the info dict through a typed struct; sign over the 34-byte `"SN-TORRENT-V1|"‖SHA1(info)` payload; add-anyway on bad signature (assumption D20).

---

### Slice 4 — Layer L: local full-text search

- **Goal:** downloaded content is extracted, chunked, and indexed into Bleve; `swartznet search <query>` and `POST /search` (local layer only) return real hits with highlighted snippets; `swartznet index` reports stats.
- **Modules touched:** `indexer` (schema v3, ingestion pipeline, extractor registry, 2 KiB chunker, 60 s watchdog, `SearchResponse`, `PickLookupToken`), `indexer/extractors/*` (pdf/epub/docx/odt/plaintext/subtitle/**ZIM**), `contracts/token`, `searchmux` (local-only tuple for now), `engine` (fan file-complete events → pipeline; `ReadSeekerAt` shim), `httpapi` (`/search`, `/index/stats`), `cmd/swartznet` (`search` direct-Bleve, `index`).
- **Depends on:** Slice 2.
- **Behavioral references:** SPEC §2.4 (all of Layer L), §2.6/§2.4 tokenization, §5.5 (schema-mismatch rebuild, dropped-event recovery). `legacy: internal/indexer/`, `indexer/extractors/`, `cmd/swartznet/cmd_search.go`, `cmd_index.go`.
- **Definition of done:** add a fixture PDF + a ZIM archive, wait for completion, `swartznet search <term>` returns a hit with a `<mark>`-highlighted snippet; **ZIM extraction succeeds against the live pipeline** (not only a test `bytes.Reader`) — proves the `Seeker→ReaderAt` shim; `search --signed-by <pk>` uses an exact `TermQuery`; deleting a torrent + "Forget" removes its docs from results; a bumped schema sentinel rebuilds the whole dir; killing a file-complete event still indexes the file within the hourly rescan window (test with a shortened interval).
- **§6 defects it must NOT reproduce:** ZIM never working in the live pipeline; index deletion unreachable (removed torrents searchable forever) — wire a reachable "Forget" action (files kept by default); Layer-D/lookup "first token" bug is pre-empted by making `MostDistinctive` the *only* lookup-token chooser (used later by Slice 10).
- **§5 rules it must preserve:** extractor first-claim-wins by MIME (plaintext declines subtitle types); single-worker extraction under watchdog + panic-recover; chunk overrun bounds (≤1.5× paragraph, ≤1.25× single chunk); per-torrent indexing toggle honored; `indexer` never touches the wire.

---

### Slice 5 — Trust, reputation, Bloom, confirm/flag (one shared path)

- **Goal:** `swartznet trust add/remove/list` manages the allowlist; confirm/flag from CLI, web, and (later) GUI all flow through **one** daemon path; the known-good Bloom and reputation tracker persist and gate rankings.
- **Modules touched:** `trust`, `reputation` (Bayesian tracker, FNV-64a Bloom v1, `SourceTracker`), `admission` (deny-by-default rules, `AdmissionEngine`, permissive policy test-only), `daemon` (`Confirm`/`Flag`), `engine` (auto-Bloom-confirm on completion; **periodic checkpoint** of Bloom+reputation), `httpapi` (`/confirm`, `/flag`, `/aggregate` admission counts), `cmd/swartznet` (`trust`, `flag`).
- **Depends on:** Slices 1, 2, 4.
- **Behavioral references:** SPEC §2.8 (trust/reputation/Bloom/attribution), §5.7 (bootstrap admission), §5.6 (Bloom format v1), §6 (crash loses Bloom/reputation), §7-D21/D22/D23. `legacy: internal/trust/`, `internal/reputation/`, `internal/engine/engine_bloom.go` (or equivalent).
- **Definition of done:** `trust add <pk>` then adding that publisher's signed torrent auto-confirms its infohash into the Bloom; a `/flag` demotes only attributed indexers and is a **no-op against a trusted publisher**; flagging an unattributed hit demotes nobody (fail-closed); a Bloom golden vector matches the frozen FNV-64a layout so an existing `known-good.bloom` still loads; kill -9 after an auto-confirm then restart retains the confirmation (proves the periodic checkpoint); `/aggregate` shows admission counts distinguishing a starved node from a quiet network.
- **§6 defects it must NOT reproduce:** admission admitting any unknown 0.5 pubkey / 3 fresh Sybils clearing endorsement (deny-by-default, permissive constructor test-only); trusted publishers demoted on flag; reputation feedback inconsistent across surfaces; crash losing Bloom/reputation (bounded periodic checkpoint).
- **§5 rules it must preserve:** neutral 0.5 prior weight 5, seed bonus `0.45·2^(−age/90d)`, threshold ≤0/NaN = always pass; `trust.json` atomic per-mutation, lowercased+sorted; attribution forgotten after a flag (no double-dock); `IsTrusted` consulted before any `RecordFlagged`; auto-completion is **not** auto-`RecordConfirmed` (D22).

---

### Slice 6 — Capability mask: the single services-bit producer

- **Goal:** `/capabilities` exposes and mutates operator sharing prefs; the announced 64-bit `sn_search` services mask is produced by exactly one pure function (`capability.Announced`) and reported live at the **HTTP readout**. (The wire half of the headline fix — the outbound `peer_announce` consuming the same producer, so a downgrade actually reaches a peer — is only wired in **Slice 7**; at Slice 6 the single-producer leaf and the HTTP readout are all that exist to test.)
- **Modules touched:** `capability` (`Sharing`, `RuntimeFacts`, `Announced`), `contracts/ltepwire` (services bitfield definition), `httpapi` (`ServicesReporter`, `/capabilities` PATCH-only-`Sharing`), `daemon` (recompute `RuntimeFacts` from live engine state), `engine` (getters feeding `RuntimeFacts`).
- **Depends on:** Slices 0, 5 (Publisher/reconciliation facts exist).
- **Behavioral references:** SPEC §2.5 (negotiation), §2.9 (`/capabilities`), §5.1 (unknown bits ignored; absence of announce = services 0 but still answered), §7-A5, §7-I54. `legacy: internal/swarmsearch/capability*.go`, `internal/httpapi/handlers_capabilities.go`.
- **Definition of done:** `GET /capabilities` reflects config; `PATCH /capabilities` with only `FileHits` toggled leaves the daemon-owned `Publishing` bit untouched (proves `Sharing`/`RuntimeFacts` type split); `/aggregate` reports the **live** mask, not a static `0x2ED`; toggling `--no-index` at start zeroes the Publisher bit in the readout; a golden vector pins `Announced()` output for a fixed (Sharing, RuntimeFacts) pair.
- **§6 defects it must NOT reproduce:** `/aggregate` static `0x2ED` and "Save sharing" clobbering the Publisher bit are **fully closed here** (single producer + type-split + HTTP readout). The third — **capability downgrades never reaching the wire** — is only *genuinely* closed in **Slice 7**, where the outbound `peer_announce` is wired to the same `Announced()` producer; Slice 6 can only prove the leaf and the readout, not the wire.
- **§5 rules it must preserve:** `Announced()` is the **sole** mask producer, consumed identically by the future wire announce and the HTTP readout; unknown/future bits ignored never rejected.

---

### Slice 7 — Layer S: `sn_search` peer-wire extension + wire-compat gate

- **Goal:** two SwartzNet peers negotiate `sn_search` in the LTEP `m` dict and answer scoped queries over the wire; a vanilla peer sees **zero** `sn_search` frames; `POST /search` now fans in swarm hits concurrently.
- **Modules touched:** `swarmsearch` (`Handler`, `QueryResponse`, query/result/reject with txid + asked-set anti-spoof, `peer_announce`, `PeerBook`, banman, token bucket, feeler, `LocalSearcher`/`RecordSource`/`RecordSink`/`GossipObserver` ports), `contracts/ltepwire` (envelope codec + golden vectors), `engine` (implements + injects `swarmsearch.Transport.SendExtension(swarmsearch.PeerToken,…)` — the seam type is **declared in `swarmsearch`**, not engine — via `LocalLtepProtocolMap.AddUserProtocol`; mints a `PeerToken` only from a recorded `m`-dict advertisement, **not** `peer_announce`; async off-read-loop dispatch with dual 256-slot semaphores + payload copy), `searchmux` (add Swarm searcher), `capability` (consume `Announced` for outbound announce), `wirecompat` (`MiniPeer` + in-memory `VanillaPeer` double, promoted to CI gate).
- **Depends on:** Slices 2, 4, 6.
- **Behavioral references:** SPEC §2.5 (all of Layer S), §2.11/§2.12 (wire-compat matrix), §5.1 (never send `sn_search` to a non-advertising peer; reject-code-2 on scope mismatch), §5.4 (async dispatch quirk), §5.9 (Layer-S error → inline string under 200). `legacy: internal/swarmsearch/`, `internal/engine/engine_ltep.go`.
- **Definition of done:** in the harness, peer A queries peer B and gets hits; the in-memory `VanillaPeer` (empty `m` dict) receives **no** extension frame across the four-direction matrix — a `wirecompat` silence assertion, run in CI `-race`; a scoped query to a non-sharing responder returns **reject code 2**; a valid-bencode wrong-shape `query` frame is charged `ScoreBadBencode`; `Hit.N` is truncated to ~60 bytes on the wire; `SendExtension` cannot be called without a `PeerToken` (compile-time — there is no bare-address send API); a real anacrolix v1.61.0 client completes a download from us (local-only interop scenario).
- **§6 defects it must NOT reproduce:** valid-bencode/wrong-shape `query` being free; torrent hits carrying the year-1 zero timestamp (populate a real freshness stamp or omit); `Hit.N` untruncated.
- **§5 rules it must preserve:** frames emitted **only** through the capability-token transport; asked-set anti-spoof on results; banman local and never gossiped; absence of `peer_announce` = services 0 but peer still answered; `swarmsearch` never imports Bleve internals (answers via injected `LocalSearcher`).

---

### Slice 8 — RIBLT sync + Aggregate record substrate

- **Goal:** two peers reconcile their signed-record sets over multi-batch RIBLT so differences >100 symbols converge; the engine mints one signed `contracts/record` per name-keyword into the swarmsearch `RecordCache`; an outbound `StartSync` initiator exists (not test-only).
- **Modules touched:** `contracts/riblt` (FNV-1a-64 nonlinear key, 12-step {2..4096} `contributes()` cycle, golden vectors), `contracts/record` (`SignedRecord`, `SigMessage`, `RecordID` excluding pow/sig), `swarmsearch` (`SyncSession` multi-batch responder + `StartSync`), `engine` (record minting on `GotInfo`), `identity`/`signing` (Signer for record minting).
- **Depends on:** Slices 3, 7.
- **Behavioral references:** SPEC §2.5 (RIBLT sync), §5.1 (two valid signings share one `RecordID`; nonlinear key + modulus cycle are cross-impl wire contracts), §6 (one-batch responder; no production initiator). `legacy: internal/swarmsearch/sync*.go`, `internal/swarmsearch/riblt*.go`.
- **Definition of done:** golden vectors pin the RIBLT coded-symbol bytes and the `contributes()` cycle; two harness peers with a 250-record symmetric difference **converge** (multi-batch responder streams >100 symbols); two distinct valid signings of one semantic record produce the **same** `RecordID` (proves pow/sig exclusion); `StartSync` is reachable from the aggregate backend wiring, not just a test; **the cross-implementation sync-session budget guards hold (SPEC §2.12/§5.1):** per-session `max_symbols`/`max_bytes` are negotiated **downward** (min of the peer's advertised budget and own defaults 2000 symbols / 1 MiB); a symbol-budget overrun ends the session with `sync_end "limit_exceeded"` while any other violation ends it `"aborted"` **plus a misbehavior charge**; byte accounting is on the **semantic record size (32+kw+20+8+8+64)**, never the bencoded byte count; and `sync_symbols.index` MUST equal the receiver's running symbols-in count — any mismatch is a **hard session abort** (encoder/decoder positions desync permanently).
- **§6 defects it must NOT reproduce:** single-batch RIBLT responder capping at ≤100 symbols; no production sync initiator.
- **§5 rules it must preserve:** `RecordID = SHA-256(pk‖kw‖ih‖t_LE)` excluding pow+sig; nonlinear FNV-1a-64 element key; 12-step modulus cycle — all byte-frozen.

---

### Slice 9 — Layer D: BEP-44 keyword index behind the RecordBackend seam

- **Goal:** `swartznet` publishes keyword→infohash BEP-44 items for its torrents and looks them up; `POST /search` now fans in DHT hits; all three layers run concurrently and reconcile only at the edge.
- **Modules touched:** `dhtindex` (`Publisher`, `Lookup`, `LookupResponse`, `RecordBackend` port, `legacyKeyword` impl, `checkPutStats`, BEP-51 `sample_infohashes` primitive), `contracts/dhtschema` (`KeywordValue` codec, 1000-byte pre-unmarshal cap, `EstimateValueSize`), `config` (`ListenHost`/`DisableIPv6`/`DHTBootstrapAddrs` for the isolated regtest DHT), `searchmux` (add DHT searcher — now full 3-layer `errgroup`), `daemon` (select `LayerDMode=legacy`; DTO adapters), `httpapi` (`/search` full, `/publish` status), `cmd/swartznet` (`search` distributed; **`crawl-probe`** one-shot BEP-51 probe).
- **Depends on:** Slices 4 (PickLookupToken), 5 (reputation filter), 8 (records/backend seam shape).
- **Behavioral references:** SPEC §2.6 (all of Layer D), §5.2 (`Exp=2h`, zero-storing-node put = failure, ≥1000-byte reject before unmarshal, ≥12-month legacy-read back-compat), §5.9 (Layer-D error → inline string under 200), §6 (first-token lookup; publishing content tokens). `legacy: internal/dhtindex/`.
- **Definition of done:** with a local regtest DHT (`SWARTZNET_UNSAFE=1`) configured for isolation — **`ListenHost=127.0.0.1`, `DisableIPv6=true`, and a placeholder `DHTBootstrapAddrs=[127.0.0.1:1]`** (without all three, the BEP-44 write-token source-IP check silently rejects puts and/or the cluster leaks onto public routers, so this DoD is unobservable) — node A publishes and node B `search`es and gets the infohash via BEP-44; lookup queries the **most distinctive** token (assert against a crafted multi-token name); only `Tokenize(torrent.Name)` keywords are published — **no content tokens**; a put reaching zero storing nodes surfaces as a failure via the shared `checkPutStats`; a >1000-byte remote value is rejected before unmarshalling; `/search` latency is `~max(layer)` not the sum (measure with all three layers slow); Layer-L error → 500 while Layer-D error → inline string under 200; `swartznet crawl-probe --addr 127.0.0.1:<port> --json` issues a single BEP-51 `sample_infohashes` query from a throwaway `127.0.0.1:0` NoSecurity+Passive DHT server (fresh-random target per run) and prints JSON samples/interval/num/nodes.
- **§6 defects it must NOT reproduce:** lookup querying `tokens[0]`; publishing content tokens to the DHT; `/search` running sequentially.
- **§5 rules it must preserve:** `Exp=2h` pin; hourly re-publish, 55 min/keyword throttle, oldest-hit eviction under 1000 bytes; all record-format specifics behind `RecordBackend`; three response types never merged into a shared hit type; BEP-44 items byte-indistinguishable from any other client's.

---

### Slice 10 — Companion index publish/subscribe

- **Goal:** `swartznet` publishes a compact companion content-index (BEP-46 pointer pattern) and a follower imports a followed publisher's index into its local Bleve.
- **Modules touched:** `companion` (`Publisher`, `Subscriber`, `CompanionIndex`, `PointerPutter`/`Getter`, `CorpusExport`/`Import` ports), `dhtindex` (BEP-46 pointer primitives), `indexer` (import path), `daemon` (companion pub→sub startup order; follows file), `httpapi` (`/companion/*`), `cmd/swartznet` (`companion` / `follow`).
- **Depends on:** Slices 4, 9.
- **Behavioral references:** SPEC §2.7 (companion cycle), §5.3 (empty index = failure; `lastRefresh` advances only on success), §6 (ingest without attribution/verification; no `GeneratedAt` dedup). `legacy: internal/companion/`.
- **Definition of done:** node A publishes a companion index for its corpus; node B `follow`s A's pubkey, resolves the pointer, fetches (fail-closed: 1 file, ≤32 MiB, safe names), and the imported docs appear in B's `search` **stamped with `SignedBy = A's pubkey`**; a snapshot whose `CompanionIndex.Publisher ≠ followed pubkey` is **rejected**; an unchanged snapshot (same `GeneratedAt`) is **not** re-ingested on the next hourly cycle; an empty published index does not advance `lastRefresh`.
- **§6 defects it must NOT reproduce:** ingesting without verifying `Publisher==followed`; no `SignedBy` stamp; no `GeneratedAt` dedup (re-ingesting hourly).
- **§5 rules it must preserve:** subscriber fail-closed fetch bounds; `lastRefresh` advances only on success; companion takes only the narrow ports (no concrete `dhtindex`/`indexer` import beyond seams).

---

### Slice 11 — Native Fyne GUI

- **Goal:** `swartznet-gui` presents Downloads/Search/Status/Companion/Settings over the **same** `Daemon`, using `searchmux` for concurrent search and the daemon's shared `Confirm`/`Flag`.
- **Modules touched:** `gui` (`App` + five tabs), `cmd/swartznet-gui` (shim constructing `daemon.Daemon`), build stamp (Apache-2.0 string).
- **Depends on:** Slices 4, 5, 7, 9, 10 (all surfaces the GUI renders).
- **Behavioral references:** SPEC §2.10 (GUI), §5.8 (frontends are pure presentation), §6 (About shows wrong license). `legacy: internal/gui/`, `cmd/swartznet-gui/`.
- **Definition of done:** `./scripts/build-gui.sh dev` builds; the GUI adds a magnet, shows live download %, runs a search rendering **per-layer L/S/D cards** via the shared `searchmux.Result`, and confirm/flag from the GUI moves the same reputation state as the web UI (verify via `/aggregate`); the About dialog states **Apache-2.0** for first-party code; version/license come from one build-stamped source.
- **§6 defects it must NOT reproduce:** GUI About showing "MPL 2.0 (engine) + MIT"; GUI holding independent reconciliation or confirm/flag logic.
- **§5 rules it must preserve:** GUI has no independent lifecycle or reconciliation — presentation only; single `Daemon` constructor.

---

### Slice 12 — Aggregate backend behind the swappable seam (opt-in, off by default)

- **Goal:** the PPMI-item + signed SNAGG B-tree companion Aggregate backend exists and passes its own tests; `composite` dual-writes and legacy-then-aggregate dual-reads; **ship default stays `LayerDMode=legacy`**.
- **Modules touched:** `contracts/snagg` (B-tree page/trailer bytes + golden vectors), `contracts/dhtschema` (`PointerValue`/`PPMIValue`), `dhtindex` (`aggregatePPMI` + `composite` impls behind `RecordBackend`; hashcash PoW), `admission` (bootstrap anchors + `MarkSeeded`; `seeds.json` load), `swarmsearch` (aggregate distribution via `StartSync`), `daemon` (seam selection line), `cmd/swartznet` (the offline **`aggregate build|inspect|find`** subcommands — a thin offline builder over `contracts/snagg` + `contracts/sign` + `identity` load-only; no daemon, no DHT).
- **Depends on:** Slices 5, 8, 9.
- **Behavioral references:** SPEC §0 (Aggregate endgame, PROVISIONAL), §5.3 (SNAGG determinism/fingerprint), §5.1 (record identity), §5.2 (≥12-month legacy-read contract), §5.7 (admission), §7-B8/B9, §7-C12–C17. `legacy: internal/dhtindex/aggregate*.go`, `internal/companion/snagg*.go`.
- **Definition of done:** SNAGG golden vectors pin the 6-byte magic, 162-byte trailer, and MIN-KEY separators; **validate the seam against the legacy path first** — legacy passes the full lookup suite before `aggregatePPMI` is switched on in a test; `composite` dual-write then legacy-reader-only read still returns hits (proves migration is adapter-selection with zero app change); a curated `seeds.json` admits anchors under deny-by-default while an empty list leaves cold-start correct-but-inert with `/aggregate` showing `admit_capped`; the offline **`aggregate build`** signs + optionally mines hashcash + packs a JSONL record set into a signed SNAGG file (mode **0644**) and prints records/pages/bytes/fingerprint — fully offline (no daemon, no DHT), **refusing `--pow-bits > 40` with exit 2**, and requiring `--piece-size` to **match the wrapping torrent's metainfo piece length** (default 16384); **`aggregate inspect`** prints trailer metadata and **fails on bad magic/version/trailer-signature**; **`aggregate find --verify`** runs a prefix query and **re-derives the fingerprint**.
- **§6 defects it must NOT reproduce:** the port leaking PPMI/B-tree assumptions into the legacy path (open risk #2 — legacy validated first); permissive admission.
- **§5 rules it must preserve:** ship with `LayerDMode=legacy`; record identity excludes pow+sig; SNAGG build deterministic and fingerprint-stable; PPMI item at `SHA256("snet.index")`.

---

### Slice 13 — Hardening: wire-compat gate, checkpoint tuning, ops tooling, docs

- **Goal:** promote `wirecompat` to a CI merge gate, finalize crash-safety, fix the ops-tooling defects, and reconcile all docs with code behavior.
- **Modules touched:** `wirecompat` (full golden-vector suites + `VanillaPeer` matrix in CI `-race`), `engine` (checkpoint interval tuning), `cmd/dht-smoke` (deterministic exit codes), `testbed/` (bounded wait-loops, restore s12 timeout), `docs/05–07,11`, `CHANGELOG.md`, `THIRD_PARTY_LICENSES`.
- **Depends on:** all prior slices.
- **Behavioral references:** SPEC §2.11/§2.12 (wire-compat matrix), §5.11 (CI floating 1.24 vs pinned 1.24.1; timing-sensitive scenarios stay local), §5.12 (docs are normative; MPL discipline; license prohibitions), §6 (dht-smoke PASS on all-fail; testbed s12 lost timeout). `legacy: cmd/dht-smoke/`, `testbed/`, `docs/`.
- **Definition of done:** the deterministic `wirecompat` assertions (vanilla silence, unknown-LTEP-key tolerance, future-service-bit tolerance, signed/unsigned twin single infohash, every `contracts/*` codec matches frozen vectors, reject-code-2 on scope mismatch) run green in CI `-race` and **block merge**; `dht-smoke` returns non-zero when all probes fail; the testbed s12 scenario enforces its timeout; docs 05/06/07/11 match code (reject code 2, per-peer token bucket, no DHT sharding, `int(bleveScore*1000)` rank); `THIRD_PARTY_LICENSES` lists ledongthuc/pdf as BSD-3-Clause; a versioned crash-safety test confirms ≤ one checkpoint interval of Bloom/reputation loss.
- **§6 defects it must NOT reproduce:** dht-smoke reporting PASS on all-fail; testbed losing the s12 timeout; doc-drift (05/06/07/11).
- **§5 rules it must preserve:** timing-dependent multi-client transfers stay local-only (don't reintroduce flakiness); CI still installs Fyne C deps to `go vet` the module; the GPL/LGPL/AGPL/SSPL/BUSL/CDDL/EPL extractor-dependency prohibition is a standing rule.

---

## Test strategy

Mirrors SPEC §5.11 / §2.11:

- **Unit coverage per module.** Every leaf (`contracts/*`, `capability`, `identity`, `signing`, `trust`, `reputation`, `config`) is a pure package with table-driven tests; branch tests (`*_branches_test.go`, `*_errors_test.go`) exercise the error taxonomy. `contracts/*` additionally carry **frozen golden byte-vectors** checked into the repo — a second-language reimplementation must reproduce them from fixtures alone.
- **In-process multi-engine harness (`wirecompat`).** Two or more `Engine`s wired over an in-memory implementation of `swarmsearch.Transport` with no sockets — the standard vehicle for Layer-S, RIBLT, Layer-D-regtest, and companion slices. Because `sn_search` frames require a capability-minted `swarmsearch.PeerToken`, the whole matrix runs deterministically in-process. The Layer-D-regtest legs run an in-process DHT cluster pinned to `ListenHost=127.0.0.1`, `DisableIPv6=true`, and a placeholder `DHTBootstrapAddrs=[127.0.0.1:1]` (SPEC §5.11) so BEP-44 puts round-trip and the cluster never reaches public routers.
- **The vanilla-client interop matrix is the gate.** The deterministic half (in-memory `VanillaPeer` silence + unknown-key/future-bit tolerance + `contracts/*` golden vectors + signed/unsigned twin infohash + reject-code-2) runs in CI `-race` and blocks merge. The timing-sensitive half — a real anacrolix v1.61.0 client fetching metadata via BEP-9 and completing a transfer, and multi-client netem scenarios — stays **local-only** so the gate does not reintroduce flakiness.
- **Negative-regression tests per slice.** Each SPEC §6 defect gets a test that fails if the defect returns; each SPEC §5 rule that a slice touches gets an assertion. Where a legacy test pinned *buggy* behavior (permissive admission, static services, sequential search, unattributed ingest), the rebuild **re-derives** the expected value rather than porting the legacy assertion.
- **Cadence:** `go test ./... -count=1 -short` on every iteration; `go test -race ./...` before any slice is declared done; both `dist/` binaries rebuilt in the same turn as the code change.

---

## Sequencing rationale

The order minimizes integration risk by making each slice *observable in isolation* and by deferring every distributed/consensus-adjacent mechanism behind a working single-node client. Slices 0–3 build the lifecycle spine, identity, and a usable add/download/seed/create/sign client with **no search and no wire novelty** — so the four hard invariants are established and testable before anything rides them. Slice 4 adds Layer L, which is self-contained (never touches the wire) and gives a usable search product early. Slices 5–6 land trust/reputation and the single capability-mask producer *before* any peer-wire code, so the wire slices inherit correct services bits and a working confirm/flag path rather than retrofitting them. Only then do the wire layers arrive in dependency order — S (Slice 7, gated immediately by `wirecompat`), the RIBLT/record substrate (Slice 8), Layer D behind the legacy backend (Slice 9), and companion (Slice 10) — each reusing the in-process harness so distributed behavior is deterministic. The GUI (Slice 11) comes after all its rendered surfaces exist, guaranteeing it is pure presentation. The genuinely new, §7-underspecified Aggregate work (Slice 12) is scheduled last and kept non-load-bearing behind the `RecordBackend`/`composite` seam, validated against the legacy path first — so GA never depends on unproven RIBLT convergence between real nodes. Final hardening (Slice 13) promotes the wire-compat gate and reconciles docs once behavior is stable.

---

## What is explicitly NOT in this plan

Deferred until the rebuild reaches parity (SPEC §7 open questions and §0 scope exclusions), each recorded as an assumption in `ARCHITECTURE.md`:

- **A curated trust root (`seeds.json`).** Deny-by-default admission ships inert; a curated anchor/seed list is an explicit **release prerequisite**, not code, and is out of scope for the build slices (open risk #1).
- **The PPMI-retirement migration schedule** (§7-C12–C17): dual-write → PPMI-only → legacy-reader retirement, PoW ramp, and salt-convergence policy are release/operator decisions, not slices. The rebuild ships `LayerDMode=legacy` and only provides the seam.
- **`next_pk` key rotation** (§7-D19) — reserved and unused.
- **A separate sync reject code and the BEP normativity of scope-reject vs downgrade** (§7-A1/A2) — the rebuild adopts the code's reject-code-2 for wire-compat and flags the spec for a future codepoint.
- **BEP-9-cannot-carry-`snet.*` crawl verification** (§7-C18) — deferred while Aggregate Channel-B crawl is non-load-bearing.
- **PeerBook per-connection-lifetime persistence** (§7-G43) — kept as intentional v1 scope (no cross-restart peer memory).
- **Any authenticated remote control surface.** Invariant #3 forbids auth in `httpapi`; a remote surface would be a *new* frontend supplying its own auth — out of scope.
- **A global event bus / global ranking authority / token / consensus** — explicitly excluded by SPEC §0; the in-engine typed fan-out + hourly rescan is the deliberate non-goal-preserving choice.
- **Primitive agility** (swapping ed25519/SHA-1/SHA-256/FNV-64a) — excluded without a versioned format bump; the frozen `contracts/*` derivations are the interop anchor.
