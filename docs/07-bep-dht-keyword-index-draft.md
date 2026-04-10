# Draft BEP: DHT Keyword Index via BEP-44 Mutable Items

| Field | Value |
|---|---|
| Title | DHT Keyword Index via BEP-44 Mutable Items |
| Version | 1 |
| Last-Modified | 2026-04-10 |
| Author | The SwartzNet Authors |
| Status | Draft |
| Type | Standards Track |
| Created | 2026-04-10 |

## Abstract

This document specifies a convention for using
[BEP-44][bep44] mutable items to publish "keyword → list of
infohashes" mappings on the BitTorrent mainline DHT. A
publisher writes one mutable item per (publisher_pubkey,
keyword) pair; a searcher computes the same DHT target,
fetches the item, verifies its signature, and reads the hit
list out of the bencoded value.

The convention requires no new BEP-5 query types, no changes
to BEP-44 itself, and no coordination beyond agreeing on the
shape of the bencoded `v` field. It is fully backwards
compatible with every BEP-44-capable DHT node already on the
mainline network — those nodes serve our items under the same
rules they serve any other BEP-44 mutable item without
needing to know what we are doing with them.

[bep44]: https://www.bittorrent.org/beps/bep_0044.html

## Motivation

[BEP-3][bep3] / [BEP-5][bep5] give us a content-addressable
peer discovery network: "given an infohash, find peers". They
do not give us a *content discovery* network. The user has to
already know an infohash to fetch a torrent.

Existing solutions either:

1. Run their own overlay (Tribler / IPv8, eDonkey / Kad).
2. Push the discovery problem to centralised tracker
   websites + scraping.
3. Sit it on the .torrent file metadata layer (BEP-46
   "mutable torrents") which is publisher-pull only and
   cannot answer free-text queries.

Yet we already have a 10 million-node DHT willing to store
arbitrary 1000-byte mutable items per BEP-44. This document
proposes the simplest possible convention to use that
storage for keyword → infohash lookups: each publisher
controls their own ed25519 namespace, salts each keyword
under their pubkey, and signs the resulting value. Searchers
fan out across known publisher pubkeys in parallel and merge
the results.

[bep3]: https://www.bittorrent.org/beps/bep_0003.html
[bep5]: https://www.bittorrent.org/beps/bep_0005.html

## Rationale

### Why per-publisher namespaces and not a global keyword pool

A "global" pool — one DHT target per keyword regardless of
who publishes it — would let any node mint hits for any
keyword and would be impossible to defend against spam. A
namespaced approach lets each publisher build their own
reputation and lets searchers trust or distrust them
individually. This is the lesson aMule's Kad network learned
the hard way (see `docs/03-p2p-search-protocols.md` §1.4).

### Why BEP-44 and not a new DHT verb

BEP-44 already gives us:

- Signed payloads (ed25519, 64-byte signatures, mandatory
  verification by BEP-44-aware nodes).
- A namespace mechanism (the `salt` field of the `put`).
- Sequence numbers and CAS for safe concurrent updates.
- A 1000-byte payload cap that is more than enough for ~25-40
  hits per keyword.
- Refresh semantics (re-announce within 2h to keep an item
  alive; 1h is the recommended interval).

A new DHT verb would require getting BitTorrent.org to
allocate a method name and then waiting for clients to
implement it. BEP-44 exists today and is already widely
deployed.

### Why ed25519 and not RSA / Schnorr / ECDSA

Because BEP-44 already mandates ed25519. We do not need to
introduce a new signature scheme — the storage layer
verifies signatures for us.

## Specification

### Identity

Every publisher MUST own a long-lived [ed25519] keypair. The
public key is the publisher's identity in this network. The
private key is used to sign every BEP-44 mutable item the
publisher writes; it MUST NOT be shared.

Implementations SHOULD persist the keypair locally with
restrictive filesystem permissions (e.g. `0600` on POSIX).

### Targets

Each (publisher, keyword) pair maps to a unique BEP-44
mutable-item target computed as:

```
salt    = utf8_lowercase_token(keyword)   ; max 64 bytes per BEP-44
target  = SHA1(publisher_pubkey || salt)  ; per BEP-44
```

