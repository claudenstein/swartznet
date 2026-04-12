#!/bin/sh
# testbed entrypoint — applies optional tc-netem network
# emulation rules, then exec's the command. Set NETEM_PROFILE
# to a file path inside /netem/ to apply a specific profile
# (e.g. NETEM_PROFILE=/netem/home-dsl.sh).
#
# Falls back to no-netem (direct network) if NETEM_PROFILE is
# empty or the file doesn't exist.

set -e

if [ -n "$NETEM_PROFILE" ] && [ -f "$NETEM_PROFILE" ]; then
    echo "testbed: applying netem profile $NETEM_PROFILE"
    sh "$NETEM_PROFILE"
else
    echo "testbed: no netem profile, running with direct network"
fi

exec "$@"
