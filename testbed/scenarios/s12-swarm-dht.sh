#!/usr/bin/env bash
# Scenario S12: Layer-D (DHT keyword index) end-to-end.
#
# STATUS (2026-04-23): work-in-progress — currently FAILS at the
# last step (indexers_responded=0 for every Layer-D search). This
# scenario is kept in-tree because:
#
#  1. It exercises the just-added --dht-bootstrap flag and the
#     companion fix to engine.startPublisher (caps.Publisher is
#     now flipped to 1 after the publisher starts, so outbound
#     PeerAnnounce frames actually carry the `pk` field and
#     gossip-discovered cross-registration fires).
#
#  2. With this scenario running, we observed that gossip now
#     works correctly — leech-1's /search --dht reports
#     indexers_asked=6 (all 6 nodes' pubkeys were cross-
#     registered via sn_search handshake), up from 0 pre-fix.
#
#  3. BEP-44 puts on the private DHT time out consistently
#     (anacrolix/dht/v2/exts/getput WRN "transaction timed
#     out"). Raw KRPC ping/pong between containers works,
#     ruling out basic UDP connectivity. Token validation,
#     SendLimiter contention, and routing-table sparseness
#     are all plausible causes; none have been definitively
#     isolated. Left for a future loop.
#
# The scenario is therefore NOT in `scripts/run-testbed.sh all`.
# Run it explicitly via `scripts/run-testbed.sh s12` to see the
# partial-pass output, which is informative.
#
# Precondition: docker compose running on docker-compose.dht.yml,
# a 6-node stack where every node has DHT enabled and bootstraps
# to seed-1 via a new --dht-bootstrap CLI flag. scripts/run-
# testbed.sh s12 handles lifecycle.
#
# This is the FIRST testbed scenario that exercises Layer-D at
# all. s1..s11 all run --no-dht, so they never execute the
# mainline-DHT path or the BEP-44 mutable-item publish/get
# round-trip that makes the SwartzNet keyword index work.
# Reaching this test required adding a --dht-bootstrap flag that
# plumbs through config.Config.DHTBootstrapAddrs into
# anacrolix's dht.ServerConfig.StartingNodes — without it, the
# default public mainline bootstrap hosts
# (router.bittorrent.com etc.) are unreachable from the docker
# bridge and no DHT ever forms.
#
# Shape:
#   1. Wait for all 6 nodes healthz.
#   2. Wait for every node's /status to report a non-zero
#      swarm.known_peers AND (on seeds) publisher.total_keywords
#      > 0. The publisher emits a BEP-44 put every 5s under
#      --regtest (production is 1h).
#   3. Wait for at least one seed's publisher to have
#      LastPublished > 0 for any keyword — proves the put
#      actually wrote to the DHT.
#   4. From leech-1, POST /search with {"q":"aethergram",
#      "dht":true, "dht_timeout_ms":5000}. Assert:
#        - dht.indexers_asked >= 1
#        - dht.indexers_responded >= 1
#        - dht.hits contains the fixture infohash
#
# Probes via docker exec (UFW-independent), same as s6..s11.
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

PROBER="sn-dht-seed-1"
SEED_NAMES=("seed-1" "seed-2")
LEECH_NAMES=("leech-1" "leech-2" "leech-3" "leech-4")
ALL_NAMES=("${SEED_NAMES[@]}" "${LEECH_NAMES[@]}")
LEECH1_CONT="sn-dht-leech-1"

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture-swarm/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_MARKER="aethergram"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 5 "http://$1:7654$2" 2>/dev/null || true
}

echo "=== S12: Layer-D (DHT keyword index) end-to-end (infohash=$FIXTURE_INFOHASH) ==="

# 1. Healthz on all 6 nodes.
for name in "${ALL_NAMES[@]}"; do
    ok=0
    for i in $(seq 1 90); do
        if api_get "$name" "/healthz" > /dev/null 2>&1; then
            ok=1; break
        fi
        sleep 1
    done
    [ "$ok" -eq 1 ] || fail "$name healthz unreachable after 90s"
done
pass "all 6 nodes healthy"

# 2. Wait for DHT to form on every node. swarm.known_peers counts
# BT peers that we've LTEP-handshaked with, which correlates with
# a live DHT in this topology (the x.pe= hints + DHT discovery
# together populate it). Also wait for seed publishers to have a
# non-zero total_keywords — that proves the Bleve index finished
# and the publisher's enqueue path ran.
status_fields() {
    # Emit "known_peers publisher_total_keywords" for the named node.
    api_get "$1" "/status" 2>/dev/null | python3 -c "
import sys, json
try: d = json.load(sys.stdin)
except Exception:
    print('0 0'); sys.exit()
print(d.get('swarm', {}).get('known_peers', 0),
      d.get('publisher', {}).get('total_keywords', 0))
" 2>/dev/null || echo "0 0"
}

