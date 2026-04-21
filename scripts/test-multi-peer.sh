#!/usr/bin/env bash
# test-multi-peer.sh — 1-seeder + 3-peer loopback scenario against
# real `dist/swartznet` binaries.
#
# The in-process testlab scenarios cover peer-wire handler logic;
# this script is the first end-to-end flow that drives the CLI
# binary as a user would: four separate daemon processes on
# loopback, wired peer-to-peer via `x.pe=` in the magnet URI,
# no DHT, no tracker. It asserts:
#
#   1. The seeder comes up and hashes its prepopulated payload
#      as complete.
#   2. Each of three peer daemons, given only a magnet URI that
#      includes the seeder's listen address, fetches metadata
#      (BEP-9), downloads all pieces (BEP-3), and verifies.
#   3. All three peers' on-disk copies are byte-identical to the
#      original source tree (SHA256 match, file count match).
#   4. The local search index on each peer indexes the extracted
#      text and a canonical keyword returns a non-empty hit set.
#
# Failure modes that this harness catches that in-process tests
# miss: CLI flag parsing regressions, XDG path resolution bugs,
# HTTP-API boot order races, magnet-URI parsing of x.pe peer
# addresses, and any defect that only manifests when the engine
# runs in its default production configuration.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GO="/usr/local/go/bin/go"
BIN="${REPO_ROOT}/dist/swartznet"
TESTDIR="${REPO_ROOT}/tests/torrent-test"
SRC="${TESTDIR}/source"
RESULTS="${TESTDIR}/results"
LOGS="${RESULTS}/logs"

# Deterministic ports + API addresses. We pick a high range that
# doesn't overlap swartznet's default (42069) or the user's real
# daemon API (7654) so running the harness while a real daemon is
# up doesn't collide.
SEED_PORT=42170
PEER_PORTS=(42171 42172 42173)
SEED_API=localhost:17650
PEER_APIS=(localhost:17651 localhost:17652 localhost:17653)

# Tunables — the transfer is ~300 KiB over loopback so everything
# should finish in well under 20 s; the 60 s cap is for diagnosing
# a broken run, not a real budget.
OVERALL_TIMEOUT=${OVERALL_TIMEOUT:-60}
POLL_INTERVAL=0.25

PIDS=()
STATUS=0

log()  { printf '[%s] %s\n' "$(date +%H:%M:%S)" "$*"; }
fail() { log "FAIL: $*"; STATUS=1; }

cleanup() {
    log "cleanup: stopping daemons"
    for pid in "${PIDS[@]}"; do
        if kill -0 "${pid}" 2>/dev/null; then
            kill -TERM "${pid}" 2>/dev/null || true
        fi
    done
    # Give daemons 3 s to exit cleanly, then SIGKILL stragglers.
    # anacrolix's client.Close can take a moment to flush listen
    # sockets so we don't want to trigger a false race.
    for _ in 1 2 3 4 5 6; do
        still=0
        for pid in "${PIDS[@]}"; do
            if kill -0 "${pid}" 2>/dev/null; then still=1; fi
        done
        [ "${still}" = 0 ] && break
        sleep 0.5
    done
    for pid in "${PIDS[@]}"; do
        if kill -0 "${pid}" 2>/dev/null; then
            kill -KILL "${pid}" 2>/dev/null || true
        fi
    done
}
trap cleanup EXIT

# -------------------------------------------------------------
# 0. Preconditions
# -------------------------------------------------------------
if [ ! -d "${SRC}" ] || [ -z "$(ls -A "${SRC}" 2>/dev/null)" ]; then
    log "source/ is empty — running gen-test-fixture.sh"
    "${REPO_ROOT}/scripts/gen-test-fixture.sh" > /dev/null
fi

command -v curl    >/dev/null || { log "curl not installed";    exit 2; }
command -v python3 >/dev/null || { log "python3 not installed"; exit 2; }