The salt is the bare keyword bytes for shard 0. Publishers
that need to spread hits across more than ~25-40 entries
MUST shard by appending `#<n>` for `n >= 1`:

```
salt[0] = keyword
salt[1] = keyword + "#1"
salt[2] = keyword + "#2"
...
```

Shard 0 SHOULD set its `more` field (see below) to `1` so
searchers know to fetch the additional shards.

### Tokenisation

Implementations SHOULD tokenise torrent names with the
following minimum rules:

- Lowercase Unicode letters and digits.
- Split on every other character.
- Drop tokens shorter than 3 bytes.
- Drop common file-extension noise (`mp4`, `iso`, `pdf`, …)
  to avoid creating DHT hot spots on those keywords.
- Drop language-appropriate stop-words.
- Cap the number of published keywords per torrent (the
  reference implementation uses 8).

The exact stop-word list and cap are implementation
choices; the goal is that two implementations following
these rules will agree on enough keywords for the same
torrent that lookups will find it.

### Value schema

The bencoded `v` field of a mutable item MUST be a
dictionary matching this shape:

```
v = {
  "ts":   <int, unix timestamp at which the snapshot was generated>,
  "hits": [
    {
      "ih":  <20-byte SHA-1 infohash>,
      "ih2": <32-byte SHA-256 infohash>,    ; OPTIONAL, BEP-52 hybrid
      "n":   "short torrent name",          ; OPTIONAL
      "s":   <int, seeders last seen>,      ; OPTIONAL
      "f":   <int, file count>,             ; OPTIONAL
      "sz":  <int, size in bytes>           ; OPTIONAL
    },
    ...
  ],
  "more": <int, 1 if shards 1+ exist, 0 otherwise>   ; OPTIONAL, defaults to 0
}
```

The encoded form of `v` MUST be ≤ 1000 bytes (the BEP-44
hard cap). Implementations MUST reject larger payloads at
publish time and MUST shard.

Field names use the same short tags as the M3 sn_search wire
format so a single Go struct can serve both transports. This
is convention, not requirement; future versions MAY add
additional optional keys.

### Signing

The signature is exactly the ed25519 signature defined by
BEP-44 over the canonical buffer:

```
4:salt<len>:<salt>3:seqi<seq>e1:v<bencoded-len>:<bencoded v>
```

