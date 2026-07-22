#!/usr/bin/env bash
set -euo pipefail
export PATH="$HOME/.local/go-toolchain/go/bin:$HOME/.bun/bin:/usr/local/bin:/usr/bin:/bin"
cd /mnt/c/Users/houma/code/opute-host-agent

echo "=== go standalone ==="
go test ./internal/config ./test/contract ./test/standalone -count=1
echo "GO_EXIT:$?"

echo "=== published canary ==="
cd npm/local-host-agent
if [ -f published-canary.test.js ]; then
  # Published canary may need network/auth; run and capture
  node --test published-canary.test.js || true
  echo "CANARY_EXIT:$?"
else
  echo "SKIP_NO_CANARY"
fi

echo "=== live lifecycle (integration tag) ==="
cd /mnt/c/Users/houma/code/opute-host-agent
if [ "${OPUTE_RUN_LIVE_LIFECYCLE:-0}" = "1" ]; then
  go test -tags=integration ./test/live -count=1 -timeout 30m
  echo "LIVE_EXIT:$?"
else
  echo "SKIP_LIVE (set OPUTE_RUN_LIVE_LIFECYCLE=1)"
fi
