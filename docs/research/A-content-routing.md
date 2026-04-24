# A — Content-routing and content-discovery systems, and what SwartzNet can learn from them

Companion to `docs/05-integration-design.md`. The SwartzNet baseline:

- **L** = local Bleve full-text index.
- **S** = `sn_search` BEP-10 extension over peer-wire.
- **D** = BEP-44 mutable items, `salt = keyword`, `target = SHA1(pubkey || salt)`, with a hardcoded + gossip-discovered set of indexer pubkeys.

Everything below is read through the constraint from CLAUDE.md: **no new reserved bit, no new DHT verb, no new UDP port**. Wire compatibility with vanilla BitTorrent is load-bearing.

---

## 1. IPFS / libp2p

### 1.1 What it does at protocol level

IPFS runs the **libp2p Kademlia DHT** over streams (TCP/QUIC), not datagrams like mainline DHT. Messages are length-prefixed protobuf. The core verbs are `FIND_NODE`, `PUT_VALUE`, `GET_VALUE`, `ADD_PROVIDER`, `GET_PROVIDERS`. Critically, IPFS separates **provider records** (who has this CID?) from **value records** (what is stored under this key?) — mainline BEP-44 collapses both into one primitive.

- `ADD_PROVIDER`: an announcement that a peer holds a CID. TTL is 24 hours, refreshed by the provider. Stored at the ~20 nodes closest to `H(CID)` (K=20 in libp2p vs K=8 in mainline BEP-5).
- `GET_PROVIDERS`: returns a list of peer multiaddrs that claim to have the CID. Response size is bounded; clients iterate via further `FIND_NODE` lookups toward `H(CID)`.
- Peer IDs are hashes of public keys (Ed25519, RSA, secp256k1). This is S/Kademlia-style sybil mitigation.

**BitSwap** (the actual content-exchange protocol, layered on top of the DHT) is a protobuf wire protocol with four primitives: `WANT-HAVE`, `WANT-BLOCK`, `HAVE`, `DONT-HAVE` + inline `BLOCK`. Each message carries a wantlist (entries: CID, priority, cancel flag, wantType, sendDontHave) and a block list. Peers broadcast want-haves to all connected peers concurrently; the first peer to answer `HAVE` becomes the source for `WANT-BLOCK`. This is basically what BitTorrent's `HAVE` + `REQUEST` do, except content-addressed per-block instead of per-piece-in-a-torrent, and the "interested / choke / unchoke" dance is replaced by direct haves/don't-haves.

**Reframe / Delegated Routing V1 (IPIP-378)** replaces DHT walking with a tiny HTTP API: `GET /routing/v1/providers/{cid}` returns up to 100 JSON `{ID, Addrs, Protocols}` records; `GET /routing/v1/peers/{peerID}` and IPNS `GET/PUT` complete the trio. ndjson streaming is supported for big result sets. This is the interesting piece for SwartzNet: Protocol Labs effectively admitted that walking the DHT for every resolution is too slow for web-scale, and gave everyone a boring HTTP fallback. The DHT still exists; it's just no longer the hot path.

**IPNI (InterPlanetary Network Indexer)**: a content-routing layer completely outside Kademlia. Providers publish **advertisement chains** — IPLD-linked DAGs of multihashes — to an announcement pubsub topic (gossipsub). Indexer nodes (e.g. cid.contact) ingest advertisements, maintain a multihash→provider mapping in Pebble/RocksDB, and serve lookups via Delegated Routing V1. Providers sign advertisements with their peer key; they can add/remove mappings with `EntryChunk.IsRm`. Readers can ask any indexer.

