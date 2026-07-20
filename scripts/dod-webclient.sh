#!/bin/bash
# Web-client Definition-of-Done. Gates the embedded vanilla-JS SPA:
#   1. the Go embed manifest test (every module is actually embedded),
#   2. a JS syntax check of every module (node used only as a linter, if present),
#   3. the live smoke test against a real daemon (serving, all reads, the CSRF
#      guard, PATCH merge/clamp, plain-text errors) — scripts/smoke-web.sh.
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GO=/usr/local/go/bin/go
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
cd "$ROOT"

# 1. embed manifest.
if $GO test ./internal/httpapi/web/ -count=1 >/tmp/dod-web-embed.log 2>&1; then
  ok "embed manifest: every module is embedded + index.html loads the entry"
else
  fail "embed test — $(tail -3 /tmp/dod-web-embed.log)"
fi

# 2. JS syntax (linter-only; skipped if node absent).
if command -v node >/dev/null 2>&1; then
  jsfail=0
  for f in internal/httpapi/web/static/*.js internal/httpapi/web/static/pages/*.js; do
    node --check "$f" 2>/tmp/dod-web-js.log || { jsfail=1; echo "  $(basename "$f"): $(cat /tmp/dod-web-js.log)"; }
  done
  [ "$jsfail" -eq 0 ] && ok "all JS modules pass node --check" || fail "JS syntax error(s) above"
else
  echo "skip - node not present (JS syntax check skipped)"
fi

# 3. live smoke against a real daemon.
if bash "$ROOT/scripts/smoke-web.sh" 7698 >/tmp/dod-web-smoke.log 2>&1; then
  n=$(grep -c '^ok' /tmp/dod-web-smoke.log)
  ok "live smoke: $n checks (serving, reads, CSRF guard, PATCH merge/clamp, plain-text errors)"
else
  fail "smoke-web.sh — $(grep '^FAIL' /tmp/dod-web-smoke.log | head -3)"
fi

echo
echo "PASS=$PASS FAIL=$FAIL"
[ "$FAIL" -eq 0 ]
