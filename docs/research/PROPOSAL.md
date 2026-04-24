# PROPOSAL — "Aggregate": unified set-reconciliation redesign of SwartzNet's distributed index

| Field | Value |
|---|---|
| Date | 2026-04-24 |
| Status | Draft proposal, not yet accepted |
| Supersedes in part | §4.3 (Layer D), §5 (sn_search v1) of `docs/05-integration-design.md` |
| Amends | `docs/07-bep-dht-keyword-index-draft.md`, `docs/06-bep-sn_search-draft.md` |
| Companion research | `docs/research/{A,B,C,D}-*.md` |

## 0. TL;DR

Four independent research tracks (content-routing, anonymity, DHT/Sybil,
gossip/set-sync) each nominated a different "flagship" upgrade for
SwartzNet. Laid side-by-side, they converge on the **same structural
redesign** of the distributed layer:

> **Stop treating BEP-44 mutable items as per-keyword storage. Treat
> them as per-publisher pointers. Put the real keyword index inside a
> regular BitTorrent companion torrent, and reconcile updates between
> peers using rateless set-reconciliation over `sn_search`.**

This inverts Layer D's cost structure from `O(publishers × keywords)`
DHT items to `O(publishers)`; escapes the 1000-byte BEP-44 cap
permanently; drops lookup latency from 1–2 seconds (cold DHT) to
<100 ms (local cache); compresses new-content propagation from
~1 hour to ~10 seconds; and does it all without changing a single byte
on the mainline BitTorrent wire. Vanilla BEP-3/5/9/10/44/46
compatibility is preserved by construction.

A small, highly-leveraged bundle of Sybil and privacy hardenings lands
alongside — hashcash PoW on publish, double-hashed DHT salts, per-record
ed25519 signatures surviving the transport, /24 routing-table hygiene,
Merkle commits for non-repudiation, and optional Dandelion++ over
`sn_search` to hide the publisher's IP.

The full picture is called **"Aggregate"** because the defining move is
that each publisher *aggregates* their keyword index into one signed
artifact (the companion torrent), and the network *aggregates* deltas
between peers via set-reconciliation rather than per-keyword DHT lookup.

## 1. What each of the four research tracks recommended independently

| Track | Flagship recommendation | Underlying insight |
|---|---|---|
| **A — Content-routing** (IPFS, Hypercore, Iroh, Willow, Nostr) | RBSR / Negentropy over `sn_search` + Hyperbee-style B-tree inside companion torrents | Every modern content-addressed system has abandoned per-key DHT storage for either set-reconciliation (Hyperswarm, Iroh-docs, Willow) or pubsub-with-indexer-nodes (IPFS Reframe, IPNI, Nostr relays). The DHT is kept as a *discovery* layer only. |
| **B — Anonymity** (PIR, OHTTP, Dandelion++, Tor, I2P, Nym) | Dandelion++ over `sn_search` for publisher-IP privacy; OHTTP + FrodoPIR as a side-channel for query privacy | Full-substrate onion/mix networks (Tor, I2P) violate mainline compat. Modern cryptographic primitives (PIR, OHTTP, Dandelion++) deliver *opt-in* privacy with zero cost to non-anonymous users. |
| **C — DHT / Sybil** (S/Kademlia, BEP-42, Hashcash, TrustChain, Whānau) | **Invert Layer D to per-publisher pointers**; hashcash PoW on publish; double-hashed salts; Merkle commit for non-repudiation | BEP-44's per-keyword storage is the wrong primitive: O(K) items per publisher, 1000-byte cap forces sharding, plaintext salts are enumeration bait, no commit means no accountability. The right primitive is *one* pointer per publisher, content lives elsewhere. |
| **D — Gossip / Set-reconciliation** (GossipSub, Plumtree, RBSR, Rateless IBLT, BIP-152) | **Rateless IBLT over `sn_search`** replaces per-keyword BEP-44 storage; `ut_pex` capability bit for O(capable peers) discovery; publisher-rooted push-on-announce for freshness | Set-reconciliation is now cheap enough (SIGCOMM 2024 riblt paper) to route the entire keyword index peer-to-peer. For N=10k peers, 5M records, steady-state cost is ~4 kB/peer/hour. |

These are three renderings of one design. A's "Hyperbee inside companion
torrent", C's "BEP-44-as-pointer", D's "peer-wire set-reconciliation" are
the three moving parts of the same system.

## 2. The design

### 2.1 Five moving parts

