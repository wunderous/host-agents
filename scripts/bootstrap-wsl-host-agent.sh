#!/usr/bin/env bash
# Install the verified Linux host-agent artifact and its bootstrap prerequisites.
#
# This is the single documented bootstrap exception. After this script returns,
# Incus, K3s, registries, Kubernetes, domains, and application changes must be
# performed through the host-agent MCP API. It is intentionally idempotent.
set -euo pipefail

BIN_SOURCE="${OPUTE_HOST_AGENT_ARTIFACT:-}"
INSTALL_ROOT="${OPUTE_HOST_AGENT_INSTALL_ROOT:-$HOME/.local/share/opute}"
BIN_DEST="$INSTALL_ROOT/opute-host-agent"
ENV_DIR="${OPUTE_HOST_AGENT_CONFIG_DIR:-$HOME/.config/opute}"
ENV_FILE="$ENV_DIR/host-agent.env"
if [[ "$(id -u)" -eq 0 ]]; then
  UNIT_DIR="/etc/systemd/system"
  UNIT_FILE="$UNIT_DIR/opute-bootstrap-mcp.service"
  SERVICE_SCOPE="system"
else
  UNIT_DIR="$HOME/.config/systemd/user"
  UNIT_FILE="$UNIT_DIR/opute-bootstrap-mcp.service"
  SERVICE_SCOPE="user"
fi
PORT="${HOST_MCP_PORT:-3014}"
STATE_DIR="${OPUTE_STANDALONE_STATE_DIR:-$HOME/.opute/standalone-bootstrap}"

fail() { echo "bootstrap-host-agent: $*" >&2; exit 1; }

reconcile_existing_standalone_listener() {
  local listener pid command_line
  listener="$(ss -ltnp "sport = :$PORT" 2>/dev/null || true)"
  pid="$(printf '%s\n' "$listener" | sed -n 's/.*pid=\([0-9][0-9]*\).*/\1/p' | head -n1)"
  [[ -z "$pid" ]] && return 0
  [[ "$pid" != "$$" ]] || return 0
  command_line="$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null || true)"
  if [[ "$command_line" == *"--mode standalone --transport http"* && "$command_line" == *"opute-host-agent"* ]]; then
    echo "bootstrap-host-agent: replacing prior standalone host-agent pid=$pid"
    kill -TERM "$pid" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "$pid" 2>/dev/null || return 0
      sleep 0.25
    done
    kill -KILL "$pid" 2>/dev/null || true
    return 0
  fi
  fail "port $PORT is owned by an unrelated process: pid=$pid command=$command_line"
}

install_agent_privilege_boundary() {
  [[ "$(id -u)" -eq 0 ]] || fail "privilege-boundary preparation must run as root"
  local target_user="${OPUTE_HOST_AGENT_TARGET_USER:-}"
  [[ -n "$target_user" ]] || fail "OPUTE_HOST_AGENT_TARGET_USER is required for privilege-boundary preparation"
  id "$target_user" >/dev/null 2>&1 || fail "target host-agent user does not exist: $target_user"

  # Host-agent package operations are deliberately limited to the commands
  # needed by the allowlisted generic host-tool and Incus installers. The
  # standalone service remains unprivileged; it can only invoke these commands
  # with sudo -n, so an interactive password prompt can never leak into an MCP
  # task or hang a deployment.
  local sudoers_file="/etc/sudoers.d/opute-host-agent"
  install -d -m 0755 /etc/sudoers.d
  cat > "$sudoers_file" <<EOF
$target_user ALL=(root) NOPASSWD: /usr/bin/apt-get, /usr/bin/dpkg, /usr/bin/install, /usr/bin/mkdir, /usr/bin/systemctl, /usr/sbin/usermod
EOF
  chmod 0440 "$sudoers_file"
  command -v visudo >/dev/null 2>&1 && visudo -cf "$sudoers_file" >/dev/null || fail "invalid host-agent sudoers policy"
  echo "HOST_AGENT_PRIVILEGE_BOUNDARY_READY user=$target_user policy=$sudoers_file"
}

if [[ "${OPUTE_HOST_AGENT_BOOTSTRAP_PREPARE_PRIVILEGE:-}" == "1" ]]; then
  install_agent_privilege_boundary
  exit 0
fi

