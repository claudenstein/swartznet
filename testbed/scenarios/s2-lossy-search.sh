#!/usr/bin/env bash
# Scenario S2: lossy-network 3-node baseline (5% loss + 150ms RTT)
#
# Precondition: docker compose is running with
#   NETEM_PROFILE=/netem/lossy.sh
# (scripts/run-testbed.sh s2 handles this.)
#
# Probes API via `docker exec` so the test is independent of the
# host's UFW FORWARD policy. See s1 for the full rationale.
#
# Asserts: healthz ok, /status JSON valid, every node has ≥ 1
# torrent. Does not assert piece transfer — s5 covers that
# under a no-netem profile; the point of s2 is to confirm the
# HTTP and engine lifecycle survive a lossy link.
#
# Exit 0 if all checks pass, 1 on any failure.

set -euo pipefail

PROBER="sn-seed-1"
NODES=("seed-1" "seed-2" "leech-1")

HEALTHZ_WAIT=90   # seconds; lossy profile may drop first SYN(s)

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null
}

echo "=== S2: lossy-network 3-node search scenario ==="
echo "    netem profile: lossy (5% loss, 150ms RTT)"
echo "    healthz timeout: ${HEALTHZ_WAIT}s"

for name in "${NODES[@]}"; do
    deadline=$(( $(date +%s) + HEALTHZ_WAIT ))
    ok=0
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 2
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after ${HEALTHZ_WAIT}s (lossy)"
    resp=$(api_get "$name" "/healthz")
    echo "$resp" | grep -q '"ok":true' || fail "$name healthz not ok: $resp"
    pass "$name healthz (lossy)"
done

for name in "${NODES[@]}"; do
    resp=$(api_get "$name" "/status") || fail "$name status unreachable"
    echo "$resp" | grep -q '"local"' || fail "$name status missing local: $resp"
    pass "$name status valid JSON"
done

for name in "${NODES[@]}"; do
    resp=$(api_get "$name" "/torrents") || fail "$name torrents unreachable"
    echo "$resp" | grep -q '"infohash"' || fail "$name has no torrents"
    pass "$name has torrents"
done

echo
echo "=== S2: all checks passed (lossy profile) ==="