**Reader privacy** (relevant to SwartzNet's §9 threat model): IPNI supports a "double-hashed" mode where the lookup key is `H(H(CID) || salt)` and provider records are symmetrically encrypted under a key derived from the CID, so the indexer can store and serve without ever learning what CID is being queried.

**GossipSub v1.1** is the pubsub layer. Peers maintain a mesh (degree D≈6) per topic, forward messages eagerly to the mesh and gossip IHAVE/IWANT lazily to non-mesh peers. v1.1 adds a per-peer score function (mesh time, message delivery, invalid messages, application-specific) so misbehaving peers get pruned. Flood publish (with score filter) is used for a node's own messages to counter eclipse attacks.

**Rendezvous protocol** (`/rendezvous/1.0.0`) is a light-weight topic-based peer discovery: peers `REGISTER` at a rendezvous server with a namespace string and TTL; `DISCOVER` returns peers that registered. Meant as infrastructure for pubsub bootstrap.

### 1.2 What SwartzNet should adopt / reject

**Adopt the IPNI pattern, loosely, for indexer-pubkey discovery.** The hardcoded seed-pubkey list in §4.3 of 05-integration-design.md is the exact problem IPNI solves: "which indexers should I ask?" A minimal port would be: SwartzNet publishes a tiny **indexer-advertisement item** to mainline DHT under a well-known salt (e.g. `salt = "snet.indexers.v1"`, `target = SHA1("" || salt)` = `SHA1("snet.indexers.v1")`), but since BEP-44 requires a pubkey this has to be a set of ~10 "anchor" pubkeys plus a community-editable registry. The indexer pubkeys then publish under their own namespaces as today, but discovery is no longer hardcoded. This is covered in §5 below.

**Adopt Delegated Routing V1 verbatim, as an HTTP fallback**, not on the wire. A `swartznet` node could expose `GET /routing/v1/snet/keyword/{term}` returning indexer pubkeys known to serve that term. If someone later runs a public aggregator (analogous to cid.contact), queries get O(1) instead of O(log N) DHT walks. This does not touch the BitTorrent wire and is strictly additive.

**Reject BitSwap.** It's a direct competitor to BitTorrent's rarest-first exchange, not an addition. Adopting it would break mainline compatibility — which is the one constraint we can't break.

**Reject GossipSub on a separate port.** SwartzNet already has a pubsub-like thing in the `peer_announce` LTEP message (§5.2.4 of 05-integration-design.md). GossipSub's score function is worth borrowing into the reputation system (§4.3 there) but we should not add a second pubsub network.

**Adopt IPNI-style double-hashed keyword lookups as a v2 privacy mode.** Today SwartzNet's salt is the plaintext keyword; a DHT node serving the item can reverse it (cheap dictionary). The fix: `salt = H(keyword || nonce)` where `nonce` is published alongside the hardcoded indexer pubkey. Indexers know the mapping; passive observers do not. Zero wire change (BEP-44 allows arbitrary salts up to 64 bytes).

### 1.3 Mainline compatibility

- Delegated Routing V1 as HTTP: **no wire impact**.
- IPNI-inspired indexer registry as a BEP-44 item: **no wire impact** (it's a standard BEP-44 item).
- BitSwap: **would break compat** (parallel block-exchange protocol). Don't.
- GossipSub: **would add a new UDP/TCP overlay**. Don't.
- Double-hashed salts: **no wire impact**; the salt is opaque bytes to mainline nodes already.

---

## 2. Hypercore / Hyperswarm / Holepunch (Keet, Pear, HyperDHT)

### 2.1 What it does at protocol level

**Hypercore** is the foundational primitive: a signed, append-only log. Each entry is keyed by integer index; the log has a single writer whose Ed25519 keypair signs blocks. Replication uses a Merkle tree (sparse downloading: request entries 100..200 and the proof path). Entries are immutable once written. This is closer to an `.epub` sequence than to a k/v store.

**Hyperbee** is a B-tree embedded in a Hypercore. Keys and values are arbitrary bytes; the tree is serialized as append-only blocks (each mutation writes a new root pointing at new internal nodes, old nodes are still valid). Prefix scans work because keys are lex-ordered — this makes Hyperbee a natural **range-queryable key-value store over a replicated log**, and directly usable as an inverted index: `key = keyword + "/" + infohash`, `value = {name, size, seeders, ...}`. Because Hyperbee is "embedded indexing" (tree nodes live inside the same Hypercore as the values), readers pull the subset they need — no full replication required.

**Autobase** layers multi-writer semantics on Hypercore. Each writer has their own Hypercore; Autobase sorts a causal DAG of writer inputs into a single linearized stream and applies it to produce a "view" Hyperbee. The view is rewritten when the ordering changes (but only up to a "checkpoint" that has stabilized). This is CRDT-adjacent: arbitrary conflict resolution, but because the output is a linear Hypercore you can serve it to read-only subscribers who never saw the original writers.

**Hyperswarm DHT (HyperDHT)** is a Kademlia-over-UDP with two notable features:

1. **First-class hole-punching.** A node publishes itself under a topic (32-byte discovery key derived from a Hypercore's pubkey); peers looking up the topic get both the target's addresses and a **relay node that will help hole-punch**. The DHT nodes aren't just address books, they're active participants in NAT traversal. UDP punch attempts are randomized (default `randomPunchInterval = 20s`) to defeat NATs that punish predictable probes.
2. **Ed25519-keyed server discovery.** `dht.announce(topic, keyPair)` publishes a signed record; `dht.lookup(topic)` returns peers that proved knowledge of `keyPair.publicKey`. This is the "connect-by-pubkey" primitive — you don't need an IP, just a 32-byte public key.

`mutablePut(keyPair, value)` / `mutableGet(publicKey, {seq, latest})` are directly comparable to BEP-44: same 1KB-ish value limit, same `seq` monotonicity, same Ed25519 signatures. The one API addition is `latest: true` which tells the DHT to keep polling for higher `seq` until it stops going up — a convenience for "fetch the freshest version" rather than "fetch the version known to this specific node." Mainline BEP-44 clients have to implement this themselves.

**DEP-0001** was the Dat Enhancement Proposal process; the actual protocol reference for modern Hypercore is the Pear/Holepunch docs, since Dat was deprecated in favour of Hypercore v10 (~2020).

### 2.2 What SwartzNet should adopt / reject

**Adopt the `latest:true` polling pattern in our BEP-44 client.** When resolving a keyword, we currently take the first reply from the first-contacted storage node. Hyperswarm's `latest:true` semantics — "keep asking different replicas until `seq` stops rising" — would give us fresher results with a small constant factor more DHT traffic. Pure client-side change, no wire impact.

**Adopt Hyperbee's "embedded index" idea for companion-index torrents.** The §11 post-v1 "companion-index torrent" in 05-integration-design.md is currently sketched as a BEP-46 pointer to a blob. If we serialized the companion index as a Hyperbee-style append-only B-tree *inside the torrent*, readers could sparsely download just the keyword ranges they need. BitTorrent already has piece-level random access (BEP-3); a B-tree laid out with large branching factor (say 256) on piece boundaries means one root piece + log₂₅₆(N) levels of pieces to hit any keyword. Much lighter than "download the whole index and grep." Does not touch the BitTorrent wire; it's purely a layout inside the piece blob.

**Reject Autobase's linearization-and-rewrite model.** It produces a consistent shared view but at the cost of writers needing to see each other's heads. SwartzNet's publisher model is pseudonymous and disjoint (each pubkey owns its own namespace); we don't need multi-writer CRDT over a shared index.

**Reject connect-by-pubkey.** The BitTorrent swarm is infohash-keyed, not pubkey-keyed, and we have no sessions bound to a persistent identity. Adding it would duplicate the trackerless-DHT lookup already done by `announce_peer`.

### 2.3 Mainline compatibility

- `latest:true` polling: **no wire impact** (client-side retry logic).
- Hyperbee layout inside a piece blob: **no wire impact** (the torrent is just another multi-file torrent).
- Autobase: would require a new DHT semantic. Reject on that ground alone.
- HyperDHT itself: **incompatible** (separate UDP network, Ed25519-keyed everything). We reuse mainline DHT instead.

---

## 3. Iroh (n0-computer)

### 3.1 What it does at protocol level

Iroh is the spiritual successor to IPFS for "connect two programs by key, send bytes." Three layers:

1. **Endpoint dialing.** You get a 32-byte Ed25519 `NodeId` and dial it. Iroh's control plane uses a pool of **public relay servers** (DERP-like, running over HTTPS) that forward encrypted traffic while peers attempt direct QUIC hole-punching. Once a direct path is found, traffic switches over transparently (QUIC multipath, in newer versions). If direct fails, the relay carries the session for its lifetime.
2. **iroh-blobs.** BLAKE3-verified streaming. A request specifies `{hash, ranges}`; the sender streams the requested byte ranges plus the BLAKE3 proof path. BLAKE3 allows O(log N) proof verification per 1 KB chunk, so a receiver can verify as the stream flows. Conceptually equivalent to BEP-52's SHA-256 Merkle tree but with BLAKE3 (faster) and byte-range granularity instead of piece granularity.
3. **Tickets.** A ticket is a base32-encoded string bundling `{NodeId, relay hint URL, optional blob hash / doc namespace}`. Sharing a ticket over a sidechannel (URL, QR code) gives the recipient everything needed to dial and fetch. This is the "magnet link for iroh" and is the single user-visible primitive for sharing.

**iroh-gossip** runs HyParView (active/passive view membership) + Plumtree (eager/lazy message push) per topic. The reference implementation keeps active view = 5, passive view = 30, and allows topic-scoped gossip without any DHT.

**iroh-docs / iroh-willow** gives replicated keyed documents. `iroh-docs` is the pre-Willow implementation, using range-based set reconciliation (RBSR, see §4 below) to sync a `(namespace, author, key) → value` triple-keyed store between peers. `iroh-willow` is the in-progress port of the full Willow spec.

### 3.2 What SwartzNet should adopt / reject

**Adopt the ticket primitive as a new share format.** A `swartznet://...` ticket could bundle `{infohash, signed_by_pubkey, trackers, recommended indexer pubkeys}` in a single base32 string. This is strictly more useful than a bare magnet link because it also seeds the recipient's indexer set. No wire impact — it's a text format. We already have `snet.pubkey` in `.torrent` metainfo (§0 of 11-signing-protocol.md); extending magnet URIs with `x.pk=...&x.idx=...,...` is a small CLI/UI change.

**Reject iroh-blobs.** BLAKE3 streaming is nicer than BitTorrent pieces, but adopting it means adopting a parallel content-exchange protocol. Hard no.

**Reject iroh-gossip for Layer S.** HyParView+Plumtree would give us a bigger-than-swarm keyword search fabric, but it requires persistent membership state and new messages. Our `peer_announce` LTEP gossip stays close enough to BEP-11 PEX that vanilla clients can ignore it; HyParView would not.

**Adopt Iroh's "relay as fallback, direct as goal" architecture as a mental model** for future NAT traversal work. Mainline BitTorrent has UPnP, NAT-PMP, and µTP hole-punching (BEP-29) + PEX; those remain sufficient. But when we ship multi-hop message routing for privacy (currently out of scope per §9), adopting Iroh's pattern — always try direct, always have a relay — is the right shape.

### 3.3 Mainline compatibility

- Tickets: **no wire impact**.
- BLAKE3 streaming, gossip, QUIC relay: all would require new ports / protocols. Reject.

---

## 4. Dat / Hypercore v10 / Willow Protocol / Earthstar (and range-based set reconciliation)

### 4.1 What it does at protocol level

**Willow** (Aljoscha Meyer + Sam Gwilym, 2023+) is a data model specifically designed around **3D range-based set reconciliation**. The model:

- Each record is an `Entry` with `{namespace_id, subspace_id, path, timestamp, payload_digest, payload_length, author}`.
- Entries live in a 3-dimensional space `(subspace_id, path, timestamp)`. A 3dRange is a rectangular region in that space.
- **Meadowcap** is the capability system: a `McCapability` is a chain of Ed25519 signatures, each delegating access to an `Area` (a restricted form of 3dRange) with read or write semantics. Capabilities are unforgeable and composable — "the owner of this namespace grants the bearer read access to subspace X's path prefix /photos/ until timestamp T."

**WGPS (Willow General Purpose Sync Protocol)** runs over an encrypted transport (spec-external, typically libp2p Noise). It uses LCMUX (logical channel multiplexing) to separate control, reconciliation, data, and payload-request channels so slow payloads can't block fingerprint exchange. Key messages:

- `PioBindHash`, `PioAnnounceOverlap`, `PioBindReadCapability` — Private Interest Overlap lets two peers discover they're interested in the same areas without revealing interests they don't share. Implemented via hashed commitments.
- `ReconciliationSendFingerprint {range, fingerprint}`
- `ReconciliationAnnounceEntries {range, count}`
- `ReconciliationSendEntry {entry, capability_handle}`
- `ReconciliationSendPayload {entry_handle, chunk}` / `ReconciliationTerminatePayload`

Range-based set reconciliation works like this (Meyer 2023, arXiv:2212.13567):

1. Peers have totally-ordered sets of items. Each peer computes a **fingerprint** over a range as `SHA256(Σ ID_i mod 2²⁵⁶ || count)` truncated to 16 bytes. Note the XOR/sum trick: fingerprints are **incrementally recomputable** (add/remove element by XOR).
2. Peer A sends `(range_bound_lo, range_bound_hi, fingerprint_A)`.
3. Peer B computes its own fingerprint over the same range. If equal, they're done for that range.
4. If different, B subdivides the range at the median of its local elements and returns either:
   - Fingerprints for each sub-range (another round), or
   - The raw ID list (if the range is small enough — `Mode=IdList` in Negentropy).
5. A compares ID lists, learns what's missing, requests those entries.

Bandwidth is O(d · log N) where d is the symmetric difference. Over 1 M items with 10 differences, sync is a handful of round-trips and a few KB.

**Negentropy** (hoyt.ec / strfry) is the concrete wire protocol for RBSR used by Nostr NIP-77. Its byte format:

```
Message     := <version=0x61> <Range>*
Range       := <upperBound> <mode varint> <payload>
Bound       := <timestamp-delta varint> <idPrefix-length varint> <idPrefix bytes>
mode 0 Skip        : payload empty
mode 1 Fingerprint : 16-byte truncated SHA256
mode 2 IdList      : varint count, then ids
```

Timestamps are delta-encoded; IDs are only sent with enough bytes to disambiguate within the range. This is dense and self-contained — a complete Negentropy exchange for near-synced sets is often under 1 KB.

**Earthstar** is a personal-data app using Willow. Its model: each person has a keypair, `writes` entries into shared namespaces, and sync is WGPS. Conflict resolution is last-write-wins by timestamp, tie-broken by author pubkey.

### 4.2 What SwartzNet should adopt / reject

**Adopt RBSR for Layer S (sn_search) peer-to-peer keyword-index sync.** This is the single highest-value finding in this research. Today, Layer S is request/response: a client asks for keyword X, the peer answers with matching torrents. That's fine for ad-hoc queries but wasteful for **peers who want to mirror a publisher's full keyword index**. If SwartzNet's Bleve index is viewable as a sorted set of `(pubkey, keyword, infohash, seq)` tuples, two peers can reconcile via a Negentropy-style message in a new LTEP sub-type `sn_search.msg_type=4 rbsr`.

Concretely: the set element is `(keyword || infohash)` ordered lex, fingerprint truncated SHA-256 as above. Two peers exchange 3-5 round-trips (each a bencoded LTEP message, `<2KB`) and converge on a union. This replaces N individual keyword queries with a full index sync. This is particularly valuable for:

- **Bootstrapping new indexers.** A new node running Swartznet can sync a reputable indexer's full index in a few dozen messages instead of weekly DHT polls.
- **Keeping gossip-discovered indexer pubkeys hot.** Currently pubkeys we learned via `peer_announce` go stale unless we poll them for every keyword of interest. RBSR-pull once, keep it fresh with deltas.

Wire-compat impact: **zero** — it's a new `sn_search.msg_type` inside LTEP. Vanilla clients never see it.

**Adopt Meadowcap-style capabilities** only if we ever let third parties publish under our pubkey. The current model (each publisher = one pubkey) doesn't need delegation. But if we allow "curator pubkey C delegates write to subkey D for the /sci-fi prefix of their index," that's exactly what McCapability chains encode. Reserve the design space; don't implement yet.

**Reject the full 3D Willow model.** Three-dimensional namespaces, `author × path × timestamp`, is vastly more machinery than we need. Our index is effectively 1D (keyword string). The RBSR algorithm generalizes trivially to 1D; skip the 3D framing.

**Private Interest Overlap is elegant but premature.** It protects peer A from revealing "I care about keyword X" to peer B unless B also cares. For keyword search this is roughly what Tor would give us, via a different mechanism. Given our §9 "not in scope: end-to-end query privacy" stance, skip PIO for v1; revisit as a named opt-in feature.

### 4.3 Mainline compatibility

- RBSR over LTEP `sn_search`: **no wire impact**. It's an LTEP sub-type; mainline clients don't advertise `sn_search` and so never receive it.
- Meadowcap: if used only inside signed `.torrent` `snet.*` fields, zero wire impact. The capability chain is a blob.
- 3D Willow: rejected, so moot.

---

## 5. Nostr

### 5.1 What it does at protocol level

Nostr is **JSON events over WebSocket to a set of client-chosen relays**. Each event is `{id, pubkey, created_at, kind, tags, content, sig}` with `id = SHA256(serialized event)` and `sig` an Ed25519 (schnorr-secp256k1 in the current spec) signature. Relays store events, forward matching events to subscribers, and drop or rate-limit at discretion. There is **no peer-to-peer layer**; all communication is client↔relay.

Key wire messages:

- `["REQ", <sub_id>, <filter1>, <filter2>, ...]` — subscribe with NIP-01 filters (authors, kinds, `#e` tag, `#p` tag, since, until, limit).
- `["EVENT", <sub_id>, <event>]` — relay → client event delivery.
- `["EOSE", <sub_id>]` — end of stored events (live events follow).
- `["CLOSE", <sub_id>]`.
- `["NEG-OPEN", <sub_id>, <filter>, <neg_hex>]` — NIP-77 Negentropy sync initiation.

**NIP-50 search.** Adds a `search` field to REQ filters: `{"search": "best nostr apps"}`. Relays interpret the string in an implementation-specific way (FTS on `content`, optional `language:`, `domain:`, `nsfw:` key/value extensions). Results are ordered by relay-scored quality, not timestamp. Explicit note in the spec: *"clients should query multiple relays supporting NIP-50 to compensate for different implementation details."*

**NIP-77 Negentropy** is described under §4 above; it's the production RBSR implementation.

### 5.2 What SwartzNet should adopt / reject

**Adopt NIP-50's "query-many, merge client-side" model** — which we already do via multi-pubkey fan-out in Layer D. Nostr validates the pattern at scale.

**Adopt NIP-50's extension syntax** (`keyword:value` key/value pairs inside the free-text query string) for SwartzNet's search box. Instead of dedicated `signed_by` / `lang` / `min_size` fields in the UI, support `signed_by:abc123 lang:en size:>1gb ubuntu 24.04`. This composes well and matches user muscle memory from GitHub/Jira/Nostr.

**Reject the relay model wholesale.** A relay is a trusted third party that sees all events. Substituting SwartzNet's mainline DHT for relays is exactly what Layer D already does, minus the trust issue. We'd regress by introducing relays.

**Reject kind-based event dispatch.** Nostr's kind numbers (0 = metadata, 1 = note, 30023 = long-form) are a weak schema. SwartzNet's equivalent is BEP-44 mutable items with structured `v`; tighter and already signed.

### 5.3 Mainline compatibility

- Query-syntax extensions: **no wire impact** (client UI + local parser).
- Multi-source merge: **no wire impact** (we do it already).
- Relays: would be a new service. Reject.

---

## 6. Veilid (Cult of the Dead Cow, 2023)

### 6.1 What it does at protocol level

Veilid is Rust, mobile-first, "IPFS + Tor" positioning. Every node gets a 256-bit public key (Ed25519 + x25519) and speaks **VLD0** (XChaCha20-Poly1305, Ed25519, x25519, BLAKE3, Argon2). The key primitive is **RoutingContext** — every RPC goes through a user-selected combination of:

- **Safety route** (sender side): a source-routed path of N hops you control, to hide your origin. You pick the hops and their keys.
- **Private route** (receiver side): a path of N hops the destination controls, advertised as a `RouteId`. You send to a RouteId, not to a node's real address.

Both directions together = full onion routing, Tor-style, but source-controlled on both ends so neither side trusts the network to pick hops for them. Default hop count = 1 extra (i.e., `me → hop1 → hop2 → you` with hop2 picked by receiver), so "reasonable privacy with low latency" is the opt-in state.

**Veilid DHT** has two record schemas:

- `DFLT` — default, one writer (the owner) per record, N subkeys, each subkey individually addressable. Subkeys can be signed and stored separately.
- `SMPL` — simple multi-writer, N subkeys, each subkey bound to a specific member pubkey, so specific subkeys have specific writers.

RPC is `GetValue(key, subkey, seq)` / `SetValue(key, subkey, value)` — with signatures and schema-enforced write permissions. Nodes can opt out of DHT storage (be a pure client) without breaking discovery.

### 6.2 What SwartzNet should adopt / reject

**Adopt the DFLT/SMPL schema distinction, as a future v2 for indexer pubkeys.** Today our BEP-44 mutable item is single-writer (the indexer's key). If we wanted multiple curators of a themed index ("/sci-fi" under indexer C), SMPL's "subkey N is writable by pubkey X" gives us the exact shape. Note: mainline BEP-44 doesn't support this at the protocol level (there's no subkey concept). We'd encode it in the `v` payload — a dict of `{subkey: {writer_pubkey, value, sig}}` with verification in the client. Wire-level: still one BEP-44 item. Semantically: multi-writer.

**Safety/private routes are the one real "adopt this for privacy" recommendation in this whole document.** BUT — mainline DHT traffic is UDP on a single port; adding source-routed hops means wrapping DHT messages in a new outer envelope, which is a new protocol. Not compatible with BEP-5/44. If SwartzNet ever ships a privacy mode, it must be a fully separate stack (run our BEP-44 client over Tor or I2P SAM) rather than bolted onto mainline DHT. Veilid's primitive is educational; not directly adoptable.

**Reject VLD0 cipher suite.** We already have Ed25519 via BEP-44 and `snet.sig`. Adding XChaCha20 / BLAKE3 adds dependencies without gain — we're not encrypting at the cryptographic layer, we're relying on the swarm's byte-integrity (piece hashes).

### 6.3 Mainline compatibility

- SMPL-style multi-writer encoded in BEP-44 `v`: **no wire impact** (just a richer payload schema).
- Safety/private routes: **incompatible** with single-port mainline DHT.

---

## 7. CAN / Pastry / Tapestry / GNUnet (classical revisit)

### 7.1 What they do at protocol level

- **CAN** (Ratnasamy et al. 2001): d-dimensional Cartesian coordinate space, each node owns a zone. Key = point in space (hash(key) mod grid). Lookup in O(d · N^(1/d)) hops. Splits zones on join. Interesting for range queries along an axis; useless for keyword strings unless you embed them in a metric space.
- **Pastry** (Rowstron-Druschel 2001): prefix-based routing, O(log₂_b N) with b=4 typically. Leaf set + routing table. Basis of PAST storage and FreePastry. Very close in shape to Kademlia.
- **Tapestry** (Zhao et al.): similar to Pastry with explicit "publish a pointer" step — a publisher announces `(object_id, node_id)` along the route from `node_id` to the root of `object_id`, letting intermediate nodes cache pointers. This **replicated pointer trail** is a clever alternative to BEP-44's "put to closest 8": pointers live on the path, not just at the destination, so a lookup can short-circuit anywhere along the way.
- **GNUnet** (already covered in `03-p2p-search-protocols.md`): KBlock (keyword hash → file identifier), encrypted under a key derived from the keyword so only people who know the keyword can decrypt the pointer. This is the original "double hashing" idea, years before IPNI.

### 7.2 What SwartzNet should adopt / reject

**Adopt Tapestry's publish-along-path idea, carefully.** When we BEP-44 `put` a keyword item to the 8 closest nodes, the 8-20 intermediate Kademlia nodes we walked through drop the put (they just return `find_node` responses). If SwartzNet were willing to have those nodes also accept and serve our BEP-44 item, lookups would terminate anywhere on the path. But BEP-44 storage is defined only at the K-closest nodes; asking intermediaries to store is a protocol change. **Reject** for wire-compat reasons; remember it for custom-DHT post-v1 work.

**Adopt GNUnet's keyword-encrypted pointer** as the basis of SwartzNet's double-hashing privacy mode (§1.2 above). `value = Encrypt(k=KDF(keyword), {infohash, metadata})` means a DHT node storing it has no idea what keyword it's for. Readers who know `keyword` can derive `salt = H(keyword)` + decrypt. Wire-compat: **zero**; it's still a BEP-44 item, just with opaque `v`.

**Reject CAN and Pastry.** Nothing they do is better for keyword search than BEP-44/Kademlia with our layer on top.

### 7.3 Mainline compatibility

- Keyword-encrypted payload: **no wire impact**.
- Publish-along-path: **incompatible** with BEP-44 storage rules.

---

## 8. Cross-cutting: privacy of "who searches for what"

Summarized across the above:

| System | Query-privacy mechanism | Cost |
|---|---|---|
| Mainline BitTorrent | None (infohash visible in `get_peers`) | Zero |
| SwartzNet today | Weak — salt = keyword is reversible by dictionary | Zero |
| IPNI double-hash | Salt = H(H(CID) ‖ nonce), value AES-GCM-encrypted | Tiny; needs a shared mapping |
| GNUnet KBlock | Value encrypted under KDF(keyword) | Small constant |
| Willow PIO | Hashed-commitment overlap detection | Extra round-trip |
| Tor / I2P (layered) | Full onion routing | ~2-5× latency |
| Veilid | Safety+private routes, user-picked hops | Latency + complexity |

The cheapest privacy upgrade SwartzNet can do today, **with zero wire change**, is the GNUnet-style keyword-encrypted `v` payload: `salt = H(keyword)` stays the same (a passive observer of a single target is unchanged), but the stored value is opaque without the keyword. Indexer nodes that don't know the keyword cannot enumerate content they're storing.

---

## 9. References

- libp2p kad-DHT spec: <https://github.com/libp2p/specs/blob/master/kad-dht/README.md>
- IPFS BitSwap: <https://specs.ipfs.tech/bitswap-protocol/>
- Delegated Routing V1: <https://specs.ipfs.tech/routing/http-routing-v1/>
- IPNI specs: <https://github.com/ipni/specs/blob/main/IPNI.md>, reader privacy: <https://github.com/ipni/specs/blob/main/reader-privacy.md>
- GossipSub v1.1: <https://github.com/libp2p/specs/blob/master/pubsub/gossipsub/gossipsub-v1.1.md>
- Rendezvous: <https://github.com/libp2p/specs/blob/master/rendezvous/README.md>
- HyperDHT: <https://github.com/holepunchto/hyperdht>
- Hyperbee: <https://github.com/holepunchto/hyperbee>
- Autobase: <https://github.com/holepunchto/autobase>
- iroh-blobs: <https://github.com/n0-computer/iroh-blobs>
- iroh-gossip: <https://docs.rs/iroh-gossip/latest/iroh_gossip/>
- iroh-docs: <https://docs.rs/iroh-docs/latest/iroh_docs/>
- iroh-willow: <https://github.com/n0-computer/iroh-willow>
- Willow protocol: <https://willowprotocol.org/>, sync: <https://willowprotocol.org/specs/sync/index.html>
- Meadowcap: <https://github.com/earthstar-project/meadowcap-js>
- range-reconcile lib: <https://github.com/earthstar-project/range-reconcile>
- RBSR paper (Meyer 2023): <https://arxiv.org/abs/2212.13567>
- Nostr NIP-50 (search): <https://github.com/nostr-protocol/nips/blob/master/50.md>
- Nostr NIP-77 (Negentropy): <https://github.com/nostr-protocol/nips/blob/master/77.md>
- strfry Negentropy: <https://github.com/hoytech/strfry/blob/master/docs/negentropy.md>
- Veilid: <https://veilid.com/how-it-works/rpc/>, private routing: <https://veilid.com/how-it-works/private-routing/>
- DHT survey (Lua et al. 2005): <https://www.cl.cam.ac.uk/research/dtg/archived/files/publications/public/mp431/ieee-survey.pdf>
