#!/bin/bash
# Slice 9 Definition-of-Done: Layer D — the BEP-44 mutable keyword index behind
# the swappable RecordBackend seam. The frozen codec + real-DHT round-trip +
# fail-closed guard are proven by Go gates (run -race); the publish-on-GotInfo
# wiring, /publish readout, and privacy cascade are driven against the real
# dist/swartznet binary. Cumulative with dod-slice0..8.
set -u
ROOT=/home/kartofel/Claude/swartznet
BIN="$ROOT/dist/swartznet"
GO=/usr/local/go/bin/go
WORK=$(mktemp -d)
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
PIDS=""
cleanup() { for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null; done; rm -rf "$WORK"; }
trap cleanup EXIT INT TERM
wait_status() { for i in $(seq 1 100); do curl -s "http://$1/status" >/dev/null 2>&1 && return 0; sleep 0.1; done; return 1; }

cd "$ROOT"
run_go() { if $GO test -race "$2" -run "$3" -count=1 >"$WORK/$1.log" 2>&1; then ok "$4"; else fail "$4 — $(tail -4 "$WORK/$1.log")"; fi; }

# ======================================================================
# Part 1 — frozen codec + backend + real-DHT round-trip (Go gates, -race)
# ======================================================================
run_go dhtschema ./contracts/dhtschema/ \
  'TestEncodeValueGoldenVector|TestEncodeBep44RemarshalIdentity|TestDecodeValueRejectsOversizeBeforeUnmarshal|TestEncodeValueCap|TestSaltForKeywordVerbatim|TestDecodeLegacyKeywordValue' \
  "dhtschema frozen: golden bytes, bep44 remarshal-identity, ≤1000 pre-unmarshal cap, verbatim salt, legacy read"
run_go backend ./internal/dhtindex/ \
  'TestLookupPicksMostDistinctiveToken|TestPublisherTokenizesNameOnly|TestLegacyKeywordPublishThenThrottle|TestLegacyKeywordRefreshRepublishesAll|TestLegacyKeywordRetractScrubsInfohash|TestAddHitEvictsOldestUnderCap|TestLookupReputationGateFiltersLowScore|TestCheckPutStatsFailsClosed|TestDecodePointerValueRejectsOversize' \
  "backend: most-distinctive token, name-only publish, 55m throttle, refresh-all, retract, oldest-hit eviction, reputation gate, fail-closed guard, pointer cap"
run_go cluster ./internal/dhtindex/ \
  'TestLayerDDHTClusterRoundTrip|TestLayerDDHTClusterExpiresWithoutTTL|TestPutFailsClosedOnZeroNodes|TestVanillaBep44' \
  "real 2-node DHT: A publishes → B resolves via BEP-44; Exp negative guard; zero-node put fails closed; vanilla bep44 readable"
run_go mux ./internal/searchmux/ \
  'TestSearchmuxConcurrentLatency|TestDHTBranchGatedOnFlagAndWiring|TestDHTErrorSurfacedInline' \
  "searchmux: 3-layer concurrent (elapsed≈max not sum), DHT branch gated, Layer-D error inline"
run_go api ./internal/httpapi/ \
  'TestSearchLayerErrorAsymmetry|TestSearchDHTBlockRendered|TestPublishRoute' \
  "httpapi §5.9: Layer-L err→500, Layer-D err→200 inline; dht block; GET /publish"
run_go engwire ./internal/engine/ \
  'TestLayerDLookupAliveWithDHT|TestLayerDPublisherBuiltOnSigner|TestLayerDNoDHTPublishSuppressesWriteSide|TestLayerDNoIndexSuppressesWriteSide|TestLayerDDisabledLeavesBothNil' \
  "engine wiring: lookup alive leech-only, publisher on signer + self-pubkey, --no-dht-publish/--no-index cascade, DHT-off → absent"
run_go crawl ./cmd/swartznet/ \
  'TestCrawlProbeJSON|TestCrawlProbeText|TestCrawlProbeMissingAddr' \
  "crawl-probe: BEP-51 one-shot JSON + text against a stub responder, required-flag guard"

# ======================================================================
# Part 2 — CLI discoverability (--help in sync with commands)
# ======================================================================
"$BIN" --help >"$WORK/help.txt" 2>&1
grep -q 'crawl-probe' "$WORK/help.txt" && ok "--help lists crawl-probe" || fail "--help missing crawl-probe"
grep -q 'no-dht-publish' "$WORK/help.txt" && ok "--help documents --no-dht-publish" || fail "--help missing --no-dht-publish"
grep -q "search --dht --json new ubuntu" "$WORK/help.txt" && ok "--help shows a runnable search --dht example" || fail "--help missing search example"
"$BIN" crawl-probe >"$WORK/cp.txt" 2>&1; [ $? -eq 2 ] && grep -q 'addr is required' "$WORK/cp.txt" \
  && ok "crawl-probe with no --addr exits 2 with a hint" || fail "crawl-probe missing-addr guard"

