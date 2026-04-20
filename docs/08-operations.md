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
├── identity.key                ← ed25519 publisher keypair, mode 0600
├── publisher.json              ← per-keyword DHT publish manifest (JSON)
├── reputation.json             ← per-pubkey reputation tracker (JSON)
├── seeds.json                  ← curated indexer-seed list (JSON), optional
├── known-good.bloom            ← Bloom filter of confirmed infohashes
├── trust.json                  ← publisher trust list (JSON, v0.5+)
├── companion-follows.json      ← followed companion publishers (JSON)
├── companion/                  ← F3 companion publisher artefacts
│   ├── current.json.gz         ← serialised local index snapshot
│   └── current.torrent         ← torrent wrapping the snapshot
├── data/                       ← downloaded torrent files
│   ├── session.json            ← open-torrents manifest (re-added on startup)
│   ├── torrents/               ← .torrent file copies for session restore
│   │   ├── <infohash>.torrent
│   │   └── …
│   ├── ubuntu-24.04-amd64.iso
│   └── …
└── index/                      ← Bleve full-text index directory
    ├── index_meta.json
    └── store/…
```

Each of these can be relocated independently via the
matching `--data-dir`, `--index-dir`, etc. CLI flags or by
setting `XDG_DATA_HOME` to a different root.

### What to back up

| File | Why | Recoverable if lost? |
|---|---|---|
| `identity.key` | Your publisher pubkey. The same key signs Layer-D entries (`07-bep-dht-keyword-index-draft.md`) AND the `.torrent` files you mint with `--sign` (`11-signing-protocol.md`). Lose it and you lose your portable publisher identity. | No — generate a new one and start over. |
| `trust.json` | Your publisher trust list. Each entry causes torrents signed by that pubkey to be auto-confirmed and surfaced as trusted. | Yes — re-add the pubkeys via `swartznet trust add`. |
| `reputation.json` | The history that informs your indexer-quality scores. | Yes, slowly — counters rebuild as you query. |
| `known-good.bloom` | Your downloaded-and-confirmed infohashes; drives the Bloom-hit boost in lookups. | Partly — auto-confirm rebuilds it as you re-download. |
| `publisher.json` | The list of keywords you have published and their hits. | Yes — re-published from your local index on next add. |
| `companion/` | The serialised local-index snapshot the F3 companion publisher seeds. | Yes — regenerated on next companion publish cycle. |
| `companion-follows.json` | Publishers you subscribe to. | Yes — re-add via the GUI or the HTTP API. |
| `data/session.json` | The list of open torrents and their state (paused / indexing / queue order). Re-added on next daemon start. | Yes — every Add/Remove/Pause/Resume/SetIndexing call rewrites it. |
| `data/torrents/` | Per-torrent `.torrent` file copies used by session restore (preserves signing fields). | Yes — magnet adds re-fetch metadata over the swarm; file adds with a missing copy fall back to magnet URI. |
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

# Restrict to torrents minted by one specific publisher
swartznet search --signed-by <64-char-hex-pubkey> "release notes"

# Snapshot of local index, swarm peers, DHT publisher, Bloom + reputation
swartznet status

# JSON output for scripts
swartznet status --json
swartznet search --swarm --dht --json "ubuntu"

# Mark a hit as good (auto-confirm normally handles this)
swartznet confirm <40-char-hex-infohash>

# Mark a hit as spam — demotes every indexer that returned it
swartznet flag <40-char-hex-infohash>

# Create a new .torrent file, signed by the local identity
swartznet create ./release-build -o release.torrent --sign \
  --tracker "udp://tracker.opentrackr.org:1337/announce" \
  --comment "Release v2.0"

# Toggle indexing for a single torrent (operates against the running daemon)
swartznet index <infohash> off

# List or change per-file priorities in a multi-file torrent
swartznet files <infohash>
swartznet files <infohash> 4 high

# Manage the publisher trust list (offline; no daemon required)
swartznet trust list
swartznet trust add <64-char-hex-pubkey> "Alice's release key"
swartznet trust remove <64-char-hex-pubkey>
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

### Selecting specific files in a multi-file torrent

When you add a multi-file torrent (TV season, photo archive,
software distribution), SwartzNet defaults to downloading every
file. To opt individual files out — save bandwidth, save disk
space, or just avoid downloading content you don't want:

1. Select the torrent row in the **Downloads** tab.
2. Click **Files...** in the toolbar.
3. A modal opens with one row per file showing: path, size,
   live progress bar, and a priority dropdown with three
   options:
   - **none** — anacrolix skips this file entirely. Pieces
     that only contain this file are never requested from
     peers.
   - **normal** — default. File downloads at normal priority.
   - **high** — anacrolix prioritises this file's pieces over
     normal-priority ones.
4. The dropdown change takes effect immediately, even while
   the torrent is already downloading. No restart needed.
5. **Select All** / **Deselect All** bulk actions at the top
   of the dialog flip every file at once.

The progress bars in the Files dialog update every 2 seconds
while the dialog is open, so you can watch individual file
progress without leaving the modal.

Under the hood this wraps `anacrolix/torrent.File.SetPriority`.
The same control is available via HTTP:

```
POST /torrents/{infohash}/files/{index}/priority
  {"priority": "none"}
