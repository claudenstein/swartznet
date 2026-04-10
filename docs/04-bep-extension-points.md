# BitTorrent Protocol Extension Points for Distributed Text Search

This report catalogs the BitTorrent Enhancement Proposals (BEPs) relevant to
building an opt-in, backwards-compatible distributed text search layer on top
of the existing BitTorrent ecosystem. It focuses on the two primary extension
points we have available — the BEP-10 peer-wire extension protocol and the
BEP-44 DHT key/value store — and walks through the exact wire formats we would
use.

All BEP numbers and quoted text are pulled from the canonical specifications at
`https://www.bittorrent.org/beps/`.

---

## 1. Relevant BEPs

### BEP-3 — The BitTorrent Protocol Specification (core, for context)

BEP-3 defines the original BitTorrent peer wire protocol: the TCP handshake,
the piece/bitfield/request/piece message set, and the .torrent file format
(SHA-1 `info` dict → infohash). The handshake is a fixed 68-byte structure:

```
pstrlen  = 0x13                      (1 byte, = 19)
pstr     = "BitTorrent protocol"     (19 bytes)
reserved = 8 bytes, all zero         (8 bytes)   <-- extension bits live here
info_hash = 20 bytes (SHA-1)
peer_id   = 20 bytes
```

BEP-3 explicitly reserves those 8 bytes for extensions: "eight reserved bytes,
which are all zero in all current implementations. If you wish to extend the
protocol using these bytes, please coordinate with Bram Cohen." After the
handshake, the two peers exchange length-prefixed messages: `<u32
length><u8 msg_id><payload>`. All integers on the wire are big-endian.

**Relevance to us:** this is the base layer. Our client MUST speak BEP-3 byte
for byte; that guarantees we can download from any vanilla client and any
vanilla client can download from us. Anything we add lives either inside BEP-10
extended messages (on-wire) or inside BEP-44 DHT items (off-wire).

---

### BEP-4 — Reserved and Unregistered Reserved Bit Assignments

BEP-4 is a registry of which of the eight reserved handshake bytes are claimed
by which extension. Notable assignments:

- `reserved[5] & 0x10` → LTEP (BEP-10 Extension Protocol)
- `reserved[7] & 0x01` → DHT (BEP-5)
- `reserved[7] & 0x02` → Peer Exchange
- `reserved[7] & 0x04` → Fast Extensions
- `reserved[7] & 0x10` → v2 upgrade hint
- `reserved[0] & 0x80` → legacy Azureus Messaging Protocol

BEP-4 explicitly recommends not grabbing a new reserved bit: "It is recommended
that further extensions use the Extension Protocol [1], a.k.a., LibTorrent
Extension Protocol (LTEP). With LTEP, extension bit collisions become
impossible since no new extension bits are allocated."

**Relevance to us:** we do **not** take a new reserved bit for our search
extension. We announce support through the BEP-10 `m` dictionary instead. This
keeps us off the registry and collision-free.

---

### BEP-5 — DHT Protocol (Kademlia)

BEP-5 defines the mainline DHT: a Kademlia overlay on UDP in which node IDs
are 160-bit strings, distance is XOR, and each node keeps a routing table
split into k-buckets. All messages are bencoded dictionaries ("KRPC") with
three universal top-level keys:

- `t` — transaction ID (opaque string)
- `y` — `"q"` (query), `"r"` (response), or `"e"` (error)
- `v` — optional client version string

Queries add `q` (method name) and `a` (arguments dict); responses add `r`
(return values dict). There are exactly **four** query types:

1. `ping` — args: `{id: <20-byte node id>}`
2. `find_node` — args: `{id, target}`, returns `nodes` (compact node info)
3. `get_peers` — args: `{id, info_hash}`, returns either `values` (list of
   compact peer infos) or `nodes` plus a `token` for the follow-up announce
4. `announce_peer` — args: `{id, info_hash, port, token}`

Critically, the BEP-5 DHT **only stores peer contact information keyed by
infohash**. A node "store[s] the IP address of the querying node and the
supplied port number under the infohash in its store of peer contact
information." There is no provision in BEP-5 itself for storing arbitrary
blobs, keyword indexes, or anything else that isn't `(infohash → [peer])`.

BEP-5 also contains **no extension mechanism for new query types**. There's
no `m`-dictionary equivalent, no "supported methods" advertisement, no version
negotiation. Any query whose `q` string is unknown is, per convention, answered
with an error (`y: "e"`, code 204 "Method Unknown") or silently dropped.

**Relevance to us:** BEP-5 gives us the overlay network itself — the set of
~10 million DHT nodes and their routing tables — but its four built-in queries
do not let us store keyword → infohash mappings. For storage we need BEP-44.
For new query verbs we rely on the "unknown method → 204" behaviour; see
§4 (DHT extension patterns).

---

### BEP-9 — Extension for Peers to Send Metadata Files (`ut_metadata`)

BEP-9 lets a peer fetch the .torrent `info` dictionary from another peer over
the peer wire, given only the infohash. This is what makes magnet links work.
It's the canonical example of a BEP-10 extension.

The extension advertises itself in the BEP-10 handshake by adding
`"ut_metadata"` to the `m` dictionary, plus a top-level `metadata_size`
integer. Example decoded handshake:

```
{'m': {'ut_metadata': 3}, 'metadata_size': 31235}
```

Three message types, distinguished by the `msg_type` integer:

- `0` = **request**, payload `{msg_type: 0, piece: <int>}`
- `1` = **data**, payload `{msg_type: 1, piece: <int>, total_size: <int>}`
  followed by up to 16 KiB of raw metadata bytes concatenated after the
  bencoded dict
- `2` = **reject**, payload `{msg_type: 2, piece: <int>}`

A peer MUST verify that the SHA-1 of the reassembled metadata matches the
infohash before serving it. Peers without complete (verified) metadata send
`reject` for every piece.

**Relevance to us:** BEP-9 is the architectural precedent we copy. Our
`lt_search` extension (or whatever name we pick) looks exactly like
`ut_metadata`: it's advertised in the BEP-10 `m` dict, uses message ID 20 on
the wire, and carries bencoded application-level messages keyed by an internal
`msg_type`. If two search-capable peers connect, they negotiate `lt_search`
during the BEP-10 handshake just as they do `ut_metadata` today.

---

### BEP-10 — Extension Protocol (LTEP) — **our primary extension point**

