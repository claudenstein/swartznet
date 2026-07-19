#!/bin/bash
# smoke-web.sh — end-to-end smoke test of the embedded web client against a real
# running daemon. Boots a throwaway node, verifies the SPA is served, every read
# endpoint answers, the CSRF guard behaves (loopback allowed / cross-origin
# rejected), a PATCH merge round-trips + clamps, and errors are plain text.
#
# Usage: scripts/smoke-web.sh [port]   (default port 7699)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/dist/swartznet"
PORT="${1:-7699}"
BASE="http://localhost:$PORT"
TMP="$(mktemp -d)"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }

DAEMON_PID=""
cleanup() { [ -n "$DAEMON_PID" ] && kill "$DAEMON_PID" 2>/dev/null; wait "$DAEMON_PID" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

[ -x "$BIN" ] || { echo "missing $BIN — run: go build -o dist/swartznet ./cmd/swartznet"; exit 1; }

# A bare random infohash is the least-side-effect way to bring the daemon up
# (no download starts). --no-dht keeps it hermetic; XDG_DATA_HOME sandboxes state.
IH="$(openssl rand -hex 20 2>/dev/null || head -c20 /dev/urandom | od -An -tx1 | tr -d ' \n')"
# Flags MUST precede the positional target (Go's flag package stops at the first
# non-flag argument).
XDG_DATA_HOME="$TMP/xdg" "$BIN" add --api-addr "localhost:$PORT" --no-dht --no-index "$IH" \
  >"$TMP/daemon.log" 2>&1 &
DAEMON_PID=$!

# Wait for the API.
for i in $(seq 1 50); do
  curl -sf "$BASE/healthz" >/dev/null 2>&1 && break
  sleep 0.2
  kill -0 "$DAEMON_PID" 2>/dev/null || { echo "daemon died:"; cat "$TMP/daemon.log"; exit 1; }
done
curl -sf "$BASE/healthz" >/dev/null 2>&1 || { echo "daemon never came up:"; cat "$TMP/daemon.log"; exit 1; }

# --- Serving ---
curl -s "$BASE/" | grep -q 'SwartzNet' && ok "GET / serves the SPA shell" || fail "GET / did not serve the shell"
curl -s "$BASE/" | grep -q '/static/app.js' && ok "shell loads the module entry" || fail "shell missing app.js"
ct=$(curl -sI "$BASE/static/app.js" | tr -d '\r' | awk -F': ' 'tolower($1)=="content-type"{print $2}')
echo "$ct" | grep -qi 'javascript' && ok "GET /static/app.js is javascript ($ct)" || fail "app.js content-type: $ct"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/static/nope.js")" = "404" ] && ok "missing static asset → 404" || fail "missing asset not 404"
[ "$(curl -s -o /dev/null -w '%{http_code}' "$BASE/no-such-path")" = "404" ] && ok "unknown path → 404 (no SPA fallback)" || fail "unknown path not 404"
for p in status torrents capabilities companion config/rate-limit config/queue; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/$p")
  [ "$code" = "200" ] && ok "GET /$p → 200" || fail "GET /$p → $code"
done
# index/stats + aggregate may be 200 or 503 (subsystem off) — both are acceptable.
for p in index/stats aggregate publish; do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/$p")
  { [ "$code" = "200" ] || [ "$code" = "503" ]; } && ok "GET /$p → $code (200 or 503 ok)" || fail "GET /$p → $code"
done

# --- CSRF guard (the client's whole security assumption) ---
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{"q":"smoke"}' "$BASE/search")
[ "$code" = "200" ] && ok "loopback POST /search → 200" || fail "loopback POST /search → $code"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -H 'Origin: http://evil.example' -d '{"q":"x"}' "$BASE/search")
[ "$code" = "403" ] && ok "cross-origin POST /search → 403 (CSRF guard)" || fail "cross-origin POST /search → $code (expected 403)"

# --- Write round-trip + merge + clamp ---
curl -s -X PATCH -H 'Content-Type: application/json' -d '{"upload_bps":1024}' "$BASE/config/rate-limit" >/dev/null
rl=$(curl -s "$BASE/config/rate-limit")
echo "$rl" | grep -q '"upload_bps":1024' && ok "PATCH rate-limit merged (upload set)" || fail "rate-limit merge: $rl"
echo "$rl" | grep -q '"download_bps":0' && ok "PATCH rate-limit left download untouched (merge semantics)" || fail "rate-limit clobbered download: $rl"
cap=$(curl -s -X PATCH -H 'Content-Type: application/json' -d '{"share_local":9}' "$BASE/capabilities")
echo "$cap" | grep -q '"share_local":2' && ok "PATCH capabilities clamps share_local 9→2" || fail "share_local not clamped: $cap"

# --- Error shape: plain text, not JSON ---
body=$(curl -s -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/torrent")
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' "$BASE/torrent")
{ [ "$code" = "400" ] && echo "$body" | grep -qi "missing 'uri'"; } && ok "POST /torrent {} → 400 plain-text error" || fail "error shape: $code $body"
echo "$body" | grep -q '^{' && fail "error body looks like JSON (client reads .text())" || ok "error body is not JSON"

echo
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