# j <json-string> <python-expr-over-d>  →  print expr result
# A minimal stand-in for `jq` — we don't want to force jq onto
# every dev box, and python3 ships everywhere we care about. `d`
# is the parsed JSON root.
j() {
    python3 -c "import sys,json
try:
    d = json.load(sys.stdin)
except Exception:
    d = None
try:
    v = ($2)
except Exception:
    v = ''
print('' if v is None else v)" <<<"$1"
}

mkdir -p "${LOGS}"
# Clean previous run artifacts but keep the LOGS dir so we can
# diff successive runs.
rm -rf "${TESTDIR}/seeder-data" "${TESTDIR}/seeder-index"
for i in 1 2 3; do
    rm -rf "${TESTDIR}/peer${i}-data" "${TESTDIR}/peer${i}-index"
done
rm -f  "${LOGS}"/*.log "${RESULTS}/report.txt" "${RESULTS}/.torrent" 2>/dev/null || true

# -------------------------------------------------------------
# 1. Build. One authoritative binary for all four daemons so a
#    version-skew bug can't creep in mid-harness.
# -------------------------------------------------------------
log "build: ${BIN}"
(cd "${REPO_ROOT}" && CGO_ENABLED=0 "${GO}" build -o "${BIN}" ./cmd/swartznet)

# -------------------------------------------------------------
# 2. Create the .torrent from source/ and stage it under the
#    seeder's data dir so anacrolix's piece-verification finds
#    the pre-existing payload and marks every piece complete.
# -------------------------------------------------------------
mkdir -p "${TESTDIR}/seeder-data"
# Our engine lays out the payload as <DataDir>/<torrent-root>/...
# so the torrent root directory ("source") must exist under the
# seeder's DataDir. Symlinking keeps SRC as the single canonical
# copy; anacrolix reads through symlinks without issue.
if [ ! -e "${TESTDIR}/seeder-data/source" ]; then
    ln -s "${SRC}" "${TESTDIR}/seeder-data/source"
fi

TORRENT="${RESULTS}/multi-peer.torrent"
mkdir -p "${RESULTS}"
log "create: ${TORRENT}"
# Go's flag package stops at the first non-flag token, so the
# <root> positional arg must come after -o / --comment.
"${BIN}" create \
    -o "${TORRENT}" \
    --comment "swartznet multi-peer test" \
    "${SRC}" \
    > "${LOGS}/create.log" 2>&1

# -------------------------------------------------------------
# 3. Boot the seeder. --no-dht keeps traffic hermetic;
#    --leech-only is NOT set (seeders serve).
# -------------------------------------------------------------
log "seeder: starting on port ${SEED_PORT} / api ${SEED_API}"
# Flags before the positional <magnet|.torrent> arg — Go's flag
# package stops parsing at the first non-flag token.
"${BIN}" add \
    --port "${SEED_PORT}" \
    --api-addr "${SEED_API}" \
    --data-dir "${TESTDIR}/seeder-data" \
    --index-dir "${TESTDIR}/seeder-index" \
    --no-dht \
    "${TORRENT}" \
    > "${LOGS}/seeder.log" 2>&1 &
PIDS+=($!)

# wait_http <base-url> <timeout-s>
wait_http() {
    local base="$1" budget="$2"
    local start; start=$(date +%s)
    while :; do
        if curl -fsS "${base}/healthz" >/dev/null 2>&1; then
            return 0
        fi
        if [ $(( $(date +%s) - start )) -ge "${budget}" ]; then
            return 1
        fi
        sleep "${POLL_INTERVAL}"
    done
}

log "seeder: waiting for /healthz"
if ! wait_http "http://${SEED_API}" 15; then
    fail "seeder /healthz never came up"
    cat "${LOGS}/seeder.log" || true
    exit 1
fi

# Poll until the seeder reports its single torrent with progress
# == 1 (anacrolix hashes lazily; the seeder is not serving until
# it has verified every piece).
log "seeder: waiting for complete-hash of source/"
start=$(date +%s)
while :; do
    snap=$(curl -fsS "http://${SEED_API}/torrents" 2>/dev/null || echo '{}')
    progress=$(j "${snap}" 'd["torrents"][0].get("progress",0) if d and d.get("torrents") else 0')
    size=$(j     "${snap}" 'd["torrents"][0].get("size",0)     if d and d.get("torrents") else 0')
    if awk -v p="${progress}" 'BEGIN{exit !(p+0 >= 0.999)}' && [ "${size}" != "0" ]; then
        break
    fi
    if [ $(( $(date +%s) - start )) -ge 30 ]; then
        fail "seeder never verified source: ${snap}"
        exit 1
    fi
    sleep "${POLL_INTERVAL}"
done
snap=$(curl -fsS "http://${SEED_API}/torrents")
IH=$(j   "${snap}" 'd["torrents"][0]["infohash"]')
NAME=$(j "${snap}" 'd["torrents"][0]["name"]')
log "seeder: infohash=${IH} name=${NAME}"

# Build a magnet URI that includes the seeder's listen address as
# an x.pe peer hint. With --no-dht on every node this is the only
# way the peers learn of the seeder.
NAME_ENC=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote(sys.argv[1], safe=""))' "${NAME}")
MAGNET="magnet:?xt=urn:btih:${IH}&dn=${NAME_ENC}&x.pe=127.0.0.1:${SEED_PORT}"
log "seeder: magnet=${MAGNET}"

# -------------------------------------------------------------
# 4. Launch three peer daemons, each with its own isolated
#    data/index directory and unique ports.
# -------------------------------------------------------------
for i in 0 1 2; do
    idx=$((i + 1))
    port=${PEER_PORTS[$i]}
    api=${PEER_APIS[$i]}
    log "peer${idx}: starting on port ${port} / api ${api}"
    "${BIN}" add \
        --port "${port}" \
        --api-addr "${api}" \
        --data-dir "${TESTDIR}/peer${idx}-data" \
        --index-dir "${TESTDIR}/peer${idx}-index" \
        --no-dht \
        "${MAGNET}" \
        > "${LOGS}/peer${idx}.log" 2>&1 &
    PIDS+=($!)
done

for i in 0 1 2; do
    idx=$((i + 1))
    api=${PEER_APIS[$i]}
    if ! wait_http "http://${api}" 15; then
        fail "peer${idx} /healthz never came up"
        cat "${LOGS}/peer${idx}.log" || true
        exit 1
    fi
done

# -------------------------------------------------------------
# 5. Wait for all peers to finish downloading.
# -------------------------------------------------------------
log "peers: waiting for all three to complete (budget ${OVERALL_TIMEOUT}s)"
start=$(date +%s)
while :; do
    done_count=0
    dump=""
    for i in 0 1 2; do
        idx=$((i + 1))
        api=${PEER_APIS[$i]}
        snap=$(curl -fsS "http://${api}/torrents" 2>/dev/null || echo '{}')
        progress=$(j "${snap}" 'd["torrents"][0].get("progress",0)       if d and d.get("torrents") else 0')
        bc=$(j       "${snap}" 'd["torrents"][0].get("bytes_completed",0) if d and d.get("torrents") else 0')
        peers=$(j    "${snap}" 'd["torrents"][0].get("active_peers",0)    if d and d.get("torrents") else 0')
        dump+=$'\t'"peer${idx}: progress=${progress} bc=${bc} active_peers=${peers}"$'\n'
        if awk -v p="${progress}" 'BEGIN{exit !(p+0 >= 0.999)}'; then
            done_count=$((done_count + 1))
        fi
    done
    if [ "${done_count}" = 3 ]; then
        log "peers: all complete"
        break
    fi
    if [ $(( $(date +%s) - start )) -ge "${OVERALL_TIMEOUT}" ]; then
        fail "peers did not complete within ${OVERALL_TIMEOUT}s"
        printf '%s\n' "${dump}"
        exit 1
    fi
    sleep "${POLL_INTERVAL}"
done

# -------------------------------------------------------------
# 6. Integrity check: sha256 the full tree under each peer's
#    data dir and diff against source/. anacrolix already verified
#    piece hashes, but this catches file-storage layout bugs
#    (wrong DataDir, wrong filename, stray partial files).
# -------------------------------------------------------------
src_manifest=$(cd "${SRC}" && find . -type f -print0 | xargs -0 sha256sum | sort -k2)
src_hash=$(printf '%s\n' "${src_manifest}" | sha256sum | awk '{print $1}')
log "integrity: source manifest sha256=${src_hash}"

for idx in 1 2 3; do
    peer_root="${TESTDIR}/peer${idx}-data/source"
    if [ ! -d "${peer_root}" ]; then
        fail "peer${idx}: source/ missing under data-dir"
        continue
    fi
    peer_manifest=$(cd "${peer_root}" && find . -type f -print0 | xargs -0 sha256sum | sort -k2)
    peer_hash=$(printf '%s\n' "${peer_manifest}" | sha256sum | awk '{print $1}')
    if [ "${peer_hash}" != "${src_hash}" ]; then
        fail "peer${idx}: manifest mismatch (got=${peer_hash} want=${src_hash})"
        diff <(printf '%s\n' "${src_manifest}") <(printf '%s\n' "${peer_manifest}") \
            | head -40 || true
    else
        log "integrity: peer${idx} manifest matches source"
    fi
done

# -------------------------------------------------------------
# 7. Search smoke: each peer should have indexed the .txt/.info
#    files. The gen-test-fixture planter drops the word "photon"
#    into every file under q-science/ (across marker+filler) so
#    a search for photon on any peer must return hits.
# -------------------------------------------------------------
for i in 0 1 2; do
    idx=$((i + 1))
    api=${PEER_APIS[$i]}
    # Give the indexer a moment to pick up newly-completed files.
    # The pipeline ingests on piece-complete events which arrive
    # before our integrity-check reads the on-disk tree, but the
    # handle-to-index chain is batched on a ticker.
    for _ in $(seq 1 40); do
        # The HTTP API's SearchRequest uses field "q" (not "query")
        # and the response is {local:{total,hits:[...]}, swarm:..., dht:...}.
        hits=$(curl -fsS -XPOST -H 'Content-Type: application/json' \
            -d '{"q":"photon","limit":20}' "http://${api}/search" \
            2>/dev/null || echo '{}')
        count=$(j "${hits}" 'len(d.get("local",{}).get("hits",[])) if d else 0')
        if [ "${count:-0}" -gt 0 ]; then break; fi
        sleep 0.25
    done
    if [ "${count}" -gt 0 ]; then
        log "search: peer${idx} indexed 'photon' hits=${count}"
    else
        fail "peer${idx} search for 'photon' returned no hits"
    fi
done

# -------------------------------------------------------------
# 8. Final report
# -------------------------------------------------------------
{
    echo "swartznet multi-peer scenario report"
    echo "====================================="
    date
    echo "infohash: ${IH}"
    echo "magnet:   ${MAGNET}"
    echo
    echo "peers:"
    for i in 0 1 2; do
        idx=$((i + 1))
        api=${PEER_APIS[$i]}
        snap=$(curl -fsS "http://${api}/torrents" || echo '{}')
        line=$(j "${snap}" '"bytes_completed={} size={} active_peers={} progress={}".format(d["torrents"][0].get("bytes_completed",0), d["torrents"][0].get("size",0), d["torrents"][0].get("active_peers",0), d["torrents"][0].get("progress",0)) if d and d.get("torrents") else "no-torrent"')
        printf '  peer%d %s: %s\n' "${idx}" "${api}" "${line}"
    done
    echo
    echo "STATUS: $( [ "${STATUS}" = 0 ] && echo PASS || echo FAIL )"
} | tee "${RESULTS}/report.txt"

exit "${STATUS}"
