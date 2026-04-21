# SwartzNet Testbed (Layer B)

Multi-container test environment for scenarios that need real
network emulation, process isolation, and longer runtimes than
the in-process Layer-A harness (`internal/testlab`). Modeled on
Bitcoin Core's functional-test framework: real binaries, real
sockets, but a controlled environment.

## Prerequisites

- Docker Engine + docker compose v2 (tested with v2.40.3)
- Go 1.24+ (to build the binary — only needed if no pre-built binary exists)
- Linux host with kernel ≥4.x (tc/netem support for network emulation)
- **Your user must be in the `docker` group:**
  ```bash
  sudo usermod -aG docker $USER
  newgrp docker          # activate without logging out, or log out/in
  docker ps              # verify: should not say "permission denied"
  ```

> **Port conflicts:** the scenarios use fixed ports 17654, 17655, and 17656
> on localhost. Stop any other process using those ports before running.

## Quick start — driver script (recommended)

```bash
# Run all five scenarios sequentially (builds binary if needed):
scripts/run-testbed.sh all

# Run a single scenario:
scripts/run-testbed.sh s1    # healthy baseline, no netem
scripts/run-testbed.sh s2    # lossy: 5% loss + 150ms RTT
scripts/run-testbed.sh s3    # mobile-4G: 40ms+20ms jitter, 10Mbit
scripts/run-testbed.sh s4    # home-DSL: 20ms+5ms jitter, 25Mbit
scripts/run-testbed.sh s5    # piece-transfer: proves leech actually downloads
```

The driver script:
1. Checks `docker compose version` and `docker info` (fails with a clear message if absent or inaccessible).
2. Builds `dist/swartznet-testbed-linux-amd64` if absent or if any Go source is newer.
3. Starts `docker compose up --build -d` with the right `NETEM_PROFILE`, waits for all three containers to reach "running" state (up to 120 s).
4. Runs the scenario assertion script and captures its exit code.
5. Runs `docker compose down -v` via an EXIT trap — even on failure.
6. Writes per-run output to `testbed/results/<scenario>-<timestamp>.log`.
7. Prints a scoreboard at the end.

## Manual operation

```bash
# 1. Build the static binary:
scripts/build-release.sh testbed
# Or directly:
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -trimpath -ldflags "-s -w -X main.Version=testbed" \
  -o dist/swartznet-testbed-linux-amd64 ./cmd/swartznet

# 2. Spin up a scenario (example: lossy):
NETEM_PROFILE=/netem/lossy.sh \
  docker compose -f testbed/docker-compose.yml up --build -d

# 3. Run the scenario assertion (in another terminal):
testbed/scenarios/s2-lossy-search.sh

# 4. Tear down:
docker compose -f testbed/docker-compose.yml down -v
```

## Network emulation profiles

Each profile applies `tc qdisc add dev eth0 root netem ...` inside the
container at startup via `testbed/entrypoint.sh`. Containers need
`CAP_NET_ADMIN` (already declared in `docker-compose.yml`).

| Profile | File | Delay | Loss | Bandwidth |
|---|---|---|---|---|
| None (baseline) | — | — | — | unlimited |
| LAN | `netem/lan.sh` | 0.5 ms | 0 | 1 Gbit |
| Home DSL | `netem/home-dsl.sh` | 20ms ±5ms | 0 | 25 Mbit |
| Mobile 4G | `netem/mobile-4g.sh` | 40ms ±20ms | 0 | 10 Mbit |
| Lossy | `netem/lossy.sh` | 75ms | 5% | 20 Mbit |

## Node layout

| Container | Hostname | API port | Role |
|---|---|---|---|
| sn-seed-1 | seed-1 | localhost:17654 | ROLE=seed, pre-populated with fixture content, serves `fixture.torrent` |
| sn-seed-2 | seed-2 | localhost:17655 | ROLE=seed, same content as seed-1 (leech has two sources) |
| sn-leech-1 | leech-1 | localhost:17656 | ROLE=leech, starts empty, magnet URI has `x.pe=seed-1:42069&x.pe=seed-2:42069` |

All nodes run with `--regtest --no-dht` so publisher/companion timings
are accelerated and no real mainline DHT traffic is generated. Peer
discovery is bootstrapped via the `x.pe=` magnet peer-address hints
(BEP-9) in the leech's command line — no tracker, no DHT required.

## Fixture content

`testbed/fixture/content/testbed-fixture-book/` holds two small
deterministic `.txt` chapters carrying a distinctive marker
(`aethergram`). `testbed/fixture/fixture.torrent` is pre-generated
from that content (piece-size 16 KiB), and
`testbed/fixture/INFOHASH` records its infohash
(`c4405d27af8462e3d5e03c30c542f66e170fe4f8`). Seeds copy the
content into `/data` on container startup (via `ROLE=seed` in
`entrypoint.sh`) so anacrolix's piece-verify pass marks the
torrent complete immediately — no download from nowhere,
real-indexable text for `/search` assertions.

## Scenario scripts

| Script | Netem profile | What it tests |
|---|---|---|
| `s1-healthy-search.sh` | none | Health, status, torrents on all 3 nodes |
| `s2-lossy-search.sh` | lossy (5% loss) | Same under 5% packet loss + 150ms RTT |
| `s3-mobile-4g-search.sh` | mobile-4G | Same under 40ms+20ms jitter, 10Mbit |
| `s4-home-dsl-search.sh` | home-DSL | Health/status/torrents + `/search` returns fixture hits on seeds |
| `s5-piece-transfer.sh` | none | End-to-end piece transfer: leech reaches progress=1.0 and bytes match fixture SHA-256 |

Each script is standalone: it assumes `docker compose up` is already running
with the correct `NETEM_PROFILE` and just runs assertions against the three
well-known ports. Exit 0 = all pass, 1 = any failure.

## Reading testbed/results/

Each run writes to `testbed/results/<scenario>-<YYYYMMDD-HHMMSS>.log`:

```
=== run-testbed: scenario=s2 ts=20260420-231500 ===
    netem=/netem/lossy.sh

[docker compose up output]
[docker wait output]
=== S2: lossy-network 3-node search scenario ===
PASS: http://localhost:17654 healthz (lossy profile)
PASS: http://localhost:17655 healthz (lossy profile)
...
=== S2: all checks passed (lossy profile) ===
[docker compose down output]
```

If a scenario fails, the log ends with a `FAIL:` line and a non-zero exit.
Docker container logs are printed during teardown for post-mortem analysis:
```bash
docker compose -f testbed/docker-compose.yml logs
```

## Architecture

See the "Proposed layered testbed" section in
[docs/05-integration-design.md](../docs/05-integration-design.md) and
[docs/10-bitcoin-lessons.md](../docs/10-bitcoin-lessons.md) for the
rationale behind the layer split:

- **Layer A** (`internal/testlab`): in-process, fast, CI-friendly.
  Catches peer-wire bugs, concurrency races, and handler logic.
- **Layer B** (this directory): multi-container, real network conditions.
  Catches timeout tuning, NAT issues, process-level failures.
- **Layer C** (future): k8s + Chaos Mesh, 50-200 nodes, fault injection.
- **Layer D** (`cmd/dht-smoke`): live mainnet measurement. Not automatable for CI.
