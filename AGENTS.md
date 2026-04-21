# AGENTS.md

Short, repo-specific notes for agents working in SwartzNet. For deep
architecture, read `CLAUDE.md` and `docs/05-integration-design.md`. This
file only captures the things an agent is likely to get wrong without
help.

## What this is

SwartzNet is a Go BitTorrent client that embeds `anacrolix/torrent` and
layers full-text + distributed keyword search on top. The load-bearing
invariant is **mainline compatibility**: no new reserved bit, no new DHT
verb, no new UDP port. A vanilla BEP-3/5/9/10/44 peer must see a normal
connection. Any change that would break this needs to be called out.

## Toolchain

- `go.mod` pins **Go 1.24.1**. The system `go` on this box may be older —
  always invoke `/usr/local/go/bin/go` explicitly (also how the
  `.claude-iterate.sh` loop is set up).
- CI uses Go 1.24 (`.github/workflows/test.yml`).
- CLI is pure Go, `CGO_ENABLED=0`, cross-compiles anywhere. GUI requires
  CGo (Fyne / OpenGL) and only builds for the host platform without
  fyne-cross.

## Build

```bash
# CLI — pure Go
go build -o dist/swartznet ./cmd/swartznet

# GUI — host-only, needs gcc + libgl1-mesa-dev + xorg-dev + libxkbcommon-dev on Linux
./scripts/build-gui.sh dev         # writes dist/swartznet-gui-dev-<os>-<arch>

# Full cross-platform release set + SHA256SUMS
./scripts/build-release.sh v0.3.1
```

**After any code change, rebuild both `dist/swartznet` and
`dist/swartznet-gui-dev-linux-amd64` in the same turn** — the user runs
binaries out of `dist/`, not `go run`. `dist/` is gitignored.

## Test

```bash
go test ./... -count=1 -short                # fast path; default iterate-loop signal
go test -race ./...                          # full race sweep (run locally)
go test -run TestFoo ./internal/engine       # single test

# Integration-style scenarios — slow, run deliberately:
go test -race ./internal/testlab/...
```

CI quirks (`.github/workflows/test.yml`) that are easy to miss:

- CI **excludes `internal/testlab`** from `go test -race`: those
  scenarios spin up multiple anacrolix clients on loopback and are
  unreliable on shared CI runners. They must still pass locally.
- CI runs `go mod tidy` and fails if `go.mod` / `go.sum` drift.
- CI runs `gofmt -l -s cmd/ internal/` only — **do not** reformat
  `research/` (vendored upstream clones of libtorrent / transmission /
  anacrolix, not our code; also gitignored).
- CI runs `go vet ./...`.
- Coverage-focused tests are named `*_errors_test.go` or
  `*_branches_test.go`. When fixing a bug or adding a branch, add a
  matching test — the iterate loop treats coverage growth as the default
  improvement signal.

## Repository layout

- `cmd/swartznet` — CLI. `main.go` dispatches to `cmd_*.go` per
  subcommand (`add`, `search`, `create`, `index`, `files`, `flag`,
  `status`, `trust`, `confirm`).
- `cmd/swartznet-gui` — Fyne-based GUI; thin shim over `internal/gui`.
- `cmd/dht-smoke` — standalone DHT probe for ops.
- `internal/daemon` — **the wiring point.** `daemon.New(ctx, Options)`
  is the single source of truth for startup order and cleanup. All
  three frontends call into it. Lifecycle changes belong here, not in a
  frontend.
- `internal/engine` — BitTorrent engine wrapper around
  `anacrolix/torrent`.
- `internal/indexer` — Bleve schema + ingestion + text extractors
  (`extractors/` is the registry for PDF/EPUB/DOCX/ODT/plaintext/subs).
- `internal/search` — query engine over Bleve.
- `internal/swarmsearch` — Layer S (`sn_search` BEP-10 extension).
- `internal/dhtindex` — Layer D (BEP-44 mutable-item keyword index).
- `internal/companion` — companion-index torrents (BEP-46 pointers).
- `internal/httpapi` — localhost HTTP API (default `:7654`) + embedded
  web UI under `httpapi/web/` (served via `go:embed` from the CLI).
