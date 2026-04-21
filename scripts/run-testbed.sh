#!/usr/bin/env bash
# run-testbed.sh — Layer-B Docker testbed driver.
#
# Usage:
#   scripts/run-testbed.sh <scenario>
#   scripts/run-testbed.sh all
#
# Scenario names: s1  s2  s3  s4  all
#
#   s1  — healthy baseline (no netem)
#   s2  — lossy profile (5% packet loss, 150ms RTT)
#   s3  — mobile-4G profile (40ms+20ms jitter, 10Mbit)
#   s4  — home-DSL profile (20ms+5ms jitter, 25Mbit)
#   all — run s1 through s4 in sequence
#
# Each scenario:
#   1. Brings up the 3-node docker compose stack with the correct NETEM_PROFILE.
#   2. Waits until all three containers are running.
#   3. Runs the scenario assertion script.
#   4. Tears down the stack (even on failure — via EXIT trap).
#   5. Writes per-run output to testbed/results/<scenario>-<timestamp>.log
#
# Prerequisites:
#   - docker compose v2 installed (checked at startup)
#   - Current user can run `docker` commands without sudo.
#     If not, run:  sudo usermod -aG docker $USER && newgrp docker
#   - dist/swartznet-testbed-linux-amd64 must exist (built by this script if
#     absent or if the source is newer — calls scripts/build-release.sh testbed).
#
# Ports used (must be free on localhost):
#   17654  seed-1 HTTP API
#   17655  seed-2 HTTP API
#   17656  leech-1 HTTP API
# Terminate any existing process on those ports before running.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TESTBED_DIR="$REPO_ROOT/testbed"
RESULTS_DIR="$TESTBED_DIR/results"
SCENARIOS_DIR="$TESTBED_DIR/scenarios"
COMPOSE_FILE="$TESTBED_DIR/docker-compose.yml"
BINARY="$REPO_ROOT/dist/swartznet-testbed-linux-amd64"
GO="${GO:-/usr/local/go/bin/go}"

# ── Helper utilities ──────────────────────────────────────────────────────────

log()  { echo "[run-testbed] $*"; }
fail() { echo "[run-testbed] ERROR: $*" >&2; exit 1; }

# ── Preflight checks ──────────────────────────────────────────────────────────

if [[ $# -ne 1 ]]; then
    echo "Usage: $0 <s1|s2|s3|s4|all>" >&2
    exit 2
fi

SCENARIO="$1"

# Validate scenario argument.
case "$SCENARIO" in
    s1|s2|s3|s4|all) ;;
    *) fail "Unknown scenario '$SCENARIO'. Valid: s1 s2 s3 s4 all" ;;
esac

# Check docker compose v2 is available.
if ! docker compose version >/dev/null 2>&1; then
    echo "" >&2
    echo "ERROR: 'docker compose' not found." >&2
    echo "" >&2
    echo "Install docker compose v2:" >&2
    echo "  sudo apt-get install docker-compose-v2" >&2
    echo "" >&2
    echo "Then ensure your user can run docker without sudo:" >&2
    echo "  sudo usermod -aG docker \$USER && newgrp docker" >&2
    echo "" >&2
    exit 1
fi

# Check docker socket is accessible (user in docker group or running as root).
if ! docker info >/dev/null 2>&1; then
    echo "" >&2
    echo "ERROR: Cannot connect to the Docker daemon." >&2
    echo "" >&2
    echo "Fix: add your user to the docker group and activate it:" >&2
    echo "  sudo usermod -aG docker \$USER" >&2
    echo "  newgrp docker              # or log out and back in" >&2
    echo "" >&2
    echo "Verify with: docker ps" >&2
    echo "" >&2
    exit 1
fi

# Build the testbed binary if absent or stale.
BUILD_NEEDED=0
if [[ ! -f "$BINARY" ]]; then
    log "Binary not found: $BINARY"
    BUILD_NEEDED=1
else
    # Rebuild if any Go source is newer than the binary.
    # find returns 0 even on no matches; we pipe to read to capture output.
    NEWER=$( find "$REPO_ROOT/cmd/swartznet" "$REPO_ROOT/internal" \
                  -name "*.go" -newer "$BINARY" 2>/dev/null | head -1 )
    if [[ -n "$NEWER" ]]; then
        log "Source newer than binary ($NEWER); rebuilding."
        BUILD_NEEDED=1
    fi
fi

if [[ "$BUILD_NEEDED" -eq 1 ]]; then
    log "Building testbed binary (CGO_ENABLED=0, linux/amd64)..."
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
        "$GO" build -trimpath -ldflags "-s -w -X main.Version=testbed" \
        -o "$BINARY" \
        "$REPO_ROOT/cmd/swartznet"
    log "Built: $BINARY ($(du -h "$BINARY" | cut -f1))"
fi

mkdir -p "$RESULTS_DIR"

# ── Per-scenario config ───────────────────────────────────────────────────────

scenario_netem_profile() {
    case "$1" in
        s1) echo "" ;;                         # no netem
        s2) echo "/netem/lossy.sh" ;;
        s3) echo "/netem/mobile-4g.sh" ;;
        s4) echo "/netem/home-dsl.sh" ;;
    esac
}

scenario_script() {
    echo "$SCENARIOS_DIR/$1-"*".sh"
}

