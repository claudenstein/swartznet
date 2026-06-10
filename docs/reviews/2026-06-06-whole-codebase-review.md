# SwartzNet Codebase Review

## Executive summary

SwartzNet is in good overall health: the codebase is unusually well-tested, layer isolation (L/S/D) is genuinely enforced, mainline wire-compatibility is preserved everywhere (no new reserved bits, DHT verbs, or UDP ports), and identity/signing discipline is largely sound. The dominant theme is **untrusted-input robustness**: every place the daemon ingests bytes from an arbitrary remote publisher — BEP-46 pointers, companion B-trees, torrent file content, DHT values, and sn_search sync sessions — has at least one fail-open or unbounded-resource path, and four of these are remotely-triggerable crashes/DoS. The secondary theme is **silent fail-open on state-changing operations** (publish marked successful with zero DHT nodes, identity silently regenerated, config silently ignored) that violate the project's deterministic/fail-closed posture. The web API's complete absence of CSRF/Origin defenses is the one classic web-security gap.

## Cross-cutting themes

1. **Untrusted-input availability holes.** The memory-safety model assumes every consumer of remote bytes bounds its work, but several do not: extractors that ignore `maxBytes` (subtitle, mkv, zip-bomb output), the companion B-tree reader recursing on unauthenticated interior pointers, unbounded swarmsearch sync sessions, and unbounded DHT-value decode. Recover() guards are present in many spots but **OOM and stack-overflow are not recoverable**, so the guards give false confidence.

2. **Fail-open on exactly-once / state-changing steps** (violates the deterministic-layer rules). Publish is marked successful when it reached zero DHT nodes (then the rate-limiter suppresses retry for ~55m); explicit `--key`/`--identity` paths silently mint a new identity instead of failing closed; companion publisher silently writes to the wrong directory; restored paused torrents flip to Normal priority.

3. **Documented-but-not-implemented surface.** Several APIs claim a capability the code lacks: DHT sharding schema (`More`/`SaltForShard`), `ShareLocal==1` swarm-only filtering, three misbehavior score constants, `AnchorReputation`/`CandidateReputation` options, `StartFeeler` idempotency. These mislead future maintainers and external implementers reading the wire schema as a spec.

4. **Goroutine-spawn-per-remote-event without admission control** (engine LTEP handler, swarmsearch PeerAnnounce, anchor-fetch, autoConfirm). Mostly low-impact individually but a recurring pattern.

## Blocking findings

| Unit | File:line | Issue | Fix |
|---|---|---|---|
| engine | engine.go:1175-1187 | Zero infohash from untrusted BEP-46 pointer hits `AddTorrentInfoHash` → `panicif.Zero` crashes daemon; no guard, no recover. Remote unauthenticated DoS. | Reject zero infohash in `AddInfoHash`; also add non-zero check in `dhtindex.GetInfohashPointerInfo` (fail-closed at parse) and a recover() mirroring `AddMagnetURI`. |
| indexer-extractors | subtitle.go:50-57,120-128 | Subtitle extractor explicitly ignores `maxBytes` and has no size guard; multi-GB `.srt` read fully into RAM → OOM. Remote DoS via ordinary content. | Wrap reader in `io.LimitReader(r, maxBytes)`; add `if c.Size > cap { return false }` to `claims`. |
| indexer-extractors | mkv.go:116 | `readFull(br, int(size))` allocates from an unbounded untrusted EBML VINT before any read → multi-GB alloc → OOM. | Reject/clamp `size > uint64(maxBytes)` before `readFull`; have `readFull` refuse oversized `n`. |
| companion | read_btree.go:161-220 | `walkToLeaves` recurses on attacker-controlled, **unsigned** interior `ChildIndex` with no cycle/depth check; self-referential page → uncatchable stack overflow. Reproduced. | Require child indices strictly downward, increasing, in-range; add depth budget and/or visited-set; regression test for self/back-pointing pages. |
| swarmsearch | handler.go:267-320,480-514 | Responder `SyncSession`s have no per-peer cap and no staleness reaper; each snapshots the full local record set and lingers forever if `sync_end` never arrives. Unbounded memory / DoS. | Add per-peer session cap with oldest-eviction; stamp creation time; bounded reaper emits `sync_end` aborted; charge misbehavior on cap breach. |

## Important findings

