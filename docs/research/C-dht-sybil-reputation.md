# Research Report C — DHT Design and Sybil/Spam Resistance for Layer D

| Field | Value |
|---|---|
| Date | 2026-04-24 |
| Scope | Layer D (`internal/dhtindex`) — BEP-44 keyword index |
| Relates to | `docs/05-integration-design.md` §4.3, `docs/07-bep-dht-keyword-index-draft.md`, `internal/reputation` |

## 0. Framing

SwartzNet's Layer D stores keyword → infohash mappings as BEP-44 mutable items keyed by `SHA1(publisher_pubkey || keyword)` (see `internal/dhtindex/schema.go:15–142`). Each item is ed25519-signed, capped at 1000 bytes, and shards via a `"more":1` pagination field. Searchers fan out to a hardcoded list of "seed" indexer pubkeys plus any gossip-discovered ones, and a Bayesian-smoothed per-pubkey reputation (`internal/reputation/reputation.go:74–77`) ranks results.

The load-bearing property we must preserve is *mainline compatibility*: no new DHT verb, no new reserved bit, no new port. Every defence examined below is evaluated through that lens — if it cannot be deployed in userspace as a convention layered on top of BEP-3/5/9/10/44/51, it is disqualified or must be wrapped in a companion torrent (BEP-46 style).

The threat model is standard for a keyword-search overlay:

- **T1 — Sybil publish spam.** Attacker mints N ed25519 keys, publishes `"stormcloud hentai"` at salt="ubuntu" under each. Today: cost is ~N BEP-44 puts and N signatures (microseconds each); it is essentially free.
- **T2 — Eclipse a keyword.** Attacker places DHT nodes whose IDs are close to `SHA1(pub‖kw)` and serves corrupted values, denying access. BEP-42 already raises this cost.
- **T3 — Keyword enumeration / privacy.** Anyone can grind ed25519 keys and run `get` against `SHA1(pub‖"bomb-making")` to see who publishes it. Because salts are plaintext UTF-8 keywords, an observer at the DHT node end can enumerate which keywords a publisher covers.
- **T4 — Repudiation / bait-and-switch.** A publisher signs a hit pointing at an infohash containing not-what-they-claimed. Today there's no cryptographic binding between the signed hit and the actual content — the publisher can rotate their reputation elsewhere and deny they ever signed anything bad.

## 1. S/Kademlia (Baumgart & Mies, 2007)

S/Kademlia combines three defences: (a) a *static* crypto puzzle where the node picks an ed25519 key such that `H(pub)` starts with c₁ zeros, plus a *dynamic* puzzle where for a chosen `x`, `H(nodeID ⊕ x)` starts with c₂ zeros (c₁ raises the bar for minting any valid ID at all; c₂ raises the bar for targeting a *specific* position close to a victim key); (b) *parallel disjoint-path lookups* — run `d` parallel lookups that share no intermediate node, and return successful only if the majority agree; (c) *sibling lists* — replication-group size `s` is decoupled from routing bucket-width `k`, so a key is stored on `s` nodes and answered by quorum even if `s-1` are malicious.

The failure probability for a lookup with fraction `f` of adversarial nodes, `d` disjoint paths of length `h` is `p_fail = (1 - (1-f)^h)^d`. For `f = 0.2`, `h = 5`, `d = 3`: `p_fail ≈ 0.27` → only 27% of lookups fail instead of the 67% failure rate with a single path. For `d = 10`: `p_fail ≈ 0.008`.

**Status in anacrolix/dht.** The vendored `anacrolix/dht/v2` implements BEP-42 node-ID derivation, basic α-parallel lookups (α=3 is BEP-5 boilerplate), and a routing table that rate-limits per-/24 subnet. It does **not** implement S/Kademlia's disjoint-path constraint (paths are merely "α in flight", not guaranteed node-disjoint) nor static crypto puzzles above what BEP-42 mandates, nor sibling-list quorum.

**What we could add in userspace around BEP-44 gets:**

1. *Disjoint-path BEP-44 fan-out.* Wrap `dht.GetMutable` so that the α=3 parallel queries each pick a different initial bucket and refuse to fall back on overlapping buckets. Even a naive implementation — "pick 3 candidates from 3 different /16 subnets" — gets most of the benefit.
2. *Sibling-list quorum.* Do the BEP-44 `get` against the 8 closest nodes (the BEP-44 replication factor) *and require k-of-n agreement* on the bencoded value before accepting it. Today anacrolix accepts the highest-sequence-number copy it sees.
3. *Dynamic puzzle on the publisher side.* A publisher who wants to be *in our seed list* must exhibit `H(pubkey || epoch_ts) < target`. Grinds a key for a few CPU-seconds once per epoch. Does not affect the mainline DHT — it's purely a SwartzNet convention checked by searchers when they decide whether to add a gossip-discovered key to their indexer set.

