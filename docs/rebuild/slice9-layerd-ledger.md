I have full grounding on the on-disk seams. Here is the synthesized, implementation-ready ledger.

---

# SLICE 9 IMPLEMENTATION LEDGER — Layer D (BEP-44 DHT keyword index, legacy per-keyword path behind `RecordBackend`)

**Scope law.** Rebuild the legacy per-`(publisher, keyword)` BEP-44 path as the shipping baseline behind a swappable `RecordBackend` (default `LayerDMode=legacy`). PPMI / Aggregate-B-tree / crawler worker-pool are Slice 12 — deferred. Mainline-compat is absolute: no new DHT verb, no new reserved bit, no new UDP port; Layer-D items are byte-indistinguishable from any other BEP-44 mutable item. **On any conflict, SPEC wins over legacy** — the two behavioral changes SPEC mandates over legacy are (a) lookup queries the *most-distinctive* token, not `tokens[0]`, and (b) `/search` fans the three layers concurrently.

New packages: `contracts/dhtschema`, `internal/dhtindex`. Everything else is edits to existing on-disk files (verified this session).

---

## 1. `contracts/dhtschema` — the KeywordValue codec (NEW, Tier-0 frozen contract)

**Path:** `/home/kartofel/Claude/swartznet/contracts/dhtschema/dhtschema.go` (+ `dhtschema_test.go` golden vectors).

**Marshaller decision (resolves the bencode open question):** use **`github.com/anacrolix/torrent/bencode`**, NOT `contracts/bencode`. Reason is load-bearing correctness: the signed BEP-44 `v` bytes must be byte-identical to what `dhtindex`'s put path re-marshals through `bep44.Put.Sign` (which uses anacrolix bencode), and `contracts/bencode` is a dict-level codec (`DecodeDict`/`EncodeDict` over `map[string]Bytes`, verified — no struct-tag/`omitempty` support). `contracts/dhtschema` therefore imports stdlib + `anacrolix/torrent/bencode` only, and knows nothing of the DHT server, engine, or Bleve. Golden vectors freeze the exact bytes so a future re-implementation is pinned.

```go
const (
    MaxValueBytes = 1000 // BEP-44 hard cap on the bencoded v field
    MaxSaltBytes  = 64   // BEP-44 salt cap
)

type KeywordValue struct {
    Ts         int64        `bencode:"ts"`                // unix ts (informational; seq orders updates)
    Hits       []KeywordHit `bencode:"hits"`
    More       int          `bencode:"more,omitempty"`    // RESERVED, v1 never sets
    NextPubKey []byte       `bencode:"next_pk,omitempty"` // RESERVED, v1 never populates
}
type KeywordHit struct {
    IH []byte `bencode:"ih"`           // 20-byte SHA-1 infohash
    N  string `bencode:"n,omitempty"`  // short torrent name
    S  int    `bencode:"s,omitempty"`  // seeders
    F  int    `bencode:"f,omitempty"`  // file count
    Sz int64  `bencode:"sz,omitempty"` // size bytes
}
```

- `EncodeValue(v) ([]byte, error)` — stamp `Ts=time.Now().Unix()` if zero; normalize nil `Hits → []KeywordHit{}`; marshal; **reject `len(out) > MaxValueBytes`** ("keyword unpublishable"). Field names `ts/hits/more/next_pk` + `ih/n/s/f/sz` are frozen for the ≥12-month legacy-read back-compat contract (SPEC §5.2).
- `DecodeValue(payload) (KeywordValue, error)` — reject empty; **reject `len(payload) > MaxValueBytes` BEFORE `bencode.Unmarshal`** (a hostile node returns up to ~64 KiB regardless of the storage cap; the pre-unmarshal cap bounds the untrusted `Hits` allocation). Cap is **inclusive** — exactly 1000 decodes.
- `EstimateValueSize(v) int` — stamp a **representative non-zero `Ts`** (10-digit, ~16 bytes) before marshalling, so the eviction loop never lets a near-cap entry pass estimation (`tsi0e`=7 bytes) then fail the live encode (permanently unpublishable). Returns `MaxValueBytes+1` on the unreachable marshal error.
- `SaltForKeyword(keyword string) ([]byte, error)` — return `[]byte(keyword)` verbatim (do **NOT** lowercase; contract-require pre-lowercased tokenizer output — resolves the salt-lowercase open question); error on empty; **error/DROP if `len > MaxSaltBytes`, never truncate** (truncation aliases distinct keywords onto one SHA1 target). `MaxSaltBytes` + the drop rule live here.
- `SaltForShard(kw, n)` — shard 0 = bare keyword, n≥1 = `"<kw>#<n>"`. **Exported but unused in v1** (reserved scaffolding for `More`; Slice-12).

