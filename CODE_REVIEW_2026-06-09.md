# SwartzNet Whole-Codebase Code Review — 2026-06-09

**Reviewer:** Lead synthesis review (fresh audit + regression-check)
**Baseline:** prior 55-finding audit at commit `08b5df0`
**Scope:** engine, indexer-core, indexer-extractors, swarmsearch, dhtindex, companion, httpapi, daemon+config, identity/signing/trust/reputation, cmd, gui

---

## 1. Executive summary

SwartzNet is in **good overall health**. This pass was a fresh whole-codebase review layered with a regression-check against the prior 55-finding audit (commit `08b5df0`). The headline result: **every prior-audit fix that was spot-checked holds in current code** — the top remote-DoS (zero-infohash crash in `AddInfoHash`), the web-security suite (CSRF/Origin/Host guard, body cap, timeout clamping, non-loopback WARN), the swarmsearch session cap + reaper + RIBLT desync abort, the dhtindex keyword-path zero-node fail-closed guard, the identity public-half re-derivation, and the GUI `SelectTab` fix are all genuinely closed and survive adversarial re-checking.

The fresh review surfaced **50 confirmed findings**: **3 blocking**, **13 important**, **18 nits**, and **16 praise/holding-fix** confirmations. No wire-compatibility regressions and no cross-layer leakage were found; the L/S/D isolation boundary at `httpapi` is real (the package re-declares every cross-layer type and imports neither engine, companion, nor daemon).

The three blocking findings are all **untrusted-input resource-exhaustion** vectors that `recover()` cannot catch (OOM / CPU-and-alloc exhaustion): two deflate-bomb decompression paths in the document/HTML extractors, and an **incompletely-fixed** prior finding — the companion B-tree walker still allows cross-page DAG fan-in to blow up exponentially. The most important systemic theme is that several **stated limits are dead code** (fail-open): a documented per-session byte budget in swarmsearch, and two of three DHT put paths that skipped the zero-node guard the keyword path received.

---

## 2. Cross-cutting themes

The confirmed findings cluster into four themes:

### Theme A — Decompression / parser inputs lack an output LimitReader on the *decompressed* stream
The extractor pipeline is well-hardened at the **input** boundary (every extractor sets an input `io.LimitReader`, `readFull` caps allocations at 64 MiB), but several parsers feed the **decompressed** per-entry stream directly into `encoding/xml` or `x/net/html` with no bound, and their output-byte guard only fires *between* tokens. A single deflate-bomb text node buffers tens-to-hundreds of GiB inside one `Token()`/`Next()` call before any guard runs. This is the root cause of both extractor blocking findings (docx/odt/odp/pptx/epub and `htmltext`). `recover()` does not catch the resulting OOM.

### Theme B — Stated limits that are never enforced (fail-open on a documented budget)
Multiple subsystems **declare** a safety limit on the wire / in a struct but never enforce it:
- swarmsearch `SyncSession.maxBytes`/`bytesIn`/`bytesOut` — declared, echoed on the wire, never incremented or compared (unbounded `sync_records` ingest → repeated synchronous ed25519 verification on the read loop).
- dhtindex `PutInfohashPointer` and `PutPPMI` — discard `getput.Put` stats and report success on zero DHT nodes; only the keyword path got the prior-audit zero-node guard.
- companion `walkToLeaves` "visited at most once" comment is false for cross-page fan-in (the prior visited-set fix was never implemented; only the per-page strict-child check was).
This is the highest-leverage theme: these are violations of the **fail-closed** production-architecture rule, and several are *partial* prior fixes.

### Theme C — Protocol-violation errors logged but not failed-closed / charged
swarmsearch swallows hard protocol violations at `Debug` instead of tearing the session down and charging misbehavior: `ApplySymbols` budget-exceeded/desync, and `ApplyRecords` (no phase guard). The package already has the correct fail-closed pattern (`Finish(SyncStatusAborted)` + `sendSyncEnd` + `releaseSyncSession` + `chargeMisbehavior`) used elsewhere — these paths just don't invoke it.

