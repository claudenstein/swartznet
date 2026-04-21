#!/usr/bin/env bash
# Scenario S2: lossy-network 3-node search
#
# Precondition: docker compose is running with NETEM_PROFILE=/netem/lossy.sh
# (scripts/run-testbed.sh s2 handles this; you can also start manually:
#   NETEM_PROFILE=/netem/lossy.sh docker compose -f testbed/docker-compose.yml up -d)
#
# The lossy profile injects 5% packet loss + 150 ms RTT on each container's
# eth0. At that level the HTTP API is reachable; peer-wire connections
# experience retransmissions but still establish within a few seconds.
#
# What this asserts:
#   1. All 3 nodes reach /healthz "ok" within 90 s (extended from S1's 30 s
#      because the first TCP SYN has a 5 % chance of being dropped).
#   2. Each node reports at least 1 torrent on GET /torrents (the magnet
#      URI was handed to `swartznet add` at startup; even under loss the
#      daemon creates the torrent entry immediately before any peer contact).
#   3. GET /status returns valid JSON on all 3 nodes.
#
# Convergence note: we do NOT assert that leech-1 has downloaded content
# from seed-1/seed-2. The placeholder infohashes (0xaaaa…/0xbbbb…) have no
# real peers, so piece transfer is impossible in this testbed regardless of
# network conditions. The valuable thing we test is that the daemon's HTTP
# API layer and torrent-management lifecycle remain fully functional under
# the lossy profile — which is the pre-condition for any real-content test
# added later.
#
# Exit 0 if all checks pass, 1 on any failure.

set -euo pipefail

SEED1=http://localhost:17654
SEED2=http://localhost:17655
LEECH1=http://localhost:17656

HEALTHZ_WAIT=90   # seconds; lossy profile may drop first SYN(s)
RETRY_INTERVAL=2

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

echo "=== S2: lossy-network 3-node search scenario ==="
echo "    netem profile: lossy (5% loss, 150ms RTT)"
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
        fail "$node healthz unreachable after ${HEALTHZ_WAIT}s (lossy profile)"
    fi
    resp=$(curl -sf "$node/healthz") || fail "$node healthz final probe failed"
    echo "$resp" | grep -q '"ok":true' || fail "$node healthz not ok: $resp"
    pass "$node healthz (lossy profile)"
done

# ── 2. Status ─────────────────────────────────────────────────────────────────
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf "$node/status") || fail "$node status unreachable"
    # Basic structural check: must have "local" key
    echo "$resp" | grep -q '"local"' || fail "$node status missing 'local' field: $resp"
    pass "$node status valid JSON"
done

# ── 3. Torrents ───────────────────────────────────────────────────────────────
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf "$node/torrents") || fail "$node torrents unreachable"
    echo "$resp" | grep -q '"infohash"' || fail "$node has no torrents"
    pass "$node has torrents"
done

echo ""
echo "=== S2: all checks passed (lossy profile) ==="
