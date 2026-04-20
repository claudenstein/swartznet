# SwartzNet documentation

Pick the audience that fits best; each section lists documents in
the order you'd most naturally read them.

## I want to use SwartzNet

Start with the [top-level README](../README.md) for install and
quick-start.

- **[08-operations.md](08-operations.md)** — file layout, common
  commands, native GUI workflow (Downloads / Search / Status /
  Companion / Settings), file selection, per-torrent indexing
  control, bandwidth limits, queue management, troubleshooting.

## I want to integrate SwartzNet or implement a SwartzNet protocol in another client

These three documents are the protocol specs. Each is a
self-contained, BEP-style specification — read them
end-to-end and produce a wire-compatible peer in any
language. The reference implementation lives in this repo.

- **[06-bep-sn_search-draft.md](06-bep-sn_search-draft.md)** —
  Layer-S, the peer-wire `sn_search` extension (LTEP). Defines
  the message envelope, capability bitfield (services bits),
  query/result/reject/peer_announce semantics, and rate limits.
- **[07-bep-dht-keyword-index-draft.md](07-bep-dht-keyword-index-draft.md)** —
  Layer-D, the BEP-44 mutable-item keyword index. Defines the
  per-publisher salt convention, the bencoded `v` schema, the
  shard-by-suffix overflow path, and key-rotation semantics.
- **[11-signing-protocol.md](11-signing-protocol.md)** —
  Publisher-signed `.torrent` files. Defines the optional
  top-level metainfo fields (`snet.pubkey`, `snet.sig`),
  the domain-prefixed signature payload, and the verification
  algorithm. Wire-compatible with every existing BitTorrent
  client.

## I want to understand the architecture

- **[05-integration-design.md](05-integration-design.md)** — the
  synthesis document. Three-layer architecture, wire format, the
  daemon layer shared by CLI / web UI / native GUI, ingestion
  pipeline, threat model, backwards-compatibility test matrix,
  roadmap.

## I want to understand why we made the choices we did

The research phase (pre-M1) produced five reports. Each stands
alone and reads in any order:

- **[01-torrent-clients-comparison.md](01-torrent-clients-comparison.md)** —
  libtorrent, Transmission, anacrolix/torrent, rqbit, WebTorrent
  compared on extension API, piece-verify hooks, DHT
  extensibility, and license. Conclusion: anacrolix/torrent.
- **[02-tribler-deep-dive.md](02-tribler-deep-dive.md)** — Tribler
  is the closest prior art (BitTorrent + keyword search since
  2006). Its architecture, limits, what we reuse vs. replace.
- **[03-p2p-search-protocols.md](03-p2p-search-protocols.md)** —
  survey of aMule/Kad, GNUnet, Gnutella, RetroShare, YaCy,
  Freenet. Identifies aMule's keyword-hash DHT as the most
  directly applicable pattern.
- **[04-bep-extension-points.md](04-bep-extension-points.md)** —
  every BEP relevant to our design, with byte-level walkthroughs
  of the LTEP handshake and BEP-44 mutable-item publish/get flow.
- **[09-v1-blocker-research.md](09-v1-blocker-research.md)** —
  what still stands between where we are and a v1.0.0 release,
  and what we can and can't do about it without real-world data.
- **[10-bitcoin-lessons.md](10-bitcoin-lessons.md)** — what the
  Bitcoin BIP process can and cannot teach us about the
  BEP-1 "two-implementations" requirement for Final status.

## I want to read the full history

- **[MILESTONES.md](MILESTONES.md)** — every milestone that
  shaped the codebase, in order.
- **[../CHANGELOG.md](../CHANGELOG.md)** — release-level change
  notes, including unreleased work.
