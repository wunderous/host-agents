#!/usr/bin/env bash
set -euo pipefail
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
cd /mnt/c/Users/houma/code/opute-host-agent/npm/local-host-agent
RUNTIME=$(mktemp -d)
export OPUTE_HOST_AGENT_BINARY=/mnt/c/Users/houma/code/opute-host-agent/dist/host-agent-linux-x64
export OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR="$RUNTIME"
export HOST_MCP_PORT=3015
export OPUTE_STANDALONE_STATE_DIR="$RUNTIME/state"
node index.js start --background
node index.js status
URL=$(node index.js url)
echo "url=$URL"
python3 - <<PY
import sys
sys.path.insert(0, "/mnt/c/Users/houma/code/opute-host-agent/scripts")
from standalone_http_mcp import StreamableHttpSession
s = StreamableHttpSession("$URL")
init = s.initialize("launcher-canary")
print("server", init.get("serverInfo"))
tools = s.rpc("tools/list", {})
print("tool_count", len(tools.get("tools", [])))
PY
node index.js stop
node index.js status || true
echo LAUNCHER_OK
