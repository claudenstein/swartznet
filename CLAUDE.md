# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

SwartzNet is a Go BitTorrent client that embeds [`anacrolix/torrent`](https://github.com/anacrolix/torrent) and layers full-text search on top. The load-bearing constraint is **mainline compatibility**: no new reserved bit, no new DHT verb, no new UDP port. A vanilla client must see nothing but BEP-3/5/9/10/44 traffic. Any change that would break this must be called out.

Deep architecture lives in `docs/05-integration-design.md`. The `sn_search` peer-wire protocol and the BEP-44 keyword index are each specified as standalone BEP drafts in `docs/06-*.md` and `docs/07-*.md` — if you touch wire format, keep those specs in sync.

## Build

Use `/usr/local/go/bin/go` (this project pins Go 1.24.1 in `go.mod`; the system `go` may be older).

```bash
# CLI — pure Go, CGO_ENABLED=0, cross-compiles anywhere.
go build -o dist/swartznet ./cmd/swartznet

# GUI — requires CGo (Fyne/OpenGL) so builds only for the host.
./scripts/build-gui.sh dev     # writes dist/swartznet-gui-dev-$GOOS-$GOARCH

# Full cross-platform release set + SHA256SUMS.
./scripts/build-release.sh v0.3.1
```

After any code change, rebuild **both** `dist/swartznet` and `dist/swartznet-gui-dev-linux-amd64` in the same turn — the user runs binaries from `dist/`, not `go run`.

## Test

```bash
go test ./... -count=1 -short        # fast path used by iteration loop
go test -race ./...                  # full race-enabled sweep
go test -run TestFoo ./internal/engine   # single test
```

Most packages have extensive table-driven tests and "branch coverage" style tests named like `foo_errors_test.go` / `foo_branches_test.go`. When fixing a bug, add a matching `*_test.go` case; when adding a branch, add a test that exercises it. The `.claude-iterate.sh` script treats coverage growth as the default improvement signal.

Integration testbed (Docker + netem scenarios) lives in `testbed/`; multi-peer fixtures live in `tests/torrent-test/`.

## Repository layout

- `cmd/swartznet` — CLI entrypoint; `main.go` dispatches to `cmd_*.go` per subcommand (`add`, `search`, `create`, `index`, `files`, `flag`, `status`, `trust`).
- `cmd/swartznet-gui` — Fyne-based native GUI; thin shim that constructs a `daemon.Daemon` and hands it to `internal/gui`.
- `cmd/dht-smoke` — standalone DHT probe for ops debugging.
- `internal/daemon` — **the wiring point**. `daemon.New(ctx, Options)` constructs engine + indexer + companion pub/sub + httpapi and is the single source of truth for startup order and cleanup. All three frontends call into it.
- `internal/engine` — BitTorrent engine wrapper around `anacrolix/torrent`. Owns piece-completion callbacks, file-priority, rate-limits, session save/restore, `.torrent` creation, publisher signing integration.
- `internal/indexer` — Bleve schema (currently v3), ingestion pipeline, text extractors under `indexer/extractors/`. Extractor registry is where PDF/EPUB/DOCX/ODT/plaintext/subtitle support lives.
- `internal/search` — query engine over Bleve, shared by local CLI, HTTP API, swarm and DHT layers.
- `internal/swarmsearch` — Layer S: `sn_search` BEP-10 extension. LTEP message envelope, services-bit capability negotiation, LRU hit cache.
- `internal/dhtindex` — Layer D: BEP-44 mutable-item keyword index publisher/fetcher.
- `internal/companion` — Companion-index torrents (BEP-46 pointer pattern) for publishing/fetching compact content indexes.
- `internal/httpapi` — localhost HTTP API (default `:7654`) plus embedded web UI (`httpapi/web/`).
- `internal/identity` / `internal/signing` / `internal/trust` / `internal/reputation` — ed25519 identity, `.torrent` signing (`snet.pubkey`/`snet.sig` fields, infohash-preserving), publisher allowlist, Bayesian-smoothed reputation + known-good Bloom filter.
- `internal/config` — config struct and XDG path resolution.
- `internal/gui` — Fyne widgets/tabs (Downloads, Search, Status, Companion, Settings) backed by a `Daemon`.
- `internal/testlab` — shared test helpers.

## Architecture anchors

**Three frontends, one daemon.** CLI, web UI (embedded via `go:embed` in the CLI binary), and native GUI all obtain a fully-wired node from `internal/daemon`. They differ only in presentation. Changes to subsystem lifecycle belong in `daemon.New` / `Daemon.Close`, not in each frontend.

**Three search layers, strict isolation.** Layer L (local Bleve) never knows about the wire; Layer S (`sn_search` peer-wire) never knows about Bleve internals; Layer D (DHT BEP-44) never knows about the HTTP API. The only cross-layer type is a small `SearchResult` struct. Preserve these boundaries when adding features.

**Per-torrent indexing is opt-in at runtime, opt-out globally.** `Engine.SetTorrentIndexing(hex, bool)` toggles per-torrent; the global `--no-index` flag prevents Bleve from opening at all (and cascades to disable Layer D publishing). The flag is checked in `engine.autoIndex` (torrent-level) and `engine.ingestFileEvents` (content-level).

**Identity is persistent and load-bearing.** `~/.local/share/swartznet/identity.key` (mode 0600) backs both Layer-D publishing and optional `.torrent` signing. Losing it means losing publisher reputation. Never regenerate implicitly.

**Default data paths (XDG-aware, all overridable via flags):**
- `~/.local/share/swartznet/data` — downloaded content
- `~/.local/share/swartznet/index` — Bleve index
- `~/.local/share/swartznet/identity.key` — ed25519 keypair
- `~/.local/share/swartznet/known-good.bloom` — spam-resistance Bloom filter
- `~/.local/share/swartznet/reputation.json` — per-pubkey reputation tracker

## Conventions

**MPL 2.0 discipline.** `anacrolix/torrent` is MPL 2.0 (file-level copyleft). Prefer extension-API usage (callbacks, `LocalLtepProtocolMap.AddUserProtocol`, direct DHT library access) over patches to the vendored library. New files in this repo stay Apache 2.0.

**Wire-compat test matrix.** `docs/05-integration-design.md` §8 lists the vanilla-client interop cases. Any change to handshakes, LTEP, or DHT items must keep that matrix green.

**Docs and CHANGELOG track the code.** Update `docs/` and `CHANGELOG.md` alongside any behavior change — the release docs anchor the planned white paper. Architectural decisions belong in `docs/05-integration-design.md`; per-milestone history in `docs/MILESTONES.md`.

**Commits get pushed.** Every commit on `main` is pushed to `origin/main` immediately.
