#!/usr/bin/env bash
# Scenario S9: pass-along + late-joiner resilience.
#
# Precondition: docker compose running on docker-compose.swarm.yml
# (the normal 6-node stack). scripts/run-testbed.sh s9 handles
# the full lifecycle including bringing leech-5 up under the
# `late-joiner` compose profile.
#
# This is the first scenario that proves SwartzNet leeches
# actually function as peers after they complete — i.e., that
# pass-along works. The shape is:
#
#   1. Wait for all 4 original leeches to reach progress=1.0
#      (via s6's logic against the same fixture).
#   2. Remove BOTH original seeds (`docker stop sn-swarm-seed-1
#      sn-swarm-seed-2`). The swarm now has four ex-leeches and
#      nobody else with the content.
#   3. Bring up leech-5 via the `late-joiner` profile. It has
#      never seen the content; its `x.pe=` hints include the
#      dead seed IPs (which fail) plus every ex-leech IP (which
#      must answer).
#   4. Assert leech-5 reaches progress=1.0 within 60 s. If this
#      fails, either the ex-leeches aren't seeding, PEX isn't
#      propagating past the dead hints, or the magnet URI's
#      multi-address resolution is broken.
#   5. Assert leech-5's on-disk bytes match the fixture SHA-256
#      byte-for-byte — catches silent corruption in a 3-hop
#      swarm (seed → leech-N → leech-5 is a real second-hop
#      path once the original seeds are gone).
#
# Probes via docker exec (UFW-independent), same as s6/s7/s8.
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

PROBER="sn-swarm-leech-1"           # prober must stay alive after seeds die
ORIGINAL_LEECH_NAMES=("leech-1" "leech-2" "leech-3" "leech-4")
SEED_CONTAINERS=("sn-swarm-seed-1" "sn-swarm-seed-2")
LATE_JOINER_CONT="sn-swarm-leech-5"
LATE_JOINER_NAME="leech-5"

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture-swarm/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_DIR="$(dirname "$0")/../fixture-swarm/content/testbed-swarm-corpus"
COMPOSE_FILE="$(dirname "$0")/../docker-compose.swarm.yml"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    # Swallow failures — the caller only needs the body, and a
    # `set -o pipefail` caller parsing the (possibly empty) body
    # with python3 would otherwise abort on the first probe before
    # the prober container's daemon is even listening.
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null || true
}

progress_for() {
    local body
    body=$(api_get "$1" "/torrents")
    echo "$body" | python3 -c "
import sys, json
try: d = json.load(sys.stdin)
except Exception: print('none'); sys.exit()
for t in d.get('torrents', []) or []:
    if t.get('infohash') == '$FIXTURE_INFOHASH':
        print(t.get('progress', 0)); sys.exit()
print('none')
" 2>/dev/null || echo none
}

# Wait for the prober container itself to be ready to handle exec
# + answer its own /healthz. The driver only waits for container
# state=running, which fires before the swartznet daemon inside
# has bound its HTTP port.
wait_prober_ready() {
    for i in $(seq 1 60); do
        if docker exec "$PROBER" curl -sf --max-time 2 \
            "http://localhost:7654/healthz" > /dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

wait_prober_ready || fail "prober $PROBER never became ready"

echo "=== S9: pass-along + late-joiner (infohash=$FIXTURE_INFOHASH) ==="

# 1. Wait for all 4 leeches to complete. Budget matches s6.
echo "step 1/5: waiting for 4 original leeches to reach progress=1.0"
start=$(date +%s)
BUDGET=120
while true; do
    done_count=0
    for name in "${ORIGINAL_LEECH_NAMES[@]}"; do
        p=$(progress_for "$name")
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "$done_count" -eq 4 ]; then
        pass "all 4 original leeches at progress=1.0 (in $(( $(date +%s) - start ))s)"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET" ]; then
        fail "only $done_count/4 original leeches completed within ${BUDGET}s"
    fi
    sleep 1
done

# 2. Kill both original seeds.
echo "step 2/5: removing original seeds (pass-along from this point)"
for c in "${SEED_CONTAINERS[@]}"; do
    docker stop "$c" > /dev/null || fail "could not stop $c"
done
pass "stopped ${SEED_CONTAINERS[*]}"

# 3. Bring up leech-5 via the late-joiner profile.
echo "step 3/5: launching leech-5 via docker compose profile late-joiner"
docker compose -f "$COMPOSE_FILE" --profile late-joiner up -d leech-5 \
    2>&1 | tail -10 || fail "compose up --profile late-joiner failed"
pass "leech-5 container started"

# Wait for leech-5 healthz.
ok=0
for i in $(seq 1 60); do
    if api_get "$LATE_JOINER_NAME" "/healthz" > /dev/null 2>&1; then
        ok=1; break
    fi
    sleep 1
done
[ "$ok" -eq 1 ] || fail "leech-5 healthz never came up"
pass "leech-5 healthz up"

# 4. leech-5 must reach progress=1 within 60s.
echo "step 4/5: waiting for leech-5 to complete via ex-leech pass-along"
BUDGET_LATE=60
start=$(date +%s)
last="(none)"
ok=0
while true; do
    p=$(progress_for "$LATE_JOINER_NAME")
    last="$p"
    if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
        ok=1; break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET_LATE" ]; then
        break
    fi
    sleep 1
done
if [ "$ok" -ne 1 ]; then
    echo "--- leech-5 /torrents ---"
    api_get "$LATE_JOINER_NAME" "/torrents" | python3 -m json.tool || true
    echo "--- leech-5 logs (last 40) ---"
    docker logs --tail 40 "$LATE_JOINER_CONT" 2>&1 || true
    fail "leech-5 never reached progress=1 within ${BUDGET_LATE}s (last=$last) — pass-along likely broken"
fi
pass "leech-5 reached progress=1.0 in $(( $(date +%s) - start ))s via ex-leech pass-along"

# 5. Byte-for-byte match.
echo "step 5/5: verifying leech-5 on-disk bytes match the fixture"
fixture_sums=$(cd "$FIXTURE_DIR" && sha256sum *.txt | sort)
leech5_sums=$(docker exec "$LATE_JOINER_CONT" sh -c "cd /data/testbed-swarm-corpus && sha256sum *.txt | sort" 2>/dev/null) \
    || fail "could not sha256 leech-5 files"
if [ "$leech5_sums" != "$fixture_sums" ]; then
    echo "--- leech-5 hashes ---"
    echo "$leech5_sums"
    echo "--- fixture hashes ---"
    echo "$fixture_sums"
    fail "leech-5 bytes do not match fixture — pass-along corrupted data"
fi
pass "leech-5 bytes match fixture byte-for-byte"

echo
echo "=== S9: all checks passed (leech-5 received 4 MiB via ex-leech pass-along, no seeds alive) ==="