### engine
- **engine.go:1639-1659,1721-1737 (determinism)** — `autoDownload` flips a restored *paused* torrent's files to Normal priority because `restoreEntry` applies paused state only after `registerLocked` spawns the goroutine. Make `activateDownload`/`queueOrActivate` skip when `IsPaused()`, or set paused state before spawning; re-flip on Resume.
- **engine.go:684-715 (concurrency)** — Inbound LTEP handler spawns a goroutine + payload copy per `sn_search` frame *before* the per-peer rate limiter applies; reply path spawns a second goroutine per write. Gate the spawn with a bounded semaphore/worker pool; check the limiter cheaply before spawning.

### indexer-core
- **indexer.go:426-429,492-528 (correctness)** — `AllTorrentDocs`/`torrentDocFromFields` silently drop `SignedBy`; the schema-v3 bump exists to carry it. No live loss today, but the next publisher-attribution caller gets empty signers with no error. Project `fieldSignedBy` and reconstruct it; add a test.
- **indexer.go:338-360 (correctness)** — `Stats.CorpusTextBytes` undercounts above 64,000 content docs due to a hardcoded `guardTTL=64`; `InflationRatio` silently wrong past 64k chunks, defeating the metric's purpose. Terminate on `len(Hits)<batch` or scale the guard off `ContentCount`; log when hit.

### indexer-extractors
- **docx.go:51-85 (and odt/odp/pptx/epub/pdf/htmltext) (security)** — ZIP/XML/PDF extractors cap only *compressed* input; decompressed text accumulates in an unbounded `strings.Builder` → zip-bomb amplification to multi-GB. Add an output-size guard mirroring `rtf.go` (stop at `maxBytes`); read PDF `GetPlainText()` through `io.LimitReader`.
- **id3.go:71-74 (performance)** — `make([]byte, 10+tagSize)` allocates up to ~256 MiB from an unvalidated header `tagSize` regardless of file size; multiplied across many tiny files. Clamp `tagSize` to `min(tagSize, maxBytes)`/remaining input before `make`.
- **pipeline.go:294-315 (concurrency)** — `safeExtract` watchdog is soft-only (logs `extract_slow`) and cannot interrupt; a wedged extractor pins a worker goroutine forever — ambiguous-limbo state violating fail-closed/bounded-recovery. Plumb a `context.Context` into `Extract`, or run `Extract` in a child goroutine selected against a hard deadline so the worker can fail the file and move on.

### swarmsearch
- **sync_session.go:250-272 (correctness)** — RIBLT decoder ignores the on-wire symbol `Index`; a single dropped `sync_symbols` frame permanently desyncs encoder/decoder with no error. Validate `m.Index == s.symbolsIn` in `ApplySymbols` and abort on mismatch.
- **handler.go:561-597 (correctness)** — `ShareLocal==1` (documented swarm-only) actually searches the entire Bleve index — privacy/capability violation that does the opposite of the contract. Enforce swarm-membership filtering, or treat `==1` as unsupported and fail closed until a swarm-aware searcher exists.

### dhtindex
- **publisher.go:270-276 (determinism)** — `getput.Put` returns nil even when it reached zero DHT nodes; `publishOne` marks the keyword published, then the ~55m rate-limiter suppresses retry — item never lands, keyword undiscoverable, manifest reports healthy. Require ≥1 confirmed node before `MarkPublished`; don't advance `LastPublished` otherwise.
- **manifest.go:156 (correctness)** — Eviction estimate uses `Ts=0` (~7 bytes) but the live encoder injects a 10-digit timestamp (~16 bytes); a near-cap entry the manifest believes fits fails `EncodeValue` at put time and can never publish. Set a representative `Ts` in `EstimateValueSize` or reserve ~16 bytes headroom; test the full `AddHit`→`EncodeValue` path.
- **schema.go:40-43 (maintainability)** — Sharding schema (`More`/`SaltForShard`) is defined but never written or read; oversize keywords silently drop oldest hits, and the dead API reads as a spec it doesn't honor. Implement shard spill+read, or remove the schema and document eviction as intended.

### companion
- **read_btree.go:193-218 (security)** — Even without a cycle, `walkToLeaves` never dedups visited pieces; a DAG-shaped tree re-walks shared subtrees exponentially → CPU/alloc exhaustion from one small unsigned file. The strict downward/increasing/in-range child check fixes this too; add a visited-set and a leaf cap as defense.