if [[ -z "$BIN_SOURCE" ]]; then
  BIN_SOURCE="/mnt/c/Users/${WINDOWS_USER:-$USER}/code/opute-host-agent/dist/host-agent-linux-x64"
fi
[[ -x "$BIN_SOURCE" ]] || fail "verified Linux artifact is missing or not executable: $BIN_SOURCE"

install_prerequisites() {
  local missing=()
  # The agent bootstrap itself only needs the HTTP client and trust store;
  # Bun/Go/Incus are later owned by their respective product/host-agent
  # operations and must not be installed by this exception.
  for command_name in curl; do
    command -v "$command_name" >/dev/null 2>&1 || missing+=("$command_name")
  done
  [[ -s /etc/ssl/certs/ca-certificates.crt ]] || missing+=("ca-certificates")
  if ((${#missing[@]})); then
    if [[ "$(id -u)" -eq 0 ]]; then
      apt-get update
      DEBIAN_FRONTEND=noninteractive apt-get install -y "${missing[@]}"
    else
      command -v sudo >/dev/null 2>&1 || fail "missing prerequisites (${missing[*]}) and sudo is unavailable"
      sudo apt-get update
      sudo DEBIAN_FRONTEND=noninteractive apt-get install -y "${missing[@]}"
    fi
  fi
  command -v systemctl >/dev/null 2>&1 || fail "systemd is required in WSL; enable systemd in /etc/wsl.conf and restart WSL"
}

install_prerequisites
reconcile_existing_standalone_listener
if getent group incus-admin >/dev/null 2>&1 && ! id -nG "$(id -un)" | tr ' ' '\n' | grep -qx incus-admin; then
  # Incus exposes its control socket to incus-admin. Reconcile this once as
  # part of host-agent installation so later infrastructure mutations use
  # host-agent MCP rather than a privileged side channel.
  if [[ "$(id -u)" -eq 0 ]]; then
    usermod -aG incus-admin "${SUDO_USER:-${USER}}"
  else
    command -v sudo >/dev/null 2>&1 || fail "incus-admin group exists but sudo is unavailable"
    sudo usermod -aG incus-admin "$(id -un)"
  fi
fi
mkdir -p "$INSTALL_ROOT" "$ENV_DIR" "$UNIT_DIR" "$STATE_DIR"
install -m 0755 "$BIN_SOURCE" "$BIN_DEST"

cat > "$ENV_FILE" <<EOF
OPUTE_AGENT_MODE=standalone
OPUTE_TRANSPORT=http
HOST_MCP_PORT=$PORT
HOST_MCP_BIND_HOST=127.0.0.1
OPUTE_STANDALONE_STATE_DIR=$STATE_DIR
OPUTE_HOST_AGENT_INSTANCE=standalone
OPUTE_HOST_AGENT_INSTANCE_ROOT=$STATE_DIR
OPUTE_HOST_AGENT_RELAY_DIR=$STATE_DIR/local-llm-relays
OPUTE_INFRA_PROVIDER_ID=incus
OPUTE_INCUS_OWNERSHIP_MODE=audit
OPUTE_STANDALONE_ALLOW_MUTATIONS=${OPUTE_STANDALONE_ALLOW_MUTATIONS:-true}
EOF
chmod 0600 "$ENV_FILE"

cat > "$UNIT_FILE" <<EOF
[Unit]
Description=Opute standalone bootstrap host-agent MCP
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=$ENV_FILE
ExecStart=$BIN_DEST --mode standalone --transport http
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
EOF

if [[ "$SERVICE_SCOPE" == "system" ]]; then
  systemctl daemon-reload
  systemctl enable --now opute-bootstrap-mcp.service
else
  systemctl --user daemon-reload
  systemctl --user enable --now opute-bootstrap-mcp.service
fi
for _ in $(seq 1 30); do
  if curl --fail --silent --show-error --max-time 2 "http://127.0.0.1:$PORT/health" >/dev/null; then
    echo "HOST_AGENT_BOOTSTRAP_PASS mcp=http://127.0.0.1:$PORT/mcp binary=$BIN_DEST"
    exit 0
  fi
  sleep 1
done
if [[ "$SERVICE_SCOPE" == "system" ]]; then
  systemctl --no-pager status opute-bootstrap-mcp.service >&2 || true
else
  systemctl --user --no-pager status opute-bootstrap-mcp.service >&2 || true
fi
fail "host-agent MCP did not become healthy on 127.0.0.1:$PORT"
