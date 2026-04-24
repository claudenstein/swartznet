#!/usr/bin/env bash
# Scenario S8: 6-node swarm under a lossy link (5% loss + 150ms RTT).
#
# Precondition: docker compose running on docker-compose.swarm.yml
# with NETEM_PROFILE=/netem/lossy.sh. scripts/run-testbed.sh s8
# handles lifecycle.
#
# This is the lossy counterpart to s6: same 6-node mesh, same 4-MiB
# fixture, but every container applies the lossy netem profile on
# startup. Loss + RTT stretches the transfer window from <1s to
# tens of seconds, which is actually the point — the mesh has to
# re-request dropped pieces through multiple sources, and PEX has
# time to fill in. The test asserts convergence despite packet
# loss, not exact timing.
#
# Probes via docker exec (UFW-independent).
#
# Asserts:
#   1. All 6 nodes reach /healthz within 120s (lossy first-SYN
#      drops extend startup).
#   2. Both seeds at progress=1.0 within 60s (hash-verify is local,
#      but the daemon's API may be slower under tc netem).
#   3. All 4 leeches reach progress=1.0 within 300s — a generous
#      budget that comfortably accommodates a lossy 4-MiB transfer
#      across 4 leeches sharing 2 seeds + each other.
#   4. Leech on-disk SHA-256 matches fixture for at least one leech
#      (smoke check that retransmits didn't corrupt bytes — full
#      4-of-4 bytewise comparison is done in s6, where timing is
#      tight enough to guarantee the transfer path).
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

PROBER="sn-swarm-seed-1"
SEED_NAMES=("seed-1" "seed-2")
LEECH_NAMES=("leech-1" "leech-2" "leech-3" "leech-4")
ALL_NAMES=("${SEED_NAMES[@]}" "${LEECH_NAMES[@]}")

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture-swarm/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_DIR="$(dirname "$0")/../fixture-swarm/content/testbed-swarm-corpus"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 10 "http://$1:7654$2" 2>/dev/null
}

progress_for() {
    api_get "$1" "/torrents" 2>/dev/null | python3 -c "
import sys, json
try: d = json.load(sys.stdin)
except Exception: print('none'); sys.exit()
for t in d.get('torrents', []) or []:
    if t.get('infohash') == '$FIXTURE_INFOHASH':
        print(t.get('progress', 0)); sys.exit()
print('none')
"
}

echo "=== S8: 6-node swarm under lossy netem (infohash=$FIXTURE_INFOHASH) ==="
echo "    netem: 5% loss + 150ms RTT"

# 1. Healthz.
for name in "${ALL_NAMES[@]}"; do
    ok=0
    for i in $(seq 1 120); do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after 120s (lossy)"
done
pass "all 6 nodes healthy"

# 2. Seeds reach progress=1.
for name in "${SEED_NAMES[@]}"; do
    ok=0
    last=""
    for i in $(seq 1 60); do
        p=$(progress_for "$name")
        last="$p"
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            ok=1; break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name never reached progress=1 (last=$last)"
    pass "$name at progress=1.0"
done

# 3. All 4 leeches reach progress=1 within 300s.
BUDGET=300
echo "waiting up to ${BUDGET}s for all 4 leeches to complete under loss..."
start=$(date +%s)
while true; do
    done_count=0
    report=""
    for name in "${LEECH_NAMES[@]}"; do
        p=$(progress_for "$name")
        report+="${name}=${p} "
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "$done_count" -eq 4 ]; then
        pass "all 4 leeches at progress=1.0 (in $(( $(date +%s) - start ))s under lossy)"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET" ]; then
        echo "--- progress at timeout: $report ---"
        fail "only $done_count/4 leeches reached progress=1 within ${BUDGET}s (lossy)"
    fi
    sleep 3
done

# 4. Byte-exact match on at least leech-1 (smoke check).
fixture_sums=$(cd "$FIXTURE_DIR" && sha256sum *.txt | sort)
leech1_sums=$(docker exec "sn-swarm-leech-1" sh -c "cd /data/testbed-swarm-corpus && sha256sum *.txt | sort" 2>/dev/null) \
    || fail "could not sha256 leech-1 files"
if [ "$leech1_sums" != "$fixture_sums" ]; then
    echo "--- leech-1 hashes ---"
    echo "$leech1_sums"
    echo "--- fixture hashes ---"
    echo "$fixture_sums"
    fail "leech-1 bytes do not match fixture (under lossy)"
fi
pass "leech-1 bytes match fixture byte-for-byte (under lossy)"

echo
echo "=== S8: all checks passed (lossy profile) ==="
