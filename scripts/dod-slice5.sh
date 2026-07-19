#!/bin/bash
# Slice 5 Definition-of-Done: trust, reputation, Bloom, and the ONE shared
# confirm/flag path — exercised against the real dist/swartznet binary.
# Cumulative with dod-slice0..4.
set -u
ROOT=/home/kartofel/Claude/swartznet
BIN="$ROOT/dist/swartznet"
GOLDEN="$ROOT/internal/reputation/testdata/known-good.bloom"
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

pop_bits() { # <api-addr>
  curl -s "http://$1/status" \
    | python3 -c 'import json,sys; b=json.load(sys.stdin).get("bloom") or {}; print(b.get("population_bits",0))' 2>/dev/null || echo 0
}
wait_status() { # <api-addr>
  for i in $(seq 1 100); do curl -s "http://$1/status" >/dev/null 2>&1 && return 0; sleep 0.1; done
  return 1
}

# ======================================================================
# Part 1 — offline trust allowlist (no daemon; trust.json under XDG root)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg1"
PUBA=$(printf 'a%.0s' {1..64})
PUBB=$(printf 'b%.0s' {1..64})

"$BIN" trust add "$PUBA" "alpha publisher" >"$WORK/t.out" 2>&1
grep -q "added: $PUBA" "$WORK/t.out" && ok "trust add reports the added pubkey" || fail "trust add: $(cat "$WORK/t.out")"
"$BIN" trust add "$PUBB" >/dev/null 2>&1

"$BIN" trust list >"$WORK/tl.out" 2>&1
grep -q "$PUBA" "$WORK/tl.out" && grep -q "alpha publisher" "$WORK/tl.out" \
  && ok "trust list shows the pubkey and label" || fail "trust list: $(cat "$WORK/tl.out")"

"$BIN" trust list --json >"$WORK/tj.out" 2>&1
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert len(d)==2, d' "$WORK/tj.out" 2>/dev/null \
  && ok "trust list --json emits both entries" || fail "trust list --json: $(cat "$WORK/tj.out")"

"$BIN" trust remove "$PUBA" >/dev/null 2>&1
"$BIN" trust list >"$WORK/tl2.out" 2>&1
grep -q "$PUBA" "$WORK/tl2.out" && fail "trust remove left the pubkey" || ok "trust remove drops the pubkey"

"$BIN" trust add "deadbeef" >"$WORK/tbad.out" 2>&1
RC=$?
{ [ $RC -ne 0 ] && grep -qi "64 hex" "$WORK/tbad.out"; } \
  && ok "trust add rejects a non-64-hex pubkey" || fail "bad pubkey accepted (rc=$RC): $(cat "$WORK/tbad.out")"

# ======================================================================
# Part 2 — golden Bloom vector loads into a running daemon unchanged
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg2"
mkdir -p "$XDG_DATA_HOME/swartznet"
cp "$GOLDEN" "$XDG_DATA_HOME/swartznet/known-good.bloom"
ADDR=localhost:25601
# An untrusted, non-completing torrent: it never touches the Bloom, so the
# golden's population must survive load untouched.
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n2" --index-dir "$WORK/n2-idx" \
  cccccccccccccccccccccccccccccccccccccccc >"$WORK/d2.out" 2>"$WORK/d2.err" &
D2=$!; PIDS="$PIDS $D2"
wait_status "$ADDR" || fail "part2 daemon did not come up"
BB=$(curl -s "http://$ADDR/status" | python3 -c 'import json,sys; b=json.load(sys.stdin)["bloom"]; print(b["population_bits"], b["hash_functions"])' 2>/dev/null)
[ "$BB" = "17 7" ] && ok "golden Bloom loads unchanged (population_bits=17, hash_functions=7)" || fail "golden bloom stats = '$BB' (want '17 7')"
kill -INT $D2 2>/dev/null; wait $D2 2>/dev/null