The static puzzle is the strong defence: it makes Sybil *identity creation* costly, not just Sybil *publishing*. For c₁=20 (~1M hashes ≈ 2s on a laptop) it's a meaningful speed-bump for minting *millions* of throwaway keys but is invisible to an attacker minting hundreds.

## 2. BEP-42 in practice

BEP-42 derives a node's first 21 bits from `CRC32C(mask(IP))`, so the possible node IDs for a given /8 are restricted to ~8 M out of 2¹⁶⁰. Empirical numbers (Pubky, Dec 2024): the mainline is ~10 M nodes, bucketed at k=20, so ~500 K buckets. A single IP falls in 1 of 500 K buckets; fully eclipsing one bucket requires ~60 cooperating BEP-42-compliant nodes (rough attacker cost ≈ $10/day in VPS + ~$500/day in bandwidth per bucket). Without BEP-42 (legacy nodes still interoperate), the same eclipse needs only 20 nodes because you can pick the IDs freely.

**Attacks that still work.** The BEP-42 IP-hash only uses the first 21 bits; the remainder is free. Owning a /8 IP block (16.7 M addresses) lets you enumerate enough hashes to land 8+ nodes in *any* chosen bucket. Cloud providers that control /13 or larger (AWS, GCP, Hetzner) effectively defeat BEP-42 at their discretion. A 2013 Wang & Kangasharju measurement identified ~300 K active Sybils on mainline *despite* BEP-42. No public follow-up measurement study exists post-2020 beyond the Pubky piece; `cl.cam.ac.uk/~lw525/MLDHT/` stats have not been refreshed.

**Pubky hardening.** Their Rust mainline implementation blocks high-risk /8s outright (US DoD ranges, known hostile subnets) and enforces "at most 2 nodes from the same /24 in my routing table." This is routing-table hygiene — it's compatible with mainline and is strictly a local policy. SwartzNet should copy it verbatim; it's a ~50-line patch in the DHT client and costs nothing.

**Takeaway for SwartzNet.** BEP-42 is necessary but not sufficient against a motivated adversary. We cannot strengthen BEP-42 without breaking mainline. We *can* strengthen our *own* routing-table policy on top of it, and we should.

## 3. R5N — GNUnet's DHT

R5N (Evans & Grothoff, 2011; LSD0004 re-spec 2022) replaces "deterministic greedy closest-peer routing" with a *two-phase* algorithm: each PUT takes one of several *randomised* paths for the first `log n` hops (unpredictable routing, evades an eclipse-positioned adversary), then switches to deterministic closest-peer for the last `log n` hops (bounded lookup cost). Replication is achieved by doing `r` PUTs rather than `r` replicas of a single target — each randomised path ends at a different closest-peer set, so you get content-replica diversity without trusting any one bucket.

