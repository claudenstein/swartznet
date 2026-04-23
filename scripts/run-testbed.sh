#!/usr/bin/env bash
# run-testbed.sh — Layer-B Docker testbed driver.
#
# Usage:
#   scripts/run-testbed.sh <scenario>
#   scripts/run-testbed.sh all
#
# Scenario names: s1 s2 s3 s4 s5 s6 s7 s8 s9 s10 s11 swarm all
#
# Optional flags (must come AFTER the scenario name):
#   --json=<path>   emit a machine-readable JSON summary of the
#                   scenarios' results, durations, and netem
#                   profile at <path>. Useful for CI consumers
#                   and perf-regression tracking; the scoreboard
#                   always prints to stdout regardless.
#
#   s1     — healthy baseline (no netem, 3-node stack)
#   s2     — lossy profile (5% packet loss, 150ms RTT)
#   s3     — mobile-4G profile (40ms+20ms jitter, 10Mbit)
#   s4     — home-DSL profile (20ms+5ms jitter, 25Mbit)
#   s5     — end-to-end piece transfer (no netem, real fixture content)
#   s6     — 6-node swarm piece transfer at scale (2 seeds + 4 leeches,
#            uses docker-compose.swarm.yml, separate ports 17664-17669)
#   s7     — Layer-S sn_search fan-out across the 6-node swarm
#   s8     — 6-node swarm under lossy netem (5% loss + 150ms RTT),
#            convergence budget 300s
#   s9     — pass-along / late-joiner: after the 4 leeches complete,
#            kill both seeds, bring up leech-5 via docker compose
#            profile, and verify leech-5 downloads entirely from the
#            ex-leeches
#   s10    — mid-transfer seed churn under lossy netem: kill seed-1
#            once leech-1 crosses 30% progress; verify all 4 leeches
#            still converge via seed-2 + mutual exchange
#   s11    — vanilla BT client interop: an aria2c container joins the
#            swarm (compose profile `vanilla`) and must download the
#            4-MiB fixture from SwartzNet peers using only BEP-3/9/10
#            traffic. Proves wire-compat at the real-TCP level.
#   s12    — Layer-D (DHT keyword index) end-to-end: DHT-enabled
#            6-node stack bootstrapping to seed-1, seeds publish
#            BEP-44 keyword→infohash mappings, leech-1's /search
#            with dht:true resolves "aethergram" to the fixture
#            infohash. First testbed scenario to exercise DHT.
#   swarm  — alias: run s6 then s7 against a single long-lived 6-node
#            stack (avoids paying the compose up/down cost twice)
#   all    — run s1..s5 (3-node) then swarm (6-node) then s8 (lossy
#            swarm) then s9 (pass-along) then s10 (mid-transfer churn)
#            then s11 (vanilla interop) then s12 (DHT/Layer-D)
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
COMPOSE_SWARM_FILE="$TESTBED_DIR/docker-compose.swarm.yml"
COMPOSE_DHT_FILE="$TESTBED_DIR/docker-compose.dht.yml"
BINARY="$REPO_ROOT/dist/swartznet-testbed-linux-amd64"
GO="${GO:-/usr/local/go/bin/go}"

# ── Helper utilities ──────────────────────────────────────────────────────────

log()  { echo "[run-testbed] $*"; }
fail() { echo "[run-testbed] ERROR: $*" >&2; exit 1; }

# ── Preflight checks ──────────────────────────────────────────────────────────

if [[ $# -lt 1 ]]; then
    echo "Usage: $0 <scenario> [--json=<path>]" >&2
    exit 2
fi

SCENARIO="$1"
shift

JSON_OUT=""
for arg in "$@"; do
    case "$arg" in
        --json=*) JSON_OUT="${arg#--json=}" ;;
        *) echo "Unknown flag: $arg" >&2; exit 2 ;;
    esac
done

# Validate scenario argument.
case "$SCENARIO" in
    s1|s2|s3|s4|s5|s6|s7|s8|s9|s10|s11|s12|swarm|all) ;;
    *) fail "Unknown scenario '$SCENARIO'. Valid: s1 s2 s3 s4 s5 s6 s7 s8 s9 s10 s11 s12 swarm all" ;;
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