GET  /torrents/{infohash}/files
```

### Bandwidth rate limiting

SwartzNet defaults to unlimited upload and download throughput.
If you want to cap either (polite-mode seeding, sharing an
internet connection with others, metered mobile hotspot):

1. Open the **Settings** tab.
2. Enter a value in KiB/s for Upload and/or Download. Zero
   means unlimited.
3. Click **Apply**.

The limits take effect immediately across every peer
connection, no daemon restart required. A limit of 500 KiB/s
roughly equals 4 Mbit/s, the typical cap mentioned in
BitTorrent community etiquette ("share-ratio-friendly").

HTTP equivalents (live since v0.6.0):

```
POST /config/rate-limit
  {"upload_bps": 512000, "download_bps": 0}
GET  /config/rate-limit
```

Current state can be read via
`Engine.UploadLimitBytesPerSec()` /
`DownloadLimitBytesPerSec()` — zero means unlimited. The web
UI Settings tab and the native GUI Settings tab share the same
endpoints.

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

#### Watching indexing progress

For torrents with lots of small files (e.g. a Project Gutenberg
or Stack Exchange mirror in the hundreds-of-thousands range),
downloading finishes long before extraction does; the pipeline
reads each file, runs its format-specific extractor, and
commits chunks to Bleve one file at a time. The Web UI's
Downloads tab renders a second, thinner progress bar under
each torrent whose indexing is enabled, with a "🔍 indexed N /
M (X%)" label. The counter advances for every file the
pipeline has finished, including files it skipped (no
matching extractor — images, videos, archives) so the bar
always reaches 100% once the queue is drained. The underlying
counts are also available programmatically on every
`/torrents` poll: `indexed_files` (all processed) and
`index_extracted` (subset that produced chunks).

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

## Publisher signing (v0.4.0+)

SwartzNet can sign the `.torrent` files it mints with the
node's persistent ed25519 identity. The signature binds the
publisher's pubkey to the torrent's infohash, so any other
SwartzNet client can verify, at add time, "this metainfo was
minted by the holder of pubkey X". Vanilla BitTorrent clients
ignore the signing fields and treat the file as any other
`.torrent`.

The full wire format and verification algorithm live in
[`11-signing-protocol.md`](11-signing-protocol.md).

### Signing a torrent

CLI:

```
swartznet create ./release-build -o release.torrent --sign
```

`--sign` loads the identity at `--identity <path>` (default:
`~/.local/share/swartznet/identity.key`) and adds
`snet.pubkey` + `snet.sig` as optional top-level fields in the
metainfo dictionary. The infohash is unchanged — every other
client sees a normal `.torrent`. The CLI prints the signing
pubkey so you can confirm it matches what you intended.

GUI: the Create Torrent dialog (Downloads tab → Create
Torrent button) has a "Sign with my ed25519 identity"
checkbox enabled by default. When checked, the resulting
`.torrent` is signed with the daemon's identity.

### Verifying a torrent

When a `.torrent` file is added (CLI `swartznet add foo.torrent`
or GUI "Add .torrent" button), the engine attempts to verify
any signing fields it finds and stores the resulting pubkey
on the handle. `Handle.SignedBy()` returns the 64-char hex
pubkey or the empty string for unsigned/failed-verify.

Magnet adds (`xt=urn:btih:...`) cannot be verified at add
time because the metainfo bytes are not yet available; the
verification happens once ut_metadata fetch completes — but
the engine does not currently re-run verification at that
point, so magnet-added torrents always show `signed_by =""`
even when the underlying `.torrent` is signed. Adding via the
`.torrent` file path is the only path that captures the
signature today.

The native GUI also ships a "Verify signature..." dialog (right-
click any torrent in Downloads) that displays the full pubkey,
trust status, and the signed infohash.

## Publisher trust list (v0.5.0+)

Trust is a strictly local concept: every SwartzNet node
maintains its own JSON-backed allowlist of ed25519 pubkeys
whose signed `.torrent` files get implicit trust. Trusted
torrents are auto-confirmed to the known-good Bloom filter
the moment metadata arrives — no waiting for the download to
complete — and surfaced with a gold star in UIs that render
trust state.

The trust list lives at `~/.local/share/swartznet/trust.json`
(override with the `TrustPath` config field). The file is a
JSON array of `{"pubkey":"...","label":"..."}` objects;
mutations are atomic (tempfile + rename).

### Managing the list

CLI (works offline; does not need a running daemon):

```
swartznet trust list
swartznet trust add <pubkey> [<label>]
swartznet trust remove <pubkey>
swartznet trust list --json   # for scripts
```

GUI: Right-click any signed torrent in the Downloads tab and
choose "Trust this publisher" / "Revoke trust for this
publisher". The "Verify signature..." dialog shows current
trust status before you decide.

### What "trusted" actually does

For a torrent whose `signed_by` matches a trust-list entry:

1. **Auto-confirm to known-good.** As soon as metadata
   arrives, the infohash is added to the Bloom filter the
   reputation system uses to boost lookup ranking. You don't
   have to wait for the download to complete (and you don't
   have to manually `swartznet confirm`).
2. **`TrustedPublisher: true`** on the torrent snapshot,
   which the GUI renders as a gold ★ prefix in the Downloads
   "Signed" column and the web UI shows in the Status tab's
   "trusted" counter.
3. **Future:** trusted-publisher search results will get a
   gold-star badge in search hits (deferred to a release
   after v0.7 because today's `SearchHit` does not yet carry
   the trust flag — see CHANGELOG).

Trust is not transitive, not chained, and not protocol-level:
it's a local UI/UX hint backed by the cryptographic
guarantee that the signature is valid. Whether to trust a
specific pubkey is your call.

## Search by publisher (v0.7.0+)

Once your local index contains torrents from multiple signed
publishers, you can restrict search to a single publisher by
passing their pubkey. The filter applies to Layer L only —
the swarm and DHT layers don't carry signed metadata yet.

CLI:

```
swartznet search --signed-by <64-char-hex-pubkey> "release notes"
swartznet search --signed-by <pubkey> --limit 50 --json "kernel"
```

HTTP:

```
POST /search
  {"q": "release notes",
   "signed_by": "<64-char-hex-pubkey>"}