### Theme D — Untrusted/local input reaching a sink without a precise bound or validation
A long tail of medium/low items: companion-torrent download with no declared-size cap (disk-fill), GUI magnet construction without URL-encoding (parameter injection from remote-sourced names), GUI flag-all reputation fan-out, single-row selection going stale after re-sort, Bloom loader one-sided bounds check (fail-open → later panic), and several CLI/HTTP input-validation laxities (`Sscanf` trailing garbage, unvalidated path segments).

---

## 3. Blocking findings

| Unit | File:line | Issue | Fix |
|------|-----------|-------|-----|
| indexer-extractors | `internal/indexer/extractors/docx.go:78,111` (also odt.go:62–68, odp.go:63–69, pptx.go:80/137, **epub.go:62**) | ZIP-document extractors feed the **decompressed** zip-entry reader into `xml.NewDecoder` with no `io.LimitReader`; the `out.Len() > maxOut` guard runs only between tokens. A single `<w:t>` CharData run buffers the whole DEFLATE-amplified (~1032:1) decompressed body in one `Token()` call → OOM that `recover()` cannot catch. | Wrap the entry reader: `rc2 := io.LimitReader(rc, maxOut)` before `xml.NewDecoder(rc2)`, at each call site (incl. epub.go and per-slide in `extractDrawingMLText`). Mirrors the correct `fb2.go:49` pattern. |
| indexer-extractors | `internal/indexer/extractors/htmltext.go:20–67` (caller `epub.go:117–124`) | `html.NewTokenizer(r)` never calls `tz.SetMaxBuf(n)` — `x/net/html` defaults `maxBuf=0` (unlimited). EPUB passes a raw deflate zip entry; one giant text token / unterminated tag buffers unbounded raw bytes before any token is emitted. OOM, uncatchable. | Belt-and-suspenders: `tz.SetMaxBuf(int(maxOut))` after `NewTokenizer`, **and** `io.LimitReader(rc, maxOut)` in `extractChapter`. Treat `ErrBufferExceeded` as graceful truncation (return accumulated text), not an error, so one oversized chapter doesn't poison the book. |
| companion | `internal/companion/read_btree.go:178–257` (false comment 173–177) | `walkToLeaves` recurses on attacker-controlled, **unsigned** interior `ChildIndex` bytes (the trailer sig covers only leaf fingerprints, not interior structure). The per-page strict-increase guard (line 226) prevents intra-page DAG/cycles but **not cross-page fan-in**: pages laid out `i → {i+1, i+2}` make root-to-leaf paths grow Fibonacci-ally. Reproduced **5,000,000+** `Piece()`/`DecodeInterior()` calls for a ~640 KiB / 40-piece file before capping (uncapped ≈ Fib(40) ≈ 10⁸ ops). `Find("")` defeats all pruning. **Incomplete prior fix** (CODE_REVIEW.md:52: visited-set + leaf cap was *not* implemented). | Add a shared visited-set keyed by piece index across the whole walk; fail closed on re-visit. Cap total leaf indices/pages visited at a small multiple of `NumPieces`. Honest pure trees consume each child exactly once, so this costs honest builders nothing. Correct the misleading comment. |

---

## 4. Important findings, grouped by package

### engine
- **`internal/engine/engine.go:1279–1314` — `FetchCompanionTorrent` downloads attacker-controlled companion torrent with no size cap** *(important, untrusted-input)*. The BEP-46 pointer resolves to an untrusted infohash; after metadata the code checks only `len(files)==1`, then `target.Download()`/`DownloadAll()` and polls `BytesCompleted() >= Length()`. `target.Length()` is fully attacker-controlled and unbounded — the only bound is the wall-clock `FetchTimeout`. **Failure mode:** a publisher serving fast pieces fills `DataDir` (disk exhaustion) before the timeout. The companion format is one small gzipped-JSON file. **Fix:** reject when `target.Length()` exceeds a fixed `maxCompanionBytes` (low tens of MiB) *before* downloading; return an error so the subscriber records a failure. Add a regression test for an oversized single-file companion torrent.

