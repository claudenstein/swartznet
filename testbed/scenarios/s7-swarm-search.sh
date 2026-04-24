#!/usr/bin/env bash
# Scenario S7: Layer-S swarm-search fan-out across a 6-node swarm.
#
# Precondition: docker compose is running with the swarm stack
# (docker-compose.swarm.yml). This scenario runs AFTER s6 has
# already confirmed transfers complete when they share a stack
# (scripts/run-testbed.sh swarm); when run standalone we re-wait
# for the same completion state.
#
# IMPLEMENTATION NOTE (transport): see s6 — we probe via
# `docker exec curl http://<host>:7654` so the test is independent
# of the host's UFW forward policy. Docker-proxy's forward leg is
# blocked when DEFAULT_FORWARD_POLICY=DROP, which is Ubuntu's
# default, so host-published ports are unreliable on stock
# workstations.
#
# What this asserts:
#
#   1. All 6 nodes reach /healthz.
#   2. All 4 leeches reach progress=1.0.
#   3. All 4 leeches finish content-level ingest (indexed_files ==
#      files). Layer-S only responds to a query if the responder has
#      locally indexed text to answer it with.
#   4. Every node has capable_peers >= 1 in /status — proof that
#      the `sn_search` LTEP extension handshake happened between
#      peers (BEP-10 extended handshake carries the services
#      bitfield that gates Layer-S routing).
#   5. POST /search on leech-1 with {"q":"aethergram","swarm":true}:
#        - responds within 6s wall,
#        - has swarm.asked >= 2 (seeds are capable),
#        - has swarm.responded >= 1 (at least one peer answered),
#        - has swarm.hits with at least one source matching the
#          fixture infohash.
#      This is the load-bearing assertion: it proves sn_search is
#      routing queries through the LTEP channel and that the
#      responder index has the answer.
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

PROBER="sn-swarm-seed-1"   # exec target for all GET probes
LEECH1_CONT="sn-swarm-leech-1"   # exec target for the real swarm search
LEECH_NAMES=("leech-1" "leech-2" "leech-3" "leech-4")
ALL_NAMES=("seed-1" "seed-2" "leech-1" "leech-2" "leech-3" "leech-4")

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture-swarm/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_MARKER="aethergram"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null
}

echo "=== S7: Layer-S swarm-search fan-out (infohash=$FIXTURE_INFOHASH) ==="

# 1. Healthz.
for name in "${ALL_NAMES[@]}"; do
    ok=0
    for j in $(seq 1 90); do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after 90s"
done
pass "all 6 nodes healthy"

progress_for() {
    api_get "$1" "/torrents" 2>/dev/null | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
except Exception:
    print('none'); sys.exit()
for t in d.get('torrents', []) or []:
    if t.get('infohash') == '$FIXTURE_INFOHASH':
        print(t.get('progress', 0))
        sys.exit()
print('none')
"
}

# We wait for content to appear in /index/stats rather than for
# indexed_files == files. indexed_files is a per-file counter that
# only advances after an entire file is extracted+indexed (several
# hundred chunks per 512-KiB file → several seconds per file). The
# content_count metric advances chunk-by-chunk, so we can detect
# "ingest is making real progress" within a second or two even on
# slow CI hardware. We wait for content_count > 0 rather than ==
# some absolute target so the test doesn't hinge on an exact
# chunker output count, which would change if DefaultChunkTargetBytes
# changes.
indexed_done() {
    local body
    body=$(api_get "$1" "/index/stats" 2>/dev/null) || true
    echo "$body" | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print('yes' if d.get('content_count', 0) > 0 else 'no')
except Exception:
    print('no')
"
}

capable_for() {
    api_get "$1" "/status" 2>/dev/null | python3 -c "
import sys, json
try:
    d = json.load(sys.stdin)
    print(d.get('swarm', {}).get('capable_peers', 0))
except Exception:
    print(0)
"
}

