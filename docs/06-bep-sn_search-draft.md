# Draft BEP: `sn_search` — peer-wire distributed text search

| Field | Value |
|---|---|
| Title | Distributed Text Search Extension (`sn_search`) |
| Version | 1 |
| Last-Modified | 2026-04-13 |
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

Capabilities are announced in a dedicated **`peer_announce`**
message (msg_type 3, see §Message types) sent once per
connection direction immediately after the LTEP handshake. The
message carries a 64-bit `services` bitfield in which each bit
indicates support for one optional sub-feature, and a `v`
integer identifying the protocol version. This is the BIP-9
"services bits" pattern from Bitcoin Core's peer protocol:
**unknown bits MUST be ignored, never rejected**, so adding
new features is structurally non-breaking.

Bit assignments (from `internal/swarmsearch/services.go`):

| Bit | Name | Meaning |
|---|---|---|
| 0 | `BitShareLocal` | Answers queries against the full local index. |
| 1 | `BitShareSwarm` | Answers queries only for torrents the responder is currently in the swarm of. Mutually exclusive with bit 0 in practice. |
| 2 | `BitFileHits` | Returns per-file path matches in addition to torrent-name matches. |
| 3 | `BitContentHits` | Returns content-level matches (extracted-text snippets). Implies a local content extractor pipeline. |
| 4 | `BitLayerDPublisher` | Publishes keyword → infohash entries to the BEP-44 DHT (`07-bep-dht-keyword-index-draft.md`). |
| 5 | `BitCompanionPublisher` | Publishes F3 companion content-index torrents (publisher side of `M11` companion-index protocol). |
| 6 | `BitCompanionSubscriber` | Subscribes to one or more companion publishers and ingests their content index. |
| 7 | `BitSnippetHighlight` | Returns Bleve highlight fragments wrapped in `<mark>...</mark>` on content hits. |
| 8 | `BitRegtest` | The peer is running in deterministic regtest mode. Loud bit so accidental cross-connections from a regtest harness to mainnet are obvious. |
| 9 | `BitSetReconciliation` | Peer speaks the Aggregate sync protocol (msg_types 4-8). Peers without this bit set MUST NOT receive sync frames; the handler rejects inbound sync messages with code 2 (`unsupported_scope`). Added v0.5.0. |
| 10–63 | reserved | Future features. Always allocate the next available bit. |

A peer that does not send a `peer_announce` message before its
first query is treated as "services unknown" (zero mask) by
the responder. Such a peer is still queried normally; its
results just cannot be filtered against advertised
capabilities. The responder MUST NOT refuse to answer a query
solely because the initiator never announced.

Querying a peer for a sub-feature whose bit is clear MAY
result in a reject of code 2 (`unsupported scope`), or in the
responder silently downgrading the query to its supported
subset — implementations MAY choose either policy.

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
| 4 | sync_begin | initiator → responder (gated on `BitSetReconciliation`) |
| 5 | sync_symbols | either (same gate) |
| 6 | sync_need | either (same gate) |
| 7 | sync_records | either (same gate) |
| 8 | sync_end | either (same gate) |

Messages 4–8 form the Aggregate set-reconciliation session
protocol added in v0.5.0. Full byte-level schema and state
machine in [`docs/research/SPEC.md`](research/SPEC.md) §2.

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
  "msg_type":  3,
  "v":         <int, this peer's ProtocolVersion>,
  "services":  <int, this peer's ServiceBits as a uint64>
}
```

`peer_announce` is the per-direction announcement of a peer's
own protocol version and capability bitfield. Each side of the
TCP connection sends exactly one `peer_announce` immediately
after observing the remote's LTEP handshake — initiator-to-
responder AND responder-to-initiator. Subsequent capability
changes (e.g. user toggling a setting at runtime) MAY trigger
a fresh `peer_announce` on the same connection; implementations
SHOULD rate-limit such re-announces.

The `services` integer encodes the bits documented in
"Capability advertisement" above. A receiver MUST mask off only
the bits it understands (bits ≥ the highest defined bit in its
own build), MUST NOT raise an error for unknown bits, and MUST
treat the absence of a `peer_announce` as `services = 0` rather
than as a protocol error.

Earlier draft revisions of this document specified
`peer_announce` as a peer-discovery gossip primitive carrying
IP/port/cap/pk lists for *other* search-capable peers. That
schema was retired during M15b in favour of the per-connection
self-announcement above; peer discovery is now handled by the
ambient swarm itself (see `internal/swarmsearch/feeler.go`
and `peerbook.go` for the current implementation).

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

### Result merging and the LRU hit cache

When a query fans out to N peers and several of them return
the same popular torrent, the merge step needs to deduplicate
by infohash and accumulate per-source attribution. Reference
implementations SHOULD maintain a bounded LRU cache of
recently-seen `MergedHit` records keyed by infohash so the
merge can skip redundant metadata comparison for hits already
in the cache and just increment the source count. The
SwartzNet reference implements this in
`internal/swarmsearch/hitcache.go` (default 4096 entries).

This is purely a local performance optimisation — the wire
format does not change, and a correct implementation that does
no merge-time caching is fully interoperable. A future v1.1
profile may add SipHash-keyed compact result encoding (BIP-152
style) that exploits the same cache as its source-of-truth
state.

### Backwards compatibility

A peer that does not advertise `sn_search` in its `m`
dictionary MUST never receive an `sn_search` message. A peer
that receives an extended message ID it does not recognise
MUST drop the message silently. These two rules together
guarantee that a vanilla BitTorrent peer can connect to an
`sn_search`-aware peer with no observable difference in
behaviour.

A capability bit unknown to the receiver MUST be treated as
"feature not present" and ignored. This is the structural
property that makes the protocol additively extensible
forever, modeled on Bitcoin Core's services field.

## Reference Implementation

A complete reference implementation in Go ships in the
SwartzNet client at `internal/swarmsearch/`:

- `protocol.go` — per-peer state, LTEP handshake observation,
  per-direction `peer_announce` emission.
- `services.go` — `ServiceBits` bitfield with named constants
  for every defined feature plus helpers (`Has`, `With`,
  `Without`, `DefaultServices`).
- `wire.go` — message encode / decode (uses
  `github.com/anacrolix/torrent/bencode`).
- `handler.go` — server-side query dispatch, capability gate,
  and reject paths.
- `query.go` — outbound `Query()` fan-out with txid routing
  and merge-by-infohash.
- `hitcache.go` — bounded LRU of MergedHits used by the merge
  step.
- `ratelimit.go` — per-peer inbound query rate limiter.
- `misbehavior.go` — defence-in-depth misbehavior-score
  tracker; peers exceeding threshold are demoted/dropped.
- `peerbook.go` + `feeler.go` — opportunistic discovery of
  search-capable peers in the ambient swarm.

The reference implementation has ~40 unit tests covering
encode/decode round-trip, capability bit handling, inbound
query handling against a faked local index, outbound fan-out
with mocked senders, hit-cache LRU semantics, and rate
limiting. All tests run under `go test -race`.

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
