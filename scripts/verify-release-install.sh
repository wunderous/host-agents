#!/usr/bin/env bash
set -euo pipefail

RELEASE_TAG="${RELEASE_TAG:-v0.1.0}"
RELEASE_URL="${RELEASE_URL:-https://github.com/opute-io/host-agents/releases/download/${RELEASE_TAG}/host-agent-linux-x64.gz}"
INSTALL_ROOT="${INSTALL_ROOT:-/tmp/opute-release-verify-$$}"
INSTALL_DIR="$INSTALL_ROOT/opt/opute"
CONFIG_DIR="$INSTALL_ROOT/etc/opute"
BINARY_PATH="$INSTALL_DIR/opute-host-agent"
PORT="${PORT:-32041}"
TOKEN="${TOKEN:-release-verify-token}"
LOG_FILE="$INSTALL_ROOT/agent.log"
PID_FILE="$INSTALL_ROOT/agent.pid"

cleanup() {
  if [ -f "$PID_FILE" ]; then
    kill "$(cat "$PID_FILE")" 2>/dev/null || true
  fi
  rm -rf "$INSTALL_ROOT"
}
trap cleanup EXIT

pass() { echo "PASS: $1"; }
fail() { echo "FAIL: $1"; exit 1; }

echo "=== Opute host agent release install verification ==="
echo "Release: $RELEASE_TAG"
echo "Install root: $INSTALL_ROOT"

mkdir -p "$INSTALL_DIR" "$CONFIG_DIR"
cd "$INSTALL_ROOT"

echo
echo "[1/7] Downloading release artifact..."
if command -v gh >/dev/null 2>&1 && gh auth status >/dev/null 2>&1; then
  gh release download "$RELEASE_TAG" --repo opute-io/host-agents \
    --pattern 'host-agent-linux-x64.gz' \
    --dir "$INSTALL_ROOT"
  pass "downloaded via gh release download ($(wc -c < host-agent-linux-x64.gz) bytes)"
elif curl -sfL "$RELEASE_URL" -o host-agent-linux-x64.gz; then
  pass "downloaded via curl ($(wc -c < host-agent-linux-x64.gz) bytes)"
else
  fail "download failed (private repo requires gh auth or GH_TOKEN)"
fi
[ -s host-agent-linux-x64.gz ] || fail "download empty"

echo
echo "[2/7] Verifying gzip integrity..."
gzip -t host-agent-linux-x64.gz
pass "gzip integrity"

echo
echo "[3/7] Installing binary to $BINARY_PATH..."
gunzip -c host-agent-linux-x64.gz > "$BINARY_PATH"
chmod +x "$BINARY_PATH"
file "$BINARY_PATH" | grep -q "ELF 64-bit" || fail "not ELF x64"
pass "installed ELF binary ($(wc -c < "$BINARY_PATH") bytes)"

echo
echo "[4/7] Writing host-agent.env (HTTP mode, no bridge)..."
cat > "$CONFIG_DIR/host-agent.env" <<EOF
HOST_MCP_PORT=$PORT
HOST_MCP_BIND_HOST=127.0.0.1
MCP_AUTH_TOKEN=$TOKEN
OPUTE_REVERSE_TUNNEL=false
OPUTE_INFRA_PROVIDER_ID=incus
OPUTE_REMOTE_AGENT_ID=release-verify-host
EOF
pass "env file written"

echo
echo "[5/7] Starting installed agent..."
set -a
# shellcheck disable=SC1090
source "$CONFIG_DIR/host-agent.env"
set +a
nohup "$BINARY_PATH" >"$LOG_FILE" 2>&1 &
echo $! > "$PID_FILE"
sleep 2

for _ in $(seq 1 30); do
  if curl -sf "http://127.0.0.1:$PORT/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

HEALTH=$(curl -sf "http://127.0.0.1:$PORT/health")
echo "$HEALTH" | grep -q '"ok":true' || fail "health check: $HEALTH"
pass "health endpoint: $HEALTH"

echo
echo "[6/7] Verifying MCP initialize + tools/list..."
INIT_HEADERS=$(mktemp)
INIT_BODY=$(mktemp)
curl -sD "$INIT_HEADERS" -o "$INIT_BODY" -X POST "http://127.0.0.1:$PORT/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"release-verify","version":"1.0.0"}}}'

grep -qi "mcp-session-id" "$INIT_HEADERS" || fail "missing mcp-session-id header"
SESSION_ID=$(grep -i "mcp-session-id" "$INIT_HEADERS" | tr -d '\r' | awk '{print $2}')
[ -n "$SESSION_ID" ] || fail "empty session id"
grep -q "host-agent" "$INIT_BODY" || fail "initialize response missing server info: $(cat "$INIT_BODY")"
pass "MCP initialize (session=$SESSION_ID)"

TOOLS_BODY=$(mktemp)
curl -sf -X POST "http://127.0.0.1:$PORT/mcp" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -H "Accept: application/json, text/event-stream" \
  -H "mcp-session-id: $SESSION_ID" \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}' \
  -o "$TOOLS_BODY"
for tool in create_vm list_vms get_host_info install_k3s; do
  grep -q "\"$tool\"" "$TOOLS_BODY" || fail "tools/list missing $tool"
done
TOOL_COUNT=$(grep -o '"name"' "$TOOLS_BODY" | wc -l)
[ "$TOOL_COUNT" -ge 50 ] || fail "expected >=50 tools, got $TOOL_COUNT"
pass "tools/list returned $TOOL_COUNT tools (includes create_vm, list_vms, get_host_info, install_k3s)"

echo
echo "[7/7] Verifying auth rejection without token..."
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -X POST "http://127.0.0.1:$PORT/mcp" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/list","params":{}}')
[ "$STATUS" = "401" ] || fail "expected 401 without auth, got $STATUS"
pass "unauthorized MCP returns HTTP 401"

echo
echo "=== All release install checks passed ==="
echo "Binary: $BINARY_PATH"
echo "Logs: $LOG_FILE"
