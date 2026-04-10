# SwartzNet Operations Guide

This document is for people running the `swartznet` daemon
day-to-day. It covers the on-disk file layout, what to back
up, how to debug a misbehaving install, and the small set of
configuration knobs you actually need to know about.

For the architectural background, see
[`05-integration-design.md`](05-integration-design.md). For
the wire formats, see
[`06-bep-sn_search-draft.md`](06-bep-sn_search-draft.md) and
[`07-bep-dht-keyword-index-draft.md`](07-bep-dht-keyword-index-draft.md).

## File layout

By default everything lives under
`$XDG_DATA_HOME/swartznet/`, which falls back to
`$HOME/.local/share/swartznet/` on Linux/macOS. The full
contents of that directory after a working install:

```
~/.local/share/swartznet/
├── identity.key            ← M4a ed25519 publisher keypair, mode 0600
├── publisher.json          ← M4d per-keyword publish manifest (JSON)
├── reputation.json         ← M5b per-pubkey reputation tracker (JSON)
├── known-good.bloom        ← M5a Bloom filter of confirmed infohashes
├── data/                   ← downloaded torrent files
│   ├── ubuntu-24.04-amd64.iso
│   └── …
└── index/                  ← Bleve full-text index directory
    ├── index_meta.json
    └── store/…
```

Each of these can be relocated independently via the
matching `--data-dir`, `--index-dir`, etc. CLI flags or by
setting `XDG_DATA_HOME` to a different root.

### What to back up

| File | Why | Recoverable if lost? |
|---|---|---|
| `identity.key` | Your publisher pubkey. Lose it and every previously-published Layer-D entry will eventually expire and be unattributable to you. | No — generate a new one and start over. |
| `reputation.json` | The history that informs your indexer-quality scores. | Yes, slowly — counters rebuild as you query. |
| `known-good.bloom` | Your downloaded-and-confirmed infohashes; drives the Bloom-hit boost in lookups. | Partly — auto-confirm rebuilds it as you re-download. |
| `publisher.json` | The list of keywords you have published and their hits. | Yes — re-published from your local index on next add. |
| `data/` | The downloaded content itself. | Only if the torrents are still on the swarm. |
| `index/` | The Bleve full-text index over downloaded content. | Yes — the indexer rebuilds it as torrents are re-added. |

The single most important file is `identity.key`. Back it up.

### Permissions

`identity.key` is created with mode `0600` and the loader
will refuse to start if its mode is anything else. The fix
when this happens is:

```
chmod 0600 ~/.local/share/swartznet/identity.key
```

If you do not own the key file or do not trust its history,
delete it and let `swartznet add` regenerate a fresh one. A
new keypair will reset your publisher reputation but is
otherwise harmless.

## Running the daemon

The simplest invocation is:

```
swartznet add "magnet:?xt=urn:btih:..."
```

This starts a long-running process that:

1. Loads (or generates) the ed25519 identity at
   `~/.local/share/swartznet/identity.key`.
2. Loads (or creates) the Bleve index, the reputation
   tracker, the Bloom filter, and the publisher manifest
   at their default paths.
3. Joins the mainline DHT, opens BitTorrent listen ports
   (TCP+uTP, default 42069), and starts the publisher
   worker.
4. Serves the local HTTP API on `localhost:7654`.
5. Adds the supplied magnet URI to the engine and starts
   downloading.
6. Runs until you Ctrl-C, persisting Bloom filter and
   reputation state to disk on shutdown.

Useful flags:

| Flag | Default | Notes |
|---|---|---|
| `--port` | 42069 | BitTorrent peer wire listen port. `0` lets the OS pick. |
| `--data-dir` | `~/.local/share/swartznet/data` | Where downloaded content lives. |
| `--index-dir` | `~/.local/share/swartznet/index` | Bleve index path. |
| `--api-addr` | `localhost:7654` | HTTP API address. Empty string disables the API entirely. |
| `--no-dht` | off | Skip joining the mainline DHT (also disables Layer-D entirely — no publisher, no lookup). |
| `--no-index` | off | Do not write the added torrent to the local Bleve index. |
| `--leech-only` | off | Do not upload to peers. Debug only. |

## Useful commands

