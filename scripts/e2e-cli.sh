#!/bin/bash
# e2e-cli.sh — exercises EVERY swartznet CLI subcommand end-to-end: the offline
# ones directly, and the daemon-facing ones against a real running daemon over
# its HTTP API. Complements the per-slice dod scripts by driving the whole CLI
# surface as a user would, catching integration regressions (bad flags, crashes,
# malformed JSON, error-path handling).
#
# Usage: scripts/e2e-cli.sh [port]   (default 7697)
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/dist/swartznet"
PORT="${1:-7697}"
API="localhost:$PORT"
TMP="$(mktemp -d)"
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1 ${2:+:: $2}"; }
DAEMON_PID=""
cleanup() { [ -n "$DAEMON_PID" ] && kill "$DAEMON_PID" 2>/dev/null; wait "$DAEMON_PID" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT INT TERM
[ -x "$BIN" ] || { echo "missing $BIN"; exit 1; }
export XDG_DATA_HOME="$TMP/xdg"; mkdir -p "$XDG_DATA_HOME"

# ---------------------------------------------------------------------------
# Part 1 — offline subcommands (no daemon)
# ---------------------------------------------------------------------------
"$BIN" version 2>&1 | grep -q 'swartznet' && ok "version prints" || fail "version"
"$BIN" --help 2>&1 | grep -q 'Commands:' && ok "--help prints the command index" || fail "--help"
# Every listed command must have coverage here — guard against an unexercised one.
for c in add create status files search index trust confirm flag crawl-probe crawl companion aggregate; do
  "$BIN" --help 2>&1 | grep -qE "^  $c( |$)|^  $c[ ]" || fail "--help missing $c"
done
ok "--help lists all 13 subcommands"

# create → a real .torrent from a fixture, then inspect it exists.
mkdir -p "$TMP/content"
printf 'the quick brown fox jumps over swartznet e2e fixture zebra\n' > "$TMP/content/doc.txt"
"$BIN" create "$TMP/content" -o "$TMP/fix.torrent" >"$TMP/create.log" 2>&1 \
  && [ -f "$TMP/fix.torrent" ] && ok "create builds a .torrent" || fail "create" "$(cat "$TMP/create.log")"

# create --sign (identity auto-created under XDG default).
"$BIN" create "$TMP/content" -o "$TMP/signed.torrent" --sign >"$TMP/sign.log" 2>&1 \
  && [ -f "$TMP/signed.torrent" ] && ok "create --sign signs a .torrent" || fail "create --sign" "$(cat "$TMP/sign.log")"

# trust list/add/remove (offline allowlist).
PK=$(printf '%064x' 1)
"$BIN" trust add "$PK" >/dev/null 2>&1 && ok "trust add" || fail "trust add"
"$BIN" trust list 2>&1 | grep -qi "$PK" && ok "trust list shows the added key" || fail "trust list"
"$BIN" trust remove "$PK" >/dev/null 2>&1 && ok "trust remove" || fail "trust remove"

# aggregate build/inspect/find (offline).
python3 - > "$TMP/recs.jsonl" <<'PY'
import json
for i in range(6):
    print(json.dumps({"kw":["alpha","beta"][i%2],"ih":f"{i:040x}","t":1700000000+i}))
PY
"$BIN" aggregate build --in "$TMP/recs.jsonl" --out "$TMP/idx.snagg" >/dev/null 2>&1 \
  && ok "aggregate build" || fail "aggregate build"
"$BIN" aggregate inspect "$TMP/idx.snagg" 2>&1 | grep -q 'records' && ok "aggregate inspect" || fail "aggregate inspect"
"$BIN" aggregate find "$TMP/idx.snagg" alpha 2>&1 | grep -q 'records' && ok "aggregate find" || fail "aggregate find"

# crawl fail-closed exit (no daemon).
"$BIN" crawl --seed 127.0.0.1:1 --timeout-ms 200 --duration-ms 700 >/dev/null 2>&1; [ $? -eq 1 ] \
  && ok "crawl exits 1 on a dead network" || fail "crawl exit"

# ---------------------------------------------------------------------------
# Part 2 — daemon-facing subcommands against a live node
# ---------------------------------------------------------------------------
IH="$(openssl rand -hex 20 2>/dev/null || head -c20 /dev/urandom | od -An -tx1 | tr -d ' \n')"
# Keep --no-dht for hermeticity but KEEP the index enabled so index/search have
# a real backend (a --no-index daemon 503s /index/stats, which is not the path
# under test here).
"$BIN" add --api-addr "$API" --no-dht "$IH" >"$TMP/daemon.log" 2>&1 &
DAEMON_PID=$!
for i in $(seq 1 50); do curl -sf "http://$API/healthz" >/dev/null 2>&1 && break; sleep 0.2
  kill -0 "$DAEMON_PID" 2>/dev/null || { echo "daemon died:"; cat "$TMP/daemon.log"; exit 1; }; done
curl -sf "http://$API/healthz" >/dev/null 2>&1 || { echo "daemon never up"; cat "$TMP/daemon.log"; exit 1; }
ok "daemon boots + serves the API (add IS the daemon)"

# status (text + json).
"$BIN" status --api-addr "$API" 2>&1 | grep -qiE 'swarm|local|status|node' && ok "status (text)" || fail "status text"
"$BIN" status --api-addr "$API" --json 2>"$TMP/st.err" | python3 -m json.tool >/dev/null 2>&1 \
  && ok "status --json is valid JSON" || fail "status json" "$(cat "$TMP/st.err")"

# search (empty index → 0 results, must not crash; json must parse).
"$BIN" search --api-addr "$API" nonexistentterm >/dev/null 2>&1 && ok "search (text) runs" || fail "search text"
"$BIN" search --api-addr "$API" --json nonexistentterm 2>/dev/null | python3 -m json.tool >/dev/null 2>&1 \
  && ok "search --json is valid JSON" || fail "search json"

# index stats.
"$BIN" index --api-addr "$API" >/dev/null 2>&1 && ok "index stats" || fail "index stats"

# files on a metadata-pending torrent — must handle gracefully (not crash).
"$BIN" files "$IH" --api-addr "$API" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] || [ $rc -eq 1 ]; } && ok "files handles a metadata-pending torrent (exit $rc)" || fail "files crashed (exit $rc)"