BEP-10 is the single most important BEP for our design. It defines a
lightweight in-band handshake that lets two peers negotiate named extension
messages without burning reserved bits and without requiring BitTorrent.org to
allocate new message IDs.

#### Wire-format fundamentals

1. **Reserved-bit advertisement.** A client supporting LTEP sets bit 20
   (counting from the right, zero-indexed) in the 8-byte reserved field of the
   BEP-3 handshake. Concretely: `reserved_byte[5] & 0x10` is the LTEP support
   check. (Both sides must have this bit set before either sends an extended
   message.)

2. **Extended-message envelope.** All LTEP messages reuse BitTorrent message
   ID **20**. The full frame on the wire is:

   ```
   uint32_t  length       (big-endian; counts the bytes after this field)
   uint8_t   msg_id = 20  (BitTorrent "extended" message type)
   uint8_t   ext_msg_id   (0 = handshake; >0 = a specific extension)
   payload   (bencoded dict; may have raw-byte trailer, see ut_metadata)
   ```

3. **Handshake message (`ext_msg_id == 0`).** Immediately after the BEP-3
   handshake, each side sends one LTEP handshake. The payload is a bencoded
   dictionary. The only mandatory key is `m`, a dict mapping extension names
   (strings) to extended message IDs (integers) **chosen locally by the
   sender**. Optional top-level keys include:

   - `p` — our TCP listen port
   - `v` — UTF-8 client name/version string
   - `yourip` — compact representation (4 or 16 bytes) of the peer's IP as
     seen by us
   - `ipv4` / `ipv6` — our own interface addresses
   - `reqq` — integer, max outstanding requests we accept (libtorrent
     default 250)

   The canonical example from BEP-10 itself:

   ```
   d1:md11:LT_metadatai1e6:ut_pexi2ee1:pi6881e1:v13:µTorrent 1.2e
   ```

   Decoded:

   ```
   {
     "m": { "LT_metadata": 1, "ut_pex": 2 },
     "p": 6881,
     "v": "µTorrent 1.2"
   }
   ```

   The remote peer reads that dict and remembers: "if I want to send this peer
   an `LT_metadata` message, I use `ext_msg_id = 1`; if I want to send a
   `ut_pex` message, I use `ext_msg_id = 2`." These IDs are **per-direction
   and per-peer** — the values are chosen by the *receiver* and used by the
   *sender*. Each side may announce totally different numeric IDs.

4. **Additive updates.** "Subsequent handshakes may be sent to enable/disable
   extensions; the m dictionary is additive." Setting an extension's value to
   `0` disables it.

5. **Negotiation rule.** If peer A advertises `lt_search` in its `m` dict and
   peer B does not, then peer B will silently ignore any `lt_search` message
   peer A sends (or, more likely, A simply won't send any — it checks B's
   handshake first). **An unknown extended message ID should be dropped.**
   This is how opt-in search stays invisible to vanilla clients.

#### Example: adding `lt_search` to our client

Suppose our client wants to open a search channel with a peer. Our LTEP
handshake looks like:

```
{
  "m": {
    "ut_metadata": 2,
    "ut_pex": 1,
    "lt_search":  7
  },
  "metadata_size": 31235,
  "p": 51413,
  "v": "SwartzNet 0.1",
  "reqq": 250
}
```

That tells the remote peer: "if you want to send me a search message, use
extended-message-ID 7 inside an ID-20 frame." The remote peer inspects our
`m` dict, sees `lt_search: 7`, and — if it also supports search — will do the
same in its own handshake (with whatever numeric ID it chose for itself).

Concretely, after bencoding (omitting the outer length prefix for clarity),
our LTEP handshake frame on the wire is:

```
00 00 00 5b           length = 0x5b bytes follow
14                    msg_id = 20 (LTEP extended)
00                    ext_msg_id = 0 (LTEP handshake)
64 31 3a 6d 64 ...    bencoded {'m': {...}, ...} payload
```

#### Example: sending an `lt_search` query

Suppose both peers have negotiated `lt_search` and the remote advertised
`lt_search: 9` in its `m` dict (so we address messages to it with
`ext_msg_id = 9`). We decide to define our own two internal message types,
using an inner `msg_type` integer exactly like BEP-9 does:

- `msg_type = 0` — search query
- `msg_type = 1` — search results
- `msg_type = 2` — reject / not available

A query with payload `{"q": "foo bar", "limit": 20}` plus our message type
bookkeeping is:

```
{
  "msg_type": 0,
  "txid":     42,
  "q":        "foo bar",
  "limit":    20
}
```

Bencoded:

```
d5:limiti20e8:msg_typei0e1:q7:foo bar4:txidi42ee
```

That's 47 bytes of bencoded payload. The complete LTEP frame is:

```
00 00 00 31           length prefix = 49 bytes after this field
14                    msg_id = 20 (LTEP)
09                    ext_msg_id = 9 (== remote's advertised lt_search)
64 35 3a 6c 69 6d     bencoded payload 'd5:lim...'
69 74 69 32 30 65
38 3a 6d 73 67 5f
74 79 70 65 69 30
65 31 3a 71 37 3a
66 6f 6f 20 62 61
72 34 3a 74 78 69
64 69 34 32 65 65
```

Length = 1 (msg_id 20) + 1 (ext_msg_id 9) + 47 (payload) = 49 bytes, which is
`0x31`. The remote peer reads msg_id 20, sees ext_msg_id 9, looks up
its own `m["lt_search"]` → "this is an lt_search message", parses the
bencoded payload, sees `msg_type: 0`, and dispatches it to its search
handler.

A response might be:

```
{
  "msg_type": 1,
  "txid":     42,
  "hits": [
    { "ih": <20-byte infohash>, "name": "foo bar OST", "score": 0.9 },
    { "ih": <20-byte infohash>, "name": "foo bar movie", "score": 0.7 }
  ]
}
```

…shipped back inside an ID-20 frame addressed with *our* advertised
`lt_search` number.

#### Why BEP-10 is the right primary extension point

- **Opt-in by construction.** Vanilla clients don't advertise `lt_search`, so
  we never send them search messages. They, in turn, don't understand ours.
  Dropping an unknown extended message ID is explicitly allowed.
- **No registry needed.** We choose our own extension name (conventionally
  prefixed, e.g. `lt_search` for "libtorrent-style search") and use it.
- **Already universally supported.** Every non-trivial modern client (µTorrent,
  qBittorrent, Transmission, libtorrent, Deluge, WebTorrent, anacrolix, …)
  implements BEP-10. The LTEP bit is set on virtually every peer we'll meet.
