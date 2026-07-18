#!/bin/bash
# Slice 1 Definition-of-Done: persistent identity, driven against the real
# dist/swartznet binary. Cumulative with scripts/dod-slice0.sh.
set -u
BIN=/home/kartofel/Claude/swartznet/dist/swartznet
WORK=$(mktemp -d)
export XDG_DATA_HOME="$WORK/xdg"
KEY="$XDG_DATA_HOME/swartznet/identity.key"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }

# serve_bg <name> [extra args...] — starts an idling add daemon (dummy infohash,
# DHT off: metadata never arrives, the node just serves its API).
serve_bg() {
  local name="$1"; shift
  "$BIN" add --no-dht --port 0 --api-addr localhost:0 --data-dir "$WORK/$name/data" --index-dir "$WORK/$name/index" "$@" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
    >"$WORK/$name.out" 2>"$WORK/$name.err" &
  SPID=$!
  ADDR=""
  for i in $(seq 1 100); do
    ADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/$name.out" | head -1)
    [ -n "$ADDR" ] && return 0
    sleep 0.05
  done
  return 1
}

pubkey_of() { curl -s "http://$1/status" | python3 -c 'import json,sys; print(json.load(sys.stdin)["publisher"].get("pubkey",""))'; }

# --- First run: auto-create at the default XDG path ------------------------
serve_bg run1 || fail "serve did not start"
[ -f "$KEY" ] && ok "identity.key created at default path" || fail "identity.key created at default path"
MODE=$(stat -c %a "$KEY"); [ "$MODE" = "600" ] && ok "mode exactly 0600" || fail "mode = $MODE, want 600"
SIZE=$(wc -c < "$KEY"); [ "$SIZE" = "64" ] && ok "64 raw bytes" || fail "size = $SIZE"
PK1=$(pubkey_of "$ADDR")
echo "$PK1" | grep -qE '^[0-9a-f]{64}$' && ok "/status pubkey is 64 lowercase hex" || fail "/status pubkey: $PK1"
TAIL=$(python3 -c 'import sys; print(open(sys.argv[1],"rb").read()[32:].hex())' "$KEY")
[ "$PK1" = "$TAIL" ] && ok "pubkey equals key file bytes [32:64]" || fail "pubkey != file tail"
"$BIN" status --api-addr "$ADDR" | grep -q "pubkey:         $PK1" && ok "thin-client status shows pubkey" || fail "thin-client status shows pubkey"
grep -q "daemon.identity_loaded" "$WORK/run1.err" && ok "identity_loaded log line" || fail "identity_loaded log line"
kill -INT $SPID; wait $SPID

# --- Restart: same pubkey ---------------------------------------------------
serve_bg run2 || fail "restart failed"
PK2=$(pubkey_of "$ADDR")
[ "$PK2" = "$PK1" ] && ok "pubkey stable across restart" || fail "pubkey changed: $PK1 vs $PK2"
kill -INT $SPID; wait $SPID

# --- chmod 0644 → rejected, file untouched, daemon still serves -------------
HASH1=$(sha256sum "$KEY" | cut -d' ' -f1)
chmod 644 "$KEY"
serve_bg run3 || fail "degraded serve did not start"
PK3=$(pubkey_of "$ADDR")
[ -z "$PK3" ] && ok "0644 key rejected: no pubkey in /status" || fail "0644 key still served pubkey $PK3"
grep -q "insecure permissions 0644, want 0600" "$WORK/run3.err" && ok "0644 rejection message" || fail "0644 rejection message"
kill -INT $SPID; wait $SPID
HASH2=$(sha256sum "$KEY" | cut -d' ' -f1)
[ "$HASH1" = "$HASH2" ] && ok "rejected key file untouched" || fail "rejected key file was modified"

# --- chmod 0400 → rejected too (exact-0600 gate) ----------------------------
chmod 400 "$KEY"
serve_bg run4 || fail "serve with 0400 key did not start"
PK4=$(pubkey_of "$ADDR")
[ -z "$PK4" ] && ok "0400 key rejected" || fail "0400 key accepted"
grep -q "insecure permissions 0400, want 0600" "$WORK/run4.err" && ok "0400 rejection message" || fail "0400 rejection message"
kill -INT $SPID; wait $SPID

# --- chmod back 0600 → ORIGINAL pubkey (proves no regeneration) -------------
chmod 600 "$KEY"
serve_bg run5 || fail "recovery serve failed"
PK5=$(pubkey_of "$ADDR")
[ "$PK5" = "$PK1" ] && ok "original identity recovered after chmod 0600" || fail "identity changed after rejections"
kill -INT $SPID; wait $SPID

# --- delete → NEW pubkey at default path ------------------------------------
rm "$KEY"
serve_bg run6 || fail "post-delete serve failed"
PK6=$(pubkey_of "$ADDR")
[ -n "$PK6" ] && [ "$PK6" != "$PK1" ] && ok "deleted key regenerated with NEW pubkey" || fail "regeneration: old=$PK1 new=$PK6"
kill -INT $SPID; wait $SPID

# --- --identity nonexistent → fatal, nothing minted -------------------------
# timeout guards the exact regression this checks: if the fatal-exit contract
# breaks, serve would block forever instead of recording a FAIL.
timeout 15 "$BIN" add --no-dht --port 0 --api-addr localhost:0 --data-dir "$WORK/li/data" --index-dir "$WORK/li/index" \
  --identity "$WORK/nope.key" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$WORK/li.out" 2>"$WORK/li.err"
RC=$?
[ "$RC" = "1" ] && ok "--identity nonexistent exits 1" || fail "--identity nonexistent exit = $RC"
grep -q "load-only" "$WORK/li.err" && ok "load-only cause on stderr" || fail "load-only cause on stderr"
[ ! -e "$WORK/nope.key" ] && ok "no key minted at explicit path" || fail "key minted at explicit path"

# --- --identity valid key created elsewhere → served ------------------------
ELSE_XDG="$WORK/xdg2"
ELSEKEY="$ELSE_XDG/swartznet/identity.key"
XDG_DATA_HOME="$ELSE_XDG" "$BIN" add --no-dht --port 0 --api-addr localhost:0 \
  --data-dir "$WORK/mk/data" --index-dir "$WORK/mk/index" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$WORK/mk.out" 2>/dev/null &
MPID=$!
for i in $(seq 1 100); do [ -f "$ELSEKEY" ] && break; sleep 0.05; done
kill -INT $MPID; wait $MPID
[ -f "$ELSEKEY" ] || fail "xdg2 key was never minted"
ELSEPK=$(python3 -c 'import sys; print(open(sys.argv[1],"rb").read()[32:].hex())' "$ELSEKEY")
serve_bg run7 --identity "$ELSEKEY" || fail "serve with explicit valid key failed"
PK7=$(pubkey_of "$ADDR")
[ "$PK7" = "$ELSEPK" ] && ok "--identity explicit valid key served" || fail "explicit key pubkey mismatch"
kill -INT $SPID; wait $SPID

echo
echo "PASS=$PASS FAIL=$FAIL"
rm -rf "$WORK"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
