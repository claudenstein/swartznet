# Draft: SwartzNet rendezvous — capable-peer discovery on the mainline DHT

Status: draft · Layer: mainline DHT (BEP-5) · Companion to `sn_peers` (`13-*`).

## Abstract

Rendezvous is how SwartzNet nodes find each other without any SwartzNet-specific
signal on the wire. Participating nodes `announce_peer` / `get_peers` a set of
well-known **rendezvous infohashes** on the ordinary mainline DHT. To a vanilla
DHT node these are indistinguishable from any other torrent's infohash — no new
DHT verb, UDP port, or reserved handshake bit. A node that joins a rendezvous
infohash finds other SwartzNet nodes in that swarm, connects to them, and
negotiates `sn_search` over the standard LTEP handshake.

Rendezvous is the *substrate* for capable-peer discovery; [`sn_peers`
PEX](13-bep-sn_peers-draft.md) is the densifier that accelerates it.

## Motivation

SwartzNet learns a peer is capable only after a handshake, and a handshake needs
a shared torrent swarm. Two users who share no content torrent never meet. A
rendezvous infohash is a swarm nodes join purely to meet: it supplies the shared
handshake context that `sn_search` (and, transitively, `sn_peers`) require.

## Specification

### Rendezvous infohashes

All derivations use a frozen, versioned salt prefix `swartznet:rv:1`. Bumping the
version migrates the whole network to a fresh set of swarms in lockstep.

| Flavour   | Infohash (20 bytes)                                  | Purpose |
|-----------|------------------------------------------------------|---------|
| Global    | `SHA1("swartznet:rv:1:global")`                      | one public swarm every node may join — broad discovery |
| Topic     | `SHA1("swartznet:rv:1:t:" + normalize(topic))`       | one swarm per topic — nodes into the same content meet; no single global list |
| Community | `HMAC-SHA1(secret, "swartznet:rv:1")`                | a private swarm only holders of `secret` can compute — closed groups meet without public enumeration |

`normalize(topic)` lowercases, collapses whitespace runs to single spaces, trims
the ends, and truncates to 128 bytes on a rune boundary, so trivially different
spellings map to the same swarm. Topics are **explicit opt-in configuration**;
they are NEVER auto-derived from the local index, which would publish the node's
library topics to a public swarm.

### Joining a swarm

A node joins a rendezvous infohash by adding it as a **metadata-less torrent**
(BEP-9 magnet with no metadata): the DHT server announces it and returns peers on
`get_peers`, exactly as for any torrent. The reference implementation marks such
torrents specially:

- data download is disallowed (no pieces are ever requested), which also
  neutralizes metadata-poisoning of the well-known infohash — even if a peer
  injects a metainfo, nothing is downloaded;
- they are never indexed;
- they are hidden from the user-facing torrent list.

Discovered peers flow through the node's ordinary
`PeerConnAdded → NotePeerAdded → OnRemoteHandshake` path and negotiate
`sn_search`. `sn_peers` addresses learned from gossip are introduced to the same
rendezvous swarms (`Torrent.AddPeers`).

### Configuration and defaults

- **Global swarm defaults ON** (`--no-rendezvous` opts out). A discovery
  mechanism that is off by default discovers nothing, and peer-IP visibility is
  already the BitTorrent baseline.
- **Topics** (`--rendezvous-topic`, repeatable) and **communities**
  (`--rendezvous-community`, repeatable) are opt-in. Community secrets are never
  sent on the wire and never logged.
- Rendezvous is skipped when the DHT is disabled (`--no-dht`): a rendezvous swarm
  cannot find peers without it.

The joined set is reconciled from configuration by deterministic control logic
(the rendezvous manager owns the set), consistent with the production
architecture rules; it is static per process, reconciled once at startup with a
periodic re-join as pure defense.

## Privacy considerations

Any *public* rendezvous makes SwartzNet **membership enumerable**: anyone can
`get_peers` the well-known global (or a known topic) infohash and harvest the
participant list. This is a sharper exposure than plain content-swarm
participation and is consistent with SwartzNet's stated non-goal of anonymity
(peer IPs are visible regardless; see `05-integration-design.md` §9). Mitigations
offered:

- **Topic sharding** avoids a single global membership list.
- **Community swarms** (HMAC of a shared secret) let a closed group meet without
  any publicly computable infohash — membership is not enumerable by outsiders.
- **Opt-out** of the global swarm entirely (`--no-rendezvous`).
- Discovered peers are still gated by the `sn_search` handshake and weighted by
  reputation, so poisoned rendezvous entries are cheap to reject.

If network-level anonymity is ever required, rendezvous (like all SwartzNet
traffic) must run behind the VPN/Tor transport the design leaves to the operator.

## Backwards compatibility

Rendezvous is pure BEP-5: announcing to and getting peers for chosen infohashes.
A vanilla DHT node serves these requests exactly as it serves any other
infohash's, and cannot tell a rendezvous infohash from a real torrent's. No new
verb, port, or reserved bit. See `05-integration-design.md` §8.

## Reference implementation

`internal/rendezvous` (derivation + manager), `internal/engine`
(`AddRendezvous`/`RemoveRendezvous`/`AddRendezvousPeers`, the `rendezvous`
handle flag), `internal/config` + `cmd/swartznet` (flags), `internal/daemon`
(`startRendezvous`).

## References

- BEP-5 (DHT), BEP-9 (metadata exchange), BEP-11 (PEX).
- `13-bep-sn_peers-draft.md`, `06-bep-sn_search-draft.md`, `05-integration-design.md` §8/§9.

## Copyright

Apache-2.0, consistent with the rest of this repository.