**Golden vectors (`dhtschema_test.go`):** freeze exact bencode bytes for a canonical `KeywordValue{Ts:1700000000, Hits:[{IH:20B, N:"ubuntu", S:42, F:1, Sz:...}]}`; assert `EncodeValue` byte-equality, `DecodeValue` round-trip, at-cap (exactly 1000) accept, >1000 reject-before-unmarshal, empty reject, and a **vanilla-decode** test (`bencode.Unmarshal` into `map[string]any` finds `hits`/`ih`/`ts`) for wirecompat row 8.3-D.

---

## 2. The `RecordBackend` swappable seam

**Path:** `/home/kartofel/Claude/swartznet/internal/dhtindex/backend.go`. Shape mirrors the Slice-8 narrow-interface pattern (`swarmsearch.RecordSource`/`RecordSink`, verified on disk). One port at publish/lookup granularity; **record bytes stay codec-owned** in `contracts/dhtschema` (never exposed through the port), so the port doesn't become a lowest-common-denominator between the legacy CAS read-modify-write model and the Slice-12 B-tree/RIBLT model.

**Resolved method set (resolves the port-granularity open question — throttle/eviction/manifest sit INSIDE the backend as format-specific state; the indexer-set/reputation/scoring orchestration sits ABOVE the port in `Lookup`):**

```go
type RecordBackend interface {
    // Publish makes this node's own hit discoverable under each name-keyword.
    // Owns format-specific persistence, oldest-hit eviction, per-keyword
    // throttle, and MarkPublished/MarkFailed state.
    Publish(ctx context.Context, keywords []string, hit dhtschema.KeywordHit) error
    // Refresh re-announces everything (driven by the Publisher worker's ticker).
    Refresh(ctx context.Context) error
    // Retract scrubs an infohash from everything this backend published.
    Retract(ctx context.Context, ih [20]byte) error
    // Lookup resolves ONE already-chosen token against ONE indexer's namespace,
    // returning that indexer's raw hits (no merge/score — Lookup does that).
    Lookup(ctx context.Context, indexerPub [32]byte, token string) ([]dhtschema.KeywordHit, error)
    // Status reports per-keyword publish state for /publish and /status.
    Status() PublisherStatus
    Close() error
}
```

- **`legacyKeyword` (default impl, `internal/dhtindex/legacy_keyword.go`)** composes `Putter`/`Getter` (`AnacrolixPutter`/`AnacrolixGetter`; in-memory fakes for tests) + the persistent `Manifest` (publisher.json, oldest-hit eviction, 55m throttle, `MarkPublished`/`MarkFailed`) + `SaltForKeyword` + the shared `checkPutStats`. `Publish` = per-kw `Manifest.AddHit` + `publishOne`; `Refresh` = `refreshAll`; `Retract` = `Manifest.RemoveAllHits`; `Lookup` = `SaltForKeyword(token)` → `Getter.Get(indexerPub, salt)` → `.Hits`.
- **Selection:** one line in `daemon.New` keyed on `config.LayerDMode`. `Validate` accepts `LayerDMode ∈ {"legacy"}` this slice.
- **How Aggregate slots in (Slice 12):** add `aggregatePPMI` and `composite` (dual-write / legacy-then-aggregate dual-read) impls behind the same port; widen `Validate` to accept them; no change to `Publisher`/`Lookup`/`searchmux`/`httpapi`. `Putter`/`Getter` + `checkPutStats` stay shared beneath all backends so the fail-closed guard cannot drift.

---

## 3. `internal/dhtindex` — Publisher, Lookup, DHT wrappers, primitives (NEW)

**Path:** `/home/kartofel/Claude/swartznet/internal/dhtindex/{dht.go, publisher.go, lookup.go, manifest.go, crawler.go, backend.go, legacy_keyword.go}`. Boundary: **never imports httpapi, never imports Bleve, never imports `internal/indexer`** (see token note). Imports `contracts/dhtschema`, `contracts/token`, `anacrolix/{dht/v2, torrent/bencode, krpc}`, `internal/reputation`.

### 3.1 DHT Put/Get wrappers + `checkPutStats` + Exp=2h

**Target derivation:** `target = SHA1(publisher_pubkey ‖ salt)` via `bep44.MakeMutableTarget(pub, salt)`; salt = lowercased keyword bytes verbatim. In-memory fakes spell it `sha1.Sum(append(pub[:], salt...))`.

