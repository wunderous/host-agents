#!/usr/bin/env bash
set -euo pipefail
export PATH="$HOME/.local/go-toolchain/go/bin:$HOME/.bun/bin:/usr/local/bin:/usr/bin:/bin"
HA=/mnt/c/Users/houma/code/opute-host-agent
OP=/mnt/c/Users/houma/code/opute
cd "$HA"

echo "=== 1 config + contract ==="
go test ./internal/config ./test/contract -count=1
echo "CONFIG_CONTRACT_OK"

echo "=== 2 standalone (build + OPUTE_STANDALONE_BINARY) ==="
mkdir -p dist
go build -ldflags "-X main.version=0.1.0" -o dist/host-agent-linux-x64 ./cmd/opute-host-agent
# Prefer absolute path; run without OPUTE_TRANSPORT in env
env -u OPUTE_TRANSPORT OPUTE_STANDALONE_BINARY="$HA/dist/host-agent-linux-x64" go test ./test/standalone -count=1
echo "STANDALONE_ARTIFACT_OK"

echo "=== 3 npm launcher ==="
cd "$HA/npm/local-host-agent"
npm test
echo "NPM_OK"

echo "=== 4 published canary ==="
RUN_PUBLISHED_NPM_CANARY=true node --test published-canary.test.js
echo "PUBLISHED_CANARY_OK"

echo "=== 5 live lifecycle ==="
cd "$HA"
go test -tags=integration ./test/live -count=1 -timeout 30m
echo "LIVE_OK"

echo "=== 6 strict opute parity ==="
cd "$OP"
export OPUTE_REQUIRE_HOST_AGENT_SCHEMA=true
export OPUTE_HOST_AGENT_SCHEMA_PATH="$HA/schemas/streamable-http-client.json"
bun test mcps/packages/shared/src/streamable-http-client.test.ts
echo "PARITY_OK"

echo "VALIDATE_ALL_FIVE_EXIT:0"
