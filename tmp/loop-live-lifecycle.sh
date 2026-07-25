#!/usr/bin/env bash
set -euo pipefail
export PATH="$HOME/.local/go-toolchain/go/bin:$HOME/.bun/bin:/usr/local/bin:/usr/bin:/bin"
cd /mnt/c/Users/houma/code/opute-host-agent
echo "=== live lifecycle ==="
go test -tags=integration ./test/live -count=1 -timeout 30m -v
echo "LIVE_EXIT:$?"
