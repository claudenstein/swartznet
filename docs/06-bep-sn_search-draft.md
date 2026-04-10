# Draft BEP: `sn_search` — peer-wire distributed text search

| Field | Value |
|---|---|
| Title | Distributed Text Search Extension (`sn_search`) |
| Version | 1 |
| Last-Modified | 2026-04-10 |
| Author | The SwartzNet Authors |
| Status | Draft |
| Type | Standards Track |
| Created | 2026-04-10 |

## Abstract

This document specifies an opt-in extension to the BitTorrent
peer wire protocol that lets two peers exchange text-search
queries and results over the same TCP connection they already
use for piece transfer. The extension is layered on top of
[BEP-10][bep10] (the LibTorrent Extension Protocol, "LTEP")
and is fully backwards compatible: peers that do not advertise
`sn_search` in their LTEP handshake never receive an
`sn_search` message and never need to know the extension
exists.

The extension is designed to enable a torrent client with a
local full-text index over its downloaded content to share
that index with peers it is already exchanging pieces with,
producing a swarm-scoped distributed search network without
any additional discovery infrastructure.

[bep10]: https://www.bittorrent.org/beps/bep_0010.html

## Motivation

Existing distributed-search systems (Tribler, eDonkey/Kad,
Gnutella) either run their own overlay alongside BitTorrent
(Tribler / IPv8) or replace BitTorrent entirely. None of them
take advantage of the simple observation that **two peers
swapping pieces of a torrent are almost certainly interested
in the same kinds of content**, which makes them excellent
candidates to ask each other about other torrents in the same
neighbourhood.

By piggybacking on the existing peer wire we get for free:

- Peer discovery (the swarm we are already in).
- A connection (the TCP socket we are already speaking BitTorrent on).
- Topical clustering (peers in the same swarm share interests).
- Strict opt-in semantics (LTEP capability negotiation).

The cost is one new BEP-10 extension name and a small set of
bencoded messages. No new ports, no new reserved bits, no
changes to the DHT, no changes to the .torrent format.

## Rationale

### Why peer wire and not DHT

The mainline DHT is the right place for *long-lived* keyword
→ infohash mappings (see the companion BEP draft on the
Layer-D BEP-44 keyword index). It is the wrong place for
*ad-hoc, low-latency* free-text queries because:

1. BEP-44 enforces a 1000-byte cap on `v` payloads, which is
   too small for a meaningful result list with snippets.
2. DHT lookups take seconds; a search box wants
   sub-100-ms responses where possible.
3. The DHT does not naturally cluster by topic, so even a
   well-formed lookup ends up asking thousands of unrelated
   nodes.

The peer wire complements the DHT: it gives fast, focused,
swarm-local results, while the DHT gives broad, slow,
cross-swarm results. SwartzNet runs both in parallel.

### Why LTEP and not a new reserved handshake bit

Per BEP-4, the BitTorrent reserved bits are a precious shared
resource, and BEP-10 is the explicit recommended path for new
extensions specifically to avoid having to grab one. LTEP
gives us per-peer opt-in negotiation, dynamic extension IDs,
and graceful fallback for free.

### Why not just embed in `ut_pex` or another existing extension

`ut_pex` is rate-limited and capped at peer addresses; it has
no semantic vocabulary for "search". Other existing LTEP
extensions are similarly type-specific. A new dedicated
extension is clearer and easier to evolve.

## Specification

### Extension name

The extension MUST be named `sn_search`. Implementations MUST
include `"sn_search"` as a key in the `m` dictionary of their
LTEP handshake message (BEP-10) when they wish to receive
`sn_search` messages.

### Capability advertisement

Implementations MAY include the following top-level keys in
their LTEP handshake dictionary:

| Key | Type | Meaning |
|---|---|---|
| `sn_search_v` | int | Highest protocol version supported. This document defines version 1. |
| `sn_search_cap` | string | Compact capability descriptor (see below). |

The capability string is four 2-character fields packed
together: `L<level>F<level>C<level>P<level>` where:

- **L**: how much of the local index this peer will share.
  - `L0` = nothing (pure leecher of search)
  - `L1` = torrents in the current swarm only
  - `L2` = the whole local index
- **F**: file-list match support.
  - `F0` = torrent-name hits only
  - `F1` = file-list hits
- **C**: content-level match support (requires text extraction).
  - `C0` = no content hits
  - `C1` = content-level hits
- **P**: DHT publishing support (the companion Layer-D
  BEP-44 keyword index).
  - `P0` = does not publish
  - `P1` = publishes

A peer MAY query a peer for capabilities the responder
explicitly disables. The responder MUST then reply with a
reject message of code 2 (`unsupported scope`).

### Message envelope

Every `sn_search` message rides inside a standard LTEP
extended message envelope (BitTorrent message ID `20`,
extended message ID equal to whatever the receiver advertised
for `sn_search` in its `m` dictionary). The payload is a
single bencoded dictionary whose first integer key is
`msg_type`, the discriminator that selects the inner schema.

```
uint32 length        (length of everything below this field)
uint8  msg_id = 20   (BitTorrent extended-message marker)
uint8  ext_msg_id    (== receiver's advertised m["sn_search"])
bytes  payload       (bencoded dictionary; details below)
```

### Message types

| `msg_type` | Name | Direction |
|---|---|---|
| 0 | query | initiator → responder |
| 1 | result | responder → initiator |
| 2 | reject | responder → initiator |
| 3 | peer_announce | either |

#### Query (msg_type 0)