**Put** (`AnacrolixPutter.Put`): `EncodeValue(value)` → `bencode.Unmarshal` back to `interface{}` (so `bep44.Put.Sign` re-marshals identically) → `target := bep44.MakeMutableTarget(a.public, salt)` → `seqToPut := func(seq int64) bep44.Put { put := bep44.Put{V:v, K:&pubArr, Salt:salt, Seq:nextSeq(seq)}; put.Sign(a.private); return put }` → `stats, err := getput.Put(ctx, target, a.server, salt, seqToPut)` → `return checkPutStats(stats, "put")`. `nextSeq(seq)=seq+1` clamped at `math.MaxInt64` (never wraps).

**Get** (`AnacrolixGetter.Get`): `target := bep44.MakeMutableTarget(pubkey, salt)` → `res, _, err := getput.Get(ctx, target, a.server, nil, salt)` → `DecodeValue([]byte(res.V))`. **Signature verification happens inside the anacrolix get path**; `DecodeValue` still re-applies the ≤1000 pre-unmarshal cap.

**`checkPutStats` (shared across keyword / BEP-46 pointer / PPMI so it cannot drift):**
```go
func checkPutStats(stats *traversal.Stats, what string) error {
    if stats == nil || stats.NumResponses == 0 {
        return fmt.Errorf("dhtindex: %s reached zero DHT nodes", what)
    }
    return nil
}
```
Load-bearing: `getput.Put` returns nil error even at zero nodes; failing closed makes `publishOne` call `MarkFailed` (NOT advance `LastPublished`), so the 55m throttle does not suppress the retry of an undiscoverable keyword.

**Exp=2h is ALREADY pinned** in `internal/engine/engine.go:181-183` (`if sc.Exp == 0 { sc.Exp = 2*time.Hour }`, inside `ConfigureAnacrolixDhtServer`) — verified on disk. Do **not** re-add in dhtindex; the pin is engine-owned. Without it every stored mutable item is instantly expired (put succeeds, get returns "value not found").

### 3.2 Publisher (worker + manifest)

- `DefaultPublisherOptions`: `RefreshInterval=1h`, `PutTimeout=30s`, `QueueSize=64`, `MinPutInterval=55m`. `RegtestPublisherOptions`: `5s / 5s / 64 / 100ms` (never production; engine logs a prominent startup warning when `Regtest`). Engine selects Regtest options when `cfg.Regtest`, mirroring the existing `runRecordPrune` pattern. **These stay `PublisherOptions`/Regtest overrides — NOT user config flags** (resolves that open question).
- `run()` = `select` over `stopCh` + buffered `tasks` channel (`Submit`, non-blocking, full queue **drops** with a warn) + `time.NewTicker(RefreshInterval)`; tick → `backend.Refresh` (re-publishes every manifest entry, checks `stopCh` between each) → Save. `Submit(PublishTask{InfoHash(20B), Name, Seeders, FileCount, SizeBytes})`; `handleTask` guards `len(InfoHash)==20`, `token.Tokenize(task.Name)` → ≤8 name keywords → `backend.Publish`. One bad keyword never stops the worker.
- **55m per-keyword throttle** (in `legacyKeyword.publishOne`): skip the put (debug `put_throttled`, no network I/O) when `MinPutInterval>0 && !entry.LastPublished.IsZero() && time.Since(entry.LastPublished) < MinPutInterval`. Zero disables (tests). Self-DoS guard (anacrolix has no put rate cap).
- **Oldest-hit eviction** (`Manifest.AddHit`): replace-in-place by infohash (keeps seeders/name fresh, replacement may be larger) else append; then `for len(entry.Hits) > 0 && dhtschema.EstimateValueSize(dhtschema.KeywordValue{Hits: entry.Hits}) > dhtschema.MaxValueBytes { entry.Hits = entry.Hits[1:] }`. Multi-shard spill (`More`) is NOT wired — eviction is the only v1 oversize handling. Emptied keyword entries are deleted so `Refresh` never re-publishes empty values.
- **Manifest:** JSON at `~/.local/share/swartznet/publisher.json`, mode 0600, atomic tmp+rename; per-keyword `ManifestEntry{Hits, LastPublished, LastError, PublishCount}`; null entries normalized on load. `MarkPublished` sets time, clears `LastError`, `PublishCount++`; `MarkFailed` records `LastError` only (no counter bump). Restart resumes full hit lists (BEP-44 has no incremental update).
- **`Status() → PublisherStatus{PubKey, keywords:[{Keyword, HitsCount, LastPublished, PublishCount, LastError}]}`** feeds `/publish` and `/status`.

