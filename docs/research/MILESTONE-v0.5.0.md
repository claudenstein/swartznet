# Milestone v0.5.0 — "Aggregate" redesign

| Field | Value |
|---|---|
| Branch | `overnight/test-harness-2026-04-20` |
| Status | Feature-complete, pending PR merge + production anchor pubkey list |
| Commits | 26 on-branch (1 research + 10 roadmap + 15 post-roadmap) |
| Design sources | `PROPOSAL.md`, `SPEC.md`, `ROADMAP.md` (this directory) |

v0.5.0 ships the "Aggregate" redesign of SwartzNet's distributed
layer: one BEP-44 pointer per publisher instead of per-keyword,
the real index living inside a companion torrent, and rateless
set-reconciliation over `sn_search` replacing per-keyword DHT
polls.

## What shipped

### New byte formats

- **PPMI** (Publisher Pointer Mutable Item) — one BEP-44 value
  per publisher at the fixed salt `SHA256("snet.index")`.
  Schema in `internal/dhtindex/ppmi.go` (v: ih, commit?,
  topics?, ts, next_pk?).
- **B-tree index torrent** — magic `SNAGG\0`, piece-aligned
  pages (root/interior/leaf/trailer). Trailer signed with
  ed25519, binding the tree to the publisher. Format in
  `internal/companion/btree.go`; build path in
  `build_btree.go`; read path in `read_btree.go`.
- **Hashcash PoW** on every record (default D=20 in production,
  currently 0 for dual-read migration). Mint +
  `SignAndMineRecord` in `internal/companion/pow.go`.

### New wire protocol

- **`sn_search` msg_types 4–8** — sync_begin, sync_symbols,
  sync_need, sync_records, sync_end. Wire encoding in
  `internal/swarmsearch/sync_wire.go`; state machine in
  `sync_session.go`; LTEP dispatch in `handler.go`.
- **`BitSetReconciliation`** (services bit 9) — capability gate.
  Peers without the bit receive `reject code 2` on any sync
  frame. Documented in `docs/06-bep-sn_search-draft.md` and
  `SPEC.md §2`.
- **Rateless IBLT** — minimal in-repo implementation in
  `internal/swarmsearch/riblt.go` using a graduated-degree
  cycle (`mod = 2^(1+idx%12)`). Converges in ~3d symbols for
  symmetric difference d.

### New runtime components

- **`companion.BTreeReader`** — trailer-sig-verified prefix
  walker, per-record sig + PoW re-verification.
- **`companion.BuildBTree`** — deterministic layout, BFS piece
  assignment, self-consistent fingerprint.
- **`swarmsearch.RecordCache`** — thread-safe map keyed by
  RIBLT element ID, implements `RecordSource` interface so
  the sync responder pulls matching records live.
- **`swarmsearch.SyncSession`** — one end of an RIBLT exchange
  with phase state machine (Idle → Begun → SymbolsFlowing →
  Needed → Fulfilled → Ended).
- **`dhtindex.PPMIPutter` / `PPMIGetter`** — put/get against
  mainline DHT via anacrolix; in-memory equivalent for tests.
- **`dhtindex.Lookup` gains PPMI path** — PPMI resolution first,
  legacy per-keyword fallback for publishers who haven't
  migrated.
- **`daemon.Bootstrap`** — three-channel cold-start orchestrator
  (anchor PPMIs, BEP-51 crawl candidates, peer_announce
  endorsement gossip) plus HTTPS anchor fallback.

### Integrations

- **Engine attaches `RecordCache`** at startup; always non-nil.
- **Engine mints records on torrent-add** — `TokenizeAll` over
  the torrent name, `SignAndMineRecord`, `RecordCache.Add`.
  Silent-skip when no identity (headless tests).
- **Daemon wires Bootstrap** — runs channel A in a background
  goroutine on startup; exposes `d.Bootstrap` for introspection.
- **Sync handler queries the RecordSource** — when attached,
  `sync_begin` responders stream real `SyncSymbols` instead
  of the zero-record `SyncEnd` stub.

### Observability

- **`GET /aggregate`** HTTP endpoint: PPMI enabled, known
  indexers + labels, record source kind, cache size,
  advertised ServiceBits, bootstrap counters.
- **`swartznet status` CLI** appends an Aggregate block with
  the same fields, best-effort (older daemons skip cleanly).
