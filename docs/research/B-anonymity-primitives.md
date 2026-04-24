# B. Anonymity Primitives for SwartzNet — 2026 Revisit

Revisit of the §9 decision in `docs/05-integration-design.md` that placed network-level
anonymity out of scope. That call was correct for the "ship F1+F2 first" baseline: every
dollar of complexity that moves off the mainline DHT threatens the project's one
load-bearing invariant (BEP-3/5/9/10/44 only; no new port; no new verb; no new reserved
bit). The question for 2026 is narrower: **which optional, opt-in privacy layerings have
become cheap enough that a user who wants them can enable them without breaking
mainline compatibility, and without forcing non-anonymous users to pay any cost?**

This document surveys ten primitives through the same lens and closes with a concrete
mapping to SwartzNet's three search layers (L, S, D) and the publisher-deanonymization
gap flagged as open question #6 of §13.

The primitives fall into three families that matter for the integration:
- **Full-network substrates** (Tor v3, I2P, Lokinet, Nym) — move every packet somewhere else.
- **Cryptographic request-hiding** (PIR, OHTTP) — keep the packets on the open internet
  but hide the contents.
- **Topology-level obfuscation** (Dandelion++, GNUnet R5N, obfs4) — small tweaks to how
  existing packets move that buy specific, bounded privacy properties cheaply.

Only the second and third families can be made opt-in without violating mainline
compat. The first family is worth covering because many users already run SwartzNet
behind one of them, and design choices we make today can either help or hurt that.

---

## 1. Tor v3 onion services

**Mechanism.** Tor v3 (rend-spec-v3, 2017) replaces the 16-char onion addresses of v2
with a 56-character base32 of a full ed25519 public key, using SHA3 and Curve25519
throughout. A hidden service picks three *introduction points* from the consensus,
publishes a signed *service descriptor* to six *HSDirs* (hash-ring positions of the
time-period-blinded public key), and waits. A client builds a three-hop circuit to an
HSDir, fetches the descriptor, builds another three-hop circuit to a *rendezvous
point*, tells an introduction point to forward the rendezvous cookie, and the service
builds its own three-hop circuit to meet. End-to-end path: **six Tor hops between
client and service, plus the rendezvous point.** Circuits are 512-byte Tor cells over
TLS; typical end-to-end latency from Tor Metrics `onionperf-latencies.html` is
1.5–5 s time-to-first-byte, download throughput on a good day a few MB/s.

**Single-hop "Single Onion Services"** (torrc `HiddenServiceSingleHopMode 1`) drop the
service side's three hops — the service reveals its IP to rendezvous points in
exchange for ~50% latency reduction. Useful for a server-side indexer that is *not*
trying to hide its own IP (it may be publicly known or even billed to a domain) but
still wants to offer clients an onion-addressed endpoint. Not useful for a regular
SwartzNet node that does want to hide.