### httpapi
- **server.go:245-270,902-918,1104-1125 (security)** — No CSRF/Origin/Host defense on a localhost daemon driving a browser UI. Pause/resume/remove and config/flag mutations are CORS-simple POSTs, so any visited web page can drive them; DNS-rebinding defeats naive same-origin. Add middleware rejecting non-loopback `Origin`/`Referer`, validating `Host` against a loopback allowlist, and/or requiring a non-simple custom header on all mutating endpoints.
- **server.go:238-243 (security)** — Operator can bind the fully-unauthenticated API to `0.0.0.0` with no warning or opt-in, exposing torrent/follow/reputation/Bloom control to the LAN/internet. Refuse non-loopback bind without an explicit flag, or log a prominent WARN; pair with the Host/Origin allowlist.

### daemon
- **daemon.go:117-122 (correctness)** — Companion publisher is gated on `CompanionDir` but writes to `DataDir`; the configured/default companion dir is silently ignored and artifacts pollute the content tree. Set `cpOpts.Dir = opts.Cfg.CompanionDir`; add a test asserting files land under `CompanionDir`.
- **daemon.go:193-201 (concurrency)** — Anchor-fetch goroutine from `New` is neither awaited nor cancel-tracked on `Close`; if the caller keeps `ctx` alive, it runs DHT fan-out against an engine being torn down. (Only bites production anchored builds.) Derive a child context + cancel in `Close`, optionally join via `WaitGroup`.

### cli
- **cmd_aggregate_build.go:124-129 (security)** — Explicit `--key`/`--identity` path calls `LoadOrCreate`, silently minting a new key if the file is absent (e.g. unmounted USB, typo); records signed by an untrusted key. Same in `cmd_create.go:86`. Violates the "identity never regenerated implicitly" invariant. Add a load-only path that errors on `os.ErrNotExist` for explicit paths.
- **cmd_aggregate_build.go:135-141 (correctness)** — `defaultIdentityPath()` hardcodes `~/.local/share/...`, ignoring `XDG_DATA_HOME`, so under a custom XDG the build tool mints a *second* identity diverging from the daemon's. Use `config.Default().IdentityPath` as `cmd_create.go` already does.
- **cmd_add.go:45-48 (security)** — `--dht-insecure` (disables BEP-42) and `--regtest` ship unhidden on the production `add` command; copy-pasting a testbed command weakens node-ID Sybil resistance on mainline. Gate behind `SWARTZNET_UNSAFE=1`/build tag, or loud WARN against a non-loopback DHT.

### gui
- **companion.go:211-233 (determinism)** — Follow rows are built by ranging a map, reshuffled every 4s poll; the index-based context menu then unfollows the wrong publisher (destructive). Sort rows deterministically and key selection by pubkey, not row index.
- **files_dialog.go:92-107 (correctness)** — `prioSelect.SetSelected` refires the stale `OnChanged` from the recycled widget's prior binding, writing the *previous* row's idx with the *new* priority on every 2s tick/scroll → wrong-file priority writes + redundant engine spam. Set `OnChanged = nil` before `SetSelected`, then reassign.
- **app.go:377-396 (correctness)** — `notificationLoop` starts with an empty `lastNotified`, so every already-seeding torrent fires a false "Download complete" on the first tick at startup. Prime the map with currently-seeding infohashes before the loop.

### security-identity
- **reputation.go:408-413 (correctness)** — `LoadSeedList` stores pubkeys without lowercasing while all lookups use lowercase hex; an uppercase/mixed-case seed entry never gets the seed bonus, silently defeating cold-start bootstrapping. Normalize via `strings.ToLower` (or reconstruct from decoded bytes); test an uppercase entry.
- **bloom.go:256-274 (error-handling)** — `readBloom` doesn't enforce `m>=1`/`k>=1`; a corrupt/truncated header with `m=0` passes validation, then `indices()` divides by zero → daemon crash on a confirm/auto-confirm. Reject `m==0`/`k==0` with an error; add a `bloom_errors_test` case.

## Nits

