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

## Privacy and threat model

SwartzNet is **spam-resistant, not anonymous**. Running the
daemon with DHT enabled and an identity loaded exposes the
following to observers:

### What is visible

1. **Your IP address, to the DHT nodes your puts reach.** Every
   BEP-44 `put` traverses the mainline DHT toward the ~8 nodes
   closest to `SHA1(pubkey || salt)`. Those nodes see your
   source IP (per BEP-42, DHT node IDs are derived from IP, so
   an adversary running Sybils close to a specific publisher
   target will reliably observe their puts). The same is true
   for subscribers doing gets, to a lesser extent.
2. **Your ed25519 publisher pubkey, as a persistent identity.**
   The key is regenerated only when you delete `identity.key`;
   otherwise every BEP-44 mutable item you post is signed by
   the same key and every Layer-D lookup can correlate past
   and future puts to the same publisher.
3. **A hourly timing fingerprint.** The publisher re-announces
   every keyword once an hour (BEP-44's 2 h TTL with the
   SwartzNet 55 min put budget). Any observer correlating
   put times across keys can cluster them to a single
   publisher even across key rotations.
4. **Geographic bias in the target set.** Because DHT node IDs
   derive from IP, the ~8 nodes closest to
   `SHA1(pubkey || salt)` tend to cluster geographically. This
   is a property of mainline DHT, not SwartzNet specifically,
   but it's worth knowing.

### What is NOT visible

- Your downloads themselves (those go through the normal
  BitTorrent swarm, same as any other client — not through the
  DHT).
- Your queries to Layer-L (the local Bleve index, never leaves
  your machine).
- The contents of the torrents you publish — Layer D only
  carries the keyword → infohash mapping, not the data.
- The local web UI at `localhost:7654`, which is bound to
  loopback and inaccessible from anywhere but the local
  machine.

### Mitigations available today

