#!/usr/bin/env bash
set -euo pipefail
export PATH="/home/linuxbrew/.linuxbrew/bin:$PATH"
cd /mnt/c/Users/houma/code/opute-host-agent
BINARY=dist/host-agent-linux-x64
if [[ ! -x "$BINARY" ]]; then
  chmod +x "$BINARY" 2>/dev/null || true
fi
# Ensure a versioned binary exists for the canary
if [[ ! -f "$BINARY" ]]; then
  echo "missing $BINARY" >&2
  exit 1
fi
RUNTIME=$(mktemp -d)
export OPUTE_HOST_AGENT_BINARY="$PWD/$BINARY"
export OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR="$RUNTIME"
export HOST_MCP_PORT=3014
export HOST_MCP_BIND_HOST=127.0.0.1
export OPUTE_STANDALONE_STATE_DIR="$RUNTIME/state"
# Stop any prior launcher state on 3014
cd npm/local-host-agent
node index.js stop >/dev/null 2>&1 || true
URL=$(node index.js start --background)
echo "tier5_url=$URL"
node index.js status
python3 - <<'PY'
import sys
sys.path.insert(0, "/mnt/c/Users/houma/code/opute-host-agent/scripts")
from standalone_http_mcp import StreamableHttpSession
s = StreamableHttpSession("http://127.0.0.1:3014/mcp")
init = s.initialize("tier5-ide-canary")
tools = s.rpc("tools/list", {})
names = sorted(t["name"] for t in tools.get("tools", []))
required = {"check_local_prerequisites", "get_local_status", "list_vms", "create_vm", "get_operation"}
missing = sorted(required - set(names))
print("server", init.get("serverInfo"))
print("tool_count", len(names))
print("missing_required", missing)
if missing:
    raise SystemExit(1)
print("tier5_client_shape_canary_passed")
PY
node index.js stop
echo TIER5_OK
