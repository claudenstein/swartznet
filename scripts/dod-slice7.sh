#!/bin/bash
# Slice 7 Definition-of-Done: Layer S (sn_search LTEP peer-wire). The wire
# behavior (two peers, vanilla silence, reject codes, anti-spoof) is proven by
# Go gates run here; the HTTP swarm surface (§5.9 inline error, /status counts)
# is driven against the real dist/swartznet binary. Cumulative with dod0..6.
set -u
ROOT=/home/kartofel/Claude/swartznet
BIN="$ROOT/dist/swartznet"
GO=/usr/local/go/bin/go
WORK=$(mktemp -d)
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
PIDS=""
cleanup() {
  for p in $PIDS; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null; done
  rm -rf "$WORK"
}
trap cleanup EXIT INT TERM
wait_status() { for i in $(seq 1 100); do curl -s "http://$1/status" >/dev/null 2>&1 && return 0; sleep 0.1; done; return 1; }

# ======================================================================
# Part 1 — the wire gates (Go, run under -race)
# ======================================================================
cd "$ROOT"
run_go() { # <name> <pkg> <run-regexp>
  if $GO test -race "$2" -run "$3" -count=1 >"$WORK/$1.log" 2>&1; then ok "$4"; else fail "$4 — $(tail -3 "$WORK/$1.log")"; fi
}
run_go codec ./contracts/ltepwire/ 'TestEnvelopeGoldenVectors|TestZeroAndNegativeTimestampOmitted|TestHitNameTruncated|TestEndorsedCapAsymmetric' \
  "wire codec golden vectors + §6 fixes (zero-t omitted, name truncated, endorsed cap)"
run_go handler ./internal/swarmsearch/ 'TestScopeRejectCode2|TestShareLocalOneFailsClosed|TestBadShapeQueryCharged|TestResultFromUnaskedPeerCharged|TestNoAnnounceStillAnswered|TestVanillaPeerNeverQueried' \
  "handler: scope reject 2, ShareLocal fail-closed, §6a bad-query charge, asked-set anti-spoof, no-announce-still-answered, vanilla never queried"
run_go vanilla ./internal/wirecompat/scenarios/ 'TestVanillaPeerSeesNoSnSearch' \
  "mainline-compat: a vanilla peer receives ZERO sn_search frames (real loopback)"
run_go wirequery ./internal/wirecompat/scenarios/ 'TestSwarmQueryOverRealWire' \
  "a peer queries the engine over the real sn_search wire and gets indexed hits"

# Compile-time: no bare-address send API — the only sender takes a PeerToken.
if grep -rn "SendExtension(.*string" internal/swarmsearch/*.go internal/engine/swarmadapter.go >/dev/null 2>&1; then
  fail "a bare-address SendExtension API exists (must take a PeerToken)"
else
  ok "no bare-address send API (SendExtension takes a PeerToken — compile-time)"
fi

# ======================================================================
# Part 2 — HTTP swarm surface against the running binary (§5.9)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg1"
ADDR=localhost:26710
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n1" --index-dir "$WORK/n1-idx" \
  aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$WORK/d1.out" 2>"$WORK/d1.err" &
D1=$!; PIDS="$PIDS $D1"
wait_status "$ADDR" || fail "part2 daemon did not come up"

# /status swarm block present with the peer counts.
curl -s "http://$ADDR/status" | python3 -c 'import json,sys; s=json.load(sys.stdin)["swarm"]; assert s=={"known_peers":0,"capable_peers":0}, s' \
  && ok "/status reports the swarm peer counts" || fail "/status swarm block"

# POST /search {swarm:true} with no peers → 200 with an INLINE error (§5.9),
# never a 500/503.
CODE=$(curl -s -o "$WORK/s.json" -w '%{http_code}' -X POST -H 'Origin: http://localhost' -d '{"q":"ubuntu","swarm":true}' "http://$ADDR/search")
[ "$CODE" = "200" ] && ok "POST /search swarm:true is 200 even with no peers (§5.9, not 5xx)" || fail "search swarm status = $CODE"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); b=d["swarm"]; assert b["error"], b; assert b["asked"]==0 and b["hits"]==[], b; assert d["local"]["hits"]==[], d' "$WORK/s.json" \
  && ok "swarm failure is surfaced inline as swarm.error with empty hits" || fail "swarm block: $(cat "$WORK/s.json")"

# Asking without swarm omits the block entirely.
curl -s -X POST -H 'Origin: http://localhost' -d '{"q":"ubuntu"}' "http://$ADDR/search" \
  | python3 -c 'import json,sys; assert "swarm" not in json.load(sys.stdin)' \
  && ok "swarm block omitted when the request did not ask for it" || fail "swarm block leaked"

# A bounded timeout still answers 200.
TC=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Origin: http://localhost' -d '{"q":"ubuntu","swarm":true,"swarm_timeout_ms":300}' "http://$ADDR/search")
[ "$TC" = "200" ] && ok "swarm search with a bounded timeout answers 200" || fail "timeout search = $TC"
kill -INT $D1 2>/dev/null; wait $D1 2>/dev/null

# ======================================================================
# Part 3 — swarm works with NO local index (Layer S is index-independent)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg2"
ADDR=localhost:26711
"$BIN" add --no-dht --no-index --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n2" --index-dir "$WORK/n2-idx" \
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb >"$WORK/d2.out" 2>"$WORK/d2.err" &
D2=$!; PIDS="$PIDS $D2"
wait_status "$ADDR" || fail "part3 daemon did not come up"
C=$(curl -s -o "$WORK/s2.json" -w '%{http_code}' -X POST -H 'Origin: http://localhost' -d '{"q":"ubuntu","swarm":true}' "http://$ADDR/search")
{ [ "$C" = "200" ] && python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert "swarm" in d and d["local"]["hits"]==[]' "$WORK/s2.json"; } \
  && ok "--no-index node still runs Layer S swarm search (200, empty local)" || fail "no-index swarm ($C): $(cat "$WORK/s2.json")"
kill -INT $D2 2>/dev/null; wait $D2 2>/dev/null

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
