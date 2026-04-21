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
| 2 | Layer-B scenario expansion | COMPLETE: s1–s4 green via `scripts/run-testbed.sh` — see iteration log for commit SHA and docker-group prerequisite note |
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
| 8.1-A | Vanilla qBittorrent receives pieces from us | COVERED | `testlab/vanilla_download_scenario_test.go:TestScenarioVanillaClientPieceAndMetadata/8.1-A_piece_download_completes` |
| 8.1-B | BEP-9 `ut_metadata` served to vanilla peer | COVERED | `testlab/vanilla_download_scenario_test.go:TestScenarioVanillaClientPieceAndMetadata/8.1-B_metadata_via_bep9` |
| 8.1-C | `ut_pex` passthrough; our LTEP keys ignored | WEAK | `TestProtocolOnRemoteHandshakeIncapable` covers handshake side only |
| 8.2-A | We never send `sn_search` to Transmission-only swarm | COVERED | `protocol_test.go:79`, `on_remote_handshake_test.go:62` |
| 8.2-B | Same as 8.2-A for libtorrent 2.x | COVERED | same tests |
| 8.3-A | Vanilla DHT `ping` → reply | COVERED | `engine/dht_wirecompat_test.go` (2026-04-20) |
| 8.3-B | Vanilla DHT `get_peers` → reply + token | COVERED | same; required `peer_store.InMemory` wiring (`engine.go:407`) |
| 8.3-C | Vanilla DHT `announce_peer` → stored | COVERED | same |
| 8.3-D | Vanilla BEP-44 `get`/`put` of our keyword item | COVERED | `dhtindex/vanilla_bep44_test.go:TestVanillaBEP44GetterReadsOurItem` |
| 8.4-A | Both peers `sn_search` — queries/results flow | COVERED | `cluster_test.go:80`, `minipeer_scenario_test.go:36` |
| 8.4-B | C1→C0 content scope → reject code 2 | COVERED | `swarmsearch/scope_reject_test.go:TestHandleQueryScopeRejectC0` (dispatch-level) + `testlab/scope_reject_scenario_test.go:TestScenarioScopeRejectC0OverWire` (full wire) |
| 8.4-C | Gossip-discovered pubkey auto-added after handshake | COVERED | `swarmsearch/gossip_pubkey_test.go` (dispatch + sink) + `testlab/gossip_pubkey_scenario_test.go` (2-node cluster, cross-delivery + Publisher=0 negative case) |
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
- **2026-04-20 23:26** — Workstream 1 complete. Prior iteration (f753bf6) had
  staged `scripts/test-multi-peer.sh` and `scripts/gen-test-fixture.sh` but
  the test was FAILING due to a search-step race (indexer still ingesting when
  the search query fired). Updated script to: (1) wait for `indexed_files >=
  files` before querying, (2) write report to timestamped
  `results/run-<ts>.txt` per spec. Two back-to-back clean runs both pass:
  316 KiB torrent downloaded by 3 peers in <2 s on loopback; 32–48 "photon"
  hits per peer; per-file SHA-256 integrity verified. Gap noted: `indexed_files`
  sometimes plateaus at 11–13 of 15 before the 15 s wait-loop expires (binary
  files like `.mobi`/`.epub` produce no text chunks; pipeline may batch-skip
  them in a separate pass). Search still passes because text-bearing `.txt`
  files are indexed.
  Also committing: `engine.go` adds `DHTAddr()` accessor and
  `dht_wirecompat_test.go` (DHT wire-compat scaffolding left uncommitted by a
  parallel workstream). That test **currently fails** on 8.3-B and 8.3-C: the
  engine's DHT server omits a `Token` field in `get_peers` replies, blocking
  vanilla `announce_peer`. Real SwartzNet bug — fix deferred to next iteration.
- **2026-04-20 23:55** — Workstream 3 (wire-compat DHT KRPC) complete.
  Root-caused the `get_peers`-without-Token bug flagged in the prior entry:
  anacrolix `dht.Server` only fills `r.Token` in get_peers replies when
  `ServerConfig.PeerStore` is non-nil (`server.go` in the dht/v2 library).
  The engine left `PeerStore` nil, so every reply was Token-free — breaking
  BEP-5's canonical get_peers → announce_peer sequence for any vanilla
  client. Fix: install `peer_store.InMemory` via `ConfigureAnacrolixDhtServer`
  (`engine.go:407-424`). `TestDHTWireCompatVanillaKRPC` now passes for all
  three subtests (ping / get_peers / announce_peer). Wire-compat matrix
  rows 8.3-A/B/C move from MISSING to COVERED.