```
{
  "msg_type":  0,
  "txid":      <u32, monotonic per-initiator>,
  "q":         "free text query string, UTF-8",
  "scope":     "nfc",                  # optional, subset of n,f,c
  "limit":     50,                     # optional, max hits requested
  "lang":      "en",                   # optional, language hint
  "min_size":  0,                      # optional, byte filter
  "max_size":  0,                      # optional, 0 = no max
  "not_ih":    [<20-byte sha1>, ...]   # optional, dedup hint
}
```

The `txid` field is the initiator's transaction id. The
responder MUST echo the same `txid` in its reply so the
initiator can match replies to outstanding queries.

The `scope` string lists which match types the initiator
wants. Letters MAY appear in any order; duplicate letters are
allowed and have no extra effect:

- `n` — match against torrent name
- `f` — match against file paths inside the torrent
- `c` — match against extracted content text

A responder that does not support a requested scope letter
SHOULD silently downgrade (run only the supported subset)
rather than reject the entire query.

#### Result (msg_type 1)

```
{
  "msg_type":  1,
  "txid":      <u32, echoes the query>,
  "total":     123,                    # responder's total hit count
  "partial":   0,                      # 1 if list was truncated
  "hits": [
    {
      "ih":   <20-byte sha1 infohash>,
      "ih2":  <32-byte sha256, optional, BEP-52 hybrid>,
      "n":    "torrent name",
      "s":    42,                      # seeders (informational)
      "l":    13,                      # leechers
      "sz":   6195404800,              # bytes
      "t":    1712649600,              # added-at unix timestamp
      "rank": 870,                     # responder's score, 0-1000
      "matches": [                     # optional, only when scope >= f or c
        {
          "fi":  4,
          "fp":  "casper/vmlinuz",
          "pr":  <32-byte BEP-52 piece-root, optional>,
          "sn":  "snippet around match",
          "off": 12345
        }
      ]
    }
  ]
}
```

#### Reject (msg_type 2)

```
{
  "msg_type":  2,
  "txid":      <u32, echoes the query>,
  "code":      <integer reject code>,
  "reason":    "human-readable explanation"   # optional
}
```

Defined reject codes:

| Code | Name | Meaning |
|---|---|---|
| 0 | rate_limited | Querier exceeded responder's per-connection rate. |
| 1 | too_expensive | Responder cannot answer queries of this scope right now. |
| 2 | unsupported_scope | Query asked for a scope the responder's `sn_search_cap` excludes. |
| 3 | query_too_broad | Query produces too many results or contains only stop-words. |
| 4 | shutting_down | Responder is closing. |

#### peer_announce (msg_type 3)

```
{
  "msg_type": 3,
  "peers": [
    {
      "ip":   <4 or 16 bytes, big-endian>,
      "port": <u16>,
      "cap":  "L1F1C0P0",
      "pk":   <32-byte ed25519 publisher pubkey, optional>
    }
  ]
}
```

`peer_announce` is the gossip primitive used to spread known
search-capable peers across the swarm. Implementations SHOULD
limit `peer_announce` to at most one message per connection
per 10 minutes and at most 20 peer entries per message.

### Rate limits

The responder is the authority on its own rate limits.
Suggested defaults:

- One outstanding query per connection at a time. A second
  query MUST receive a `reject` of code `0`.
- 100 queries per peer per hour for any combination of `n`
  and `f` scope.
- 10 queries per peer per hour for `c` scope (more expensive).

Implementations MAY override these for trusted peers (e.g.
peers whose pubkey appears in the local known-good list).

### Backwards compatibility

A peer that does not advertise `sn_search` in its `m`
dictionary MUST never receive an `sn_search` message. A peer
that receives an extended message ID it does not recognise
MUST drop the message silently. These two rules together
guarantee that a vanilla BitTorrent peer can connect to an
`sn_search`-aware peer with no observable difference in
behaviour.

The `sn_search_v` and `sn_search_cap` keys are top-level keys
on the LTEP handshake dictionary. Vanilla clients will see
unknown keys and ignore them per BEP-10's general unknown-
field policy.

## Reference Implementation

A complete reference implementation in Go ships in the
SwartzNet client at `internal/swarmsearch/`:

- `protocol.go` — peer-state tracking and capability flags.
- `wire.go` — message encode / decode (uses
  `github.com/anacrolix/torrent/bencode`).
- `handler.go` — server-side query dispatch and reject paths.
- `query.go` — outbound `Query()` fan-out with txid routing
  and merge-by-infohash.

The reference implementation has 18 unit tests covering
encode/decode round-trip, capability discovery, inbound query
handling against a faked local index, and outbound fan-out
with mocked senders. All tests run under `go test -race`.

## Security Considerations

`sn_search` does NOT provide:

- Anonymity. Queries leak the initiator's IP to whichever
  peer they were sent to. Use a VPN or Tor for that.
- Authentication of results. A responder can return any
  infohash for any query; the initiator MUST verify
  results client-side via the reputation tracker (see the
  Layer-D companion BEP) and/or against its own known-good
  set.
- Integrity of the result list. Implementations SHOULD
  cap response sizes (default: 100 hits) to bound the
  damage from a malicious responder flooding hits.

`sn_search` DOES provide:

- Strict opt-in (vanilla clients are unaffected).
- Rate-limited responses (default 100 queries/hour; configurable).
- Bounded payload sizes (response capped at 100 hits).
- No exposure of any local content to peers that did not
  ask for it via a query.

## References

- BEP-3: The BitTorrent Protocol Specification
- BEP-10: Extension Protocol (LTEP)
- BEP-52: BitTorrent v2 (per-file SHA-256 piece roots)
- `docs/05-integration-design.md` — full SwartzNet architecture and rationale
- `docs/04-bep-extension-points.md` — survey of relevant BEPs and the LTEP wire format

## Copyright

This document is placed in the public domain.
