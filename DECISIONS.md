# SwartzNet Rebuild — Decision Log

> The running decision log the rebuild playbook requires: every assumption made on the
> author's behalf, every question still open, and where the full rationale lives. This file
> consolidates; the detailed records are authoritative:
>
> - `SPEC.md` §7 — all 81 open questions, with `file:line` evidence anchors
> - `ARCHITECTURE.md` § "Assumptions & deferred questions" — the answered defaults in context
> - `docs/rebuild/phase3-design-record.md` — the three candidate architectures, judge scoring, critic fixes
> - `docs/rebuild/phase1-verification-ledger.md` — per-claim adversarial verification of SPEC §6 (135 confirmed / 13 refuted)

## Phase state

Phases 0–3 complete (artifacts committed 2026-07-09). **The Phase-3/4 gate CLEARED on
2026-07-17**: the author confirmed all five SPEC §0 checklist items unamended and approved
`ARCHITECTURE.md` + `PLAN.md`. Phase 4 is in progress, starting at Slice 0.

## Process decisions

| # | Decision | Rationale |
|---|---|---|
| P1 | Legacy preserved on the **`legacy-snapshot`** branch (pushed), not a `legacy/` directory | Keeps full history and diffability; the working branch stays a buildable Go module with stable import paths |
| P2 | The playbook's `DESIGN.md` is delivered as **`ARCHITECTURE.md`** (design) + **`PLAN.md`** (14-slice build order) | Two audiences, one contract; where they disagree the architecture wins and the plan is corrected |
| P3 | Every SPEC §6 defect claim was adversarially verified before being trusted; **refuted claims must NOT be "fixed"** in the rebuild | Several plausible-looking "bugs" (e.g. PoW=0 minting, seeded heavy-tail rule) are intentional, documented design — ledger in `docs/rebuild/phase1-verification-ledger.md` |
| P4 | Separation of new vs old code is **by branch, not by directory**: the rebuild replaces the tree on the rebuild branch while `legacy-snapshot` preserves the reference | Wire/disk compatibility is the deliverable; parallel trees would fork the module path, `dist/` workflow, and CI for no isolation gain |
| P5 | Same stack retained: Go 1.24.1, anacrolix/torrent v1.61.0, Bleve v2, Fyne v2 | SPEC §5.4's quirk catalog and the wire-compat matrix are anacrolix-specific and interop-tested; a stack change would invalidate the behavioral spec's anchors |
| P6 | Legacy tests that pin *buggy* behavior (permissive admission, static services mask, sequential search, unattributed ingest) are **re-derived, not ported** | Porting them would freeze the §6 defects the rebuild exists to remove |

## Assumed defaults (recorded on the author's behalf — override any of these)

Each maps a SPEC §7 question to the default the architecture adopts. Full context in
`ARCHITECTURE.md` § "Assumptions & deferred questions".