- **Web UI Status tab** — new "Aggregate (v0.5)" card with
  3 bundle-content smoke tests guarding against JS regressions.
- **Native GUI Status tab** — matching card in the Fyne UI
  via direct `Lookup`/`Protocol` introspection.

### Ops tooling

- **`swartznet aggregate build`** — reads JSONL records,
  signs + mines + packs into a B-tree file. Offline.
- **`swartznet aggregate inspect <file>`** — trailer metadata.
- **`swartznet aggregate find <file> <prefix>`** — prefix query
  with optional `--verify` for full fingerprint check.

### Regression gates

- **`BenchmarkPrefixQuery`** — 50k records, narrow prefix,
  target <50 ms (observed ~16.8 ms on i9-14900K).
- **`BenchmarkRIBLTConverge_Diff{0,10,100,500}`** — parameterised
  convergence cost reporting symbols/op and bytes/op.
- **`TestAggregateEndToEnd`** (daemon package) — full publisher
  → PPMI → subscriber → prefix-query flow in one pass.

### Docs

- `docs/05-integration-design.md` §4.3 gets a supersession
  callout + new §4.3.1 describing the PPMI layout, migration
  staging, bootstrap channels, and sync complement.
- `docs/07-bep-dht-keyword-index-draft.md` same supersession
  notice; draft text stays verbatim for the dual-read window.
- `docs/06-bep-sn_search-draft.md` capability table gains bit 9;
  message-types table gains rows for msg_types 4-8.
- `CHANGELOG.md` "Aggregate redesign" section under Unreleased.

## Exercise the flow

### 1. Offline build + query

```
$ cat > /tmp/recs.jsonl <<EOF
{"kw":"ubuntu","ih":"1111111111111111111111111111111111111111","t":1}
{"kw":"ubuntu","ih":"2222222222222222222222222222222222222222","t":2}
{"kw":"linux","ih":"3333333333333333333333333333333333333333","t":3}
EOF

$ swartznet aggregate build --in /tmp/recs.jsonl --out /tmp/index.bin --seq 1
Built Aggregate index
  records: 3, pages: 3, bytes: 49152, fingerprint: …

$ swartznet aggregate inspect /tmp/index.bin
  publisher pk: …, records: 3, min PoW bits: 0, …

$ swartznet aggregate find /tmp/index.bin ubu
Matches for prefix "ubu": 2 records
  1111…  ubuntu  t=1
  2222…  ubuntu  t=2
```

### 2. Running daemon

Start any daemon (the CLI `swartznet add` does this transparently).
The engine auto-mints one record per keyword token on every
torrent add. Inspect:

```
$ curl -s http://localhost:7654/aggregate | jq
{
  "ppmi_enabled": true,
  "known_indexers": 5,
  "record_source_kind": "cache",
  "record_cache_size": 147,
  "services": "00000000000002ef",
  "bootstrap": { "anchors": 0, "admitted": 0 }
}
```

Or use the same CLI command users already run:

```
$ swartznet status
…
Aggregate (Layer D v0.5, PPMI + B-tree + RIBLT):
  PPMI enabled:         true
  known indexers:       5
  record source:        cache
  record cache size:    147
  services advertised:  0x00000000000002ef
  bootstrap anchors:    0
  bootstrap admitted:   0
```

### 3. Peer-to-peer sync

When two `sn_search` peers meet and both advertise
`BitSetReconciliation`, either can initiate a sync session via
`sync_begin`. The responder's `RecordSource` is queried
automatically; matching records stream back as RIBLT symbols
then materialize as `SyncRecords` on demand. No DHT round-trip
required.

## What's deferred

These remain post-v0.5 follow-ons; none blocks the release.

1. **Hardcoded anchor pubkeys** — `DefaultAnchorPubkeys` is an
   empty slice today. A release build populates it with real
   project/operator keys; until then, operators can pass keys
   via `BootstrapOptions.AnchorHexes` programmatically.
2. **BEP-51 crawler wiring** — `Bootstrap.CandidateFromCrawl`
   exists and is tested, but no engine component calls it.
   Requires hooking into the anacrolix/dht sample_infohashes
   stream.
3. **`peer_announce` endorsement gossip** — `endorsed` field
   from SPEC §3.3 is documented but the handler doesn't yet
   extract + route endorsements into
   `Bootstrap.IngestEndorsement`.