### indexer-core
- **`internal/indexer/pipeline.go:184–193, 302–339` — extract-watchdog timeout closes the file `Reader` out from under the still-running extractor goroutine (Read-after-Close race)** *(important, concurrency)*. On timeout, `safeExtract` returns `errExtractTimeout` while the child goroutine is still inside `ex.Extract(r, …)` reading `r`; `handle()` returns and fires the deferred `c.Close()`. In production `r` is an anacrolix `torrent.Reader` whose `Read`/`Close` mutate shared `storageReader` without synchronization — a genuine data race that can panic the leaked goroutine or corrupt state. `recover()` cannot make a memory race safe. The watchdog is the defense against adversarial input, so an attacker who wedges an extractor reliably triggers the racy close; leaked goroutines accumulate under sustained input. **Fix:** move the `io.Closer.Close` into the child goroutine's `defer` (after `Extract` returns); `handle()` closes only on non-timeout paths. This serializes Read-then-Close.

### swarmsearch
- **`internal/swarmsearch/sync_session.go:87–96, 425–442` — per-session byte/record budget declared but never enforced (unbounded `sync_records` ingest)** *(important, untrusted-input)*. `maxBytes`/`bytesIn`/`bytesOut` are declared and echoed on the wire (SPEC §2.9) but never incremented or compared. `ApplyRecords` has no phase guard and no record/frame counter; `onSyncRecords` accepts a `sync_records` frame on any registered session. A capability-gated peer sends one `sync_begin` then streams unlimited `sync_records` frames at that txid — each frame forces up to 500 synchronous ed25519 verifications on the **read-loop goroutine** (~25ms/frame, unbounded). The documented `limit_exceeded`/`maxBytes` path is dead code. *(Scope note: in the shipped wiring `RecordCache` is bounded via `SetMaxRecords(100_000)` FIFO, so the unmitigated harm is read-loop CPU, not memory.)* **Fix:** in `ApplyRecords`, add frame byte size to `s.bytesIn`, abort with `SyncStatusLimitExceeded` + release once over `maxBytes`; add a phase check; track a cumulative record count. In `onSyncRecords`, on limit-exceeded send `sync_end limit_exceeded`, release, and charge misbehavior.
- **`internal/swarmsearch/handler.go:367–378` — `ApplySymbols` budget-exceeded / desync errors only logged, not failed-closed or charged** *(important, concurrency)*. `onSyncSymbols` logs `ApplySymbols` errors at `Debug` and returns. The three error cases (txid mismatch, §2.5 index desync, `ErrSymbolBudgetExceeded`) are all unambiguous protocol violations but none tears down the session, emits terminal `sync_end aborted`, or charges misbehavior; the session sits in `PhaseSymbolsFlowing` limbo until the 2-minute reaper, and the peer can fire post-budget/desynced frames forever. **Fix:** thread a `reply ReplyFunc` into `onSyncSymbols` (it currently has none), then on these errors send `sync_end aborted`/`limit_exceeded`, `releaseSyncSession`, and `chargeMisbehavior(ScoreUnexpectedMessage)`.

### dhtindex
- **`internal/dhtindex/dht.go:143–146` — `PutInfohashPointer` reports success when the BEP-46 pointer reached zero DHT nodes (fail-open state transition)** *(important, wire-determinism)*. `getput.Put` returns nil error even when the get-traversal reached zero nodes (cold table / partition / all-reject) and the item never landed. The keyword path at `dht.go:249–264` was hardened against exactly this; the pointer path discards the stats and returns nil. The live caller `companion/publisher.go:334` then calls `recordSuccess()`, advancing `lastRefresh` and clearing `lastError` — the dashboard shows green for a pointer that never landed and subscribers cannot resolve the content index. Same fail-open class the prior audit fixed for keywords, missed on the pointer path. **Fix:** capture stats and mirror the keyword guard: `if stats == nil || stats.NumResponses == 0 { return errors.New(...) }`. Add a test asserting `recordFailure` is taken.