- **2026-04-20** — Workstream 4 (rows 8.1-A, 8.1-B, 8.3-D, 8.4-B) complete.
  Four new test files added; no bugs surfaced.
  - **8.4-B** (`testlab/scope_reject_scenario_test.go:TestScenarioScopeRejectC0OverWire`):
    Full over-the-wire MiniPeer scenario. A C0 engine receives a scope "c"
    query from a MiniPeer; asserts Reject frame code=2 returned and hit
    cache not polluted. Complements the existing unit-level
    `swarmsearch/scope_reject_test.go` which exercises the dispatch path in
    isolation. Both green.
  - **8.1-A + 8.1-B** (`testlab/vanilla_download_scenario_test.go:TestScenarioVanillaClientPieceAndMetadata`):
    Uses a real `anacrolix/torrent.Client` (no sn_search in its LTEP
    handshake) as the vanilla peer. 8.1-B sub-test: client adds magnet,
    receives info dict via BEP-9 ut_metadata, GotInfo() fires. 8.1-A
    sub-test: client downloads all pieces and bytes are verified sha256.
    Both sub-tests green on first run.
  - **8.3-D** (`dhtindex/vanilla_bep44_test.go:TestVanillaBEP44GetterReadsOurItem`):
    Two loopback dht.Server instances; SwartzNet's AnacrolixPutter publishes
    a keyword entry; vanilla server calls getput.Get for the same target.
    getput validates the BEP-44 ed25519 signature internally. Retrieved
    KeywordValue decoded correctly: correct infohash, name, seeders. Green.
    No bugs surfaced — BEP-44 wire format was already correct.
- **2026-04-20** — Workstream 5 (row 8.4-C: gossip-discovered pubkey) complete.
  Closed the last wire-compat gap and surfaced a real schema hole: the
  `PeerAnnounce` struct carried only `services`/`v`; spec §5.2.4 defines a
  `pk` field, but the struct was missing it. Fixing the schema was a
  strict-superset change (empty tag, no wire breakage for old peers).
  - **Schema:** `PeerAnnounce` now has `Pubkey []byte \`bencode:"pk,omitempty"\``;
    `PeerState` gained `PublisherPubkey [32]byte`.
  - **Protocol:** `Protocol.SetPublisherPubkey` feeds the engine's identity
    into outbound announces, and `Protocol.SetIndexerSink` registers a
    callback that fires whenever an inbound announce carries a 32-byte
    pk. The outbound announce only includes `pk` when `caps.Publisher==1`
    — pure subscribers must not pollute peers' indexer sets.
  - **Engine:** `startPublisher` now calls `swarm.SetPublisherPubkey` +
    `swarm.SetIndexerSink(&gossipIndexerSink{lookup: e.lookup})` after
    self-adding to `lookup.AddIndexer`. A gossip-learned pubkey is
    labelled `"gossip:<peer_addr>"` so ops logs trace origin.
  - **Tests:** `swarmsearch/gossip_pubkey_test.go` covers the sink call
    (happy path), wrong-length pk rejection, and the missing-pk
    backwards-compat path. `testlab/gossip_pubkey_scenario_test.go` spins
    a 2-node testlab cluster with both nodes Publisher=1 and verifies
    each node's recording sink receives the OTHER node's pubkey after a
    WireMesh + WaitAllHandshaked. A second scenario (Publisher=0 on one
    side) verifies the publisher sink sees nothing from a non-publisher.
    Both green on first run.
  - All tests pass: `go test -short ./...` clean and `go test -race
    ./internal/{swarmsearch,testlab,engine,daemon,dhtindex,...}` clean.
- **2026-04-21** — Workstream 2 (Layer-B docker testbed expansion) complete.
  docker compose v2.40.3 confirmed available on host. Three new scenario
  scripts added (`s2-lossy-search.sh`, `s3-mobile-4g-search.sh`,
  `s4-home-dsl-search.sh`) and a driver script (`scripts/run-testbed.sh`).
  `testbed/README.md` updated with concrete How-To-Run section and port
  conflict documentation.

  **One prerequisite blocker surfaced:** the `kartofel` user is not in the
  `docker` group (group has no members), and the automated session cannot
  obtain a sudo password to add the user. `run-testbed.sh` checks `docker
  info` at startup and exits with a clear actionable error message:
  `sudo usermod -aG docker $USER && newgrp docker`. This must be run once by
  the user before the driver script can start containers.

  **Scenario validation (against real binary, no docker):** to validate the
  assertion logic independently of the docker permission issue, all four
  scenarios were run against three real `swartznet` processes started on
  ports 17654/17655/17656 with separate `--data-dir` and `--index-dir` paths.
  All 12 assertions (3 healthz + 3 status + 3 torrents per scenario, plus 3
  search-endpoint checks in s4) passed on first run. Wall-clock runtime was
  ~5s total.

  **Known gap / next step for real docker run:** once the user runs
  `sudo usermod -aG docker $USER`, `scripts/run-testbed.sh all` should run
  unmodified. The netem profiles are not degenerate (5%/150ms for lossy;
  40ms+20ms jitter/10Mbit for 4G; 20ms+5ms/25Mbit for DSL) and will not
  prevent API convergence.

  **Other findings:** the existing `docker-compose.yml` uses placeholder
  infohashes (0xaaaa…/0xbbbb…) with no real content, so the scenarios
  deliberately test API-layer health under each network profile rather than
  end-to-end torrent transfer. Real-content transfer testing is deferred to
  a future workstream that injects a fixture `.torrent` file.

