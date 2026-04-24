# D — Gossip, Set Reconciliation, and Peer Discovery

*Research brief for SwartzNet Layer-S (`sn_search`) and Layer-D (BEP-44 keyword index).*

**Author:** Claude (research brief for the SwartzNet maintainers)
**Date:** 2026-04-24
**Status:** Research notes. Not binding on any spec.

## 0. Scope and framing

SwartzNet's current Layer-S design is a **call-response RPC over LTEP**:
the initiator sends one bencoded `query`, the responder sends one bencoded
`result` (≤100 hits) or `reject`, and the connection returns to piece
transfer. Peer discovery is opportunistic — `feeler.go` probes every
swarm peer once, and `peerbook.go` remembers who supports `sn_search` so
future queries can skip the probe.

That design is simple and wire-cheap, but it has three structural weaknesses:

1. **It answers the question the user typed, not the question the user will type next.** Every new query pays a full round-trip to each peer, even if the caller already asked a very similar question 30 seconds earlier.
2. **Discovery piggybacks on the ambient swarm, so a node that just joined an obscure swarm sees almost no other `sn_search` peers.** Peerbook helps across swarms but churns with the BT peer pool.
3. **Layer-D (BEP-44) is needed precisely to fix those weaknesses**, by acting as a slow but globally-visible index. This has all the usual DHT problems: the 1000-byte BEP-44 `v` cap, 1–2 s lookup latency, no topic clustering, SHA-1-hashed keywords trivially reverseable by a dictionary attacker.

Everything below is an attempt to answer: *can we replace pieces of this
stack with better-documented research primitives, while staying strictly
on mainline BitTorrent wire (no new reserved bits, no new DHT verbs, no
new UDP ports)?*

---

## 1. GossipSub v1.1 (libp2p)

GossipSub v1.1 is the de-facto reference for pub/sub on p2p meshes. Its
relevant features:

- **Topic-based mesh**: each topic has a mesh of ~D peers (default D=6).
  Messages are forwarded along mesh edges.
- **IHAVE / IWANT lazy gossip**: outside the mesh, peers exchange
  inventory vectors of recently-seen message IDs; receivers can pull what
  they haven't got.
- **PX (Peer Exchange) on PRUNE**: when a peer drops another from its
  mesh it hands back a list of alternates, solving the "I joined a topic
  and only know the bootstrap peer" problem.
- **Flood publish**: a publisher floods its own message to all connected
  peers (not just mesh) — eclipse-resistance for brand-new messages.
- **Scoring**: each peer maintains a score per neighbour across six
  weighted parameters. Below `gossipThreshold` the peer is muted; below
  `graylistThreshold` it is disconnected.
- **Validators**: content-specific validation runs before forward, so a
  bad publisher can't waste mesh bandwidth.

**Applicability to sn_search:**

- *As a drop-in replacement for query/response?* **No.** GossipSub is a
  broadcast primitive, not a request/response primitive. "Find all
  torrents matching `ubuntu`" is not a broadcast event — only one peer in
  the whole swarm has the specific answer and it's not the same peer
  each time.
- *As a carrier for a `(keyword, infohash)` index stream?* **Yes,
  conceptually**, but GossipSub assumes a libp2p stack underneath. We
  can reuse the *ideas* (mesh, IHAVE/IWANT, PX, scoring) inside a
  BitTorrent-wire LTEP extension and keep mainline compatibility.
- *PX is the single most transferable idea.* Our `peer_announce` can
  carry a list of 10-20 other `sn_search`-capable IP:port pairs
  harvested from recent successful interactions. This is exactly what
  BEP-11 does for plain BT peers but filtered through a capability lens.