```bash
# Combined search across local + swarm + DHT
swartznet search --swarm --dht "ubuntu 24.04"

# Local-only search (does not need a running daemon)
swartznet search "ubuntu 24.04"

# Snapshot of local index, swarm peers, DHT publisher, Bloom + reputation
swartznet status

# JSON output for scripts
swartznet status --json
swartznet search --swarm --dht --json "ubuntu"

# Mark a hit as good (auto-confirm normally handles this)
swartznet confirm <40-char-hex-infohash>

# Mark a hit as spam — demotes every indexer that returned it
swartznet flag <40-char-hex-infohash>
```

## Backwards compatibility

`swartznet` speaks vanilla BitTorrent end-to-end. A
qBittorrent / Transmission / libtorrent peer that connects
to your daemon sees:

- A standard BEP-3 handshake with the LTEP reserved bit set
  (because we support [BEP-9][bep9] and [BEP-10][bep10] like
  every other modern client).
- A standard LTEP handshake `m` dictionary advertising
  `ut_metadata`, `ut_pex`, and the new `sn_search` extension
  name. Vanilla clients ignore unknown extension names per
  the LTEP spec.
- Standard BitTorrent piece transfer.

The mainline DHT sees:

- A standard BEP-5 node that responds to `ping`, `find_node`,
  `get_peers`, and `announce_peer` exactly as expected.
- A standard BEP-44 storage participant that serves `get` /
  `put` for arbitrary mutable items. Our Layer-D keyword
  index entries look identical to any other BEP-44 mutable
  item — the only thing that distinguishes them is what we
  put in the `v` payload, and other clients have no reason
  to care.

You can run `swartznet` alongside qBittorrent, Transmission,
or libtorrent on the same machine without any conflicts other
than the obvious port collision (use `--port`).

[bep9]: https://www.bittorrent.org/beps/bep_0009.html
[bep10]: https://www.bittorrent.org/beps/bep_0010.html

## Troubleshooting

### "swartznet: cannot reach the daemon at localhost:7654"

You ran `swartznet search --swarm` (or `status`, `confirm`,
`flag`) without a daemon running. The daemon is `swartznet
add <magnet>` in another terminal. You can also run a
"placeholder" daemon by adding any old infohash with
`--no-index`:

```
swartznet add --no-index --no-dht "magnet:?xt=urn:btih:0000000000000000000000000000000000000000"
```

### "identity.key has insecure permissions"

Run `chmod 0600 ~/.local/share/swartznet/identity.key`. Or
delete the file to regenerate. The strict permission check
exists because the keypair signs every BEP-44 publish; a
keypair readable by other users on the same machine can be
used to impersonate your publisher identity.

### "indexer.schema_rebuild" warning at startup

The on-disk Bleve index was created with an older schema
version and has been rebuilt automatically. Existing data is
lost (the rebuild empties the index); previously-added
torrents will be re-indexed as you re-add them.

This warning fires once after a SwartzNet upgrade that
bumped the schema. If it fires every startup, something is
overwriting the schema sentinel between runs — file an issue.

### "swarmsearch: no search-capable peers known"

Layer S only works once you are actually connected to peers
that also speak `sn_search`. Until other clients implement
the extension, this only happens between two `swartznet`
instances on the same swarm. Empty results from `--swarm` on
day one are expected.

### Status shows `total_keywords: 0` but I added a torrent

The publisher waits for torrent metadata to arrive before
running tokenisation. Magnet links can take anywhere from a
few seconds to a few minutes to fetch metadata depending on
swarm health. Watch the daemon log for `engine.publisher_started`
followed by `dhtindex.publisher.put_ok` — those mean the
publisher has work and is doing it.

### `search --swarm` returns hits but `search --dht` does not

The DHT publisher needs `--no-dht` to be off, an identity to
be loaded, and at least one indexer pubkey to be known. By
default the engine adds your own pubkey as `self`, so a
single-node test should still produce hits once the publisher
has had a chance to push.

`swartznet status` shows everything you need to debug this:
the `bloom` block tells you whether the Bloom filter is
loaded, the `reputation` block tells you which indexer
pubkeys are known, and the `publisher.keywords` table tells
you what has been published successfully.

## Where to file issues

For now, the project lives at
<https://github.com/claudenstein/swartznet>. Issues are
welcome. Pull requests with new extractors, additional
language stop-word lists, or implementations of the draft
BEPs in other clients are even more welcome.