- **Works for streaming and for one-shots.** We can use it for synchronous
  query/response, or for long-lived subscriptions (push notifications of new
  results).

---

### BEP-11 — Peer Exchange (PEX, `ut_pex`)

BEP-11 defines `ut_pex`, a BEP-10 extension that gossips peer contact info
between peers in the same swarm. The message payload is a bencoded dict:

```
{
  "added":     <compact IPv4+port list of newly-known peers>,
  "added.f":   <1 flag byte per added peer>,
  "added6":    <IPv6 compact list>,
  "added6.f":  <flags>,
  "dropped":   <compact IPv4+port list of disconnected peers>,
  "dropped6":  <IPv6 compact list>
}
```

Rate limit: no more than one `ut_pex` message per minute, and (after the
initial message) no more than 50 added+dropped entries per version family.
Only *verified* peers — ones we've successfully handshaked with — should appear
in `added`.

**Relevance to us:** PEX is both a prior art example of a BEP-10 extension we
can mimic *and* a possible piggyback point. We could define a parallel
`lt_search_pex` message that gossips "known search-capable peers" (by IP:port)
alongside the regular per-swarm peer set, or we could simply use `ut_pex`
delivery and check which of those gossiped peers support `lt_search` via
their own LTEP handshake when we connect.

A simpler model: every time we LTEP-handshake with a peer that advertises
`lt_search`, record that peer. Ask *them* for more search-capable peers via a
dedicated `lt_search` sub-message (`msg_type = "peers"` or similar). That way
our search overlay is gossip-discovered rather than DHT-discovered, and it
maps onto the same swarms as regular BitTorrent.

---

### BEP-14 — Local Service Discovery (LSD)

BEP-14 is the LAN peer discovery mechanism: every 5 minutes per interface, a
client multicasts an HTTP-over-UDP packet to `239.192.152.143:6771` (IPv4) or
`[ff15::efc0:988f]:6771` (IPv6):

```
BT-SEARCH * HTTP/1.1\r\n
Host: <host>\r\n
Port: <port>\r\n
Infohash: <40-char hex>\r\n
cookie: <cookie (optional)>\r\n
\r\n
\r\n
```

Multiple `Infohash:` headers may be stacked. Packets must stay under 1400
bytes.

**Relevance to us:** mostly tangential. We could define an LSD-like multicast
for announcing search capability on the LAN, but for a WAN-scale distributed
index this is a minor nicety. Worth mentioning only because the verb is
literally `BT-SEARCH`, which is historical and unrelated to text search — do
not confuse them.

---

### BEP-18 — Search Engine Specification (Deferred)

BEP-18 defines a `.btsearch` XML file that acts like an OpenSearch description
document: it tells a client how to POST a user's query to some *external*,
*centralised* search engine. It is essentially browser-style search-provider
metadata, not a distributed protocol. Status: **Deferred**, apparently because
the community lost interest and it duplicates OpenSearch.

**Relevance to us:** effectively none for the distributed case, but it's worth
noting that the BEP number "18" is already taken and any future search BEP we
submit will have its own number. If we eventually standardise our extension we
may want to mark BEP-18 as obsoleted-in-spirit.

---

### BEP-42 — DHT Security Extension

BEP-42 ties DHT node IDs to IP addresses. The valid ID prefix is derived as:

- IPv4: `crc32c((ip & 0x030f3fff) | (r << 29))`
- IPv6: `crc32c((ip & 0x0103070f1f3f7fff) | (r << 61))`

where `r ∈ [0, 7]`. The first 21 bits of the node ID must match the CRC32C,
and the last byte must equal `r`. This prevents Sybil attacks where an
attacker spawns many fake nodes near a target infohash.

**Relevance to us:** any DHT-level extension we build must respect the BEP-42
constraint, otherwise modern clients will treat our nodes as untrusted and
evict them from their routing tables. If we store keyword indexes in the DHT
(via BEP-44 or our own verbs), the nodes holding that data must still have
IDs derived from their IPs. This doesn't change our protocol; it just means
our node-ID generator is a standard one.

---

### BEP-43 — Read-only DHT Nodes

BEP-43 adds a top-level `ro: 1` flag to KRPC messages sent by nodes that cannot
accept incoming queries (e.g. behind strict NAT, mobile battery-constrained).
Read-only nodes send queries but do not respond to any, and they ask that they
not be added to routing tables.

**Relevance to us:** if our client is on a mobile device and wants to consult
the DHT (for either peer lookups or BEP-44 search index entries) without
committing to serving queries back, we set `ro: 1` on outgoing KRPC. This is
orthogonal to the search design but worth honoring.

---

### BEP-44 — Storing Arbitrary Data in the DHT — **critical**

BEP-44 is the other major extension point for our design. It adds two new DHT
query verbs — `get` and `put` — that let nodes store **arbitrary bencoded
blobs up to 1000 bytes** in the DHT, either keyed by content (immutable) or
keyed by an ed25519 public key (mutable). This is how we store per-keyword
index entries.

#### The 1000-byte limit