# Build the vanilla anacrolix/torrent CLI binary if absent. This
# is a separate binary used only by s11 (vanilla-leech container).
# Its deps (tagflag, prometheus/client_golang) aren't in our
# go.mod, so we build it in a throwaway temp module to avoid
# polluting the project's module graph. We only rebuild if the
# file is missing — there's no source in this repo that would
# invalidate it.
VANILLA_BIN="$REPO_ROOT/dist/torrent-vanilla-linux-amd64"
if [[ ! -f "$VANILLA_BIN" ]]; then
    log "Vanilla binary not found: $VANILLA_BIN"
    log "Building github.com/anacrolix/torrent/cmd/torrent@v1.61.0 in a temp module..."
    VANILLA_TMP=$(mktemp -d /tmp/sn-vanilla-build-XXXXXX)
    (
        cd "$VANILLA_TMP"
        "$GO" mod init tempvanilla > /dev/null
        GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
            "$GO" install github.com/anacrolix/torrent/cmd/torrent@v1.61.0
    ) || fail "vanilla build failed"
    cp "$HOME/go/bin/torrent" "$VANILLA_BIN" || fail "could not stage $VANILLA_BIN"
    rm -rf "$VANILLA_TMP"
    log "Built: $VANILLA_BIN ($(du -h "$VANILLA_BIN" | cut -f1))"
fi

mkdir -p "$RESULTS_DIR"

# ── Per-scenario config ───────────────────────────────────────────────────────

scenario_netem_profile() {
    case "$1" in
        s1) echo "" ;;                         # no netem
        s2) echo "/netem/lossy.sh" ;;
        s3) echo "/netem/mobile-4g.sh" ;;
        s4) echo "/netem/home-dsl.sh" ;;
        s5) echo "" ;;                         # piece transfer, no netem
        s6|s7) echo "" ;;                      # 6-node swarm, no netem
        s8) echo "/netem/lossy.sh" ;;          # 6-node swarm under lossy
        s9) echo "" ;;                         # pass-along, no netem
        s10) echo "/netem/lossy.sh" ;;         # mid-transfer churn under lossy
        s11) echo "" ;;                        # vanilla interop, no netem
        s12) echo "" ;;                        # Layer-D / DHT, no netem
    esac
}

# Which compose file and container set does this scenario use?
scenario_compose_file() {
    case "$1" in
        s1|s2|s3|s4|s5)       echo "$COMPOSE_FILE" ;;
        s6|s7|s8|s9|s10|s11)  echo "$COMPOSE_SWARM_FILE" ;;
        s12)                  echo "$COMPOSE_DHT_FILE" ;;
    esac
}

scenario_containers() {
    case "$1" in
        s1|s2|s3|s4|s5)
            echo "sn-seed-1 sn-seed-2 sn-leech-1" ;;
        s6|s7|s8|s9|s10|s11)
            # s9 starts with the baseline 6 and later brings up
            # sn-swarm-leech-5 via the `late-joiner` profile from
            # inside the scenario script; s10 kills sn-swarm-seed-1
            # mid-scenario; s11 brings up sn-swarm-vanilla-leech
            # via the `vanilla` profile. The startup wait here
            # only checks the baseline six; teardown
            # (`--profile '*' compose down -v`) covers
            # profile-started services too.
            echo "sn-swarm-seed-1 sn-swarm-seed-2 sn-swarm-leech-1 sn-swarm-leech-2 sn-swarm-leech-3 sn-swarm-leech-4" ;;
        s12)
            echo "sn-dht-seed-1 sn-dht-seed-2 sn-dht-leech-1 sn-dht-leech-2 sn-dht-leech-3 sn-dht-leech-4" ;;
    esac
}

scenario_script() {
    echo "$SCENARIOS_DIR/$1-"*".sh"
}

# ── Run one scenario ──────────────────────────────────────────────────────────

