# Contributing to SwartzNet

Thanks for your interest. SwartzNet is pre-1.0 and the local APIs are still in
motion, so **please open an issue to discuss any non-trivial change before
sending a large patch** — it saves everyone a rewrite.

## Ground rules

1. **Mainline compatibility is load-bearing.** SwartzNet must look like an
   ordinary BitTorrent client on the wire: no new reserved handshake bit, no new
   DHT verb, no new UDP port. A vanilla peer must see nothing but
   BEP-3/5/9/10/44/46 traffic. Any change that would break this has to be called
   out and justified. The interop test matrix lives in
   [`docs/05-integration-design.md`](docs/05-integration-design.md) §8 — keep it
   green.

2. **Three frontends, one daemon.** The CLI, the embedded web UI, and the native
   Fyne GUI all obtain a fully-wired node from `internal/daemon`. Anything
   user-facing should be reachable from **both** the web UI and the native GUI —
   don't land a feature in only one.

3. **License discipline.** The BitTorrent engine (`anacrolix/torrent`) is MPL 2.0
   (file-level copyleft); the rest of this repo is Apache 2.0. Prefer the engine's
   extension APIs (callbacks, `LocalLtepProtocolMap.AddUserProtocol`, direct DHT
   access) over patching the vendored library. New files here stay Apache 2.0.

## Building and testing

Requires Go 1.24+ (pinned in `go.mod`).

```bash
# Build both binaries.
go build -o dist/swartznet ./cmd/swartznet   # CLI: pure Go, CGO_ENABLED=0
./scripts/build-gui.sh dev                   # native GUI: needs CGo (Fyne/OpenGL)

# Test.
go test ./... -count=1 -short   # fast path
go test -race ./...             # full race-enabled sweep — must pass before a PR
```

When you fix a bug, add a matching `*_test.go` case that fails without the fix.
When you add a branch, add a test that exercises it.

## Commits and PRs

- Group related edits into one commit with a clear, conventional message
  (`feat:` / `fix:` / `docs:` / `refactor:` …) that explains the **why**.
- Keep `docs/` and `CHANGELOG.md` in sync with behavior changes in the same PR.
- If you touch the wire format, update the standalone BEP drafts
  (`docs/06-*`, `docs/07-*`, `docs/11-*`) too.
- Open your PR against `main`.

## Reporting bugs and requesting features

Use GitHub Issues. For anything security-sensitive, see
[SECURITY.md](SECURITY.md) instead of filing a public issue.
