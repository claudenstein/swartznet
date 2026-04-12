#!/usr/bin/env bash
#
# Build SwartzNet GUI for the native host platform.
#
# Usage:
#   scripts/build-gui.sh [<version>]
#
# Output:
#   ./dist/swartznet-gui-<version>-linux-amd64   (or your host OS/arch)
#
# Unlike build-release.sh (pure Go, CGO_ENABLED=0), the GUI binary
# requires CGo for Fyne's OpenGL/GLFW bindings. This means the
# build happens on the native host — cross-compilation requires
# fyne-cross (Docker-based, see below).
#
# Build dependencies (Linux):
#   sudo apt-get install -y gcc libgl1-mesa-dev xorg-dev libxkbcommon-dev
#
# Build dependencies (macOS):
#   xcode-select --install
#
# Build dependencies (Windows):
#   MSYS2 + mingw-w64-x86_64-toolchain
#
# For cross-platform releases, use fyne-cross instead:
#   go install github.com/fyne-io/fyne-cross@latest
#   fyne-cross linux   -app-id net.swartznet.gui ./cmd/swartznet-gui
#   fyne-cross windows -app-id net.swartznet.gui ./cmd/swartznet-gui
#   fyne-cross darwin  -app-id net.swartznet.gui ./cmd/swartznet-gui

set -euo pipefail

VERSION="${1:-dev}"
DIST="./dist"
PKG="./cmd/swartznet-gui"

mkdir -p "$DIST"

# Detect native host OS/arch for the output filename.
GOOS="$(go env GOOS)"
GOARCH="$(go env GOARCH)"
SUFFIX="$GOOS-$GOARCH"
if [[ "$GOOS" == "windows" ]]; then
    SUFFIX="$SUFFIX.exe"
fi

OUT="$DIST/swartznet-gui-$VERSION-$SUFFIX"
echo ">>> building $OUT (CGO_ENABLED=1)"

CGO_ENABLED=1 \
    go build -trimpath \
    -ldflags "-s -w -X main.Version=$VERSION" \
    -o "$OUT" "$PKG"

echo
echo "GUI built:"
ls -lh "$OUT"
echo
echo "Run with: $OUT"
