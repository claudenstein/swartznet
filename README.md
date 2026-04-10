# SwartzNet

A BitTorrent client with **built-in distributed text search** — search not just
torrent names, but the files inside your torrents and, over time, torrents
published by other peers on the network — while remaining fully backwards
compatible with vanilla BitTorrent (BEP-3/5/9/10/44 on the mainline DHT).

This repository is in early development. See [docs/](docs/) for the full
research and design documents that motivate the architecture.

## Status

| Milestone | Status | What works |
|---|---|---|
| **Research & design** | ✅ Complete | Five reports in `docs/` totalling ~4,400 lines. |
| **M1 — Go scaffold + engine smoke test** | 🚧 In progress | Minimal CLI wraps `anacrolix/torrent`, adds a magnet link, downloads and seeds. |
| M2 — Local Bleve index (Layer L) | Planned | Piece-complete hook → text extractor → local full-text index. |
| M3 — Peer-wire `sn_search` extension (Layer S) | Planned | BEP-10 extension for peer-to-peer keyword queries. |
| M4 — DHT keyword publisher (Layer D) | Planned | BEP-44 mutable items carrying `keyword → [infohash]`. |
| M5 — Spam resistance | Planned | Per-pubkey reputation + client-side Bloom filter. |
| M6 — Full text extractor set | Planned | PDF, EPUB, DOCX, subtitles, source code. |
| M7 — v1 release | Planned | — |

The full roadmap and per-milestone rationale is in
[`docs/05-integration-design.md`](docs/05-integration-design.md) §12.

## Design in one paragraph

SwartzNet embeds [`anacrolix/torrent`](https://github.com/anacrolix/torrent)
(Go, MPL-2.0) as its BitTorrent engine. We hook its piece-completion callback
to feed downloaded file contents into a local [Bleve](https://github.com/blevesearch/bleve)
full-text index (**Layer L**). We register a custom `sn_search` extension via
the BEP-10 Extension Protocol so that two SwartzNet peers already talking
BitTorrent can ask each other keyword queries on the same TCP connection
(**Layer S**). We publish keyword → infohash mappings on the existing mainline
DHT using BEP-44 mutable items, keyed by per-publisher ed25519 pubkey with the
keyword as salt (**Layer D**). Nothing we add requires a new reserved bit, a
new DHT verb, or a second UDP port — vanilla clients see a normal peer speaking
BEP-3/5/9/10/44.

## Documentation

### Research (completed)

- [`docs/01-torrent-clients-comparison.md`](docs/01-torrent-clients-comparison.md) —
  libtorrent, Transmission, anacrolix/torrent, rqbit, WebTorrent compared on
  extension API, piece-verify hooks, DHT extensibility, and license. Winner:
  anacrolix/torrent.
- [`docs/02-tribler-deep-dive.md`](docs/02-tribler-deep-dive.md) — Tribler is
  the closest prior art (BitTorrent + keyword search since 2006). Its search
  architecture, limitations, and what we reuse vs. replace.
- [`docs/03-p2p-search-protocols.md`](docs/03-p2p-search-protocols.md) — survey
  of aMule/Kad, GNUnet, Gnutella, RetroShare, YaCy, Freenet. Identifies
  aMule's keyword-hash DHT as the most directly applicable pattern.
- [`docs/04-bep-extension-points.md`](docs/04-bep-extension-points.md) — every
  BEP relevant to our design, with byte-level walkthroughs of the LTEP
  handshake and BEP-44 mutable-item publish/get flow.
- [`docs/05-integration-design.md`](docs/05-integration-design.md) — the
  synthesis: three-layer architecture, the `sn_search` wire format spec,
  ingestion pipeline, threat model, backwards-compatibility test matrix,
  ordered roadmap.

## Building

Requires Go 1.22 or later. Go 1.24+ is recommended.

```bash
go build ./cmd/swartznet
./swartznet --help
```

## Running (M1)

```bash
# Add and download a torrent from a magnet link, seed on completion.
./swartznet add "magnet:?xt=urn:btih:..."

# See current swarm status.
./swartznet status
```

More commands will be added as milestones land.

## Backwards compatibility

A vanilla BitTorrent client connecting to SwartzNet sees a standard BEP-3
handshake with the LTEP bit set, a standard LTEP handshake advertising
`ut_metadata`, `ut_pex`, and (for SwartzNet peers only) `sn_search`, and
standard BEP-5 DHT traffic. The `sn_search` extension is strictly opt-in: if
the other side doesn't advertise it in their LTEP `m` dictionary, we never
send them a search message. Everything we store in the DHT is a standard
BEP-44 mutable item — vanilla nodes serve it under the same rules they serve
any other BEP-44 item.

See [`docs/05-integration-design.md`](docs/05-integration-design.md) §8 for the
explicit backwards-compatibility test matrix.

## Explicit non-goals

- **Anonymity / traffic analysis resistance.** SwartzNet is not Tor. Users who
  need network-level anonymity should layer a VPN or Tor transport under their
  BitTorrent traffic.
- **Legal content filtering.** SwartzNet does not enforce copyright or
  jurisdictional content restrictions. Users are responsible for what they
  search and download, exactly as with every existing BitTorrent client.
- **Global content-level distributed index.** The local-first content index
  (Layer L) makes files you already have searchable on your machine. Sharing
  content-level indexes across the network is an explicit v2 feature, not v1.

See [`docs/05-integration-design.md`](docs/05-integration-design.md) §9 for the
full threat model.

## Contributing

SwartzNet is in early development and the internal APIs are changing rapidly.
Please open an issue before sending large patches so we can align on the
approach. The roadmap in `docs/05-integration-design.md` §12 is authoritative
for what's in scope for each milestone.

## License

Apache License 2.0. See [LICENSE](LICENSE).

The upstream dependency `anacrolix/torrent` is licensed MPL 2.0; any
modifications we make inside that library are published under MPL 2.0. Our own
code in this repository is Apache 2.0.
