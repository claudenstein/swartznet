#!/bin/bash
# Slice 0 Definition-of-Done: drive the real dist/swartznet binary.
set -u
BIN=/home/kartofel/Claude/swartznet/dist/swartznet
WORK=$(mktemp -d)
# Hermetic XDG root: identity auto-creation (Slice 1+) follows the default
# path and must never touch the operator's real ~/.local/share/swartznet.
export XDG_DATA_HOME="$WORK/xdg"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
check() { # check <desc> <expr...>
  local desc="$1"; shift
  if "$@" >/dev/null 2>&1; then ok "$desc"; else fail "$desc"; fi
}

# --- CLI basics -------------------------------------------------------------
out=$("$BIN" version); [ "$out" = "swartznet v0.9.0-dev" ] && ok "version output" || fail "version output: $out"
"$BIN" help | grep -q "SWARTZNET_LOG" && ok "help documents SWARTZNET_LOG" || fail "help documents SWARTZNET_LOG"
"$BIN" help | grep -q "SWARTZNET_UNSAFE" && ok "help documents SWARTZNET_UNSAFE" || fail "help documents SWARTZNET_UNSAFE"
"$BIN" wat >/dev/null 2>"$WORK/unk.err"; [ $? -eq 2 ] && ok "unknown command exit 2" || fail "unknown command exit 2"
grep -q 'unknown command "wat"' "$WORK/unk.err" && ok "unknown command message" || fail "unknown command message"
"$BIN" >/dev/null 2>&1; [ $? -eq 2 ] && ok "no-args exit 2" || fail "no-args exit 2"

# --- serve lifecycle --------------------------------------------------------
"$BIN" add --no-dht --no-index --port 0 --api-addr localhost:0 --data-dir "$WORK/data" --index-dir "$WORK/index" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >"$WORK/serve.out" 2>"$WORK/serve.err" &
SPID=$!
ADDR=""
for i in $(seq 1 100); do
  ADDR=$(sed -n 's/^HTTP API listening on //p' "$WORK/serve.out" | head -1)
  [ -n "$ADDR" ] && break
  sleep 0.05
done
[ -n "$ADDR" ] && ok "add reports listening address ($ADDR)" || fail "add reports listening address"

# /status JSON validity
BODY=$(curl -s "http://$ADDR/status")
echo "$BODY" | python3 -m json.tool >/dev/null 2>&1 && ok "curl /status parses as JSON" || fail "curl /status parses as JSON"
# Structural golden: since Slice 1 a real daemon also carries publisher.pubkey.
echo "$BODY" | python3 -c '
import json,re,sys
d=json.load(sys.stdin)
pk=d["publisher"].pop("pubkey","")
assert re.fullmatch(r"[0-9a-f]{64}", pk), f"bad pubkey {pk!r}"
assert d=={"local":{"indexed":False,"doc_count":0},"swarm":{"known_peers":0,"capable_peers":0},"publisher":{"total_keywords":0,"total_hits":0}}, d
' && ok "/status golden body (+pubkey)" || fail "/status golden body: $BODY"
CT=$(curl -s -o /dev/null -w '%{content_type}' "http://$ADDR/status")
[ "$CT" = "application/json" ] && ok "/status content-type" || fail "/status content-type: $CT"

# healthz
curl -s "http://$ADDR/healthz" | grep -q '"ok":true' && ok "/healthz ok" || fail "/healthz ok"
curl -s "http://$ADDR/healthz" | grep -q '"version":"v0.9.0-dev"' && ok "/healthz version" || fail "/healthz version"

# thin-client status agrees with curl
"$BIN" status --api-addr "$ADDR" >"$WORK/st.txt" 2>"$WORK/st.err"
[ $? -eq 0 ] && ok "status exit 0" || fail "status exit 0: $(cat "$WORK/st.err")"
grep -q "SwartzNet daemon status" "$WORK/st.txt" && ok "status text header" || fail "status text header"
grep -q "not configured" "$WORK/st.txt" && ok "status shows unwired index" || fail "status shows unwired index"
"$BIN" status --api-addr "$ADDR" --json | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["status"]["local"]["indexed"] is False; assert "aggregate" not in d' \
  && ok "status --json envelope matches /status" || fail "status --json envelope matches /status"

# CSRF: spoofed Origin rejected on non-GET; loopback Origin passes guard (405 from mux)
C1=$(curl -s -o "$WORK/csrf1.out" -w '%{http_code}' -X POST -H 'Origin: http://evil.example' "http://$ADDR/status")
[ "$C1" = "403" ] && ok "POST spoofed Origin -> 403" || fail "POST spoofed Origin -> 403 (got $C1)"
grep -q 'forbidden: cross-origin request' "$WORK/csrf1.out" && ok "403 body text" || fail "403 body text"
C2=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Origin: http://localhost:7654' "http://$ADDR/status")
[ "$C2" = "405" ] && ok "POST loopback Origin passes guard -> 405" || fail "POST loopback Origin -> 405 (got $C2)"
C3=$(curl -s -o /dev/null -w '%{http_code}' -H 'Origin: http://evil.example' "http://$ADDR/status")
[ "$C3" = "200" ] && ok "GET exempt from guard" || fail "GET exempt from guard (got $C3)"
C4=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Host: evil.example' "http://$ADDR/status")
[ "$C4" = "403" ] && ok "POST non-loopback Host -> 403" || fail "POST non-loopback Host -> 403 (got $C4)"

