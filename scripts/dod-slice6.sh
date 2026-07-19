#!/bin/bash
# Slice 6 Definition-of-Done: the single sn_search services-mask producer and
# its live HTTP readout — exercised against the real dist/swartznet binary.
# Cumulative with dod-slice0..5.
set -u
ROOT=/home/kartofel/Claude/swartznet
BIN="$ROOT/dist/swartznet"
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
cap()  { curl -s "http://$1/capabilities" | python3 -c "import json,sys; d=json.load(sys.stdin); print(d[\"$2\"])"; }
agg()  { curl -s "http://$1/aggregate" | python3 -c 'import json,sys; print(json.load(sys.stdin)["services"])'; }
patch(){ curl -s -o /dev/null -w '%{http_code}' -X PATCH -H 'Origin: http://localhost' -d "$2" "http://$1/capabilities"; }

# ======================================================================
# Part 1 — default (index-on) node: live mask 0x0FD, publisher on
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg1"
ADDR=localhost:26610
IH=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n1" --index-dir "$WORK/n1-idx" "$IH" >"$WORK/d1.out" 2>"$WORK/d1.err" &
D1=$!; PIDS="$PIDS $D1"
wait_status "$ADDR" || fail "part1 daemon did not come up"

[ "$(cap "$ADDR" share_local)" = "2" ] && ok "GET /capabilities share_local reflects config default (2)" || fail "share_local wrong"
[ "$(cap "$ADDR" file_hits)" = "True" ] && [ "$(cap "$ADDR" content_hits)" = "True" ] && ok "file_hits/content_hits default true" || fail "file/content default"
[ "$(cap "$ADDR" publisher)" = "True" ] && ok "publisher bit set on an index-on node" || fail "publisher not set"
[ "$(cap "$ADDR" services)" = "00000000000000fd" ] && ok "GET /capabilities services is the live mask 0x0FD" || fail "cap services = $(cap "$ADDR" services)"
# Defect #1: /aggregate renders the LIVE mask, not the static 0x2ED.
S=$(agg "$ADDR")
{ [ "$S" = "00000000000000fd" ] && [ "$S" != "00000000000000ed" ]; } && ok "/aggregate services is live (0x0FD), not the static 0x2ED" || fail "/aggregate services = $S"

# Defect #2: PATCH toggling only file_hits must NOT clobber the Publisher bit.
[ "$(patch "$ADDR" '{"file_hits":false}')" = "200" ] && ok "PATCH file_hits accepted" || fail "PATCH file_hits status"
[ "$(cap "$ADDR" publisher)" = "True" ] && ok "PATCH file_hits left the daemon-owned Publisher bit untouched (§6 clobber fix)" || fail "Publisher clobbered!"
[ "$(cap "$ADDR" share_local)" = "2" ] && [ "$(cap "$ADDR" content_hits)" = "True" ] && ok "PATCH is preserve-unset (share_local/content_hits unchanged)" || fail "merge changed wrong fields"
[ "$(cap "$ADDR" services)" = "00000000000000f9" ] && ok "services dropped only the FileHits bit (0x0F9)" || fail "services after file toggle = $(cap "$ADDR" services)"

# Defect #3: a share-nothing downgrade actually changes the advertised bits.
patch "$ADDR" '{"share_local":0,"file_hits":false,"content_hits":false}' >/dev/null
[ "$(agg "$ADDR")" = "00000000000000f0" ] && ok "downgrade honored: /aggregate → 0x0F0 (legacy would still show static 0x2ED)" || fail "downgrade /aggregate = $(agg "$ADDR")"

# PATCH clamps share_local to 0..2.
patch "$ADDR" '{"share_local":9}' >/dev/null;  [ "$(cap "$ADDR" share_local)" = "2" ] && ok "PATCH clamps share_local 9→2" || fail "clamp hi"
patch "$ADDR" '{"share_local":-5}' >/dev/null; [ "$(cap "$ADDR" share_local)" = "0" ] && ok "PATCH clamps share_local -5→0" || fail "clamp lo"

# POST alias works.
[ "$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Origin: http://localhost' -d '{"share_local":1}' "http://$ADDR/capabilities")" = "200" ] \
  && [ "$(cap "$ADDR" share_local)" = "1" ] && ok "POST /capabilities alias applies" || fail "POST alias"

# Cross-origin PATCH is rejected by the CSRF guard.
[ "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH -H 'Origin: http://evil.example' -d '{"share_local":2}' "http://$ADDR/capabilities")" = "403" ] \
  && ok "cross-origin PATCH /capabilities rejected (403)" || fail "CSRF not enforced"
kill -INT $D1 2>/dev/null; wait $D1 2>/dev/null

# ======================================================================
# Part 2 — --no-index cascade zeroes the Publisher bit (mask 0x0ED)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg2"
ADDR=localhost:26611
"$BIN" add --no-dht --no-index --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n2" --index-dir "$WORK/n2-idx" \
  bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb >"$WORK/d2.out" 2>"$WORK/d2.err" &
D2=$!; PIDS="$PIDS $D2"
wait_status "$ADDR" || fail "part2 daemon did not come up"
[ "$(cap "$ADDR" publisher)" = "False" ] && ok "--no-index zeroes the Publisher bit (publisher=false)" || fail "no-index publisher = $(cap "$ADDR" publisher)"
[ "$(cap "$ADDR" services)" = "00000000000000ed" ] && ok "--no-index mask is 0x0ED (bit 4 clear)" || fail "no-index services = $(cap "$ADDR" services)"
[ "$(agg "$ADDR")" = "00000000000000ed" ] && ok "--no-index /aggregate matches (live)" || fail "no-index /aggregate = $(agg "$ADDR")"
kill -INT $D2 2>/dev/null; wait $D2 2>/dev/null

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
