#!/usr/bin/env bash
# Scenario S4: home-DSL 3-node baseline (20ms ±5ms jitter, 25 Mbit)
#   — also the only S1-S4 scenario that exercises /search end-to-end.
#
# Precondition: docker compose running with
#   NETEM_PROFILE=/netem/home-dsl.sh
# Probes via docker exec (UFW-independent).
#
# Asserts:
#   1. healthz on all 3 nodes,
#   2. /status returns valid JSON,
#   3. /torrents lists at least one torrent,
#   4. /search on both seeds returns hits for the fixture marker
#      "aethergram" (seeds pre-populated + auto-indexed on startup),
#   5. /search on the leech is at least structurally reachable (we
#      don't assert hits here — s5 is the end-to-end transfer proof).

set -euo pipefail

PROBER="sn-seed-1"
SEEDS=("seed-1" "seed-2")
LEECH="leech-1"
NODES=("${SEEDS[@]}" "$LEECH")

HEALTHZ_WAIT=60

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null
}

api_post_search() {
    docker exec "$PROBER" curl -sf --max-time 5 \
        -X POST -H 'Content-Type: application/json' \
        -d "{\"q\":\"aethergram\",\"limit\":5}" \
        "http://$1:7654/search" 2>/dev/null
}

echo "=== S4: home-DSL 3-node search scenario ==="
echo "    netem: home-DSL (20ms+5ms jitter, 25Mbit)"

for name in "${NODES[@]}"; do
    deadline=$(( $(date +%s) + HEALTHZ_WAIT ))
    ok=0
    while [ "$(date +%s)" -lt "$deadline" ]; do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 2
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after ${HEALTHZ_WAIT}s (home-DSL)"
    resp=$(api_get "$name" "/healthz")
    echo "$resp" | grep -q '"ok":true' || fail "$name healthz not ok: $resp"
    pass "$name healthz"
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

# Give the seeds a moment to finish content ingest (fixture is 3
# KiB; extraction finishes in well under a second once the pipeline
# starts, but we allow a small budget for startup tasks to settle
# under the home-DSL profile).
for i in $(seq 1 30); do
    all_ready=1
    for name in "${SEEDS[@]}"; do
        stats=$(api_get "$name" "/index/stats" 2>/dev/null || echo "{}")
        cc=$(echo "$stats" | python3 -c "import sys,json;print(json.load(sys.stdin).get('content_count',0))")
        if [ "${cc:-0}" -lt 1 ]; then
            all_ready=0
        fi
    done
    [ "$all_ready" -eq 1 ] && break
    sleep 1
done

for name in "${SEEDS[@]}"; do
    resp=$(api_post_search "$name") || fail "$name /search unreachable"
    echo "$resp" | grep -q '"local"' || fail "$name search response missing 'local': $resp"
    hits=$(echo "$resp" | python3 -c "import sys,json;d=json.load(sys.stdin);print(len(d.get('local',{}).get('hits',[]) or []))")
    [ "${hits:-0}" -gt 0 ] || fail "$name /search for 'aethergram' returned 0 hits"
    pass "$name search returned $hits hits for 'aethergram'"
done

# Leech: structural check only.
resp=$(api_post_search "$LEECH") || fail "$LEECH /search unreachable"
echo "$resp" | grep -q '"local"' || fail "$LEECH search missing 'local': $resp"
pass "$LEECH search endpoint reachable (structural check only)"

echo
echo "=== S4: all checks passed (home-DSL profile) ==="