# ======================================================================
# Part 3 — trusted-publisher auto-confirm at metadata arrival
#          (content NOT placed → isolates the metadata path from completion)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg3"
mkdir -p "$WORK/lib"; printf 'trusted publisher content marker\n' > "$WORK/lib/a.txt"
OUT=$("$BIN" create --sign -o "$WORK/s.torrent" "$WORK/lib" 2>&1)
PUB=$(echo "$OUT" | sed -n 's/^Signing with identity //p')
IHSIGNED=$(echo "$OUT" | sed -n 's/^  InfoHash: //p')
[ -n "$PUB" ] && ok "create --sign prints the signing pubkey" || fail "no pubkey from create --sign: $OUT"
"$BIN" trust add "$PUB" "trusted" >/dev/null 2>&1

ADDR=localhost:25603
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n3" --index-dir "$WORK/n3-idx" "$WORK/s.torrent" \
  >"$WORK/d3.out" 2>"$WORK/d3.err" &
D3=$!; PIDS="$PIDS $D3"
wait_status "$ADDR" || fail "part3 daemon did not come up"
P=0; for i in $(seq 1 80); do P=$(pop_bits "$ADDR"); [ "${P:-0}" -ge 1 ] && break; sleep 0.1; done
[ "${P:-0}" -ge 1 ] && ok "trusted publisher auto-confirmed into the Bloom at metadata (pop=$P)" || fail "trusted publisher not confirmed (pop=$P)"
grep -q "trusted_publisher_confirmed" "$WORK/d3.err" && ok "trusted-publisher confirm logged" || fail "no trusted_publisher_confirmed log"
grep -q "auto_confirmed" "$WORK/d3.err" && fail "completion path fired (content was not placed)" || ok "confirm came from the metadata path, not completion"
TP=$(curl -s "http://$ADDR/torrents" | python3 -c 'import json,sys; print(json.load(sys.stdin)["torrents"][0].get("trusted_publisher"))' 2>/dev/null)
[ "$TP" = "True" ] && ok "/torrents marks the signed torrent trusted_publisher=true" || fail "trusted_publisher=$TP"
kill -INT $D3 2>/dev/null; wait $D3 2>/dev/null

# ======================================================================
# Part 4 — the shared confirm/flag path + durability + honest reporting
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg4"
ADDR=localhost:25604
IH=dddddddddddddddddddddddddddddddddddddddd
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n4" --index-dir "$WORK/n4-idx" "$IH" \
  >"$WORK/d4.out" 2>"$WORK/d4.err" &
D4=$!; PIDS="$PIDS $D4"
wait_status "$ADDR" || fail "part4 daemon did not come up"

BEFORE=$(pop_bits "$ADDR")
"$BIN" confirm --api-addr "$ADDR" "$IH" >"$WORK/cf.out" 2>&1
grep -q "confirmed: $IH" "$WORK/cf.out" && ok "confirm CLI reports success" || fail "confirm CLI: $(cat "$WORK/cf.out")"
AFTER=$(pop_bits "$ADDR")
[ "${AFTER:-0}" -gt "${BEFORE:-0}" ] && ok "confirm added the infohash to the Bloom ($BEFORE→$AFTER)" || fail "bloom did not grow ($BEFORE→$AFTER)"

# unattributed flag demotes nobody and says so honestly (the §6 fix)
"$BIN" flag --api-addr "$ADDR" "$IH" >"$WORK/fl.out" 2>&1
grep -q "flagged: $IH" "$WORK/fl.out" && grep -qi "no reputations changed" "$WORK/fl.out" \
  && ok "flag on an unattributed hit reports an honest no-op" || fail "flag CLI: $(cat "$WORK/fl.out")"
FJ=$(curl -s -X POST -H 'Origin: http://localhost' -d "{\"infohash\":\"$IH\"}" "http://$ADDR/flag" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["indexers_flagged"], d["attribution"])' 2>/dev/null)
[ "$FJ" = "0 none" ] && ok "/flag returns indexers_flagged=0 attribution=none (demotes nobody)" || fail "/flag json = '$FJ' (want '0 none')"