# ======================================================================
# Part 3 — publish-on-GotInfo populates the manifest + /publish readout
#   (DHT enabled but isolated via a dead-end bootstrap so nothing leaks)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg1"
mkdir -p "$WORK/debian bookworm netinst amd64"; printf 'iso content\n' > "$WORK/debian bookworm netinst amd64/disk.img"
"$BIN" create -o "$WORK/t.torrent" "$WORK/debian bookworm netinst amd64" >/dev/null 2>&1
ADDR=localhost:26910
mkdir -p "$WORK/node"; cp -r "$WORK/debian bookworm netinst amd64" "$WORK/node/"
"$BIN" add --dht-bootstrap 127.0.0.1:1 --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/node" --index-dir "$WORK/node-idx" "$WORK/t.torrent" >"$WORK/d1.out" 2>"$WORK/d1.err" &
D1=$!; PIDS="$PIDS $D1"
wait_status "$ADDR" || fail "part3 daemon did not come up"

# Identity pubkey renders on /publish independent of any publisher collaborator.
PK=$(curl -s "http://$ADDR/publish" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("pubkey",""))')
[ "${#PK}" = "64" ] && ok "/publish renders the 64-hex identity pubkey" || fail "/publish pubkey = '$PK'"

# The publisher tokenizes the torrent NAME → 4 keywords land in the manifest.
for i in $(seq 1 100); do
  TK=$(curl -s "http://$ADDR/publish" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("total_keywords",0))' 2>/dev/null || echo 0)
  [ "${TK:-0}" -ge 4 ] && break; sleep 0.1
done
[ "${TK:-0}" -ge 4 ] && ok "/publish total_keywords grows as name-keywords are published ($TK ≥ 4)" || fail "total_keywords = ${TK:-0} (want ≥4)"

# Every published keyword is a NAME token — no content leaked to Layer D.
curl -s "http://$ADDR/publish" | python3 -c '
import json,sys
d=json.load(sys.stdin)
kws=set(k["keyword"] for k in d.get("keywords",[]))
name=set("debian bookworm netinst amd64".split())
sys.exit(0 if kws and kws.issubset(name) else 1)' \
  && ok "/publish keywords are all torrent-name tokens (no content leak)" || fail "published keyword not a name token"

# /status folds in the publisher totals.
[ "$(curl -s "http://$ADDR/status" | python3 -c 'import json,sys;print(json.load(sys.stdin)["publisher"]["total_keywords"])')" -ge 4 ] \
  && ok "/status publisher block reports the keyword total" || fail "/status publisher total"

# A --dht search returns a Layer-D block (self-pubkey in the lookup set).
curl -s -X POST "http://$ADDR/search" -H 'Content-Type: application/json' -H 'Origin: http://localhost:7654' \
  -d '{"q":"debian","dht":true,"dht_timeout_ms":800}' >"$WORK/s1.json" 2>/dev/null
python3 -c 'import json;d=json.load(open("'"$WORK"'/s1.json"));assert d.get("dht") is not None and d["dht"]["indexers_asked"]>=1' \
  && ok "/search dht=true returns a dht block with the self indexer" || fail "dht block missing/empty: $(cat "$WORK/s1.json")"
kill -INT $D1 2>/dev/null; wait $D1 2>/dev/null

# ======================================================================
# Part 4 — privacy cascade: --no-dht-publish suppresses publication but
#   keeps the read side alive (leech-only Layer D)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg2"
ADDR=localhost:26911
mkdir -p "$WORK/node2"; cp -r "$WORK/debian bookworm netinst amd64" "$WORK/node2/"
"$BIN" add --dht-bootstrap 127.0.0.1:1 --no-dht-publish --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/node2" --index-dir "$WORK/node2-idx" "$WORK/t.torrent" >"$WORK/d2.out" 2>"$WORK/d2.err" &
D2=$!; PIDS="$PIDS $D2"
wait_status "$ADDR" || fail "part4 daemon did not come up"
sleep 1  # give autoIndex a beat; with the write side suppressed nothing should publish
[ "$(curl -s "http://$ADDR/publish" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("total_keywords",0))')" = "0" ] \
  && ok "--no-dht-publish: nothing published (total_keywords=0)" || fail "--no-dht-publish still published"
# But the identity still renders and the read side still answers a --dht search.
PK2=$(curl -s "http://$ADDR/publish" | python3 -c 'import json,sys;print(json.load(sys.stdin).get("pubkey",""))')
[ "${#PK2}" = "64" ] && ok "--no-dht-publish: identity pubkey still renders" || fail "no-dht-publish pubkey"
curl -s -X POST "http://$ADDR/search" -H 'Content-Type: application/json' -H 'Origin: http://localhost:7654' \
  -d '{"q":"debian","dht":true,"dht_timeout_ms":800}' >"$WORK/s2.json" 2>/dev/null
python3 -c 'import json;d=json.load(open("'"$WORK"'/s2.json"));assert d.get("dht") is not None' \
  && ok "--no-dht-publish: Layer-D lookup stays alive (leech-only)" || fail "leech-only lookup missing"
kill -INT $D2 2>/dev/null; wait $D2 2>/dev/null

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