4. **Production hashcash difficulty** — currently minted at
   D=0. Bumping to D=20 (MinPoWBitsDefault) requires a schema
   bump coordinated with reader enforcement.
5. **Per-record reputation** — today `reputation.Tracker` keys
   on publisher pubkey only. PROPOSAL §9.6 sketches
   per-(pubkey, record-prefix) reputation for a future bump.
6. **OHTTP + FrodoPIR query privacy** — opt-in privacy design
   from PROPOSAL §2.3 section B. Unstarted; separate track.
7. **Dandelion++ publisher anonymity** — same note.

## Migration for operators

The v0.5 series dual-writes **and** dual-reads. Existing v0.4.x
clients can continue to interoperate:

- **Publishers on v0.5** emit PPMIs (via engine) AND still use
  the legacy per-keyword DHT publisher (until it's retired in
  v0.7 per PROPOSAL §6 Phase 3).
- **Readers on v0.5** try PPMI first, fall back to per-keyword
  legacy for publishers without a PPMI. `Lookup.Query` handles
  the routing transparently.
- **v0.4.x readers** only see the legacy items; they stay
  functional but miss any PPMI-only publishers.

No operator action required on upgrade. The new state (cache
size, bootstrap counters) is visible via `swartznet status`
and the three UI surfaces.

## Test coverage

- **~180 new test functions** across the touched packages
  (`companion/`, `dhtindex/`, `swarmsearch/`, `daemon/`,
  `httpapi/`, `cmd/swartznet/`, `gui/`, `engine/`).
- `go test -race ./... -count=1 -short` runs clean across all
  15 packages.
- Two documented rough-edges caught in-development:
  1. P3.1 `contributes()` — initial constant-1/3 rate produced
     no pure symbols for d≥5; graduated-degree cycle fixed.
  2. P3.1 `Key()` — initial linear first-8-bytes-as-u64 made
     every symbol look "pure" in the decoder; FNV-1a fixed.

## Commit history (chronological)

| Hash | Subject |
|---|---|
| `1b3dc06` | docs/research: protocol survey + "Aggregate" redesign proposal |
| `8aa333b` | companion: P1.1 — B-tree page encode/decode |
| `c0a153c` | dhtindex+companion: P2.1 PPMI schema + P1.2 B-tree builder |
| `13237ea` | companion: P1.3 — B-tree reader with prefix-query walker |
| `ac20327` | swarmsearch: P3.1 — rateless IBLT encoder/decoder |
| `4d7624a` | companion+swarmsearch: P5.1 — hashcash PoW mint + misbehavior hooks |
| `5112c9f` | dhtindex: P2.2 — PPMI DHT put/get + memory store coexistence |
| `0c07af6` | dhtindex: P2.3 — Lookup.Query resolves PPMIs, falls back to legacy |
| `4ab0df1` | swarmsearch: P3.2 — sn_search sync-session wire + state machine |
| `4dba164` | daemon: P4.1 — three-channel cold-subscriber bootstrap |
| `338f16d` | daemon: P5.2 HTTPS anchor fallback + Final E2E integration |
| `5fdcfe9` | swarmsearch: wire sync msg_types 4-8 + BitSetReconciliation gate |
| `9e6f613` | swarmsearch: plumb RecordSource so sync replies carry real records |
| `768fef6` | docs: sync §4.3 + BEP-07 draft for Aggregate PPMI inversion |
| `0b53b43` | companion+swarmsearch: SPEC §7 regression benchmarks |
| `3416638` | cli: swartznet aggregate {inspect,find} — Aggregate ops tooling |
| `b60fbc1` | cli: swartznet aggregate build — sign + pack JSONL into a signed index |
| `6d6d7a6` | swarmsearch: RecordCache — in-memory RecordSource for the engine |
| `fac200d` | httpapi: GET /aggregate — Aggregate subsystem introspection |
| `ec7a791` | cli: render Aggregate block in 'swartznet status' |
| `160b5e6` | httpapi/web: render Aggregate card on the Status tab |
| `fe09827` | gui: Aggregate card on the Status tab |
| `303b484` | engine: attach RecordCache as Aggregate RecordSource at startup |
| `8a62e6c` | engine: mint Aggregate records on torrent add |
| `a36f44b` | daemon: wire Aggregate Bootstrap into daemon.New |
| `6ca5a5b` | httpapi+cli: expose Aggregate Bootstrap state on /aggregate and status |
