#!/bin/sh
# testbed entrypoint — applies optional tc-netem network
# emulation rules, optionally pre-populates /data with the
# fixture content (seed role), then exec's the command.
#
# Env vars:
#   NETEM_PROFILE   path inside /netem/ (e.g. /netem/lossy.sh)
#   ROLE            "seed"  → copy /fixture/content/* into /data
#                   "leech" → /data stays empty (content will
#                             download from a seed)
#                   default is "leech".

set -e

if [ -n "$NETEM_PROFILE" ] && [ -f "$NETEM_PROFILE" ]; then
    echo "testbed: applying netem profile $NETEM_PROFILE"
    sh "$NETEM_PROFILE"
else
    echo "testbed: no netem profile, running with direct network"
fi

case "${ROLE:-leech}" in
    seed)
        if [ -d /fixture/content ]; then
            echo "testbed: seeding — copying fixture content into /data"
            # -a preserves timestamps so anacrolix's piece-verify
            # pass sees identical bytes and marks the torrent as
            # already complete.
            cp -a /fixture/content/. /data/
            ls -lR /data | head -20
        else
            echo "testbed: ROLE=seed but /fixture/content missing — continuing anyway"
        fi
        ;;
    leech)
        echo "testbed: leech role — /data starts empty"
        ;;
    *)
        echo "testbed: unknown ROLE='$ROLE' (expected seed|leech), defaulting to leech"
        ;;
esac

exec "$@"