### 3.3 Lookup (read side)

- `Query(ctx, query) (*LookupResponse, error)`: **`keyword := token.MostDistinctive(token.Tokenize(query))`** — the ONLY chooser (fixes §6.1). Import `contracts/token` **directly** (Tier-0 leaf); do NOT import `internal/indexer.PickLookupToken` (would create a `dhtindex→indexer` layering edge — DECISIONS C16/S4-3). The one-liner is byte-identical to `PickLookupToken`; pin equivalence with a shared golden ("new ubuntu" → "ubuntu"). Empty tokens → error `"query produces no tokens"`.
- Snapshot the known-indexer set (self-pubkey auto-added; `sn_search peer_announce` gossip via `NotePublisherSeen`; `AddIndexerHex` validates 64-hex). **Reputation gate:** when a tracker is wired and `MinIndexerScore > 0`, keep only indexers where `tracker.Threshold(pubkey, minScore)`; default 0 = disabled. Empty / filtered-to-empty set → **empty response, not an error**.
- **Parallel** fan-out: one goroutine per indexer calling `backend.Lookup(ctx, indexerPub, keyword)`; per-indexer errors tolerated (one responder suffices); record `HitsReturned` and per-hit `(infohash→pubkey)` attribution to a `SourceTracker` for targeted `/flag`. Merge by 40-hex infohash (max seeders, first non-empty name, first non-zero size/files, accumulate Sources).
- **Scoring:** `mean(source reputations; 0.5 when no tracker) + 0.05·(extra sources, cap +0.2) + 0.25 flat Bloom boost`, clamped [0,1]. Sort: `BloomHit desc, Score desc, source-count desc, Name asc`. Source label falls back to first 16 hex of pubkey.

```go
type LookupResponse struct {
    IndexersAsked     int
    IndexersResponded int
    Hits              []LookupHit
}
type LookupHit struct {
    InfoHash string // 40-hex lowercase
    Name     string; Seeders int; Size int64; Files int
    Sources  []string; Score float64; BloomHit bool
}
```
**PPMIsResolved/PPMIMissing are OMITTED in Slice 9** (resolves that open question — the `RecordBackend` port, not response-struct fields, is the forward-compat mechanism; Slice 12 adds them without breaking the daemon field-by-field adapter). Multi-word search stays single-keyword DHT lookup + client-side intersection; **AND/OR MUST NEVER be pushed into the DHT** (resolves that open question).

### 3.4 BEP-51 `sample_infohashes` primitive (in-scope; crawler worker deferred)

```go
func SampleInfohashes(ctx, server *dht.Server, addr dht.Addr, target krpc.ID) (SampleInfohashesResult, error)
// server.Query(ctx, addr, "sample_infohashes", dht.QueryInput{MsgArgs: krpc.MsgArgs{Target: target}})
// nil-guard server/addr; surface res.ToError(); error "reply has no r dict" when r==nil.
type SampleInfohashesResult struct {
    Samples  []krpc.ID       // 20-byte infohashes (0-len for old clients)
    Interval int64           // politeness seconds — caller MUST wait ≥Interval
    Num      int64           // node's total tracked infohashes
    Nodes    []krpc.NodeInfo // merged IPv4 Nodes + IPv6 Nodes6
}
```
Standard BEP-51 verb — no new verb/port. **Extract ONLY this primitive** from legacy `crawler.go`; `CrawlOnce`/`PublisherFromMetainfo`/`crawler_tick`/admission are Slice 12.

### 3.5 BEP-46 pointer primitive (lands with the package; consumed by companion Slice 10)

`PutInfohashPointer(salt, ih)` puts `{ih:20B, ts:unix(omitempty)}`, fails closed via the shared `checkPutStats`; `GetInfohashPointer` validates `len(ih)==20` and shares the ≤1000 pre-unmarshal cap (`decodePointerValue`). Included in `dhtindex` because `checkPutStats` + the decoder cap are shared; **no Slice-9 CLI/daemon deliverable depends on it** — it's the seam companion (Slice 10) consumes.

---

## 4. `searchmux` — 3-layer concurrent DHT searcher (§6.1 sequential-search fix)

