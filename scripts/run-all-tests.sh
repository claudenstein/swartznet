#!/bin/bash
# run-all-tests.sh — the single entry point for the whole SwartzNet test
# environment. It builds both binaries, then runs, in order:
#   1. the race-enabled unit suite (every package except the timing-sensitive
#      wirecompat/scenarios, mirroring the CI merge gate),
#   2. every per-slice binary Definition-of-Done script (drives the real CLI),
#   3. the web-client DoD (embed test + node --check + live smoke, incl. CSRF),
#   4. the whole-CLI end-to-end (every subcommand vs a live daemon),
#   5. the Slice-13 CI-mirror gate (gofmt / vet / tidy + wire-compat goldens).
# Each stage is tallied; a non-zero exit means at least one stage failed.
#
# Usage:
#   scripts/run-all-tests.sh            # everything
#   scripts/run-all-tests.sh --quick    # skip the slow per-slice DoD sweep
set -u
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GO=/usr/local/go/bin/go
QUICK=0; [ "${1:-}" = "--quick" ] && QUICK=1
cd "$ROOT"
STAGES=0; FAILED=0
declare -a RESULTS
stage() {
  STAGES=$((STAGES+1))
  local name="$1"; shift
  printf '\n\033[1m── %s ──\033[0m\n' "$name"
  if "$@"; then RESULTS+=("PASS  $name"); else RESULTS+=("FAIL  $name"); FAILED=$((FAILED+1)); fi
}

# 0. build both binaries (the CLI embeds the web assets).
build() {
  CGO_ENABLED=0 "$GO" build -o dist/swartznet ./cmd/swartznet || return 1
  ./scripts/build-gui.sh dev >/dev/null 2>&1 || return 1
  echo "both binaries built"
}
stage "build binaries" build

# 1. race unit suite (exclude the timing-sensitive scenarios, like CI).
unit() {
  local pkgs
  pkgs=$("$GO" list ./... | grep -v '/internal/wirecompat/scenarios$')
  "$GO" test -race -count=1 $pkgs
}
stage "unit tests (-race, CI set)" unit

# 2. per-slice binary DoD sweep.
if [ "$QUICK" -eq 0 ]; then
  dodsweep() {
    local rc=0
    for s in $(ls scripts/dod-slice*.sh | sort -V); do
      printf '  · %s ... ' "$(basename "$s")"
      if bash "$s" >/tmp/rat-$(basename "$s").log 2>&1; then echo "ok"; else echo "FAIL"; rc=1; tail -3 /tmp/rat-$(basename "$s").log; fi
    done
    return $rc
  }
  stage "per-slice binary DoD" dodsweep
else
  echo "(--quick: skipping the per-slice DoD sweep)"
fi

# 3. web-client DoD.
stage "web-client DoD" bash scripts/dod-webclient.sh

# 4. whole-CLI e2e.
stage "CLI end-to-end" bash scripts/e2e-cli.sh

# 5. CI-mirror gate (gofmt/vet/tidy + wire-compat goldens).
stage "CI-mirror gate (dod-slice13)" bash scripts/dod-slice13.sh

# ---- summary ----
printf '\n\033[1m═══ summary ═══\033[0m\n'
for r in "${RESULTS[@]}"; do
  case "$r" in FAIL*) printf '\033[31m%s\033[0m\n' "$r";; *) printf '\033[32m%s\033[0m\n' "$r";; esac
done
printf '\n%d stage(s), %d failed\n' "$STAGES" "$FAILED"
[ "$FAILED" -eq 0 ]