R5N also features *on-path validation*: intermediate nodes apply a key-format check (e.g. GNUnet's GNS records are self-verifying) and drop malformed values so bad data never reaches the destination bucket. This is essentially free for BEP-44 because signatures are already verified by every replica, but it's a useful model for *value-schema validation* — a SwartzNet indexer who received a keyword value containing 10000 fake infohashes could drop it on the way.

**What SwartzNet can borrow.** We can't randomise the *mainline* routing algorithm (other clients do deterministic Kademlia). But we can randomise our *publication path*: instead of always writing to the 8 closest nodes to `SHA1(pub‖kw)`, we choose a set of nearby-but-not-deterministic targets. Spec-wise this is illegal under BEP-44 — the *storing node* must be close to the target for anyone else to find it. So R5N's routing trick does not transfer.

The *replication-count* lesson does transfer: instead of 1 BEP-44 item per keyword, publish to 2-3 *salts* (`"ubuntu"`, `"ubuntu#a"`, `"ubuntu#b"`) each near a different bucket. Triples per-keyword storage cost but makes eclipse attacks 3× harder. This is exactly the sharding scheme SwartzNet already has (`SaltForShard`, `dhtindex/schema.go:138`) — we just need to use it for *redundancy*, not only for overflow.

## 4. Tribler's TrustChain / IPv8

TrustChain is an individual-ledger blockchain (per-node chain of signed records, hash-linked to previous entries, cross-signed by counterparty). Every peer maintains *their own chain*, and when A serves data to B, both sign a receipt; the receipts are gossiped and validators spot-check them. Designed for bandwidth-accounting in Tribler's Tor-like anonymisation tunnels (2017–2020). Extended to MultiChain (parallel chains per relationship type) and the "trust score" shown in the Tribler GUI.

**Strengths.** No global consensus needed. Receipts are *non-repudiable*: a peer signs that they served content X — they can't later deny it. Sybil resistance comes from long-running chain histories being hard to fake retroactively.

**Weaknesses.** Gossip of receipts is expensive; Tribler has measurable overhead from it (see `docs/02-tribler-deep-dive.md`). Chain verification requires chasing hash-links, which is heavy on cold start. And TrustChain does not prevent *first-time Sybils* — a fresh key has a fresh empty chain; there's no prior behaviour to evaluate.

**For SwartzNet Layer D.** The *non-repudiation* idea is valuable: a publisher's BEP-44 value could contain a Merkle root over `(infohash, extracted_text_hash, timestamp)` triples. Signed. Publicly verifiable. A searcher who downloads the torrent and finds it does not match the indexed keywords has a cryptographic artifact to show others: "this publisher signed this hit, here's the torrent, here's the text, here's proof of mismatch." That transforms reputation from subjective ("I didn't like the result") to objective ("here's the proof that publisher P lied about keyword K at sequence N"). This is the first concrete improvement I'd add.

Full TrustChain-style chained receipts are overkill for just the indexing layer — we don't need bandwidth accounting. But a *per-keyword signed Merkle commit* is a 32-byte field in the BEP-44 value.

## 5. EigenTrust / EigenTrust++ / PowerTrust

EigenTrust (Kamvar et al., 2003) computes a global reputation vector as the left eigenvector of a row-stochastic local-trust matrix T, where T[i][j] = (fraction of transactions with j that i considered satisfactory, normalised). Converges in ~10 iterations of distributed power-iteration. Crucially **requires a set of pre-trusted peers** to both anchor the eigenvector and break up malicious collectives — without them the algorithm converges to uniform or to a collective. EigenTrust++ (Kurdi 2015, "HonestPeer") refines the pre-trusted set dynamically; PowerTrust (Zhou & Hwang 2007) uses a power-law anchor set.

**Tractability for SwartzNet.** Honestly poor. EigenTrust needs *every peer to talk to several others about every transaction* — we'd need a whole gossip substrate just for the trust matrix, roughly the scope of Tribler's IPv8 overlay. It also requires pre-trusted peers, which SwartzNet already has in the form of seed indexer pubkeys — but once you have those, you might as well use the simpler Bayesian tracker we already have (`internal/reputation/reputation.go`). The marginal gain from computing a global eigenvector instead of a local per-user average is small when the graph is sparse and latency to converge is tens of seconds of DHT traffic.

**What to steal.** The *normalisation* insight: a peer who flags 100% of results as bad is not trustworthy feedback either. EigenTrust normalises `max(sat - unsat, 0)` so spiteful downvoters don't dominate. Our reputation.go could use the same clamp — today it naively subtracts flags from confirms, which could push an honest publisher to zero if one bad actor mass-flags them.

## 6. Proof of Personhood / BrightID / Idena / Privacy Pass

PoP systems bind one account to one human. BrightID uses verification parties (humans on a video call vouching for each other) and achieves ~100 K unique humans at mid-2024. Idena uses simultaneous-CAPTCHA epochs where users must solve a "FLIP" (a sequence of 4 pictures that tells a story) *at the same time as everyone else*, deterring a single human from operating multiple identities; ~50 K epochal participants. Worldcoin/World ID uses biometric iris scans; Humanode uses liveness-detected 3D face scans. Privacy Pass is lower-stakes: unlinkable per-action anonymous tokens issued by a fate-line (Cloudflare, Apple Private Access) which attest "this token came from a real user" without revealing *which*.

**Could we gate an indexer pubkey on PoP?** In principle yes. Concretely:

- Make a seed indexer pubkey valid only if it carries a BrightID or Idena attestation on its first publication. An attestation is a signature from the PoP issuer over the indexer's pubkey. Verifiable offline.
- Searchers add pubkeys to their indexer set only if they carry a valid, unrevoked attestation.

**Why this is harder than it sounds.**

1. It imports a centralised or semi-centralised trust root. BrightID is decentralised but has a central verification ceremony; Worldcoin is explicitly one company. This contradicts SwartzNet's "no new central party" principle.
2. PoP issuers rotate keys; offline verification needs periodic re-fetching of the issuer's attestation set. Practically this wants a DNS or HTTPS endpoint — we can't embed it in the DHT without... another DHT item.
3. Worldcoin/Humanode require biometrics; many users will refuse. Idena's synchronous CAPTCHA is incompatible with casual participation (you must show up at the epoch start).
4. None of them are particularly Sybil-hard: as of 2024 BrightID had <200 K verified users, several academic papers report forged identities via deepfake-video-call and via "renting an account".

**Privacy Pass** (IETF-chartered; RFC 9578) is more interesting. It gives *unlinkable rate-limit tokens*: an issuer (could be SwartzNet's seed-indexer operator) hands out K tokens per human per epoch. Each token unlocks one indexer publish. No global identity; just "this publish came from a token-holder." This is essentially anonymous credentials with a per-issuer rate limit — a valuable primitive for SwartzNet's *publish* path, though it still requires a trusted issuer.

**Verdict.** Not v1. Could be a v2+ add-on via the signing protocol in `docs/11-signing-protocol.md` (a pubkey could carry an optional PoP attestation that boosts its reputation seed). Not a substitute for the Bayesian tracker.

## 7. Hashcash / PoW on publish

Cost-to-publish is the cheapest, most deployable Sybil deterrent. Adam Back's Hashcash (2002): to send a message, prefix it with a nonce such that `H(msg‖nonce) < target`. A cost linear in the puzzle difficulty, verifiable in O(1). Perfect for the "one human writes K messages/day, a spammer wants to write K×10⁶" asymmetry.

**Current situation.** BEP-44 already imposes a mild cost: generating a valid PUT requires an ed25519 signature (~50 μs) plus a round-trip to 8 DHT nodes (~3-5 s). Also, the receiving node rate-limits PUTs per source IP (libtorrent default: 5 puts/s). So the raw publish throughput from a single attacker IP is bounded to ~5 puts/s regardless of their CPU. With a /24 they can burst 1280 puts/s; with a /16 they can do 320 K puts/s. That's already enough to saturate Layer D search for any popular keyword.

**Stronger primitive.** Make the *BEP-44 value itself* contain a hashcash proof over `(pubkey || seq || sha256(value_payload))`. Require difficulty `D` on publish; searchers reject values below threshold `D'`. Because sequence numbers are monotonic in BEP-44, an attacker cannot reuse a PoW across seq values — every new publish requires a fresh grind.

Concrete parameters:
- D = 20 leading zero bits → ~1M SHA256 ops per publish ≈ 20 ms CPU on a laptop, ~2 ms on a GPU.
- A single publisher doing 100 keyword-publishes on torrent-add takes 2 seconds: acceptable.
- A Sybil attacker trying to spam 10⁶ keyword-items pays 10⁶ × 20 ms = 5.5 CPU-hours = $0.10-$0.50 on cloud: still cheap but now *measurable*.
- Raising to D = 24 costs 16× more: ~$1-$8 per million items. This is where "free spam" becomes "the attacker pays for every campaign."

**Deployability.** This is a value-schema addition. Old SwartzNet readers ignore the field; new readers require it. The BEP-44 network doesn't care. This is the *second* concrete improvement I'd add — it stacks with everything else and is independent of reputation.

The "ERGO" paper (Gupta et al., J. Comput. Syst. Sci. 2023, ex-arXiv 2010.06834) shows you can do asymmetric resource burning: honest users burn O(T·J + J) when attackers burn T, where J is the join rate. Interesting but requires a coordinator — not directly applicable, but reinforces that PoW on publish is the least-bad lever available to us.

## 8. libp2p Kad-DHT innovations

libp2p's IPFS DHT has evolved through the problems SwartzNet is about to hit:

- *Provider records* store "this peer has CID X". Until 2023 this was keyed on the full CID; libp2p PR #422 migrated the key to the *raw multihash* inside the CID. The practical effect: an observer on the DHT cannot tell whether "provider X has CID V1:CidV1Foo" vs "provider X has CID V0:CidV0Foo" — the lookup key is identical. This prevents a provider-enumeration attack.
- *Double-hashed provider records* (DHT v2 PR / specs): the DHT key stored is `H(H(content_key))`, not `H(content_key)` — an observer of DHT traffic sees `H(H(key))` and cannot reverse it to the content identifier without already knowing the content. Mitigates T3 (keyword enumeration) significantly. Providers prove they know the original content by presenting a proof alongside their ADD_PROVIDER, so the receiving node can verify the key is well-formed without knowing the content.
- *Accelerated DHT client* (Stebalien, Schmidt, 2022–2023): crawl-based full-routing-table lookups cut provider-find p50 from ~2 s to ~200 ms at the cost of ~30 MB RAM per client — a bigger crawl, fewer hops.
- *Optimistic Provide*: skip the last hop of a provide by using network-size estimation to predict which bucket is close enough. Trades publish latency for bandwidth.

**Applying double-hashing to BEP-44 salts.** This is the big finding for SwartzNet. BEP-44's target is `SHA1(pubkey || salt)`. If we set `salt = SHA256("ubuntu")` instead of `salt = "ubuntu"`, then the DHT target `SHA1(pubkey || SHA256("ubuntu"))` reveals nothing about the keyword to anyone watching the DHT traffic. The publisher hashes once to compute the salt; the searcher hashes once to compute the same salt. BEP-44 doesn't care what the salt is as long as it's ≤ 64 bytes. This is a *purely cosmetic wire-level change* with a real privacy benefit: an observer at a DHT node sees a stream of `SHA1(pk‖h)` lookups and cannot learn the keyword without guessing it and hashing.

An attacker can still *probe* — they hash a candidate keyword and run the GET — but they can't *crawl* (they'd have to enumerate the dictionary, not just observe). Enumeration cost goes from ~free to ~1 SHA256 per guess, which means the attacker's burden is dictionary-sized, not DHT-sized. For a 100K-word English dictionary that's still cheap; for random multi-word phrases it's infeasible. Recommend deploying this unconditionally.

**Caveat.** Double-hashed salt breaks human-readable debugging ("what's at this DHT target?"). We should keep a client-side mapping `hash → plaintext` in local storage for the keywords *we* publish.

## 9. Bitmagnet / BEP-51 sample_infohashes

BEP-51 adds one new query (`sample_infohashes`) which returns up to 50 random infohashes the queried node currently tracks, plus `num` (its total count) and `interval` (politeness hint). A single crawler issuing `sample_infohashes` to every DHT node it meets can survey all ~60 M daily-active infohashes in a few hours, which is exactly how bitmagnet operates.

**anacrolix support.** The `anacrolix/dht/v2` library has had `sample_infohashes` since 2019. bitmagnet itself is built on anacrolix/torrent.

**Application to SwartzNet.** SwartzNet can passively discover indexer pubkeys without a hardcoded seed list by a two-step crawl:

1. Run a BEP-51 sampler over ~100 peer nodes to harvest infohashes.
2. For each infohash with `snet.pubkey` in its metainfo (requires BEP-9 fetch of metadata — expensive: ~1-2 s per infohash), extract the publisher pubkey.
3. Maintain a set of "observed publisher pubkeys" ranked by how many torrents they've signed.

Bandwidth cost: ~50 KB/s idle for the sampling + 5 KB × N for metadata fetches. For 10 K torrents/day that's ~50 MB of metadata fetches — affordable on any residential link.

This is the right mechanism to *discover* indexers; it does not replace reputation (a pubkey observed signing 10 K torrents could still be a spammer). But it does kill the hardcoded-seed problem: the network becomes its own seed.

## 10. Plumtree / HyParView / SWIM

HyParView maintains two partial views: an *active* view of O(log n) peers you keep alive with TCP heartbeats, and a *passive* view of O(k) backups you know exist but don't talk to. Plumtree builds a spanning tree on top: messages are *eagerly* forwarded along tree edges and *lazily* (just ID digests) along non-tree edges; when a lazy digest arrives and you haven't seen the message, you pull it and rewire the tree. SWIM is a failure-detector with piggybacked membership updates — simpler than HyParView, weaker reliability guarantees.

**Fit for SwartzNet.** Replacing the hardcoded seed-indexer list with pure gossip is exactly the HyParView use case. A SwartzNet node's active view is its ~10-50 active BitTorrent peers that speak `sn_search`. Its passive view is the larger set of pubkeys it's observed via BEP-51 crawling (§9) plus the ones that peers have gossiped to it over `sn_search` extension messages.

A Plumtree overlay for *indexer pubkey announcements* would be very small — "new-indexer" gossip messages are ~48 bytes (pubkey + sig + ts) and only fire when a new publisher joins. Piggybacking them on the existing `sn_search` handshake costs almost nothing.

**Why not use it for search fan-out too?** Because search queries leak plaintext keywords. Gossip of queries is how eMule Kad's keyword privacy collapsed. Keep it publisher-discovery only.

## 11. Bitcoin INV/getdata / Ethereum discv5

Bitcoin's `inv` message announces "I have these 500 transactions" and peers reply with `getdata` for the subset they don't have. Compact-block relay (BIP 152) reduces block propagation by sending short IDs pre-agreed with the peer. The crucial design pattern: *announce identifiers cheaply, fetch content only on demand*.

**Relevance to SwartzNet.** The keyword index is conceptually an `inv` announcement: publisher's items are "I claim to have hits for these keywords." If the peer already has a cached value from that publisher, they don't need to re-fetch. Today Layer D re-fetches unconditionally. A simple hack: have `sn_search` peers exchange `(publisher, keyword_root, seq)` tuples — if my seq matches yours, no re-fetch needed. This saves DHT load and lets the network scale sub-linearly with query rate.

**Ethereum discv5.** Signed ENRs, per-IP rate limits (Lighthouse cites "limit 2 per /24 per bucket, 10 per /24 per table" — exactly the BEP-42 hardening from §2), topic advertisement tables with "topic radius" that trades efficiency for eclipse-resistance. Also uses a WHOAREYOU handshake to prove IP ownership before entering the routing table. The ENR/radius idea doesn't map directly to BEP-44 but the *per-subnet table diversity* rule is portable and should be adopted as routing-table policy (see §2).

## 12. Mainline DHT measurements 2023–2026

What we have:

- **Pubky (Dec 2024)**: ~10 M concurrent nodes, organised into ~500 K buckets of k=20. Attack cost estimate: $510/day in infra to fully eclipse one bucket.
- **Grokipedia / handwiki (Jan 2025)**: refresh of 2013 Wang measurement methodology estimates 16–28 M concurrent users *per day* with ~300 K Sybils observable.
- **bitmagnet operations (2024–25)**: a single crawler node (reported on GitHub / HN) harvests ~50 K unique infohashes/hour via BEP-51; that's ~1.2 M/day from one node, confirming DHT scale.
- **libtorrent simulation (arvidn/libtorrent test_dht.cpp)**: in ideal conditions BEP-44 PUT success rate ~95% (8-of-8 replicas accept the put); in production the CAS issue #3578 suggests many clients reject puts with stale-seq which we interpret as failure rather than no-op. No independent production measurement post-2020 that I could find.

**What we don't have.** No credible public measurement of the fraction of mainline nodes that *implement BEP-44* specifically. Rough estimates based on client market share: libtorrent (qBittorrent, Deluge, BiglyBT, etc.) dominate ~70-80% of mainline, all BEP-44-capable. µTorrent: BEP-44 ships since 3.4.2 (2015). Transmission: BEP-44 support added 2018. The one big holdout was webtorrent until ~2020. Conservative estimate: **85–95% of mainline nodes accept BEP-44 PUT/GET today**. This is not *measured*, it's inferred from client adoption history.

**Latency.** Pkarr's own measurements (their BEP-44-based DNS system) report p50 ~3 s and p95 ~15 s for a cold DHT get, p50 ~150 ms through a relay. For SwartzNet a per-keyword GET latency of 3 s is tolerable for search; it's intolerable for interactive UX. Pkarr mitigates by running relays — SwartzNet could do the same ("the seed indexers run always-on relays"), but this reintroduces a centralised component.

## Priority synthesis

### Q1 — Is the hardcoded seed-indexer pubkey list actually buying much, or is it just tracker-style bootstrap?

It is *mostly* a tracker-style bootstrap, and it has the same problems as trackers:

- Ship with 20 hardcoded pubkeys; 3 go offline in year 1; 2 get compromised by year 3. No graceful degradation path.
- New clients download a cached binary-bundle containing seed pubkeys that can be a year old.
- If the project lead (the entity curating the seed list) is attacked or disappears, the list stagnates.

However, unlike trackers, the seed list has *one genuine function beyond bootstrap*: it provides an anchor for EigenTrust-like reputation propagation (a new publisher's reputation is built by being correlated-with-good-answers-from-seeds). Remove the seed list and your Bayesian tracker has nothing to bootstrap from — every new pubkey is neutral, forever, until a human marks it good.

Recommendation: **keep the seed list purely as a reputation anchor** (like EigenTrust's pre-trusted set) but **do the actual discovery by BEP-51 crawl + `sn_search` gossip** (§9, §10). The seed list becomes ~5 pubkeys, not 20; it has one job — anchoring trust; and the vast majority of publisher pubkeys are organic.

### Q2 — Can we drop per-pubkey ed25519 reputation in favour of a Bloom filter of known-good infohashes?

You can. But it's a different system, not a simpler one.

Pros:
- No attribution overhead: a hit is either a known-good infohash or it isn't. No publisher identity needed at the DHT-value level.
- Robust to pubkey rotation: an attacker who churns through keys gains nothing; their hits are still judged on the infohash.
- The filter is ~100 KB for 10 K known-good torrents at 1% FPR.

Cons:
- You still need *some* way to decide whether an infohash is known-good. Today that decision uses publisher reputation (a trusted publisher's hits get auto-confirmed). Without reputation, every infohash must be manually blessed by a user download + confirmation, which is slow and doesn't scale.
- The filter cannot cover new content by construction. The whole point of Layer D is to *discover* torrents you haven't seen yet.
- Bloom filters only answer "maybe in set" — you can't use them to *rank* results.

Recommendation: **use the Bloom filter as a second-stage filter, not a replacement for reputation.** A hit from publisher P returns rank = `reputation(P) * (1 + known_good_bonus if ih in bloom)`. The Bloom filter makes "confirmed good" hits float to the top; reputation covers the rest. This is what `internal/reputation/bloom.go` seems set up for already; the insight is that we should not try to get rid of one layer — they solve different problems.

### Q3 — TrustChain-style non-repudiable receipts for publishers?

Yes, cheaply. Add one field to the BEP-44 value: `"commit": sha256(canonical_hit_list || seq || ts)`. Sign as part of the BEP-44 mutable-item signature. Then any searcher can later:

1. Fetch the signed value at (pub, kw, seq).
2. Notice one hit is garbage / fraudulent.
3. Publish to the network a "reputation receipt": `{publisher_pk, seq, offending_hit_idx, proof_of_garbage}`.
4. Other searchers verify the signature and the publisher's commit — they cannot claim "I didn't publish that."

Cost: 32 bytes in the BEP-44 value. Zero wire-protocol changes. The *proof of garbage* is out-of-band: "download infohash X, hash its content, compare to what was claimed" or "here's an extracted text that doesn't contain keyword K." Enforcement is still socially subjective, but the *artefact of lie* is cryptographically verifiable.

This is the single cleanest reputation upgrade available.

### Q4 — Double-hashed CIDs → double-hashed keyword salts in BEP-44?

Direct port. `salt = SHA256(keyword)` instead of `salt = keyword`. BEP-44 doesn't care (it's just bytes). Privacy benefit: DHT observers cannot read keyword streams. Attacker cost goes from "free" to "dictionary-grind" for enumeration. Implementation cost: 3 lines in `dhtindex.SaltForKeyword`. Backward-incompatible with old readers; stage via a `sn_search` capability-bit flip.

Worth doing in v1. Arguably *should have been* the original design.

### Q5 — What if the DHT isn't used at all, and keyword search lives entirely in peer-wire?

This is the most radical option and it's under-weighted in the current design.

The existing `sn_search` LTEP protocol (§4.2 of `docs/05-integration-design.md`) already queries your 50 connected peers. Each peer has ~1 K torrents. That's 50 K torrents per query, served in one round-trip, no DHT at all. For *trending / popular* content, this is already enough — popular torrents have many seeders, and your peer-set in a popular swarm is topically clustered by construction.

For *long-tail* content (obscure torrents) you need something with broader reach. Options:

- **BEP-51-crawl + push**. Each peer publishes their keyword index as a *companion torrent* (§7.3 of `04-bep-extension-points.md`), infohash announced via BEP-46 pointer. Searchers fetch the companion torrent from seeders and query it locally. No DHT-for-keyword at all; the DHT is used only for BEP-46 pointer resolution and peer discovery. This scales linearly with the number of publishers but decouples search from DHT performance.
- **Plumtree gossip of hit summaries**. "Ubuntu has new torrent IH=X" broadcasts to the Plumtree overlay. Lossy but eventually consistent. Good for new-content notification; bad for historical search.

The companion-torrent approach is *already the v2 plan* in SwartzNet's docs. The surprise is that it's arguably strictly better than BEP-44 Layer D for keyword search, *except* for the cold-start case where you don't know which publishers exist. Once you have BEP-51 crawl (§9) to discover publishers, the DHT's only remaining job is serving the BEP-46 pointer for each publisher's current index-torrent. That's 1 GET per publisher per session — a tiny fraction of today's Layer D traffic.

**Recommendation.** Treat BEP-44 Layer D as a bootstrap-only mechanism: BEP-44 holds the BEP-46 pointer to the publisher's *current companion-index torrent*; the actual keyword search happens against the companion torrent (which is cached locally after first fetch, doesn't have the 1000-byte cap, scales to millions of keywords). This converts BEP-44 from "per-keyword storage" (~N×K items) to "per-publisher pointer" (~N items) and unblocks the whole 1000-byte-cap problem.

## Short summary

### (1) Is BEP-44 the right primitive for Layer D?

**Partially.** BEP-44 is correct for *publisher pointer resolution* — one mutable item per publisher, carrying a BEP-46-style pointer to that publisher's current companion-index torrent. It is *incorrect* as a per-keyword store: the 1000-byte cap forces sharding, which multiplies DHT load linearly with keyword count; the privacy story is bad (plaintext salts); put/get latency is poor (seconds p50); and there is no global keyword namespace, forcing publishers-per-keyword discovery anyway. Move the *keyword index data* to companion torrents (BEP-46 pointer pattern, already planned in SwartzNet docs for v2). Keep BEP-44 for the ~100-byte pointer per publisher. This inverts the cost from O(publishers × keywords) items on the DHT to O(publishers).

### (2) One concrete Sybil-resistance upgrade that does not need a new blockchain or PKI

**Hashcash proof-of-work on every Layer D publish.** Require the BEP-44 value to contain a nonce such that `SHA256(pubkey || seq || value_payload || nonce)` has D=20 leading zero bits. Costs a honest publisher ~20 ms per publish (negligible for the typical ~10 keywords per torrent-add); costs a Sybil attacker trying to mass-spam 10⁶ items about $0.10–$0.50 on cloud CPU; raising to D=24 multiplies that by 16. Requires zero wire changes — it's a schema-level addition to the bencoded `v` field. Stacks additively with reputation, Bloom filtering, and the double-hashed-salt privacy fix.

### (3) Single most surprising finding that changes how Layer D should be designed

**BEP-44's salt privacy is trivially fixable with no mainline-compatibility cost**, and the cost of *not* fixing it is that anyone operating a DHT node can passively enumerate every keyword every SwartzNet publisher has ever put on the network. Setting `salt = SHA256(keyword)` instead of `salt = keyword` costs three lines of code in `dhtindex/schema.go` and moves keyword enumeration from "observe DHT traffic" (free) to "grind a dictionary and GET each" (per-guess DHT round-trip). This is a *strictly dominant* change with no downside that I could identify, and it changes the privacy properties of Layer D fundamentally — from "plaintext stream visible to every adversarial DHT node" to "dictionary attack required per target keyword." Everything else in this document — reputation, PoP, EigenTrust — is optional optimisation, but the double-hashed salt is an obvious bug fix. It should land before v1.0.0 GA.

## References

- Baumgart, I. & Mies, S. (2007). *S/Kademlia: A Practicable Approach Towards Secure Key-Based Routing.* ICPADS. [telematics.tm.kit.edu](https://telematics.tm.kit.edu/publications/Files/267/SKademlia_2007.pdf)
- Mega, G. (2023). *Secure Key-Based Routing with S/Kademlia* [giulianomega.com](https://www.giulianomega.com/post/2023-07-24-secure-kademlia/)
- BEP-42 & discussion — [bittorrent.org/beps/bep_0042](https://www.bittorrent.org/beps/bep_0042.html), [github webtorrent/bittorrent-dht#15](https://github.com/webtorrent/bittorrent-dht/issues/15)
- Evans, N. & Grothoff, C. (2011). *R5N: Randomized recursive routing for restricted-route networks.* NSS. [ieeexplore](https://ieeexplore.ieee.org/document/6060022/). LSD0004 spec 2022 [lsd.gnunet.org/lsd0004](https://lsd.gnunet.org/lsd0004/).
- Pouwelse, J. et al. (2006–2020). *Tribler / TrustChain / IPv8* [tribler.org/IPv8](https://tribler.org/IPv8/)
- Kamvar, S., Schlosser, M., Garcia-Molina, H. (2003). *EigenTrust* [nlp.stanford.edu/pubs/eigentrust.pdf](https://nlp.stanford.edu/pubs/eigentrust.pdf)
- Kurdi, H. (2015). *HonestPeer* ScienceDirect.
- Gupta, D., Saia, J., Young, M. (2023). *Bankrupting Sybil Despite Churn.* J. Comput. Syst. Sci. [arXiv:2010.06834](https://arxiv.org/abs/2010.06834)
- Back, A. (2002). *Hashcash — A Denial of Service Counter-Measure.* [hashcash.org/hashcash.pdf](http://www.hashcash.org/hashcash.pdf)
- libp2p Kad-DHT spec [github.com/libp2p/specs/kad-dht](https://github.com/libp2p/specs/blob/master/kad-dht/README.md)
- libp2p PR #422 — *Provider records use multihashes instead of CIDs* [github.com/libp2p/go-libp2p-kad-dht#422](https://github.com/libp2p/go-libp2p-kad-dht/pull/422)
- BEP-51 sample_infohashes [bittorrent.org/beps/bep_0051](https://www.bittorrent.org/beps/bep_0051.html)
- bitmagnet [github.com/bitmagnet-io/bitmagnet](https://github.com/bitmagnet-io/bitmagnet)
- Leitão, J., Pereira, J., Rodrigues, L. (2007). *HyParView* DSN. [asc.di.fct.unl.pt/~jleitao/pdf/dsn07-leitao.pdf](https://asc.di.fct.unl.pt/~jleitao/pdf/dsn07-leitao.pdf)
- Ethereum discv5 [github.com/ethereum/devp2p/discv5-rationale](https://github.com/ethereum/devp2p/blob/master/discv5/discv5-rationale.md)
- Wang, L. & Kangasharju, J. (2012). *Real-World Sybil Attacks in BitTorrent Mainline DHT.* [nymity.ch/sybilhunting/pdf/Wang2012a.pdf](https://nymity.ch/sybilhunting/pdf/Wang2012a.pdf)
- Bühler, S. A. (Dec 2024). *Mainline DHT — Censorship Resistance Explained.* Pubky. [medium.com/pubky](https://medium.com/pubky/mainline-dht-censorship-explained-b62763db39cb)
- Lesniewski-Laas, C. (2010). *Whānau: A Sybil-proof Distributed Hash Table* NSDI. [pdos.csail.mit.edu/papers/whanau-nsdi10.pdf](https://pdos.csail.mit.edu/papers/whanau-nsdi10.pdf)
- Pkarr (2023–2024) [pubky.github.io/pkarr](https://pubky.github.io/pkarr/)
- *Securing Distributed Hash Tables using Proofs of Space* (2025). eprint.iacr.org/2025/804.
- *Sybil Attack Strikes Again: Denying Content Access in IPFS* (ARES 2024). DOI 10.1145/3664476.3664482.
- Vitalik Buterin (2023). *What do I think about biometric proof of personhood?* [vitalik.eth.limo](https://vitalik.eth.limo/general/2023/07/24/biometric.html)