1. **Publisher-pointer mutable item (PPMI).** Each publisher owns exactly
   one BEP-44 mutable item at `target = SHA1(publisher_pubkey ||
   SHA256("snet.index"))`. Its `v` field is small (well under 1000 bytes)
   and carries:
   - `ih` — infohash of the publisher's current *index torrent*.
   - `seq` — monotonic sequence (BEP-44 native).
   - `commit` — `SHA256(canonical bencoded record set)`; binds the
     pointer to the full index contents. A reader who fetches the
     index torrent MUST verify the commit matches.
   - `topics` — optional 32-byte cuckoo-filter digest summarising the
     keyword prefixes this publisher covers. Lets searchers skip
     publishers whose topics don't overlap the query.
   - `ts` — unix timestamp.
   - `next_pk` — reserved (existing schema field) for key rotation;
     still empty in v1 but now load-bearing for v1.1.

   The entire network stores `O(publishers)` BEP-44 items, not
   `O(publishers × keywords)`. For 10k publishers that's 10k items
   totalling <2 MB — the DHT barely notices.

2. **Index torrent.** A regular BitTorrent torrent whose metainfo is
   signed in the existing `snet.pubkey` / `snet.sig` fields from
   `docs/11-signing-protocol.md`. The payload is a single file (or a
   small tree of files) encoding the publisher's signed index
   records in a prefix-queryable layout:
   - **Record form:** `{pk: <32B>, kw: <string>, ih: <20B>, t: <ts>,
     pow: <varint hashcash>, sig: <64B ed25519>}`. Each record is
     individually verifiable — the sig covers the first five fields.
   - **Layout:** keys `(keyword-lowercase || infohash)` ordered lex,
     packed into B-tree pages aligned on torrent piece boundaries
     (default piece = 256 KiB, branching ≈ 256). The root page lives
     at the start of the file; interior pages point to descendants by
     (file-offset, piece-hash); leaf pages hold records. A reader who
     wants keyword `"ubuntu"` downloads the root page (1 piece),
     walks ~log₂₅₆(N) internal pages (another few pieces), and pulls
     the leaf pages for the `"ubuntu"` range only. For 5 M records
     this is ~6 pieces ≈ 1.5 MB — once per cold read, cached forever.
   - **Subscribing** means `torrent.Add(infohash)` exactly like any
     other torrent. It seeds, piece-completion callbacks fire, the
     existing ingestion pipeline (§6 of integration-design) hands the
     pages to the indexer.

3. **Set-reconciliation over `sn_search` (RIBLT).** A new LTEP sub-type
   family — `msg_type = 4 (sync_begin)`, `5 (sync_node)`, `6
   (sync_end)` — carries a Rateless IBLT exchange (Yang et al.,
   SIGCOMM 2024; Go reference `github.com/yangl1996/riblt`, Apache-2.0).
   Two peers converge on the union of their signed record sets in
   `1 + ⌈1.35·d⌉` symbols for symmetric difference `d`. Each RIBLT
   symbol is one bencoded record (~150 bytes). Concrete numbers from
   track D:
   - **Cold join** (10k network, 5M records): ~760 kB per new peer
     over its first day, amortised across 10 peer-meetings.
   - **Steady state** (1000 network-wide new publishes per hour,
     50 peer-meetings per hour): ~4 kB per peer-meeting. Cheaper
     than one BEP-44 `get` RPC today.
   - **Signatures survive the transport.** The receiver verifies
     every record's ed25519 signature against the publisher's
     declared `pk` before ingestion. A RIBLT relay can't inject
     bogus records; at worst it can withhold real ones.

4. **Publisher-rooted push-on-announce.** Extend `peer_announce`
   (msg_type 3, already in `docs/06-bep-sn_search-draft.md`) with an
   optional `recent` array: the publisher's last K self-signed records
   (default K=16, capped under 4 kB). Directly-connected peers index
   these on receipt and re-emit on their own next `peer_announce`
   with probability decaying per hop (Plumtree-lite lazy push). For
   a network with hundreds of peers per node, freshness saturates
   in O(log N) hops and seconds. For isolated nodes, degrades
   gracefully to RIBLT convergence speed.