**Path:** `/home/kartofel/Claude/swartznet/internal/searchmux/searchmux.go`. Current state (verified): `Mux{Local, Swarm}`, `sync.WaitGroup` fan-out, `Query.DHT bool` already carried, `Result.DHT` a reserved comment. Edits:

- Add `DHTSearcher` interface (declared here, satisfied by a daemon adapter — searchmux imports no engine/dht code): `DHTSearch(ctx context.Context, q string, limit int) (*dhtindex.LookupResponse, error)`.
- Add `Mux.DHT DHTSearcher`; add `Result.DHT *dhtindex.LookupResponse` + `Result.DHTErr error` (import `contracts`-free: searchmux already imports `indexer`/`swarmsearch` native types, add `dhtindex`).
- Convert the `WaitGroup` fan-out to `golang.org/x/sync/errgroup` (matches ARCHITECTURE `Result{*indexer.SearchResponse, *swarmsearch.QueryResponse, *dhtindex.LookupResponse}`). Add a **third concurrent branch** gated on `q.DHT && m.DHT != nil`; write `res.DHT`/`res.DHTErr`. Three native response types — **never merged into a shared hit type**. Layer-L error stays fatal (500); Layer-S/D errors surface inline.

---

## 5. Engine — publish-on-GotInfo (behind `--no-dht-publish`) + retract-on-removal

**Path:** `/home/kartofel/Claude/swartznet/internal/engine/{index.go, engine.go, records.go}`. Engine has **no dhtindex wiring today** (verified). Additions:

- **Construct** `dhtindex.Publisher` + `dhtindex.Lookup` from `e.dhtServer()` (engine.go:406, unwraps `AnacrolixDhtServerWrapper`) + the identity installed via `SetSigner` (records.go) + the reputation tracker. Skip when `cfg.DisableDHT` (no server). Select `RegtestPublisherOptions` when `cfg.Regtest`.
- **Publish hook** in `autoIndex` (index.go:64), immediately after `<-h.T.GotInfo()` and **alongside `e.mintAggregateRecords(h)` (line 78)**, BEFORE the Layer-L gate: submit `PublishTask{InfoHash, Name:h.T.Name(), Seeders, FileCount, SizeBytes}` with keywords from **`token.Tokenize(h.T.Name())` — NAME ONLY, never content tokens** (the §5.2 [weird] invariant). Gate on `hasSigner && !cfg.NoIndex && !cfg.DisableDHTPublish`. Per-torrent indexing-off still publishes existence to Layer D; only `--no-index`/`--no-dht-publish` suppress network-visible publication.
- **Retract** in `RemoveTorrent` (engine.go:503): call `publisher.Retract(ih)` before/after `h.T.Drop()`.
- **Expose** `DHTPublisher()`, `DHTLookup()`, and `PublisherStatus() dhtindex.PublisherStatus` for the daemon. Wire the swarm `PublisherObserver.NotePublisherSeen` → `Lookup.AddIndexer` so gossiped publisher pubkeys enter the lookup set; self-pubkey auto-added on `SetSigner`.
- **Privacy cascade (mandatory):** `--no-index` is already mirrored to `Config.NoIndex` before engine construction (verified daemon pattern). Ensure the keyword Publisher worker is suppressed by NoIndex/DisableDHTPublish while `Lookup` + pointer getter stay alive (leech-only Layer D) — skipping only Bleve would keep the publisher announcing under the user's identity (documented privacy regression).

---

## 6. `httpapi` `/search` dht block + `/publish` status + daemon adapters (zero-import law)

**`internal/httpapi/dto.go`** (verified — has the `// dht block lands with its slice` comment, `SearchParams` lacks `DHTTimeoutMS`):
- Add `DHTBlock{ IndexersAsked int "indexers_asked"; IndexersResponded int "indexers_responded"; Hits []DHTHit "hits"; Error string "error,omitempty" }` and `DHTHit{ InfoHash, Name, Size, Seeders, Score, BloomHit, Sources }`.
- Add `Dht *DHTBlock "dht,omitempty"` to `SearchResponse`; add `Dht *DHTBlock` to `SearchResult`; add `DHTTimeoutMS int` to `SearchParams`. `SearchRequestBody.DHTTimeout` already exists.

**`internal/httpapi/search.go`** (verified): thread `DHTTimeoutMS: req.DHTTimeout` into `SearchParams`; after the swarm inline line add `out.Dht = res.Dht`. **Layer-D error is a 200-with-`dht.error` string (§5.9), never a 5xx; Layer-L error stays the only 500.** The `dht` block appears only when the request asked for DHT AND the collaborator is wired.

