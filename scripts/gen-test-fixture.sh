#!/usr/bin/env bash
# gen-test-fixture.sh — deterministic multi-peer test corpus
#
# Writes 15 files (~480 KiB total) to tests/torrent-test/source/,
# matching the profile described in tests/torrent-test/task-spec.txt
# (3 "books" * 5 file types). Content is synthetic but each file
# carries a unique, searchable marker line so downstream search
# assertions have something to look for.
#
# Re-runnable: nukes the existing source/ tree before writing. Do
# not point SRC at anything you want to keep.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="${REPO_ROOT}/tests/torrent-test/source"

# Three synthetic "books" — directory layout mirrors the task
# spec's Gutenberg-derived tree (subject/id/files) so the harness
# exercises nested paths, not a single flat directory.
BOOKS=(
    "q-science/38755:Quantum Lectures:photon"
    "h-social-sciences/62157:Public Policy Essays:polity"
    "d-world-history/62152:Civilizations Atlas:meridian"
)

# Five file extensions per book. Sizes are picked so the total
# lands near 485 KiB (the task-spec target) without any single
# file dominating.
FILES=(
    ".txt:30720"       # 30 KiB
    ".html.zip:8192"   #  8 KiB (we only ship a fake zip header + padding)
    ".info:1024"       #  1 KiB
    ".epub:32768"      # 32 KiB
    ".mobi:32768"      # 32 KiB
)

echo "gen-test-fixture: SRC=${SRC}"
rm -rf "${SRC}"
mkdir -p "${SRC}"

# deterministic_bytes <seed> <length>  →  writes <length> bytes of
# reproducible pseudo-random content on stdout. Uses a simple
# Linear-Congruential recurrence in Python (available everywhere
# Go toolchains are). We avoid /dev/urandom so the fixture is
# byte-identical across runs, which makes piece hashes stable.
deterministic_bytes() {
    local seed="$1"
    local length="$2"
    python3 -c '
import sys, struct
seed = int(sys.argv[1]) & 0xFFFFFFFF
n    = int(sys.argv[2])
out  = bytearray(n)
s    = seed if seed != 0 else 0xdeadbeef
for i in range(n):
    s = (s * 1664525 + 1013904223) & 0xFFFFFFFF
    out[i] = (s >> 16) & 0xFF
sys.stdout.buffer.write(bytes(out))
' "${seed}" "${length}"
}

# Magic marker that every file carries in cleartext so the search
# suite can find at least one hit without relying on the binary
# payload being indexable. We pick a distinct marker per-book so
# the search assertions can also verify per-book scope.
for entry in "${BOOKS[@]}"; do
    IFS=':' read -r book_dir book_title book_marker <<< "${entry}"
    dir="${SRC}/${book_dir}"
    mkdir -p "${dir}"
    for fentry in "${FILES[@]}"; do
        IFS=':' read -r ext size <<< "${fentry}"
        path="${dir}/${book_dir##*/}${ext}"
        seed=$(printf '%s' "${book_dir}${ext}" | cksum | awk '{print $1}')

        # Build file: a human-readable preamble with the marker,
        # then padding to the target size. .txt stays plaintext
        # so extractors can index it; binary extensions carry a
        # pseudo-random payload so piece hashes differ across
        # files.
        case "${ext}" in
            .txt|.info)
                {
                    printf 'Title: %s\n'                "${book_title}"
                    printf 'Marker: %s\n'               "${book_marker}"
                    printf 'Path: %s\n'                 "${book_dir}${ext}"
                    printf -- '-- synthetic test corpus, seed=%s --\n' "${seed}"
                    # Fill remainder with repeating lorem-like text so
                    # the file is actually text-indexable.
                    filler_seed=$((seed % 100000))
                    python3 -c '
import sys
seed = int(sys.argv[1])
n    = int(sys.argv[2])
words = ["alpha","beta","gamma","delta","epsilon","zeta","photon",
         "polity","meridian","atlas","corpus","verse","lexicon",
         "sigil","glyph","rune","lattice","orbit","quantum","chord"]
import random
r = random.Random(seed)
buf = []
total = 0
while total < n:
    line = " ".join(r.choice(words) for _ in range(r.randint(6, 14)))
    line += "\n"
    if total + len(line) > n:
        line = line[: n - total]
    buf.append(line)
    total += len(line)
sys.stdout.write("".join(buf))
' "${filler_seed}" $(( size - $(printf 'Title: %s\nMarker: %s\nPath: %s\n-- synthetic test corpus, seed=%s --\n' "${book_title}" "${book_marker}" "${book_dir}${ext}" "${seed}" | wc -c) ))
                } > "${path}"
                ;;
            *)
                # Binary-ish extensions: a short header with the
                # marker (so a grep against files turns up hits)
                # followed by deterministic pseudo-random bytes.
                header=$(printf 'SNET-FIXTURE marker=%s book=%s\n' "${book_marker}" "${book_dir}")
                header_len=${#header}
                {
                    printf '%s' "${header}"
                    deterministic_bytes "${seed}" $(( size - header_len ))
                } > "${path}"
                ;;
        esac
    done
done

echo "gen-test-fixture: files written:"
find "${SRC}" -type f | sort | sed "s|${SRC}/|  |"
total_size=$(find "${SRC}" -type f -printf '%s\n' | awk '{s+=$1} END {print s}')
echo "gen-test-fixture: total size: ${total_size} bytes"
