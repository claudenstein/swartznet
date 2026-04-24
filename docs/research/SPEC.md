# SPEC — Wire & on-disk formats for "Aggregate"

| Field | Value |
|---|---|
| Date | 2026-04-24 |
| Status | Draft, targets PROPOSAL.md iteration 2 |
| Companion | `docs/research/PROPOSAL.md` (design rationale) |

This document closes the three implementation-blocking open
questions from `PROPOSAL.md` §9:

- §1 — B-tree page format for the companion index torrent (§9.1)
- §2 — RIBLT wire format for `sn_search msg_type = 4..8` (§9.2)
- §3 — Cold-subscriber bootstrap procedure (§9.5)

Byte layouts here are the contract between publisher and reader
implementations. The remaining open questions (§9.3 migration
detail, §9.4 append-only updates, §9.6 per-record reputation,
§9.7 BEP-44 support measurement) are not spec-blocking.

## 1. Companion index-torrent B-tree layout

### 1.1 Goals

- Support prefix range scans (`keyword LIKE "ubu%"`) with minimum
  piece download — a reader walking for `"ubuntu"` should pull
  only the ~log₂₅₆(N) pages on the root-to-leaf path plus the leaf
  pages that overlap the key range.
- Verify integrity per page using BitTorrent's native piece hashes
  (no redundant per-page checksum needed; the BT protocol already
  authenticates each piece against the .torrent's pieces array).
- Stable, deterministic layout given a sorted record stream so two
  publishers with identical records produce byte-identical torrents
  — required for the PPMI `commit` (PROPOSAL §2.2) to be
  reproducible.
- Fit inside one BitTorrent piece per B-tree page. The piece size
  is chosen by the publisher at torrent creation via
  `metainfo.ChoosePieceLength`; we REQUIRE 256 KiB ≤ piece ≤ 4 MiB
  so the branching factor and record density stay predictable.

### 1.2 File structure

The companion torrent is a single-file torrent. Its name is
`<pubkey-hex>-<seq>.snag` where `<seq>` matches the PPMI sequence
number at publish time. File contents:

```
[page 0 : root                          ]  ← always piece 0
[page 1 : interior or leaf              ]
[page 2 : …                             ]
…
[page N-1 : …                           ]
[trailer page                           ]  ← last piece
```

`N` is the number of B-tree pages; the **trailer page** is always
the final piece and encodes global metadata (see §1.6). Reader
workflow: fetch piece 0 (root) and the final piece (trailer),
follow child pointers from root down to the leaves covering the
queried key range, fetch only those leaves.

### 1.3 Page header

Every page begins with a fixed 16-byte header so a reader can
dispatch on `kind` and `level` without decoding the payload:

```
offset  size  field
------  ----  -----
0       6     magic             ASCII "SNAGG\0"
6       1     version           0x01 (this spec)
7       1     kind              0x00 root, 0x01 interior, 0x02 leaf, 0xFF trailer
8       1     level             B-tree depth; 0 = leaf; root kind has its own level
9       1     flags             bit 0: has_overflow_ptr
10      2     payload_length    little-endian uint16, bytes in payload below
12      4     _reserved         must be zero
16      …     payload           kind-specific (§1.4, §1.5, §1.6)
```

Version `0x01` readers MUST refuse `version != 0x01`. The magic
lets a vanilla tool `file(1)`-identify these torrents.

### 1.4 Interior / root pages

Interior and root pages have identical payload encoding; the only
difference is that the root page is always piece 0.

```
payload := num_children:u16
           [ separator_key, child_piece_index ] * num_children
           [ filler_zero_bytes to page end ]

separator_key     := varint_length || key_bytes
                     (the smallest key in the child subtree)
child_piece_index := u32 little-endian, index into torrent's piece array
```

`key_bytes` is the full `keyword || 0x00 || infohash` byte sequence
— lowercased UTF-8 keyword, one `NUL` separator, 20-byte raw
infohash. The lex order over these keys is well-defined and
matches the leaf ordering in §1.5.

Branching factor is not fixed; it adapts to per-page byte budget.
For a 256 KiB page with ~20-byte separators, each child costs
~24 bytes → ~10,000 children max. Publishers SHOULD target 256
children per interior page in practice so trees are short and
well-balanced.

### 1.5 Leaf pages

```
payload := num_records:u16
           [ record_length:varint, record_bytes ] * num_records
           [ filler_zero_bytes ]

record_bytes := canonical bencoded dict
                { "pk": <32B>, "kw": <utf-8 string, ≤64 bytes>,
                  "ih": <20B>,   "t": <unix ts int>,
                  "pow":<varint>, "sig": <64B ed25519> }
```

Records are sorted by `keyword || 0x00 || infohash` — the same
ordering used for interior-page separators. The signature covers
`pk || kw || ih || t || pow` (no length prefixes; canonical form).
The `pow` field is the hashcash nonce such that
`SHA256(pk || kw || ih || t || nonce)` has D leading zero bits
(D=20 at v1 launch; readers enforce `D >= published_minimum`
from the trailer page §1.6).

A record too large for one leaf page is illegal in v1 — records
are bounded at 256 bytes by construction. Oversize keywords
(`len(kw) > 64`) MUST be rejected at publish time.

### 1.6 Trailer page

Last piece of the torrent. Binds the tree to the publisher and
lets readers verify global invariants before trusting leaves.

```
payload := trailer_version:u8         # 0x01
           pubkey:[32]byte
           seq:u64                     # matches PPMI seq
           created_ts:u64
           root_piece_index:u32        # always 0 but explicit
           num_pages:u32
           num_records:u64
           min_pow_bits:u8             # typically 20
           tree_fingerprint:[32]byte   # SHA-256 over canonical record stream
           publisher_sig:[64]byte      # ed25519 over all fields above
           _reserved: zero-padded to page end
```

`tree_fingerprint` is exactly the PPMI `commit` field. A reader
fetches the trailer piece first, verifies `publisher_sig`, and
only then trusts the tree. A publisher who rewrites a leaf without
bumping `tree_fingerprint` produces an invalid trailer; readers
reject. A relay that swaps in a malicious trailer breaks the
publisher's signature; readers reject.

### 1.7 Reader workflow (prefix query)

```
func FindRecords(torrent, prefix []byte) []Record {
    trailer := torrent.Piece(torrent.NumPieces - 1)
    verifyTrailerSig(trailer)                        // ed25519 over payload

    root := torrent.Piece(0)
    requireMagic(root, "SNAGG\0")

    leaves := walk(root, prefix)                     // DFS on interior pages,
                                                     // only descending where
                                                     // [separator_i, separator_{i+1})
                                                     // overlaps prefix
    var out []Record
    for _, leafIdx := range leaves {
        page := torrent.Piece(leafIdx)
        for _, rec := range decodeLeaf(page) {
            if !bytes.HasPrefix(rec.Kw, prefix) { continue }
            verifyRecordSig(rec)                     // ed25519 per record
            verifyPoW(rec, trailer.MinPoWBits)       // hashcash check
            out = append(out, rec)
        }
    }
    return out
}
```

For a 5 M-record corpus at branching 256, depth = ⌈log₂₅₆(5 M)⌉ = 3
interior levels → ≤4 pages on the root→leaf path + the leaves in
the prefix range. For a narrow prefix like `"ubuntu-24.04"` this
is typically 5–8 pieces ≈ 1.5 MB, fetched once, cached.

### 1.8 Publisher workflow (build)

```
func BuildIndexTorrent(records []Record, pubkey, privkey, seq) (*metainfo.MetaInfo, error) {
    sort.Slice(records, byKeyThenInfohash)
    pages := layOutLeaves(records, pageSize)       // greedy fill, page per piece
    for len(pages) > 1 {
        pages = buildInteriorLevel(pages, pageSize)
    }
    root := pages[0]

    fileBytes := concatPages(root, ...leaves...)
    fileBytes = append(fileBytes, encodeTrailer(
        pubkey, seq, time.Now(), root, numRecords,
        minPoWBits=20, fingerprint=SHA256(canonRecords(records)),
        sig=ed25519.Sign(privkey, ...)))

    mi := buildMetainfo(fileBytes, pieceLength=256<<10)
    signMetainfo(mi, pubkey, privkey)              // existing snet.sig path
    return mi, nil
}
```

Deterministic: two publishers with identical input records produce
byte-identical `fileBytes` → identical infohash → identical PPMI
commit. This is what makes RIBLT convergence globally coherent.

## 2. `sn_search` set-reconciliation wire format

### 2.1 Message types

Extends `docs/06-bep-sn_search-draft.md`:

| `msg_type` | Name | Direction | Added in | Gate |
|---|---|---|---|---|
| 0 | query | initiator → responder | v1 | always |
| 1 | result | responder → initiator | v1 | always |
| 2 | reject | responder → initiator | v1 | always |
| 3 | peer_announce | either | v1 | always |
| 4 | sync_begin | initiator → responder | v1.1 | `services.BitSetReconciliation` (bit 9) |
| 5 | sync_symbols | either | v1.1 | bit 9 |
| 6 | sync_need | either | v1.1 | bit 9 |
| 7 | sync_records | either | v1.1 | bit 9 |
| 8 | sync_end | either | v1.1 | bit 9 |

Peers without bit 9 set in their last `peer_announce` MUST NOT
receive msg_types 4–8. Peers that receive a message type whose bit
is unset SHOULD reply with `reject { code: 2 /* unsupported */ }`.

### 2.2 Session state machine

```
(idle)
   ─── sync_begin ──▶ (negotiating)
                         ─── sync_symbols …       (RIBLT exchange, iterative)
                         ─◀── sync_symbols …
                         …
                         ─── sync_need ──────▶   (record request)
                         ─◀── sync_records ──
                         ─── sync_need ──────▶   (optional reverse direction)
                         ─◀── sync_records ──
                         ─── sync_end ───────▶   (done)
(idle)
```

A session is scoped to one `txid` and one filter. Either side
SHOULD send `sync_end` within 60 s of the last message or the peer
MAY consider the session abandoned. A peer MUST NOT open a second
concurrent sync session on the same TCP connection (same rule as
the existing msg_type 0 query).

### 2.3 `sync_begin` (msg_type 4)

```
{
  "msg_type":     4,
  "txid":         <u32>,
  "algo":         "riblt-v1",
  "filter": {
    "pubkeys": [<32B ed25519>, ...],    # required: publishers of interest
    "since":   <unix ts int>,           # optional: records from this time onward
    "prefix":  "<utf8 keyword prefix>"  # optional
  },
  "element_size": 32,                   # fixed bytes per RIBLT element ID
  "local_count":  <int>,                # how many records the sender has matching filter
  "max_symbols":  <int>,                # sender's cap on total symbols per side
  "max_bytes":    <int>                 # sender's cap on bulk records per side
}
```

`pubkeys` is MANDATORY. A subscriber who wants to sync "everyone I
know about" sends the full pubkey list from their local publisher
set. `element_size = 32` means RIBLT IDs are 32-byte values
(see §2.4). `local_count` gives the peer a hint for initial symbol
budget. `max_symbols` and `max_bytes` bound the session cost —
violating them triggers `sync_end` with `"limit_exceeded"`.

### 2.4 RIBLT element ID

The RIBLT element for a record `r` is its 32-byte record ID:

```
record_id(r) := SHA256(r.pk || r.kw || r.ih || r.t)   // [:32]
```

Note: the PoW nonce and signature are NOT part of the ID — two
valid signings of the same semantic record produce the same ID,
which is what we want. A receiver's "records I have matching the
filter" is computed as the set of `record_id` values for matching
local records; that is what RIBLT will reconcile.

### 2.5 `sync_symbols` (msg_type 5)

```
{
  "msg_type": 5,
  "txid":     <u32>,
  "symbols": [                          # 1..100 symbols per message
    {
      "c":  <signed int32>,             # net count
      "h":  <u64 hash-sum>,             # XOR-sum of element IDs' first 8 bytes
      "b":  <32 bytes>                  # XOR-sum of full 32-byte element IDs
    },
    …
  ],
  "done": 0|1,                          # 1 = "I will send no more symbols"
  "index": <u32>                        # first symbol's position in sender's stream
}
```

The `index` field is critical for rateless encoding: RIBLT's
codebook is seeded so symbol `i` always XORs the same subset of
elements on both sender and receiver. The receiver subtracts their
local symbol `i` from the sender's symbol `i` to get the
difference symbol; peeling proceeds in index order.

Maximum 100 symbols per message keeps each ≤ 5 KB. A sender streams
symbols in batches until either:
(a) the receiver ACKs convergence with a `sync_need` pointing at
    zero remaining unknowns (shouldn't happen — there's always at
    least one direction's unknowns),
(b) the receiver sends its own `sync_need` indicating decode
    success, or
(c) `max_symbols` is reached → `sync_end { status: "limit_exceeded" }`.

### 2.6 `sync_need` (msg_type 6)

```
{
  "msg_type": 6,
  "txid":     <u32>,
  "ids":      [ <32B record_id>, ... ]   # ≤ 1000 per message
}
```

After RIBLT decoding, a peer knows the IDs of records the *other*
side has that *it* lacks. It sends `sync_need` with those IDs. The
peer responds with `sync_records`. A `sync_need` of zero IDs
signals "I'm done decoding from my side"; if both sides send it,
the session is complete and either side can send `sync_end`.

### 2.7 `sync_records` (msg_type 7)

```
{
  "msg_type": 7,
  "txid":     <u32>,
  "records": [                          # ≤ 500 records per message
    { "pk": <32B>, "kw": "lin", "ih": <20B>,
      "t":  <int>, "pow": <varint>, "sig": <64B> },
    …
  ],
  "missing": [ <32B>, … ]               # IDs from the preceding sync_need
                                        # that the sender doesn't have either
}
```

On receipt: the receiver MUST re-verify the signature and PoW of
every record before ingesting into its local index. Any record
failing verification is silently dropped and the sender's
misbehavior score is bumped (`internal/swarmsearch/misbehavior.go`).

`missing` handles the common race where the requester computed
"peer has IDs X" from a symbol set that included records since
deleted on the peer's side (e.g. TTL expiry). Reader doesn't retry
those — the records are gone.

### 2.8 `sync_end` (msg_type 8)

```
{
  "msg_type": 8,
  "txid":     <u32>,
  "status":   "converged" | "limit_exceeded" | "aborted",
  "decoded":  <int>,                    # # new records this session ingested
  "sent":     <int>,                    # # records sent to peer this session
  "bytes_in": <int>,
  "bytes_out":<int>,
  "abort_code": <int, when status=aborted>
}
```

Either side MAY send `sync_end` to close a session. The counter
fields are observational — useful for regression tests and for
feeding `max_bytes` back-pressure decisions on future sessions
with the same peer.

### 2.9 Rate limits

- Max one active sync session per TCP connection.
- Max one sync session initiated per peer per 5 minutes.
- Cumulative: 10 MB of `sync_records` bulk per peer per hour.
- Default `max_symbols` = 2000, default `max_bytes` = 1,048,576.
- If a peer initiates a second session before the first's
  `sync_end`, respond with `reject { code: 0 /* rate_limited */ }`.

Implementations MUST expose these as config knobs; they are not
wire-level constraints and may be tightened per peer based on the
misbehavior tracker.

## 3. Cold-subscriber bootstrap

A subscriber with an empty publisher set and an empty local
reputation tracker reaches a useful state through **three
independent channels**, running in parallel. The subscriber is
useful as soon as *one* returns records.

### 3.1 Channel A — hardcoded anchor pubkeys

Ship 5 ed25519 pubkeys in the binary (`internal/dhtindex/anchors.go`).
These are the SwartzNet project's own and partner operators' keys.
They exist for exactly one reason: to bootstrap the reputation
tracker with non-zero weights on trustworthy publishers. On first
launch:

1. For each anchor `pk`, issue a BEP-44 `get` at
   `target = SHA1(pk || SHA256("snet.index"))` to fetch the PPMI.
2. If found, add torrent `ppmi.ih` to the engine.
3. On completion, subscriber.go parses the B-tree trailer
   (§1.6), verifies the publisher signature, and ingests records
   via the local index.
4. Reputation tracker seeded: anchor pubkeys start at score 0.8;
   records from anchors feed into the known-good Bloom filter on
   ingest.

Expected bandwidth: 5 PPMI gets (~5 KB) + 5 index torrents at
~5 MB each = ~25 MB once per cold boot. Ongoing refresh every
12 hours.

### 3.2 Channel B — BEP-51 `sample_infohashes` crawl

The anacrolix DHT library exposes `SampleInfohashes(addr)` which
returns up to 50 infohashes a node has seen plus (`num`, `interval`).
A background crawler:

1. Picks 10 random DHT nodes from our routing table.
2. For each, calls `SampleInfohashes`.
3. Filters results: for each infohash, checks whether it's
   already known, known-bad, or a candidate.
4. For up to 5 candidate infohashes per crawl round, issues a
   BEP-9 metadata fetch. On completion, inspects the metainfo for
   a `snet.pubkey` field (top-level; see `11-signing-protocol.md`).
5. If `snet.pubkey` is present and the metainfo's `snet.sig`
   verifies, add the pubkey to the "observed publishers" set
   with reputation 0.1 (low; not yet trusted).
6. Issue the PPMI get for that publisher on a throttled queue —
   at most 1 new PPMI subscription per hour per subscriber.

Expected bandwidth: ~100 KB/s peak for the sampling, up to ~1 MB
per metadata fetch, ~5 metadata fetches per minute. Over 24 hours
this harvests 50–500 candidate publishers — far more than the
subscriber could sensibly follow, so an admission policy is
required.

**Admission policy.** A newly observed publisher is provisionally
added to the "known publishers" set iff:

- Their PPMI has `pow ≥ min_pow_bits` (deters effortless Sybil),
- Their reputation.Tracker has room (default cap: 100 followed
  publishers),
- At least 2 of their hits appear in the subscriber's known-good
  Bloom filter OR at least 1 of their hits matches an
  anchor-publisher's index.

Publishers failing admission are logged to the "seen" set but
skipped; they can be re-evaluated on future crawl rounds.

### 3.3 Channel C — `sn_search peer_announce` gossip

Per `docs/06-bep-sn_search-draft.md` §peer_announce, every
`sn_search` peer sends a `peer_announce` after LTEP handshake.
Extend the schema (already reserved in v1 but not used):

```
peer_announce {
  "msg_type": 3,
  "v":        <int, ProtocolVersion>,
  "services": <uint64, services bitfield>,
  "pk":       <32B ed25519, optional>,   # this peer's publisher pubkey
                                         # iff services.BitLayerDPublisher set
  "endorsed": [ <32B>, ... ]             # optional, up to 10 pubkeys this peer
                                         # follows and vouches for
}
```

The `endorsed` field is new and small: up to 10 × 32B = 320 bytes
per handshake. Subscribers collect endorsements as weak signals:
a pubkey endorsed by 3+ peers with reputation > 0.5 each gets
auto-admitted to the known set (bypasses the Bloom-filter gate
in §3.2).

This is Plumtree-lite gossip of *publisher pubkey discovery*,
distinct from the `peer_announce.recent` push of records
(PROPOSAL §2.1 point 4). Both piggyback on the same message;
separating them keeps per-handshake overhead bounded.

### 3.4 Day-one UX

Within 30 seconds of first launch, a subscriber has:

- 5 PPMI fetches in flight (channel A).
- 3 BEP-51 samples in flight (channel B).
- 10-50 direct BT peers joined (the existing swarm logic).

Within 2 minutes:

- All 5 anchor index torrents have their metadata; piece downloads
  in progress.
- 20-100 candidate publisher pubkeys observed via channel B.
- `peer_announce` endorsements from 5-20 peers collected.

Within 15 minutes:

- 5 anchor indexes fully ingested → local index contains ~250 k
  records by default.
- Search works against those records immediately.
- RIBLT sync with connected peers (`sn_search` bit 9 enabled) pulls
  additional ~500 k records over the next hour from follow-through
  publishers.

The subscriber's "search something and get results" moment
happens within 2 minutes of first launch, not after a full DHT
BEP-44 walk of 20 seed indexers as in the current design.

### 3.5 What to do when everything fails

If channels A, B, C all return nothing (subscriber is behind a
hostile firewall, anchor operators are all offline, no swarm
peers): the subscriber falls back to direct HTTP fetch of a
bootstrap endpoint at `https://snet.bootstrap.example/v1/anchors`
which returns the current anchor pubkey list. This is the only
HTTPS touchpoint in the design and serves pubkeys only — never
records — so compromising it cannot poison the local index,
only delay bootstrap.

## 4. Conformance checklist for the v1.1 release

An implementation is Aggregate-compliant iff:

1. It publishes PPMI at `target = SHA1(pubkey || SHA256("snet.index"))`
   with commit binding the companion torrent's trailer fingerprint.
2. Its companion torrents use the §1 B-tree layout with the magic
   `"SNAGG\0"` and trailer signature.
3. It implements RIBLT over `sn_search msg_types 4-8` per §2.
4. It advertises `services.BitSetReconciliation` (bit 9) when
   capable, and honors the gate for peers without it.
5. It runs the §3 three-channel bootstrap at first launch.
6. It ignores msg_types with unknown `services` bits rather than
   reject (the existing "unknown bits ignore" rule survives).
7. It retains read compatibility with the v1 per-keyword BEP-44
   items for ≥ 12 months (PROPOSAL §6 migration Phase 3 gate).
8. It gates every ingest with per-record ed25519 signature
   verification and hashcash-bit verification at D ≥ 20.

## 5. Things explicitly out of scope for v1.1

- Multi-writer companion indexes (Willow/Meadowcap-style capability
  chains). Slot: PROPOSAL §8.
- Append-only index updates (Hyperbee-style). Today: full rewrite
  per publish. Slot: PROPOSAL §9.4.
- Per-record reputation weights. Today: per-publisher only.
  Slot: PROPOSAL §9.6.
- OHTTP + PIR query privacy. Side channel; separate ship track.
- Dandelion++ publishing. Opt-in feature; separate ship track.

## 6. Tests required for v1.1 sign-off

Each is a failing test today, passing when spec is correctly
implemented. Names correspond to Go test functions under the
respective packages.

```
# §1 — B-tree layout
internal/companion/btree_test.go:
  TestBuildDeterministic
  TestPrefixQueryNarrowRange
  TestPrefixQueryEmpty
  TestTrailerSignatureBindsFingerprint
  TestTrailerRejectsMutatedLeaf
  TestOversizeKeywordRefused
  TestOversizeRecordRefused
  TestBranchingFactor256

# §2 — RIBLT set sync
internal/swarmsearch/riblt_test.go:
  TestConverge_Diff0
  TestConverge_Diff1
  TestConverge_Diff100
  TestConverge_Diff10000
  TestConverge_SymbolLimitExceeded
  TestRejectUnknownServicesBit
  TestRejectBadSignatureDuringIngest
  TestMaxSessionsPerConnection
  TestSyncNeedMissingRace

# §3 — bootstrap
internal/daemon/bootstrap_test.go:
  TestAnchorFetchPopulatesIndex
  TestBEP51CandidateFlow
  TestEndorsementGossipAdmission
  TestAdmissionPolicyRejectsLowPoW
  TestHTTPSFallbackOnlyOnTotalFailure
  TestReputationSeededFromAnchors

# Wire-compat (cross-cutting)
tests/torrent-test/wire_compat_test.go:
  TestVanillaPeerIgnoresAggregateBits
  TestOldSwartzNetReadsLegacyItems
  TestNewClientFallsBackToLegacy
```

## 7. Benchmarks required for regression-gating

```
internal/companion/btree_test.go:
  BenchmarkPrefixQuery_5M   # target: < 50ms for narrow prefix hit

internal/swarmsearch/riblt_test.go:
  BenchmarkConverge_NetworkScale  # 10k peer simulation, target: <5 kB/peer/hr

testbed/scenario_bootstrap:
  scenario: 1 publisher + 3 subscribers
  measure:  time-to-first-result, bytes-to-convergence
  gate:     TTFR < 2 min, B2C < 25 MB
```

## 8. Implementation order refinement

Refining PROPOSAL §11 with spec-backed concrete phases:

| Phase | Deliverable | Spec § | LoC est. | Open deps |
|---|---|---|---|---|
| P1.1 | `internal/companion/btree.go`: encode/decode pages | §1.3-1.6 | ~500 | — |
| P1.2 | `internal/companion/build.go` build path calls into btree | §1.8 | ~150 | P1.1 |
| P1.3 | `internal/companion/subscriber.go` prefix-query path | §1.7 | ~200 | P1.1, P1.2 |
| P2.1 | `internal/dhtindex/ppmi.go` schema + encode/decode | PROPOSAL §2.1 | ~200 | — |
| P2.2 | PPMI publisher glue in `publisher.go` | PROPOSAL §2.1 | ~150 | P2.1 |
| P2.3 | PPMI reader + BEP-44 get in `lookup.go` | PROPOSAL §2.1 | ~150 | P2.1 |
| P3.1 | `internal/swarmsearch/riblt.go` wrapping yangl1996/riblt | §2 | ~300 | — |
| P3.2 | sn_search msg_types 4-8 wire handlers | §2.2-2.8 | ~400 | P3.1 |
| P4.1 | `internal/daemon/bootstrap.go` three channels | §3 | ~400 | P2.3, P3.2 |
| P5.1 | Hashcash + double-hashed salt + misbehavior bumps | §1.5, §2.7 | ~200 | P2.1, P3.2 |
| P5.2 | HTTPS anchor fallback | §3.5 | ~100 | P4.1 |

Total: ~2750 LoC production + ~2000 LoC tests, matching
PROPOSAL §11's estimate. Phases run mostly in parallel — P1,
P2, P3 are independent until the bootstrap (P4) wires them
together.

---

*End of SPEC.md iteration 1. The design is now implementable:
byte layouts are specified, wire messages are typed, the
cold-start flow is a procedure. Remaining open items are
performance-tuning decisions (RIBLT symbol-budget defaults,
B-tree branching), not correctness questions.*
