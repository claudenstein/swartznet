#!/usr/bin/env bash
# Scenario S4: home-DSL network 3-node search
#
# Precondition: docker compose is running with NETEM_PROFILE=/netem/home-dsl.sh
# (scripts/run-testbed.sh s4 handles this; you can also start manually:
#   NETEM_PROFILE=/netem/home-dsl.sh docker compose -f testbed/docker-compose.yml up -d)
#
# The home-DSL profile adds 20 ms base delay + 5 ms jitter (uniform) and
# caps bandwidth at 25 Mbit/s. This is the mildest degraded profile —
# typical of a cable/DSL connection with a bit of jitter. API responses
# are fast; the only observable effect at this scale is slightly higher
# latency on the first connection setup.
#
# What this asserts:
#   1. All 3 nodes reach /healthz "ok" within 60 s.
#   2. GET /status returns valid JSON on all 3 nodes.
#   3. GET /torrents reports at least 1 torrent per node.
#   4. POST /search with a simple query returns a valid SearchResponse
#      JSON structure (even with 0 hits — the important thing is that the
#      search path is end-to-end reachable under the DSL profile).
#
# S4 is the only scenario that also exercises the /search endpoint, because
# the DSL profile is gentle enough that the 10-second HTTP timeout is
# unlikely to be hit. Lossy/4G scenarios deliberately skip /search to avoid
# flakiness from the search handler's ReadTimeout.
#
# Exit 0 if all checks pass, 1 on any failure.

set -euo pipefail

SEED1=http://localhost:17654
SEED2=http://localhost:17655
LEECH1=http://localhost:17656

HEALTHZ_WAIT=60   # DSL profile is mild; 60 s is generous
RETRY_INTERVAL=2

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

echo "=== S4: home-DSL network 3-node search scenario ==="
echo "    netem profile: home-DSL (20ms+5ms jitter, 25Mbit)"
echo "    healthz timeout: ${HEALTHZ_WAIT}s"
echo ""

# ── 1. Healthz ───────────────────────────────────────────────────────────────
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    deadline=$(( $(date +%s) + HEALTHZ_WAIT ))
    ok=0
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if curl -sf "$node/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep "$RETRY_INTERVAL"
    done
    if [ "$ok" -eq 0 ]; then
        fail "$node healthz unreachable after ${HEALTHZ_WAIT}s (home-DSL profile)"
    fi
    resp=$(curl -sf "$node/healthz") || fail "$node healthz final probe failed"
    echo "$resp" | grep -q '"ok":true' || fail "$node healthz not ok: $resp"
    pass "$node healthz (home-DSL profile)"
done

# ── 2. Status ─────────────────────────────────────────────────────────────────
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf "$node/status") || fail "$node status unreachable"
    echo "$resp" | grep -q '"local"' || fail "$node status missing 'local' field: $resp"
    pass "$node status valid JSON"
done

# ── 3. Torrents ───────────────────────────────────────────────────────────────
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf "$node/torrents") || fail "$node torrents unreachable"
    echo "$resp" | grep -q '"infohash"' || fail "$node has no torrents"
    pass "$node has torrents"
done

# ── 4. Search endpoint reachable ──────────────────────────────────────────────
# Sends a local-only query (no "swarm":true / "dht":true so no fan-out).
# Expects a valid SearchResponse with at minimum a "local" key.
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf -X POST "$node/search" \
        -H "Content-Type: application/json" \
        -d '{"q":"testbed","limit":5}') \
        || fail "$node search endpoint unreachable"
    echo "$resp" | grep -q '"local"' \
        || fail "$node search response missing 'local' key: $resp"
    pass "$node search endpoint reachable (0 hits expected, structure valid)"
done

echo ""
echo "=== S4: all checks passed (home-DSL profile) ==="