```

Web UI: the Search panel has a "publisher" text input next
to the search options. Paste a 64-char hex pubkey to filter,
or click the `✓ <prefix>` badge on any signed hit to pivot —
the badge populates the publisher input and re-runs the
search, giving you a one-click "everything else by this
publisher" workflow.

The filter is implemented in `internal/indexer/indexer.go` as
a Bleve term-match conjunction on the `signed_by` keyword
field (added to schema v3). An empty or absent filter
returns all hits as usual.

## Content extractors

When a file finishes downloading, the engine dispatches it to
an extractor based on MIME type / file extension. The
extracted text is chunked and added as Bleve content
documents, so a search like `"chapter 7"` finds the EPUB it
came from even if "chapter 7" never appears in the torrent
name or file path.

Nineteen extractors ship today (all pure-Go, no CGo):

| Format | Extractor | Surfaces |
|---|---|---|
| Plaintext / source code / HTML / JSON / XML / Markdown | `plaintext.go` | full text |
| Subtitles (SRT, VTT, ASS, SSA) | `subtitle.go` | dialog text only (timestamps stripped) |
| Archive contents listing (ZIP, TAR, TAR.GZ, TGZ) | `archive.go` | sorted file-name listing inside the archive |
| PDF | `pdf.go` | extracted text via `ledongthuc/pdf` |
| EPUB | `epub.go` | XHTML body text |
| DOCX | `docx.go` | `<w:t>` text runs |
| ODT | `odt.go` | `<text:p>` / `<text:h>` / `<text:span>` |
| RTF | `rtf.go` | text after stripping control words and groups |
| FB2 (FictionBook 2) | `fb2.go` | body paragraphs and titles |
| PPTX | `pptx.go` | every `<a:t>` in slide order |
| ODP | `odp.go` | text from LibreOffice Impress |
| MOBI / AZW / AZW3 | `mobi.go` | full title + EXTH metadata |
| ID3 (MP3 tags) | `id3.go` | TIT2/TPE1/TALB/TDRC/TCON/TRCK/TPUB/COMM/USLT |
| EXIF (JPEG metadata) | `exif.go` | camera make/model/software/artist/copyright/date/GPS |
| FLAC | `flac.go` | VORBIS_COMMENT block |
| OGG (Vorbis / Opus) | `ogg.go` | comment packet (re-uses FLAC parser) |
| MKV / WebM | `mkv.go` | EBML walker for Info / Tracks / Chapters / Tags |
| MP4 / M4A / M4B / M4V | `mp4.go` | iTunes-style atom set under `moov/udta/meta/ilst` |
| ZIM (OpenZIM / Kiwix) | `zim.go` | per-article HTML text from the cluster store; uncompressed + zstd; 5K-article / 32 MiB cap |

To add a new extractor: implement the `Extractor` interface
from `internal/indexer/extractors/extractor.go`, register it
via `extractors.Register(impl, claimsFn)` in an `init()`
block, add the file extension to `extTypes` if the stdlib
mime table doesn't know about it, and ship tests that
synthesize the format in-memory (the existing extractors do
this).

## Where to file issues

For now, the project lives at
<https://github.com/claudenstein/swartznet>. Issues are
welcome. Pull requests with new extractors, additional
language stop-word lists, or implementations of the draft
BEPs (see [`06-bep-sn_search-draft.md`](06-bep-sn_search-draft.md),
[`07-bep-dht-keyword-index-draft.md`](07-bep-dht-keyword-index-draft.md),
and [`11-signing-protocol.md`](11-signing-protocol.md)) in
other clients are even more welcome.