**`GET /publish` status route** (resolves that open question): new route reusing the frozen `PublisherStatus`/`PublisherKeywordEntry` DTOs (already in dto.go, verified); wired via `Options.PublisherStatus func() httpapi.PublisherStatus`. `PublisherPubKey` already renders independent of a publisher collaborator.

**`internal/daemon/search_adapter.go`** (verified — `swarmSearchAdapter`/`swarmBlock` is the template):
- Add `dhtSearchAdapter{eng}` satisfying `searchmux.DHTSearcher`, calling `a.eng.DHTLookup().Query(ctx, ...)`.
- In `daemon.go` (verified: `mux := &searchmux.Mux{Swarm: ...}` at line 184), set `mux.DHT = &dhtSearchAdapter{eng: eng}` when `!cfg.DisableDHT`, and select `LayerDMode` (one line).
- In the `search` closure: when `p.DHT`, extend the context deadline with `clampSearchTimeout(p.DHTTimeoutMS, defaultDHTTimeout)` (`defaultDHTTimeout=5s`, ≤`maxSearchTimeout=30s`, `+swarmCtxTimeoutGrace` 500ms). Add `out.Dht = dhtBlock(res.DHT, res.DHTErr)`.
- Add `dhtBlock(resp *dhtindex.LookupResponse, err error) *httpapi.DHTBlock` — on error, a block carrying only `Error`; else map `IndexersAsked/IndexersResponded/Hits` field by field. `httpapi` imports none of these types.
- Wire `Options.PublisherStatus` from `eng.PublisherStatus()`.

---

## 7. `config` — ListenHost / DisableIPv6 / DHTBootstrapAddrs + new fields

**Path:** `/home/kartofel/Claude/swartznet/internal/config/config.go` (verified).
- **Already exist and sufficient for the isolated regtest DHT:** `ListenHost` (49), `DisableIPv6` (70), `DHTBootstrapAddrs` (67), plus `DisableDHT` (56), `DisableDHTPublish` (59, "consumed by the Layer-D slice" — now this slice), `Regtest`/`DHTInsecure` (behind `SWARTZNET_UNSAFE=1`), `NoIndex`. Engine consumes all of these in `ConfigureAnacrolixDhtServer` (verified engine.go:166-184: honors bootstrap `StartingNodes`, sets `NoSecurity` on `DHTInsecure`, pins `Exp=2h`).
- **Add:** `LayerDMode string` (default `"legacy"`; `Validate` accepts `{"legacy"}` this slice) and `MinIndexerScore float64` (default 0 = reputation gate disabled, SPEC §5.7). Both resolve the open questions.

---

## 8. `cmd/swartznet crawl-probe` + `--help` (+ `--no-dht-publish`)

**Path:** `/home/kartofel/Claude/swartznet/cmd/swartznet/cmd_crawl_probe.go` (NEW) + dispatch in `main.go run()` (verified: `switch` at line 47, `printUsage` at 72).

- Stateless one-shot, no daemon: `net.ListenPacket("udp","127.0.0.1:0")` → `dht.NewServer(&dht.ServerConfig{Conn: conn, NoSecurity: true, Passive: true})` (NoSecurity ⇒ arbitrary node ID; Passive ⇒ never answers inbound); both `defer .Close()`. Issue ONE `SampleInfohashes(ctx, srv, dht.NewAddr(udp), target)`.
- Flags: `--addr host:port` (required), `--target` (40-hex/20-byte, **default fresh-random per run** via `rand.Read`), `--timeout-ms` (5000), `--json`. Text output: header/peer/target/interval(`%ds`)/num-tracked/samples(hex)/closest nodes (`id @ addr`). JSON: `{addr, target, samples[], interval, num, nodes[]}` (samples/nodes hex, 2-space indent). Query failure → stderr + `exitRuntime`.
- **CLI-help rule (both in the same change):** add `crawl-probe` to `main.go` dispatch + `printUsage` command index with a one-line description and a runnable per-command example (`swartznet crawl-probe --addr 127.0.0.1:<port> --json`); **also add the missing `--no-dht-publish` flag to `cmd_add.go` and `printUsage`** (a config field with no CLI surface — SPEC §6.3). Note `cmd_search.go` already threads `--dht`/`--dht-timeout-ms` and the deadline formula `swarmTimeout+dhtTimeout+2000ms` (verified) — no change needed there beyond confirming it stays.

---

## 9. §6 defects to FIX + §5 invariants — each → a test

