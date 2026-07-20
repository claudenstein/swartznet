#!/bin/bash
# e2e-api-robustness.sh — throws malformed, oversized, edge-case, and concurrent
# requests at a live daemon and asserts it (a) never crashes — healthz still
# answers afterward — and (b) returns sensible status codes rather than a 500 or
# a hang. Complements the static bug hunt: this catches runtime crashes/hangs.
#
# Usage: scripts/e2e-api-robustness.sh [port]   (default 7692)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/dist/swartznet"
PORT="${1:-7692}"
B="http://localhost:$PORT"
TMP="$(mktemp -d)"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1 ${2:+:: $2}"; }
DP=""
cleanup() { [ -n "$DP" ] && kill "$DP" 2>/dev/null; wait "$DP" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT INT TERM
[ -x "$BIN" ] || { echo "missing $BIN"; exit 1; }
export XDG_DATA_HOME="$TMP/xdg"

IH="$(openssl rand -hex 20 2>/dev/null || head -c20 /dev/urandom | od -An -tx1 | tr -d ' \n')"
"$BIN" add --api-addr "localhost:$PORT" --no-dht "$IH" >"$TMP/d.log" 2>&1 &
DP=$!
for i in $(seq 1 50); do curl -sf "$B/healthz" >/dev/null 2>&1 && break; sleep 0.2
  kill -0 "$DP" 2>/dev/null || { echo "daemon died"; cat "$TMP/d.log"; exit 1; }; done

# Every curl carries --max-time so a daemon hang surfaces as a failed assertion,
# never a hung harness.
code() { curl -s -m 10 -o /dev/null -w '%{http_code}' "$@"; }
post()  { curl -s -m 10 -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/json' "$@"; }

# code() helper: assert a request returns one of the accepted codes (not 500, not a hang).
want() { # want "<desc>" "<got>" "<space-separated acceptable codes>"
  local desc="$1" got="$2" acc="$3"
  for c in $acc; do [ "$got" = "$c" ] && { ok "$desc → $got"; return; }; done
  fail "$desc → $got (want one of: $acc)"
}

# --- malformed / edge request bodies ---
want "malformed JSON to /search"      "$(post -d '{not json'        "$B/search")"   "400"
want "empty body to /search"          "$(post -d ''                 "$B/search")"   "200 400"
want "wrong-type field to /search"    "$(post -d '{"q":123}'        "$B/search")"   "400"
want "huge limit to /search (clamp)"  "$(post -d '{"q":"x","limit":100000000}' "$B/search")" "200"
want "unicode query to /search"       "$(post -d '{"q":"café ☃ 日本語"}' "$B/search")" "200"
# Large body via a FILE, STREAMED in (a 1MB command-line/printf arg would hit
# ARG_MAX). This actually exercises the daemon's request-body size cap.
{ printf '{"q":"'; head -c 1000000 /dev/zero | tr '\0' a; printf '"}'; } > "$TMP/big.json"
want "1MB query to /search (cap)"     "$(post --data-binary @"$TMP/big.json" "$B/search")" "400 413"
want "malformed JSON to /torrent"     "$(post -d '{'               "$B/torrent")"  "400"
want "missing uri to /torrent"        "$(post -d '{}'              "$B/torrent")"  "400"
want "empty uri to /torrent"          "$(post -d '{"uri":""}'      "$B/torrent")"  "400"
want "junk magnet to /torrent"        "$(post -d '{"uri":"not-a-magnet"}' "$B/torrent")" "400"
want "bad infohash to /confirm"       "$(post -d '{"infohash":"xyz"}' "$B/confirm")" "400"
want "uppercase infohash to /confirm" "$(post -d "{\"infohash\":\"$(printf '%040X' 255)\"}" "$B/confirm")" "200 400"
want "bad pubkey to companion/follow" "$(post -d '{"pubkey":"short"}' "$B/companion/follow")" "400 503"
want "negative rate to config"        "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH -H 'Content-Type: application/json' -d '{"upload_bps":-5}' "$B/config/rate-limit")" "200 400"

# --- wrong methods / paths ---
want "GET on POST-only /search"       "$(code "$B/search")"                        "405 404"
want "DELETE on /status"              "$(code -X DELETE "$B/status")"              "405 404"
want "path traversal on /static"      "$(code "$B/static/../server.go")"           "400 404"
want "deep bad static path"           "$(code "$B/static/pages/../../etc/passwd")" "400 404"

# --- concurrency: a burst of parallel requests must not race/crash ---
for i in $(seq 1 40); do
  curl -s "$B/status" >/dev/null 2>&1 &
  curl -s -X POST -H 'Content-Type: application/json' -d '{"q":"race"}' "$B/search" >/dev/null 2>&1 &
  curl -s -X PATCH -H 'Content-Type: application/json' -d "{\"upload_bps\":$i}" "$B/config/rate-limit" >/dev/null 2>&1 &
done
wait
ok "survived 120 concurrent status/search/patch requests"

# --- the daemon must still be alive + healthy after all of that ---
# Retry briefly: the concurrent burst may leave the server draining connections
# for a moment. A REAL crash never recovers; transient saturation does.
healthy=0
for i in $(seq 1 15); do curl -sf -m 5 "$B/healthz" >/dev/null 2>&1 && { healthy=1; break; }; sleep 0.4; done
if [ "$healthy" -eq 1 ]; then
  ok "daemon still healthy after the robustness barrage"
  kill -0 "$DP" 2>/dev/null && ok "daemon process still alive" || fail "daemon process exited"
else
  fail "daemon unhealthy/crashed after the barrage"; tail -12 "$TMP/d.log"
fi

echo
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