**Cost/latency for a BT+search daemon.** BitTorrent over Tor is a known-bad combination
(the Tor project's FAQ explicitly says so), because piece-fetch RTT dominance pins
throughput at a few hundred KB/s and because a misconfigured client leaks UDP outside
the circuit. But a *search daemon* has very different traffic: tiny queries, modest
result blobs, can tolerate a 2–5 s round-trip. Running only Layer D and Layer S over
Tor while keeping piece transfer on the clearnet is a plausible opt-in split, though
it requires care: Layer S piggybacks on the *existing* TCP piece-transfer socket
(BEP-10 is an extension on that socket), so there is no separate socket to route
through Tor. A Tor-option would force SwartzNet to open a second, onion-addressed
BitTorrent socket for peers that want search-only links — that is exactly the kind of
extra overlay we promised not to build.

**Mainline compat.** Neutral. `torrent` speakers don't care whether the underlying
socket is clearnet, Tor, or a pigeon. But advertising an onion address requires an
*alternate peer encoding* beyond the 6-byte compact IPv4/IPv6 forms in BEP-23 — today
that means using the BEP-7 / BEP-32 extended compact peers or shipping hostnames in
LTEP. Non-blocking but nontrivial.

**Integration score.** For Layer L: irrelevant (local). For Layer D (DHT BEP-44):
possible via `SOCKSProxy` on the anacrolix DHT library, with the understanding that
the DHT is UDP and Tor requires TCP — this means tunneling DHT through an onion-proxy
like `onioncat` or operating only against a set of DHT-over-TCP bridges (which do not
exist at scale). **Layer D over Tor is not practical in 2026.** For Layer S
(peer-wire search): possible only if the underlying BitTorrent connection is itself
Tor-carried, which forces all traffic including pieces through the same three hops.

---

## 2. I2P

**Mechanism.** I2P is a packet-switched overlay with *garlic routing* — multiple
messages in a single onion layer, with per-hop symmetric encryption. Clients build
*tunnels* (two unidirectional three-hop tunnels per destination, inbound + outbound),
and the Kademlia-like *NetDB* (floodfill peers) publishes *leasesets* mapping long-lived
destination pubkeys to currently-active inbound tunnels. I2PSnark, bundled with the
Java I2P router, is the reference BitTorrent client; a full DHT variant
(`I2PSnark DHT`, a Kademlia implemented over I2P destinations) has been stable since
I2P 0.9.2 (2012).

**Latency/bandwidth.** Round-trip 500 ms–2 s for the first packet of a fresh
destination (tunnel build is slow); steady-state throughput often a few hundred KB/s
per tunnel, higher if you raise tunnel-quantity. Default three-hop tunnel adds 6 hops
of per-packet crypto round-trip.

**SwartzNet fit.** I2PSnark's DHT is *its own* DHT, not mainline — an I2P-resident
client cannot participate in the mainline 10M-node DHT SwartzNet's Layer D relies on.
So "run SwartzNet over I2P" is really "run a separate SwartzNet installation inside
I2P's walled-garden DHT," which loses the network-effect argument that §7 of
`integration-design.md` calls the single most important call in the design. The only
reasonable I2P integration story is the one Tor offers: route queries but not torrents.
That exposes a mismatch — Layer D targets the *mainline* DHT, which I2P cannot reach.

**Mainline compat.** Fine at the wire level; same caveat as Tor for DHT-over-TCP.

**Verdict.** Interesting for users who already run I2P, but not worth building in as a
first-class option. Document how to point a SwartzNet instance at a SOCKS proxy
terminating on an I2P outproxy and let power users sort it out.

---

## 3. Lokinet / Oxen

**Mechanism.** Lokinet (LLARP protocol) is a low-latency onion router implemented on
top of the Oxen service-node network. Service nodes stake OXEN and are incentivized to
stay online. Clients build 3-hop paths (`3/8` nested frames, BLAKE2b-keyed symmetric
crypto) across service nodes. Unlike Tor, paths are IP-level — Lokinet routes any
protocol (TCP, UDP, ICMP), which means **it is the only major onion network that can
carry mainline UDP DHT traffic natively.**

**Cost/latency.** Typical 200–500 ms extra latency, throughput limited by the weakest
hop but routinely sustains ~10 MB/s per path thanks to the paid-relay economics.

**SwartzNet fit.** Of the three onion overlays, Lokinet is the best fit because it
preserves the UDP-based mainline-DHT transport. However:

1. Active network is small (a few thousand service nodes vs. Tor's ~8,000 guards and
   relays).
2. Participation requires trusting the Oxen chain and a for-profit staking system that
   has had its share of governance drama.
3. The UDP-through-onion property helps Layer D, but exposes the same IP-to-pubkey
   linkability to the exit node that BEP-44 publishes to DHT nodes today.

**Mainline compat.** Good — Lokinet exposes a TUN interface, packets look like
ordinary BitTorrent to the kernel.

**Verdict.** Worth documenting as the cleanest "run SwartzNet anonymously" option for
users who want to hide their IP from DHT nodes and peers in one step. Not worth
building into the daemon.

---

## 4. Nym mixnet

**Mechanism.** Nym is a Loopix-style continuous-time mixnet (Piotrowska, Danezis et al.,
USENIX Security 2017). Packets are Sphinx-format onion layers, each hop adds an
independent Poisson-distributed delay (currently ~50 ms mean per hop at three hops);
each mix node injects *cover traffic* at its own Poisson rate so that an observer
cannot distinguish a real client packet from noise. A blockchain-based reward layer
(NYM token) incentivizes running mix nodes honestly.

**Cost/latency.** 500–800 ms end-to-end for a packet (Nym's current defaults), plus
cover-traffic bandwidth overhead of a few KB/s per active client — dominant
consideration on mobile. The Sphinx per-hop crypto adds ~0.32 ms of CPU, negligible.

**SwartzNet fit.** Nym is designed for request/response patterns: exactly Layer D and
Layer S traffic. Bandwidth of a BEP-44 get is ~500 bytes in, ~1 KB out — fits trivially
in a Sphinx packet (~2 KB maximum, per the Nym white paper). **Layer D BEP-44 get
tunneled through Nym is an excellent mechanical fit for the primitive.** Cost: ~500 ms
added per query, plus a sustained ~5 KB/s of cover traffic if you want the anonymity
guarantees.

**Mainline compat.** Nym currently ships SOCKS5 gateways; if you pipe UDP DHT through
a SOCKS5 UDP associate, the BEP-44 bytes on the wire are indistinguishable from a
non-Nym client. The DHT sees a Nym exit node as the source — a perfectly fine
mainline DHT peer.

**Verdict.** The highest-leverage full-network substrate for SwartzNet's threat model
(query privacy). Mentioned as a candidate in the §5 integration recommendations below.

---

## 5. HORNET, Karaoke, Vuvuzela, Stadium

Research-grade mix designs chosen because their latency profiles suit a search
workload (small query, slightly larger result).

**HORNET** (Chen, Asoni, Barrera, Danezis, Perrig — CCS 2015) is network-layer onion
routing with per-packet, keyed symmetric-only forwarding. No per-flow state on
intermediate routers. Line-rate processing (93 Gb/s on commodity hardware in the
paper). Requires routers to cooperate — it's a clean-slate design for SCION-style
networks and has no deployed open network. Practically unusable in 2026.

**Vuvuzela** (van den Hooff, Lazar, Zaharia, Zeldovich — SOSP 2015) and **Stadium**
(Tyagi, Gilad, Leung, Zaharia, Zeldovich — SOSP 2017) are dead-drop messaging
systems. They achieve differential privacy against passive global adversaries by
adding noise messages. Vuvuzela is centralized (one chain); Stadium shards across
many chains. Latency 10–40 s for Vuvuzela, worse for Stadium at scale.

**Karaoke** (Lazar, Gilad, Zeldovich — OSDI 2018) improves on both with efficient
Bloom-filter noise verification. Published numbers: **6.8 s latency for 2 M users**,
~10× better than Stadium. Still a round-based system: messages only ship at round
boundaries.

**SwartzNet fit.** Search queries can tolerate a few seconds, so latency is not the
blocker. The blockers are:
- None of these have deployed open networks. Vuvuzela's public deployment is gone;
  Karaoke is a research artifact. Running one means operating the infrastructure
  yourself.
- All require trusting a fixed (small) set of servers not to collude. This is a strong
  assumption compared to Tor's "any guard" model.
- Round-based messaging means queries queue for up to a round (seconds).

**Verdict.** None of these are deployable as a dependency in 2026. Note Karaoke's
design as a reference if SwartzNet ever needs its *own* differential-privacy layer
(unlikely before v2).

---

## 6. Private Information Retrieval (PIR)

This is the single most important recent development for SwartzNet's Layer D privacy
problem. The 2022–2024 cohort of single-server PIR schemes made it plausible to hide
*which keyword* a client is querying from the server answering it.

**Single-server PIR.** Client wants entry `i` from an `n`-entry database on one
server; wants server to learn nothing about `i`. Historically this required
fully-homomorphic encryption with server work near the size of the database per query —
prohibitive.

The 2022–2023 breakthroughs:
- **Spiral** (Menon, Wu — IEEE S&P 2022): composition of Regev and GSW encryption
  gives a 4.5× query-size, 1.5× response-size, 2× throughput improvement over OnionPIR.
  SpiralStreamPack: **1.9 GB/s throughput, 0.81 rate** for >1 M-record DBs.
- **SimplePIR / DoublePIR** (Henzinger, Hong, Corrigan-Gibbs, Meiklejohn, Vaikuntanathan
  — USENIX Security 2023): `10 GB/s/core` server throughput (memory-bandwidth bound);
  DoublePIR trades per-query communication (345 KB) for a small 16 MB hint and gets
  `7.4 GB/s/core`. **This is 30× faster than any previous single-server PIR.** Online
  communication for an 8 GB / 2^36 entry DB: 756 KB.
- **FrodoPIR** (Davidson, Pestana, Celi — PETS 2023): stateful, LWE-only (no ring
  structure), designed for flexibility and production deployment. 1 M × 1 KB DB:
  <1 s query response, ~3.6× blow-up. ~$1 per 100 k queries on commodity cloud.
- **Pantheon** (Ahmad et al. — VLDB 2023) and **ChalametPIR / "Call Me By My Name"**
  (Celi & Davidson — CCS 2024): keyword-PIR variants. ChalametPIR reports 6–11× runtime
  and 3.75–11.4× financial-cost improvements over prior keyword-PIR approaches.

**DPF-based multi-server PIR.** Distributed Point Functions (Gilboa & Ishai 2014,
refined 2022 by Boyle, Gilboa, Ishai) let a client split a "retrieve index `i`" query
into two shares given to two non-colluding servers. Each server evaluates the DPF
across its copy of the database and XORs matching records. Checklist (PETS 2021) is
the canonical deployed DPF-PIR. Requires 2+ non-colluding indexers; in SwartzNet
terms, this maps to "pick two well-known indexer pubkeys and assume they do not
collude."

**Could SwartzNet use PIR against the well-known indexer pubkey set (§4.3)?**

The §4.3 design posits ~20 seed indexer pubkeys, each publishing BEP-44 entries
keyed by `SHA1(pubkey || keyword)`. The "database" a client wants to privately query
is the *keyword directory* one of these indexers maintains. Characteristics:
- Size: even if each of the top-1M English-language keywords has an entry per
  indexer, that's ~1 M × ~4 KB = ~4 GB per indexer. Well within FrodoPIR's proven
  scale.
- Updates: indexers would republish BEP-44 items continuously; a PIR front-end would
  need to regenerate its preprocessing (hint) when the DB changes. FrodoPIR's 16 MB
  hint is per-DB-version — regenerating on hour boundaries is fine.
- Query: a FrodoPIR query for a 1 M × 1 KB DB is a few hundred KB uplink, <1 s
  response. Acceptable for a search query.
- Keyword-PIR (ChalametPIR): even better fit — the user wants a *keyword*, not an
  *index*. No client-side hashing tables to maintain.

So PIR is **tractable in 2026 bytes and CPU** for SwartzNet's Layer D if we reframe
the indexer from "a BEP-44 publisher" into "a PIR-answering HTTP service." The catch:
that service no longer lives on the mainline DHT. It lives at an HTTPS endpoint the
indexer runs.

This is the key architectural pivot. See §11 for how to make this opt-in without
abandoning the current DHT path.

---

## 7. Oblivious HTTP (RFC 9458)

**Mechanism.** RFC 9458 (January 2024, Thomson & Wood of Mozilla/Cloudflare) defines a
three-party protocol: *client* encrypts an HTTP request to the *gateway* (the target
origin server), wraps it in HPKE, and sends it through a *relay*. The relay sees
only the ciphertext and the gateway's IP; the gateway sees only the plaintext
request and the relay's IP. Neither learns the other half. Live deployments as of
2024 include Apple Private Cloud Compute, Mozilla Firefox telemetry (via Fastly
relay), Google Safe Browsing, and Divvi Up.

**Cost/latency.** One extra hop and one HPKE encrypt/decrypt per request. On a typical
home → relay → origin path, 50–200 ms. Payload overhead: ~160 bytes HPKE header.
Drastically cheaper than Tor (one hop vs. three) at the cost of requiring the relay
and gateway not to collude.

**SwartzNet fit.** OHTTP maps directly to the PIR-indexer architecture above. If
Layer D is offered *optionally* as a PIR-over-HTTPS service from each well-known
indexer pubkey, a client can route that HTTPS call through any public OHTTP relay.
Result: the indexer learns "someone sent a PIR query" (and from PIR, nothing about
which keyword); the relay learns "someone is talking to this indexer" (and nothing
about the query). **No single party learns both "who" and "what."**

OHTTP is also useful *without* PIR: a client can route Layer S-style search calls to
indexers that offer an HTTP endpoint as an alternative to sn_search LTEP, and get IP
hiding at very low cost.

**Mainline compat.** Perfect. OHTTP is a pure application-layer primitive on 443. A
vanilla BT client sees nothing.

**Verdict.** The cheapest IP-hiding primitive available for any HTTP-shaped traffic.
Pair with PIR for the full "hide who + hide what" story on Layer D queries.

---

## 8. GNUnet CADET + R5N

**Mechanism.** R5N (Evans & Polot, IEEE P2P 2011, with a refresh in Schanzenbach's
IETF draft `draft-schanzen-r5n`) is GNUnet's DHT routing algorithm. For the first
`L2NSE` (log₂-network-size-estimate) hops of a get or put, the packet takes a *random
walk*, after which it switches to classical XOR-based Kademlia routing. The random
walk blurs the mapping between a queryer's source position and the target key: an
observer that sees only part of the path cannot easily tell whether the local node
is the originator or a forwarder. A Bloom filter of visited peer IDs rides along to
prevent routing loops.

CADET is GNUnet's transport, a mesh of encrypted end-to-end channels over the R5N
substrate.

**Cost/latency.** The random walk adds `L2NSE` hops beyond classical Kademlia. For
a network of 1 M nodes that's ~20 extra hops worst-case; GNUnet's deployment chooses
smaller parameters (~6–8 random hops).

**SwartzNet fit.** Conceptually beautiful for the problem at hand — it *is* a DHT
with built-in queryer deniability, which is precisely the §13 #6 gap. But:
- GNUnet's DHT is its own overlay, not mainline. Using R5N means not using mainline,
  same problem as I2P.
- The random-walk trick could in principle be *imitated* inside a SwartzNet BEP-44
  get by doing the get through an intermediate SwartzNet peer instead of directly to
  the BEP-44 storage nodes. This is essentially Dandelion++ (§10) restated.

**Mainline compat.** R5N as a wholesale replacement is a hard no. R5N as *inspiration*
for a stem-phase before the Kademlia-phase of a BEP-44 get is interesting.

**Verdict.** Direct adoption impractical; the *technique* (random walk before
Kademlia) is the core idea we borrow in §11.

---

## 9. Orchid / Dust / obfs4 (traffic-analysis resistance)

**Mechanism.** These make packet sequences *look like something else*. obfs4
(Yawning Angel) is the canonical Tor pluggable transport: it establishes an
authenticated encrypted stream whose packet sizes and inter-arrival times are
randomized, resisting DPI that tries to fingerprint Tor traffic. It does **not**
provide anonymity; it provides *unobservability* of protocol identity. Dust is an
older primitive with similar goals; Orchid is a VPN-like product using Ethereum
payments and OpenVPN/WireGuard underneath.

**SwartzNet fit.** The sn_search LTEP messages ride on a regular BitTorrent TCP
stream that already carries ordinary piece messages, so *by construction* they are
not separately fingerprintable by a passive observer — they look like ordinary BEP-10
LTEP extension traffic, which is common. Adding obfs4 on top of sn_search would
harden against an adversary who has already identified the connection as BitTorrent
and is now trying to distinguish SwartzNet-capable peers from vanilla peers.

The one concrete case where this matters: a censor who wants to block SwartzNet-
capable peers without blocking BitTorrent in general. Such a censor could fingerprint
the LTEP handshake advertisement that announces `sn_search`. A mitigation would be to
*not* advertise in the LTEP handshake and instead probe for `sn_search` support with
a nonce-based opaque message — but this breaks the BEP-10 contract and is exactly the
kind of protocol divergence we promised not to do.

**Mainline compat.** obfs4 on top of BT is a hack; it replaces the wire protocol and
vanilla peers cannot interop. Not a viable integration for the BT side.

**Verdict.** Not useful as a SwartzNet integration. Relevant only insofar as users who
run SwartzNet behind Tor inherit obfs4 automatically via Tor pluggable transports.

---

## 10. Dandelion / Dandelion++

**Mechanism.** Dandelion (Fanti, Venkatakrishnan, Bakshi, Denby, Bhat, Viswanath —
SIGMETRICS 2017) and its successor Dandelion++ (Fanti et al., SIGMETRICS 2018)
obfuscate the *source* of a broadcast item on a P2P network. Two-phase:
- **Stem phase:** the originator forwards the item to exactly one randomly-chosen
  neighbor. That neighbor, with probability `q`, forwards to exactly one of *its*
  neighbors. Repeat.
- **Fluff phase:** with probability `1−q` per hop, the current holder flips from
  stem to fluff and performs ordinary gossip broadcast.
Dandelion++ adds per-epoch `q` shuffling and privacy graphs to resist intersection
attacks. Formalized in BIP-156 for Bitcoin; deployed in Grin and Monero.

Claimed property: against an adversary with a constant fraction of nodes,
source-identification probability decays from `O(1)` (ordinary gossip) to `O(1/n)`.

**SwartzNet fit.** Dandelion++ applies *extremely cleanly* to "publish a BEP-44
item without revealing the publisher's IP to the storage nodes." The publisher does
not put the item to the DHT's 8-closest nodes directly. Instead:

1. **Stem phase.** Publisher sends a *signed-put-envelope* (BEP-44 `{k, v, sig, seq,
   salt}` fields, unchanged) over LTEP sn_search to a single connected peer, marked
   as "stem."
2. That peer, with probability `q=0.9`, re-stems to one of *its* LTEP-capable peers.
3. With probability `1−q=0.1`, the peer flips to fluff: performs a real BEP-44 put
   to the DHT on the publisher's behalf.

The storage nodes see the put coming from the *fluff-phase peer*'s IP, not the
originator's. The sig, seq, and salt are bound to the publisher's pubkey and
cannot be forged by the relay — so the publisher's *identity* (pubkey) remains
linked to the publication, but the publisher's *network location* (IP) is
decoupled from the publication event.

Stem-phase peer selection can reuse the same peer set sn_search already uses —
SwartzNet peers in current swarms with the sn_search extension advertised. Overhead:
one LTEP message per stem hop, fluff peer pays one BEP-44 put to the DHT. Average
stem length at `q=0.9` is ~10 hops, i.e. ~10 × 50 ms = ~500 ms added latency per
publish. Since publishes are background and infrequent, this is negligible.

**Mainline compat.** *Perfect.* The fluff phase emits standard BEP-44 puts that any
mainline DHT node accepts unchanged. The stem phase uses a new sn_search message
type (already our extension space). Zero new ports, zero new DHT verbs, zero new
reserved bits. This is the *one* primitive in this survey that is genuinely opt-in
at the protocol level: a publisher with the feature disabled publishes directly; a
publisher with the feature enabled stem-then-fluffs. No peer needs to opt in for the
publisher's privacy to work — the fluff-phase peer just performs an ordinary put.

**Verdict.** The *single most implementable* privacy upgrade for SwartzNet in this
survey. Addresses §13 #6 (publisher IP-deanonymization) directly and cheaply.

---

## 11. Mapping to SwartzNet layers

### 11.1 Layer D querying (hide which keyword)

Today: a BEP-44 get for keyword `K` under indexer pubkey `P` reveals `SHA1(P||K)` to
the DHT nodes contacted. Dictionary attack is trivial for any keyword in the top
~10⁴.

**Cheapest opt-in upgrade: OHTTP + small-DB PIR against well-known indexer pubkeys.**
- Well-known indexers (§4.3 option 1) offer an optional HTTPS endpoint in addition
  to BEP-44 publication. Announcing endpoint presence uses the sn_search
  `peer_announce` gossip (§5.2 of integration-design) so nothing changes on-wire.
- The endpoint answers FrodoPIR / ChalametPIR queries against its keyword table
  (<1 GB per indexer, trivially fits in RAM, `<1 s` per query).
- The client routes its HTTPS query through an OHTTP relay, of which public ones
  exist (Fastly, Cloudflare, DivviUp).
- Fallback: if no PIR endpoint is available or OHTTP relays are unreachable, client
  falls back to ordinary BEP-44 get, same as today.
- Cost to non-anonymous users: zero (they skip this path).
- Cost to anonymous users: ~200–500 KB uplink per query, ~1 s latency, one extra
  hop through an OHTTP relay.
- Cost to indexers: a single-core FrodoPIR server can sustain dozens of queries/s.

**Mainline compat:** perfect — this is a side channel, not a DHT change.

### 11.2 Layer D publishing (hide the publisher's IP)

**Cheapest opt-in upgrade: Dandelion++ over sn_search.**

Detailed in §10. `q=0.9`, average ~10 stem hops, fluff peer performs the BEP-44 put.
The publisher's ed25519 signature binds the content to them but not their IP. For
publishers who also want pubkey-anonymity (per-publication unlinkability, per §13 #6
second mitigation "rotate pubkeys"), generate a throwaway keypair per batch and
include it in the published record as usual.

Mainline compat: perfect. The DHT sees ordinary BEP-44 puts from ordinary peers.

### 11.3 Layer S (peer-wire search) privacy

This one is *much* harder. Layer S queries ride the existing BitTorrent TCP stream
to a known peer. The peer is already known to the querier (it's in a swarm with
them), so there's no "hide who I am" problem — there's only "hide what I'm asking."
That is PIR-shaped again, but the peer's database is not public, not static, and not
shared — it's whatever that one peer has chosen to publish from its own Bleve index.

Options:
- **Restrict Layer S to broad topic queries** that the querier was going to reveal by
  virtue of being in that swarm anyway. Today's design already does this
  implicitly; no change needed.
- **K-anonymity by keyword bundling:** querier sends a batch of `k−1` decoy keywords
  with the real one. Peer answers all, querier filters. Trivial to implement, adds
  `k×` bandwidth cost, provides modest privacy. Doable as an sn_search request
  flag. Not a win over just accepting that Layer S is best-effort.
- **PIR against individual peers:** infeasible — peer-wire partners' databases are
  too small and too dynamic to justify the preprocessing, and most peers are on
  residential bandwidth.

**Verdict:** Layer S is structurally harder to anonymize than Layer D. Accept that
Layer S remains plaintext-to-peer and document it.

### 11.4 Piece-transfer privacy (hide which infohash I'm downloading)

Orthogonal to SwartzNet's search layers, but the user asked. The honest answer: you
can't. The moment you join a swarm for infohash `H`, every peer in that swarm knows
you want `H` — that's the BitTorrent protocol. Mitigation *above* SwartzNet:
- Run SwartzNet over Lokinet (§3) — the peer and the DHT node see a Lokinet exit
  IP, not yours.
- Or over a commercial VPN — same, with less cryptographic rigor but more bandwidth.
- Or over Tor — slow, and the Tor project asks you not to.

A BEP-44 *get* leaks the target key (`SHA1(pubkey||salt)`), not the infohash — so
"which infohash am I looking up in the DHT?" is an *announce-peers* or *get-peers*
issue, not a BEP-44 issue. Hiding *those* is a separate problem that requires
tunneling mainline DHT itself (Lokinet, OnionCat) or abandoning mainline DHT
(Tribler-style IPv8 overlay, which we explicitly reject in §10).

No cheap option exists. Accept the limitation; document it.

---

## 12. Summary comparison table

| Primitive | Layer D query | Layer D publish | Layer S query | Piece privacy | Mainline-compat | Dev cost |
|---|---|---|---|---|---|---|
| Tor v3 onion | TCP only; needs bridge | possible w/ care | forces all BT over Tor | slow but works | neutral | high (second socket) |
| I2P | separate DHT | separate DHT | separate DHT | yes | fine (different net) | high |
| Lokinet | UDP-capable | UDP-capable | yes | yes | fine | medium (config only) |
| Nym mixnet | excellent | possible | possible | possible | fine via SOCKS | medium |
| HORNET/Karaoke/Vuvuzela | no deployment | no deployment | no deployment | no deployment | n/a | n/a |
| PIR (FrodoPIR/Spiral/Chalamet) | **excellent** | n/a (PIR is read) | poor fit | n/a | fine (side channel) | medium |
| OHTTP (RFC 9458) | **excellent pair with PIR** | n/a | possible | n/a | perfect | low |
| GNUnet R5N | inspiration only | inspiration only | n/a | n/a | bad (separate overlay) | n/a |
| obfs4 / Dust | n/a | n/a | n/a | n/a | breaks BT compat | n/a |
| **Dandelion++** | n/a | **excellent** | n/a | n/a | **perfect** | **low** |

---

## 13. Recommendations

1. **Adopt Dandelion++ for Layer D publish (v1.0 target).** Cheap, one new sn_search
   message type, zero mainline-DHT impact, addresses §13 #6 directly. Implementation
   is localized to `internal/dhtindex` + `internal/swarmsearch`. Estimate:
   ~500 lines of code, two weeks of work including tests.

2. **Adopt OHTTP + FrodoPIR for Layer D query (v1.1 target).** Requires cooperating
   indexers to stand up a PIR endpoint, but the client-side lift is small: a Go
   FrodoPIR client and an OHTTP client are both ~1 k LoC. Announce endpoint
   availability via the existing `peer_announce` sn_search gossip.

3. **Document Lokinet as the recommended full-stack anonymization** for users who
   want to hide even their participation in a swarm. No code changes needed; just
   verify that the daemon works with a SOCKS5/TUN-mode Lokinet configuration and
   write it up.

4. **Do not build Tor support into the daemon.** The TCP-vs-UDP mismatch and the
   two-socket problem are not worth the complexity. Tor users can wrap the whole
   process in `torsocks`; document the caveats.

5. **Do not build I2P support.** Users who want I2P torrenting already have I2PSnark.
   SwartzNet's value-add (mainline DHT network effect) is negated inside I2P.

6. **Accept Layer S plaintext-to-peer as-is.** A k-anonymous decoy-keyword mode
   could be a v2 feature; not a priority.

---

## 14. Citations

- **Tor v3 onion services.** Kadianakis, Mathewson, Angel et al., *Tor Rendezvous
  Specification — Version 3*, `rend-spec-v3.txt`. Tor Metrics, `onionperf-latencies.html`.
- **I2P.** I2P Team, *Invisible Internet Project Documentation*, `geti2p.net/en/docs/`.
- **Lokinet / Oxen.** *LLARP: Low-Latency Anonymous Routing Protocol*, Oxen Project
  documentation (`docs.oxen.io/oxen-docs/products-built-on-oxen/lokinet`).
- **Nym / Loopix.** Piotrowska, Hayes, Elahi, Meiser, Danezis, *The Loopix Anonymity
  System*, USENIX Security 2017. Nym Tech, *The Nym Network white paper*,
  `nym.com/nym-whitepaper.pdf`.
- **Sphinx.** Danezis & Goldberg, *Sphinx: A Compact and Provably Secure Mix Format*,
  IEEE S&P 2009.
- **HORNET.** Chen, Asoni, Barrera, Danezis, Perrig, *HORNET: High-speed Onion
  Routing at the Network Layer*, ACM CCS 2015.
- **Vuvuzela.** van den Hooff, Lazar, Zaharia, Zeldovich, *Vuvuzela: Scalable
  Private Messaging Resistant to Traffic Analysis*, SOSP 2015.
- **Stadium.** Tyagi, Gilad, Leung, Zaharia, Zeldovich, *Stadium: A Distributed
  Metadata-Private Messaging System*, SOSP 2017.
- **Karaoke.** Lazar, Gilad, Zeldovich, *Karaoke: Distributed Private Messaging
  Immune to Passive Traffic Analysis*, OSDI 2018.
- **Spiral.** Menon & Wu, *Spiral: Fast, High-Rate Single-Server PIR via FHE
  Composition*, IEEE S&P 2022 (IACR ePrint 2022/368).
- **SimplePIR / DoublePIR.** Henzinger, Hong, Corrigan-Gibbs, Meiklejohn,
  Vaikuntanathan, *One Server for the Price of Two: Simple and Fast Single-Server
  Private Information Retrieval*, USENIX Security 2023.
- **FrodoPIR.** Davidson, Pestana, Celi, *FrodoPIR: Simple, Scalable, Single-Server
  Private Information Retrieval*, PETS 2023, Issue 1.
- **Pantheon.** Ahmad et al., *Pantheon: Private Retrieval from Public Key-Value
  Store*, VLDB 2023.
- **ChalametPIR / Call Me By My Name.** Celi & Davidson, *Call Me By My Name: Simple,
  Practical Private Information Retrieval for Keyword Queries*, ACM CCS 2024.
- **Checklist.** Dong, Lambert, *Checklist: Fast Private Computation for Finite
  Sets*, PETS 2021.
- **Distributed Point Functions.** Gilboa & Ishai, *Distributed Point Functions and
  Their Applications*, EUROCRYPT 2014; Boyle, Gilboa, Ishai, *Programmable
  Distributed Point Functions*, CRYPTO 2022.
- **Oblivious HTTP.** Thomson & Wood, *Oblivious HTTP*, IETF RFC 9458, January 2024.
- **GNUnet R5N.** Evans & Polot, *R5N: Randomized Recursive Routing for
  Restricted-Route Networks*, IEEE P2P 2011; Schanzenbach et al., *The R5N Distributed
  Hash Table*, `draft-schanzen-r5n`.
- **obfs4.** Yawning Angel, *obfs4 (The obfourscator)*, `gitlab.com/yawning/obfs4`.
- **Dandelion.** Bojja Venkatakrishnan, Fanti, Viswanath, *Dandelion: Redesigning
  the Bitcoin Network for Anonymity*, SIGMETRICS 2017.
- **Dandelion++.** Fanti, Venkatakrishnan, Bakshi, Denby, Bhat, Viswanath,
  *Dandelion++: Lightweight Cryptocurrency Networking with Formal Anonymity
  Guarantees*, SIGMETRICS 2018; Bradbury, *BIP-156: Dandelion — Privacy Enhancing
  Routing*.