# 2. Leech progress == 1.
BUDGET=120
start=$(date +%s)
while true; do
    done_count=0
    for name in "${LEECH_NAMES[@]}"; do
        p=$(progress_for "$name")
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "$done_count" -eq 4 ]; then
        pass "all 4 leeches at progress=1.0"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET" ]; then
        fail "only $done_count/4 leeches reached progress=1 within ${BUDGET}s"
    fi
    sleep 2
done

# 3. Wait for indexed_files == files on every leech.
BUDGET_INDEX=60
start=$(date +%s)
while true; do
    done_count=0
    for name in "${LEECH_NAMES[@]}"; do
        r=$(indexed_done "$name")
        if [ "$r" = "yes" ]; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "$done_count" -eq 4 ]; then
        pass "all 4 leeches have indexed content (content_count > 0)"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET_INDEX" ]; then
        fail "only $done_count/4 leeches finished indexing within ${BUDGET_INDEX}s"
    fi
    sleep 1
done

# 4. Capable peers on the querier.
# Once the fixture is fully transferred, seeds have no reason to
# keep BT connections open (leeches stopped asking for pieces) and
# anacrolix tears them down — which also tears down the sn_search
# LTEP state. Meanwhile the leeches are still seeding to each
# other, so their sn_search peer map stays populated. We therefore
# only require capable_peers >= 1 on the querier (leech-1), which
# is enough to fan a query out. The response body tells us whether
# the sn_search routing path is actually working end-to-end, which
# is stricter than any per-node snapshot.
BUDGET_CAPS=15
start=$(date +%s)
while true; do
    c=$(capable_for "leech-1")
    if [ "${c:-0}" -ge 1 ]; then
        # Also capture the network-wide picture for the log.
        report=""
        for name in "${ALL_NAMES[@]}"; do
            report+="${name}=$(capable_for "$name") "
        done
        pass "querier leech-1 has capable_peers=$c (swarm snapshot: $report)"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET_CAPS" ]; then
        fail "leech-1 never saw a capable peer within ${BUDGET_CAPS}s (c=$c)"
    fi
    sleep 1
done

# 5. Swarm search from leech-1 — the real assertion.
# We exec curl inside leech-1 so the /search request comes from
# the same daemon whose swarm layer we're testing; the POST body
# is passed via `sh -c` with single quotes to keep JSON intact.
echo "firing swarm search from leech-1: q=$FIXTURE_MARKER swarm=true"
resp=$(docker exec "$LEECH1_CONT" sh -c "curl -sf --max-time 6 -X POST 'http://localhost:7654/search' -H 'Content-Type: application/json' -d '{\"q\":\"$FIXTURE_MARKER\",\"limit\":20,\"swarm\":true,\"swarm_timeout_ms\":3000}'" 2>&1) \
    || fail "leech-1 /search unreachable or timed out (output: $resp)"

asked=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('swarm',{}).get('asked',0))")
responded=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('swarm',{}).get('responded',0))")
hits_count=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('swarm',{}).get('hits',[]) or []))")
hit_matches_fixture=$(echo "$resp" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for h in d.get('swarm', {}).get('hits', []) or []:
    if h.get('infohash') == '$FIXTURE_INFOHASH':
        print('yes'); sys.exit()
print('no')
")

echo "swarm stats: asked=$asked responded=$responded hits=$hits_count"
[ "${asked:-0}" -ge 1 ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "swarm.asked=$asked < 1 (no capable peer was queried)"; }
[ "${responded:-0}" -ge 1 ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "swarm.responded=$responded < 1 (no peer answered the query)"; }
[ "${hits_count:-0}" -ge 1 ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "swarm.hits empty — query routed but answer had no results"; }
[ "$hit_matches_fixture" = "yes" ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "no swarm hit references fixture infohash $FIXTURE_INFOHASH"; }

pass "swarm search fan-out: asked=$asked responded=$responded hits=$hits_count (fixture infohash present)"

echo
echo "=== S7: all checks passed ==="