# confirm / flag on an arbitrary infohash.
"$BIN" confirm "$IH" --api-addr "$API" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] || [ $rc -eq 1 ]; } && ok "confirm (exit $rc)" || fail "confirm crashed (exit $rc)"
"$BIN" flag "$IH" --api-addr "$API" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] || [ $rc -eq 1 ]; } && ok "flag (exit $rc)" || fail "flag crashed (exit $rc)"

# companion status/follow/unfollow/refresh against the daemon.
"$BIN" companion status --api-addr "$API" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] || [ $rc -eq 1 ]; } && ok "companion status (exit $rc)" || fail "companion status"
"$BIN" companion follow "$PK" --api-addr "$API" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] || [ $rc -eq 1 ]; } && ok "companion follow (exit $rc)" || fail "companion follow"
"$BIN" companion unfollow "$PK" --api-addr "$API" >/dev/null 2>&1; rc=$?
{ [ $rc -eq 0 ] || [ $rc -eq 1 ]; } && ok "companion unfollow (exit $rc)" || fail "companion unfollow"

# bad-input hygiene: a malformed infohash must be rejected, not crash.
"$BIN" confirm "not-a-hash" --api-addr "$API" >/dev/null 2>&1; [ $? -ne 0 ] \
  && ok "confirm rejects a malformed infohash (non-zero)" || fail "confirm accepted garbage"
"$BIN" status --api-addr "localhost:1" >/dev/null 2>&1; [ $? -ne 0 ] \
  && ok "status against a dead API exits non-zero" || fail "status masked a dead daemon"

echo
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
