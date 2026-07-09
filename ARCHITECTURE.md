# SwartzNet Architecture (Rebuild)

> Status: authoritative design for the from-scratch rebuild. Backbone = the panel's top-ranked proposal ("Keep the Seams, Kill the Defect Classes"), with the runners-up's best ideas grafted in where the judges flagged them: a type-level "advertised-peer" transport token and a composite dual-read Layer-D adapter from the Hexagonal proposal, and a frozen `contracts/*` golden-vector tier and wire-confinement framing from the Vertical-Slices proposal. Where judges split, this document favors **spec fidelity and invariant safety over elegance**.

---

## Guiding principles

SwartzNet is a mainline-compatible BitTorrent client with built-in distributed full-text search. Everything below serves one wager (SPEC §0): you can add real search to BitTorrent **without breaking mainline compatibility** by stratifying it into three independently-degrading layers — L (local Bleve), S (`sn_search` LTEP peer-wire), D (BEP-44 DHT keyword index) — defended only by local, individually-owned trust (persistent ed25519 identity, signed torrents, allowlists, Bayesian reputation, known-good Bloom filter). No token, no consensus, no global ranking authority.

### The four hard invariants, restated as design constraints

These are immutable (SPEC §0, §2.12). Each is enforced **structurally**, not by discipline:

1. **Mainline wire compatibility is absolute.** No new reserved handshake bit, no new DHT verb, no new UDP port. A vanilla peer must observe only BEP-3/5/9/10/44/46/51 traffic — the only novelty it may see is one ignorable name (`sn_search`) in the LTEP `m` dict, and BEP-44 items indistinguishable from anyone else's. *Design constraint:* every byte that can reach a non-SwartzNet observer is produced by a codec in a leaf `contracts/*` package with frozen golden byte-vectors; `sn_search` frames can only be transmitted through a transport that **requires a capability token minted from a recorded remote advertisement** (so "send to a non-advertising peer" is uncompilable); the advertised services mask is produced by exactly one pure function. A `wirecompat` conformance suite makes this a CI merge gate, not a convention.

2. **Identity is persistent and load-bearing.** `identity.key` (mode exactly 0600) backs publisher reputation and signing; losing it loses standing. Never regenerate implicitly. *Design constraint:* one `identity` package with exact-0600 enforcement, seed→pubkey re-derivation on load, and auto-create **only** at the default XDG path (an explicit `--identity` is load-only). `Load` returns an `IsDefaultPath` flag so the "auto-create only at default" rule has one enforcement site.

3. **The control plane is localhost-only and unauthenticated by construction.** The HTTP API's security model *is* the loopback bind plus a CSRF/DNS-rebind guard. *Design constraint:* `httpapi` has no concept of auth tokens or remote sessions — that code does not exist, so it cannot accidentally become network-trusting. A future authenticated remote surface must be a *new* frontend that supplies its own auth.

4. **One daemon, three coequal frontends.** CLI (with embedded web UI), native Fyne GUI, and any future frontend obtain a fully-wired node from a single constructor; subsystem lifecycle lives in exactly one place. *Design constraint:* `daemon.New` is the sole constructor with a fixed startup/teardown order, and the two places the legacy let frontends diverge — search reconciliation and confirm/flag semantics — are relocated into shared owners (`searchmux` and `daemon.Confirm/Flag`) so frontends are pure presentation and **cannot** hold different behavior.

### Legacy as behavioral reference only

The `legacy-snapshot` branch is a **behavioral** reference — it tells us *what the software does and is meant to do*, verified against `file:line` anchors in SPEC §2/§5. It is **not** a structural template. Do not copy its module map, its central-hub engine imports, or its duplicated derivations. Preserve every SPEC §5 rule that touches structure or wire/disk format; **fix**, never reproduce, every SPEC §6 defect. Where SPEC §7 leaves intent unresolved, this document picks the most defensible default and records it under *Assumptions & deferred questions*.

---

## System overview

Three frontends, one daemon, three strictly-isolated search layers:

- **One daemon.** `daemon.New(ctx, Options)` wires the whole node. CLI, embedded web UI (served by the CLI binary), and native GUI all consume it and differ only in presentation.
- **Three search layers, strict isolation** (SPEC §2.12, §5.8). Layer L never touches the wire; Layer S never touches Bleve internals; Layer D never touches the HTTP API. Each layer owns its own response type (`indexer.SearchResponse`, `swarmsearch.QueryResponse`, `dhtindex.LookupResponse`); they reconcile **only** at the edge, and never via a shared cross-layer hit type.
- **A frozen leaf tier.** Every cross-implementation wire/disk derivation lives once, in a stdlib-only `contracts/*` package with checked-in golden byte-vectors, imported by whichever subsystems must agree byte-for-byte.

### Dependency diagram

Arrows point **from a package to the package it depends on** (`A → B` means "A imports B"). The graph is a DAG; there are no cycles. `httpapi` is a pure sink of locally-declared interfaces and imports **no** subsystem.

```
                                   ┌───────────────────────── cmd/ (swartznet CLI, swartznet-gui, dht-smoke)
                                   │                                   │
                                   ▼                                   ▼
                              ┌─────────┐                         ┌────────┐
                              │ daemon  │────────────────────────▶│  gui   │   (gui also → daemon, searchmux,
                              └─────────┘   (single wiring point)  └────────┘    capability; presentation only)
        ┌──────────────┬──────────┼──────────┬───────────┬──────────────┬─────────────┐
        ▼              ▼          ▼          ▼           ▼              ▼             ▼
   ┌─────────┐   ┌──────────┐ ┌────────┐ ┌───────────┐ ┌─────────┐  ┌─────────┐  ┌─────────┐
   │ httpapi │   │ searchmux│ │ engine │ │ admission │ │companion│  │  ...    │  │ httpapi │
   │ (no sub-│   └────┬─────┘ └───┬────┘ └─────┬─────┘ └────┬────┘  │ adapters│  │  DTOs   │
   │  imports)│       │           │            │            │       │(in daemon)│ └─────────┘
   └─────────┘        │           │            │            │
                      ▼           ▼            ▼            ▼
             ┌──────────────────────────────────────────────────────────┐
   Layer     │  indexer(L)     swarmsearch(S)      dhtindex(D)           │
   subsystems │   │   │           │   │              │   │  │            │
             └───┼───┼───────────┼───┼──────────────┼───┼──┼────────────┘
                 ▼   ▼           ▼   ▼              ▼   ▼  ▼
            ┌──────────────────────────────────────────────────────────┐
   Tier 1   │ capability   signing   trust   reputation   config        │
   leaves   │      │          │                                          │
            └──────┼──────────┼──────────────────────────────────────────┘
                   ▼          ▼
            ┌──────────────────────────────────────────────────────────┐
   Tier 0   │ contracts/{bencode, token, record, sign, dhtschema,       │
   frozen   │            riblt, snagg, ltepwire}      identity          │
   leaves   │ (stdlib-only + anacrolix/bencode; golden byte-vectors)    │
            └──────────────────────────────────────────────────────────┘
```