### companion
- **`internal/companion/read_btree.go:128–158` — `Find` collects unbounded duplicate records under a fan-in DAG** *(important, untrusted-input)*. Independently of (and compounding) the `walkToLeaves` blowup, `Find` appends every record of every returned leaf with no dedup and no cap on `len(out)`. Under cross-page fan-in the same leaf is returned K times, so its verified records are re-verified (ed25519 + optional PoW SHA256) and appended K times — an amplification vector even if the walk were bounded. Existing cycle tests cover only intra-page DAG edges, not cross-page fan-in. **Fix:** the primary `walkToLeaves` visited-set largely resolves this; as defense-in-depth, dedup leaf indices and/or cap `len(out)`, failing closed when exceeded.

### identity / signing / trust / reputation
- **`internal/reputation/bloom.go:274–285` — Bloom loader `bitsLen` check is one-sided; undersized bitset passes load then panics out-of-bounds on first Test/Add** *(important, security-correctness)*. `readBloom` validates only the upper bound (`bitsLen > (m+63)/64+1`). A file declaring large `m` but small `bitsLen` is accepted; the bitset is allocated at `bitsLen` while `indices()` returns positions in `[0,m)` and accesses `b.bits[idx/64]` — the next `Add`/`Test` panics (reproduced: `index out of range [13586] with length 1`). The file is local, so the trigger is a truncated/partial write, disk corruption, or tampering — turning a recoverable load error into a daemon-wide crash. **Fail-open** (a corrupt input must fail closed at parse time). **Fix:** require exact match `if bitsLen != (m+63)/64 { return error }` (the writer always emits exactly that count), rejecting both undersized and oversized at the parse boundary.

### cmd
- **`cmd/swartznet/cmd_create.go:117–148` — `create --seed` seeds with DHT disabled and an OS-assigned port, leaving a trackerless seed undiscoverable** *(important, security-correctness)*. `cmdCreate` unconditionally sets `cfg.DisableDHT = true` and `cfg.ListenPort = 0` (justified as a "one-shot tool"), but `--seed` opts into real seeding and prints "Seeding… (Ctrl-C to stop)". With DHT off and an ephemeral port, the seed has **no discovery mechanism** unless `--tracker` was passed — the project default is trackerless DHT discovery, so `create <path> --seed` advertises seeding while being unreachable. **Fix:** gate the DHT-off/port-0 minimal config on `!startSeed` (or at minimum print a prominent WARN when seeding with DHT disabled and no trackers).

### gui
- **`internal/gui/downloads.go:936–943, 891–894, 465–488` — single-row selection index goes stale after background re-sort, hitting the wrong torrent** *(important, concurrency)*. `dl.selected` is a row **index** into `dl.snaps`, but `pollLoop` re-runs `sortSnapsLocked()` every 2s and `toggleSort` re-sorts on click. The multi-select set is infohash-keyed to survive this; the single primary selection was left as a bare integer. When no multi-selection exists and a sort column is active, Remove/Pause/Files/Toggle-Index issued after an intervening poll operates on a **different** torrent. Remove is destructive. **Fix:** store the primary selection as an infohash string (mirroring `selectedSet`/`followSelectedKey`) and resolve by scanning `dl.snaps`.
- **`internal/gui/downloads.go:354–356` (also `search.go:356–358, 363`) — magnet links built by raw string concatenation without URL-encoding the display name** *(important, security-correctness)*. Both the Downloads context menu and Search hit menu build `"magnet:?xt=urn:btih:"+ih+"&dn="+name` with `name` unescaped. Names from search hits are **remote/DHT-sourced (attacker-controlled)**; an embedded `&` in the name (e.g. `foo&tr=evil`) injects bogus magnet params — and in `search.go` the same unescaped magnet is fed straight back into `AddMagnetURI`, making it **tracker/parameter injection**, not just clipboard breakage. Neither file imports `net/url`. **Fix:** `if name != "" { magnet += "&dn=" + url.QueryEscape(name) }` at all three sites.
- **`internal/gui/search.go:419–445` — `flagHit` demotes the reputation of EVERY known indexer when a hit has no source attribution** *(important, security-correctness)*. When `SourceTracker` has no recorded sources for the infohash (common for DHT/swarm hits, or after `sources.Forget`), `flagHit` falls back to enumerating `tracker.Snapshot()` and calling `RecordFlagged` on every pubkey — a single click penalizes all indexers, including trusted publishers. Attacker-weaponizable: seed an unattributed spam result, get it flagged, crater the user's whole reputation table. The sibling `confirmHit` deliberately does *not* fan out, confirming the asymmetry. **Fix:** drop the demote-all fallback; no-op with a user-visible note or record against the infohash only. *(Note: `httpapi/coverage_test.go:371 TestHTTPFlagFallbackNoSources` locks this behavior at the HTTP layer and will need updating.)*