- **engine.go:1660-1669 (determinism)** — Batch restore can momentarily exceed `maxActiveDownloads` because N independent `autoDownload` goroutines race on `countActiveDownloads`; transient over-subscription, not corruption. Serialize promotion after `RestoreSession` finishes.
- **engine.go:1747-1774 (concurrency)** — `autoConfirmOnComplete` doesn't select on `bgCtx.Done()`, leaking a goroutine up to 24h after `RemoveTorrent`/shutdown. Add the `bgCtx` case.
- **indexer.go:459-460, content.go:97-100 (security)** — Infohash interpolated into a Bleve `QueryString` without escaping; safe only because HTTP callers validate hex. Use `NewTermQuery(...).SetField(...)` as the `SignedBy` path already does.
- **indexer.go:771-799 (determinism)** — `deleteByQueryLocked` has no terminal bound; if a delete is ever silently ineffective it spins forever holding the global index mutex. Add a bounded iteration guard that errors on no-progress.
- **zim.go:100-143 (performance)** — Random (non-LRU) cluster cache eviction can thrash on URL-sorted (not cluster-sorted) ZIM files, re-triggering up-to-64 MiB zstd decodes. Switch to a small LRU if it shows in profiling; otherwise document as best-effort.
- **feeler.go:29-45 (concurrency)** — `StartFeeler` comment claims idempotency but unconditionally `go feelerLoop(...)`; a double call (e.g. config reload) leaks goroutines and multiplies query fan-out. Add a `sync.Once`/atomic guard or fix the comment.
- **misbehavior.go:37-70 (error-handling)** — `ScoreMalformedResult`, `ScoreStaleTxID`, `ScoreInsufficientPoW` are defined but never charged; anti-abuse posture is weaker than comments imply. Wire them in or remove them.
- **protocol.go:535-549 (concurrency)** — `PeerAnnounce` sent fire-and-forget (`go ... _ = sender.Send`) per handshake, error discarded, unbounded by handshake rate. Bound via the engine write path; log Send failures.
- **query.go:386-413 (correctness)** — `mergeResponses` double-counts a peer that repeats an infohash in one Result, inflating Score and Sources diversity. Dedup per-peer infohashes within a Result.
- **schema.go:96-105 (dhtindex) (security)** — `DecodeValue`/`DecodePPMI` parse untrusted DHT bytes (up to ~64KB UDP) with no size bound while the encode side enforces `MaxValueBytes` — asymmetric. Reject payloads over the cap before unmarshaling.
- **read_btree.go:231-269 (companion) (security)** — `VerifyFingerprint` scans/allocs every leaf before checking count against `trailer.NumRecords`. Bail early once count exceeds `NumRecords`; cap `NumPages` against file size in `OpenBTree`.
- **publisher.go:339-345 (companion) (determinism)** — `recordFailure` sets `lastRefresh` on failure (incl. empty-index), conflating failures with successful publishes; throttles manual refresh and makes `Status().LastRefresh` misleading. Track `lastAttempt` vs `lastRefresh` separately.
- **subscriber.go:471-493 (companion) (concurrency)** — `Unfollow` during a sync pass re-inserts a stale `lastSync` entry; persistent state leak. Re-check membership under lock before write-back.
- **btree.go:269-274 (companion) (maintainability)** — Dead no-op block in `EncodeInterior` first-child separator handling; reads like it clears the first separator but doesn't. Drop it or make it enforce the empty-first-separator invariant.
- **server.go:678-694 (httpapi) (correctness)** — `handleStatus` never populates `out.Publisher.PubKey`, so the web UI's pubkey row never renders and clients always see empty. Populate from the publisher's identity.
- **server.go:486-490,520-524 (httpapi) (performance)** — Client-supplied `SwarmTimeoutMs`/`DHTTimeoutMs` are unbounded (only zero→default). Clamp to a ceiling (~30s) like `maxSearchLimit`.
- **bootstrap_https.go:60-75,100-111 (daemon) (security)** — Anchor-bootstrap fetch accepts any scheme incl. `http://`, despite a threat model assuming TLS integrity; an on-path attacker can inject trust anchors. Reject non-`https` (with a guarded localhost test exemption).
- **bootstrap.go:61-68,372-398 (daemon) (maintainability)** — `AnchorReputation`/`CandidateReputation` are documented, validated, defaulted, but never applied — silent no-op that misleads tuning. Thread the score through or remove the fields.
- **cmd_create.go:136-140 (cli) (error-handling)** — `create --seed` returns exit 0 on SIGINT instead of 130, unlike `cmdAdd`. Return `reportRunErr(ctx.Err(), stderr)`.
- **cmd_flag.go:48-52 (cli) (correctness)** — Infohash validation checks length but not hex-ness (also `cmd_index.go:32`, `cmd_files.go:49,102`); a 40-char non-hex typo gets a worse server error. Validate with `hex.DecodeString` locally.
- **app.go:399-418 (gui) (maintainability)** — `SelectTab` comment claims case-insensitive but only matches two exact spellings. Lowercase+`TrimSpace` the input or fix the comment.
- **identity.go:84-89 (security-identity) (correctness)** — Loaded private key's public half is trusted without re-deriving from the seed; corrupt trailing bytes yield silent verify failures far from the cause. Re-derive and compare, failing loud at load.
- **bloom.go:204-216 (security-identity) (correctness)** — Double-hashing collapses to a single bit when `h2 % m == 0`, raising FP rate for those inputs; harmless (only over-ranks). Force `h2 |= 1` (Kirsch-Mitzenmacher).
- **cluster.go:170-197, cluster_dht.go:173-193 (config-testlab) (maintainability)** — `Node.IndexDir` reports `root/bleve` but the real index opens at `root/bleve-idx`; dead config, a trap for the first test that trusts it. Reconcile the paths.
- **config.go:247-264 (config-testlab) (error-handling)** — `Config.Validate` doesn't reject `Regtest`/`DHTInsecure`, relying solely on an engine-side Warn an operator can miss. Consider an env/build-tag guard so dangerous flags fail closed.

