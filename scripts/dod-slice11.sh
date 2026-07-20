#!/bin/bash
# Slice 11 Definition-of-Done: the native Fyne GUI. It is PURE PRESENTATION over
# the same daemon.Daemon — the per-layer L/S/D search rendering, the shared
# search fan-out, and the Apache-2.0 About fix are proven by headless Go gates
# (Fyne test driver, run -race); the GUI binary itself is built via
# build-gui.sh and smoke-run. Cumulative with dod-slice0..10.
set -u
ROOT=/home/kartofel/Claude/swartznet
GO=/usr/local/go/bin/go
GUI="$ROOT/dist/swartznet-gui-dev-linux-amd64"
WORK=$(mktemp -d)
PASS=0; FAIL=0
ok()   { PASS=$((PASS+1)); echo "ok   - $1"; }
fail() { FAIL=$((FAIL+1)); echo "FAIL - $1"; }
trap 'rm -rf "$WORK"' EXIT INT TERM

cd "$ROOT"
run_go() { if $GO test -race "$2" -run "$3" -count=1 >"$WORK/$1.log" 2>&1; then ok "$4"; else fail "$4 — $(tail -4 "$WORK/$1.log")"; fi; }

# ======================================================================
# Part 1 — GUI presentation logic + shared search path (Go gates, -race)
# ======================================================================
run_go gui ./internal/gui/ \
  'TestSearchRendersPerLayerCards|TestSearchLayerErrorRendersInline|TestSearchLayersGatedOnChecks|TestAboutLicenseIsApache|TestParseSearchLimit' \
  "GUI: per-layer L/S/D cards from the shared searchmux.Result, inline layer errors, layer gating, Apache-2.0 About, strict limit parse"
run_go dsearch ./internal/daemon/ 'TestDaemonSearchLocalLayer' \
  "Daemon.Search — the ONE fan-out the GUI + HTTP API share (Local hits, unrequested layers stay nil)"

# ======================================================================
# Part 2 — the GUI binary builds + runs + carries the corrected license
# ======================================================================
PATH="/usr/local/go/bin:$PATH" bash scripts/build-gui.sh dev >"$WORK/build.log" 2>&1
[ -x "$GUI" ] && ok "build-gui.sh dev produced an executable GUI binary" || fail "GUI binary missing — $(tail -3 "$WORK/build.log")"

"$GUI" --help >"$WORK/help.txt" 2>&1
grep -q 'swartznet-gui' "$WORK/help.txt" && ok "GUI binary runs (--help prints usage)" || fail "GUI --help failed"
grep -q -- '-no-dht-publish' "$WORK/help.txt" && ok "GUI exposes the privacy flags (--no-dht-publish)" || fail "GUI missing --no-dht-publish"

# §6 fix: the built binary carries the Apache-2.0 first-party license, never the
# legacy "MIT (SwartzNet code)" defect.
if strings "$GUI" 2>/dev/null | grep -q 'Apache-2.0 (SwartzNet)'; then
  ok "GUI About license states Apache-2.0 for first-party code"
else
  fail "GUI binary missing the Apache-2.0 license string"
fi
if strings "$GUI" 2>/dev/null | grep -q 'MIT (SwartzNet'; then
  fail "GUI binary still carries the legacy 'MIT (SwartzNet code)' license defect"
else
  ok "GUI binary does NOT carry the legacy MIT license defect"
fi

# The CLI still builds alongside the GUI (shared module).
$GO build -o "$WORK/swartznet" ./cmd/swartznet 2>>"$WORK/build.log" && ok "CLI still builds with Fyne in the module" || fail "CLI build regressed"

echo
echo "PASS=$PASS FAIL=$FAIL"
exit $([ $FAIL -eq 0 ] && echo 0 || echo 1)
