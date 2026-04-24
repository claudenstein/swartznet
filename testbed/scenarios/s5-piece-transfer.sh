#!/usr/bin/env bash
# Scenario S5: end-to-end piece transfer on the 3-node stack.
#
# Precondition: docker compose running on docker-compose.yml (no
# netem). Seeds pre-populated with testbed/fixture/content; leech-1
# was started with a magnet URI carrying x.pe= hints to both seeds.
#
# Probes via docker exec (UFW-independent).
#
# Asserts:
#   1. Both seeds reach progress=1.0 within 30s (hash-verify only,
#      no transfer).
#   2. leech-1 reaches progress=1.0 within 90s — the load-bearing
#      assertion that Layer-B is actually testing a transfer.
#   3. leech-1's on-disk bytes match the fixture byte-for-byte.

set -euo pipefail

PROBER="sn-seed-1"
SEED_NAMES=("seed-1" "seed-2")
LEECH_NAME="leech-1"
LEECH_CONT="sn-leech-1"

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_DIR="$(dirname "$0")/../fixture/content/testbed-fixture-book"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null
}

echo "=== S5: piece-transfer scenario (infohash=$FIXTURE_INFOHASH) ==="

for name in "${SEED_NAMES[@]}" "$LEECH_NAME"; do
    ok=0
    for i in $(seq 1 60); do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after 60s"
done
pass "all 3 nodes healthy"

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

for name in "${SEED_NAMES[@]}"; do
    ok=0
    last=""
    for i in $(seq 1 30); do
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

ok=0
last="(none)"
for i in $(seq 1 90); do
    p=$(progress_for "$LEECH_NAME")
    last="$p"
    if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
        ok=1; break
    fi
    sleep 1
done
if [ "$ok" -ne 1 ]; then
    echo "--- leech-1 /torrents dump ---"
    api_get "$LEECH_NAME" "/torrents" | python3 -m json.tool || true
    echo "--- sn-leech-1 logs (last 80) ---"
    docker logs --tail 80 "$LEECH_CONT" 2>&1 || true
    fail "$LEECH_NAME never reached progress=1 (last=$last)"
fi
pass "$LEECH_NAME reached progress=1.0 (via peer-hints, no DHT)"

leech_data=$(docker exec "$LEECH_CONT" sh -c "cd /data/testbed-fixture-book && sha256sum *.txt | sort" 2>/dev/null) \
    || fail "could not compute leech hashes"
fixture_data=$(cd "$FIXTURE_DIR" && sha256sum *.txt | sort)
if [ "$leech_data" != "$fixture_data" ]; then
    echo "--- leech hashes ---"
    echo "$leech_data"
    echo "--- fixture hashes ---"
    echo "$fixture_data"
    fail "leech bytes do not match fixture"
fi
pass "leech on-disk bytes match fixture byte-for-byte"

echo
echo "=== S5: all checks passed ==="