**§6 defects Slice 9 must NOT reproduce:**
| Defect | Fix | Test |
|---|---|---|
| First-token lookup (`tokens[0]`) | `token.MostDistinctive(token.Tokenize(query))` is the sole chooser | `TestLookupPicksMostDistinctiveToken` — "new ubuntu" derives the `ubuntu` target, NOT `new` |
| Publishing content tokens | Publish **only** `Tokenize(torrent.Name)` — preserve, do not "fix" | `TestPublishNameTokensOnly` / `TestNoContentTokensPublished` — a content marker in a file is never a published keyword |
| Sequential `/search` | 3-layer `errgroup` fan-out | `TestSearchmuxConcurrentLatency` — three slow layers, elapsed ≈ max, not sum |

**§5 invariants each → a test:**
| Invariant | Test |
|---|---|
| Exp=2h pin | `TestLayerDDHTClusterRoundTrip` (regtest 2-node) — put then immediate get returns the value |
| Zero-storing-node put = failure | `TestPutFailsClosedOnZeroNodes` — dead-node server; every put path errors `"reached zero DHT nodes"` |
| ≥1000-byte reject BEFORE unmarshal | `TestDecodeValueRejectsOversizeBeforeUnmarshal` (+ pointer decoder) |
| Encode at-cap accept / over-cap reject | `TestEncodeValueCap` (exactly-1000 accepts; 1001 rejects) |
| `EstimateValueSize` Ts-width headroom | `TestEstimateValueSizeStampsTs` — near-cap entry that passes eviction also survives the live encode |
| Oldest-hit eviction | `TestAddHitEvictsOldestUnderCap` — front-of-slice dropped until ≤1000 |
| 55m per-keyword throttle | `TestPublisherMinPutIntervalThrottles` — 2nd submit updates the manifest hit but issues no put |
| Hourly refresh re-announces all | `TestRefreshRepublishesAllEntries` |
| Retract-on-removal | `TestRetractScrubsInfohash` — infohash gone from every keyword; emptied entries deleted |
| ≥12-month legacy-read back-compat | `TestDecodeLegacyKeywordValue` golden (frozen bytes decode) |
| Byte-indistinguishable | `wirecompat: TestVanillaBep44` (row 8.3-D) — stock `bep44.Verify`+`bep44.Check`, decode `v` into `map[string]any` finds `hits`/`ih`/`ts` |
| Layer-error asymmetry (§5.9) | `TestSearchLayerErrorAsymmetry` — Layer-L err → 500; Layer-D err → 200 with `dht.error` |

---

## 10. DoD → test mapping (every Slice-9 DoD bullet)

| DoD bullet | Test / harness |
|---|---|
| Regtest isolation: `ListenHost=127.0.0.1` + `DisableIPv6=true` + placeholder `DHTBootstrapAddrs=[127.0.0.1:1]` (+`DHTInsecure`) | `TestLayerDDHTClusterRoundTrip` harness asserts all three set; a negative guard test shows a missing knob makes the round-trip fail (write-token source-IP reject / router leak) |
| A publishes, B searches, gets the infohash via BEP-44 | `TestLayerDDHTClusterRoundTrip` — node A `Publish`, node B `Query` returns A's infohash (B learns A's pubkey via `peer_announce` gossip or `AddIndexerHex`) |
| Lookup queries the most-distinctive token (crafted multi-token name) | `TestLookupPicksMostDistinctiveToken` |
| Only `Tokenize(name)` keywords published — no content tokens | `TestNoContentTokensPublished` |
| Zero-storing-node put surfaces as failure via shared `checkPutStats` | `TestPutFailsClosedOnZeroNodes` |
| >1000-byte remote value rejected before unmarshal | `TestDecodeValueRejectsOversizeBeforeUnmarshal` |
| `/search` latency ~max(layer), not the sum | `TestSearchmuxConcurrentLatency` |
| Layer-L error → 500, Layer-D error → inline 200 | `TestSearchLayerErrorAsymmetry` |
| `crawl-probe --addr … --json` one-shot BEP-51, fresh-random target, JSON samples/interval/num/nodes | `TestCrawlProbeJSON` (cmd-level: throwaway `127.0.0.1:0` NoSecurity+Passive server against a stub responder) |

---

## 11. Resolved open questions

