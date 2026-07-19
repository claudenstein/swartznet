#!/bin/bash
# Slice 10 Definition-of-Done: the companion content-index (BEP-46 pointer
# pattern). The serialize/build/publish/subscribe logic + the security-critical
# subscriber pipeline + a REAL two-engine publish→fetch→import are proven by Go
# gates (run -race); the CLI surface, startup wiring, and follow-file
# persistence are driven against the real dist/swartznet binary. Cumulative
# with dod-slice0..9.
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
# Part 1 — companion logic + the security pipeline + real 2-engine e2e (Go, -race)
# ======================================================================
run_go serialize ./internal/companion/ \
  'TestEncodeDecodeRoundTrip|TestEncodeNilTorrentsIsEmptySlice|TestDecodeRefusesBadFormatAndVersion|TestCompanionFileName|TestBuildFromIndex' \
  "companion codec: gzip-JSON round-trip, nil→[], format/version refuse, filename rule, build-from-index"
run_go pubsub ./internal/companion/ \
  'TestPublisherEmptyIndexIsFailure|TestPublisherSuccessAdvancesLastRefresh|TestPublisherRefreshNowThrottle|TestSubscriberImportsAndStampsSignedBy|TestSubscriberRejectsPublisherMismatch|TestSubscriberDedupsUnchangedSnapshot|TestSubscriberWorkerFollowUnfollow' \
  "publisher (empty=failure, lastRefresh, throttle) + subscriber (§6 fixes: verify-publisher, dedup, SignedBy stamp)"
run_go engbounds ./internal/engine/ \
  'TestValidateCompanionInfoBounds|TestUnsafeCompanionName|TestPointerAccessorsGatedOnDHTAndSigner' \
  "engine fail-closed fetch bounds (1 file / ≤32 MiB / safe name) + pointer accessor gating"
run_go apiroutes ./internal/httpapi/ \
  'TestCompanionStatusRoute|TestCompanionStatusUnconfigured503|TestCompanionRefreshThrottle429|TestCompanionFollowValidatesPubKey' \
  "httpapi /companion routes: status, 503 unconfigured, 429 throttle, follow pubkey validation"
run_go e2e ./internal/wirecompat/scenarios/ 'TestCompanionPublishFollowImport' \
  "REAL 2-engine e2e: A publishes+seeds, B fetches over BitTorrent fail-closed, verifies author, imports stamped SignedBy=A, dedups"

# ======================================================================
# Part 2 — CLI discoverability (--help in sync + guards)
# ======================================================================
"$BIN" --help >"$WORK/help.txt" 2>&1
grep -qE '^  companion ' "$WORK/help.txt" && ok "--help lists the companion command" || fail "--help missing companion"
grep -q "Flags for 'companion'" "$WORK/help.txt" && ok "--help has a companion flags section" || fail "--help missing companion flags"
grep -q 'companion follow' "$WORK/help.txt" && ok "--help shows a runnable companion example" || fail "--help missing companion example"
grep -q -- '--regtest' "$WORK/help.txt" && ok "--help documents --regtest" || fail "--help missing --regtest"
"$BIN" companion >"$WORK/c0.txt" 2>&1; [ $? -eq 2 ] && grep -q 'status|follow|unfollow|refresh' "$WORK/c0.txt" \
  && ok "companion with no subcommand exits 2 with usage" || fail "companion no-subcommand guard"
"$BIN" companion status --api-addr localhost:1 >"$WORK/c1.txt" 2>&1; [ $? -eq 1 ] \
  && grep -q 'cannot reach' "$WORK/c1.txt" && ok "companion status with no daemon fails cleanly" || fail "companion no-daemon guard"

# ======================================================================
# Part 3 — companion publisher/subscriber wired in the running daemon;
#   the publisher builds a companion index from a real indexed torrent, and
#   follow/unfollow persists to the follow file (regtest = fast timings).
# ======================================================================
export SWARTZNET_UNSAFE=1
export XDG_DATA_HOME="$WORK/xdg1"
mkdir -p "$WORK/debian bookworm netinst amd64"; printf 'iso content\n' > "$WORK/debian bookworm netinst amd64/disk.img"
"$BIN" create -o "$WORK/t.torrent" "$WORK/debian bookworm netinst amd64" >/dev/null 2>&1
ADDR=localhost:27010
mkdir -p "$WORK/node"; cp -r "$WORK/debian bookworm netinst amd64" "$WORK/node/"
"$BIN" add --regtest --dht-bootstrap 127.0.0.1:1 --port 0 --api-addr "$ADDR" \
  --data-dir "$WORK/node" --index-dir "$WORK/node-idx" "$WORK/t.torrent" >"$WORK/d1.out" 2>"$WORK/d1.err" &