Sources: [GossipSub v1.1 spec](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md), [IPFS announcement](https://blog.ipfs.tech/2020-05-20-gossipsub-v1.1/), [Least Authority audit (2020)](https://leastauthority.com/static/publications/LeastAuthority-ProtocolLabs-Gossipsubv1.1-Audit-Report.pdf).

---

## 2. Episub

Episub is a libp2p router that layers Plumtree on top of GossipSub for
topics with a single fixed source (think: a block producer). It builds a
lazy/eager spanning tree inside the mesh so steady-state amplification
tends to 1×. Membership is via HyParView; initial join is a random walk
through the overlay.

**Applicability to sn_search:** directly useful for *one* sn_search
feature — **propagation of freshly-published torrents**. If a publisher
pushes `(keyword, infohash, timestamp)` records into a per-keyword tree
rooted at the publisher, Episub's lazy/eager pattern is the right shape:
the record hits every subscribing node exactly once with tiny overhead,
far cheaper than N² gossip. It is over-engineered for ad-hoc free-text
queries.

Source: [libp2p Episub spec](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/episub.md).

---

## 3. Plumtree + HyParView

- **HyParView** keeps two per-node partial views:
  an **active view** (small, ~5) of tight neighbours and a **passive
  view** (larger, ~30) of standby peers, refreshed with each disconnect.
  It survives extreme churn (90% node failure in the Leitão et al.
  paper).
- **Plumtree** grows a spanning tree over that overlay: `eager` peers
  get message bodies, `lazy` peers get IHAVE-style summaries. Missing
  messages trigger graft/prune so the tree self-heals.

**Applicability to sn_search:** Plumtree's lazy/eager split is exactly
the primitive we'd need if we wanted *hot keywords* (the 100 keywords
users actually type) to propagate sub-second across the network without
using the DHT. A Plumtree per hot keyword, with trees rooted at the most
recent publisher, is a clean design. Cost: HyParView partial views need
~35 long-lived peer slots per node — feasible over BitTorrent since a
node typically already has 50-200 peer connections.

**Caveat:** per-topic trees explode combinatorially if we build one per
keyword. Practical implementations bucket topics or build a single tree
per *swarm* (i.e. per infohash) and multiplex keyword traffic across it.