Key acyclicity facts:
- `httpapi` imports **nothing** from the subsystem tree; the daemon owns adapters that translate engine/layer types into `httpapi`'s locally-declared DTOs field-by-field (SPEC §5.8).
- `searchmux`, `admission`, and `httpapi` collaborate only through **interfaces they define themselves**, satisfied by adapters wired in `daemon`.
- The `contracts/*` and `identity` leaves import only stdlib (plus `anacrolix/bencode` for the raw-bytes-preserving codec). They can never import upward, so a format change is a compile-or-golden-test failure in exactly the packages that must agree.
- **The `sn_search` transport seam is declared in `swarmsearch`, not `engine`.** The opaque `swarmsearch.PeerToken` and the `swarmsearch.Transport`/`SendExtension` interface live in `swarmsearch`; `engine` imports that port and provides the concrete implementation. So `engine → swarmsearch` is strictly one-directional and provably acyclic (engine depends on the port and satisfies it; `swarmsearch` never imports `engine`) — the graft cannot introduce an engine↔swarmsearch cycle.
- **Only `engine` (BEP-3/5/9/10/11/19/27 + the LTEP transport) and `swarmsearch` (sn_search frame bodies) and `dhtindex`/`companion` (BEP-44/46/51 items) touch the peer/DHT wire.** This confinement — grafted from the Vertical-Slices proposal — makes invariant #1 auditable by inspecting four packages.

---

## Modules

Each module lists its responsibility, its **boundary** (what it must not know), key exported types, and the SPEC §2 capabilities it owns.

### contracts/ — the frozen leaf tier (grafted from Vertical-Slices)

Stdlib-only packages, each with checked-in golden byte-vectors, holding every derivation that MUST stay byte-identical across implementations or across on-disk file versions. This generalizes the backbone proposal's single `record` package into the full set the SPEC repeatedly warns about (§5.1, §5.2, §5.5, §5.6). A second-language implementation must be buildable from these packages plus their fixtures alone.

- **contracts/bencode** — raw-bytes-preserving bencode; decodes metainfo as `map[string]bencode.Bytes` so the info dict passes through untouched. *Owns:* nothing user-facing; the substrate for signing and all wire codecs. *Must not know:* torrent semantics.
- **contracts/token** — `Tokenize(name)` (lowercase Unicode letter/digit runs, ≥3 bytes, stopword + extension deny-lists, dedup first-appearance, cap 8, applied after filtering) and `MostDistinctive(tokens)` (longest non-stopword). *Owns:* the tokenization contract behind §2.6/§2.4. *Fixes* SPEC §6 "queries the FIRST token" — `MostDistinctive` is the only lookup-token chooser. *Must not know:* Bleve, DHT.
- **contracts/record** — `SignedRecord{Pk,Kw,Ih,T,Pow,Sig}`, `SigMessage()` = `pk‖kw‖ih‖LE64(t)‖uvarint(pow)`, and `RecordID()` = `SHA-256(pk‖kw‖ih‖t_LE)` **excluding pow and sig** (two valid signings of one semantic record must share an ID or reconciliation never converges — SPEC §5.1). *Owns:* the Aggregate record identity shared by Layer S sync, Layer D aggregate backend, companion. *Must not know:* how records are transported.
- **contracts/sign** — the 34-byte signing payload `"SN-TORRENT-V1|"‖SHA1(info)` and the ed25519 sign/verify over it. **The verification taxonomy is three-way, not two (preserve all three — SPEC §5.6):** missing `snet` fields → `ErrNotSigned` (benign, a normal outcome); a crypto failure → `ErrBadSignature` (firm tamper signal, with the `Signature` struct **still fully populated**); a wrong pubkey/sig **length** → a plain error that is **neither sentinel**. UIs distinguish benign-vs-tamper on exactly this split, so the third (length) case must not be folded into either sentinel. *Must not know:* file I/O.
- **contracts/dhtschema** — `KeywordValue`/`PointerValue`/`PPMIValue` bencode codecs, all hard-capped and length-checked at the 1000-byte BEP-44 limit **before unmarshalling** (SPEC §5.2); `EstimateValueSize` stamps a representative non-zero `Ts` before marshalling. **Preserve (SPEC §5.2):** a keyword whose UTF-8 salt exceeds **64 bytes is DROPPED, never truncated** — truncation would alias distinct keywords onto one DHT target; this is a hard drop, owned here (or in `contracts/token`), not a silent clamp. *Must not know:* the anacrolix DHT server.
- **contracts/riblt** — RIBLT coded symbols with the FNV-1a-64 **nonlinear** element key and the `contributes()` 12-step modulus cycle {2..4096} (both are cross-implementation wire-compat requirements — SPEC §5.1). *Must not know:* peers or sessions.
- **contracts/snagg** — SNAGG B-tree page/trailer byte layout: 6-byte magic `SNAGG\0`, page kinds, the fixed 162-byte trailer, MIN-KEY-of-subtree separators, and the deterministic build/fingerprint rules (SPEC §5.3). *Must not know:* the DHT or torrents.
- **contracts/ltepwire** — the `sn_search` bencoded envelope: `msg_type` discriminator 0–8, `txid`, and the 64-bit `services` bitfield (append-only; unknown bits ignored). *Must not know:* which peer a frame goes to (that's the transport's job).

### config

