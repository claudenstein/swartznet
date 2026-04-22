#!/usr/bin/env bash
# gen-swarm-fixture.sh — generate the larger fixture used by the
# 6-node Layer-B swarm scenarios (s6, s7).
#
# The canonical 3-KiB testbed fixture (testbed/fixture/) is too
# small for the swarm scenarios: loopback transfers complete in
# well under a second, so by the time an assertion samples
# `active_peers` or `capable_peers` the BT connections have
# already torn down (peers lose interest once everyone has the
# single piece). The swarm fixture targets ~4 MB across 8 files
# so on healthy loopback the window in which peers are actively
# connected stretches to several seconds — long enough for
# assertions to observe PEX propagation and the sn_search LTEP
# handshake.
#
# Output:
#   testbed/fixture-swarm/
#     content/testbed-swarm-corpus/*.txt     8 deterministic .txt files
#     fixture.torrent                         pre-generated torrent
#     INFOHASH                                hex infohash of fixture.torrent
#
# The torrent is built with a 64-KiB piece size so progress updates
# are granular enough to catch during the transfer window.
#
# Re-runnable: wipes content/, regenerates bytes, rebuilds torrent.
# Keeps identical bytes across runs because the generator uses a
# fixed-seed LCG (no /dev/urandom).

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
GO="${GO:-/usr/local/go/bin/go}"
BIN="${REPO_ROOT}/dist/swartznet"
FIXTURE_ROOT="${REPO_ROOT}/testbed/fixture-swarm"
CONTENT_ROOT="${FIXTURE_ROOT}/content/testbed-swarm-corpus"

# Total size target — 8 chapters × 512 KiB each = 4 MiB payload.
# With 64-KiB piece size that's 64 pieces, enough to see progress
# tick while the transfer runs.
NUM_CHAPTERS=8
CHAPTER_BYTES=524288
PIECE_KIB=64

# Marker that every chapter carries — the s7 swarm-search
# assertion fires with this query and expects hits from seeds.
# Matches the marker used by the small fixture so assertions can
# share strings.
MARKER="aethergram"

echo "gen-swarm-fixture: FIXTURE_ROOT=${FIXTURE_ROOT}"
rm -rf "${FIXTURE_ROOT}"
mkdir -p "${CONTENT_ROOT}"

# Deterministic per-chapter filler. LCG over a per-chapter seed
# with word replacements from a small dictionary so the output is
# text-indexable (Bleve extractors ignore binary noise).
generate_chapter() {
    local idx="$1" out="$2"
    python3 - "$idx" "$CHAPTER_BYTES" "$MARKER" "$out" <<'PY'
import sys, random
idx = int(sys.argv[1])
n_target = int(sys.argv[2])
marker = sys.argv[3]
out = sys.argv[4]

# Fixed seed → byte-identical output across runs.
r = random.Random(0xBEEF0000 + idx)

words = [
    "aether", "prism", "meridian", "signal", "lattice", "orbit",
    "corpus", "lexicon", "sigil", "rune", "chord", "aria",
    "forge", "kindle", "ember", "glyph", "vellum", "atlas",
    "photon", "quantum", "aurora", "mirth", "solace", "helix",
]

with open(out, "w") as f:
    # Preamble with the marker. Indexers read the first ~1 KiB
    # preferentially so we keep the marker near the top.
    f.write(f"# Chapter {idx} — the {marker} transmissions\n")
    f.write(f"Marker: {marker}\n")
    f.write(f"Chapter: {idx}\n")
    f.write("---\n\n")
    written = f.tell()
    # Pseudo-prose filler. Short sentences so no single line gets
    # huge. We also sprinkle the marker occasionally so the search
    # query has multiple hit positions per chapter.
    while written < n_target:
        sentence_len = r.randint(8, 18)
        parts = [r.choice(words) for _ in range(sentence_len)]
        if r.random() < 0.1:
            parts.insert(r.randint(0, len(parts)), marker)
        line = " ".join(parts).capitalize() + ".\n"
        if written + len(line) > n_target:
            line = line[: n_target - written]
        f.write(line)
        written += len(line)
PY
}

for i in $(seq 1 "$NUM_CHAPTERS"); do
    out="${CONTENT_ROOT}/chapter-$(printf '%02d' "$i").txt"
    generate_chapter "$i" "$out"
done

total_bytes=$(find "${CONTENT_ROOT}" -type f -printf '%s\n' | awk '{s+=$1} END {print s}')
echo "gen-swarm-fixture: wrote ${NUM_CHAPTERS} chapters, total $(numfmt --to=iec-i --suffix=B "${total_bytes}")"

# Build the swartznet binary if absent so we can run `swartznet create`.
if [ ! -x "${BIN}" ]; then
    echo "gen-swarm-fixture: building ${BIN}"
    (cd "${REPO_ROOT}" && CGO_ENABLED=0 "${GO}" build -o "${BIN}" ./cmd/swartznet)
fi

# Create the torrent with a fixed piece size. --comment is
# cosmetic; the infohash depends only on the piece bytes and the
# file tree layout.
TORRENT="${FIXTURE_ROOT}/fixture.torrent"
echo "gen-swarm-fixture: creating ${TORRENT}"
"${BIN}" create \
    -o "${TORRENT}" \
    --piece-kib "${PIECE_KIB}" \
    --comment "swartznet Layer-B 6-node swarm fixture" \
    "${CONTENT_ROOT}"

# Extract the infohash and persist it so scenario scripts can
# read it without re-parsing the .torrent.
IH=$(python3 - "${TORRENT}" <<'PY'
import sys, hashlib, re
# Minimal bencode parser: we only need info-dict bounds.
data = open(sys.argv[1], "rb").read()
# Find the start of the info dict value.
key = b"4:infod"
i = data.find(key)
if i < 0:
    raise SystemExit("infodict not found")
start = i + len(key) - 1   # back up to the 'd' start-of-dict byte
# Parse bencode from `start` to find the matching 'e'.
def parse(buf, p):
    t = buf[p:p+1]
    if t.isdigit():
        colon = buf.index(b":", p)
        ln = int(buf[p:colon])
        return colon + 1 + ln
    if t == b"i":
        e = buf.index(b"e", p)
        return e + 1
    if t in (b"l", b"d"):
        p += 1
        while buf[p:p+1] != b"e":
            if t == b"d":
                p = parse(buf, p)   # key
            p = parse(buf, p)       # value
        return p + 1
    raise SystemExit(f"bad bencode at {p}: {t}")
end = parse(data, start)
info_bytes = data[start:end]
print(hashlib.sha1(info_bytes).hexdigest())
PY
)
echo "${IH}" > "${FIXTURE_ROOT}/INFOHASH"
echo "gen-swarm-fixture: infohash=${IH}"
echo "gen-swarm-fixture: wrote ${FIXTURE_ROOT}/INFOHASH"