(Per BEP-44, the buffer is the literal byte string above
with placeholders filled in. Storing nodes verify against
the publisher's public key.)

### Refresh schedule

Publishers MUST re-publish every live entry at least once
every 2 hours (the BEP-44 expiry). The reference
implementation re-publishes hourly.

Publishers MAY back off the refresh schedule for entries
that have not changed (same `seq`, same `v`) but MUST NOT
allow any entry to expire while it is still considered
live.

### Searcher behaviour

A searcher receives a free-text query and:

1. Tokenises it the same way the publishers do.
2. Picks the most distinctive token (longest non-stopword).
3. Computes `salt = lowercase(token)`.
4. For each known indexer pubkey `pk`:
   - Computes `target = SHA1(pk || salt)`.
   - Issues a BEP-44 `get` for `target`.
   - On success, verifies the signature.
   - Decodes the `v` field as a `KeywordValue` dict.
5. Merges the resulting hit lists by infohash, summing
   reputation scores across publishers, and returns the
   merged list to the user.

Searchers MUST tolerate per-indexer errors (timeout, missing
target, signature failure) without aborting the overall
lookup. Per the design document, even a single responding
indexer is enough to be useful.

### Indexer pubkey discovery

This document does not mandate a specific mechanism for
learning indexer pubkeys. The reference implementation
supports four sources, in increasing order of trust:

1. **Self-pubkey.** A node always queries its own pubkey
   so it can find its own freshly-published entries during
   single-node testing.
2. **Hardcoded seeds.** A small list of well-known
   community indexers shipped with the client. Empty in
   the v1 reference release.
3. **Gossip via sn_search peer_announce.** When two peers
   speak the M3 `sn_search` extension, the responder MAY
   include its own publisher pubkey in `peer_announce`
   messages. The recipient SHOULD add it to the local
   indexer set.
4. **User-supplied.** A CLI command or config file can add
   pubkeys explicitly.

### Reputation

This document does not specify a reputation system; that
is a strictly local concern of each implementation. The
reference implementation maintains per-pubkey counters
(`hits_returned`, `hits_confirmed`, `hits_flagged`) and a
Bayesian-smoothed score which it uses both to gate which
indexers are queried at all and to rank merged hits. Users
are encouraged but not required to implement something
similar.

## Backwards Compatibility

This convention is fully backwards compatible:

- Vanilla BEP-44 storage nodes serve our items under the
  same rules they serve any other mutable item. They do not
  need to understand what is in the `v` payload.
- Vanilla searchers that do not know the convention are
  unaffected. They never hit our targets unless they
  manually compute the same SHA1.
- BEP-3 / BEP-5 traffic is unaffected. We add no new DHT
  verbs and no new query types.

The only requirement on the network is that mainline DHT
nodes implement BEP-44 (still formally Draft, but widely
implemented in the major libraries since ~2014).

## Reference Implementation

A complete reference implementation in Go ships in the
SwartzNet client at `internal/dhtindex/`:

- `tokenize.go` — tokenisation rules with stop-word and
  extension filtering.
- `schema.go` — `KeywordValue` and `KeywordHit` structs
  with the canonical bencode field names, plus
  `EncodeValue` / `DecodeValue` and salt helpers.
- `dht.go` — `Putter` / `Getter` interfaces with
  `AnacrolixPutter` / `AnacrolixGetter` production
  implementations and a `MemoryPutterGetter` test double.
- `manifest.go` — persistent per-keyword manifest with
  oldest-first eviction when an entry would overflow the
  1000-byte cap.
- `publisher.go` — long-running worker with refresh
  ticker.
- `lookup.go` — parallel fan-out across known indexer
  pubkeys with merge-by-infohash.

The reference implementation has 24 unit tests covering
encoding, manifest persistence, publisher worker behaviour,
and lookup fan-out with reputation + Bloom filter
integration. All run under `go test -race`.

## Security Considerations

### Spam

The biggest known threat is publisher spam — anyone with an
ed25519 keypair can publish whatever they want under their
own namespace. The reference implementation defends against
this with two mechanisms documented in
`docs/05-integration-design.md` §4.3:

1. **Per-pubkey reputation** maintained locally. Searchers
   skip indexers below a configurable score and demote
   their hits in the merge.
2. **Known-good infohash Bloom filter.** Hits whose
   infohash the user has previously downloaded
   successfully are boosted to the top of the result list.

Neither mechanism is mandatory, but any implementation
intending to ship to real users SHOULD implement both or
something similar.

### DDoS

A flood of `put` traffic to the same target (popular
keyword) is rate-limited by the storing nodes per BEP-44's
existing protections. We add no new attack surface.

### Privacy

A searcher's IP is visible to every DHT node it queries
(including the storing nodes for the target). The keyword
itself is hashed into the target via SHA1, so the plaintext
keyword does not appear on the wire — but a global observer
running a dictionary of common keywords can reverse the
hash.

If stronger privacy is required, layer the entire DHT
client behind a SOCKS5 proxy / Tor / VPN. This document
does not propose anonymity; it proposes discovery.

### Identity rotation

A user may rotate their publisher keypair at any time. The
old pubkey's published entries will expire after 2h
without refresh. There is no protocol-level mechanism for
"linking" the old and new pubkeys — that is intentional,
as it preserves the user's ability to start over with a
clean reputation if they need to.

## References

- BEP-3: The BitTorrent Protocol Specification
- BEP-5: DHT Protocol
- BEP-44: Storing Arbitrary Data in the DHT
- BEP-46: Updating Torrents via DHT Mutable Items
- BEP-52: BitTorrent v2
- The companion `06-bep-sn_search-draft.md` describing the
  peer-wire extension.
- `docs/05-integration-design.md` — full SwartzNet
  architecture and design rationale.
- `docs/03-p2p-search-protocols.md` — survey of prior art
  including aMule/Kad's keyword-hash DHT.

## Copyright

This document is placed in the public domain.