1. **First-token vs most-distinctive in Slice 9:** FIX NOW (SPEC-over-legacy). `dhtindex.Lookup` calls `token.MostDistinctive(token.Tokenize(query))`. No like-for-like `tokens[0]` baseline.
2. **RecordBackend replaces vs wraps Putter/Getter:** `Putter`/`Getter` + `checkPutStats` live **beneath** the port, shared by all backends; `legacyKeyword` composes them + Manifest. Port granularity = `Publish/Refresh/Retract/Lookup(per-indexer)/Status`. Manifest/throttle/eviction are format-specific → inside `legacyKeyword`; indexer-set/reputation/scoring → in `Lookup` above the port.
3. **KeywordValue stays on the wire; `contracts/record` is separate:** `legacyKeyword` keeps the `KeywordValue` dict; `contracts/record.Record` (RIBLT substrate) is the Slice-12 Aggregate backend — never put on the BEP-44 wire in Slice 9.
4. **Codec package/marshaller:** `contracts/dhtschema` owns `KeywordValue`/`EncodeValue`/`DecodeValue`/`EstimateValueSize`/`SaltForKeyword`/`MaxSaltBytes`, using **anacrolix `torrent/bencode`** (byte-identity with the signing path; `contracts/bencode` is dict-level and unsuitable). Golden vectors pin the bytes.
5. **Refresh/throttle constants:** stay `PublisherOptions`/`RegtestPublisherOptions` (not user flags).
6. **Reserved `more`/`next_pk`:** kept in the struct with `omitempty` (frozen wire shape, decoder-tolerant); never populated in v1.
7. **Salt lowercasing:** `SaltForKeyword` keeps input verbatim; contract-requires pre-lowercased tokenizer output (preserves target byte-identity).
8. **Token chooser import:** `dhtindex` imports `contracts/token` directly (no `internal/indexer` edge), per DECISIONS C16/S4-3; equivalence to `PickLookupToken` pinned by a golden test. (The Slice-10-vs-9 numbering in S4-3's parenthetical is stale — Layer D is Slice 9.)
9. **`LookupResponse` PPMI fields:** OMITTED in Slice 9; added in Slice 12. The port is the forward-compat seam.
10. **New config fields:** `LayerDMode="legacy"` and `MinIndexerScore=0` added this slice.
11. **`/publish`:** dedicated `GET /publish` route reusing the frozen `PublisherStatus` DTO.
12. **`DHTTimeoutMS`:** added to `SearchParams`, threaded via `dhtBlock` adapter (default 5s, ≤30s, +500ms grace). CLI deadline formula already in `cmd_search.go`.
13. **Multi-word AND/OR:** single-keyword DHT lookup + client-side intersection only — never pushed into the DHT.
14. **Admission policy for Slice-9 `Lookup`:** consume `AddIndexer` as-is; deny-by-default admission rework is Slice-12 crawler territory. `MinIndexerScore=0` keeps the gate off by default.
15. **BEP-51 primitive extraction:** only `SampleInfohashes` (pulls in `anacrolix/krpc`); crawler worker deferred.

---

## 12. DEFERRED to Slice 12 (do NOT build in Slice 9)

- **PPMI / Aggregate:** legacy `ppmi*.go`, the SNAGG B-tree companion, RIBLT-over-DHT, hashcash PoW mining raise, the `aggregatePPMI` and `composite` `RecordBackend` impls (`LayerDMode ∈ {composite, aggregatePPMI}` refused by `Validate` this slice), and PPMI's `SHA256("snet.index")` double-hash salt.
- **Crawler worker-pool:** `CrawlOnce`, `crawler_tick.go`, `PublisherFromMetainfo`, sink/admission-from-crawl, frontier expansion, per-node `Interval` politeness loop. Only the `SampleInfohashes` primitive + `crawl-probe` are Slice 9.
- **`Lookup.PPMIGetter` / `SetPPMIGetter` / `resolvePPMIs` and the `PPMIsResolved`/`PPMIMissing` response fields.**
- **Reserved-not-wired scaffolding:** `SaltForShard` shard≥1, `KeywordValue.More`, `KeywordValue.NextPubKey` (key rotation) — frozen on the wire, activated later.
- **The deny-by-default `admission` rework** (permissive live behavior preserved until the crawler slice).

**Mainline-compat, restated:** Layer D rides only standard BEP-44 (mutable put/get with salt), BEP-46 (pointer at literal `_sn_content_index` — do not "fix" the salt), and BEP-51 (`sample_infohashes`). Items are ordinary `SHA1(pubkey‖salt)` targets with opaque ≤1000-byte bencoded `v`, standard ed25519 over `4:salt…3:seqi…e1:v…`, monotonic seq, salt ≤64 bytes — no new verb, reserved bit, port, or classifiable marker.