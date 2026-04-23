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
# Run the 3-node scenarios + the 6-node swarm back-to-back:
scripts/run-testbed.sh all

# 3-node scenarios (docker-compose.yml — 2 seeds + 1 leech):
scripts/run-testbed.sh s1    # healthy baseline, no netem
scripts/run-testbed.sh s2    # lossy: 5% loss + 150ms RTT
scripts/run-testbed.sh s3    # mobile-4G: 40ms+20ms jitter, 10Mbit
scripts/run-testbed.sh s4    # home-DSL: 20ms+5ms jitter, 25Mbit
scripts/run-testbed.sh s5    # piece-transfer: proves leech actually downloads

# 6-node swarm scenarios (docker-compose.swarm.yml — 2 seeds + 4 leeches):
scripts/run-testbed.sh s6    # 4-leech piece transfer at scale + PEX evidence
scripts/run-testbed.sh s7    # sn_search (Layer-S) fan-out hits fixture infohash
scripts/run-testbed.sh s8    # 6-node swarm under lossy netem (5% loss + 150ms)
scripts/run-testbed.sh s9    # pass-along: kill seeds, late-joiner leech-5 still completes
scripts/run-testbed.sh s10   # mid-transfer churn: kill seed-1 at 30% progress, expect convergence
scripts/run-testbed.sh s11   # vanilla BT interop: anacrolix CLI downloads from SwartzNet using BEP-9/10 only
scripts/run-testbed.sh swarm # alias: s6 + s7 against one compose lifecycle

# Emit a machine-readable JSON summary in addition to the scoreboard:
scripts/run-testbed.sh all --json=/tmp/run.json
```

The JSON output is one object with a top-level `scenarios` array
containing `{name, result, duration_s, netem_profile}` per run,
plus `started_at`, `finished_at`, `total_wall_clock_s`, and
`overall_exit`. Suitable for CI bots, perf-regression dashboards,
and trending analysis.

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

**3-node stack** (`docker-compose.yml` — scenarios s1-s5):

| Container  | Hostname | Static IP  | API port | Role |
|------------|----------|------------|----------|------|
| sn-seed-1  | seed-1   | 172.27.0.2 | 17654    | ROLE=seed, pre-populated with fixture content |
| sn-seed-2  | seed-2   | 172.27.0.3 | 17655    | ROLE=seed, same content as seed-1 |
| sn-leech-1 | leech-1  | 172.27.0.4 | 17656    | ROLE=leech, `x.pe=172.27.0.2:42069&x.pe=172.27.0.3:42069` |

**6-node swarm stack** (`docker-compose.swarm.yml` — scenarios s6, s7):

| Container        | Hostname | Static IP    | API port | Role |
|------------------|----------|--------------|----------|------|
| sn-swarm-seed-1  | seed-1   | 172.28.0.2   | 17664    | ROLE=seed, pre-populated with the 4-MiB swarm fixture |
| sn-swarm-seed-2  | seed-2   | 172.28.0.3   | 17665    | ROLE=seed, same content |
| sn-swarm-leech-1 | leech-1  | 172.28.0.4   | 17666    | ROLE=leech, `x.pe=` hints to every other node |
| sn-swarm-leech-2 | leech-2  | 172.28.0.5   | 17667    | ROLE=leech, `x.pe=` hints to every other node |
| sn-swarm-leech-3 | leech-3  | 172.28.0.6   | 17668    | ROLE=leech, `x.pe=` hints to every other node |
| sn-swarm-leech-4 | leech-4  | 172.28.0.7   | 17669    | ROLE=leech, `x.pe=` hints to every other node |
| sn-swarm-leech-5 | leech-5  | 172.28.0.8   | 17670    | ROLE=leech, **late-joiner** (compose profile `late-joiner` — only started by s9), `x.pe=` hints to every node so it can source from ex-leeches after seeds are gone |
| sn-swarm-vanilla-leech | vanilla-leech | 172.28.0.9 | — | **Vanilla BT client** (compose profile `vanilla` — only started by s11), stock `anacrolix/torrent` CLI built from upstream without any SwartzNet extensions; proves wire-compat by downloading from SwartzNet peers using BEP-3/9/10 only |

The swarm stack pins IPs in a dedicated `172.28.0.0/24` IPAM subnet
because anacrolix's `StringAddr` dialer feeds the raw `x.pe=` string
into its peer list without eager DNS resolution — dotted-quad IPs
keep the hint path unambiguous. The swarm fixture lives in
`testbed/fixture-swarm/` and is generated by
`scripts/gen-swarm-fixture.sh` (8 × 512-KiB deterministic text
chapters).

**UFW interaction.** `docker-compose.yml`'s published ports
(17654-17656 and 17664-17669) only work host-side if the kernel
FORWARD chain accepts bridge traffic. On stock Ubuntu
(`/etc/default/ufw` ships with `DEFAULT_FORWARD_POLICY="DROP"`)
host → container HTTP requests are RST'd even though the TCP
handshake completes; docker-proxy's forward leg is blocked. The
s6/s7 assertion scripts sidestep this by probing the API from
inside the prober container (`docker exec sn-swarm-seed-1 curl
http://seed-1:7654/…`), which talks directly over the internal
bridge. If you want the host-side ports to work for manual
inspection, run `sudo ufw default allow FORWARD` or add an
explicit accept rule for the docker bridge.

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

| Script | Netem profile | Compose stack | What it tests |
|---|---|---|---|
| `s1-healthy-search.sh` | none | 3-node | Health, status, torrents on all 3 nodes |
| `s2-lossy-search.sh` | lossy (5% loss) | 3-node | Same under 5% packet loss + 150ms RTT |
| `s3-mobile-4g-search.sh` | mobile-4G | 3-node | Same under 40ms+20ms jitter, 10Mbit |
| `s4-home-dsl-search.sh` | home-DSL | 3-node | Health/status/torrents + `/search` returns fixture hits on seeds |
| `s5-piece-transfer.sh` | none | 3-node | End-to-end piece transfer: leech reaches progress=1.0 and bytes match fixture SHA-256 |
| `s6-swarm-transfer.sh` | none | 6-node (swarm) | All 4 leeches hit progress=1.0 via mesh; peak active\_peers during transfer ≥ 2 (PEX evidence); bytes match fixture |
| `s7-swarm-search.sh` | none | 6-node (swarm) | Content indexed on every leech; `swarm:true` search from leech-1 returns a hit whose infohash is the fixture's |
| `s8-swarm-lossy.sh` | lossy | 6-node (swarm) | Same 6-node mesh under 5% loss + 150ms RTT: all 4 leeches converge within 300s, leech-1 bytes match fixture byte-for-byte |
| `s9-swarm-late-joiner.sh` | none | 6-node + late-joiner | After original leeches complete, stop both seeds, launch leech-5 via compose profile `late-joiner`; leech-5 must pull 4 MiB entirely from ex-leeches and bytes must match fixture |
| `s10-swarm-churn.sh` | lossy | 6-node (swarm) | Once `leech-1.progress ≥ 0.3`, stop `sn-swarm-seed-1` mid-flight; assert all 4 leeches still converge within 300s via seed-2 + mutual exchange; leech-1 bytes match fixture |
| `s11-vanilla-interop.sh` | none | 6-node + vanilla | Stock `anacrolix/torrent` CLI joins the swarm (no SwartzNet extensions, `--no-dht --no-pex --no-seed`) and downloads 4 MiB from SwartzNet peers via BEP-9 `x.pe=` hints + BEP-3/10 only; bytes match fixture |

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