---

## 5. Nits & praise

### Nits (18 confirmed — defensible hardening, not live vulnerabilities)
- **engine** — inbound `sn_search` reply path spawns one unbounded `go` write per `reply()` not gated by `snSearchSem` (`engine.go:756–774`, low multiplier); `autoDownload`/`autoIndex`/`upgradeMagnetSession` metadata waits ignore `bgCtx`, leaking goroutines up to 5–10 min after Close (`engine.go:1810–1815` et al.); `FetchCompanionTorrent` re-joins untrusted `info.Name` into `DataDir` (`1324–1329`, defense-in-depth — anacrolix already enforces sub-path); `restoreEntry` joins `TorrentFile` with no basename validation (`1678–1685`, local trusted state).
- **indexer-extractors** — EPUB per-chapter cap reuses the 256 MiB *input* budget as the text budget (~512 MiB worst-case accumulation vs intended 64 MiB; `epub.go:91–106`, downgraded important→nit); archive name-list extractor omits its documented 4 MiB output cap (`archive.go:84–90`, tar.gz branch genuinely unbounded today); EXIF `cnt`-driven size can integer-overflow the bounds check only on 32-bit targets (`exif.go:224–253`, not in the 64-bit-only release matrix; `recover()` catches it).
- **indexer-core** — `AllTorrentDocs`/`ContentDocsForInfoHash` hold the global mutex across an unbounded deep-pagination walk with no `maxPages` backstop (`indexer.go:423–457, 463–497`); documented `SignedBy`-only search is rejected by the empty-query guard (`587–588, 664–666`).
- **swarmsearch** — `routeResult`/`routeReject` deliver into a pending query from any peer, not just asked targets; txids are guessable (`query.go:267–316`, bounded impact).
- **dhtindex** — `PutPPMI` lacks the zero-node guard (latent — no production caller yet; `ppmi_dht.go:77–80`); `GetInfohashPointerInfo` decodes untrusted value with no size cap (`dht.go:193–199`, signature-bound); `PutPPMI` uses raw `seq+1` instead of overflow-clamping `nextSeq` (`ppmi_dht.go:72`).
- **httpapi** — file-index `Sscanf("%d")` accepts trailing garbage (`server.go:1051–1056`); no length cap on search query before Bleve (`server.go:490–518`, bounded by 1 MiB body, localhost-only).
- **daemon+config** — anchor-fetch goroutine misses anchors added after `New` via `FallbackToHTTPS` (dead cold-start path in dev default; `daemon.go:209–222`); `LoadFollowFile` decodes local config with no size bound (`follows.go:40–55`); `MaxTrackedPublishers` cap counts anchors and can starve crawl/endorsement admissions silently (`bootstrap.go:355–380`, latent — default anchor set empty).
- **cmd** — `create --identity` silently ignored unless `--sign` is also passed (`cmd_create.go:80–97`, fail-open footgun); `files set-priority` interpolates an unvalidated index into the URL path (`cmd_files.go:106–123`).
- **gui** — search limit parsed with unchecked `Sscanf`, negative/garbage passes to swarm layer (`search.go:130–133`, downstream clamps); `lastNotified` map in `notificationLoop` grows without bound (`app.go:38, 413–430`, single-goroutine slow leak).