| §7 ref | Decision | Rationale |
|---|---|---|
| A5 | Services mask always-OR of `DefaultServices()` is a **bug**; `capability.Announced()` is the sole, live producer | Downgrades must reach the wire or ShareLocal=0 nodes advertise capabilities they reject |
| A3 | Unknown/future service bits are **ignored, never rejected** | Doc 06's masking wording is self-contradictory; ignore-unknown is the only forward-compatible reading |
| A1/A2 | Keep the code's **reject code 2** on scope mismatch (and its reuse for sync refusal) for wire compat; flag the BEP draft for a future codepoint | Interop with existing peers beats doc fidelity; the spec is fixable, shipped bytes are not |
| H46 | One unsafe gate: **`SWARTZNET_UNSAFE=1`** (+ `testing.Testing()` for testlab), documented | The two-name undocumented sequence broke the project's own testbed |
| B8/B9 | Admission is **deny-by-default**; permissive policy is test-only; a curated `seeds.json` is a **release prerequisite, not code** | The legacy placeholder admitted any unknown pubkey — inverted its own documented default |
| C12–C17 | Legacy per-keyword BEP-44 stays the shipping default; Aggregate lives behind the `RecordBackend` seam, **off by default**; retirement/PoW-ramp/salt schedule is an operator decision | ≥12-month legacy-read contract is binding; migration becomes adapter selection, not re-architecture |
| C16 | `MostDistinctive` is the **only** lookup-token chooser, now | The first-token lookup is a verified defect ("new ubuntu" queried the DHT for "new") |
| D20 | Bad-signature torrents **still add** (empty `SignedBy`, `ErrBadSignature` logged) | Blocking would fork behavior from vanilla and punish benign re-shares; UIs distinguish benign vs tamper |
| D21 | Trusted publishers **are exempt** from flag demotion | The trust package documents the exemption; the legacy just never checked it |
| D22 | Download completion does **not** auto-`RecordConfirmed` | Avoids self-reinforcing reputation; confirm stays an explicit user signal, consistent across surfaces |
| D23 | Bloom + reputation get a **bounded periodic checkpoint** (~5 min + batch flush) in addition to save-on-close | Crash loses at most one interval instead of the whole session's spam-resistance signal |
| D25 | identity.key permission gate stays **exactly 0600** (0400 rejected) | Preserves the legacy's observable contract; loosening is a one-line change if the author prefers "no group/other bits" |
| E27 | Queue promotion is **decoupled** from the Bloom gate | A nil Bloom stranding a completed torrent's slot is a verified defect, not a design |
| E28 | Per-torrent indexing-off **still publishes existence** to Layer D; only `--no-index`/`--no-dht-publish` suppress publication | Matches the code comment's stated privacy posture; flagged for the author under §7-28 |
| F36 | Index deletion becomes a reachable **"Forget"** action; downloaded files kept by default | Removed-but-searchable-forever is a verified defect; file deletion stays a separate, explicit act |
| §7-Q37 | Local + companion docs share one ID namespace, last-write-wins, **except non-empty `SignedBy` is sticky** against unsigned overwrites | Keeps the companion-attribution fix from being clobbered by a later local magnet add |
| §7-Q40 | Raw `.html` stays indexed by the **plaintext** extractor with tags left in; no standalone HTML extractor | "Improving" this silently changes the indexed content of every `.html` file |
| G43 | PeerBook state does **not** persist across restarts (v1 scope) | Matches legacy behavior; AddrMan-style persistence is deferred, recorded, and additive later |
| I51 | `add`-as-daemon stays (no `serve` subcommand in the final CLI; Slice 0's `serve` is a temporary scaffold deleted in Slice 2); bare 40-hex accepted as infohash add | Preserves the legacy UX contract; the scaffold exists only because Slice 0 has no engine to `add` |
| I53/I54 | `Options.PublisherPubKey` wired from identity; `/capabilities` mutates **only** `Sharing` with PATCH semantics | The daemon-owned Publisher bit becomes unreachable from the setter — the clobber becomes unrepresentable |
| J60 | User-facing license string: **Apache-2.0** (single build-stamped source) | `LICENSE`, README, and `THIRD_PARTY_LICENSES` all say Apache-2.0; the GUI About string was drift |
| §2.9/§5.9 | Non-loopback API bind **warns loudly and binds** (one-time "API is UNAUTHENTICATED" warning), not a hard refusal | Matches the spec's warn-and-bind; invariant #3's guarantee is *no auth concept in httpapi*, not a bind restriction |

## Resolved — the author gate (2026-07-17)

1. **`SPEC.md` §0 confirmed** — all five checklist items (mission framing, Aggregate
   endgame, scope exclusions, the four invariants, design-center user), unamended. §0 is
   now the normative statement of intent.
2. **`ARCHITECTURE.md` + `PLAN.md` approved** — implementation authorized, starting at
   Slice 0 (walking skeleton).

## Open — deliberately deferred (does not block Phase 4)

- **A1/A2** — BEP normativity of scope-reject vs downgrade; a dedicated sync reject code.
- **B8** — who curates/distributes the production `seeds.json` trust root (hard release gate; `/aggregate` admission counts make a starved node observable meanwhile).
- **C12–C17** — PPMI retirement schedule, PoW ramp mechanics, salt convergence.
- **C18** — how signed metainfo reaches BEP-9-only fetchers (`ut_signature` vs HTTPS mirror vs in-info signing); deferred while Channel-B crawl is non-load-bearing.
- **D19** — `next_pk` key rotation: reserved, unused.
- **G43** — PeerBook persistence (intentional v1 scope).
- The long tail of §7 (ops/docs/CI questions, §7-K/L/M) — resolved during Slice 13 or as encountered; every resolution gets appended here.

## Slice 0 decisions (2026-07-17)

| # | Decision | Rationale |
|---|---|---|
| S0-1 | §7-Q48 resolved: `Validate()` runs every pure rejection (empty DataDir, port range, unsafe gate) **before** either `MkdirAll` | A rejected config must leave the filesystem untouched (fail-closed ethos); error strings and the create-set are unchanged; pinned by test |
| S0-2 | `GET /healthz` ships in Slice 0 with an **instance-scoped** version (field on `httpapi.Options`), not the legacy process-global atomic | SPEC §5.9 flags the global as a wart; observable JSON is identical; deliberate §5 deviation, recorded |
| S0-3 | Middleware order is the legacy `bodylimit ⊃ CSRF ⊃ mux`; ARCHITECTURE.md's reversed prose corrected | SPEC §5.9 and the legacy code agree; the cap only swaps `r.Body`, so a CSRF-403 never reads the body |
| S0-4 | `serve --api-addr ""` runs an API-less daemon (empty-path = feature-off); a *failed* bind exits 1 (`swartznet: http api did not start`) after the daemon's degraded-start warning | Distinguishes "requested none" from "requested but failed"; `daemon.New` itself stays degrade-not-abort per SPEC §2.1 |
| S0-5 | `status` unreachable-daemon hint says `start it with: swartznet serve` until Slice 2 restores the legacy `swartznet add <magnet>` | The hinted command must exist; first line stays byte-identical to legacy |
| S0-6 | Version placeholder is `v0.9.0-dev`; release stamping stays `-ldflags "-X main.Version=..."` | Rebuild binaries must be distinguishable from the legacy v0.8.0 line |
| S0-7 | `newLogger`/`signalContext`/`reportRunErr` stay in `package main` until the GUI slice needs to share them | No new module outside the ARCHITECTURE map; sharing lands with its second consumer |
| S0-8 | `SWARTZNET_LOG` gains an explicit `"info"` case and a Warn on unrecognized non-empty values (§7-Q50: yes, warn); unknown values still run at Info | Behavior-compatible with legacy's silent fall-through, but observable; values remain case-sensitive |
| S0-9 | `config` has no `NoIndex` field yet; the daemon's NoIndex→Cfg mirroring line (SPEC §5.8) lands in Slice 2 with the engine that consumes it | Dead fields invite drift; the rule is pinned by SPEC/PLAN and gets its regression test with a consumer |
| S0-10 | `status --json` keeps the legacy `{"status":...}` envelope but skips the best-effort `/aggregate` fetch until that slice | Output is byte-identical to legacy-against-an-aggregate-less daemon; one less dead request |
| S0-11 | `httpapi.NewWithOptions` keeps the legacy empty-addr → `localhost:7654` constructor fallback even though the daemon treats empty as disabled | Legacy-pinned constructor contract; the daemon-level empty=off semantics are what users observe |
| S0-12 | Teardown log vocabulary frozen: `daemon.close_begin` → `daemon.bg_joined` → `httpapi.stopped` → `daemon.close_done` (new surface — legacy logged no teardown) | PLAN DoD requires reverse-order teardown observable in logs; later slices extend the same dotted style |
| S0-13 | SPEC §2.1's `./swartznet-data` fallback corrected to the code-true `./swartznet-state` (was §6.3-flagged drift) | Code behavior is the spec of record for the share-root fallback |
| S0-14 | The GUI binary is deliberately not rebuilt until the GUI slice; `dist/swartznet-gui-dev-linux-amd64` still contains the **legacy** build (as does `dist/swartznet-legacy-v0.8.0`, kept on purpose) | The rebuilt tree has no GUI source yet; the per-change both-binaries rule resumes at Slice 11 |
| S0-15 | Adversarial-review fixes: `goBG` registration is teardown-aware (no Add/Wait race, no-op after Close); double `Start` errors instead of orphaning the first listener; `cmd_status` non-200/decode output restored to byte-identical legacy shape; legacy `//go:embed index.html static/*` pattern kept; the per-keyword status table ported now (renders against a legacy daemon); a real-signal-path test (`syscall.Kill` SIGINT/SIGTERM → 130) guards the DoD; `scripts/dod-slice0.sh` checked in so the binary DoD run is reproducible | Each was a review finding with a concrete failure scenario; fixes verified by new pinning tests |

## Slice 1 decisions (2026-07-18)

| # | Decision | Rationale |
|---|---|---|
| S1-1 | `identity.Load(path string, allowCreate bool) (*Identity, error)` — the caller computes `allowCreate`; the redundant `isDefaultPath` return in ARCHITECTURE's original sketch is dropped (ARCHITECTURE amended) | The stdlib-only identity leaf cannot resolve the XDG default itself; enforcement stays in one place (Load's create branch), closing the legacy stat-then-create TOCTOU |
| S1-2 | Auto-create rule is **path-based**: the daemon compares `Cfg.IdentityPath == config.Default().IdentityPath`; `--identity` naming the default path behaves like the default | The daemon cannot know flag provenance; a user explicitly typing the default path and getting first-run creation is unsurprising |
| S1-3 | Explicit `--identity` load failure is **fatal** at `serve` (exit 1, `swartznet: identity did not load`); default-path failure **degrades** (warn + publisher-less) per SPEC §2.8 | The user explicitly named a key — running without it contradicts intent; SPEC pins only the default-path (non-fatal) case |
| S1-4 | The four legacy identity error strings preserved byte-verbatim; new load-only-missing error wraps `os.ErrNotExist` | Operator tooling and tests may match on them; `errors.Is` compatibility retained |
| S1-5 | Log events renamed `engine.identity_loaded/…_load_err` → `daemon.identity_loaded/…_load_err`, attr keys `pubkey`/`err` kept | Ownership moved from engine to daemon per ARCHITECTURE; the attrs are the observable contract |
| S1-6 | Creation: plain `WriteFile` + explicit `Chmod(0600)` after (no O_EXCL, no tmp+rename) | The chmod defeats exotic-umask bricking (e.g. 0277 → 0400 file → next load rejected); torn writes already fail closed on the size gate; atomicity is spec-mandated for trust.json/.torrent, not identity.key |
| S1-7 | Parent-dir `MkdirAll(0700)` moved to the **create arm** only; a parent-is-a-file path now surfaces the raw ENOTDIR stat error instead of legacy's `identity: mkdir` error | A refused load must have zero side effects (same principle as S0-1); pinned by test |
| S1-8 | Legacy dead branch "does not contain a valid ed25519 key" dropped; symlink semantics stay `os.Stat` (follow); no Windows perm carve-out yet — the exact-0600 gate makes identity unloadable on Windows exactly as legacy shipped, flagged under §7 open questions | Rebuild-faithful; a Windows carve-out is a deliberate future decision, not a silent divergence |
| S1-9 | `/status` `publisher.pubkey` renders whenever the probe is non-nil — deliberately un-nested from any publisher collaborator (legacy nested it, half of the §6 defect) | Slice 1 has no publisher; nesting would make the DoD unachievable and re-hide the pubkey for leech-only nodes later |
| S1-10 | `httpapi.Options.PublisherPubKey` stays `func() string` (legacy shape), not a plain string | Leaves room for key rotation (`next_pk`, §7-D19) without an Options change |
| S1-11 | Adversarial-review fixes: `allowCreate` compares `filepath.Clean`ed paths (a `/./`-spelled default still auto-creates, pinned by test); `swartznet help` and `serve -h` now agree on the path-based `--identity` wording; a hostile-umask (0277) unit test pins the post-create Chmod; `TestServeBadConfig` gained structural XDG isolation; the DoD script got a `timeout` on the fatal-exit check, readiness polling instead of a fixed sleep, and python3 instead of xxd; ARCHITECTURE/PLAN stale `IsDefaultPath` wording amended; the Windows perm-gate corollary recorded in SPEC §7-Q25 | Each was a review finding with a concrete scenario; fail-closed behavior unchanged |

