#!/usr/bin/env bash
# Install the verified Linux host-agent artifact and its bootstrap prerequisites.
#
# This is the single documented bootstrap exception. After this script returns,
# Incus, K3s, registries, Kubernetes, domains, and application changes must be
# performed through the host-agent MCP API. It is intentionally idempotent.
set -euo pipefail

fail() { echo "bootstrap-host-agent: $*" >&2; exit 1; }

BIN_SOURCE="${OPUTE_HOST_AGENT_ARTIFACT:-}"
INSTALL_ROOT="${OPUTE_HOST_AGENT_INSTALL_ROOT:-$HOME/.local/share/opute}"
BIN_DEST="$INSTALL_ROOT/opute-host-agent"
ENV_DIR="${OPUTE_HOST_AGENT_CONFIG_DIR:-$HOME/.config/opute}"
ENV_FILE="$ENV_DIR/host-agent.env"
SERVICE_SCOPE="${OPUTE_HOST_AGENT_SERVICE_SCOPE:-auto}"
if [[ "$SERVICE_SCOPE" == "auto" ]]; then
  if [[ "$(id -u)" -eq 0 ]]; then SERVICE_SCOPE="system"; else SERVICE_SCOPE="user"; fi
fi
[[ "$SERVICE_SCOPE" == "user" || "$SERVICE_SCOPE" == "system" ]] || fail "OPUTE_HOST_AGENT_SERVICE_SCOPE must be user or system"
TARGET_USER="${OPUTE_HOST_AGENT_TARGET_USER:-${SUDO_USER:-${USER:-}}}"
if [[ "$SERVICE_SCOPE" == "system" ]]; then
  [[ "$(id -u)" -eq 0 ]] || fail "system host-agent service scope requires root"
  [[ -n "$TARGET_USER" && "$TARGET_USER" != "root" ]] || fail "OPUTE_HOST_AGENT_TARGET_USER is required for system host-agent scope"
  TARGET_HOME="$(getent passwd "$TARGET_USER" | cut -d: -f6)"
  [[ -n "$TARGET_HOME" && -d "$TARGET_HOME" ]] || fail "target user home directory is unavailable"
  INSTALL_ROOT="${OPUTE_HOST_AGENT_INSTALL_ROOT:-$TARGET_HOME/.local/share/opute}"
  BIN_DEST="$INSTALL_ROOT/opute-host-agent"
  ENV_DIR="${OPUTE_HOST_AGENT_CONFIG_DIR:-$TARGET_HOME/.config/opute}"
  ENV_FILE="$ENV_DIR/host-agent.env"
fi
if [[ "$SERVICE_SCOPE" == "system" ]]; then
  UNIT_DIR="/etc/systemd/system"
  UNIT_FILE="$UNIT_DIR/opute-bootstrap-mcp.service"
else
  UNIT_DIR="$HOME/.config/systemd/user"
  UNIT_FILE="$UNIT_DIR/opute-bootstrap-mcp.service"
fi
PORT="${HOST_MCP_PORT:-3014}"
STATE_DIR="${OPUTE_STANDALONE_STATE_DIR:-$HOME/.opute/standalone-bootstrap}"
if [[ "$SERVICE_SCOPE" == "system" ]]; then
  STATE_DIR="${OPUTE_STANDALONE_STATE_DIR:-$TARGET_HOME/.opute/standalone-bootstrap}"
fi

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

disable_previous_user_unit() {
  [[ "$SERVICE_SCOPE" == "system" ]] || return 0
  local runtime_dir="/run/user/$(id -u "$TARGET_USER")"
  if [[ -S "$runtime_dir/bus" ]]; then
    runuser -u "$TARGET_USER" -- env XDG_RUNTIME_DIR="$runtime_dir" DBUS_SESSION_BUS_ADDRESS="unix:path=$runtime_dir/bus" systemctl --user disable --now opute-bootstrap-mcp.service >/dev/null 2>&1 || true
  fi
}

disable_previous_system_unit() {
  [[ "$SERVICE_SCOPE" == "user" ]] || return 0
  # The standalone bootstrap listener is single-owner. Older installs could
  # leave the system-scoped unit enabled while a user-scoped install enabled
  # the same unit name and port, producing an endless user-service bind loop.
  # Reconcile the opposite scope before starting the selected owner; do not
  # silently accept two supervisors for one MCP endpoint.
  if [[ "$(id -u)" -eq 0 ]]; then
    systemctl disable --now opute-bootstrap-mcp.service >/dev/null 2>&1 || true
  elif command -v sudo >/dev/null 2>&1; then
    sudo -n systemctl disable --now opute-bootstrap-mcp.service >/dev/null 2>&1 || true
  fi
  if systemctl is-active --quiet opute-bootstrap-mcp.service 2>/dev/null; then
    fail "system-scoped opute-bootstrap-mcp.service still owns the standalone listener"
  fi
}