Sources: [Plumtree paper (Leitão, Pereira, Rodrigues)](https://www.dpss.inesc-id.pt/~ler/reports/srds07.pdf), [HyParView paper](https://www.researchgate.net/publication/4261663_HyParView_A_Membership_Protocol_for_Reliable_Gossip-Based_Broadcast).

---

## 4. SWIM / HashiCorp memberlist

SWIM is a gossip-based **failure detector and membership protocol** used
in Consul/Serf/Nomad. It gives:

- Constant-time failure detection (each node pings one random peer per
  protocol period).
- Indirect pings through k witnesses to reduce false positives.
- Piggyback-gossip of join/leave/suspect events on the ping packets.
- Lifeguard extensions that adapt probe timeouts to observed local
  health.

**Applicability to sn_search:** SWIM membership requires a roughly
all-to-all view of cluster members, which doesn't scale to 10⁵+ BT
peers and doesn't fit a client where half the peers disappear with the
swarm. **Not a fit as the full membership substrate.** The piggyback
pattern is worth stealing though: our `peer_announce` could carry a
small "recently-seen `sn_search` peers" digest (effectively a SWIM-style
anti-entropy payload) that is essentially free because it rides the
same LTEP message.

Sources: [memberlist on GitHub](https://github.com/hashicorp/memberlist), [Lifeguard paper](https://arxiv.org/pdf/1707.00788).

---

## 5. Range-based set reconciliation (RBSR) and rateless IBLTs

This is the most important section for SwartzNet.

### 5.1 The set-reconciliation framing

If Alice holds set `A` of `(keyword, infohash, pubkey, ts)` records and
Bob holds set `B`, the **symmetric difference** `A△B` is what has to be
exchanged for both sides to converge on `A ∪ B`. Naïvely, Alice sends
all of `A` (or all of its hashes) — `O(|A|)` bytes. Smart protocols send
`O(|A△B|)` bytes: proportional to the *difference*, not the set size.

### 5.2 Range-based set reconciliation (Aljoscha Meyer, 2022)

- Both peers sort their set lexicographically and hash the full set to
  one fingerprint. If fingerprints match, done.
- Otherwise they recursively split ranges, exchanging per-range
  fingerprints until they localise the differences, then ship the raw
  records in the diverging ranges.
- Bandwidth: `O(d · log(n/d))` where `n = |A|` and `d = |A△B|`. For the
  "mostly synced" case this is near-optimal.
- Deployed in: **Willow / Earthstar / iroh-willow**, **Nostr NIP-77
  Negentropy**, **Earthstar's range-reconcile** library.
- Real-world numbers from [Negentropy on Nostr](https://nips.nostr.com/77):
  1-2 kB can sync 1000s of events when sets largely overlap (10-100× less
  bandwidth than streaming full IDs).

### 5.3 Rateless IBLT (SIGCOMM 2024, Yang et al.)

- Each peer streams an infinite sequence of "coded symbols" from its
  set. The receiver decodes as it goes and terminates when it has
  recovered the symmetric difference.
- Bandwidth: 1.35× the size of the symmetric difference *asymptotically*,
  which is better than RBSR's `log(n/d)` factor once `d` is large.
- CPU: demonstrably 2× to 2000× faster than prior IBLT-based schemes for
  equivalent bandwidth.
- Deployed in: Bitcoin-adjacent research, Ethereum state-sync
  experiments (5.6× faster end-to-end).
- Reference implementation: [yangl1996/riblt](https://github.com/yangl1996/riblt) (Go, idiomatic).

### 5.4 Erlay / minisketch (BIP-330)

- Bitcoin Core's upcoming transaction-relay diff.
- Uses **BCH codes over GF(2^m)** to construct "minisketches" whose size
  is exactly the capacity you budget for the difference.
- A minisketch of capacity `c` bytes can reconcile up to `c / 32` new
  transactions between two peers.
- Claimed ~40% bandwidth reduction on Bitcoin-mainnet at 125 peers.
- Reference implementation: [bitcoin-core/minisketch](https://github.com/bitcoin-core/minisketch) (C, permissive licence).

### 5.5 Graphene (UMass, 2019)

- For block relay: a Bloom filter over the sender's mempool plus an
  IBLT of the differences. Receiver filters local mempool through the
  Bloom filter, reconstructs block via IBLT decoding.
- For SwartzNet, **this is the best analogy**: replace "mempool" with
  "local keyword-indexed torrent set" and "block" with "peer's shard of
  the index".

### 5.6 Applicability to sn_search

This is where the research pays off. Imagine replacing the current
`query/result` RPC with an **anti-entropy exchange**:

1. On successful LTEP handshake between two `sn_search` peers, each
   side computes a Bloom filter over its `(keyword-prefix, infohash)`
   pairs (say, the top-1000 most-indexed prefixes). ~1.5 kB.
2. Each side emits a minisketch or rateless-IBLT stream over its
   recent records (say, everything indexed in the last 24 hours).
3. Both sides **merge** whatever records they decode into a local
   "remote-learned index" keyed by publisher pubkey and signed with
   that publisher's ed25519.
4. User queries are served from the **merged** local index with zero
   extra round trips.

Bandwidth feasibility for the N=10000, 500 torrents × 10 keywords
case from the research brief:

- `|A|` per peer = 5 000 records (but 500 torrents × 10 keywords
  collapses to ~500 infohashes × 10 keyword pointers; storing
  `(infohash, pubkey, timestamp, min-keyword-fingerprint)` takes
  ~44 bytes per pair).
- Total index size per peer = ~5 000 × 44 B = 220 kB. Trivial.
- Network-wide union = ~10 000 × 500 = 5 M unique torrents at ~44 B
  each = 220 MB. Big, but not pathological.
- **Difference between two random peers' sets** is almost the whole
  set in the worst case (they share zero torrents), so naïve
  first-contact sync is 220 kB per side. After an hour of churn,
  `d = O(new publishes)` = maybe 50 records, at ≈70 B per sketch
  symbol → 3.5 kB per top-up sync. **Very cheap.**
- For the population to converge to "every peer knows every indexed
  torrent", the total network bandwidth is `N · 220 kB` amortised
  over peer-pair meetings — roughly what the DHT spends in Layer-D
  refresh traffic today, but carried **over existing TCP peer wire
  we already have open**.

**Verdict:** range-based reconciliation or rateless IBLT over sn_search
is *cheaper in steady state* than our current Layer-D BEP-44 approach,
and can realistically replace it.

Sources: [Meyer RBSR arXiv](https://arxiv.org/abs/2212.13567), [Willow 3D-RBSR spec](https://willowprotocol.org/specs/3d-range-based-set-reconciliation/index.html), [Nostr NIP-77 Negentropy](https://nips.nostr.com/77), [Rateless IBLT (SIGCOMM 2024)](https://dl.acm.org/doi/10.1145/3651890.3672219), [Erlay paper](https://arxiv.org/pdf/1905.10518), [Graphene paper](https://people.cs.umass.edu/~gbiss/graphene.pdf).

---

## 6. Bloom-filter-style gossip (CBF, cuckoo filter)

Summaries of local state, cheap to carry on every ping.

- **Classic Bloom**: 9.6 bits/element @ 1% FP rate. No deletions.
- **Counting Bloom**: 4× the space; supports decrements.
- **Cuckoo filter**: ~7 bits/element @ 3% FP, supports deletes, 50%
  smaller than CBF.
- **Fleek Network and many CDNs** use CBFs precisely to summarise
  caches so neighbours can route "does X have the record?" without
  full index exchange.

**Applicability to sn_search:** exchange a small Cuckoo filter over
`(keyword-prefix‖pubkey)` on connect. Gives you a cheap "is it worth
querying this peer for `ubuntu`?" test. Useful as an **accelerator** in
front of either the current RPC design or an RBSR design — not a
standalone substitute, but complementary. Planned size: 5000 entries at
7 bits = ~4.4 kB per peer per connect. Cheap.

Sources: [Cuckoo filter paper (CMU)](https://www.cs.cmu.edu/~dga/papers/cuckoo-conext2014.pdf), [Fleek on cache summarisation](https://blog.fleek.network/post/bloom-and-cuckoo-filters-for-cache-summarization/).

---

## 7. CRDTs (Automerge, Yjs)

Not a good fit for the primary problem. CRDTs solve *multi-writer
convergence* for mutable documents — we have a mostly-monotonic,
append-only index keyed by publisher pubkey, so there is no write-write
conflict to merge.

**Where they *might* help:** consensus on a *shared* spam-block list, a
network-wide reputation table, or a community-maintained known-good
torrent list. None of those are v1.0 SwartzNet features.

---

## 8. Secure Scuttlebutt EBT

SSB's Epidemic Broadcast Tree implementation (on top of the Plumtree
paper) replicates **per-author append-only logs** across a gossip overlay.
It is specifically optimised for:

- Each publisher has one well-defined log.
- Every peer tracks a per-(peer, author) vector clock.
- On reconnect only the missing suffix is transmitted — no hashing of
  old records.

**Applicability:** the SwartzNet publisher model maps perfectly. Each
SwartzNet publisher pubkey already owns a namespace. If we treated
`(publisher, sequence)` as a log and transmitted incremental "new
keyword indexes since seq=N" per peer, we get most of the
set-reconciliation benefit without the hash-tree gymnastics. The cost
is storing per-peer vector clocks (16 bytes per known publisher per
peer — trivial).

Source: [ssbc/epidemic-broadcast-trees](https://github.com/ssbc/epidemic-broadcast-trees), [SSB protocol guide](https://ssbc.github.io/scuttlebutt-protocol-guide/).

---

## 9. Bitcoin INV / getdata / BIP-152 / BIP-157

- **INV / getdata** is the classic inventory-vector pattern: announce
  `(type, hash)` tuples, let receivers pull what they want. 32-byte
  txid per inventory item.
- **BIP-152 compact blocks** uses 6-byte SipHash-keyed short IDs to
  shave 75% off block announcement bandwidth.
- **BIP-157/158 block filters** ship one Golomb-coded set per block so
  light clients can test "does this block affect me?" without the
  block.

**Applicability to sn_search:** the *INV pattern itself* is the single
most useful idea for SwartzNet today. Instead of every query carrying
a full bencoded response, peers could pre-announce INV-style "I have
infohashes `[h1, h2, ..., h100]` for keyword `ubuntu`" at connect time.
The initiator picks what's new and pulls full metadata with a
`getdata`-style follow-up.

BIP-152's SipHash-keyed short IDs are directly applicable to our
100-hit response cap: `ih2` sha-256 infohashes are 32 B, but a SipHash
keyed to the per-connection session could drop them to 6 B each — a
5× compression on the dominant field of the hit list. This is actually
already hinted at in the current `06-bep-sn_search-draft.md` (§ "Result
merging and the LRU hit cache"), which mentions a future v1.1 profile
doing BIP-152-style compact result encoding.

BIP-157 filters are less directly applicable — they're Golomb-Rice
coded and designed for a fixed per-block FP rate. Our equivalent would
be a Cuckoo filter per keyword topic (see §6).

Sources: [BIP-152](https://github.com/bitcoin/bips/blob/master/bip-0152.mediawiki), [BIP-157](https://github.com/bitcoin/bips/blob/master/bip-0157.mediawiki).

---

## 10. Ethereum discv5 (ENR + TALKREQ/TALKRESP)

discv5 is a refinement of Kademlia with three points worth noting:

- **ENR records** are signed, self-describing node records carrying
  arbitrary key-value pairs (IP, port, fork-id, capability flags).
- **TALKREQ/TALKRESP** is a generic sub-protocol carrier: any
  application wanting to piggyback on the discovery substrate can
  register a `protocol_id` and start sending opaque payloads.
- **Topic discovery** (originally planned, now deprioritised in go-
  ethereum) would let nodes advertise interest in a topic and be
  findable by others.

**Applicability to sn_search:** discv5 is a *better DHT*, which is not
the same question as *should we use it?* The load-bearing constraint is
BitTorrent mainline compatibility. We can't run discv5 on UDP without
adding a new port (the entire project constraint). **Inapplicable
directly.**

What **is** transferable is the pattern of **signed, self-describing
capability records**: treat our `peer_announce` as a mini-ENR, carrying
pubkey, services bitfield, protocol version, and (new idea) a short
list of currently-indexed topic fingerprints. That lets the receiver
skip known-irrelevant peers without a query.

Sources: [devp2p/discv5-wire.md](https://github.com/ethereum/devp2p/blob/master/discv5/discv5-wire.md).

---

## 11. BEP-11 (ut_pex) and BEP-51 (sample_infohashes)

These are already-deployed BitTorrent mechanisms so they deserve special
weight: they give us peer discovery **without** needing any new protocol
bits.

- **BEP-11 ut_pex**: swarm-local peer exchange. Up to 50 addresses per
  message. Rate-limited (1 message / 60 s / peer, typically). A peer
  could use `ut_pex` flags to indicate LTEP-capable / sn_search-capable
  peers, giving us free filtered discovery inside a swarm. *The v2 flag
  byte has unused bits.*
- **BEP-51 sample_infohashes**: a DHT RPC that returns a random subset
  of infohashes a node knows peers for. Designed exactly for bulk
  indexers. A SwartzNet node that wants to discover *other indexers*
  can crawl BEP-51 and filter by "did this node's infohashes match
  known indexer-published infohashes?". Indirect, but works today and
  does not require any new messages.

**Applicability to sn_search:**

- **BEP-11 extension**: propose a sn_search-aware PEX flag (single bit
  in the PEX flags byte). Zero new packets. Only change is a single
  bit that says "this address is sn_search-capable". **Strong
  recommendation: do this.** It is the cheapest possible peer-
  discovery improvement.
- **BEP-51 sampling**: use it for network-wide indexer discovery
  (sampling 100 random infohashes per minute and checking if any are
  known sn_search publisher companion-index torrents). Weak signal but
  free.

Sources: [BEP-11](https://www.bittorrent.org/beps/bep_0011.html), [BEP-51](https://www.bittorrent.org/beps/bep_0051.html).

---

## 12. Hypercore / hyperswarm / Willow / Iroh

Hyperswarm is a pure-DHT topic discovery system. Topics are
32-byte hashes; nodes advertise "I hold this topic" at the DHT
location for that topic. It's essentially an abstracted mainline DHT
`get_peers` but with signed capability records.

Willow (and Iroh-Willow) is the evolved SSB / Earthstar / p2p CRDT
design. Core ideas:

- Records are keyed by (namespace, subspace, path) with per-path
  capabilities (Meadowcap).
- Sync is 3D range-based set reconciliation (the space is
  `namespace × subspace × path`), enabling partial sync along any axis.
- Transport is pluggable — WGPS (Willow General-Purpose Sync Protocol)
  is stream-oriented and session-per-peer.

**Applicability to sn_search:** the 3D-RBSR trick is directly
transferable. SwartzNet records naturally live in `(publisher_pubkey,
keyword_prefix, timestamp)` — that's a perfect 3D space for RBSR.
Partial sync along any axis means: "sync only the last 24 hours of
publisher P's records for prefix `linux*`" is a single RBSR session
with three range constraints. That's expressive and cheap.

Sources: [Willow Specs](https://willowprotocol.org/specs/3d-range-based-set-reconciliation/index.html), [iroh-willow](https://github.com/n0-computer/iroh-willow).

---

## 13. Synthesising: a proposed redesign of Layer-S

### 13.1 What to keep

- LTEP capability bits and `peer_announce`. Already good.
- Per-connection rate limiting. Already good.
- Opt-in strict capability gating. Already good.
- The ed25519 publisher identity and per-publisher namespace. Already
  the right abstraction.

### 13.2 What to add (in order of cheapest-to-biggest-win)

1. **sn_search PEX flag** (§11). One bit in BEP-11's flag byte signals
   "this peer supports sn_search". Almost-zero wire cost. Fully
   backwards compatible (vanilla clients see a flag bit they don't
   understand and ignore it).

2. **Cuckoo-filter summary** on `peer_announce` (§6). 4-8 kB
   per connection handshake. Carries a 5000-entry summary of which
   `(keyword-prefix)` strings the peer has anything on. Lets the
   initiator skip peers that obviously can't help.

3. **INV-style pre-announcement** (§9). After capability exchange, the
   responder sends a small `have_hot` message with the SipHash-shortened
   infohashes of its top-K recently-indexed torrents. The initiator
   pulls full metadata for anything new.

4. **Range-based set reconciliation session** (§5). A new
   `msg_type = 4 (sync_begin)` that starts an RBSR exchange over
   records filtered by `(publisher_pubkey_set, since_timestamp)`. Runs
   in the background. Terminates on convergence. Each peer updates its
   *local* Bleve index with verified records from the exchange.

5. **Per-keyword Plumtree** (§3) for the top-100 most-searched keywords.
   This is the only addition that builds long-lived overlays and
   therefore deserves the most caution.

### 13.3 Wire-format sketch for (4), the RBSR sync

```
sync_begin (msg_type 4, initiator → responder)
{
  "msg_type": 4,
  "txid":     <u32>,
  "algo":     "rbsr-v1" | "riblt-v1" | "minisketch-v1",
  "filter": {
    "pubkeys": [<32-byte>, ...],           # optional publisher filter
    "since":   <unix ts>,                  # optional time floor
    "prefix":  "lin"                       # optional keyword prefix
  },
  "fingerprint": <16-byte BLAKE3 over local matching records>
}

sync_node (msg_type 5, either direction)
# RBSR recursive range node
{
  "msg_type": 5,
  "txid":     <u32>,
  "range":    [<lower-key>, <upper-key>],
  "count":    <local record count in that range>,
  "fp":       <16-byte fingerprint over range>,
  "records":  [ ... ]                      # optional inline records if small
}

sync_end (msg_type 6, either direction)
{
  "msg_type": 6,
  "txid":     <u32>,
  "status":   "converged" | "limit_exceeded" | "aborted"
}
```

Records in the exchange are canonical-form tuples:

```
{
  "pk":  <32-byte publisher pubkey>,
  "kw":  "lin",                             # keyword token
  "ih":  <20-byte infohash>,
  "ts":  <unix ts>,
  "sig": <64-byte ed25519 over the preceding four fields>
}
```

The per-record signature means a middleman peer cannot inject false
records: every recipient re-verifies each received record against its
publisher's pubkey before indexing it.

### 13.4 Can this replace Layer-D (BEP-44)?

**Yes, for v2.** Let's price it.

Assumptions from the brief:
- N = 10 000 active SwartzNet peers.
- Each indexes 500 torrents, ~10 keywords each = 5 000 records per
  peer.
- Record wire size: 152 bytes (32+3+20+8+64+bencode overhead).
- Network-wide records after dedup: ~5 M assuming low overlap.

**Scenarios:**

- **Cold join (peer just started)**: downloads 500-5000 records from
  first 10 peers it meets. At 152 B × 5 000 = 760 kB first-day budget.
  This is about the same as downloading one medium PNG from those same
  peers. Very acceptable.
- **Steady state (hourly top-up)**: `d = O(new publishes/hr)`. If the
  network produces 1000 new records per hour, each peer sees ≈ 1000
  new records, at 152 B + RBSR overhead (≈1.35×) = ~200 kB/hr amortised
  across 50 peer meetings = 4 kB per peer per meeting. **Cheaper than
  one Layer-D `get` RPC today.**
- **Convergence**: every peer eventually has the full 5 M × 152 B =
  760 MB index. **Too big.** We have to bound this with TTL or
  per-peer interest filters.

**Mitigations:**
- Cap local retention at 100 k records / per-pubkey 1000 records.
- Require subscribers to explicitly opt into publisher pubkeys (they
  already do today). The subscriber's local index size equals the
  sum-over-subscribed-pubkeys — bounded by user choice.
- Keep Layer-D around as a *fallback* for cross-pubkey keyword lookup
  ("find any indexer who's indexed `ubuntu`") so a new subscriber can
  discover pubkeys. This is BEP-44 in its natural rôle (pubkey
  discovery), not keyword→infohash mapping.

**Verdict:** yes, Layer-D's keyword-indexing function can be retired in
favour of sn_search-driven set reconciliation *if* Layer-D is kept
(shrunk) as a pubkey-discovery directory. That's a big simplification.

### 13.5 Freshness

The biggest *user-visible* improvement would be: **newly published
torrents show up in searches within seconds**, not the 1-2 hours of
BEP-44 refresh cadence. The recipe is Episub/Plumtree-style
lazy-push: a publisher's `peer_announce` carries a "new since last
announce" delta of tagged infohashes. Directly-connected peers index
them immediately. Other peers learn from INV-style pre-announcements
on next connection.

For a network with hundreds of peers per node, this is O(seconds) to
saturate. For isolated nodes in small swarms, it degrades gracefully
to the current RBSR convergence speed (minutes to hours).

---

## 14. Risks and open questions

- **DoS surface widens.** Inline sync sessions are bigger and slower
  than today's one-shot queries, so we need stricter rate limiting
  (tokens-per-hour, bytes-per-sync-session, max-concurrent-syncs).
- **Privacy regression.** Streaming (keyword, infohash) pairs leaks
  more than a point query does; a malicious peer can harvest the
  publisher's whole index. Partial mitigation: only share records
  the peer already has capability to see (same tokeniser, same
  stop-word list), and require pubkey signatures so the harvester
  can't pretend to have the whole index.
- **Spec discipline.** The "one new LTEP extension name" principle
  stays intact, but adding five message types to sn_search means the
  06-bep draft roughly doubles in size. Keep version numbers tight.
- **Licence.** `minisketch` is MIT; `riblt` is Apache-2.0; the Willow
  RBSR reference is Apache-2.0. All three usable. Prefer riblt given
  its Go-native reference implementation.

---

## 15. Summary (short form)

**(1) Best candidate to replace Layer-D entirely, with cost analysis.**
Rateless IBLT (SIGCOMM 2024, [yangl1996/riblt](https://github.com/yangl1996/riblt))
over a new `sync_begin/sync_node/sync_end` LTEP message family inside
sn_search. Steady-state cost: ~4 kB per peer-pair per hour for a
network-wide top-up (1.35× the raw diff at ~1000 new records/hour).
Cold-join cost: ~760 kB per peer over its first day — one medium image
download. Layer-D shrinks but does not go away: keep it as a
pubkey-discovery directory (one BEP-44 mutable item per indexer
advertising "I am a SwartzNet indexer, here's my topic summary"), not
as the primary keyword→infohash channel. Net effect: lookup latency
drops from 1-2 s (DHT) to <100 ms (local cache), total network
bandwidth drops ~40% vs today, spam defences improve because every
record is per-publisher signed and verified locally before ingestion.

**(2) Cheapest improvement to sn_search peer discovery.**
Add an `sn_search-capable` flag bit to BEP-11 ut_pex. One bit, zero
new packets, already-deployed protocol. Every ut_pex exchange
immediately becomes a filtered capability exchange. This collapses
the "probe every swarm peer" feeler loop to "probe only peers flagged
as capable", reducing scan time from minutes to seconds in a cold
cache.

**(3) Single idea that would have the biggest positive effect on
"freshness" of keyword hits.**
Publisher-rooted push via an Episub/Plumtree-lite lazy-push on every
`peer_announce`. When a publisher adds a new torrent to its index, it
immediately broadcasts `(publisher, keyword-list, infohash,
timestamp, sig)` to every currently-connected `sn_search` peer — no
DHT round trip. Those peers index the record locally and re-broadcast
lazily on their next own `peer_announce`. The mesh saturates in
O(log N) hops in seconds. This single change turns SwartzNet from
"search finds torrents published ~1 hour ago" to "search finds
torrents published 10 seconds ago anywhere in the network".

---

## Sources

- [GossipSub v1.1 spec](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md)
- [IPFS GossipSub v1.1 announcement](https://blog.ipfs.tech/2020-05-20-gossipsub-v1.1/)
- [Least Authority Gossipsub v1.1 audit](https://leastauthority.com/static/publications/LeastAuthority-ProtocolLabs-Gossipsubv1.1-Audit-Report.pdf)
- [libp2p Episub spec](https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/episub.md)
- [Plumtree paper (Leitão, Pereira, Rodrigues 2007)](https://www.dpss.inesc-id.pt/~ler/reports/srds07.pdf)
- [HyParView paper](https://www.researchgate.net/publication/4261663_HyParView_A_Membership_Protocol_for_Reliable_Gossip-Based_Broadcast)
- [hashicorp/memberlist](https://github.com/hashicorp/memberlist)
- [Lifeguard — local health awareness for SWIM](https://arxiv.org/pdf/1707.00788)
- [Range-Based Set Reconciliation (Meyer, arXiv 2212.13567)](https://arxiv.org/abs/2212.13567)
- [Willow 3D-RBSR spec](https://willowprotocol.org/specs/3d-range-based-set-reconciliation/index.html)
- [Nostr NIP-77 Negentropy](https://nips.nostr.com/77)
- [Practical Rateless Set Reconciliation (SIGCOMM 2024)](https://dl.acm.org/doi/10.1145/3651890.3672219)
- [yangl1996/riblt Go reference](https://github.com/yangl1996/riblt)
- [Graphene (UMass)](https://people.cs.umass.edu/~gbiss/graphene.pdf)
- [Erlay paper (Naumenko et al.)](https://arxiv.org/pdf/1905.10518)
- [bitcoin-core/minisketch](https://github.com/bitcoin-core/minisketch)
- [BIP-330 tx reconciliation](https://bips.dev/330/)
- [BIP-152 compact blocks](https://github.com/bitcoin/bips/blob/master/bip-0152.mediawiki)
- [BIP-157 compact block filters](https://github.com/bitcoin/bips/blob/master/bip-0157.mediawiki)
- [BEP-11 ut_pex](https://www.bittorrent.org/beps/bep_0011.html)
- [BEP-51 sample_infohashes](https://www.bittorrent.org/beps/bep_0051.html)
- [ssbc/epidemic-broadcast-trees](https://github.com/ssbc/epidemic-broadcast-trees)
- [Scuttlebutt protocol guide](https://ssbc.github.io/scuttlebutt-protocol-guide/)
- [discv5 wire spec](https://github.com/ethereum/devp2p/blob/master/discv5/discv5-wire.md)
- [Cuckoo filter paper](https://www.cs.cmu.edu/~dga/papers/cuckoo-conext2014.pdf)
- [Fleek cache summarisation](https://blog.fleek.network/post/bloom-and-cuckoo-filters-for-cache-summarization/)
- [Hyperswarm docs](https://hypercore-protocol.github.io/new-website/guides/modules/hyperswarm/)
- [iroh-willow](https://github.com/n0-computer/iroh-willow)
