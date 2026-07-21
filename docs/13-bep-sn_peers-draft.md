# Draft BEP: `sn_peers` — peer exchange for the SwartzNet search overlay

Status: draft · Layer: peer-wire (LTEP) · Companion to `sn_search` (`06-*`) and the SwartzNet rendezvous scheme (`12-*`).

## Abstract

`sn_peers` is a peer-exchange (PEX) message carried inside the existing
`sn_search` LTEP extension. Two SwartzNet peers that have opted in gossip each
other the addresses of *other* `sn_search`-capable peers they know, so the
capable-peer overlay becomes densely connected instead of relying purely on
chance co-membership in content swarms. It is the densifier half of SwartzNet
capable-peer discovery; the [rendezvous scheme](12-rendezvous-draft.md) is the
substrate that provides a shared swarm to actually connect within.

A vanilla BitTorrent client never sees an `sn_peers` message: it is a new
`msg_type` under the opt-in `sn_search` extension, sent only to peers that
advertise the `BitPeerGossip` capability bit.

## Motivation

SwartzNet learns that a peer speaks `sn_search` only *after* a BitTorrent + LTEP
handshake, and that handshake only happens inside a torrent swarm both nodes
joined. Two SwartzNet users who share no content torrent therefore never meet,
and the search overlay stays sparse and fragmented. Standard BitTorrent solved
the analogous problem for content swarms with PEX (BEP-11); `sn_peers` is the
same idea scoped to the SwartzNet capable-peer graph: once you meet one capable
peer, it introduces you to the others, and the overlay self-densifies.

Crucially, a gossiped address has nowhere to connect on its own — BitTorrent
connections are per-torrent. `sn_peers` addresses are dialed within a
[rendezvous swarm](12-rendezvous-draft.md), whose well-known infohash gives the
shared handshake context. `sn_peers` therefore presupposes rendezvous.

## Rationale

- **Why a new `msg_type` and not BEP-11 `ut_pex`.** `ut_pex` gossips peers of a
  *specific torrent*. The SwartzNet overlay is cross-torrent — the set we want to
  gossip is "peers that speak `sn_search`", independent of any one swarm.
  Overloading `ut_pex` would leak SwartzNet peers to vanilla clients and conflate
  the two graphs. A dedicated message under the opt-in extension keeps the overlay
  invisible to non-participants.
- **Why an opt-in capability bit (consent).** Advertising `BitPeerGossip` means
  both "I speak `sn_peers`" *and* "I consent to having my address shared onward."
  A node only gossips addresses of peers that set the bit, so a participant that
  does not want to be listed in the overlay directory simply does not advertise
  it and is never gossiped.
- **Why compact addresses.** The BEP-11 compact encoding (6 bytes v4, 18 bytes
  v6) is small, familiar, and trivially bounded.

## Specification

### Capability

A node that participates advertises **`BitPeerGossip` (services bit 10,
`0x400`)** in its `peer_announce` services mask (see `sn_search` §"peer_announce").
A node MUST NOT send `sn_peers` to a peer that has not advertised `BitPeerGossip`,
and MUST NOT include in a gossip the address of any peer that has not itself
advertised `BitPeerGossip`.

### Message: `sn_peers` (msg_type 9)

Carried as the payload of the `sn_search` LTEP extended message, a bencoded dict:

| Key        | Type   | Meaning                                                        |
|------------|--------|----------------------------------------------------------------|
| `msg_type` | int    | `9`                                                            |
| `v4`       | string | packed 6-byte compact IPv4 endpoints (4-byte addr + 2-byte port), optional |
| `v6`       | string | packed 18-byte compact IPv6 endpoints (16-byte addr + 2-byte port), optional |

Ports are big-endian. There is no `txid` (it is unsolicited gossip). At most
**`MaxPeersPerGossip` = 32** endpoints total per frame.

### Producer rules (encode)

- Include only endpoints of peers that advertised `BitPeerGossip`.
- Do not include the recipient's own address, nor your own.
- Cap the total at 32; an implementation SHOULD prefer recently-seen peers.
- An endpoint whose IP is not exactly 4 or 16 bytes is a programming error and
  MUST NOT be emitted.

### Consumer rules (decode) — tolerant

- Drop trailing bytes that do not complete a 6-/18-byte record.
- Truncate the total to 32 endpoints.
- Drop any record with an all-zero IP or a zero port.
- **The receiver decides what to do with the addresses.** The reference
  implementation hands them to its rendezvous swarms (via `Torrent.AddPeers`) and
  additionally applies IP-sanity filtering — rejecting loopback, unspecified,
  link-local, and (by policy) private ranges — so a hostile gossip cannot steer
  it at internal hosts. Addresses are advisory: no address is dialed outside a
  swarm the node already participates in, and — critically — PEX-learned addresses
  are introduced only to PUBLIC (global/topic) rendezvous swarms, NEVER to a
  private community swarm (see `12-rendezvous-draft.md`), so a public-swarm
  attacker cannot make the node dial its listener under the community infohash and
  recover that secret infohash from the handshake.

### Cadence and rate limits

`sn_peers` MAY be sent when a peer newly advertises `BitPeerGossip` and
periodically thereafter (the reference implementation: on a low-frequency timer).
Receivers SHOULD rate-limit inbound `sn_peers` per peer and MUST bound the work a
single frame can trigger (the 32-cap does this on the wire; deduplication against
already-known peers does it downstream).

### Backwards compatibility

`sn_peers` is a new `msg_type` inside the opt-in `sn_search` extension, gated on
a capability bit. A vanilla client (no `sn_search`) never negotiates the
extension; an `sn_search` peer that does not advertise `BitPeerGossip` never
receives an `sn_peers` frame. No new reserved handshake bit, DHT verb, or UDP
port is introduced. The rendezvous swarms the addresses feed into are ordinary
BEP-5 swarms (see `12-*`). Mainline compatibility is preserved.

## Security considerations

- **Address injection / SSRF.** A malicious peer can gossip arbitrary addresses.
  The consumer never dials them directly; it only introduces them to a rendezvous
  swarm, and it filters loopback/unspecified/link-local/private IPs first. The
  32-cap and per-peer rate limit bound amplification.
- **Membership enumeration.** Participating advertises the node as part of the
  SwartzNet overlay (as does joining a public rendezvous swarm). This is the
  consent model: opt out by not advertising `BitPeerGossip`. See the privacy note
  in `12-rendezvous-draft.md`.
- **Alloc amplification.** The `v4`/`v6` strings are decoded with the bounded
  decoder (`MaxStrLen` = payload length), so a small frame cannot force a large
  allocation. Endpoint counts are then capped at 32.

## Reference implementation

`contracts/ltepwire` (the `SnPeers` type, `EncodeSnPeers`/`DecodeSnPeers`,
`BitPeerGossip`), `internal/swarmsearch` (send/receive, consent + dedup + rate
limit), and `internal/engine` (`AddRendezvousPeers`, numeric-only IP filtering).

## References

- BEP-11 (Peer Exchange), BEP-5 (DHT), BEP-10 (LTEP).
- `06-bep-sn_search-draft.md`, `12-rendezvous-draft.md`, `05-integration-design.md` §8.

## Copyright

Apache-2.0, consistent with the rest of this repository (the vendored
`anacrolix/torrent` engine remains MPL-2.0).