- **Turn off DHT entirely** with `swartznet add --no-dht`. You
  still get Layer L (local search), Layer S (peer-wire search
  on the torrents you're actively in the swarm for), and
  normal BitTorrent downloads. You lose Layer D (no publish,
  no subscribe to other publishers' keyword indexes).
- **Run the daemon on a host you're already using Tor/VPN
  for.** BEP-44 traffic is UDP, which means a SOCKS5 proxy
  alone is not sufficient (SOCKS5 UDP associate is rarely
  supported and Tor does not carry UDP at all). The practical
  approach is full-interface routing via WireGuard/OpenVPN
  with a privacy-preserving operator, or a VPS-hosted daemon
  you SSH into.
- **Rotate `identity.key` between deployments.** Move it aside,
  let the daemon generate a fresh one, and re-share the new
  pubkey with your subscribers. This loses accrued reputation.

### What v1.0.0 does NOT ship

- **No onion-routed publish path.** The research in
  `docs/09-v1-blocker-research.md` confirmed every other
  production BEP-44 deployment (pkarr, pubky, iroh, btlink)
  uses stable ed25519 keys with no built-in anonymisation.
  GNUnet's R5N DHT achieves what we'd want but is incompatible
  with mainline.
- **No key rotation schedule.** The Layer-D schema is already
  forward-prepared with an optional `next_pubkey` field so a
  future client can publish a new key under the old one's
  signature, mirroring Tor v3's time-period-key chain — but
  the actual rotation logic will land in v1.1.

If you need real publisher anonymity, layer your own Tor / VPN
/ i2p on top. SwartzNet's threat model scope is spam resistance
in distributed search, not traffic analysis resistance.

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

## Native GUI (v0.3.0+)

Alongside the CLI (`cmd/swartznet`) and the localhost web UI,
v0.3.0 ships a native cross-platform GUI built with Fyne v2
(BSD 3-Clause). All GUI code is Go — no HTML/CSS/JS.

The GUI binary is `cmd/swartznet-gui`. It starts its own daemon
(same engine + indexer + companion wiring as the CLI) and presents
five tabs:

  - **Downloads** — live torrent table with Name / Status / Progress
    / Size / Peers / Indexed columns. Toolbar actions: Add Magnet
    (with per-torrent "index this?" checkbox), Add .torrent file
    picker, **Create Torrent** (build a new .torrent from a local
    file or folder), Pause, Resume, Remove, Toggle Index.
  - **Search** — query input plus Local / Swarm / DHT checkboxes,
    results rendered as cards with Confirm / Flag buttons.
  - **Status** — dashboard cards for local-index stats, swarm peer
    counts, DHT publisher keyword table, Bloom filter load, and a
    per-indexer reputation list.
  - **Companion** — F3 companion publisher status, plus follow /
    unfollow management for subscribed publishers.
  - **Settings** — sharing-level radio (L0 / L1 / L2), per-type
    hit toggles (file paths, content snippets), and read-only
    display of the current daemon configuration.

A system tray icon (on Linux, macOS, Windows) keeps the daemon
running in the background when the window is closed. Completed
downloads fire desktop notifications.

### Creating a new torrent

The "Create Torrent" button in the Downloads tab opens a modal
that walks you through every field in a `.torrent` file:

  - **Root** — file or folder to share. Folder produces a multi-
    file torrent following BEP-3 conventions; file produces a
    single-file torrent.
  - **Name** — overrides the info.name field. Leave empty to use
    the basename of Root.
  - **Piece length** — Auto / 64 KiB / 256 KiB / 1 MiB / 2 MiB /
    4 MiB / 8 MiB / 16 MiB. Auto uses `metainfo.ChoosePieceLength`
    which targets 1024–2048 pieces total.
  - **Trackers** — one announce URL per line, optional. Leaving it
    empty produces a DHT-only torrent.
  - **Webseeds** — optional HTTP(S) URLs (BEP-19) that serve the
    exact content layout as an alternative download source.
  - **Comment** — arbitrary human-readable note.
  - **Private** — BEP-27 flag that disables DHT and PEX peer
    discovery. Useful for private-tracker uploads.
  - **Output .torrent path** — where to save the .torrent file.
  - **Start seeding immediately** — when checked, the newly-created
    torrent is added to the engine and seeded from the same Root
    path right away.

Piece hashing is synchronous and I/O-bound. A ~1 GiB folder takes
seconds; 100 GiB takes minutes. A "Hashing pieces..." modal with
an indeterminate progress bar stays up until hashing completes.

The underlying engine method is `Engine.CreateTorrent(opts)` /
`Engine.CreateTorrentFile(opts, outPath)`, both wrapping
`metainfo.Info.BuildFromFilePath` from anacrolix/torrent.

### Per-torrent indexing control

By default every torrent added to SwartzNet is indexed: its
metadata (name, file list, trackers) is written to the Bleve
index within seconds, and as each file finishes downloading the
extraction pipeline (PDF / EPUB / DOCX / ODT / plaintext /
subtitles) feeds the text to Bleve content documents.

Two ways to opt a torrent out:

  1. When adding via **Add Magnet**, uncheck the "Index this
     torrent's files after download" checkbox in the dialog. The
     torrent will download and seed, but nothing about it hits
     your local search index.
  2. Select any row and click **Toggle Index**. The new state is
     reflected in the "Indexed" column and takes effect
     prospectively — file completions from that point forward
     skip the pipeline, but content already indexed is not
     removed (use `indexer.DeleteContentForTorrent` via a future
     admin command if you want a full scrub).

The **global** `--no-index` flag on `swartznet add` is a stronger
switch: it prevents the Bleve index from being opened at all, so
no torrent — past, present, or future — contributes anything.

### Building the GUI

The GUI requires CGo (for Fyne's OpenGL bindings). Native build
deps per platform:

  - **Linux (Ubuntu/Debian):** `sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev`
  - **macOS:** `xcode-select --install`
  - **Windows:** MSYS2 with `mingw-w64-x86_64-toolchain`

Then run:

```
scripts/build-gui.sh v0.3.0
```

This produces `dist/swartznet-gui-v0.3.0-<OS>-<ARCH>` for the
native host. For cross-platform release builds, install and use
[fyne-cross](https://github.com/fyne-io/fyne-cross) (Docker-based):

```
go install github.com/fyne-io/fyne-cross@latest
fyne-cross linux   -app-id net.swartznet.gui ./cmd/swartznet-gui
fyne-cross windows -app-id net.swartznet.gui ./cmd/swartznet-gui
fyne-cross darwin  -app-id net.swartznet.gui ./cmd/swartznet-gui
```

### GUI vs CLI vs web UI

All three frontends talk to the same internal packages; they differ
only in presentation:

  - **CLI** (`swartznet add|search|status|flag|confirm`) — scriptable,
    Unix-pipe friendly, no CGo, smallest binary (~40 MB).
  - **Web UI** (`http://localhost:7654/` while `swartznet add` is
    running) — zero-install access from any browser, works over SSH
    port-forward.
  - **Native GUI** (`swartznet-gui`) — desktop-native window, system
    tray, desktop notifications, no browser required. Requires CGo.

The three can all run simultaneously: the GUI starts its own daemon
and by default also binds the HTTP API on `localhost:7654`, so you
can run `swartznet search --swarm "ubuntu"` in another terminal
against the same instance.

## Where to file issues

For now, the project lives at
<https://github.com/claudenstein/swartznet>. Issues are
welcome. Pull requests with new extractors, additional
language stop-word lists, or implementations of the draft
BEPs in other clients are even more welcome.