Straight from BEP-44: "Storing nodes MAY reject put requests where the
bencoded form of v is longer than 1000 bytes." The limit applies to the
bencoded representation of `v`, not the cleartext. Error code 205 ("Message
too large") signals a rejection.

#### Immutable items

Immutable items are keyed by the SHA-1 of their bencoded value. You cannot
modify them (the key is the hash of the content). No authentication is
required.

**PUT (immutable):**

```
{
  "a": {
    "id":    <20-byte node id>,
    "token": <write token from previous get>,
    "v":     <any bencoded type, size <= 1000>
  },
  "q": "put",
  "t": <txid>,
  "y": "q"
}
```

**GET (immutable):** `{id, target}` → `{id, token, v, nodes, nodes6}`. The
looker-up MUST verify `SHA1(bencode(v)) == target`.

#### Mutable items

Mutable items are keyed by `SHA1(public_key || salt)` (or just
`SHA1(public_key)` when no salt is used). The key-holder can publish new
values under the same key by bumping a monotonic `seq` counter and signing
`(salt, seq, v)` with ed25519.

**PUT (mutable):**

```
{
  "a": {
    "id":    <20-byte node id>,
    "k":     <32-byte ed25519 public key>,
    "salt":  <optional, <= 64 bytes>,
    "seq":   <monotonic sequence number>,
    "sig":   <64-byte ed25519 signature>,
    "token": <write token>,
    "v":     <bencoded value, <= 1000 bytes>,
    "cas":   <optional expected current seq>
  },
  "q": "put",
  "t": <txid>,
  "y": "q"
}
```

**GET (mutable):** `{id, target, seq?}` → `{id, k, seq, sig, token, v, nodes,
nodes6}`. If the caller supplies `seq`, responders MAY skip sending `v`/`sig`
when their stored `seq` is no higher than the one requested (saves bandwidth
when polling for updates).

**Signing buffer.** The signature covers a bencoded concatenation
(deliberately chosen so it looks like a partial bencoded dict but is not one
itself):

```
(if salt present)  "4:salt" <len(salt)> ":" <salt>
                   "3:seqi" <seq> "e"
                   "1:v"   <len(v)> ":" <v>
```

So for `salt = "foobar"`, `seq = 4`, `v = "Hello world!"` the signed buffer is
literally the bytes:

```
4:salt6:foobar3:seqi4e1:v12:Hello world!
```

The `sig` field is a 64-byte ed25519 signature over exactly that byte string.
A validator reconstructs the same string from the PUT's arguments, looks up
the public key `k`, and verifies the signature. Nodes that receive an invalid
signature reply with error code 206 ("invalid signature").

**CAS.** The optional `cas` field is "compare-and-swap": the storing node
writes only if the currently stored sequence number equals `cas`. Mismatches
return error 301 ("CAS mismatch"). This is our tool for racing publishers of
the same key.

**Monotonicity.** `seq` must be strictly increasing. Storing nodes "MUST not
downgrade a list head from a higher sequence number to a lower one." Error
302 means "sequence number less than current."

**Tokens.** Tokens for `put` work exactly like `get_peers` → `announce_peer`
in BEP-5: you first `get` to get a short-lived token from the storage node,
then you `put` using that token. This prevents spoofed-source writes.

**Expiry / refresh.** "Without re-announcement, these items MAY expire in 2
hours. In order to keep items alive, they SHOULD be re-announced once an
hour." Refresh is just another `put` with the same (or higher) seq, issued
roughly hourly by the publisher. Republishing from the publisher is not
strictly required if more than 8 copies exist and the 8 closest nodes hold the
latest seq.

**No append.** BEP-44 is overwrite-only. There is no append primitive. If we
want a growing list (e.g. "infohashes for keyword 'foo'"), we must:

1. Put the whole list into one mutable item, bumping seq on each update; or
2. Shard across multiple items (each one a different salt or a different
   keypair) and have readers union them; or
3. Structure the item as a pointer to a larger payload distributed elsewhere
   (e.g. an infohash that points to a torrent containing the real index
   shard).

For a 1000-byte blob that's about 40 × 20-byte infohashes, or fewer if we
include names / timestamps / weights. Our design must account for this.

**Relevance to us:** BEP-44 is exactly what we need to publish keyword → {list
of infohashes} mappings as first-class DHT entries. We keypair per publisher
(or per shard), use the keyword string as salt, and the 20-byte target
(`SHA1(pubkey || salt)`) is what a searcher looks up. "All published items
under pubkey P whose salt is keyword K" is a single well-defined DHT target.
With careful chunking we can store a keyword index natively on mainline DHT.

Caveat: BEP-44 is still status **Draft**, but it has been implemented in
libtorrent and several clients for years, and it's the mechanism BEP-46 relies
on, so effective deployment is broad.

---

### BEP-46 — Updating Torrents via DHT Mutable Items

BEP-46 builds directly on BEP-44. It defines a convention: a mutable DHT item
whose `v` is `{ "ih": <20-byte infohash> }` acts as a "pointer" to the current
version of a torrent. Consumers reference it via a magnet URL of the form

```
magnet:?xs=urn:btpk:<hex public key>&s=<hex salt>
```

Periodically, clients `get` that mutable item, read `v["ih"]`, and if it
differs from what they have, they fetch the new torrent. Bumping `seq` and
`put`-ing a new infohash is how the publisher rolls out an update.

**Relevance to us:** BEP-46 is a direct precedent for our design — it shows
that the community already uses BEP-44 for "small mutable pointers indexed by
human-chosen salts." Our keyword index is the same pattern, with (keyword →
list of infohashes) replacing (name → single infohash). A user who subscribes
to "all torrents by publisher P tagged 'foo'" is effectively doing BEP-46 over
our salt namespace.

---

### BEP-51 — DHT Infohash Indexing (`sample_infohashes`) — **extension
precedent**

BEP-51 is the single clearest precedent for "new DHT query verbs added by
clients without any protocol-level negotiation." It introduces one new query,
`sample_infohashes`, whose purpose is to let an indexing node efficiently
sample the universe of infohashes known to the DHT without having to passively
watch `get_peers` traffic.

**Request:**

```
{
  "a": { "id": <20 byte id>, "target": <20 byte id> },
  "q": "sample_infohashes",
  "t": <txid>,
  "y": "q"
}
```

**Response:**

```
{
  "r": {
    "id":       <20 byte id>,
    "interval": <seconds until resample is allowed>,
    "nodes":    <compact node info near target>,
    "num":      <total infohashes in local storage>,
    "samples":  <N * 20 bytes of random infohashes>
  },
  "t": <txid>,
  "y": "r"
}
```

Nodes that don't implement `sample_infohashes` reply with error 204 "method
unknown" (or drop the query). Nodes that *do* implement it recognize the `q`
string and populate `samples`. This works — because the unknown-method path is
already defined by BEP-5 — without any protocol-level extension negotiation.

**Relevance to us:** this is the pattern we copy if we decide to add a new DHT
verb directly (e.g. `search_keyword`). We do not need the community to bless a
new reserved bit or a handshake flag; we just start sending the new query.
Nodes that don't know it ignore it; nodes that do handle it. Section 4 expands
this into a general pattern.

---

### BEP-52 — BitTorrent v2 (SHA-256 Merkle Trees)

BEP-52 is the v2 torrent format. Key differences from BEP-3:

- Hashes are SHA-256 instead of SHA-1, so the v2 infohash is 32 bytes.
- The file list is replaced by a hierarchical `file tree` dict:

  ```
  info: {
    file tree: {
      dir1: {
        dir2: {
          "fileA.txt": {
            "": { "length": 12345, "pieces root": <32-byte merkle root> }
          }
        }
      }
    }
  }
  ```

