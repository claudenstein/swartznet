#!/usr/bin/env bash
# Scenario S1: healthy 3-node baseline
#
# Precondition: `docker compose -f testbed/docker-compose.yml up -d`
# is running. Probes the API via `docker exec sn-seed-1 curl
# http://<hostname>:7654/...` over the internal bridge network, so
# the test is independent of the host's UFW FORWARD policy (on
# stock Ubuntu with DEFAULT_FORWARD_POLICY=DROP, the host-published
# ports are RST'd by the kernel and s1 cannot rely on them).
#
# Exit 0 if all checks pass, 1 otherwise.

set -euo pipefail

PROBER="sn-seed-1"
NODES=("seed-1" "seed-2" "leech-1")

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null
}

echo "=== S1: Healthy 3-node search scenario ==="

for name in "${NODES[@]}"; do
    ok=0
    for i in $(seq 1 30); do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after 30s"
    resp=$(api_get "$name" "/healthz")
    echo "$resp" | grep -q '"ok":true' || fail "$name healthz not ok: $resp"
    pass "$name healthz"
done

for name in "${NODES[@]}"; do
    resp=$(api_get "$name" "/status") || fail "$name status unreachable"
    echo "$resp" | grep -q '"local"' || fail "$name status missing local: $resp"
done

for name in "${NODES[@]}"; do
    resp=$(api_get "$name" "/torrents") || fail "$name torrents unreachable"
    echo "$resp" | grep -q '"infohash"' || fail "$name has no torrents"
    pass "$name has torrents"
done

echo
echo "=== S1: all checks passed ==="