## What's done well

- **Mainline wire-compat is genuinely preserved across every unit.** All `sn_search` traffic rides one LTEP user protocol; companion torrents are plain BEP-3 v1 metainfo discovered only via existing BEP-44/46 pointers; signing only adds optional top-level metainfo fields vanilla clients ignore; DHT access goes through the anacrolix extension API (no vendored patches, respecting MPL). The testlab `MiniPeer` faithfully models a vanilla client to catch leaks.
- **Layer isolation (L/S/D) is real, not aspirational.** Each layer carries its own response type and depends only on narrow local interfaces; `httpapi` re-declares every cross-package type and imports neither engine, companion, nor daemon. The seam is reconciled only at the daemon/httpapi boundary, exactly as the architecture mandates.
- **Concurrency hazards are handled with care where it counts.** The engine correctly offloads `sn_search` handling to goroutines to avoid the anacrolix `mainReadLoop` client-lock self-deadlock (with a precise explanatory comment); `fileTracker`'s `doneReplay` closes a genuine seed-at-startup race; the indexer's per-infohash counters are race-free via `sync.Map` + atomics; background goroutines are bounded by `bgCtx`.
- **Persistence is uniformly atomic and fail-soft.** Session, `.torrent` copies, manifest, trust/reputation/bloom, and config all use tempfile+rename with cleanup; signing is infohash-preserving (raw bencode bytes). Optional subsystems (bloom/reputation/trust/identity/session) degrade with a warning rather than crashing.
- **The ZIM extractor and RTF extractor are the reference model** the other extractors should follow: per-cluster caps, `zstd` max-memory, article caps, and — crucially — a cumulative *output*-byte cap. Every byte-parsing extractor wraps `Extract` in panic-recovery, and edge/truncation test coverage (`*_branches_test.go`, `*_truncated_test.go`) is unusually thorough.
- **Identity and signing discipline is sound at the core:** `LoadOrCreate` fails closed on wrong file permissions and never regenerates implicitly; signing uses a domain-separated payload binding pubkey to infohash; `VerifyBytes` length-checks before `ed25519.Verify`. The `--no-index` cascade to Layer-D is correctly wired and regression-tested.

## Recommended next steps

1. **Fix the five blocking remote-DoS/crash paths first** (engine zero-infohash, subtitle/mkv extractor allocs, companion B-tree recursion, swarmsearch unbounded sessions). These are unauthenticated, remotely triggerable, and three are confirmed crashes. Add the regression tests called out in each finding.
2. **Generalize the output-cap defense across all extractors** (the zip-bomb amplification group + ID3 alloc), and **plumb `context.Context` into `Extractor.Extract`** so the watchdog can actually interrupt — this closes the systemic "recover() can't save OOM" gap and the wedged-worker limbo state in one stroke.
3. **Add CSRF/Origin/Host middleware to `httpapi` and a non-loopback-bind guard.** Single highest-leverage web-security fix; protects every state-mutating endpoint at once.
4. **Close the fail-open state-transition bugs** (dhtindex zero-node publish + retry suppression, manifest ts-width estimate, CLI implicit identity minting + XDG divergence, daemon companion-dir misroute, engine paused-restore priority). These directly violate the project's deterministic/fail-closed rules.
5. **Reconcile documented-but-unimplemented surface** (DHT sharding schema, `ShareLocal==1`, unused misbehavior scores, dead anchor-reputation config, `StartFeeler` idempotency) — either implement or delete, since the wire schema doubles as an external spec.
6. **Sweep the GUI correctness trio** (non-deterministic follow ordering → wrong unfollow, file-priority `OnChanged` refire, false startup notification) — user-visible and one is destructive.
7. **Harden the remaining untrusted-parse boundaries defensively** (dhtindex decode size cap, indexer infohash→structured query, bloom `m=0` guard, seed-list lowercasing, key-file public-half verification) as a batched defense-in-depth pass.