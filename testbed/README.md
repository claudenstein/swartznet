# SwartzNet Testbed (Layer B)

Multi-container test environment for scenarios that need real
network emulation, process isolation, and longer runtimes than
the in-process Layer-A harness (`internal/testlab`). Modeled on
Bitcoin Core's functional-test framework: real binaries, real
sockets, but a controlled environment.

## Prerequisites

- Docker + docker-compose v2
- Go 1.24+ (to build the binary)
- Linux host with kernel ≥4.x (tc/netem support)

## Quick start

```bash
# 1. Build the static binary for the testbed.
scripts/build-release.sh testbed

# 2. Spin up the 3-node cluster.
docker-compose -f testbed/docker-compose.yml up --build

# 3. In another terminal, run the scenario script.
testbed/scenarios/s1-healthy-search.sh
```

## Network emulation profiles

Pass `NETEM_PROFILE` to apply tc-netem rules on each container:

```bash
# Home DSL: 25 Mbit down, 40ms RTT
NETEM_PROFILE=/netem/home-dsl.sh docker-compose up --build

# Mobile 4G: 10 Mbit, 80ms RTT with jitter
NETEM_PROFILE=/netem/mobile-4g.sh docker-compose up --build

# Lossy: 5% packet loss, 150ms RTT
NETEM_PROFILE=/netem/lossy.sh docker-compose up --build

# LAN: 1 Gbit, 1ms RTT (the happy-path baseline)
NETEM_PROFILE=/netem/lan.sh docker-compose up --build
```

Containers need `CAP_NET_ADMIN` for tc (already declared in
docker-compose.yml).

## Node layout

| Container | Hostname | API port | Role |
|---|---|---|---|
| sn-seed-1 | seed-1 | localhost:17654 | Seeds torrent `0xaa...` |
| sn-seed-2 | seed-2 | localhost:17655 | Seeds torrent `0xbb...` |
| sn-leech-1 | leech-1 | localhost:17656 | Starts empty, discovers content via Layer S |

All nodes run with `--regtest --no-dht` so publisher/companion
timings are accelerated and no real mainline DHT is contacted.

## Scenario scripts

| Script | What it tests |
|---|---|
| `s1-healthy-search.sh` | Basic health + status + torrents on all 3 nodes |

## Architecture

See the "Proposed layered testbed" section in the
[testbed architecture proposal](../docs/05-integration-design.md)
and the [Bitcoin Core lessons](../docs/10-bitcoin-lessons.md) for
the rationale behind the layer split:

- **Layer A** (`internal/testlab`): in-process, fast, CI-friendly.
  Catches peer-wire bugs, concurrency races, and handler logic.
- **Layer B** (this directory): multi-container, real network
  conditions. Catches timeout tuning, NAT issues, process-level
  failures.
- **Layer C** (future): k8s + Chaos Mesh, 50-200 nodes, fault
  injection.
- **Layer D** (`cmd/dht-smoke`): live mainnet measurement. Not
  automatable for CI.
