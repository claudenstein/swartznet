# Overnight test-harness expansion — 2026-04-20

Branch: `overnight/test-harness-2026-04-20`
Start: 2026-04-20 22:54 local

## Baseline state (Phase 0)

All local verification green on `main` at commit `1402218`:

- `go build ./cmd/swartznet`: clean
- `go vet ./...`: clean
- `go test ./... -count=1 -short`: all 14 packages pass
- `go test -race ./...`: all pass (testlab takes ~11s)

## Observed gaps

1. **`tests/torrent-test/`** — `task-spec.txt` defines a 1-seeder + 3-peer
   Gutenberg-content scenario. `source/`, `seeder/`, `peer1/`…`peer3/` all
   empty. Commit `1402218` ("checkpoint torrent-test task-spec") paused
   the work. This is the highest-value gap to close.
2. **Layer-B Docker testbed** — one scenario only (`s1-healthy-search.sh`,
   a healthz probe). Netem profiles (lossy/DSL/4G/LAN) unused. Blocked on
   this box: `docker compose` v2 plugin is not installed.
3. **Wire-compat matrix (`docs/05 §8`)** — documented as prose. No single
   automated checker confirms every case is still green. Audit in flight.
4. **Per-binary smoke** — no end-to-end test drives real `dist/swartznet`
   binaries in a multi-process configuration (only in-process `testlab`).

## Plan (phases)

| Phase | Work | Output |
|---|---|---|
| 0 | Baseline + audit (this note) | Progress log + audit report |
| 1 | Multi-peer loopback harness (bash + real binaries) | `scripts/test-multi-peer.sh`, fixture generator |
| 2 | Layer-B scenario expansion | *Deferred: needs docker compose v2* |
| 3 | Wire-compat matrix automation | New testlab cases for each `§8` row |
| 4 | Bug-fix loop as defects surface | Per-defect reproducer + fix commits |

## Run constraints

- Subagent model: `sonnet` (Sonnet 4.6)
- Stop at 20 commits, 3 consecutive failures, OR any suite turning red
  and not fixable in 1 iteration
- Every commit rebuilds both `dist/swartznet` and
  `dist/swartznet-gui-dev-linux-amd64` per `CLAUDE.md` discipline
- Push branch after every commit

## Wire-compat matrix audit (Phase 0 output)

Audit of `docs/05-integration-design.md` §8 against `internal/testlab/`,
`internal/swarmsearch/`, `internal/dhtindex/`, `internal/companion/`,
`internal/engine/`.

| Case | Description | Status | Evidence |
|---|---|---|---|
| 8.1-A | Vanilla qBittorrent receives pieces from us | WEAK | no explicit assertion |
| 8.1-B | BEP-9 `ut_metadata` served to vanilla peer | WEAK | `internal/swarmsearch/protocol_test.go:84` sets up vanilla peer; no BEP-9 fetch assertion |
| 8.1-C | `ut_pex` passthrough; our LTEP keys ignored | WEAK | `TestProtocolOnRemoteHandshakeIncapable` covers handshake side only |
| 8.2-A | We never send `sn_search` to Transmission-only swarm | COVERED | `protocol_test.go:79`, `on_remote_handshake_test.go:62` |
| 8.2-B | Same as 8.2-A for libtorrent 2.x | COVERED | same tests |
| 8.3-A | Vanilla DHT `ping` → reply | **MISSING** | none |
| 8.3-B | Vanilla DHT `get_peers` → reply + token | **MISSING** | none |
| 8.3-C | Vanilla DHT `announce_peer` → stored | **MISSING** | none |
| 8.3-D | Vanilla BEP-44 `get`/`put` of our keyword item | WEAK | `layerd_test.go:42` only exercises publisher→our-lookup |
| 8.4-A | Both peers `sn_search` — queries/results flow | COVERED | `cluster_test.go:80`, `minipeer_scenario_test.go:36` |
| 8.4-B | C1→C0 content scope → reject code 2 | **MISSING** | `wire.go:24` constant defined, never asserted |
| 8.4-C | Gossip-discovered pubkey auto-added after handshake | WEAK | announce/multi-indexer paths tested, not the link |
| 8.4-D | `sn_search_v: 1` ignores unknown fields from v2 | COVERED | `minipeer_adversarial_test.go:107` |

### Top 5 gaps to close (ranked)

1. **8.3-A/B/C — KRPC ping / get_peers / announce_peer.** Foundation of
   DHT participation; currently delegated to anacrolix with zero regression
   fence. One test spinning up a minimal KRPC responder closes three rows
   and guards against anacrolix delegation breakage.
2. **8.4-B — C1→C0 content scope reject code 2.** `RejectUnsupportedScope`
   is defined but the dispatch path is untested; silent scope escalation
   could slip in on refactor.
3. **8.1-A — Vanilla peer receives pieces without any `sn_search` frame.**
   The core backwards-compat claim; a MiniPeer variant that advertises no
   `sn_search` and asserts piece data arrives + no extension frames are
   sent is a one-commit fix.
4. **8.3-D — Vanilla BEP-44 getter reads our item.** The whole
   justification for BEP-44 is "our items look like any other BEP-44 item";
   currently only same-codebase lookup is tested.
5. **8.4-C — Handshake adds gossip-discovered pubkey to indexer set.**
   Architecturally implicit, never asserted; needs a 2-node cluster test
   inspecting `Lookup.Indexers()` pre- and post-handshake.

## Iteration log

- **2026-04-20 22:54** — Phase 0 started. Baseline green; branch created;
  progress log + audit captured (this commit).
- _(next iterations to be appended here)_