run_scenario() {
    # Args:  sc=<scenario-name>  [extra_scripts=<space-separated extra scripts
    #        to run against the same live stack after the primary one>]
    local sc="$1"
    local -a EXTRA_SCRIPTS=()
    if [[ $# -gt 1 ]]; then
        # Shift past sc; remaining args are extra scenario names whose
        # scripts run against the same stack. Used by the `swarm` alias
        # to fold s6+s7 into one compose lifecycle.
        shift
        EXTRA_SCRIPTS=("$@")
    fi

    local ts
    ts=$(date +%Y%m%d-%H%M%S)
    local logfile="$RESULTS_DIR/$sc-$ts.log"
    local netem_profile
    netem_profile=$(scenario_netem_profile "$sc")
    local compose_file
    compose_file=$(scenario_compose_file "$sc")
    [[ -f "$compose_file" ]] || fail "Compose file not found for $sc: $compose_file"

    local script_glob="$SCENARIOS_DIR/${sc}-*.sh"
    local script
    script=$(echo $script_glob)   # expand glob (one match expected)
    if [[ ! -f "$script" ]]; then
        fail "Scenario script not found: $script_glob"
    fi

    log "=== Running $sc (log: $logfile) ==="
    log "    compose: $compose_file"
    [[ -n "$netem_profile" ]] && log "    netem: $netem_profile" || log "    netem: none"

    # Tear down the stack on EXIT (success or failure).
    local flag_file
    flag_file=$(mktemp /tmp/sn-teardown-XXXXXX)
    rm -f "$flag_file"   # file absent = teardown not yet done

    _do_teardown() {
        if [[ ! -f "$flag_file" ]]; then
            touch "$flag_file"
            log "Tearing down docker compose stack..."
            # Include every profile defined in the compose file so
            # any service gated behind a profile (s9's leech-5) also
            # gets stopped and removed. Without this, `down -v`
            # leaves profile-gated containers running, which holds
            # the bridge network and logs "Resource is still in
            # use" during network removal.
            NETEM_PROFILE="$netem_profile" \
                docker compose -f "$compose_file" \
                --profile '*' down -v 2>&1 | \
                tee -a "$logfile" || true
        fi
    }
    trap _do_teardown EXIT

    # Write scenario header to log.
    {
        echo "=== run-testbed: scenario=$sc ts=$ts ==="
        echo "    compose=$compose_file"
        echo "    netem=$netem_profile"
        if [[ ${#EXTRA_SCRIPTS[@]} -gt 0 ]]; then
            echo "    extra scenarios against same stack: ${EXTRA_SCRIPTS[*]}"
        fi
        echo ""
    } | tee "$logfile"

    # Start the stack.
    log "Starting docker compose (netem=${netem_profile:-none})..."
    NETEM_PROFILE="$netem_profile" \
        docker compose -f "$compose_file" up --build -d 2>&1 | tee -a "$logfile"

    # Wait for all containers to be in "running" state.
    local containers_str
    containers_str=$(scenario_containers "$sc")
    # shellcheck disable=SC2206
    local CONTAINERS=($containers_str)
    local WAIT_SECS=120
    local deadline=$(( $(date +%s) + WAIT_SECS ))
    log "Waiting up to ${WAIT_SECS}s for ${#CONTAINERS[@]} containers to be running..."
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
            log "All ${#CONTAINERS[@]} containers running."
            break
        fi
        if [[ "$(date +%s)" -ge "$deadline" ]]; then
            log "ERROR: Containers not running after ${WAIT_SECS}s" | tee -a "$logfile"
            docker compose -f "$compose_file" ps 2>&1 | tee -a "$logfile"
            docker compose -f "$compose_file" logs 2>&1 | tail -50 | tee -a "$logfile"
            _do_teardown
            trap - EXIT
            rm -f "$flag_file"
            return 1
        fi
        sleep 2
    done

    # Run the primary assertion script.
    log "Running assertion script: $script"
    local assertion_exit=0
    bash "$script" 2>&1 | tee -a "$logfile" || assertion_exit=$?

    # Run any extra scripts against the same live stack (used by the
    # swarm alias so s6+s7 share one `compose up`). If the primary
    # script failed we still try the extras so the log captures
    # everything, but the final exit is the max of all codes.
    for extra in "${EXTRA_SCRIPTS[@]}"; do
        local extra_script
        extra_script=$(echo "$SCENARIOS_DIR/${extra}-"*".sh")
        if [[ ! -f "$extra_script" ]]; then
            log "WARN: extra scenario script not found: $extra_script"
            continue
        fi
        log "Running extra assertion script (same stack): $extra_script"
        local extra_exit=0
        bash "$extra_script" 2>&1 | tee -a "$logfile" || extra_exit=$?
        if [[ "$extra_exit" -ne 0 ]]; then
            assertion_exit="$extra_exit"
        fi
    done

    _do_teardown
    trap - EXIT
    rm -f "$flag_file"

    return "$assertion_exit"
}

# ── Main dispatch ─────────────────────────────────────────────────────────────

SCENARIOS_TO_RUN=()
case "$SCENARIO" in
    # s12 is deliberately NOT in `all` — the DHT put path times
    # out in the 6-node private DHT (post-investigation note in
    # testbed/scenarios/s12-swarm-dht.sh). The scenario +
    # docker-compose.dht.yml are kept in-tree so a future loop
    # can continue the investigation, and so the gossip-caps
    # fix in engine.startPublisher has a real end-to-end target.
    all)    SCENARIOS_TO_RUN=(s1 s2 s3 s4 s5 swarm s8 s9 s10 s11) ;;
    swarm)  SCENARIOS_TO_RUN=(swarm) ;;
    *)      SCENARIOS_TO_RUN=("$SCENARIO") ;;
esac

declare -A RESULTS
declare -A DURATIONS        # scenario → wall-clock duration in seconds
declare -A NETEM_USED       # scenario → netem profile that was applied
OVERALL_EXIT=0
WALL_START=$(date +%s)

for sc in "${SCENARIOS_TO_RUN[@]}"; do
    sc_start=$(date +%s)
    # `swarm` is an alias: run s6's compose stack and execute the s6
    # and s7 assertion scripts back-to-back against it.
    if [[ "$sc" == "swarm" ]]; then
        if run_scenario "s6" "s7"; then
            RESULTS["swarm"]="PASS"
            log "swarm (s6+s7): PASS ($(( $(date +%s) - sc_start ))s)"
        else
            RESULTS["swarm"]="FAIL"
            OVERALL_EXIT=1
            log "swarm (s6+s7): FAIL ($(( $(date +%s) - sc_start ))s)"
        fi
        DURATIONS["swarm"]=$(( $(date +%s) - sc_start ))
        NETEM_USED["swarm"]=""
        if [[ "${#SCENARIOS_TO_RUN[@]}" -gt 1 ]]; then
            sleep 3
        fi
        continue
    fi
    if run_scenario "$sc"; then
        RESULTS["$sc"]="PASS"
        log "$sc: PASS ($(( $(date +%s) - sc_start ))s)"
    else
        RESULTS["$sc"]="FAIL"
        OVERALL_EXIT=1
        log "$sc: FAIL ($(( $(date +%s) - sc_start ))s)"
    fi
    DURATIONS["$sc"]=$(( $(date +%s) - sc_start ))
    NETEM_USED["$sc"]=$(scenario_netem_profile "$sc")
    # Allow a brief settle between scenarios to avoid port conflicts.
    if [[ "${#SCENARIOS_TO_RUN[@]}" -gt 1 ]]; then
        sleep 3
    fi
done

WALL_TOTAL=$(( $(date +%s) - WALL_START ))

# ── Scoreboard ────────────────────────────────────────────────────────────────
#
# Per-scenario timing is surfaced here (in addition to the per-run
# log line) so flakes and slow trends are visible at a glance
# without having to read the log file.

echo ""
echo "╔════════════════════════════════════════════════╗"
echo "║            Testbed scenario results            ║"
echo "╠════════════════════════════════════════════════╣"
printf "║  %-8s  %-8s  %-24s  ║\n" "SCENARIO" "RESULT" "DURATION"
echo "╠════════════════════════════════════════════════╣"
for sc in "${SCENARIOS_TO_RUN[@]}"; do
    result="${RESULTS[$sc]:-SKIP}"
    dur="${DURATIONS[$sc]:--}"
    if [[ "$dur" != "-" ]]; then
        dur="${dur}s"
    fi
    printf "║  %-8s  %-8s  %-24s  ║\n" "$sc" "$result" "$dur"
done
echo "╠════════════════════════════════════════════════╣"
printf "║  total wall clock: %-28s  ║\n" "${WALL_TOTAL}s"
echo "╚════════════════════════════════════════════════╝"

# ── Optional JSON summary ────────────────────────────────────────────────────
#
# Emitted only when --json=<path> was supplied. Format is a
# single object with a top-level "scenarios" array suitable for
# consumption by CI status bots, perf-regression dashboards, and
# git-logged trending analysis. Durations are integer seconds.

if [[ -n "$JSON_OUT" ]]; then
    # Export per-scenario fields into the environment so the
    # python emitter can read them by key. Bash associative
    # arrays don't marshal into python arg lists cleanly; env
    # vars are the simplest portable handoff. Keys are
    # name-mangled with an SN_ prefix to avoid shell collisions.
    for sc in "${SCENARIOS_TO_RUN[@]}"; do
        export "SN_RES_${sc}=${RESULTS[$sc]:-SKIP}"
        export "SN_DUR_${sc}=${DURATIONS[$sc]:-0}"
        export "SN_NET_${sc}=${NETEM_USED[$sc]:-}"
    done

    # Emit JSON via python so strings are properly quoted/escaped
    # (bash `printf %q` produces shell-escape form, not JSON).
    # python3 is already a hard dependency of every scenario
    # script, so this adds no new build requirement.
    python3 - "$WALL_START" "$WALL_TOTAL" "$OVERALL_EXIT" "$JSON_OUT" "${SCENARIOS_TO_RUN[@]}" <<'PY'
import json, os, sys

started   = int(sys.argv[1])
total     = int(sys.argv[2])
overall   = int(sys.argv[3])
out_path  = sys.argv[4]
scenarios = sys.argv[5:]

out = {
    "started_at":         started,
    "finished_at":        started + total,
    "total_wall_clock_s": total,
    "overall_exit":       overall,
    "scenarios": [
        {
            "name":          sc,
            "result":        os.environ.get("SN_RES_" + sc, "SKIP"),
            "duration_s":    int(os.environ.get("SN_DUR_" + sc, "0")),
            "netem_profile": os.environ.get("SN_NET_" + sc, ""),
        }
        for sc in scenarios
    ],
}
with open(out_path, "w") as f:
    json.dump(out, f, indent=2)
    f.write("\n")
PY
    log "Wrote JSON summary to $JSON_OUT"
fi
echo ""
echo "Logs: $RESULTS_DIR/"
echo ""

exit "$OVERALL_EXIT"