echo "waiting for DHT formation + publisher warm-up (60s budget)..."
start=$(date +%s)
BUDGET=60
while true; do
    report=""
    seeds_have_keywords=0
    leeches_have_peers=0
    for name in "${SEED_NAMES[@]}"; do
        read -r kp tk <<< "$(status_fields "$name")"
        report+="${name}(kp=${kp},tk=${tk}) "
        if [ "${tk:-0}" -gt 0 ]; then
            seeds_have_keywords=$((seeds_have_keywords + 1))
        fi
    done
    for name in "${LEECH_NAMES[@]}"; do
        read -r kp tk <<< "$(status_fields "$name")"
        report+="${name}(kp=${kp}) "
        if [ "${kp:-0}" -gt 0 ]; then
            leeches_have_peers=$((leeches_have_peers + 1))
        fi
    done
    # Ready when all seeds have keywords queued AND at least one
    # leech has a visible peer. (Leeches need SOME peer to have
    # formed a DHT; requiring all 4 is unnecessary because
    # Layer-D lookup only uses the querier's own DHT.)
    if [ "$seeds_have_keywords" -eq 2 ] && [ "$leeches_have_peers" -ge 1 ]; then
        pass "DHT formed + publishers primed ($report) after $(( $(date +%s) - start ))s"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET" ]; then
        echo "--- snapshot at timeout: $report ---"
        echo "--- seed-1 logs (last 30) ---"
        docker logs --tail 30 sn-dht-seed-1 2>&1 | tail -30 || true
        echo "--- leech-1 logs (last 30) ---"
        docker logs --tail 30 sn-dht-leech-1 2>&1 | tail -30 || true
        fail "DHT/publisher not ready in ${BUDGET}s (seeds_with_kw=$seeds_have_keywords leeches_with_peers=$leeches_have_peers)"
    fi
    sleep 2
done

# 3. Wait for at least one seed's publisher to have LastPublished
# set on any keyword. The regtest publisher runs every 5s, so
# after the first cycle any Bleve-extracted keyword should have
# a LastPublished timestamp. This proves the put actually went
# out to the DHT — if the DHT table is empty, puts fail silently
# and LastPublished stays empty.
any_published() {
    api_get "$1" "/status" 2>/dev/null | python3 -c "
import sys, json
try: d = json.load(sys.stdin)
except Exception:
    print('no'); sys.exit()
kws = d.get('publisher', {}).get('keywords', []) or []
for k in kws:
    if k.get('last_published'):
        print('yes'); sys.exit()
print('no')
" 2>/dev/null || echo no
}

echo "waiting for at least one seed publisher to emit a BEP-44 put (60s budget)..."
start=$(date +%s)
BUDGET_PUB=60
while true; do
    pub_count=0
    for name in "${SEED_NAMES[@]}"; do
        r=$(any_published "$name")
        if [ "$r" = "yes" ]; then
            pub_count=$((pub_count + 1))
        fi
    done
    if [ "$pub_count" -ge 1 ]; then
        pass "at least one seed publisher has emitted a BEP-44 put (pub_count=$pub_count)"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET_PUB" ]; then
        echo "--- seed-1 status ---"
        api_get seed-1 "/status" | python3 -m json.tool || true
        fail "no seed publisher emitted a BEP-44 put within ${BUDGET_PUB}s"
    fi
    sleep 2
done

# 4. Layer-D search from leech-1. This is the load-bearing
# assertion: proves the full round-trip (leech DHT query →
# BEP-44 get from seed's pubkey → hit aggregation in
# dhtindex.Lookup) works end-to-end.
echo "firing Layer-D search from leech-1: q=$FIXTURE_MARKER dht=true"
resp=$(docker exec "$LEECH1_CONT" sh -c "curl -sf --max-time 12 -X POST 'http://localhost:7654/search' -H 'Content-Type: application/json' -d '{\"q\":\"$FIXTURE_MARKER\",\"limit\":20,\"dht\":true,\"dht_timeout_ms\":8000}'" 2>&1) \
    || fail "leech-1 /search unreachable or timed out (output: $resp)"

asked=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('dht',{}).get('indexers_asked',0))")
responded=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(d.get('dht',{}).get('indexers_responded',0))")
hits_count=$(echo "$resp" | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d.get('dht',{}).get('hits',[]) or []))")
hit_fixture=$(echo "$resp" | python3 -c "
import sys, json
d = json.load(sys.stdin)
for h in d.get('dht', {}).get('hits', []) or []:
    if h.get('infohash') == '$FIXTURE_INFOHASH':
        print('yes'); sys.exit()
print('no')
")

echo "dht stats: asked=$asked responded=$responded hits=$hits_count"
[ "${asked:-0}" -ge 1 ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "dht.indexers_asked=$asked < 1"; }
[ "${responded:-0}" -ge 1 ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "dht.indexers_responded=$responded < 1 — no indexer answered the BEP-44 get"; }
[ "${hits_count:-0}" -ge 1 ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "dht.hits empty — Layer-D round-trip degraded silently"; }
[ "$hit_fixture" = "yes" ] || { echo "--- response ---"; echo "$resp" | python3 -m json.tool || echo "$resp"; fail "no dht hit references fixture infohash $FIXTURE_INFOHASH"; }

pass "Layer-D search: asked=$asked responded=$responded hits=$hits_count (fixture infohash present)"

echo
echo "=== S12: all checks passed (DHT formed, BEP-44 published, Layer-D lookup round-tripped) ==="