- Each file has its own merkle root ("pieces root"), constructed with
  branching factor 2 over 16 KiB blocks. That means a file can be independently
  addressed by its own hash without knowing the full torrent.
- Hybrid torrents (v1 + v2) keep both the old `pieces` string and the new
  `file tree` simultaneously, aligned on 16 KiB boundaries, so a single
  .torrent is indexable by both a SHA-1 infohash and a SHA-256 infohash.

**Relevance to us:** if we want to index per-file rather than per-torrent
(e.g. "find all torrents that contain a file named 'README.md' with this merkle
root") BEP-52 gives us stable per-file identifiers. In a world of hybrid
torrents, a single file's `pieces root` is effectively a content-addressable
name usable across torrents that repackage the same file. We should design our
index record format so that a hit can optionally carry a per-file merkle root
(`pr`) in addition to a torrent-level infohash (`ih`). Future versions can
then deduplicate hits across torrents.

---

### BEP-1 — The BEP Process (for §6)

BEP-1 is the process doc for proposing new BEPs. Covered in section 6 below.

---

### Other BEPs worth a one-liner

- **BEP-32 (draft)** — IPv6 extension for DHT (`want` argument on `find_node`
  and `get_peers`). Relevant only insofar as our BEP-44 puts will need to work
  on dual-stack nodes.
- **BEP-33 (draft)** — DHT scrape; lets nodes ask for swarm-size histograms.
  Orthogonal.
- **BEP-36 (draft)** — torrent RSS feeds. Centralised; superseded in spirit by
  BEP-46.
- **BEP-49 (draft)** — distributed torrent feeds, layered on BEP-46. Closest
  cousin to our proposal: it uses BEP-44 mutable items as feed publication
  points. We should study its chunking scheme before designing ours.
- **BEP-55 (draft)** — Holepunch extension; unrelated but commonly advertised
  via BEP-10 so it's another example of the pattern we copy.

---

## 2. The BEP-10 Extension Message Flow (byte-level walkthrough)

Let's trace a full search interaction between our client **S** and a peer
**P** that also supports our extension.

### Step 1: BEP-3 handshake with LTEP reserved bit set

Both sides send the 68-byte fixed handshake. The 8-byte reserved block has
`reserved[5] = 0x10` (the LTEP support bit). If P also has
`reserved[5] & 0x10`, both sides know the other speaks LTEP and will send an
LTEP handshake next.

```
S → P:  \x13 BitTorrent protocol \x00\x00\x00\x00\x00\x10\x00\x00 <infohash> <S's peer_id>
P → S:  \x13 BitTorrent protocol \x00\x00\x00\x00\x00\x10\x00\x04 <infohash> <P's peer_id>
                                                  ^^^^  P also sets the DHT bit (0x04 on byte 7)
```

### Step 2: LTEP handshake (extended message ID 0)

S sends its handshake announcing `lt_search: 7`, meaning "if you want to send
me a search message, use ext_msg_id 7."

Bencoded payload:

```
d1:md11:ut_metadatai2e6:ut_pexi1e9:lt_searchi7ee13:metadata_sizei31235e1:pi51413e4:reqqi250e1:v13:SwartzNet 0.1e
```

Frame on the wire:

```
<u32 length = 1 + 1 + payload_len>
<u8  20>            msg_id = 20 (LTEP)
<u8  0>             ext_msg_id = 0 (handshake)
<bencoded payload bytes>
```

P parses S's handshake, notes `lt_search: 7`, and remembers "to talk search to
S I send ext_msg_id = 7 inside an ID-20 frame." P then sends its own LTEP
handshake:

```
d1:md11:ut_metadatai3e6:ut_pexi2e9:lt_searchi11ee13:metadata_sizei31235e1:pi6881e4:reqqi250e1:v16:qBittorrent 5.0e
```

S now knows: "to talk search to P I send ext_msg_id = 11 inside an ID-20
frame." The numbers differ in each direction — that's normal.

**Negotiation failure mode.** If P's handshake did **not** contain
`lt_search`, S simply never sends a search message to P. Nothing breaks; the
rest of the BitTorrent session proceeds as normal. Likewise, if P sends S an
`ext_msg_id` that S doesn't recognize, S drops that frame. "Drop unknown"
is the universal rule.

### Step 3: sending the search query

S wants to ask P `{"q": "foo bar", "limit": 20}`. S wraps it in a dispatch
envelope with `msg_type: 0`:

```
payload_dict = {
  "msg_type": 0,
  "txid":     42,
  "q":        "foo bar",
  "limit":    20
}
bencoded     = d5:limiti20e8:msg_typei0e1:q7:foo bar4:txidi42ee   (47 bytes)
```

Frame:

```
00 00 00 31            length = 49  (1 + 1 + 47)
14                     msg_id = 20
0b                     ext_msg_id = 11 (== P's advertised lt_search)
64 35 3a 6c 69 6d      d 5 : l i m
69 74 69 32 30 65      i t i 2 0 e
38 3a 6d 73 67 5f      8 : m s g _
74 79 70 65 69 30      t y p e i 0
65 31 3a 71 37 3a      e 1 : q 7 :
66 6f 6f 20 62 61      f o o   b a
72 34 3a 74 78 69      r 4 : t x i
64 69 34 32 65 65      d i 4 2 e e
```

P reads msg_id 20, ext_msg_id 11, maps that to "lt_search" via its own `m`,
dispatches into its search handler, parses the bencoded payload, sees
`msg_type: 0`, and runs the query against its local index.

### Step 4: receiving results

P assembles a result set:

```
{
  "msg_type": 1,
  "txid":     42,
  "hits": [
    { "ih": <20-byte infohash>, "name": "foo bar OST",   "score": 900 },
    { "ih": <20-byte infohash>, "name": "foo bar movie", "score": 700 }
  ],
  "partial": 0
}
```

P sends this back in a frame with `ext_msg_id = 7` (the ID S advertised for
`lt_search`). S's receive loop sees msg_id 20, ext_msg_id 7, dispatches to
its search-response handler, and matches `txid: 42` to the outstanding query.

### Step 5: optional follow-up

S can now pick an infohash from `hits`, issue a DHT `get_peers` on it (vanilla
BEP-5 behaviour, no extension needed), join the swarm, and BEP-9 the metadata
if it doesn't already have the .torrent. None of these downstream steps
require the other peer to understand search — they use the base protocol.

---

## 3. The BEP-44 Storage Flow (walkthrough)

Scenario: our client C1 wants to publish the fact that keyword `"foo bar"`
indexes three infohashes. C2 later wants to look up that keyword.

### Step 0: keys and naming

C1 owns an ed25519 keypair `(sk, pk)`. It uses the keyword itself as the
salt. The DHT lookup target is:

```
target = SHA1(pk || salt) = SHA1(pk || "foo bar")
```

Any client that knows `pk` and `"foo bar"` can compute the same 20-byte
target and look it up.

### Step 1: encoding the value

`v` must be a bencoded value whose encoded size is ≤ 1000 bytes. A reasonable
schema:

```
v = {
  "ts":   1712649600,           # unix timestamp of this snapshot
  "hits": [
    <20-byte infohash>,
    <20-byte infohash>,
    <20-byte infohash>
  ]
}
```

Encoded: `d4:hitsl20:...20:...20:...e2:tsi1712649600ee` — about 80 bytes, well
under the limit. We can fit roughly 40-45 bare 20-byte infohashes per mutable
item, or ~25 if we include per-hit names. For larger lists, shard: use salts
like `"foo bar#0"`, `"foo bar#1"`, … and maintain a top-level "shard count"
item at salt `"foo bar"`.

### Step 2: signing

Current sequence number: `seq = 1`. The signing buffer (literal bytes) is:

```
4:salt7:foo bar3:seqi1e1:v<len>:<bencoded v bytes>
```

So for our example that's:

```
4:salt7:foo bar3:seqi1e1:v80:d4:hitsl20:...20:...20:...e2:tsi1712649600ee
```

C1 computes `sig = ed25519_sign(sk, that_buffer)`. `sig` is 64 bytes.

### Step 3: get the write token

Before putting, C1 needs a write token from the storage nodes. It does a GET
first, both to discover the 8 closest nodes to `target` and to collect
per-node tokens:

```
get request:
{
  "a": { "id": <C1's node id>, "target": <20-byte target>, "seq": 0 },
  "q": "get",
  "t": "g1",
  "y": "q"
}
```

Each responding node returns a `token` (plus `nodes` for iterative lookup,
and — if it already stores an item — `k`, `seq`, `sig`, `v`). C1 walks the
DHT toward `target` Kademlia-style until it finds the 8 closest nodes. Each
one hands back its own short-lived `token`.

### Step 4: PUT to each of the 8 closest nodes

For each of the 8 closest nodes, C1 sends:

```
put request:
{
  "a": {
    "id":    <C1's node id>,
    "k":     <32-byte pubkey>,
    "salt":  "foo bar",
    "seq":   1,
    "sig":   <64-byte ed25519 signature>,
    "token": <that node's write token>,
    "v":     <bencoded value>
    # "cas" omitted on first publication
  },
  "q": "put",
  "t": "p1",
  "y": "q"
}
```

Each storage node checks: token valid? `v` ≤ 1000 bytes? signature verifies
against `k` over the canonical buffer? If yes, it stores the tuple `(target,
k, salt, seq, sig, v)` and responds `{"r": {"id": ...}, "y": "r"}`. Errors:
205 too large, 206 bad sig, 207 salt too large, 301 CAS mismatch, 302 seq
regressed.

### Step 5: lookup by C2

C2 wants to search for "foo bar". It already knows C1's `pk` (out of band —
the keyword → publisher association is part of our app-level trust model, see
below). It computes `target = SHA1(pk || "foo bar")`, then issues:

