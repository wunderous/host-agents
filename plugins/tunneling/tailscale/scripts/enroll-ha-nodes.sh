#!/usr/bin/env bash
# Fallback operator helper: enroll opute-ha-a / opute-ha-b into Tailscale and print IPv4s.
# Prefer recipe com.opute.tailscale.ha-network-bundle (mesh-runtime.ensure-agent before enroll).
# This script still installs the agent for emergency use; product path uses mesh-runtime.v1.
# Prefer driving enrollment through the provider MCP live backend when available.
set -euo pipefail

ENV_FILE="${OPUTE_TAILSCALE_ENV_FILE:-$HOME/.config/opute/tailscale.env}"
AUTH_FILE="${TAILSCALE_AUTH_KEY_FILE:-$HOME/.config/opute/tailscale.authkey}"

if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  set -a
  source "$ENV_FILE"
  set +a
fi

if [[ -z "${TAILSCALE_AUTH_KEY:-}" && -f "$AUTH_FILE" ]]; then
  TAILSCALE_AUTH_KEY="$(tr -d '\r\n' <"$AUTH_FILE")"
  export TAILSCALE_AUTH_KEY
fi

if [[ -z "${TAILSCALE_AUTH_KEY:-}" ]]; then
  echo "TAILSCALE_AUTH_KEY (or $AUTH_FILE) is required" >&2
  exit 1
fi

enroll_one() {
  local name="$1"
  local uri="${2:-container:local:$1}"
  echo "=== enrolling $name ($uri) ===" >&2
  if command -v incus >/dev/null 2>&1; then
    local instance="${name}"
    if ! incus exec "$instance" -- true >/dev/null 2>&1; then
      echo "incus instance $instance is not reachable; skipping direct exec" >&2
      return 1
    fi
    incus exec "$instance" -- bash -lc '
      set -euo pipefail
      if ! command -v tailscale >/dev/null 2>&1; then
        curl -fsSL https://tailscale.com/install.sh | sh
      fi
      systemctl enable --now tailscaled >/dev/null 2>&1 || true
    '
    printf '%s\n' "$TAILSCALE_AUTH_KEY" | incus exec "$instance" -- bash -lc '
      set -euo pipefail
      IFS= read -r auth_key
      tailscale up --authkey="$auth_key" --hostname="'"$name"'" --accept-routes=false --reset
      for i in $(seq 1 30); do
        ip="$(tailscale ip -4 2>/dev/null | head -n1 || true)"
        if [[ -n "$ip" ]]; then
          printf "%s %s\n" "'"$name"'" "$ip"
          exit 0
        fi
        sleep 2
      done
      echo "no Tailscale IPv4 for '"$name"'" >&2
      exit 1
    '
  else
    echo "incus not available; set OPUTE_HOST_AGENT_ENDPOINT and use the provider MCP enroll path" >&2
    return 1
  fi
}

enroll_one "opute-ha-a" "container:local:opute-ha-a" || true
enroll_one "opute-ha-b" "container:local:opute-ha-b" || true