# web stub
curl -s "http://$ADDR/" | grep -q "<h1>SwartzNet</h1>" && ok "GET / serves stub page" || fail "GET / serves stub page"
C5=$(curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/nope")
[ "$C5" = "404" ] && ok "unknown path 404 (no SPA fallback)" || fail "unknown path 404 (got $C5)"
curl -s -o /dev/null -w '%{http_code}' "http://$ADDR/static/style.css" | grep -q 200 && ok "static asset served" || fail "static asset served"

# SIGINT -> teardown order + exit 130
kill -INT $SPID
wait $SPID; RC=$?
[ "$RC" = "130" ] && ok "SIGINT exit 130" || fail "SIGINT exit 130 (got $RC)"
# Ctrl-C during the metadata wait is silent on stdout (legacy contract);
# "Shutting down..." prints only from the post-metadata progress loop.
grep -q "Shutting down..." "$WORK/serve.out" && fail "unexpected shutdown line in metadata-wait phase" || ok "metadata-wait interrupt is silent"
python3 - "$WORK/serve.err" <<'EOF' && ok "teardown log order" || fail "teardown log order"
import sys
log = open(sys.argv[1]).read()
names = ["daemon.close_begin", "daemon.bg_joined", "httpapi.stopped", "engine.stopped", "daemon.close_done"]
idx = [log.find(n) for n in names]
assert all(i >= 0 for i in idx), f"missing lines: {idx}"
assert idx == sorted(idx), f"out of order: {idx}"
EOF

# status against a dead daemon
"$BIN" status --api-addr "$ADDR" >/dev/null 2>"$WORK/dead.err"; RC=$?
[ "$RC" = "1" ] && ok "status vs dead daemon exit 1" || fail "status vs dead daemon exit 1 (got $RC)"
grep -q "start it with: swartznet add <magnet>" "$WORK/dead.err" && ok "dead-daemon hint" || fail "dead-daemon hint"

# SWARTZNET_LOG=debug changes verbosity
SWARTZNET_LOG=debug "$BIN" add --no-dht --no-index --port 0 --api-addr localhost:0 --data-dir "$WORK/d2" --index-dir "$WORK/i2" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >"$WORK/dbg.out" 2>"$WORK/dbg.err" &
DPID=$!; sleep 0.7; kill -INT $DPID; wait $DPID
grep -q "level=INFO" "$WORK/dbg.err" && ok "SWARTZNET_LOG=debug still logs info" || fail "debug run has info lines"
# default run must not show DEBUG; debug run is allowed to (none exist yet at slice 0) — assert level plumbed via a warn check instead
SWARTZNET_LOG=bogus "$BIN" add --no-dht --no-index --port 0 --api-addr localhost:0 --data-dir "$WORK/d3" --index-dir "$WORK/i3" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >"$WORK/bogus.out" 2>"$WORK/bogus.err" &
BPID=$!; sleep 0.7; kill -INT $BPID; wait $BPID
grep -q "unrecognized SWARTZNET_LOG value" "$WORK/bogus.err" && ok "bogus SWARTZNET_LOG warns" || fail "bogus SWARTZNET_LOG warns"

# SIGTERM also exits 130
"$BIN" add --no-dht --no-index --port 0 --api-addr localhost:0 --data-dir "$WORK/d4" --index-dir "$WORK/i4" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$WORK/t.out" 2>/dev/null &
TPID=$!; sleep 0.7; kill -TERM $TPID; wait $TPID; RC=$?
[ "$RC" = "130" ] && ok "SIGTERM exit 130" || fail "SIGTERM exit 130 (got $RC)"

# non-loopback bind: binds + warns UNAUTHENTICATED exactly once
"$BIN" add --no-dht --no-index --port 0 --api-addr 0.0.0.0:0 --data-dir "$WORK/d5" --index-dir "$WORK/i5" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  >"$WORK/nl.out" 2>"$WORK/nl.err" &
NPID=$!; sleep 0.7
N=$(grep -c "API is UNAUTHENTICATED" "$WORK/nl.err")
[ "$N" = "1" ] && ok "non-loopback bind warns exactly once" || fail "non-loopback warn count = $N"
grep -q "HTTP API listening on " "$WORK/nl.out" && ok "non-loopback bind still binds" || fail "non-loopback bind still binds"
kill -INT $NPID; wait $NPID

# --api-addr "" disables the API but daemon runs
"$BIN" add --no-dht --no-index --port 0 --api-addr "" --data-dir "$WORK/d6" --index-dir "$WORK/i6" aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa >"$WORK/off.out" 2>"$WORK/off.err" &
OPID=$!; sleep 0.7
kill -0 $OPID 2>/dev/null && ok "api-less add stays up" || fail "api-less add stays up"
grep -q "httpapi.listening" "$WORK/off.err" && fail "api-less add must not listen" || ok "api-less add has no listener log"
kill -INT $OPID; wait $OPID; RC=$?
[ "$RC" = "130" ] && ok "api-less add exit 130" || fail "api-less add exit 130 (got $RC)"

echo
echo "PASS=$PASS FAIL=$FAIL"
rm -rf "$WORK"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