```
get request:
{
  "a": { "id": <C2's node id>, "target": <target>, "seq": 0 },
  "q": "get",
  "t": "g2",
  "y": "q"
}
```

…and walks the DHT toward `target`. The 8 nearest nodes return the stored
item. C2:

1. Verifies `SHA1(k || salt) == target` (ensures the salt/pubkey match the
   target it asked for).
2. Reconstructs the canonical signing buffer from `salt`, `seq`, `v`.
3. Verifies `sig` against `k`.
4. Parses `v` and extracts the infohashes.

If several nodes return different `seq` values for the same `target`, C2
takes the one with the highest `seq` (as long as the signature verifies).
That's the "list head" semantics BEP-44 mandates: "storing nodes MUST not
downgrade a list head from a higher sequence number to a lower one."

### Step 6: refresh

Every ~1 hour, C1 republishes the item with the same (or bumped) `seq`. The
get→put cycle is the same; items may expire after 2 hours without refresh.

### Appending — the hard part

BEP-44 has no append primitive. To "add an infohash to the existing list" C1
must:

1. `get` the current item (to learn current `seq`).
2. Decode `v`, append the new infohash.
3. Bump `seq` to `current + 1`.
4. Re-sign.
5. `put` with `cas = current seq`, so concurrent publishers can't silently
   overwrite each other. A CAS failure (301) means we reread and retry.

This is fine for a single-publisher-per-keypair model, but it means that if
we want *crowdsourced* keyword contributions from multiple nodes, each
contributor should use **its own keypair** (and therefore its own target),
and the searcher must union results across contributors it trusts. One common
pattern: the searcher has a known set of "indexer pubkeys" and issues one
GET per (pubkey, keyword) pair, then merges.

### Size-limit workarounds

To get past the 1000-byte value limit we can:

- **Shard by salt.** `salt = f"{keyword}#{shard_index}"`, with a root item at
  `salt = keyword` holding only `{ "n": <num shards> }`. Reader fetches root
  first, then fans out.
- **Pointer item.** Put a small item containing `{ "ih": <infohash of a
  torrent holding the real index> }`, following the BEP-46 pattern. The
  torrent itself is then downloaded via normal BitTorrent and can be
  arbitrarily large.
- **Tombstone + rolling window.** Only keep the most recent N entries inline;
  older entries move to archive shards.

---

## 4. DHT Extension Patterns

### Is there an "LTEP for DHT"?

**No.** BEP-5 does not define a negotiation/handshake mechanism for KRPC. There
is no equivalent of BEP-10's `m` dictionary. Each KRPC query just has a `q`
method-name string, and either the receiving node understands it or it
doesn't.

The BEP-5 error section defines error code 204 "Method Unknown," and in
practice clients either:

- respond with `{"y": "e", "e": [204, "Method Unknown"]}`, or
- simply drop the unrecognized query.

### The precedent: BEP-44, BEP-51, BEP-33

Three existing BEPs add new DHT query verbs without any explicit handshake
layer:

- **BEP-44** added `get` and `put`.
- **BEP-51** added `sample_infohashes`.
- **BEP-33** added `scrape` (in the form of a modified `get_peers` response).

