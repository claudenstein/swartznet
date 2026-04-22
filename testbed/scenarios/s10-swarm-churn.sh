#!/usr/bin/env bash
# Scenario S10: mid-transfer seed churn.
#
# Precondition: docker compose running on docker-compose.swarm.yml
# with NETEM_PROFILE=/netem/lossy.sh. scripts/run-testbed.sh s10
# handles lifecycle.
#
# This is the only scenario in the matrix that kills a seed *while*
# transfers are actively in flight, as opposed to after completion
# (s9) or never (s6/s8). The distinguishing shape is:
#
#   - Lossy netem stretches the 4-MiB transfer window to ~15-20s,
#     so there is a real mid-flight window to trigger churn in.
#   - We wait for leech-1 to cross progress >= 0.3 (some pieces
#     already fetched) before stopping sn-swarm-seed-1. The check
#     fires from a prober on leech-1 itself, so we observe the
#     same counter the seed-killer cares about.
#   - seed-2 is deliberately kept alive — we are testing the
#     single-seed-failure path, not the zero-seed path (s9 covers
#     that end state). The swarm should fail over transparently:
#     outbound BT connections to seed-1 drop, leeches' peer
#     managers replace them from the x.pe= hints + PEX.
#   - All 4 leeches must still reach progress=1.0 within 300s.
#     Byte-for-byte check on leech-1 at the end rules out silent
#     corruption introduced by retransmits across the churn.
#
# Probes via docker exec (UFW-independent), same as s6/s7/s8/s9.
#
# Exit 0 if all pass, 1 otherwise.

set -euo pipefail

# We use leech-1 as the prober so killing seed-1 mid-scenario
# doesn't take the prober with it. (s6/s7/s8 use seed-1; s9
# already switched to leech-1 for the same reason.)
PROBER="sn-swarm-leech-1"
LEECH_NAMES=("leech-1" "leech-2" "leech-3" "leech-4")
SEED_TO_KILL="sn-swarm-seed-1"

CHURN_TRIGGER_PROGRESS="0.3"   # kill seed-1 once leech-1 crosses this
BUDGET_TRIGGER=60              # give up waiting to cross trigger
BUDGET_CONVERGE=300            # budget for full convergence post-churn

FIXTURE_INFOHASH=$(cat "$(dirname "$0")/../fixture-swarm/INFOHASH" | tr -d '\n' | tr -d ' ')
FIXTURE_DIR="$(dirname "$0")/../fixture-swarm/content/testbed-swarm-corpus"

fail() { echo "FAIL: $1"; exit 1; }
pass() { echo "PASS: $1"; }

api_get() {
    docker exec "$PROBER" curl -sf --max-time 10 "http://$1:7654$2" 2>/dev/null || true
}

progress_for() {
    local body
    body=$(api_get "$1" "/torrents")
    echo "$body" | python3 -c "
import sys, json
try: d = json.load(sys.stdin)
except Exception: print('none'); sys.exit()
for t in d.get('torrents', []) or []:
    if t.get('infohash') == '$FIXTURE_INFOHASH':
        print(t.get('progress', 0)); sys.exit()
print('none')
" 2>/dev/null || echo none
}

wait_prober_ready() {
    for i in $(seq 1 90); do
        if docker exec "$PROBER" curl -sf --max-time 2 \
            "http://localhost:7654/healthz" > /dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    return 1
}

echo "=== S10: mid-transfer seed churn (lossy netem, infohash=$FIXTURE_INFOHASH) ==="
wait_prober_ready || fail "prober $PROBER never became ready"

# 1. Wait for leech-1 to cross the trigger progress.
echo "step 1/3: waiting for leech-1 to cross progress >= ${CHURN_TRIGGER_PROGRESS}"
start=$(date +%s)
crossed=0
last="0"
while true; do
    p=$(progress_for "leech-1")
    last="$p"
    # Compare numerically via awk.
    if awk -v p="${p:-0}" -v t="$CHURN_TRIGGER_PROGRESS" \
         'BEGIN{ exit !(p+0 >= t+0) }'; then
        crossed=1; break
    fi
    # If leech-1 already completed (fixture too small / transfer
    # too fast for the lossy profile to matter), there's no
    # "mid-transfer" window left — treat as a graceful skip.
    if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
        echo "WARN: leech-1 already at progress=1 before we could churn; treating as pass (no mid-transfer window)"
        echo "=== S10: no mid-transfer window available under this netem config; skipping churn ==="
        exit 0
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET_TRIGGER" ]; then
        fail "leech-1 never crossed progress=${CHURN_TRIGGER_PROGRESS} within ${BUDGET_TRIGGER}s (last=$last)"
    fi
    sleep 1
done
pass "leech-1 crossed progress=${CHURN_TRIGGER_PROGRESS} (actual=${last})"

# 2. Kill seed-1 while transfers are still in flight.
echo "step 2/3: stopping $SEED_TO_KILL mid-transfer"
docker stop "$SEED_TO_KILL" > /dev/null || fail "could not stop $SEED_TO_KILL"
pass "stopped $SEED_TO_KILL ($(date -u +%H:%M:%S))"

# 3. All 4 leeches reach progress=1 via seed-2 + mutual exchange.
echo "step 3/3: waiting for convergence post-churn (budget ${BUDGET_CONVERGE}s)"
start=$(date +%s)
while true; do
    done_count=0
    report=""
    for name in "${LEECH_NAMES[@]}"; do
        p=$(progress_for "$name")
        report+="${name}=${p} "
        if [ "$p" = "1" ] || [ "$p" = "1.0" ]; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "$done_count" -eq 4 ]; then
        pass "all 4 leeches converged post-churn in $(( $(date +%s) - start ))s"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "$BUDGET_CONVERGE" ]; then
        echo "--- progress at timeout: $report ---"
        fail "only $done_count/4 leeches converged within ${BUDGET_CONVERGE}s after losing $SEED_TO_KILL"
    fi
    sleep 3
done

# 4. Byte check on leech-1.
fixture_sums=$(cd "$FIXTURE_DIR" && sha256sum *.txt | sort)
leech1_sums=$(docker exec "sn-swarm-leech-1" sh -c "cd /data/testbed-swarm-corpus && sha256sum *.txt | sort" 2>/dev/null) \
    || fail "could not sha256 leech-1 files"
if [ "$leech1_sums" != "$fixture_sums" ]; then
    echo "--- leech-1 hashes ---"
    echo "$leech1_sums"
    echo "--- fixture hashes ---"
    echo "$fixture_sums"
    fail "leech-1 bytes diverged from fixture (retransmit-across-churn corruption)"
fi
pass "leech-1 bytes match fixture byte-for-byte (no retransmit corruption across churn)"

echo
echo "=== S10: all checks passed (4 leeches converged after seed-1 died mid-transfer) ==="