### Praise (16 holding-fix / well-built confirmations)
- **engine** — `AddInfoHash` zero-infohash + `recover()` guard (the campaign's top blocking remote-DoS) holds with regression test.
- **indexer-extractors** — ID3 `tagSize` clamp, FLAC `readFull` 64 MiB cap, ZIM cluster cap, MKV oversized-element discard all intact.
- **indexer-core** — `SignedBy` attribution end-to-end and the `Stats` content-count-scaled `maxPages` ceiling hold; `AddedAt` round-trips into companion `BuildFromIndex`.
- **swarmsearch** — session cap + reaper, RIBLT desync abort, `ShareLocal==1` fail-closed, feeler CAS idempotency, malformed/stale-txid charges all hold.
- **dhtindex** — keyword-path zero-node fail-closed guard and `EstimateValueSize` Ts-stamp hold and are well-commented.
- **companion** — 1 GiB decompressed gzip-bomb cap holds; publisher is cleanly fail-closed on empty index and never advances `lastRefresh` on failure (matches the production-architecture exactly-once rule).
- **httpapi** — CSRF/Origin/Host guard fails closed across all mutating methods (empty Host, `null` Origin, bare `localhost` edges all verified by executing the functions); body-limit wraps the guard.
- **daemon+config** — companion publisher writes to `CompanionDir` not `DataDir`; anchor-fetch goroutine is cancel-tracked and joined on Close; `config.Validate` fails closed on Regtest/DHTInsecure outside test binaries.
- **identity/signing/trust/reputation** — identity public-half re-derivation, seed-list lowercasing, Bloom `h2 |= 1` Kirsch-Mitzenmacher stride all hold.
- **cmd** — explicit `--key`/`--identity` no longer silently mints a key; SIGINT→130, local hex infohash validation, `SWARTZNET_UNSAFE=1` gating all hold.
- **gui** — `SelectTab` case-insensitive/trimmed matching across all five tabs holds.

---

## 6. Regression-check note

The prior 55-finding audit (commit `08b5df0`) is **holding well**. Every fix spot-checked across all units verified as present and correct in current code (16 explicit praise/holding-fix confirmations above), and **zero** prior fixes were found regressed or bypassed.

**Two prior fixes are incomplete / not carried across, however:**

1. **companion `walkToLeaves` (CODE_REVIEW.md:52) — partial.** The prior audit's recommended remedy was a **visited-set + leaf cap**; only the per-page strict-child-index check was implemented. That check is per-page-local and cannot see **cross-page fan-in**, so the exponential-blowup DoS the finding targeted is **still reproducible today** (5M+ `Piece()` calls for a 40-piece file). This is a **blocking** finding (§3). The "visited at most once" comment is now affirmatively false.

2. **dhtindex zero-node fail-closed guard — applied to keyword path only.** The prior audit hardened `AnacrolixPutter.Put` (keyword) against zero-node fail-open; the same class was **not** carried over to `PutInfohashPointer` (important, §4 — live companion consumer records false success) or `PutPPMI` (nit — latent, no production caller yet). Recommend factoring the zero-node assertion into a shared helper so the three put paths cannot drift again.

Everything else from the prior campaign is intact and load-bearing.

---

## 7. Appendix — verification accounting

| Severity | Confirmed |
|----------|-----------|
| blocking | 3 |
| important | 13 |
| nit | 18 |
| praise / holding-fix | 16 |
| **total** | **50** |

**Findings dropped as already-fixed / false-positive during verification: 0.**

Note on verification rigor: although no findings were *dropped*, the verifier actively **re-scoped** several during the adversarial pass — e.g. the EPUB text-budget finding was **downgraded important→nit** (the "256 GiB accumulation" / "unbounded tokenizer" premise was false; real worst case ~512 MiB), and `PutPPMI`'s zero-node guard was **downgraded important→nit** (latent scaffolding, no production caller). The swarmsearch ingest finding's unbounded-**memory** sub-claim was corrected (the shipped wiring bounds `RecordCache` to 100k records; the genuine harm is read-loop CPU). The EXIF integer-overflow finding was confirmed to be **unreachable on the 64-bit-only release matrix**. These re-scopings (rather than wholesale drops) are what the empty dropped-set reflects: each candidate was tightened to exactly the failure mode that survives scrutiny.