In every case, the deployment model was: publish the BEP, implement in one or
more clients, and let the new verbs propagate. Nodes that know the verb
respond; nodes that don't, error or drop. Over time, client coverage grows.

### The "overlaid DHT" observation

The user's framing is exactly right: **a new DHT verb effectively creates a
logical overlay on top of the same physical Kademlia network**. All nodes
participate in routing (`ping`, `find_node`) because those are universal. But
only *capable* nodes respond to the new verb. A client issuing our
`search_keyword` query will walk the DHT normally, and at each hop, only a
fraction of the nodes contacted will answer meaningfully — the rest will
204/drop. The walker keeps going, retrying with the next-closest node in its
routing table. As long as *some* density of capable nodes exists in the
keyspace near the target, lookups succeed.

This has important consequences for our design:

1. **Bootstrapping density matters.** If only 0.1% of DHT nodes support our
   verb, a Kademlia lookup may have to contact many nodes before getting
   enough useful responses. The 8-node target population for BEP-44 may need
   to grow to a larger effective population (e.g. "16 capable nodes near
   target") to account for the fact that only a subset of the 8 nearest
   physical nodes will understand us.
2. **Capability discovery.** We want a way to mark nodes as "capable." Easy
   patterns:
   - Include a sentinel field in our responses (`"search": 1`) so capable
     nodes can be added to a "capable subset" of our routing table.
   - Piggyback on BEP-10 peer-wire sessions: any peer that advertised
     `lt_search` over the wire is almost certainly a capable DHT node too,
     so we can pre-seed our capable-subset routing table from BEP-10
     connections.
3. **No separate port, no separate DHT.** We don't need to run a *second*
   DHT on a different UDP port. That would forfeit the huge network-effect
   benefit of the existing ~10M-node DHT. Overlaying on top is strictly
   better.

### Two options for storing the keyword index in the DHT

**Option A — use BEP-44 verbatim.** Publish each (publisher, keyword) as a
mutable item. No new verbs, completely standard, works today on any BEP-44
client. The tradeoff is the 1000-byte limit and the shard-management burden.
This is by far the safer default.

**Option B — add a new verb pair** (e.g. `search_put` / `search_get` /
`search_query`). More flexible — we could define larger values, native
full-text matching, relevance scoring, etc. — but requires a critical mass of
supporting nodes to be useful, and means publishing a new draft BEP-style
document. This is the "logical overlay" approach.

**Recommendation:** start with Option A (BEP-44 mutable items under a salt =
keyword scheme), measure, then decide whether Option B is worth the complexity.

### Things a new DHT verb must NOT do

- **Do not break existing verbs.** Any new verb must be named so that it
  doesn't collide with `ping`, `find_node`, `get_peers`, `announce_peer`,
  `get`, `put`, `sample_infohashes`. Prefix-namespacing like
  `lt_search_get` is safer than `search` alone.
- **Do not assume all nodes respond.** Iterative lookup must treat "no
  response to new verb" as "degrade to classic routing," not as failure.
- **Do not violate BEP-42.** Our nodes must still derive node IDs from their
  IPs so that modern clients add us to their routing tables.
- **Do not ignore BEP-43.** If a querier is marked `ro: 1`, don't add it to
  our routing table.

---

## 5. Interoperability Rules (backwards compatibility)

Given the above, here are the strict rules our client must obey:

### (a) Our client can download from any vanilla BitTorrent client

- We MUST implement BEP-3 byte-for-byte.
- We MUST support BEP-5 DHT queries (to find peers).
- We MUST support BEP-9 `ut_metadata` so we can fetch .torrent data from
  magnet links.
- We MUST support BEP-10 (LTEP) — a prerequisite for BEP-9.
- We SHOULD support BEP-11 (PEX), BEP-14 (LSD), BEP-15 (UDP trackers),
  BEP-29 (uTP), BEP-55 (holepunch).
- Every message we send that could be seen by a vanilla client MUST be a
  standard BEP-3/BEP-5/BEP-10 message. Vanilla clients see nothing unusual.

### (b) Any vanilla BitTorrent client can download from us

- We MUST accept BEP-3 handshakes with our custom reserved bit clear.
- We MUST serve pieces to peers regardless of whether they advertise
  `lt_search`.
- We MUST answer standard KRPC (`ping`, `find_node`, `get_peers`,
  `announce_peer`, and — if we implement BEP-44 — `get`/`put`) from any
  requester, including those that never heard of our extension.
- Our node ID MUST satisfy BEP-42 so we get treated as a normal DHT citizen.

### (c) Search is opt-in and only works between search-capable peers

- We advertise `lt_search` in our LTEP handshake `m` dictionary.
- We only send `lt_search` messages to peers whose own LTEP handshake
  included `lt_search` with a non-zero ID.
- If a peer does not advertise `lt_search`, we never send them search
  messages and never complain about the absence. The BitTorrent session
  proceeds as normal.
- If we receive an extended message ID we don't recognize, we drop it
  silently (this is the LTEP rule anyway).
- If we implement a new DHT verb (Option B above), we treat "no response"
  / "204 Method Unknown" as "this node is not search-capable; continue
  iterative lookup on other nodes in my routing table."

### (d) The DHT keeps working for vanilla peer lookups regardless

- We MUST NOT run our search overlay on a separate UDP port. We share the
  mainline DHT.
- We MUST NOT pollute our routing table with "capable-only" entries. The
  routing table is full-population; the "capable subset" is a *view* on top
  of it.
- Our new verbs (if any) MUST NOT reuse names that collide with BEP-5 /
  BEP-33 / BEP-44 / BEP-51 verbs.
- We MUST still answer `ping`, `find_node`, `get_peers`, `announce_peer`
  correctly. Those are what keep our entry in other nodes' routing tables
  alive; dropping them would isolate us.
- Our BEP-44 usage (putting keyword indexes) must not exceed the reasonable
  rate-limits other nodes enforce. In particular we must refresh items
  rather than re-creating keys, to avoid looking like a flood.

### Putting it together

A vanilla client connecting to us sees: standard BEP-3 handshake, LTEP bit
set, LTEP handshake containing `ut_metadata`, `ut_pex`, and some weird name
`lt_search`. It ignores `lt_search`, uses `ut_metadata` and `ut_pex` as
normal, and downloads whatever torrent it wanted. From its perspective we are
indistinguishable from any other modern BitTorrent client.

A search-capable client connecting to us sees the same handshake, notices
`lt_search`, and unlocks the search channel alongside the normal transfer.
Both clients are on the same connection — we do not need a second socket.

A vanilla DHT node receiving our `get`/`put` (BEP-44) answers normally
(assuming it implements BEP-44; most modern ones do). A vanilla DHT node
receiving a hypothetical new verb 204s us. Either way, our participation in
the basic DHT protocol remains unaffected.

---

## 6. BEP Draft Process (for eventual standardization)

If we want to standardize our extension as an official BEP, the process
(from BEP-1) is:

1. **Champion the idea.** File an issue in the
   `bittorrent/bittorrent.org` GitHub repo describing the single key proposal
   (one BEP = one idea — not a grab-bag). Informal community discussion
   happens here.
2. **Write the draft.** Submit a pull request against the same repo
   containing the draft BEP, formatted per BEP-1 with the required header
   fields: BEP number (assigned by the editor), Title, Version, Last-Modified,
   Author(s), Status, Type (Standards Track / Informational / Process),
   Content-Type, Created, Post-History. The body should include an Abstract,
   Motivation, Rationale, Specification (the actual wire format), Reference
   Implementation, and Copyright section.
3. **BEP editor assigns a number.** The current BEP editor is Steven Siloti
   (ssiloti@bittorrent.com). On merge, the proposal enters status **Draft**.
4. **Community review.** Discussion happens on the GitHub issue and on
   bittorrent-dev mailing lists. Editors and implementers give feedback. The
   author iterates on the draft.
5. **BDFL review.** If consensus emerges, Bram Cohen (as BDFL) signs off and
   the status moves to **Accepted**.
6. **Final.** Status becomes **Final** once there is a working reference
   implementation that has been "tested in live BitTorrent swarms" — i.e., at
   least one publicly-available client ships it and real-world interop has
   been demonstrated. Standards Track BEPs specifically require a complete
   description and at least one implementation with public source before Final.

Alternative terminal states: **Deferred** (community lost interest, like
BEP-18), **Rejected** (explicitly turned down), **Obsolete** (replaced by a
newer BEP).

### Practical strategy for us

Don't chase a BEP number on day one. The usual path is:

1. Ship the extension in our own client.
2. Document the wire format publicly (our own spec document, not in the BEP
   repo yet).
3. Get at least one other client to implement it — that's what "tested in
   live swarms" really requires.
4. Then submit the draft BEP, referencing the existing deployed
   implementations.

In parallel, we can claim an `lt_search`-style extension name (the `lt_`
prefix is historically libtorrent's, so pick a different two-character
prefix if we're not libtorrent — e.g. `sn_search` for SwartzNet). Name
collisions in BEP-10 are avoided by convention, not by registry.

---

## Summary cheat sheet

| BEP | Title | Status | Our use |
|-----|-------|--------|---------|
| 3 | Core protocol | Final | MUST implement (base) |
| 4 | Reserved bit registry | Accepted | Note: do NOT grab a new bit |
| 5 | DHT | Accepted | MUST implement; overlay search on top |
| 9 | ut_metadata | Accepted | Architectural precedent; reuse for fetching .torrent |
| 10 | LTEP | Accepted | **Primary extension point** for `lt_search` |
| 11 | ut_pex | Draft | Gossip peers; model for search-peer gossip |
| 14 | LSD | Accepted | LAN peer discovery; mostly tangential |
| 18 | Search engine spec | Deferred | Irrelevant (centralized OpenSearch) |
| 42 | DHT security | Draft | MUST obey: node ID from IP |
| 43 | Read-only DHT | Draft | Honor `ro: 1` flag |
| 44 | Arbitrary DHT data | Draft | **Primary storage point**: mutable items, 1000-byte cap |
| 46 | Mutable torrent pointers | Draft | Direct precedent; same pattern for our index |
| 49 | Distributed torrent feeds | Draft | Study their chunking scheme |
| 51 | sample_infohashes | Draft | Precedent for adding a new DHT verb without negotiation |
| 52 | v2 torrents | Draft | Per-file merkle roots for per-file indexing |

### The architecture this report supports

- **Discovery & indexing layer.** Store `(publisher pubkey, keyword)` → list
  of infohashes in the DHT as BEP-44 mutable items with `salt = keyword`.
  Refresh hourly. Shard across multiple items when the 1000-byte limit is
  hit. Optionally, use BEP-46-style pointer items to larger "index torrents"
  when the full keyword index outgrows in-band storage.
- **Peer-wire query layer.** Negotiate an `lt_search` (or `sn_search`)
  extension over BEP-10. Define bencoded `msg_type` 0/1/2 for
  query/response/reject. Allow both direct queries and streaming
  subscription pushes. Per-file hits can carry a BEP-52 `pieces root` in
  addition to the torrent infohash.
- **Network effect.** Both layers piggyback on the existing ~10M-node DHT
  and the existing LTEP-capable peer base. Vanilla clients see nothing
  unusual; search-capable clients get an opt-in feature. Our client remains
  a fully-compliant BitTorrent client at every layer it shares with the
  mainline.

### Primary sources consulted

- `https://www.bittorrent.org/beps/bep_0001.html` — BEP process
- `https://www.bittorrent.org/beps/bep_0003.html` — Core protocol
- `https://www.bittorrent.org/beps/bep_0004.html` — Reserved bit registry
- `https://www.bittorrent.org/beps/bep_0005.html` — DHT
- `https://www.bittorrent.org/beps/bep_0009.html` — ut_metadata
- `https://www.bittorrent.org/beps/bep_0010.html` — LTEP
- `https://www.bittorrent.org/beps/bep_0011.html` — PEX
- `https://www.bittorrent.org/beps/bep_0014.html` — LSD
- `https://www.bittorrent.org/beps/bep_0018.html` — Search engine spec (deferred)
- `https://www.bittorrent.org/beps/bep_0042.html` — DHT security
- `https://www.bittorrent.org/beps/bep_0043.html` — Read-only DHT
- `https://www.bittorrent.org/beps/bep_0044.html` — Arbitrary data in DHT
- `https://www.bittorrent.org/beps/bep_0046.html` — Mutable torrent updates
- `https://www.bittorrent.org/beps/bep_0051.html` — sample_infohashes
- `https://www.bittorrent.org/beps/bep_0052.html` — v2 torrents
- `https://www.bittorrent.org/beps/bep_0000.html` — BEP index