# ── Run one scenario ──────────────────────────────────────────────────────────

run_scenario() {
    local sc="$1"
    local ts
    ts=$(date +%Y%m%d-%H%M%S)
    local logfile="$RESULTS_DIR/$sc-$ts.log"
    local netem_profile
    netem_profile=$(scenario_netem_profile "$sc")
    local script_glob="$SCENARIOS_DIR/${sc}-*.sh"
    local script
    script=$(echo $script_glob)   # expand glob (one match expected)

    if [[ ! -f "$script" ]]; then
        fail "Scenario script not found: $script_glob"
    fi

    log "=== Running $sc (log: $logfile) ==="
    [[ -n "$netem_profile" ]] && log "    netem: $netem_profile" || log "    netem: none"

    # Tear down the stack on EXIT (success or failure).
    # Using a flag file instead of a variable so the trap fires correctly
    # even if the subshell approach changes scope.
    local flag_file
    flag_file=$(mktemp /tmp/sn-teardown-XXXXXX)
    rm -f "$flag_file"   # file absent = teardown not yet done

    _do_teardown() {
        if [[ ! -f "$flag_file" ]]; then
            touch "$flag_file"
            log "Tearing down docker compose stack..."
            NETEM_PROFILE="$netem_profile" \
                docker compose -f "$COMPOSE_FILE" down -v 2>&1 | \
                tee -a "$logfile" || true
        fi
    }
    trap _do_teardown EXIT

    # Write scenario header to log.
    {
        echo "=== run-testbed: scenario=$sc ts=$ts ==="
        echo "    netem=$netem_profile"
        echo ""
    } | tee "$logfile"

    # Start the stack.
    log "Starting docker compose (netem=${netem_profile:-none})..."
    NETEM_PROFILE="$netem_profile" \
        docker compose -f "$COMPOSE_FILE" up --build -d 2>&1 | tee -a "$logfile"

    # Wait for all three containers to be in "running" state.
    local CONTAINERS=("sn-seed-1" "sn-seed-2" "sn-leech-1")
    local WAIT_SECS=120
    local deadline=$(( $(date +%s) + WAIT_SECS ))
    log "Waiting up to ${WAIT_SECS}s for all containers to be running..."
    while true; do
        local all_running=1
        for c in "${CONTAINERS[@]}"; do
            local st
            st=$(docker inspect --format='{{.State.Status}}' "$c" 2>/dev/null || echo "missing")
            if [[ "$st" != "running" ]]; then
                all_running=0
                break
            fi
        done
        if [[ "$all_running" -eq 1 ]]; then
            log "All containers running."
            break
        fi
        if [[ "$(date +%s)" -ge "$deadline" ]]; then
            log "ERROR: Containers not running after ${WAIT_SECS}s" | tee -a "$logfile"
            docker compose -f "$COMPOSE_FILE" ps 2>&1 | tee -a "$logfile"
            docker compose -f "$COMPOSE_FILE" logs 2>&1 | tail -50 | tee -a "$logfile"
            _do_teardown
            trap - EXIT
            rm -f "$flag_file"
            return 1
        fi
        sleep 2
    done

    # Run the assertion script.
    log "Running assertion script: $script"
    local assertion_exit=0
    bash "$script" 2>&1 | tee -a "$logfile" || assertion_exit=$?

    _do_teardown
    trap - EXIT
    rm -f "$flag_file"

    return "$assertion_exit"
}

# ── Main dispatch ─────────────────────────────────────────────────────────────

SCENARIOS_TO_RUN=()
if [[ "$SCENARIO" == "all" ]]; then
    SCENARIOS_TO_RUN=(s1 s2 s3 s4)
else
    SCENARIOS_TO_RUN=("$SCENARIO")
fi

declare -A RESULTS
OVERALL_EXIT=0
WALL_START=$(date +%s)

for sc in "${SCENARIOS_TO_RUN[@]}"; do
    sc_start=$(date +%s)
    if run_scenario "$sc"; then
        RESULTS["$sc"]="PASS"
        log "$sc: PASS ($(( $(date +%s) - sc_start ))s)"
    else
        RESULTS["$sc"]="FAIL"
        OVERALL_EXIT=1
        log "$sc: FAIL ($(( $(date +%s) - sc_start ))s)"
    fi
    # Allow a brief settle between scenarios to avoid port conflicts.
    if [[ "${#SCENARIOS_TO_RUN[@]}" -gt 1 ]]; then
        sleep 3
    fi
done

# ── Scoreboard ────────────────────────────────────────────────────────────────

echo ""
echo "╔══════════════════════════════════════╗"
echo "║        Testbed scenario results      ║"
echo "╠══════════════════════════════════════╣"
for sc in "${SCENARIOS_TO_RUN[@]}"; do
    result="${RESULTS[$sc]:-SKIP}"
    printf "║  %-6s  %-28s  ║\n" "$sc" "$result"
done
echo "╠══════════════════════════════════════╣"
printf "║  total wall clock: %-18s  ║\n" "$(( $(date +%s) - WALL_START ))s"
echo "╚══════════════════════════════════════╝"
echo ""
echo "Logs: $RESULTS_DIR/"
echo ""

exit "$OVERALL_EXIT"
