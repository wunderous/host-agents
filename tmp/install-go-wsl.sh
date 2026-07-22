#!/usr/bin/env bash
set -euo pipefail

GO_VER="${GO_VER:-1.23.4}"
DEST="$HOME/.local/go-toolchain"

if [ ! -x "$DEST/go/bin/go" ]; then
  mkdir -p "$DEST"
  ARCH="$(uname -m)"
  case "$ARCH" in
    x86_64) GOARCH=amd64 ;;
    aarch64) GOARCH=arm64 ;;
    *) GOARCH=amd64 ;;
  esac
  URL="https://go.dev/dl/go${GO_VER}.linux-${GOARCH}.tar.gz"
  echo "downloading $URL"
  curl -fsSL "$URL" -o "/tmp/go-${GO_VER}.tar.gz"
  tar -C "$DEST" -xzf "/tmp/go-${GO_VER}.tar.gz"
fi

"$DEST/go/bin/go" version