- `internal/identity` / `signing` / `trust` / `reputation` — ed25519
  identity, `.torrent` signing, allowlist, Bayesian-smoothed reputation
  + known-good Bloom filter.
- `internal/config`, `internal/gui`, `internal/testlab`.
- `testbed/` — Docker + netem multi-peer scenarios.
- `tests/torrent-test/` — multi-peer fixtures.
- `research/` — gitignored upstream clones; never our code.

## Non-obvious architectural invariants

- **Three frontends, one daemon.** CLI, web UI, and native GUI all
  construct a fully-wired node via `internal/daemon`. Don't duplicate
  wiring in a frontend.
- **Three search layers, strict isolation.** Layer L (local Bleve) never
  knows the wire; Layer S (`sn_search` peer-wire) never knows Bleve
  internals; Layer D (DHT BEP-44) never knows the HTTP API. The only
  cross-layer type is a small `SearchResult` struct.
- **Indexing opt-outs.** `Engine.SetTorrentIndexing(hex, bool)` toggles
  per-torrent; the global `--no-index` flag prevents Bleve from opening
  at all and **cascades to disable Layer D publishing**. Flag is
  checked in `engine.autoIndex` (torrent-level) and
  `engine.ingestFileEvents` (content-level).
- **Identity is persistent and load-bearing.**
  `~/.local/share/swartznet/identity.key` (mode 0600) backs both Layer-D
  publishing and optional `.torrent` signing. Losing it loses publisher
  reputation. Never regenerate implicitly.
- **Wire-format specs track code.** `sn_search` and the BEP-44 keyword
  index are specified as standalone BEP drafts in `docs/06-*.md` and
  `docs/07-*.md`. If you touch wire format, update those.

## Default data paths (all XDG-aware, all overridable via flags)

- `~/.local/share/swartznet/data` — downloads
- `~/.local/share/swartznet/index` — Bleve index
- `~/.local/share/swartznet/identity.key` — ed25519 keypair
- `~/.local/share/swartznet/known-good.bloom` — spam-resistance filter
- `~/.local/share/swartznet/reputation.json` — per-pubkey tracker

HTTP API binds `localhost:7654` (loopback only). `--api-addr ""`
disables it.

## Conventions

- **Licenses.** `anacrolix/torrent` is MPL 2.0 (file-level copyleft).
  Prefer extension-API usage (callbacks,
  `LocalLtepProtocolMap.AddUserProtocol`, direct DHT library access)
  over patching the vendored library. New files in this repo stay
  Apache 2.0.
- **Wire-compat test matrix.** `docs/05-integration-design.md` §8 lists
  the vanilla-client interop cases. Any handshake / LTEP / DHT change
  must keep that matrix green.
- **Docs + CHANGELOG track the code.** Update `docs/` and
  `CHANGELOG.md` alongside any behavior change — the release docs
  anchor the planned white paper. Architectural decisions go in
  `docs/05-integration-design.md`; milestone history in
  `docs/MILESTONES.md`.
- **Commits on `main` get pushed.** Every commit on `main` is pushed to
  `origin/main` immediately.

## Release flow

Pushing a tag `vX.Y.Z` triggers `.github/workflows/release.yml`:

- `build-cli` runs `scripts/build-release.sh` for 5 platforms (pure Go).
- `build-gui-linux` / `build-gui-darwin` (arm64 native + amd64 via
  `clang -arch x86_64`) / `build-gui-windows` build GUI binaries
  natively per runner (Fyne needs platform-specific C toolchains).
- `publish` regenerates `SHA256SUMS` over CLI + all GUI binaries and
  extracts release notes from `CHANGELOG.md` between `## vX.Y.Z` and
  the next `## ` heading. If that section is empty the release notes
  fall back to "See CHANGELOG.md for details." Anything with a `v0.`
  prefix is marked prerelease.

## Existing instruction files

- `CLAUDE.md` — long-form project instructions (Claude Code). Content
  overlaps this file; both are kept in sync.
- `docs/05-integration-design.md` — architecture canon.
- `docs/06-bep-sn_search-draft.md`, `docs/07-bep-dht-keyword-index-draft.md`
  — wire-format specs. Update alongside any wire change.
- `docs/08-operations.md` — user-facing operations guide.
- `docs/MILESTONES.md` — per-milestone engineering log.