# bad-hex infohash rejected by the CLI (exit 2, no daemon call)
"$BIN" confirm --api-addr "$ADDR" "nothex" >"$WORK/bh.out" 2>&1
[ $? -eq 2 ] && grep -qi "40 hex" "$WORK/bh.out" && ok "confirm rejects a non-40-hex infohash" || fail "bad-hex not rejected: $(cat "$WORK/bh.out")"

# --help lists --api-addr for the new confirm/flag commands (discoverability)
"$BIN" --help | grep -q "'confirm' and 'flag'" && ok "--help documents --api-addr for confirm/flag" || fail "--help omits confirm/flag api-addr"

# /aggregate: a quiet fresh node answers 200 with a well-formed bootstrap
# block (distinguishable from a 503 "not configured" / starved node)
AGG=$(curl -s -o "$WORK/agg.json" -w '%{http_code}' "http://$ADDR/aggregate")
{ [ "$AGG" = "200" ] && python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["services"]=="0000000000000000"; assert set(d["bootstrap"])=={"anchors","admitted","pending"}; assert "known_indexers" in d' "$WORK/agg.json"; } \
  && ok "/aggregate reports a well-formed quiet-node snapshot" || fail "/aggregate ($AGG): $(cat "$WORK/agg.json")"

# durability: kill -9 mid-run, restart, confirmed state must persist
POP_LIVE=$(pop_bits "$ADDR")
kill -9 $D4; wait $D4 2>/dev/null
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n4" --index-dir "$WORK/n4-idx" "$IH" \
  >"$WORK/d4b.out" 2>"$WORK/d4b.err" &
D4B=$!; PIDS="$PIDS $D4B"
wait_status "$ADDR" || fail "part4 restart daemon did not come up"
POP_RESTART=$(pop_bits "$ADDR")
[ "${POP_RESTART:-0}" = "${POP_LIVE:-x}" ] && [ "${POP_RESTART:-0}" -ge 1 ] \
  && ok "confirmed Bloom survives kill -9 (pop $POP_LIVE persisted)" || fail "bloom lost on crash ($POP_LIVE→$POP_RESTART)"
kill -INT $D4B 2>/dev/null; wait $D4B 2>/dev/null

# ======================================================================
# Part 5 — corrupt trust.json fails CLOSED (review fix): flagging must
#          NOT silently demote when the exemption cannot be checked.
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg5"
mkdir -p "$XDG_DATA_HOME/swartznet"
printf '{not valid json' > "$XDG_DATA_HOME/swartznet/trust.json"
ADDR=localhost:25605
IH=$(printf 'e%.0s' {1..40})
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n5" --index-dir "$WORK/n5-idx" "$IH" \
  >"$WORK/d5.out" 2>"$WORK/d5.err" &
D5=$!; PIDS="$PIDS $D5"
wait_status "$ADDR" || fail "part5 daemon did not come up"
grep -q "trust_load_err" "$WORK/d5.err" && ok "corrupt trust.json logs a load error at startup" || fail "no trust_load_err logged"
"$BIN" flag --api-addr "$ADDR" "$IH" >"$WORK/f5.out" 2>&1
{ grep -q "flagged: $IH" "$WORK/f5.out" && grep -qi "trust list unavailable" "$WORK/f5.out"; } \
  && ok "flag fails CLOSED when trust is degraded (no silent demotion)" || fail "flag did not fail closed: $(cat "$WORK/f5.out")"
FA=$(curl -s -X POST -H 'Origin: http://localhost' -d "{\"infohash\":\"$IH\"}" "http://$ADDR/flag" \
  | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d["indexers_flagged"], d["attribution"])' 2>/dev/null)
[ "$FA" = "0 trust-unavailable" ] && ok "/flag returns attribution=trust-unavailable (fail-closed)" || fail "/flag degraded json = '$FA'"
kill -INT $D5 2>/dev/null; wait $D5 2>/dev/null

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
