#!/usr/bin/env bash
# Scenario S5: end-to-end piece transfer across the testbed.
#
# Precondition: docker-compose up is running with the 3 containers
# from docker-compose.yml (seed-1, seed-2, leech-1). Seeds are
# pre-populated with testbed/fixture/content and leech-1 was
# started with a magnet URI that carries x.pe= peer hints to
# both seeds.
#
# Assertions:
#   1. Both seeds report the fixture as complete (progress=1.0)
#      within 30s of API coming up (they just hashed the pre-
#      populated files, no network transfer required).
#   2. leech-1 reaches progress=1.0 on the fixture infohash
#      within 90s of API coming up. This is the load-bearing
#      assertion that proves Layer-B is testing real transfer.
#   3. leech-1's on-disk files match the seeds' bytes exactly
#      (checked by SHA-256 over each fixture file).
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

SEED1=http://localhost:17654
SEED2=http://localhost:17655
LEECH1=http://localhost:17656

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_DIR="$(dirname "$0")/../fixture/content/testbed-fixture-book"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

echo "=== S5: piece-transfer scenario (infohash=$FIXTURE_INFOHASH) ==="

# Wait for all nodes healthy.
for node in "$SEED1" "$SEED2" "$LEECH1"; do
    for i in $(seq 1 60); do
        if curl -sf "$node/healthz" > /dev/null 2>&1; then
            break
        fi
        sleep 1
    done
    curl -sf "$node/healthz" > /dev/null 2>&1 || fail "$node healthz unreachable after 60s"
done
pass "all 3 nodes healthy"

# progress_for <node-url> <infohash>  →  prints progress (0..1), or "none" if
# the torrent isn't listed. Relies on python for JSON parse.
progress_for() {
    local node="$1"
    local ih="$2"
    curl -sf "$node/torrents" 2>/dev/null | python3 -c "
import sys, json
d = json.load(sys.stdin)
for t in d.get('torrents', []) or []:
    if t.get('infohash') == '$ih':
        print(t.get('progress', 0))
        sys.exit()
print('none')
"
}

# Check both seeds reach complete (they start with files already on disk).
for label in seed-1:$SEED1 seed-2:$SEED2; do
    name="${label%%:*}"
    url="${label#*:}"
    ok=0
    for i in $(seq 1 30); do
        p=$(progress_for "$url" "$FIXTURE_INFOHASH")
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            ok=1
            break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name never reached progress=1 (last=$p)"
    pass "$name at progress=1.0"
done

# Leech must reach complete within 90s.
ok=0
last_progress="(none)"
for i in $(seq 1 90); do
    p=$(progress_for "$LEECH1" "$FIXTURE_INFOHASH")
    last_progress="$p"
    if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
        ok=1
        break
    fi
    sleep 1
done
if [ "$ok" -ne 1 ]; then
    # Dump leech's torrent list on failure for post-mortem.
    echo "--- leech-1 /torrents dump ---"
    curl -s "$LEECH1/torrents" | python3 -m json.tool || true
    echo "--- sn-leech-1 logs (last 80 lines) ---"
    docker logs --tail 80 sn-leech-1 2>&1 || true
    fail "leech-1 never reached progress=1 (last=$last_progress)"
fi
pass "leech-1 reached progress=1.0 (via peer-hints, no DHT)"

# Byte-exact match: leech files vs. fixture.
leech_data=$(docker exec sn-leech-1 sh -c "cd /data/testbed-fixture-book && sha256sum *.txt | sort" 2>/dev/null) \
    || fail "could not compute leech hashes"
fixture_data=$(cd "$FIXTURE_DIR" && sha256sum *.txt | sort)
if [ "$leech_data" != "$fixture_data" ]; then
    echo "--- leech hashes ---"
    echo "$leech_data"
    echo "--- fixture hashes ---"
    echo "$fixture_data"
    fail "leech bytes do not match fixture bytes"
fi
pass "leech on-disk bytes match fixture byte-for-byte"

echo
echo "=== S5: all checks passed ==="