install_agent_privilege_boundary() {
  local target_user="${OPUTE_HOST_AGENT_TARGET_USER:-${SUDO_USER:-}}"
  [[ -n "$target_user" ]] || fail "OPUTE_HOST_AGENT_TARGET_USER is required for privilege-boundary preparation"
  if [[ "$(id -u)" -eq 0 ]]; then
    id "$target_user" >/dev/null 2>&1 || fail "target host-agent user does not exist: $target_user"
  else
    id "$target_user" >/dev/null 2>&1 || fail "target host-agent user does not exist: $target_user"
  fi

  # Host-agent operations are deliberately limited to the commands needed by
  # the allowlisted generic host-tool, Kubernetes, and Incus installers. The
  # standalone service remains unprivileged; it can only invoke these commands
  # with sudo -n, so an interactive password prompt can never leak into an MCP
  # task or hang a deployment.
  local sudoers_file="/etc/sudoers.d/opute-host-agent"
  local policy_tmp
  policy_tmp="$(mktemp)"
  trap 'rm -f "$policy_tmp"' RETURN
  cat > "$policy_tmp" <<EOF
$target_user ALL=(root) NOPASSWD: /usr/bin/apt-get, /usr/bin/dpkg, /usr/bin/install, /usr/bin/mkdir, /usr/bin/systemctl, /usr/bin/loginctl, /usr/sbin/usermod, /usr/local/bin/k3s, /usr/bin/true, /usr/bin/test, /usr/bin/tee, /usr/bin/chmod, /usr/bin/rm, /usr/bin/readlink, /usr/bin/realpath, /usr/bin/sed, /usr/bin/tr, /usr/bin/awk, /usr/bin/grep, /usr/bin/head, /usr/bin/cut, /usr/bin/date, /usr/bin/sleep, /usr/bin/base64, /usr/bin/ln, /usr/bin/cp, /usr/bin/chown, /usr/bin/incus, /usr/sbin/iptables, /usr/local/lib/opute/setup-incus-nat.sh
EOF
  if [[ "$(id -u)" -eq 0 ]]; then
    install -d -m 0755 /etc/sudoers.d
    install -m 0440 "$policy_tmp" "$sudoers_file"
    command -v visudo >/dev/null 2>&1 && visudo -cf "$sudoers_file" >/dev/null || fail "invalid host-agent sudoers policy"
  else
    sudo -n install -d -m 0755 /etc/sudoers.d || fail "cannot prepare host-agent sudoers directory"
    sudo -n install -m 0440 "$policy_tmp" "$sudoers_file" || fail "cannot install host-agent sudoers policy"
  fi
  rm -f "$policy_tmp"
  trap - RETURN
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

ensure_user_manager_lifecycle() {
  # A user-scoped systemd service is not a host supervisor when its manager is
  # allowed to terminate with the last login session.  WSL commands used by
  # MCP orchestration are deliberately non-interactive, so without lingering
  # the distro can shut down seconds after a caller exits and take every
  # unrelated service (relay, tunnel, model runtime, and Incus) with it.
  # This is a generic installation prerequisite, not an Opute product rule.
  command -v loginctl >/dev/null 2>&1 || fail "systemd-logind is required for a persistent service"
  local target_user="${OPUTE_HOST_AGENT_TARGET_USER:-${SUDO_USER:-${USER:-}}}"
  [[ -n "$target_user" ]] || fail "cannot determine host-agent user for persistent service lifecycle"
  if [[ "$SERVICE_SCOPE" == "system" ]]; then
    loginctl enable-linger "$target_user" || true
    loginctl show-user "$target_user" -p Linger | grep -qx 'Linger=yes' || fail "user manager linger was not enabled for $target_user"
    echo "HOST_AGENT_USER_MANAGER_PERSISTENT user=$target_user scope=system"
    return 0
  fi
  command -v loginctl >/dev/null 2>&1 || fail "systemd-logind is required for a persistent user service"
  if [[ "$(id -u)" -eq 0 ]]; then
    loginctl enable-linger "$target_user"
  else
    sudo -n loginctl enable-linger "$target_user" || fail "cannot enable persistent user-service lifecycle for $target_user"
  fi
  loginctl show-user "$target_user" -p Linger | grep -qx 'Linger=yes' || fail "user manager linger was not enabled for $target_user"
  echo "HOST_AGENT_USER_MANAGER_PERSISTENT user=$target_user"
}

install_prerequisites
ensure_user_manager_lifecycle
if [[ "$SERVICE_SCOPE" == "system" ]]; then
  install_agent_privilege_boundary
else
  OPUTE_HOST_AGENT_TARGET_USER="$(id -un)" install_agent_privilege_boundary
fi
disable_previous_user_unit
disable_previous_system_unit
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
if [[ "$SERVICE_SCOPE" == "system" ]]; then
  chown -R "$TARGET_USER":"$TARGET_USER" "$INSTALL_ROOT" "$ENV_DIR" "$STATE_DIR"
fi

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
User=$TARGET_USER
Environment=XDG_RUNTIME_DIR=/run/user/$(id -u "$TARGET_USER")
Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$(id -u "$TARGET_USER")/bus
ExecStart=$BIN_DEST --mode standalone --transport http
# The bootstrap command may be invoked through this very MCP service. In that
# case it deliberately restarts the unit and the current process exits cleanly;
# always restart so a self-reconcile cannot strand the standalone endpoint.
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
EOF

if [[ "$SERVICE_SCOPE" == "system" ]]; then
  systemctl daemon-reload
  systemctl enable opute-bootstrap-mcp.service
  # Reconcile the running process with the artifact just installed. `enable
  # --now` is intentionally not sufficient here: systemd leaves an already
  # active unit running, which would make an idempotent artifact refresh serve
  # stale Go code until the next host reboot.
  systemctl restart opute-bootstrap-mcp.service
else
  systemctl --user daemon-reload
  systemctl --user enable opute-bootstrap-mcp.service
  # Always restart after replacing the binary so the service observes the
  # reconciled artifact during the same idempotent bootstrap invocation.
  systemctl --user restart opute-bootstrap-mcp.service
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
