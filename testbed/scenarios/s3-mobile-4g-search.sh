#!/usr/bin/env bash
# Scenario S3: mobile-4G 3-node baseline (40ms ±20ms jitter, 10 Mbit)
#
# Precondition: docker compose running with
#   NETEM_PROFILE=/netem/mobile-4g.sh
# Probes via docker exec (UFW-independent). Asserts healthz/status/
# torrents reachable under the mobile-4G profile.

set -euo pipefail

PROBER="sn-seed-1"
NODES=("seed-1" "seed-2" "leech-1")

HEALTHZ_WAIT=90

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null
}

echo "=== S3: mobile-4G 3-node search scenario ==="
echo "    netem: mobile-4G (40ms+20ms jitter, 10Mbit)"

for name in "${NODES[@]}"; do
    deadline=$(( $(date +%s) + HEALTHZ_WAIT ))
    ok=0
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 2
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after ${HEALTHZ_WAIT}s (mobile-4G)"
    resp=$(api_get "$name" "/healthz")
    echo "$resp" | grep -q '"ok":true' || fail "$name healthz not ok: $resp"
    pass "$name healthz (mobile-4G)"
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
echo "=== S3: all checks passed (mobile-4G profile) ==="