5. **`ut_pex` capability bit.** One bit in BEP-11 `ut_pex` flags
   byte signals "this peer advertises `sn_search`". Zero new packets,
   zero new BEP, fully backward compatible (vanilla clients see a flag
   bit they don't understand and ignore). The `feeler.go` probe loop
   collapses from "LTEP-handshake every swarm peer and filter" to
   "only probe peers marked capable". Cold-cache cost drops from
   minutes to seconds.

### 2.2 Sybil / privacy bundle (ships with the redesign, small marginal cost)

These are cheap, additive, and each of them was independently
nominated by at least one research track.

- **Hashcash PoW on every record** (C). A publisher mints records only
  if `SHA256(pk || kw || ih || ts || nonce)` has D=20 leading zero
  bits. Honest cost: ~20 ms per record on a laptop. Attacker cost for
  10⁶ spam records: $0.10–$0.50 of cloud CPU. Stacks with reputation:
  the attacker still has to grind PoW per throwaway pubkey *and* build
  reputation per key. Raising D in future releases is a schema bump
  readers enforce; the bencoded `pow: <varint>` field is already
  reserved in 2.1's record form.
- **Double-hashed DHT salts** (A, C). The PPMI target uses `salt =
  SHA256("snet.index")` — a fixed bytestring, not a keyword. A passive
  DHT-node observer sees only `SHA1(pk || SHA256("snet.index"))`
  streaming past; learns nothing about what the publisher has
  indexed. Three-line change.
- **Merkle commit** (C). The PPMI's `commit` field is a
  `SHA256(canonical records)` that binds the pointer to exactly one
  set of records. A publisher who swaps hits after signing produces
  mismatched commits; readers refuse the index. A publisher who signs
  garbage and later tries to deny it has provably committed to the
  garbage. Non-repudiation for 32 bytes.
- **Keyword-encrypted residual v** (A). In the rare case we keep a
  per-keyword item (e.g. fallback lookup for rare keywords that no
  publisher covers), `v = Encrypt(KDF(keyword), hits)`. DHT storage
  nodes can't enumerate. This is the GNUnet KBlock idea ported into
  BEP-44. Optional; off by default in v1.
- **/24 routing-table hygiene** (C, D). Cap two nodes per /24 in the
  DHT client's routing table. Pubky ships this in ~50 Rust lines;
  we port to anacrolix/dht. Zero wire impact.
- **Shrink seed indexer list to ~5 EigenTrust-style anchors** (C).
  Today §4.3 plans ~20 hardcoded seed pubkeys. With PPMI + RIBLT the
  discovery problem shifts to "find publishers via BEP-51
  `sample_infohashes` crawl" (already one-liner on anacrolix/dht) plus
  `sn_search` gossip of known publisher pubkeys. Seed list becomes a
  trust anchor for bootstrapping reputation, not a registry.

### 2.3 Optional opt-in privacy (v1.1 target, not blocking v1)

- **Dandelion++ over `sn_search`** (B) for publisher IP anonymity.
  A publisher sends a signed BEP-44 put-envelope (schema unchanged:
  `{k, v, sig, seq, salt}`) via a new `sn_search msg_type = 7 stem`
  to one LTEP-capable peer. That peer re-stems with probability
  q=0.9, fluffs with probability 0.1 (performs the real BEP-44 put
  to the DHT). Storage nodes see the fluff-phase peer's IP, not the
  publisher's. The ed25519 signature still binds the content to the
  publisher's pubkey, so pubkey-level anonymity is a separate,
  composable layer (rotate keys per publication). ~500 LoC.
- **OHTTP + FrodoPIR / ChalametPIR** (B) for Layer-D query privacy.
  Cooperating indexers stand up an optional HTTPS+PIR endpoint;
  clients route queries through a public OHTTP relay (Fastly,
  Cloudflare, DivviUp). Relay sees "client talked to indexer X,"
  indexer sees "a query came through the relay" but neither sees
  which keyword. ~1–2 kLoC client-side. Pure side channel; doesn't
  touch the BT wire.

### 2.4 User-facing polish (bundled with the redesign because cheap)

- **Iroh-style `swartznet://` ticket URIs** (A). Base32-encoded blob
  containing `{infohash, signed_by_pubkey, trackers, recommended
  indexer pubkeys, ppmi target?}`. Strictly richer than a bare
  magnet link; shareable via URL/QR. The `.torrent` signing fields
  from `docs/11-signing-protocol.md` already provide the
  `signed_by_pubkey`; ticket is a presentation layer.
- **NIP-50-style query syntax** (A). Support `signed_by:abc lang:en
  size:>1gb ubuntu 24.04` in the search box. Parsed locally;
  composes with existing `POST /search` fields.

## 3. Why this is novel

### 3.0 Prior art inside SwartzNet

The **BEP-46-pointer-to-index-torrent** primitive is not new to the
project. `docs/04-bep-extension-points.md` §7.3 catalogues three
options for escaping BEP-44's 1000-byte cap: (1) multi-shard
pagination, (2) pointer item referencing a companion torrent,
(3) tombstone + rolling window. The current design (ships in
v0.4.x) picked (1) — hence `dhtindex.Manifest` and the
`"more":1` pagination field. Option (2) was noted as a possible
future when "the full keyword index outgrows in-band storage."

Likewise `internal/companion/` already implements a fully-functional
publisher and subscriber for F3 content-index companion torrents via
the BEP-46 pointer pattern (see `companion/publisher.go:1-363`,
`subscriber.go:1-497`). That subsystem currently operates on a
separate track from Layer D and isn't exposed as a search source.

### 3.1 What this proposal actually adds

The PPMI pointer primitive alone is a design-option repositioning,
not a research contribution. The novelty is the *combination*:

1. **Elevate pointer items from "optional" to "primary"** and retire
   per-keyword BEP-44 storage entirely — collapsing the existing
   shard manifest and the "more:1" pagination code path.
2. **Fuse the pointer-plus-companion-torrent pattern with Rateless
   IBLT set-reconciliation** over `sn_search`, so peers never need
   to re-fetch the companion torrent just to get a few new records.
3. **B-tree piece-aligned layout** inside the companion torrent so a
   reader can prefix-scan without downloading it all.
4. **Per-record ed25519 signatures surviving the RIBLT transport**
   — the Sybil defence survives the relay.
5. **Merkle commit on the PPMI binding the pointer to a specific
   record set** — closes the bait-and-switch gap that neither the
   shard-based nor the vanilla pointer design addresses.
6. **Hashcash PoW on every record** plus **double-hashed salt** —
   both cheap, both independently valuable, stacking additively with
   reputation.
7. **Publisher-rooted push-on-announce** over `peer_announce` —
   turns the current "1-hour refresh" freshness into seconds.
8. **`ut_pex` flag bit** for `sn_search`-capable peers — removes the
   feeler-probe-every-peer cost.

No prior system combines *all three* of:

1. **Published index as a signed BitTorrent companion torrent**,
   with piece-aligned B-tree layout enabling sparse range fetch.
2. **Per-publisher BEP-44 pointer** instead of per-keyword items.
3. **Rateless IBLT set-reconciliation over the peer-wire** as the
   primary propagation channel, with DHT demoted to pubkey/pointer
   discovery.

Individually each component exists in prior art:

- IPFS/IPNI: has indexer-node content-routing, but over a separate
  protobuf DHT and a separate pubsub, and requires centrally-operated
  indexer aggregators (cid.contact) to scale.
- Hyperswarm / Hyperbee: has the signed append-only log and the
  prefix-queryable B-tree, but lives on its own UDP DHT with its own
  hole-punching and its own identity system.
- Nostr: has NIP-50 search and NIP-77 Negentropy set-reconciliation,
  but centralised around relays and runs over WebSockets.
- Iroh-docs / Willow: has RBSR over session-per-peer transports, but
  requires a separate ticket+relay infrastructure and a 3D namespace.
- Freenet/GNUnet: has keyword-encrypted pointers, but stores them in
  a custom DHT with onion routing.
- BitMagnet: uses BEP-51 crawl + central Postgres, not peer-to-peer.

The SwartzNet "Aggregate" design is the first (to the best of our
research) to ride **entirely on mainline BitTorrent primitives**
(BEP-3/5/9/10/44/46/51 + one LTEP extension name) while delivering
the end-user experience of a modern content-addressed search system.
The mainline DHT does one thing — serve pointers — and every other
function lives in peers or in standard BitTorrent torrents.

## 4. Cost / benefit vs. the current design

| Property | Current (`docs/05-integration-design.md`) | Aggregate |
|---|---|---|
| Number of DHT items (10k publishers, 100 keywords each) | ≥ 1,000,000 | ~10,000 |
| Per-keyword storage cap | 1000 bytes (forces shard pagination) | None (lives in torrent piece blob) |
| Cold hot-keyword lookup | 1–2 s DHT walk p50 (Pkarr measurement) | <100 ms local cache hit after cold sync |
| Hot-keyword lookup (popular) | Fan-out to N indexers; O(log N · RTT) | Local; O(1) |
| Freshness (new publish visible elsewhere) | ~1 hour (BEP-44 refresh cadence) | ~10 s (push-on-announce saturates) |
| Keyword enumeration leak | Trivial (plaintext salt) | Dictionary-grind only (double-hashed) |
| Publisher spam cost per 10⁶ items | $0 | $0.10–$0.50 with D=20 hashcash |
| Publisher IP leak to DHT storage nodes | Yes | Opt-in via Dandelion++ |
| Non-repudiation of bad hits | None | Per-record ed25519 sig + Merkle commit |
| Mainline wire impact | LTEP extension + BEP-44 items | LTEP extension + BEP-44 items + ut_pex flag bit (same envelope categories) |
| Lines of new code vs current | — | ~2500 LoC across dhtindex/swarmsearch/companion/reputation |

Total distributed-layer bandwidth drops ~40% per track D's analysis
(set-reconciliation is cheaper than repeated DHT GETs for hot-path
queries), while delivering dramatically better freshness and privacy.

## 5. Interop matrix (every cell must stay green)

| Scenario | Expected behaviour |
|---|---|
| Vanilla qBittorrent / libtorrent peer sees SwartzNet peer | Sees LTEP handshake with an extra `sn_search` entry in `m` and an extra flag bit in `ut_pex`; both ignored per BEP-10 / BEP-11 "unknown keys" rules. Piece transfer unaffected. |
| Vanilla peer queries SwartzNet DHT node | Gets standard BEP-44 responses — PPMI items are just BEP-44 mutable items with small opaque `v`. No custom verbs. |
| Vanilla peer downloads a SwartzNet publisher's index torrent | Just a torrent. If they don't parse SwartzNet's B-tree format, they have a file tree they can ignore. The `snet.pubkey`/`snet.sig` fields are optional per `docs/11-signing-protocol.md`; infohash preserved. |
| Two SwartzNet peers with `sn_search` negotiate RIBLT | Fall back to legacy `sn_search` query/result if either side's `services` bitfield clears the new bit (allocated from the reserved range bits 9–63 per `docs/06-bep-sn_search-draft.md`). |
| Old SwartzNet client (v1.0) meets new Aggregate client | v1.0 doesn't advertise the Aggregate bit in services. New client serves legacy query/result from its local index; never ships RIBLT. Old client's per-keyword BEP-44 items remain readable (we keep the v1 `KeywordValue` decoder for ≥12 months as a compat fallback). |
| Dandelion++ stem arrives at a non-stem-capable peer | That peer has no stem-capability bit set; publisher picks another stem hop or falls back to direct fluff. No protocol violation. |

The design introduces *no new mainline verbs, no new reserved bits, no
new UDP ports*. Every change lives inside the existing LTEP extension
space, the existing BEP-44 value schema, the existing BEP-11 PEX flag
byte, or inside ordinary BitTorrent torrents.

## 6. Migration

The existing Layer D (per-keyword BEP-44 items, shard manifests, "more"
pagination) does not disappear on day one. The migration plan is four
phases over ~two minor releases.

### Phase 1 — land alongside (v0.5.0)
- Ship PPMI publisher + reader, index-torrent build + publish.
- Publisher writes *both* formats: the new PPMI *and* the legacy
  per-keyword items. Readers prefer PPMI when available; fall back
  to legacy lookup otherwise.
- Ship `sn_search services` bit 9 (`BitSetReconciliation`) and the
  three new msg_types behind a capability gate.
- Ship `ut_pex` flag bit (we claim an unused bit per BEP-11 §4).

### Phase 2 — deprecate-warn (v0.6.0)
- Publisher writes PPMI only; no new per-keyword items.
- Reader's legacy fallback continues to work but emits a one-time
  deprecation notice in `/status`.
- Hashcash difficulty bumped from 0 to 20 bits.
- Double-hashed PPMI salt goes live.

### Phase 3 — retire legacy (v0.7.0)
- Reader removes the legacy per-keyword fallback. Clients that still
  speak it only will stop getting answers and must upgrade.
- `dhtindex.Manifest` type (shard manifest) can be removed, ~245 lines
  of code.

### Phase 4 — opt-in privacy (v0.8.0+)
- Ship Dandelion++ behind `--private-publish` and `sn_search services`
  bit 10 (`BitDandelionRelay`).
- Ship OHTTP+PIR behind `--private-query` and a new indexer capability
  flag.

Data migration: existing users' reputation tracker, known-good Bloom
filter, and identity key all carry over unchanged — these are the
per-pubkey trust anchors and they are orthogonal to the storage
layout.

## 7. Test plan

### 7.1 Unit tests (per-package, under `go test -race`)

- `internal/dhtindex/ppmi_test.go` — PPMI encode/decode round-trip,
  cap-check, commit verification, `next_pk` passthrough.
- `internal/dhtindex/pow_test.go` — Hashcash verify/mint across
  difficulty thresholds; attacker worst-case timing.
- `internal/dhtindex/doublehashed_salt_test.go` — known-answer test
  that `salt = SHA256("snet.index")` matches the spec hex.
- `internal/companion/btree_test.go` — B-tree page encode/decode,
  prefix range iteration, piece-aligned layout fuzz.
- `internal/companion/sparse_fetch_test.go` — given a partial piece
  set, confirm that a prefix walk only requests the needed pieces.
- `internal/swarmsearch/riblt_test.go` — RIBLT symmetric-difference
  cases (d=0, d=1, d=100, d=10k); convergence round count.
- `internal/swarmsearch/push_on_announce_test.go` — lazy-push decay,
  dedup on re-emit, bounded fan-out.
- `internal/reputation/bloom_test.go` — no change; existing coverage
  survives.

### 7.2 Integration tests (Docker + netem per `testbed/`)

- **Three-node Aggregate cold join.** Start one seed publisher with
  500 pre-indexed torrents, two cold subscribers. Assert RIBLT
  convergence time < 60 s on LAN; bandwidth used < 1 MB per
  subscriber.
- **Netem loss 5% / latency 200 ms.** Same, but lossy. RIBLT
  round-count MUST increase gracefully; no deadlock.
- **Sybil publisher.** One honest publisher with PoW, one attacker
  minting records without PoW. Subscribers MUST reject attacker
  records at ingestion; attacker's BEP-44 seq MUST NOT displace the
  honest publisher (the attacker is under a different pubkey).
- **Mainline-interop.** Two SwartzNet peers + one vanilla
  libtorrent peer. The libtorrent peer MUST complete a piece
  transfer of the shared test torrent with zero protocol errors.
  Capture the LTEP handshake; the libtorrent peer MUST ignore the
  `sn_search` entry and the `ut_pex` flag bit without logging a
  warning.
- **Legacy-fallback.** A v0.4.x client queries a v0.6.0 network.
  MUST receive legacy-format responses for ≥12 months.

### 7.3 Property tests

- **Commit binding.** For any PPMI with `commit = SHA256(records)`,
  if records are permuted or one record is altered, the reader MUST
  reject with a well-defined error.
- **Signature verification.** For any RIBLT-received record, if the
  signature fails verification (wrong pubkey, tampered fields), the
  record MUST NOT be ingested into the local index.
- **Monotonic seq.** For any PPMI, `seq` MUST be strictly increasing
  per publisher. A reader MUST refuse to downgrade.

### 7.4 Measurement / regression gates

On every CI run, track and regression-gate:

- Bytes per peer-meeting for a synthetic 5M-record network (target:
  < 8 kB steady state).
- Cold-keyword p50 lookup latency (target: <200 ms after first
  index-torrent fetch).
- Freshness latency — time from publisher.Publish() to any other
  peer seeing the record (target: <30 s over 50-peer mesh).
- Index-torrent size for 500-torrent publisher (target: <10 MB).

### 7.5 Wire-compat matrix (§5) as automated gates

One CI job per row in the §5 table. Each job boots a fresh container,
runs the described scenario, and asserts the expected behaviour. This
is the binding constraint — a row going red blocks the release.

## 8. What this proposal does NOT do

- **Does not** change the anonymity stance. Queries over `sn_search`
  still leak to peers; piece transfer still joins swarms visibly. The
  Dandelion++ and OHTTP+PIR additions are opt-in privacy for
  specific threats (publisher IP, query contents) on specific
  layers (publish, Layer-D query). Full-network anonymity is still
  "use a VPN / Lokinet / Tor" per §10 of integration-design.
- **Does not** implement multi-writer indexes or capability delegation
  (Willow/Meadowcap). One pubkey = one index. If we need curated
  multi-writer indexes later, the Veilid SMPL schema idea slots in
  cleanly; don't build it speculatively.
- **Does not** introduce a new pubsub network, relay protocol, or
  ticket server. The ticket URI format is a presentation of existing
  data; it isn't a routing layer.
- **Does not** break the Bleve ingest pipeline, the per-torrent
  indexing opt-in, the publisher signing protocol, the reputation
  tracker, or the known-good Bloom filter. All four survive the
  redesign; Aggregate is a storage-layer change, not an app change.

## 9. Open questions for the next iteration

1. **Exact B-tree page format.** The sketch in §2.1 assumes 256-way
   branching on 256 KiB pieces. A real design needs to specify:
   page header layout, interior-page pointer format (is it
   `(offset, piece_sha1)` or `(piece_index)`?), leaf-page record
   packing (fixed-size slots vs varint-encoded), handling of
   oversized records (keywords > 1 page?). Prototype on a 5M-record
   corpus to measure read amplification before committing.
2. **RIBLT parameter tuning.** The 1.35× overhead assumes the
   canonical symbol count; in practice we want to trade slightly
   more symbols for lower round count on high-RTT links. The riblt
   Go reference exposes knobs; we should benchmark.
3. **What lives at the old per-keyword BEP-44 target during migration?**
   During phase 1 we dual-write. But the double-hashed salt means the
   PPMI sits at a different target than the legacy per-keyword items;
   that's fine. During phase 3 we stop dual-writing — do we proactively
   wipe the old items (BEP-44 CAS delete) or let them expire (24-hour
   TTL)? Letting them expire is simpler and mainline-friendly.
4. **Publisher re-balancing.** Today each publisher writes their own
   full index on every change, which is O(N) per update. A future
   Hyperbee-style append-only companion torrent would be O(log N) per
   update. For v1 we accept the simpler full-rewrite model; scheduled
   for v1.2.
5. **Companion-index discovery for brand-new publishers.** The
   `peer_announce.recent` push and RIBLT both require a known pubkey
   set. How does a totally-cold subscriber bootstrap? Proposed:
   (a) 5 hardcoded seed pubkeys as reputation anchors only,
   (b) BEP-51 `sample_infohashes` crawl filtered by `snet.pubkey`
   presence in metainfo, (c) `sn_search` peer_announce carrying the
   peer's own publisher pubkey (already in the v1 schema). Three
   independent channels for Sybil-resistant bootstrap.
6. **Reputation with per-record provenance.** Today `reputation.Tracker`
   keeps one score per publisher pubkey. After Aggregate, a single
   publisher can provably commit to specific records (via Merkle
   commit). Should reputation become per-(pubkey, record-prefix)?
   Probably yes — a publisher who's great on `linux` may be spammy on
   `music`. Track D's fingerprint-per-range suggests a clean data
   structure. Deferrable.
7. **BEP-44 support fraction.** C estimated 85–95% of mainline nodes
   support BEP-44 gets/puts; no independent 2026 measurement. Before
   phase 3 we should run our own measurement (BEP-51 crawl + sampled
   `put` against random nodes) and publish numbers. If <60% we need
   to rethink the pointer design.

## 10. Why this is safe to implement and test

- **The five moving parts are independently shippable.** PPMI alone
  (without RIBLT) is already a win over per-keyword items. RIBLT
  alone (without PPMI) works as an overlay on legacy Layer D.
  Publisher-rooted push-on-announce alone works as a freshness
  optimization regardless of the storage layout. `ut_pex` flag bit
  alone is a peer-discovery speedup. Dandelion++ is an opt-in
  post-v1 feature. Any failure localises.
- **Every part has a prior-art reference implementation in Go or
  straightforwardly portable.** anacrolix/torrent for BT; yangl1996/riblt
  for RIBLT; hashicorp/memberlist's SWIM+gossip code for push-on-
  announce patterns; miekg/dns-style double-hashing is trivial.
- **Testbed already exists.** `testbed/` has netem scenarios; `tests/
  torrent-test/` has multi-peer fixtures; regtest mode (`services` bit
  8) already isolates test clients from mainnet.
- **The interop matrix is small and automatable.** §5 lists
  six rows; each is one CI job.
- **The migration plan is explicit and dual-writing.** A user
  upgrading from v0.4.x to v0.5.0 sees no user-visible change; they
  get faster results and don't know why. The deprecation is phased
  over at least two minor releases, with one-time UI notices.

## 11. Implementation order (suggested)

In the interest of landing value quickly and de-risking the biggest
unknowns first, tackle in this order:

1. **PPMI schema + publisher + reader in `internal/dhtindex`.** Schema
   is ~150 LoC; publisher glue ~200 LoC; reader with compat-fallback
   to legacy ~200 LoC. Can be tested entirely in the existing fake-DHT
   harness.
2. **`ut_pex` flag bit + feeler filter.** ~50 LoC across
   `internal/swarmsearch/feeler.go` and the anacrolix ut_pex handler.
   Gives a measurable peer-discovery speedup on its own.
3. **Index-torrent build/consume in `internal/companion`.** The
   B-tree layout is the unknown here — prototype and measure first,
   then freeze the format. ~800 LoC.
4. **RIBLT over sn_search.** ~600 LoC including the riblt library
   integration. This is the biggest single chunk; treat as its own
   milestone. Unit-testable against a fixture set in-process.
5. **Push-on-announce.** ~150 LoC. Plugs into the existing
   `peer_announce` path.
6. **Sybil bundle: hashcash + double-hashed salt + Merkle commit +
   /24 hygiene.** ~250 LoC total. Land together; they're small and
   orthogonal.
7. **NIP-50 query parser + ticket URI format.** ~200 LoC. User-facing
   polish; zero wire impact.
8. **Deferred: Dandelion++, OHTTP+PIR, per-record reputation.**

Total estimate: ~2500 LoC of production code + ~2000 LoC of tests,
over the course of M16–M19 (four milestones), landing ~Q3 2026.

## 12. References

The four companion research documents enumerate primary sources:

- `docs/research/A-content-routing.md` — libp2p, Hypercore, Iroh,
  Willow, Nostr, Veilid, CAN/Pastry/Tapestry.
- `docs/research/B-anonymity-primitives.md` — Tor v3, I2P, Lokinet,
  Nym, HORNET/Karaoke/Vuvuzela/Stadium, PIR family, OHTTP, GNUnet R5N,
  obfs4, Dandelion / Dandelion++.
- `docs/research/C-dht-sybil-reputation.md` — S/Kademlia, BEP-42,
  R5N, TrustChain, EigenTrust, PoP, hashcash, libp2p Kad-DHT, BEP-51,
  HyParView/Plumtree/SWIM, Bitcoin INV / Ethereum discv5, Pkarr and
  mainline DHT measurements.
- `docs/research/D-gossip-setsync-discovery.md` — GossipSub v1.1,
  Episub, Plumtree + HyParView, SWIM/memberlist, RBSR/riblt/
  minisketch/Graphene, Bloom/Cuckoo summaries, CRDTs, SSB-EBT,
  Bitcoin BIP-152/BIP-157, Ethereum discv5, BEP-11/51, Hypercore/
  Willow/iroh.

Key individual citations load-bearing for this proposal:

- **Rateless IBLT**: Yang, Wang, Kuzmanovic, Katz-Bassett,
  "Practical Rateless Set Reconciliation," SIGCOMM 2024.
  <https://dl.acm.org/doi/10.1145/3651890.3672219>. Go reference:
  `github.com/yangl1996/riblt` (Apache-2.0).
- **RBSR**: Meyer, "Range-Based Set Reconciliation," arXiv:2212.13567,
  2023. Production reference: strfry Negentropy.
- **Dandelion++**: Fanti et al., "Dandelion++: Lightweight
  Cryptocurrency Networking with Formal Anonymity Guarantees,"
  SIGMETRICS 2018.
- **BEP-44, BEP-46, BEP-51**, BitTorrent.org.
- **S/Kademlia**: Baumgart & Mies, ICPADS 2007.
- **Hashcash**: Back, 2002.
- **Pkarr mainline DHT latency**: <https://pubky.github.io/pkarr/>.
- **BEP-42 eclipse analysis**: Bühler, "Mainline DHT — Censorship
  Resistance Explained," Pubky, Dec 2024.
- **Hyperbee / Hyperswarm**: `github.com/holepunchto/hyperbee`,
  `github.com/holepunchto/hyperdht`.
- **Oblivious HTTP**: Thomson & Wood, IETF RFC 9458.
- **FrodoPIR**: Davidson, Pestana, Celi, PETS 2023 Issue 1.
- **NIP-50 (Nostr search)**: <https://github.com/nostr-protocol/nips/blob/master/50.md>.

---

*This proposal is the output of iteration 1 of the
`/loop do protocol level research …` conversation. Subsequent
iterations should (a) prototype the B-tree page layout on a 5M-record
corpus, (b) benchmark RIBLT parameter tradeoffs on simulated
10k-peer topology, (c) firm up the `ut_pex` flag-bit allocation with
an inquiry to the BEP-11 maintainers, and (d) convert §7's test plan
into concrete stub files in the appropriate package directories.*
