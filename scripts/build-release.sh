#!/usr/bin/env bash
#
# Build SwartzNet release binaries for every supported platform.
#
# Usage:
#   scripts/build-release.sh <version>
#
# Example:
#   scripts/build-release.sh v0.1.0
#
# Output:
#   ./dist/swartznet-<version>-linux-amd64
#   ./dist/swartznet-<version>-linux-arm64
#   ./dist/swartznet-<version>-darwin-amd64
#   ./dist/swartznet-<version>-darwin-arm64
#   ./dist/swartznet-<version>-windows-amd64.exe
#
# Plus a SHA256SUMS file with one line per binary.
#
# Pure Go cross-compilation, no cgo, no toolchain switching needed.
# CGO_ENABLED=0 is forced so the resulting binaries are fully
# static and have no glibc / musl dependency.

set -euo pipefail

if [[ $# -ne 1 ]]; then
    echo "usage: $0 <version>" >&2
    echo "example: $0 v0.1.0" >&2
    exit 2
fi

VERSION="$1"
DIST="./dist"
PKG="./cmd/swartznet"

mkdir -p "$DIST"
rm -f "$DIST"/swartznet-"$VERSION"-* "$DIST"/SHA256SUMS

# Stripped, trimpath, version baked in via -ldflags. CGO disabled
# so the result is a fully static binary.
LDFLAGS="-s -w -X main.Version=$VERSION"

# (goos goarch suffix)
TARGETS=(
    "linux   amd64 linux-amd64"
    "linux   arm64 linux-arm64"
    "darwin  amd64 darwin-amd64"
    "darwin  arm64 darwin-arm64"
    "windows amd64 windows-amd64.exe"
)

for entry in "${TARGETS[@]}"; do
    read -r goos goarch suffix <<<"$entry"
    out="$DIST/swartznet-$VERSION-$suffix"
    echo ">>> building $out"
    GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
        go build -trimpath -ldflags "$LDFLAGS" -o "$out" "$PKG"
done

# Generate SHA256SUMS for the release notes / verification.
(
    cd "$DIST"
    sha256sum swartznet-"$VERSION"-* > SHA256SUMS
)

echo
echo "Built binaries in $DIST/ for version $VERSION:"
ls -lh "$DIST"/swartznet-"$VERSION"-* "$DIST"/SHA256SUMS
echo
echo "Upload to GitHub release:"
echo "  gh release upload $VERSION $DIST/swartznet-$VERSION-* $DIST/SHA256SUMS"
