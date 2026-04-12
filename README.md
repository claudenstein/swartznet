# SwartzNet

**A BitTorrent client that lets you search inside the files you share — and find torrents that other peers publish — all on the same mainline DHT every other client already uses.**

[![Go 1.22+](https://img.shields.io/badge/Go-1.22%2B-00ADD8?logo=go)](https://go.dev/) [![License: Apache 2.0](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE) [![Release](https://img.shields.io/github/v/release/claudenstein/swartznet?include_prereleases)](https://github.com/claudenstein/swartznet/releases)

SwartzNet behaves like any other BitTorrent client on the wire — a vanilla peer sees a normal BEP-3/5/9/10/44 connection — but it adds full-text search across the content you've downloaded, optional keyword search across every SwartzNet peer you're connected to, and a DHT-backed discovery layer that lets you find torrents by topic rather than just by infohash.

---

## Features

- **Search inside your torrents, not just their names.** PDFs, EPUB / DOCX / ODT office docs, subtitles (SRT/VTT), plaintext and source code are text-extracted on completion and indexed with [Bleve](https://github.com/blevesearch/bleve). Bleve's query syntax (`+required`, `-excluded`, `"phrases"`, `field:value`) all work.
- **Three frontends, one daemon.** CLI for scripting, browser-based web UI at `http://localhost:7654/`, and a native cross-platform desktop GUI — all running against the same engine, index, and config.
- **Distributed keyword search, no new network.** Two peers that speak the `sn_search` BEP-10 extension can query each other's indexes over the same TCP connection they're already using for BitTorrent. Keyword → infohash pointers published on the existing mainline DHT via BEP-44 mutable items.
- **Spam resistance built in.** A persistent Bloom filter of known-good infohashes (auto-populated by successful downloads), a Bayesian-smoothed per-publisher reputation tracker, and a targeted "flag this is spam" gesture that demotes only the indexers responsible for the flagged hit.
- **Full torrent-client features.** Create new `.torrent` files, select which files in a multi-file torrent to actually download, cap upload/download bandwidth, cap concurrent active downloads, per-torrent indexing opt-out, system-tray operation on Linux / macOS / Windows.
- **Fully backwards compatible.** No new reserved bit, no new DHT verb, no new UDP port. A vanilla client sees a regular peer speaking BEP-3/5/9/10/44.

---

## Install

### Download a pre-built binary

Head to the [latest release](https://github.com/claudenstein/swartznet/releases/latest) and download:

| Platform | CLI | GUI |
|---|---|---|
| Linux x86_64 | `swartznet-vX.Y.Z-linux-amd64` | `swartznet-gui-vX.Y.Z-linux-amd64` |
| Linux ARM64 | `swartznet-vX.Y.Z-linux-arm64` | (build from source) |
| macOS Intel | `swartznet-vX.Y.Z-darwin-amd64` | (build from source) |
| macOS Apple Silicon | `swartznet-vX.Y.Z-darwin-arm64` | (build from source) |
| Windows x86_64 | `swartznet-vX.Y.Z-windows-amd64.exe` | (build from source) |

Verify the download with the `SHA256SUMS` file attached to the release:

```bash
sha256sum -c SHA256SUMS
```

Then `chmod +x swartznet-*` on Unix-likes and move it somewhere on your `$PATH`.

The CLI binary is fully static (no CGo, no glibc dependency). The GUI binary requires a working OpenGL stack — on Linux that means `libgl1` and a running X11 or Wayland session.

### Build from source

Requires Go 1.22 or later (1.24+ recommended).

```bash
# CLI only — pure Go, cross-compiles to every platform.
go build -o swartznet ./cmd/swartznet

# Native GUI — requires CGo and platform build deps.
# Linux:   sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev
# macOS:   xcode-select --install
# Windows: MSYS2 with mingw-w64-x86_64-toolchain
./scripts/build-gui.sh v0.3.0-dev
```

For cross-platform GUI release builds use [fyne-cross](https://github.com/fyne-io/fyne-cross) (Docker-based). Details in [docs/08-operations.md](docs/08-operations.md#native-gui-v030).

---

## Quick start

### Add and index a torrent

```bash
# Downloads + seeds, auto-indexes metadata and file contents.
# Also starts the HTTP API on localhost:7654 so you can open the
# web UI in a browser or use --swarm / --dht in another terminal.
swartznet add "magnet:?xt=urn:btih:..."
```

### Search

```bash
# Local index only — works without a running daemon.
swartznet search ubuntu
swartznet search --limit 50 "ubuntu 24.04"
swartznet search +linux -windows name:iso

# Include search-capable peers you're connected to.
swartznet search --swarm ubuntu

# Also query the BEP-44 DHT keyword index.
swartznet search --swarm --dht ubuntu
```

### Create a new torrent

```bash
# Build a .torrent from a local folder, with trackers.
swartznet create -o myfiles.torrent \
  --tracker https://tracker.example.com/announce \
  --comment "My collection" \
  /path/to/my-content

# Same, but also start seeding it from the current host.
swartznet create -o myfiles.torrent --seed \
  --data-dir ~/share \
  /path/to/my-content
```

### Open the GUI

```bash
swartznet-gui
```

The window presents five tabs — Downloads, Search, Status, Companion, Settings — and the app runs in your system tray so closing the window doesn't stop the daemon. See [docs/08-operations.md](docs/08-operations.md#native-gui-v030) for a tour of each tab.

### Or use the browser

Any running `swartznet add` daemon serves a single-page web UI at `http://localhost:7654/`. Same features as the native GUI, accessible over SSH port-forward without any extra install.

---

## Three frontends, one daemon

| Frontend | When it's the right choice | Binary |
|---|---|---|
| **CLI** (`swartznet`) | Scripts, SSH sessions, CI, headless servers | Static, ~40 MB, no CGo |
| **Web UI** (`http://localhost:7654/`) | Remote access via port-forward, no extra install | Embedded in the CLI binary via `go:embed` |
| **Native GUI** (`swartznet-gui`) | Desktop use with system tray, native file dialogs, create-torrent wizard, context menu | Fyne v2.7, ~46 MB, CGo (OpenGL) |

All three call into the same `internal/daemon` package and can run concurrently against the same data directory.

---

## How it works

SwartzNet embeds [`anacrolix/torrent`](https://github.com/anacrolix/torrent) (Go, MPL 2.0) as its BitTorrent engine and layers three search features on top:

- **Layer L (local):** piece-completion callback feeds downloaded file contents through a text-extractor registry (PDF, EPUB, DOCX, ODT, plaintext, subtitles) into a local Bleve full-text index.
- **Layer S (peer-wire):** a BEP-10 extension called `sn_search` that lets two SwartzNet peers exchange keyword queries over the same TCP connection they're using for BitTorrent. Strictly opt-in — vanilla peers never see a search message.
- **Layer D (DHT):** keyword → infohash mappings published as BEP-44 mutable items on the existing mainline DHT, keyed by per-publisher ed25519 pubkey with the keyword as salt.

Read [`docs/05-integration-design.md`](docs/05-integration-design.md) for the architecture diagram, the `sn_search` wire format, the ingestion pipeline, and the threat model.

---

## Documentation

The [`docs/` directory](docs/) is organized by audience. Start with [`docs/README.md`](docs/README.md) for a guide, or jump straight to:

- **Using SwartzNet:** [`docs/08-operations.md`](docs/08-operations.md)
- **Porting `sn_search` to another client:** [`docs/06-bep-sn_search-draft.md`](docs/06-bep-sn_search-draft.md) and [`docs/07-bep-dht-keyword-index-draft.md`](docs/07-bep-dht-keyword-index-draft.md)
- **Architecture:** [`docs/05-integration-design.md`](docs/05-integration-design.md)
- **Research background:** [`docs/01-04`](docs/) plus [`docs/09`](docs/09-v1-blocker-research.md) and [`docs/10`](docs/10-bitcoin-lessons.md)
- **History:** [`CHANGELOG.md`](CHANGELOG.md) (releases) and [`docs/MILESTONES.md`](docs/MILESTONES.md) (milestone-by-milestone engineering log)

---

## Compatibility and non-goals

**Backwards compatibility.** A vanilla BitTorrent client connecting to SwartzNet sees a standard BEP-3 handshake with the LTEP bit set and a standard LTEP `m` dictionary; if it doesn't advertise `sn_search`, we never send it a search message. Items we store on the DHT are standard BEP-44 mutable items, served by vanilla nodes under the same rules as any other BEP-44 data. See [`docs/05-integration-design.md`](docs/05-integration-design.md) §8 for the explicit test matrix.

**Non-goals.** SwartzNet is not Tor. Users who need network-level anonymity should layer a VPN or Tor transport themselves. SwartzNet also does not enforce copyright or jurisdictional content restrictions — users are responsible for what they search and download, as with every existing BitTorrent client. See [`docs/05-integration-design.md`](docs/05-integration-design.md) §9 for the full threat model.

---

## Configuration

Default paths (Linux/macOS):

- Downloaded data: `~/.local/share/swartznet/data`
- Search index: `~/.local/share/swartznet/index`
- Persistent identity (ed25519 keypair): `~/.local/share/swartznet/identity.key`
- Known-good Bloom filter: `~/.local/share/swartznet/known-good.bloom`
- Reputation tracker: `~/.local/share/swartznet/reputation.json`

All honour `$XDG_DATA_HOME`. All overridable with `--data-dir` / `--index-dir` and siblings. See `swartznet help` for the full flag list.

The HTTP API binds to `localhost:7654` by default (loopback only). Override with `--api-addr`; set to empty string to disable.

---

## Development

Main branch is where active work happens. Pre-releases land on `v0.x.y` tags; the first GA will be `v1.0.0`.

```bash
# Run the test suite under -race.
go test -race ./...

# Build both binaries.
go build -o dist/swartznet ./cmd/swartznet
./scripts/build-gui.sh dev
```

See [`docs/05-integration-design.md`](docs/05-integration-design.md) §12 for the roadmap.

**Contributing.** APIs are still in motion, so please open an issue before sending large patches so we can align on the approach.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).

The BitTorrent engine is `anacrolix/torrent` under MPL 2.0; the rest of this repository is Apache 2.0.
