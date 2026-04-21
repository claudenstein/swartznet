#!/usr/bin/env bash
# Scenario S3: mobile-4G network 3-node search
#
# Precondition: docker compose is running with NETEM_PROFILE=/netem/mobile-4g.sh
# (scripts/run-testbed.sh s3 handles this; you can also start manually:
#   NETEM_PROFILE=/netem/mobile-4g.sh docker compose -f testbed/docker-compose.yml up -d)
#
# The mobile-4G profile adds 40 ms base delay + 20 ms jitter (uniform) and
# caps bandwidth at 10 Mbit/s. The high jitter (up to 60 ms per hop) means
# connection setup takes 120–240 ms round-trip, which is perfectly realistic
# for a mobile client but pushes deadline-sensitive probes past the 30 s
# window used in S1.
#
# What this asserts:
#   1. All 3 nodes reach /healthz "ok" within 90 s.
#   2. GET /status returns valid JSON (with "local" field) on all 3 nodes.
#   3. GET /torrents reports at least 1 torrent per node.
#
# The 10 Mbit/s cap does not materially affect the API probes (HTTP responses
# are tiny), but it correctly represents the bandwidth budget available for
# any future real-content download test on this profile.
#
# Exit 0 if all checks pass, 1 on any failure.

set -euo pipefail

SEED1=http://localhost:17654
SEED2=http://localhost:17655
LEECH1=http://localhost:17656

HEALTHZ_WAIT=90   # seconds; jitter can push first response past 30 s
RETRY_INTERVAL=2

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

echo "=== S3: mobile-4G network 3-node search scenario ==="
echo "    netem profile: mobile-4G (40ms+20ms jitter, 10Mbit)"
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
        fail "$node healthz unreachable after ${HEALTHZ_WAIT}s (mobile-4G profile)"
    fi
    resp=$(curl -sf "$node/healthz") || fail "$node healthz final probe failed"
    echo "$resp" | grep -q '"ok":true' || fail "$node healthz not ok: $resp"
    pass "$node healthz (mobile-4G profile)"
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

echo ""
echo "=== S3: all checks passed (mobile-4G profile) ==="