D1=$!; PIDS="$PIDS $D1"
wait_status "$ADDR" || fail "part3 daemon did not come up"

grep -q 'Companion publisher started' "$WORK/d1.out" && ok "node prints 'Companion publisher started'" || fail "no publisher startup line"
grep -q 'Companion subscriber started' "$WORK/d1.out" && ok "node prints 'Companion subscriber started'" || fail "no subscriber startup line"

# The companion publisher builds + writes the gz-JSON payload from the indexed
# torrent (regtest re-publishes every ~10s; the payload is written before the
# pointer put, which itself fails-closed on the isolated DHT).
COMPDIR="$XDG_DATA_HOME/swartznet/companion"
for i in $(seq 1 150); do ls "$COMPDIR"/swartznet-content-index-*.json.gz >/dev/null 2>&1 && break; sleep 0.1; done
ls "$COMPDIR"/swartznet-content-index-*.json.gz >/dev/null 2>&1 \
  && ok "companion publisher wrote a gz-JSON index from the indexed corpus" || fail "no companion payload written"

# GET /companion renders the publisher pubkey (from the loaded identity).
PK=$(curl -s "http://$ADDR/companion" | python3 -c 'import json,sys;print(json.load(sys.stdin)["publisher"].get("pubkey_hex",""))')
[ "${#PK}" = "64" ] && ok "GET /companion renders the 64-hex publisher pubkey" || fail "companion pubkey = '$PK'"

# Review fix #1: the companion SEED torrent must NOT pollute the local index —
# the real content torrent is searchable but "swartznet-content-index-*" is not.
DEB=$(curl -s -X POST "http://$ADDR/search" -H 'Content-Type: application/json' -d '{"q":"debian"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["local"]["total"])' 2>/dev/null || echo 0)
CMP=$(curl -s -X POST "http://$ADDR/search" -H 'Content-Type: application/json' -d '{"q":"swartznet content index"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["local"]["total"])' 2>/dev/null || echo 0)
[ "${DEB:-0}" -ge 1 ] && ok "real content torrent is indexed (search 'debian' → $DEB)" || fail "debian torrent not indexed"
[ "${CMP:-0}" = "0" ] && ok "companion seed torrent is NOT indexed (no self-referential pollution)" || fail "companion torrent leaked into the index ($CMP hits)"

# Follow a publisher via the CLI → persisted to the follow file + visible in status.
FOLLOWED=$(printf 'ab%.0s' $(seq 1 32))
"$BIN" companion follow "$FOLLOWED" --label seed-1 --api-addr "$ADDR" >"$WORK/f.txt" 2>&1 \
  && ok "companion follow succeeds" || fail "companion follow failed: $(cat "$WORK/f.txt")"
FF="$XDG_DATA_HOME/swartznet/companion-follows.json"
[ -f "$FF" ] && grep -q "$FOLLOWED" "$FF" && ok "follow persisted to companion-follows.json" || fail "follow file not persisted"
CNT=$(curl -s "http://$ADDR/companion" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["subscriber"]))')
[ "$CNT" = "1" ] && ok "companion status shows 1 followed publisher" || fail "subscriber count = $CNT"

# Unfollow → removed from the file + status.
"$BIN" companion unfollow "$FOLLOWED" --api-addr "$ADDR" >/dev/null 2>&1
CNT2=$(curl -s "http://$ADDR/companion" | python3 -c 'import json,sys;print(len(json.load(sys.stdin)["subscriber"]))')
[ "$CNT2" = "0" ] && ok "companion unfollow removes the follow" || fail "after unfollow count = $CNT2"
kill -INT $D1 2>/dev/null; wait $D1 2>/dev/null

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