- **Responsibility:** the `Config` struct, XDG share-root resolution, `Validate()` side effects (create `DataDir` 0755 and `IndexDir`'s parent only), empty-path = feature-off semantics, and the **single unified unsafe-mode gate**. *Owns:* SPEC §2.1 config/paths.
- **Boundary:** must not import any subsystem; must not know about Bleve, DHT, or the wire.
- **Key types:** `Config`, `Default()`, `Validate()`, `ResolveShareRoot()`, `unsafeAuthorized(inTest bool)`.
- **DHT-isolation fields (load-bearing for an isolated regtest DHT — SPEC §5.8/§5.11):** `ListenHost` (default off; set `127.0.0.1` for a cluster — the BEP-44 write-token is SHA1'd over the query's **source IP**, and a `0.0.0.0` bind lets the kernel flap source IP per send → silent "invalid token" → value-not-found), `DisableIPv6` (default off; dual-stack spawns two DHT servers but the publisher drives only one, and v4-mapped-v6 addresses break put round-trips), and `DHTBootstrapAddrs` (an **empty list falls through to anacrolix's public routers** and poisons an isolated cluster, so a regtest cluster seeds a placeholder `127.0.0.1:1` instead). All three default off/empty — production is dual-stack with public bootstrap.
- **§6 fix:** one gate — `SWARTZNET_UNSAFE=1` (or `testing.Testing()` for testlab) — authorizes `Regtest`/`DHTInsecure` at both the CLI and config layers, replacing the undocumented two-name sequence that broke the project's own testbed.

### identity

- **Responsibility:** ed25519 keypair load-or-create; exact-0600 enforcement (0400 is rejected — SPEC §5.6); public-half re-derivation from seed on load; never implicit regeneration; auto-create only at the default XDG path. *Owns:* SPEC §2.8 identity persistence (invariant #2).
- **Boundary:** must not know about signing formats, the DHT, or the wire — it produces a `Signer`, nothing more.
- **Key types:** `Identity`, `Signer`, `Load(path, allowCreate bool) (Identity, isDefaultPath bool, error)`, `PublicKeyHex()`.

### capability — the single producer of the services mask

- **Responsibility:** THE source of truth for the 64-bit `sn_search` services mask (invariant #1 enforcement point). Splits **operator-settable** `Sharing` from **daemon-owned** `RuntimeFacts`, and derives the announced mask by one pure function. *Owns:* SPEC §2.5 capability negotiation, §2.9 `/capabilities`.
- **Boundary:** must not know about peers, HTTP, or the engine — it is a pure value + function.
- **Key types:** `ServiceBits`; `Sharing{ShareLocal uint8; FileHits, ContentHits bool}`; `RuntimeFacts{Publishing, Reconciliation, Regtest, CompanionPub, CompanionSub, SnippetHighlight bool}`; `Announced(Sharing, RuntimeFacts) ServiceBits`.
- **§6 fixes (three at once):** `Announced()` is the **only** producer of the mask, consumed identically by `swarmsearch`'s outbound `peer_announce` and by `httpapi`'s `ServicesReporter` — so (a) capability downgrades are always reflected on the wire, (b) `/aggregate` reports live bits not the static `0x2ED`, and (c) wire and readout provably cannot diverge. Because `Publisher` lives in `RuntimeFacts` and the HTTP/GUI setter can only reach `Sharing`, "Save sharing settings clobbers the Publisher bit" is **unrepresentable by type**. Unknown bits are ignored, never rejected; absence of `peer_announce` means services 0 and the peer is still answered (SPEC §5.1).

### signing

- **Responsibility:** infohash-preserving `.torrent` signing — top-level `snet.pubkey`/`snet.sig` over `contracts/sign`'s payload, decoding metainfo as `map[string]bencode.Bytes` so the info dict is byte-untouched. *Owns:* SPEC §2.3 signing model.
- **Boundary:** must not round-trip the info dict through a typed struct (would change the infohash); must not know about the engine.
- **Key types:** `Sign(raw, Signer)`, `Verify(raw) (pubkey, error)`, re-exports `ErrNotSigned`/`ErrBadSignature`.

### trust

- **Responsibility:** offline publisher allowlist (`trust.json`, atomic per-mutation write, lowercased pubkeys, sorted). Consulted by the auto-Bloom-confirm path **and** (new) by the flag path so trusted publishers are exempt from demotion. *Owns:* SPEC §2.8 trust list.
- **Boundary:** must not know about reputation math or the DHT.
- **Key types:** `Store`, `Add/Remove/List`, `IsTrusted(pubkey)`.
- **§6 fix:** `IsTrusted` is consulted before any `RecordFlagged`, honoring the documented exemption the legacy never checked.

### reputation

- **Responsibility:** Bayesian per-indexer tracker (neutral 0.5, prior weight 5, seed bonus `0.45·2^(−age/90d)`, threshold ≤0/NaN = always pass), the known-good **Bloom filter** with the frozen FNV-64a Kirsch-Mitzenmacher double-hash (format v1, golden-vectored — SPEC §5.6), and the `SourceTracker` LRU for flag/confirm attribution. *Owns:* SPEC §2.8 reputation/Bloom/attribution.
- **Boundary:** pure package — must not import any subsystem or the wire. The Bloom on-disk derivation is pinned by golden vectors so a rewrite cannot silently invalidate existing `known-good.bloom` files.
- **Key types:** `Tracker`, `Score(pk)`, `RecordReturned/Confirmed/Flagged`, `Bloom`, `SourceTracker`.

### indexer — Layer L

- **Responsibility:** Bleve scorch schema v3, ingestion pipeline, extractor registry (first-claim-wins by MIME; plaintext declines subtitle types), 2 KiB chunker (paragraph→line→hard split, ≤1.5× overrun, ≤1.25× single chunk), single-worker extraction with a 60 s watchdog, exact-match `TermQuery` for `signed_by`/`infohash`. Owns `indexer.SearchResponse` and `PickLookupToken` (delegating to `contracts/token.MostDistinctive`). *Owns:* SPEC §2.4 all of Layer L.
- **Boundary:** never touches the wire; never learns which peer or HTTP request triggered a query.
- **Key types:** `Index`, `SearchResponse`, `SearchHit`, `Pipeline`, `extractors.Registry`, `ReadSeekerAt` shim.
- **§6 fix:** a `Seeker→ReaderAt` shim wraps the anacrolix `torrent.Reader` so **ZIM extraction works against the live pipeline**, not only against test `bytes.Reader`/`os.File`. Schema-mismatch still rebuilds the whole dir; a dropped file-complete event is recovered by an hourly rescan of completed files (grafted from Vertical-Slices; preserves SPEC §5.5).
- **Preserve (golden-vector candidate — SPEC §5.5):** MIME resolution consults SwartzNet's `extTypes` override map **before** stdlib `mime.TypeByExtension`. Go's builtin table lacks `.txt`, so on Alpine/scratch the plaintext extractor silently claims nothing without the override; `.ts` MUST resolve to TypeScript, not MPEG-TS; any `;charset=` suffix on a stdlib result is stripped. A rebuild that trusts stdlib alone silently stops indexing plaintext.
- **Preserve (SPEC §5.5, §7-Q40):** raw `.html`/`.htm` files are indexed by the **plaintext** extractor with tags left **in** (`text/html` is in plaintext's accept list); the tag-stripping HTML walker is used **only** for XHTML inside EPUB/ZIM. There is deliberately **no standalone HTML extractor** — a rebuild that "improves" this silently changes the indexed content of every `.html` file.

### swarmsearch — Layer S

- **Responsibility:** the `sn_search` LTEP extension state machine — `contracts/ltepwire` envelope, query/result/reject with txid + asked-set anti-spoof, `peer_announce` (pk/endorsed gossip), RIBLT sync sessions (**multi-batch responder**), AddrMan `PeerBook`, misbehavior banman (local, never gossiped), per-peer token bucket, feeler. Consumes `capability.Announced` for its outbound announce and `contracts/record` for sync. Owns `swarmsearch.QueryResponse`. *Owns:* SPEC §2.5 all of Layer S.
- **Boundary:** never touches Bleve internals (answers inbound queries through an injected `LocalSearcher` interface); never opens a socket itself. It **declares** the `Transport`/`SendExtension` seam and the opaque `PeerToken` here, and hands frames to whatever satisfies that port (the engine, in production). **Frames are emitted only through a transport that requires a capability-verified `PeerToken`**, so "send to a non-advertising peer" is a type-level guarantee (grafted from Hexagonal, the panel's #1 graft into the backbone). Because the seam type is owned here and merely *implemented* by `engine`, the graft adds no import edge back into `engine` and no engine↔swarmsearch cycle.
- **Key types:** `Handler`, `QueryResponse`, `PeerBook`, `SyncSession`, the `Transport`/`SendExtension` send seam + opaque `PeerToken` (both **defined here**, satisfied by an engine adapter), `LocalSearcher`/`RecordSource`/`RecordSink`/`GossipObserver` (all defined here, satisfied by adapters).
- **§6 fixes:** the RIBLT responder streams multiple `sync_symbols` batches (not one ≤100-symbol batch); a valid-bencode/wrong-shape `query` frame is charged `ScoreBadBencode` like every other decode failure; `Hit.N` is truncated to ~60 bytes on the wire; torrent hits carry a **real** freshness timestamp (or omit it) instead of the year-1 zero time.

### dhtindex — Layer D, behind the swappable RecordBackend seam

- **Responsibility:** publish/lookup of keyword→infohash mappings, plus BEP-46 pointer and BEP-51 crawl primitives. All record-format specifics sit behind one `RecordBackend` port (see *The swappable Layer-D seam*). Owns `dhtindex.LookupResponse`. *Owns:* SPEC §2.6 all of Layer D.
- **Boundary:** never touches the HTTP API; never touches Bleve. Pins BEP-44 `Exp=2h`; a put reaching zero storing nodes is a **failure** via one shared `checkPutStats` guard across all put paths (SPEC §5.2). All remote value decoders reject >1000 bytes before unmarshalling.
- **Key types:** `Publisher`, `Lookup`, `LookupResponse`, `RecordBackend` (interface), plus `legacyKeyword`/`aggregatePPMI`/`composite` implementations.
- **§6 fixes:** lookup queries `indexer.PickLookupToken` (the most distinctive token), not `tokens[0]`; publishes **only** `Tokenize(torrent.Name)` keywords, never content tokens.

### companion

- **Responsibility:** companion content-index publish/subscribe (BEP-46 pointer pattern): gzip-JSON snapshot + `companion.torrent` + pointer put; subscriber resolves pointer, fetches (fail-closed: 1 file, ≤32 MiB, safe names), and imports into Bleve. *Owns:* SPEC §2.7 companion cycle.
- **Boundary:** takes narrow `PointerPutter`/`Getter` and `CorpusExport`/`Import` interfaces (no `dhtindex`/`indexer` concrete import beyond those seams).
- **Key types:** `Publisher`, `Subscriber`, `CompanionIndex`, `PointerPutter`/`Getter`.
- **§6 fixes:** the subscriber **verifies `CompanionIndex.Publisher == followed pubkey`**, stamps `TorrentDoc.SignedBy = followed pubkey`, and **dedups on `GeneratedAt`** so unchanged snapshots don't re-ingest hourly. `lastRefresh` advances only on success (empty index = failure — SPEC §5.3).

### engine — anacrolix wrapper and integration hub

- **Responsibility:** the single owner of the anacrolix `Client` + DHT server, integrated **only** through extension APIs (callbacks, `LocalLtepProtocolMap.AddUserProtocol`, direct DHT access — MPL discipline, SPEC §5.12). Owns the download queue, session persistence/restore, `fileTracker` fan-out (64-slot replay), per-torrent snapshots, rate limits, piece callbacks. Wires L ingestion, the S transport, the D publisher; recomputes `capability.RuntimeFacts` from live state on every announce. *Owns:* SPEC §2.2 engine, §2.3 engine-side create, and the anacrolix quirk-taming of §5.4.
- **Boundary:** must never patch the vendored anacrolix library; must not embed HTTP/GUI presentation. The many §5.4 quirks (positive rate-limiter burst even when unlimited, `PeerStore` for BEP-5 write tokens, `Exp=2h` pin, async off-read-loop sn_search dispatch with dual 256-slot semaphores + payload copy, per-file-priority activation, seed-in-place `FilePathMaker` on the real basename, background `VerifyData` rehash, magnet→metainfo upgrade guard) are honored **inside** this package.
- **Key types:** `Engine`, `Handle`, `TorrentSnapshot`, `queue`, `session`, `fileTracker`, and the concrete **implementation of `swarmsearch.Transport`** (the seam type itself is declared in `swarmsearch`, not here).
- **The transport token (graft):** the seam is `swarmsearch.Transport.SendExtension(peer swarmsearch.PeerToken, frame []byte)` — **defined in `swarmsearch`**, implemented and injected by `engine`. The engine mints a `PeerToken` **only** in its `PeerConnAdded`/extended-handshake path, and **only** when the remote advertised `sn_search` in its LTEP `m` dict — the gate is that `m`-dict advertisement, **not** `peer_announce`, so a token-holding peer that never announced is still answered (SPEC §5.1: absence of `peer_announce` = services 0 but the peer is still served). There is no API to send an extension frame to a bare address. Because the type lives in `swarmsearch` and the engine only *implements* it, this graft creates no engine↔swarmsearch cycle. It turns SPEC §5.1's load-bearing rule ("never send an `sn_search` frame to a peer that did not advertise it") into a compile-time property and lets the whole wire-compat matrix run against an in-memory peer with no sockets.
- **§6 fixes:** queue promotion on completion is **decoupled from the Bloom gate** (a nil Bloom no longer strands a completed torrent's slot); `countActiveDownloads` inspects file priorities so an all-`none` torrent frees its slot; `capability.RuntimeFacts` is recomputed live so downgrades reach the wire.

### searchmux — the concurrent reconciliation seam

- **Responsibility:** fan L/S/D out under an `errgroup` and return a tuple of the three **native** response types (no merged hit type). Shared by the daemon's httpapi adapter **and** the GUI so fan-out exists once. *Owns:* the reconciliation half of SPEC §2.9's search seam.
- **Boundary:** knows nothing of Bleve/DHT internals — only the three searcher interfaces.
- **Key types:** `Mux`, `Result{*indexer.SearchResponse, *swarmsearch.QueryResponse, *dhtindex.LookupResponse}`, `LocalSearcher`/`SwarmSearcher`/`DHTSearcher`.
- **§6 fix:** `/search` runs the three layers **concurrently**; worst-case latency drops from summed to `max(layer)`, and web and GUI cannot drift in reconciliation. Layer-failure asymmetry is preserved as contract: Layer-L error → 500 for the whole request; Layer-S/D errors → inline error strings under HTTP 200 (SPEC §5.9).

### admission — deny-by-default bootstrap policy

- **Responsibility:** typed, fail-closed publisher-admission rules for the Aggregate cold-start bootstrap. Named `AnchorRule`/`EndorsementRule`/`CrawlRule`, each deny-by-default, composed by an `AdmissionEngine`; anchor cap-exemption and `MarkSeeded` preserved. *Owns:* SPEC §2.1/§5.7 bootstrap admission.
- **Boundary:** consumes `reputation` only; knows nothing of HTTP.
- **Key types:** `AdmissionEngine`, `Policy`, `Rule`, `DefaultPolicy()` (= deny), `State{admitted, pending, anchors}`.
- **§6 fix:** inverts the legacy defect where `bloomPolicy` admitted **any** unknown 0.5-scored pubkey and 3 fresh Sybils cleared the endorsement bar for free. The permissive policy is a separate constructor reachable only from tests. Admission counts are surfaced via `/aggregate` so a starved node is distinguishable from a quiet network.

### httpapi — loopback-only unauth control plane + embedded web UI

- **Responsibility:** the localhost HTTP control plane (`localhost:7654`), the IPC surface for daemon-dependent CLI subcommands, and the `go:embed` web UI (Search/Add/Downloads/Companion/Status/Sharing). *Owns:* SPEC §2.9 all endpoints, invariant #3.
- **Boundary (architectural law, SPEC §5.8):** **zero subsystem imports.** Every collaborator is a narrow interface declared here; every DTO is re-declared and JSON-tagged locally; a nil collaborator returns 503. No auth concept exists. CSRF/DNS-rebind guard (Origin-then-Referer, literal `localhost` trusted, other DNS names rejected) wraps a 1 MiB body cap wraps the mux; the services readout comes from an injected `ServicesReporter` bound to `capability.Announced`.
- **Key types:** `Server`, the searcher/control/companion/status interfaces, DTOs, `ServicesReporter`.
- **§6 fixes:** `/config/rate-limit` and `/config/queue` get **merge (PATCH) semantics** so a partial update can't zero the other field; `/capabilities` mutates only `Sharing`; `/aggregate` reports live services; the sequential-search defect is gone because search delegates to `searchmux`.
- **Preserve (security-load-bearing — SPEC §5.9):** the embedded web UI injects Bleve highlighter fragments and highlighted names via `innerHTML`, on the explicit assumption that **Bleve pre-escapes the text and only adds `<mark>` tags**. A rebuild of *either* the highlighter or the web UI that drops that escaping guarantee turns this into stored/reflected XSS — so the escaping contract must hold on both sides, or the injection must switch to text nodes.

### daemon — the single wiring point

- **Responsibility:** invariant #4. Fixed startup order engine→indexer→companion pub→companion sub→bootstrap→session restore→httpapi; degraded start (only engine + indexer failures abort); reverse-order `Close` that cancels+joins background goroutines **before** any subsystem teardown. Owns the adapters translating engine/companion/layer types into httpapi DTOs, the follows file, bootstrap wiring, `Options.PublisherPubKey`, and the **one shared `Confirm`/`Flag` implementation** both httpapi and gui call. *Owns:* SPEC §2.1 daemon lifecycle.
- **Boundary:** holds only wiring + adapters + the two shared control operations; all logic stays in the leaf packages it delegates to (guards against a fat daemon).
- **Key types:** `Daemon`, `New(ctx, Options)`, `Close()`, adapters, `Confirm/Flag`.
- **§6 fixes:** reputation feedback is consistent across surfaces (one `Confirm`/`Flag` path that records attributed sources and consults `trust.IsTrusted` before demotion); `Options.PublisherPubKey` is set from `identity.PublicKeyHex` so `/status` renders the publisher pubkey.

### gui — native Fyne frontend

- **Responsibility:** Downloads/Search/Status/Companion/Settings over the same `Daemon`, using `searchmux` for concurrent search and the daemon's shared `Confirm/Flag`. *Owns:* SPEC §2.10.
- **Boundary:** presentation only; no independent lifecycle or reconciliation logic. License/version strings come from one build-stamped source.
- **Key types:** `App`, the five tabs.
- **§6 fix:** the About dialog states **Apache-2.0** for first-party code (the legacy "MPL 2.0 (engine) + MIT" string was wrong).

### cmd/swartznet `aggregate` — offline signed-index tooling (no daemon, no DHT)

- **Responsibility:** the fully-offline `aggregate build|inspect|find` subcommands (SPEC §2.1 dispatch, §2.7, §5.10). `build` reads JSONL `{kw,ih,t}` records, signs each with the publisher key, optionally mines hashcash PoW, packs them into a signed SNAGG B-tree file (mode **0644**), and prints records/pages/bytes/fingerprint; `inspect` prints trailer metadata and **fails on bad magic/version/trailer-signature**; `find --verify` runs a keyword-prefix query and **re-derives the tree fingerprint**. A thin offline builder composed over `contracts/snagg` + `contracts/sign` + `identity` (**load-only** — `--key`/default path obeys the same auto-create-only-at-default rule as `create --sign`). *Owns:* the offline half of SPEC §2.7's aggregate surface.
- **Boundary:** never constructs a `daemon`, never opens the DHT or the HTTP API, never touches Bleve — pure file I/O over the frozen `contracts/*` codecs. It shares the SNAGG byte layout and determinism/fingerprint rules with `dhtindex`'s `aggregatePPMI` backend but takes no dependency on it.
- **Key types:** cmd-level `buildAggregate`/`inspectAggregate`/`findAggregate` over `contracts/snagg`'s builder+reader and `contracts/sign`.
- **§5/§6 rules it must preserve:** `--pow-bits > 40` is **refused (exit 2)** (cost-prohibitive mining guard); `--piece-size` defaults to 16384 and **MUST match the wrapping torrent's metainfo piece length** or B-tree paging is misread; PoW is mine-then-sign (the nonce is inside the signed payload); `build` output is byte-deterministic (identical records → identical file/infohash/fingerprint).

### wirecompat — the CI-gated conformance module (evolves testlab)

- **Responsibility:** `MiniPeer` + an in-memory `VanillaPeer` double (grafted from Hexagonal) + golden wire-vector suites, promoted into the CI merge gate. Asserts: a non-advertising peer receives **zero** `sn_search` frames; a vanilla anacrolix v1.61.0 client fetches metadata via BEP-9 and completes a download from us; BEP-44 items are byte-indistinguishable; signed/unsigned `.torrent` twins share one infohash; every `contracts/*` codec matches its frozen vectors; a scoped query to a non-sharing responder yields reject code 2. *Owns:* the SPEC §2.11/§2.12 wire-compat matrix, turning invariant #1 into a gate.
- **Boundary:** the deterministic golden-vector and single-`VanillaPeer` silence assertions run in CI `-race`; the timing-sensitive multi-client piece-transfer scenarios stay local-only (SPEC §5.11), so promoting the gate does not reintroduce flakiness.

---

## Data flow

### Download → extract → index

1. An `add` (magnet / `.torrent` / bare 40-hex infohash / in-memory metainfo) enters through `AddService`. The engine parses the input locally (invalid magnets fail fast; attacker-controlled infohash adds are wrapped in `recover`), persists a `SessionEntry`, and kicks a background `VerifyData` rehash so a fresh seed or restore shows a real percentage without waiting for a peer (SPEC §5.4). A `.torrent`'s `snet` signature is verified at add time: valid → `SignedBy` on the handle; **bad → the torrent still adds, `SignedBy` empty, `ErrBadSignature` logged** (add-anyway is the intended model — see Assumptions); a trust-listed signer additionally auto-confirms the infohash into the Bloom immediately.
2. On `GotInfo` (≤5 min wait), the engine flips each file to Normal priority **per file** (not `DownloadAll`, so both anacrolix priority surfaces update), respecting paused state and the slot cap. It writes a `TorrentDoc` to `indexer` (Layer L) unless the per-torrent toggle is off, and hands `Tokenize(name)` keywords to the `dhtindex` publisher (Layer D) — unless `--no-index`/`--no-dht-publish` suppress it — and mints one signed `contracts/record` per keyword into the swarmsearch `RecordCache`. **Per-torrent indexing-off still publishes existence to Layer D** (SPEC §5.4); only `--no-index`/`DisableDHTPublish` suppress network-visible publication.
3. Each file-complete event fans out through the 64-slot `fileTracker` (with a done-replay buffer for late subscribers) into the `indexer.Pipeline`. An extractor is chosen first-claim-wins by MIME (ZIM now reachable via the `ReadSeekerAt` shim), run under a 60 s watchdog + panic-recover, and its output chunked at 2 KiB into `ContentDoc`s. A dropped event (channel full) is recovered by the **hourly rescan of completed files**.
4. On full completion the infohash is auto-added to the known-good Bloom (background, 24 h cap), and — **decoupled from the Bloom gate** — the queue promotes the next torrent.

### Search across all three layers, reconciled at the seam

A search enters through one of three doors — CLI direct-Bleve local mode, `httpapi POST /search`, or GUI in-process — but every concurrent path runs through the **same `searchmux.Mux`**. Under an `errgroup` it launches Layer L (Bleve), Layer S (fan-out to advertising peers, each holding a capability token), and Layer D (BEP-44 gets across reputation-filtered indexers) **simultaneously**, each on its own timeout budget (swarm 2 s, DHT 5 s, clamped ≤30 s; +500 ms grace).

The layers reconcile **only at the edge**. `searchmux` returns a `Result` tuple of the three native response types with no merged hit type. For the HTTP boundary, the daemon adapter translates each native type field-by-field into httpapi's own DTOs (`LocalHit`/`SwarmHit`/`DHTHit` stay structurally distinct), so httpapi imports no subsystem. Layer-failure asymmetry is a contract: Local error → 500; Swarm/DHT errors → inline strings under 200. The GUI consumes the same `Result` and renders per-layer cards.

**L1 in-swarm sharing** is implemented as a `LocalIndex` query filtered to the engine-owned active-swarm set (grafted from Hexagonal), so ShareLocal==1 actually shares in-swarm results instead of failing closed — fixing the SPEC §6 "L1 shares nothing" defect while keeping ShareLocal==0 a true opt-out. **The swarm-filter must itself stay fail-closed (SPEC §5.1):** when ShareLocal==1 and a query targets a torrent **not** in the active swarm, the filtered searcher returns **nothing** — it must never fall back to the full index. Otherwise fixing "L1 shares nothing" would reintroduce the over-serve leak the old fail-closed reject path protected against.

### Publish → lookup (Layer D)

Publishing puts one BEP-44 mutable item per name-keyword at `SHA1(pubkey‖keyword)`, `Exp=2h`, re-published hourly, throttled 55 min/keyword, with a persistent manifest (oldest-hit eviction to stay under 1000 bytes). A put reaching zero storing nodes is a failure. Lookup fans BEP-44 gets for the **most distinctive** token across reputation-filtered known indexers (self + gossip-learned + admitted anchors), verifies each signature, merges by infohash (mean source reputation + multi-source bonus + Bloom boost), and writes per-hit attribution to the `SourceTracker` so a later `/flag` demotes only the responsible indexers.

### Event propagation

Cross-subsystem signals ride the engine's typed per-torrent event streams — piece-state (buffer 64, drop-on-full) and file-completion (exactly one event per file, fanned out with a 64-slot replay buffer). The rebuild keeps this in-engine fan-out (it is proven and SPEC-pinned) rather than introducing a global event bus, because the Vertical-Slices judges flagged an event bus as a **new load-bearing failure mode** (a dropped terminal event silently un-indexes) that duplicates the very mechanism it replaces. The hourly rescan is the recovery guarantee for the one drop that matters.

---

## The swappable Layer-D seam

The SPEC §0 endgame ("Aggregate") is the intended future of Layer D but is **not yet load-bearing**: legacy per-keyword BEP-44 is the shipping baseline that must keep working, and the ≥12-month legacy-read back-compat contract (SPEC §5.2) is binding. This architecture makes the format a **config/adapter choice, not a re-architecture**, by combining the backbone's `RecordBackend` seam with the Hexagonal proposal's composite adapter:

```
        dhtindex.Publisher / Lookup
                    │
                    ▼
          ┌──────────────────┐        one port: publish(keywords,hits) / lookup(token)→hits
          │   RecordBackend   │◀── selected by one line in daemon.New (config.LayerDMode)
          └──────────────────┘
             ▲        ▲       ▲
   ┌─────────┘        │       └──────────────┐
   │                  │                       │
legacyKeyword     composite            aggregatePPMI
(BEP-44 per-      (dual-WRITE +        (PPMI item at SHA256("snet.index")
 keyword; the      legacy-then-         + signed SNAGG B-tree companion
 SHIPPING          aggregate            torrent bound by `commit`; RIBLT
 default)          dual-READ)           sync; hashcash PoW)  — OFF by default
```

- **`RecordCodec` stays a separate concern** (`contracts/record`, `contracts/dhtschema`, `contracts/snagg`) so record bytes are codec-owned and the port stays at `publish/lookup` granularity — avoiding a lowest-common-denominator interface between a CAS-read-modify-write model and a B-tree/RIBLT model (a risk the judges flagged for both migration proposals).
- **`composite`** implements dual-write + legacy-then-aggregate dual-read behind the same port, so the §7-C migration (dual-write → PPMI-only → legacy-reader retirement) is an adapter selection with **zero** app/domain change.
- **Discipline:** define the seam against the **legacy** path first; add `aggregatePPMI` only once legacy passes the full lookup test suite (the panel's explicit sequencing note). Ship with `LayerDMode = legacy`.

The Aggregate distribution seam with Layer S is preserved: the engine mints per-keyword records into the swarmsearch `RecordCache`, and the RIBLT responder is **multi-batch** so differences >100 symbols converge (fixing the SPEC §6 one-batch limit). An outbound sync initiator (`StartSync`) is wired behind the aggregate backend, not left test-only.

---

## Wire-compatibility strategy

Invariant #1 gets three overlapping structural guards plus a gate:

1. **All vanilla-observable bytes come from leaf codecs.** No wire bytes are assembled ad hoc in `engine` or `daemon`; every codec lives in `contracts/*` (or `signing`) with frozen golden byte-vectors. A byte-level drift is a golden-test failure in exactly the package that owns the format.
2. **One services-mask producer.** `capability.Announced()` is the sole function emitting the mask, so the advertised set is provably a pure function of (operator prefs, build facts) and can never claim a feature the build lacks or leak a downgrade the operator made.
3. **The transport token.** `sn_search` frames can only be sent through `swarmsearch.Transport.SendExtension` (the seam type is declared in `swarmsearch` and implemented by `engine`), which requires a `swarmsearch.PeerToken` minted from a recorded remote `m`-dict advertisement — not from `peer_announce`. Sending to a non-advertising peer is uncompilable — the strongest available form of SPEC §5.1's load-bearing rule.

Because Layer D rides only standard BEP-44/46/51 (items indistinguishable from anyone's, `Exp=2h` pinned, node obeys BEP-42/43), and Layer S rides the BEP-10 `m` dict, **there is no new reserved bit, DHT verb, or UDP port anywhere in the tree.**

The `wirecompat` gate makes this testable and enforced: an in-memory `VanillaPeer` (empty LTEP `m`-dict impersonation) runs the four-direction matrix in-process with no netem — asserting vanilla silence, tolerance of unknown LTEP keys and future service bits, and reject-code-2 on scope mismatch — while the real anacrolix v1.61.0 interop scenario stays as a belt-and-suspenders local/non-unit gate. Deterministic assertions run in CI `-race`; timing-dependent multi-client transfers stay local-only, so promoting the gate does not reintroduce the flakiness that got testlab excluded (SPEC §5.11).

---

## Cross-cutting concerns

### Identity, signing, trust
One `identity` package owns the keypair (exact-0600, seed→pubkey verification, default-path-only auto-create, `IsDefaultPath` flag). Every publisher-shaped consumer (Layer-D publishing, `.torrent` signing, `peer_announce` pk, Aggregate record minting) receives the same `Signer`. Signing is infohash-preserving (top-level `snet.*`, info dict byte-untouched); verification taxonomy (`ErrNotSigned` benign vs `ErrBadSignature` firm tamper) is surfaced to UIs, and unsigned torrents are never penalized in default rankings.

### Trust and reputation
Local and social only — no global consensus. Bayesian reputation (90-day seed-bonus half-life), known-good Bloom (frozen FNV-64a format), `SourceTracker` attribution. Confirm/flag flow through **one** daemon path: confirm adds to the Bloom and records attributed sources on **all** surfaces; flag demotes **only** attributed indexers, **exempts trusted publishers**, forgets attribution after (no double-dock), and fails closed (demotes nobody) when unattributed.

### Config and paths
Single XDG share-root; empty path = feature off, consistently. One unified `SWARTZNET_UNSAFE=1` gate (plus `testing.Testing()`) authorizes regtest/insecure at both layers. `Options.NoIndex` mirrors into `Config.NoIndex` before engine construction so `--no-index` cascades (Bleve never opens, Layer-D publisher disabled, Publisher bit zeroed).

### Logging
Structured `slog` text to stderr; `SWARTZNET_LOG` (`debug|info|warn|error`, default info) documented (fixing the SPEC §6 "undocumented" note). Regtest logs a loud warning; admission surfaces `admit_capped` so a starved node is distinguishable from a quiet one.

### Persistence and crash-safety — periodic checkpointing (new)
All state files are atomic (`.tmp`+rename). `trust.json` saves per-mutation; the session manifest saves per queue/state change. **The legacy persists the Bloom filter and reputation tracker only at clean engine `Close`, so a crash loses the session's confirmations/flags/auto-confirms (SPEC §6, §7-D Q23).** The rebuild adds a **bounded periodic checkpoint**: the engine flushes Bloom + reputation on a coarse interval (e.g. every 5 min, and on each auto-confirm/flag batch) in addition to `Close`, so at most one interval of spam-resistance signal is lost on crash. Checkpoints are atomic and skipped when the path is empty (in-memory mode stays a no-op). This is deterministic code owning state transitions (per the production-architecture rule): no LLM, no ambiguous limbo — each flush either succeeds or is retried on the next tick.

### Shutdown ordering
`Close` is the strict reverse of startup: **cancel the daemon-owned background context and join background goroutines first** (so no anchor fetch or checkpoint touches the engine mid-teardown), then stop httpapi (3 s timeout) → companion subscriber → companion publisher → indexer → engine. Engine close itself: cancel bgCtx → stop extraction pipeline (before storage) → stop DHT publisher (before the DHT server) → final Bloom/reputation save → close subscriptions → `client.Close`. Repeated close returns the cached error.

---

## How this fixes the §6 defect classes

Each defect **class** maps to a structural change that makes it unrepresentable or impossible-by-construction, not a spot patch:

| §6 defect class | Structural fix |
|---|---|
| Bootstrap admission inverts default-deny (any unknown 0.5 pubkey admitted; 3 Sybils clear endorsement) | `admission` package: typed rules, `DefaultPolicy()`=deny, permissive policy test-only |
| Capability downgrades never reach the wire; `/aggregate` reports static `0x2ED` | `capability.Announced()` is the **single** mask producer, consumed by wire + `ServicesReporter` |
| "Save sharing" clobbers the Publisher bit | `Sharing` (operator) vs `RuntimeFacts` (daemon) are distinct types; the setter can't reach `Publisher` |
| L1 in-swarm sharing shares nothing | in-swarm searcher = `LocalIndex` filtered to the active-swarm set |
| Companion ingests without attribution/verification; no `GeneratedAt` dedup | subscriber verifies `Publisher==followed`, stamps `SignedBy`, dedups on `GeneratedAt` |
| Download-slot accounting: nil-Bloom skips promotion; all-`none` torrent holds a slot | promotion decoupled from Bloom gate; `countActiveDownloads` inspects file priorities |
| ZIM never works in the live pipeline | `ReadSeekerAt` shim adapts `torrent.Reader` |
| Layer-D queries the first token | `contracts/token.MostDistinctive` is the only lookup-token chooser |
| Torrent hits carry year-1 garbage timestamp | wire path populates real `AddedAt`/seeders or omits |
| Reputation feedback inconsistent across surfaces; trusted publishers demoted | one daemon `Confirm/Flag` path; consults `trust.IsTrusted` before demotion |
| `/search` strictly sequential | `searchmux` fan-out under `errgroup` |
| `/config/rate-limit` can't partial-update | PATCH/merge semantics in httpapi |
| Two undocumented env gates break the testbed | one `SWARTZNET_UNSAFE` gate, documented |
| Valid-bencode wrong-shape `query` frame is free | charge `ScoreBadBencode` like every other decode failure |
| RIBLT sync responder is one-batch; no production initiator | multi-batch responder + wired initiator behind the aggregate backend |
| `/status` publisher.pubkey never set | `daemon.New` sets `Options.PublisherPubKey` from identity |
| Index deletion unreachable (removed torrents searchable forever) | wire `DeleteTorrent`/`DeleteContentForTorrent` to a reachable "Forget" action (files kept by default, per the documented remove contract) |
| Crash loses Bloom/reputation | periodic checkpoint (above) |
| GUI About shows wrong license; dht-smoke PASS on all-fail; testbed s12 lost timeout | single build-stamped Apache-2.0 string; deterministic exit-code contracts and bounded wait-loops in ops tooling |

Doc-drift (SPEC §6.3): the rebuild follows the **code** behavior (reject code 2 on scope mismatch; per-peer token bucket; no DHT sharding; `int(bleveScore*1000)` rank) and updates docs 05/06/07/11 to match — those specs are normative and must track any wire change (SPEC §5.12).

---

## Stack & dependencies

- **Go 1.24.1** (pinned in `go.mod`; build with `/usr/local/go/bin/go`). *Tradeoff:* CI's floating `1.24` vs the pinned `1.24.1` must stay reconciled (SPEC §5.11).
- **anacrolix/torrent v1.61.0 + anacrolix/dht/v2 v2.23.0**, integrated **only** through extension APIs (Callbacks, `LocalLtepProtocolMap.AddUserProtocol`, DHT server config, `TorrentSpec` storage override). *Tradeoff:* MPL-2.0 file-level copyleft is contained by **never patching** the library (SPEC §5.12) — the integration surface is `engine` alone, which must reproduce upstream traps as first-party pins (`Exp=2h`, positive burst floor, `PeerStore` for write tokens, async off-read-loop sn_search dispatch, seed-in-place `FilePathMaker`, background `VerifyData`). `anacrolix/bencode` is used as a normal library dependency for the raw-bytes-preserving codec.
- **Bleve v2 (scorch)** for Layer L (BM25, QueryString, HTML `<mark>` highlighter, `SetInternal` schema sentinel). *Tradeoff:* `Store=true` on the text field costs 3–5× disk for stored snippets; `Stats()` scans every content doc, so `/index/stats` stays human-cadence. Behind the `indexer` package so a future tantivy/FTS5 migration is contained.
- **Homegrown minimal RIBLT** (`contracts/riblt`) rather than a third-party lib. *Tradeoff:* we carry the correctness burden of the nonlinear FNV-1a-64 key and 12-step cycle — mitigated by golden vectors in `wirecompat`, and justified because the wire format is a cross-implementation contract that must be byte-frozen and spec'd in doc 06.
- **Go stdlib `net/http` ServeMux** (1.22+ method+wildcard patterns), no third-party router; `go:embed` web UI. *Tradeoff:* manual 40-hex path validation and explicit middleware nesting (bodylimit ⊃ CSRF ⊃ mux) — but that explicitness *is* the DNS-rebind defense.
- **Fyne v2 (CGo/OpenGL)** for the native GUI, built host-only; the CLI stays `CGO_ENABLED=0` and cross-compiles. *Tradeoff:* the GUI can't cross-compile and inflates `THIRD_PARTY_LICENSES` (Fyne/systray/go-gl); the merge-gate CI must still install Fyne C deps to `go vet` the module (SPEC §5.11).
- **Frozen stdlib crypto as format anchors:** ed25519 (identity/signing/BEP-44/records), SHA-1 (DHT targets/infohash), SHA-256 (PPMI salt, tree fingerprint, hashcash, RIBLT element ID), FNV-64a (Bloom). *Tradeoff:* no primitive agility without a versioned format bump — accepted as the price of interop; `contracts/*` centralizes the derivations so they cannot drift.
- **License:** first-party code is **Apache-2.0** (repo-level `LICENSE`, "The SwartzNet Authors", 2026, no per-file SPDX by policy). `THIRD_PARTY_LICENSES` is the authoritative attribution (ledongthuc/pdf is BSD-3-Clause, not MIT). The standing prohibition on GPL/LGPL/AGPL/SSPL/BUSL/CDDL/EPL extractor dependencies is a rule for every future format, not a one-time check (SPEC §5.12).

---

## Assumptions & deferred questions

Answered defaults (SPEC §7), each chosen for defensibility and noted as an assumption:

- **A5 / §7-A5 (services always OR `DefaultServices`)** — treated as a **bug**. `Announced()` is a pure function of live caps; downgrades are reflected. The doc-06 masking wording (self-contradictory) is resolved as: ignore bits ≥ your highest-understood bit, never reject.
- **H46 (two env gates)** — unify to **`SWARTZNET_UNSAFE=1`** (survives) + `testing.Testing()` for testlab. Documented.
- **B8/B9 (anchors, `bloomPolicy`)** — `admission` deny-by-default; ship an empty `DefaultAnchorPubkeys` and treat a **curated `seeds.json` as an explicit release prerequisite**, not code. Cold-start is correct-but-inert without a trust root; `/aggregate` surfaces admission counts so this is observable.
- **C12–C17 (migration)** — legacy is the shipping default; Aggregate lives behind the `RecordBackend`/`composite` seam; `MostDistinctive` token now; the PPMI-retirement schedule and salt convergence stay a release/operator decision.
- **D20 (bad-signature add)** — keep **add-anyway** (empty `SignedBy`, `ErrBadSignature` logged); a bad signature is not the same as unsigned, but blocking the add would fork behavior from vanilla and punish a benign re-share. UIs distinguish the two.
- **D21 (trusted exemption)** — **yes**, trusted publishers are exempt from flag demotion.
- **D22 (auto-`RecordConfirmed` on completion)** — deliberately **not** auto-boosted, to avoid self-reinforcing reputation; confirm records attributed sources consistently across all surfaces.
- **D23 (crash-safety)** — periodic checkpoint added (above).
- **I51 (add-as-daemon; bare infohash)** — `add`-as-daemon stays the design (clean Ctrl-C exits 130); a bare 40-hex argument is accepted as an infohash add (small ergonomic improvement over "anything non-magnet is a file path").
- **I53/I54 (`Options.PublisherPubKey`, `/capabilities` semantics)** — wired; `/capabilities` mutates only `Sharing` with preserve-unset (PATCH) semantics.
- **J60 (license string)** — Apache-2.0.
- **§2.9/§5.9 (non-loopback API bind)** — the security model is **loopback-default + CSRF/DNS-rebind guard**, *not* a hard refusal: the server still **binds a non-loopback address if asked**, emitting a loud **one-time "API is UNAUTHENTICATED" warning** (operators may front their own auth). "Loopback-only" throughout this doc means *loopback by default*, not *refuses otherwise*; invariant #3's guarantee is the absence of any auth/remote-session concept in `httpapi`, not a bind restriction. This matches the spec (warn-and-bind); if a future release deliberately tightens this to a hard refusal, record it explicitly as a narrowing of invariant #3.
- **§7-Q37 (shared local/remote doc-ID namespace)** — local and companion-imported docs share one doc-ID namespace (`t:x` / `c:x:i:c`), last-write-wins. Chosen default: **the followed-publisher `SignedBy` attribution is sticky** — content/Trackers follow last-write-wins, but a later *unsigned* local add of the same infohash (magnet adds get `SignedBy=""`) must **not blank** an existing non-empty `SignedBy`. This keeps the §6 companion-attribution fix (subscriber stamps `SignedBy = followed pubkey`) from being clobbered by a subsequent local add, while a local add that carries its own signature still wins.

Deliberately left open (require author/operator or a second implementation): the exact BEP normativity of scope-reject vs downgrade (A1) and whether sync gets its own reject code (A2) — the rebuild adopts the code's reject-code-2 for wire-compat and flags the spec for a future codepoint; `next_pk` key rotation (D/§7-D19) stays reserved and unused; the BEP-9-cannot-carry-`snet.*` crawl-verification problem (C18) — deferred because Channel-B crawl is not load-bearing while Aggregate is off; PeerBook per-connection-lifetime persistence (G43) is kept as intentional v1 scope (SPEC §5.1).

---

## Open risks

1. **Deny-by-default admission ships inert.** With an empty anchor/seed list, cold-start discovery finds nobody until a curated `seeds.json` ships. The design is correct but starved without operator keys — treat shipping a trust root as a hard release gate, and keep `/aggregate` admission counts visible so a starved node is distinguishable from a quiet network.
2. **The `RecordBackend`/`composite` seam could be drawn wrong.** If the port leaks PPMI/B-tree assumptions into the legacy path, the §7-C migration re-architects anyway. Mitigation: define and validate the seam against the legacy path first; add `aggregatePPMI` only after legacy passes the full lookup suite; keep format specifics behind `contracts/*` codecs.
3. **`engine` remains a fat integration hub** importing ~11 packages and taming every anacrolix quirk. `RuntimeFacts` must be recomputed at exactly the live-state moments (recomputed on every announce from engine getters, not cached) or advertising silently drifts. This is the single largest package and the highest-churn artifact.
4. **The transport token must be truly the only send path.** The import-cycle half of this risk is now **resolved structurally**: the `Transport`/`SendExtension` seam and the opaque `PeerToken` are declared in `swarmsearch` and merely implemented by `engine`, so the graft is provably one-directional and cannot force `swarmsearch` to import `engine`. The residual risk is behavioral, not architectural: if any code assembles and writes an extension frame outside `swarmsearch.Transport.SendExtension`, the type-level guarantee degrades to convention. Enforce with a review rule and a `wirecompat` silence assertion.
5. **`contracts/*` couples multiple layers to one byte format.** A format change now touches every importer together — intended (they MUST agree), but it raises change cost. Golden vectors make accidental divergence a test failure, which is the point; deliberate changes require a coordinated versioned bump.
6. **Fixing §6 changes behavior the legacy testbed/tests pinned.** Several legacy scenarios assert the *buggy* behavior (permissive admission, static services, sequential search, unattributed ingest). The rebuild must **re-derive** expected values, not port legacy assertions — a real, one-time cost.
7. **Crash-safety is bounded, not perfect.** Periodic checkpointing narrows the loss window but a crash between checkpoints still loses that interval's spam-resistance signal. This is an accepted trade (deterministic, bounded) versus per-mutation Bloom/reputation writes, which would add I/O on every hit.
8. **Aggregate is genuinely new work with unresolved §7-C intent** (PoW ramp, salt convergence, initiator wiring). Schedule it last, keep it non-load-bearing behind the seam, and do not let GA depend on unproven RIBLT convergence between real nodes.