## Slice 2 decisions (2026-07-18)

| # | Decision | Rationale |
|---|---|---|
| S2-1 | HTTP routes keep the legacy paths (`POST /torrent`, `GET /torrents`, …); PLAN's `/add`,`/downloads` are informal labels | SPEC §3.2 freezes the legacy shapes, ARCHITECTURE owns §2.9's endpoints unrenamed, and architecture wins over plan (P2) |
| S2-2 | The `-` stdin add is implemented (io.ReadAll → the shared `AddTorrentBytes` path, persisted `added_via:"file"` with a torrents/ copy); `POST /torrent` stays magnet-only | One code path for file/stdin gives Slice 3 a single signature-verification insertion point |
| S2-3 | `PATCH` and `POST` both serve the merge handlers on `/config/rate-limit` and `/config/queue` | Legacy full-body POSTs behave identically under merge; the DoD's PATCH works; nothing regresses |
| S2-4 | Pause/resume/remove routes land now (legacy controlOne shape) | The engine implements them in this slice; leaving them API-less would strand tested behavior |
| S2-5 | The CLI drops legacy's `DownloadAll()` after GotInfo | The engine's per-file Normal flip is the §5-mandated activation and DownloadAll would bypass the queue cap; scenarios assert all-normal priorities instead |
| S2-6 | `AddTorrentMetaInfo` returns `*engine.Handle`, not legacy's `any` | The `any` existed only so companion could avoid importing engine; the rebuild's daemon-adapter rule makes it unnecessary |
| S2-7 | `--regtest` and `--no-dht-publish` flags deferred (S0-9 dead-flag principle); `--dht-insecure`/`--dht-bootstrap`/`--no-dht`/`--leech-only`/`--no-index` land now with consumers | Regtest timings and Layer-D publishing arrive in Slices 7–10/9 |
| S2-8 | `config.DisablePortForwarding` added (maps to anacrolix `NoDefaultPortForwarding`); the harness sets it | Tests were reaching the real gateway over UPnP — a hermeticity leak; also a legitimate operator knob |
| S2-9 | `fileTracker` builds piece spans from `t.Files()` (`BeginPieceIndex`/`EndPieceIndex`) instead of legacy's info-offset math | Observables identical (Path=DisplayPath, same spans); less duplicated arithmetic |
| S2-10 | Honest lock names replace the legacy `-Locked` inversion (`promoteQueued` acquires its own locks); `queued` state not persisted, re-derived on restore | SPEC §6.3 pins the naming trap; a rewrite following legacy names would deadlock |
| S2-11 | `VerifyData` runs on metainfo-add and restore only (legacy shape) — a plain `.torrent` add with pre-existing data shows 0% until restart | Matches legacy; the restart path covers the common case; noted as a known gap |
| S2-12 | `status` gains a best-effort Downloads section (text + `"downloads"` JSON key); any `/torrents` fetch failure silently omits it | New surface with no legacy anchor; DoD only pins "percentage visible"; older/controller-less daemons keep working |
| S2-13 | CI exclusion moves from end-anchored `/internal/testlab$` to `-Ev '/internal/wirecompat/scenarios(/|$)'` | The legacy grep silently ran future subpackages in CI (§6.3-adjacent footgun) |
| S2-14 | Adversarial-review fixes (behavioral): resume-over-a-full-cap now resets file priorities to none when queuing (the legacy shape let it download outside the cap — empirically confirmed; pinned by test); the magnet→metainfo upgrade re-checks removal and uses an existence-conditional session write so `RemoveTorrent` can't be raced into resurrecting an entry; restore cross-checks the torrents/ copy's infohash against the entry key (zombie-entry guard); `watchCompletion` drops its 24h timeouts (multi-day downloads must still promote) and gains its own regression test; duplicate metainfo adds skip the re-verify rehash; the mkdir-torrents failure is fatal to `engine.New` (legacy shape) while a corrupt manifest degrades self-healingly (session keeps its paths so the next save rewrites it); `Engine.Close` is concurrency-safe (late callers block on the first teardown); pieceSubscription closes its channel on every exit path; the legacy session error-string wrappers are restored | Each was a review finding with a confirmed failure scenario |
| S2-15 | Adversarial-review resolutions (fidelity/quality): `AddTrustedPeerEngine` returns AddClientPeer's real added-count; queue moves stay runtime-only (persistState calls dropped for legacy parity); Ctrl-C during the metadata wait is silent on stdout (legacy contract, pinned); `contracts/bencode` is wired into `AddTorrentBytes` as the strict validator + infohash cross-check (no longer test-only); the DHT `/status` block is wired via `httpapi.Options.DHTStats` (omitted = disabled, present-zero = isolated); `--dht-insecure` stays documented in help WITH its gate noted — resolving the SPEC §6.3 under-documentation defect in favor of discoverability over the legacy's deliberate omission; CI gofmt now covers `contracts/` | Byte-fidelity restored where pinned; contested doc points resolved and recorded |

