#!/usr/bin/env bash
# Scenario S11: vanilla BT client interop.
#
# Precondition: docker compose running on docker-compose.swarm.yml.
# scripts/run-testbed.sh s11 handles lifecycle including bringing
# up sn-swarm-vanilla-leech via the `vanilla` compose profile.
#
# This is the only scenario in the matrix that puts a *non*-
# SwartzNet client on the same bridge as our nodes. Everything
# else (s1-s10) mixes only swartznet binaries; any wire-compat
# regression in our LTEP layer, reserved-bit usage, or peer-wire
# framing would slip past those because the peers on both ends
# understand our dialect. Here the leech is aria2c, an independent
# BT implementation that has no idea about sn_search.
#
# The load-bearing assertion: aria2c downloads the 4-MiB fixture
# from the SwartzNet seeds + swartznet leeches *only* via
# BEP-3/9/10 traffic. If this fails, either we've started using a
# reserved bit that isn't BEP-10, we've broken extended-handshake
# handling, or our seeds are emitting non-mainline behavior —
# violating CLAUDE.md's "vanilla client must see nothing but
# BEP-3/5/9/10/44" rule.
#
# Shape:
#   1. Wait for all 4 SwartzNet leeches to reach progress=1.0
#      (via s6's logic) so the vanilla leech has multiple source
#      options (2 seeds + 4 ex-leeches).
#   2. Bring up sn-swarm-vanilla-leech via compose profile
#      `vanilla`. Its command is aria2c with --enable-dht=false,
#      --seed-time=0, and x.pe= hints to every SwartzNet IP.
#   3. Wait for the aria2c container to exit 0 within 180s.
#      aria2c's --seed-time=0 makes it stop immediately after
#      verifying the download; a non-zero exit or timeout means
#      download failed.
#   4. SHA-256 the downloaded /vanilla-data/testbed-swarm-corpus
#      against the on-host fixture. Mismatches indicate a
#      wire-level integrity bug (which the BT protocol's
#      per-piece SHA-1 normally catches — but if aria2c's verify
#      disagrees with our seed's hashing, that's a different
#      class of bug).
#
# Probes via docker exec (UFW-independent), same as s6..s10.
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

PROBER="sn-swarm-seed-1"
LEECH_NAMES=("leech-1" "leech-2" "leech-3" "leech-4")
VANILLA_CONT="sn-swarm-vanilla-leech"

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture-swarm/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_DIR="$(dirname "$0")/../fixture-swarm/content/testbed-swarm-corpus"
COMPOSE_FILE="$(dirname "$0")/../docker-compose.swarm.yml"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
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

echo "=== S11: vanilla aria2c interop (infohash=$FIXTURE_INFOHASH) ==="

# 1. Wait for the SwartzNet leeches to complete. Gives aria2c
#    a rich peer set to pull from.
echo "step 1/4: waiting for 4 SwartzNet leeches to reach progress=1.0"
start=$(date +%s)
BUDGET=120
while true; do
    done_count=0
    for name in "${LEECH_NAMES[@]}"; do
        p=$(progress_for "$name")
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "$done_count" -eq 4 ]; then
        pass "all 4 SwartzNet leeches at progress=1.0 (in $(( $(date +%s) - start ))s)"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET" ]; then
        fail "only $done_count/4 swartznet leeches completed within ${BUDGET}s"
    fi
    sleep 1
done

# 2. Launch the vanilla leech.
echo "step 2/4: launching vanilla-leech (aria2c) via compose profile"
docker compose -f "$COMPOSE_FILE" --profile vanilla up -d vanilla-leech \
    2>&1 | tail -5 || fail "compose up --profile vanilla failed"
pass "vanilla-leech container started"

# 3. aria2c runs until it verifies the download, then exits.
#    --bt-stop-timeout=180 caps the run at 180s of inactivity.
#    We poll the container state for "exited" and then check
#    the exit code.
echo "step 3/4: waiting for vanilla-leech to verify + exit (budget 240s)"
start=$(date +%s)
exit_code=""
while [ $(( $(date +%s) - start )) -lt 240 ]; do
    state=$(docker inspect --format='{{.State.Status}}' "$VANILLA_CONT" 2>/dev/null || echo missing)
    if [ "$state" = "exited" ]; then
        exit_code=$(docker inspect --format='{{.State.ExitCode}}' "$VANILLA_CONT")
        break
    fi
    if [ "$state" = "missing" ]; then
        fail "vanilla-leech container disappeared"
    fi
    sleep 2
done
if [ -z "$exit_code" ]; then
    echo "--- vanilla-leech logs (last 40) ---"
    docker logs --tail 40 "$VANILLA_CONT" 2>&1 || true
    fail "vanilla-leech did not exit within 240s (wire-compat regression?)"
fi
if [ "$exit_code" != "0" ]; then
    echo "--- vanilla-leech logs (last 40) ---"
    docker logs --tail 40 "$VANILLA_CONT" 2>&1 || true
    fail "vanilla-leech exited non-zero ($exit_code) — aria2c could not complete"
fi
pass "vanilla-leech verified+exited cleanly in $(( $(date +%s) - start ))s (exit_code=0)"

# 4. Byte match. The container has already exited (its process
# terminates as soon as the torrent CLI finishes), so docker
# exec is unreachable. docker cp works on stopped containers
# though — we copy the downloaded tree to a host scratch dir
# and sha256 from there.
echo "step 4/4: verifying vanilla-leech bytes against the fixture"
scratch=$(mktemp -d /tmp/s11-vanilla-XXXXXX)
trap "rm -rf '$scratch'" EXIT
docker cp "$VANILLA_CONT:/vanilla-data/testbed-swarm-corpus" "$scratch/" \
    2>/dev/null || fail "docker cp from vanilla-leech failed"
fixture_sums=$(cd "$FIXTURE_DIR" && sha256sum *.txt | sort)
vanilla_sums=$(cd "$scratch/testbed-swarm-corpus" && sha256sum *.txt | sort) \
    || fail "could not sha256 extracted vanilla-leech files"
if [ "$vanilla_sums" != "$fixture_sums" ]; then
    echo "--- vanilla hashes ---"
    echo "$vanilla_sums"
    echo "--- fixture hashes ---"
    echo "$fixture_sums"
    fail "vanilla bytes diverged from fixture (wire-level corruption?)"
fi
pass "vanilla-leech bytes match fixture byte-for-byte — SwartzNet seeds are mainline-compatible"

echo
echo "=== S11: all checks passed (aria2c downloaded 4 MiB from SwartzNet peers, BEP-3/9/10 only) ==="
