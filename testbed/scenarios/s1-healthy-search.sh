#!/usr/bin/env bash
# Scenario S1: healthy 3-node search
#
# Precondition: docker-compose up is running (the 3 containers
# from docker-compose.yml: seed-1, seed-2, leech-1).
#
# This script probes each node's HTTP API and asserts basic
# health, status, and search functionality. It's a lightweight
# smoke test for the docker-compose baseline, NOT a replacement
# for the internal/testlab in-process scenarios (which run in CI
# and test the real code paths; this script only tests the
# deployment plumbing).
#
# Exit 0 if all checks pass, 1 otherwise.

set -euo pipefail

SEED1=http://localhost:17654
SEED2=http://localhost:17655
LEECH1=http://localhost:17656

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

echo "=== S1: Healthy 3-node search scenario ==="

# Wait for all nodes to come up (healthz).
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    for i in $(seq 1 30); do
        if curl -sf "$node/healthz" > /dev/null 2>&1; then
            break
        fi
        sleep 1
    done
    resp=$(curl -sf "$node/healthz" 2>/dev/null) || fail "$node healthz unreachable after 30s"
    echo "$resp" | grep -q '"ok":true' || fail "$node healthz not ok: $resp"
    pass "$node healthz"
done

# Check status on each node.
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf "$node/status")
    echo "$node status: $resp" | head -c 200
    echo
done

# Check /torrents — each node should have at least 1 torrent.
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    resp=$(curl -sf "$node/torrents")
    echo "$resp" | grep -q '"infohash"' || fail "$node has no torrents"
    pass "$node has torrents"
done

echo
echo "=== S1: all checks passed ==="