## Slice 3 decisions (2026-07-18)

| # | Decision | Rationale |
|---|---|---|
| S3-1 | `contracts/sign` owns payload + `Signature` + sentinels + crypto/length checks; `internal/signing` does the .torrent-level field work and returns `(sign.Signature, error)` — ARCHITECTURE's `(pubkey, error)` sketch is satisfied via `Signature.PubKeyHex()` | Only the .torrent layer can detect missing fields (ErrNotSigned); the populated-on-ErrBadSignature Signature must surface, which a bare pubkey return cannot |
| S3-2 | **J1 fixed (deliberate divergence):** `create --sign --seed` feeds the SIGNED bytes to the engine via the new `AddTorrentBytesSeedFrom`, so the creator's own node verifies, persists, and displays its signature | The legacy re-marshaled the unsigned struct and the publisher's own node never badged itself — recorded as legacy defect (a) in the extraction |
| S3-3 | Duplicate adds upgrade `SignedBy` stickily (verified signature fills an empty attribution; an unsigned re-add never blanks it) | Extends §7-Q37 stickiness to the add path; without it a signed re-add of a magnet-added torrent stayed unsigned forever |
| S3-4 | No-seed `create` builds no engine at all (`CreateTorrent*` are standalone functions) | A pure hashing run must not bind sockets or create XDG state; SPEC pins the config shape, not the bind |
| S3-5 | `--piece-kib` gains real validation (power of two ≥ 16 KiB, clear error) | The legacy documented the constraint but silently accepted garbage, producing broken torrents — fail closed instead |
| S3-6 | `SignFile`/`VerifyFile` path helpers dropped; the API is bytes-only | Zero production callers in legacy; the create path signs in memory |
| S3-7 | `"signing: bad private key length %d"` lives only in `contracts/sign.Sign` (raw-key callers); `identity.Signer` guarantees length structurally | The check is unreachable through the Signer path by construction |
| S3-8 | Verify-at-add log events keep legacy names (`engine.torrent_signature_verified`/`_rejected`) with `info_hash` replacing the legacy `path` attr | Stdin adds have no path; `info_hash` matches the package-wide log-key convention |
| S3-9 | Adversarial-review fixes: the sticky upgrade also refreshes the persisted torrent copy (stored bytes always back the claimed `signed_by`) and uses a CAS so concurrent signed adds agree; `--piece-kib` gains an overflow guard (wrap-around bypassed the S3-5 validation — empirically confirmed); empty-directory create fails closed (`root directory contains no files`); `create --no-dht` added so hermetic/local seeding needs no gateway or public bootstrap (the DoD download-from-seeder check rides it); the seed paths dedup into one `addSeedSpec` helper; the anacrolix singleton-list-unwrap leniency in Verify is pinned by test as accepted surface; slog-capture tests freeze the signature event names/attrs; DoD script gained trap cleanup, timeouts, a restart-`signed_by` check, and an end-to-end download from the signed seeder | Each was a review finding; the two majors were coverage gaps (restore of signed_by and AddTorrentBytesSeedFrom serving bytes), both now proven at binary level |

