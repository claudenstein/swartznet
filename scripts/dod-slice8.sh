#!/bin/bash
# Slice 8 Definition-of-Done: RIBLT set-reconciliation + the signed-record
# substrate. The frozen math + reconciliation convergence are proven by Go
# gates (run -race); the record-minting + /aggregate readout are driven against
# the real dist/swartznet binary. Cumulative with dod-slice0..7.
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
run_go() { if $GO test -race "$2" -run "$3" -count=1 >"$WORK/$1.log" 2>&1; then ok "$4"; else fail "$4 — $(tail -3 "$WORK/$1.log")"; fi; }

# ======================================================================
# Part 1 — frozen contracts + reconciliation (Go gates, -race)
# ======================================================================
run_go riblt ./contracts/riblt/ 'TestKeyFNVGolden|TestContributesSplitMix64Pin|TestEncodeStreamGolden|TestDecodeSymmetricDiff|TestConvergeManyElements' \
  "RIBLT frozen math: FNV key, contributes cycle, coded-symbol golden, decode/converge"
run_go record ./contracts/record/ 'TestElementIDGolden|TestElementIDExcludesPowSig|TestSignVerifyRoundTrip|TestPoWMineVerify' \
  "record substrate: ElementID golden, pow/sig-excluded dedup, sign/verify, PoW"
run_go synccodec ./contracts/ltepwire/ 'TestSyncGoldenVectors|TestSyncDecodeStrictness|TestSyncRecordShapeEnforced' \
  "sync wire codec golden vectors + strictness (element_size 32, kw≤64)"
run_go converge ./internal/swarmsearch/ 'TestSyncReconcileBidirectional|TestSyncReconcileLargeMultiBatch' \
  "two peers reconcile a 250-record symmetric difference to the union, multi-batch"
run_go budgets ./internal/swarmsearch/ 'TestBudgetNegotiatesDownward|TestApplySymbolsReordersAndBuffers|TestSyncFrameWithoutCapabilityCharged|TestStartSyncErrors' \
  "budget downward-negotiation, index-desync hard abort, capability gate, StartSync reachable"
run_go mint ./internal/wirecompat/ 'TestEngineMintsAggregateRecords|TestEngineNoSignerNoRecords' \
  "engine mints one signed record per torrent name-keyword on GotInfo (no signer = none)"

# ======================================================================
# Part 2 — HTTP readout + minting against the running binary
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg1"
mkdir -p "$WORK/debian bookworm netinst amd64"; printf 'iso content\n' > "$WORK/debian bookworm netinst amd64/disk.img"
"$BIN" create -o "$WORK/t.torrent" "$WORK/debian bookworm netinst amd64" >/dev/null 2>&1
ADDR=localhost:26810
mkdir -p "$WORK/node"; cp -r "$WORK/debian bookworm netinst amd64" "$WORK/node/"
"$BIN" add --no-dht --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/node" --index-dir "$WORK/node-idx" "$WORK/t.torrent" >"$WORK/d1.out" 2>"$WORK/d1.err" &
D1=$!; PIDS="$PIDS $D1"
wait_status "$ADDR" || fail "part2 daemon did not come up"

# bit 9 (reconciliation) is advertised now.
[ "$(curl -s "http://$ADDR/status" | python3 -c 'import json,sys;print((json.load(sys.stdin).get("swarm") or {}).get("known_peers"))')" = "0" ] \
  && ok "/status swarm block present" || fail "/status swarm block"
SVC=$(curl -s "http://$ADDR/aggregate" | python3 -c 'import json,sys;print(json.load(sys.stdin)["services"])')
python3 -c "import sys; v=int('$SVC',16); sys.exit(0 if (v>>9)&1 else 1)" \
  && ok "/aggregate services advertises BitSetReconciliation (bit 9)" || fail "bit 9 not set: $SVC"
[ "$(curl -s "http://$ADDR/aggregate" | python3 -c 'import json,sys;print(json.load(sys.stdin)["reconciliation"])')" = "True" ] \
  && ok "/aggregate reports reconciliation=true" || fail "reconciliation flag"

# Records minted from the torrent name-keywords (debian/bookworm/netinst/amd64).
for i in $(seq 1 100); do
  CS=$(curl -s "http://$ADDR/aggregate" | python3 -c 'import json,sys;print(json.load(sys.stdin)["cache_size"])' 2>/dev/null || echo 0)
  [ "${CS:-0}" -ge 4 ] && break; sleep 0.1
done
[ "${CS:-0}" -ge 4 ] && ok "/aggregate cache_size grows as records are minted ($CS ≥ 4 keywords)" || fail "cache_size = ${CS:-0} (want ≥4)"
kill -INT $D1 2>/dev/null; wait $D1 2>/dev/null

# ======================================================================
# Part 3 — --no-index node still advertises + mints (Layer-D independent)
# ======================================================================
export XDG_DATA_HOME="$WORK/xdg2"
ADDR=localhost:26811
"$BIN" add --no-dht --no-index --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/n2" --index-dir "$WORK/n2-idx" \
  cccccccccccccccccccccccccccccccccccccccc >"$WORK/d2.out" 2>"$WORK/d2.err" &
D2=$!; PIDS="$PIDS $D2"
wait_status "$ADDR" || fail "part3 daemon did not come up"
# --no-index → Publisher bit off (0x2ED), but reconciliation bit still on.
SVC2=$(curl -s "http://$ADDR/aggregate" | python3 -c 'import json,sys;print(json.load(sys.stdin)["services"])')
python3 -c "import sys; v=int('$SVC2',16); sys.exit(0 if ((v>>9)&1 and not (v>>4)&1) else 1)" \
  && ok "--no-index: reconciliation bit set, Publisher bit clear (0x2ED)" || fail "no-index services = $SVC2"
kill -INT $D2 2>/dev/null; wait $D2 2>/dev/null

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