## Slice 4 decisions (2026-07-18)

| # | Decision | Rationale |
|---|---|---|
| S4-1 | **Real bug found + fixed:** `ReadSeekerAt` is size-bounded (`NewReadSeekerAt(rs, size)`). anacrolix `File.NewReader().Read(buf)` reads up to `len(buf)` from storage and only sets EOF *after* the position passes the file end — so a buffer larger than the file over-reads into the following files. Every earlier file in a multi-file torrent was mis-indexed (a `.txt` saw later binary bytes → "NUL detected" skip); the last file coincidentally worked | The shim bounds every Read/ReadAt to the file length; pinned by `TestMultiFileEachIndexedFromOwnBytes` and the DoD's txt+pdf+zim torrent. Also makes ZIM's internal-offset ReadAt safe near the file end |
| S4-2 | `/search` with a nil index answers **200 with an empty local block**, not 503 — a deliberate override of the package's nil⇒503 law | SPEC §2.9: "always runs Layer L when the index is wired"; the web UI depends on the shape. `/index/stats` (whose whole purpose IS the index) keeps 503 |
| S4-3 | `PickLookupToken` = `MostDistinctive(Tokenize(query))` with **capped** Tokenize; lives in `indexer`. dhtindex (Slice 10) will import `contracts/token` directly, NOT `indexer.PickLookupToken` — the layer diagram gives dhtindex no indexer edge | `contracts/token` is a Tier-0 leaf importable from anywhere; a literal `indexer` import from dhtindex would violate the layering |
| S4-4 | The reachable **Forget** is `RemoveTorrent(ih string, forget bool)` on the controller, composed in the daemon adapter (`eng.RemoveTorrent` then `eng.ForgetIndex`); HTTP `DELETE /torrents/{ih}?forget=1`. Files are kept; a nil index makes Forget a no-op that still returns 200 | Keeps index deletion off the hot engine path; Forget must not fail because search is off (F36) |
| S4-5 | The **hourly rescan** is an engine-owned goroutine on `bgCtx` with a package-var interval; it re-submits completed, not-yet-submitted files (pipeline `WasSubmitted` dedup). The legacy had no recovery despite the docs claiming one | Deterministic bounded recovery of dropped file-complete events (SPEC §5.5); no LLM in the loop |
| S4-6 | Daemon tests default to `NoIndex` (Bleve open is ~1 s each); a dedicated `TestIndexerWiring`/`TestNoIndexLeavesSearch503` exercises Layer L. `dod-slice0/1` pass `--no-index` so their `/status` goldens stay `indexed:false` | Keeps the fast suites fast; the indexer's own package tests cover Bleve exhaustively |
| S4-7 | The ZIM test fixture builder is duplicated into a wirecompat test helper rather than exported from `extractors` | Avoids coupling to another package's test scope; the ZIM format is frozen |
| S4-8 | Chunker offset off-by-blank-run fixed (offsets point at the paragraph's first byte); the dead `gz` extension token kept for list fidelity; the stale "~10 KiB"/"then spaces" chunker doc drift corrected | Zero observable surface change (offset never reaches the doc); matches the contract's recommended resolutions |
| S4-9 | Adversarial-review fixes: the rescan cadence is now a per-engine field from `config.IndexRescanInterval` (0 = 1 h) instead of a package var — the var was read by every `engine.New`'s rescan goroutine while a test mutated it, a deterministic `-race` failure; `registerLockedRestore` applies the persisted indexing/signedBy/queueOrder BEFORE spawning the index goroutines (a restore-time race could index an OFF torrent or blank a signed_by — pinned by `TestIndexingOffSurvivesRestart`); `ForgetSubmitted` also clears the pipeline counters (no double-count / unbounded growth on churn); direct-Bleve `search` fails closed with a route hint instead of hanging when a daemon holds the index lock; the CLI restores the legacy per-torrent `tracker:` line and renders `<mark>` snippets in API mode too; the 2100-doc stats test is `-short`-gated; `TestNoIndexStats503ButSearch200` renamed to match what it asserts | Each was a review finding; the rescan race is the notable one — a genuine `-race` regression this slice introduced |

## Slice 5 decisions (2026-07-18)

| # | Decision | Rationale |
|---|---|---|
| S5-1 | **The confirm/flag state transition is ONE deterministic code path in the daemon** (`daemon.Confirm`/`daemon.Flag` → `engine.ConfirmHit`/`engine.FlagHit`). httpapi and the CLI only marshal DTOs and map error sentinels; no LLM, prompt, or per-frontend logic touches Bloom/reputation state | production-architecture-rules.md: exactly-once / state-transition actions live in deterministic code with a fail-closed terminal outcome; the three frontends share the one path |
| S5-2 | Admission is **deny-by-default** (`admission.DefaultPolicy`), inverting the legacy's permissive §6 admission | A spam-resistance boundary must reject the unknown, not admit it; `PermissivePolicyForTest` keeps the old behavior reachable only from tests |
| S5-3 | The Bloom is a **frozen FNV-64a Kirsch-Mitzenmacher v1 `SBLM` format** pinned by a golden byte-vector (`golden_vector_test.go`) and a checked-in `testdata/known-good.bloom` (population 17, k=7) | Format drift silently breaks every existing `known-good.bloom`; the golden makes drift a failing test, and the file fixture proves cross-version load |
| S5-4 | Reputation is **Bayesian-smoothed** (prior weight 5.0, neutral 0.5, seed bonus 0.45·2^(−age/90d)); `Snapshot()` is score-sorted with a deterministic tie-break | A raw good/bad ratio is unstable at low counts; smoothing toward neutral resists both cold-start noise and single-flag swings |
| S5-5 | Two Bloom auto-confirm paths, both **`bloom.Add` only, never `RecordConfirmed`** (D22): trusted-publisher confirm fires at **metadata arrival** (no download needed), completion confirm fires **unconditionally** on `Complete()`. `watchCompletion` **promotes the queued slot BEFORE** the Bloom side-effect + checkpoint | Completion/trust are self-signals that must not self-reinforce reputation; promotion is latency-sensitive and must never wait on the Bloom nil-check or the synchronous checkpoint's disk I/O (a promote-after-checkpoint ordering caused a `-race` `TestCompletionPromotesQueued` timeout) |
| S5-6 | Flag **fails closed on zero attribution** — demotes nobody, never the legacy demote-every-indexer fallback — and reports **honestly**: `indexers_flagged` is NOT `omitempty` (0 renders), `attribution` ∈ {`targeted`,`trusted-exempt`,`trust-unavailable`,`none`}, and the CLI prints "no reputations changed …" whenever nobody was demoted | Fixes the legacy §6 dishonest-success defect (flag claimed success while demoting nobody); trusted publishers are exempt, filtered BEFORE `RecordFlagged` |
| S5-7 | **Review fix (HIGH):** Bloom + reputation `Save` write a **unique `os.CreateTemp` file** (not a fixed `<path>.tmp`), and `engine.Checkpoint` is serialized by `ckptMu` with `Close` **joining the checkpoint goroutine** (WaitGroup) before its final flush | Six unsynchronized goroutines call `Checkpoint`; on a fixed tmp name two concurrent saves of different-length JSON tore each other's writes into an invalid `reputation.json` that rename then published, silently losing ALL confirmed reputation on restart. Pinned by `TestConcurrentTrackerSaveNoCorruption` |
| S5-8 | **Review fix (MED):** a corrupt/unreadable but **configured** `trust.json` fails **CLOSED** — `loadSpamResistance` sets `trustFailed`, and `FlagHit` returns `attribution=trust-unavailable` and demotes nobody rather than treating a nil store as "trust off" | Otherwise a hand-edit typo silently disabled the trusted-publisher exemption and demoted trusted maintainers under a legitimate-looking `Flagged=1`. The attribution is preserved (no `Forget`) so a retry after the file is fixed still acts. Pinned by `TestFlagFailsClosedWhenTrustDegraded` |
| S5-9 | **Review fix (LOW) + polish:** `--help` documents `--api-addr` for `confirm`/`flag`; offline `trust add` `MkdirAll`s its parent dir (clean-install fix found by the DoD); `EstimatedItems` reports a clean `0` instead of negative zero at empty population | Small correctness/discoverability gaps caught by the review and by driving the real binary |

## Log

- **2026-07-09** — Phases 0–3 executed and committed (`8e687ab` SPEC §1–7, `96b109c` provisional §0, `ee54afb` ARCHITECTURE+PLAN, `26a897e` provenance records). Paused at the Phase-3/4 human gate.
- **2026-07-17** — State re-verified (working tree clean, `legacy-snapshot` intact, no code drift since the spec snapshot); this consolidated decision log created; gate re-presented to the author.
- **2026-07-17** — **Gate cleared.** Author confirmed SPEC §0 (all five items) and approved ARCHITECTURE.md + PLAN.md. Phase 4 begins: the legacy Go tree is removed from the rebuild branch (preserved on `legacy-snapshot`), and Slice 0 implementation starts.
- **2026-07-17** — **Slice 0 built.** Contracts extracted from legacy by a 4-agent workflow (exact strings ledger in `docs/rebuild/` provenance); implementation + full test suite landed; 37/37 binary DoD checks pass; `go test -race` clean; adversarially reviewed before commit. Slice decisions S0-1…S0-13 above.
- **2026-07-18** — **Slice 1 built.** Identity contracts extracted by a 3-agent workflow; `internal/identity` + daemon/httpapi/CLI wiring landed; 19/19 Slice-1 + 37/37 Slice-0 binary DoD checks pass; race-clean; adversarially reviewed before commit. Slice decisions S1-1…S1-10 above.
- **2026-07-18** — **Slice 2 built.** Engine contracts extracted by a 5-agent workflow (137KB ledger); `contracts/bencode` + `internal/engine` + wirecompat harness + HTTP/CLI surface landed; the `serve` scaffold deleted and folded into `add`; 15/15 Slice-2 + 19/19 + 37/37 cumulative binary DoD checks pass, including a real two-binary loopback magnet transfer; race-clean incl. the transfer scenario; adversarially reviewed before commit. Slice decisions S2-1…S2-13 above.
- **2026-07-18** — **Slice 3 built.** Signing contracts extracted by a 3-agent workflow (golden vector byte-verified against actual legacy output); `contracts/sign` + `internal/signing` + engine create/verify-at-add + `cmd create` landed; 19/19 Slice-3 + cumulative binary DoD checks pass; race-clean; adversarially reviewed before commit. Slice decisions S3-1…S3-9 above.
- **2026-07-18** — **Slice 4 built.** Layer L contracts extracted by a 5-agent workflow (117KB ledger); leaf packages (`contracts/token`, `internal/indexer` + extractors) built by an implementation workflow, then wired (engine ingestion + hourly rescan + Forget, `searchmux`, `/search`+`/index/stats`, CLI `search`/`index`). Driving the real binary caught a genuine multi-file over-read bug (fixed via a size-bounded `ReadSeekerAt`); the 4-lens review caught a `-race` regression on the rescan interval and a restore-ordering race — both fixed and pinned. 18/18 Slice-4 + 108 cumulative binary DoD checks; race-clean. Slice decisions S4-1…S4-9 above.
- **2026-07-18** — **Slice 5 built.** Spam-resistance: leaf packages `internal/reputation` (frozen FNV-64a Bloom v1 + golden vector, Bayesian reputation, source-attribution LRU), `internal/trust` (publisher allowlist), `internal/admission` (deny-by-default) built by a leaf workflow, then wired — engine `loadSpamResistance`/`Checkpoint`/`ConfirmHit`/`FlagHit`, the **one shared** `daemon.Confirm`/`daemon.Flag` path, httpapi `POST /confirm`·`/flag`·`GET /aggregate` + `/status` Bloom/reputation blocks, CLI `trust`/`confirm`/`flag`. Driving the real binary caught an offline `trust add` clean-install dir bug; the 4-lens review (10 agents, 6 raw → 3 confirmed after adversarial verify) caught a **HIGH** concurrent-checkpoint `.tmp` corruption race (→ unique `os.CreateTemp` + `ckptMu` + `Close` joins the checkpoint goroutine), a **MED** corrupt-`trust.json` fail-OPEN that could silently demote trusted publishers (→ fail-CLOSED `trust-unavailable`), and a **LOW** `--help` gap — all fixed and pinned. 22/22 Slice-5 + 130 cumulative binary DoD checks; race-clean (0 data races across 18 packages). Slice decisions S5-1…S5-9 above.
