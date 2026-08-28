#!/usr/bin/env bash
set -euo pipefail

# This is intentionally fail-closed. A provider reset is destructive and may
# only run against an explicitly disposable environment held by the existing
# coordination lease. The actual teardown must be supplied by typed provider
# teardown operations; arbitrary shell hooks are not accepted.

if [[ "${OPUTE_DISPOSABLE_ENV:-}" != "1" ]]; then
  echo "provider-reset-chat-e2e: set OPUTE_DISPOSABLE_ENV=1" >&2
  exit 2
fi
if [[ -z "${OPUTE_AGENT_WORK_COORDINATION_LEASE:-}" ]]; then
  echo "provider-reset-chat-e2e: OPUTE_AGENT_WORK_COORDINATION_LEASE is required" >&2
  exit 2
fi
if [[ "${OPUTE_PROVIDER_RESET_CONFIRM:-}" != "I-OWN-THIS-DISPOSABLE-ENV" ]]; then
  echo "provider-reset-chat-e2e: set OPUTE_PROVIDER_RESET_CONFIRM=I-OWN-THIS-DISPOSABLE-ENV" >&2
  exit 2
fi

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
evidence_dir="${OPUTE_PROVIDER_RESET_EVIDENCE_DIR:-${root_dir}/tmp/provider-reset-chat-e2e/$(date -u +%Y%m%dT%H%M%SZ)}"
mkdir -p "$evidence_dir"

# Inventory is deliberately observational and redacted. It does not resolve
# symlinks, stop processes, read credential contents, or delete paths.
{
  printf 'provider-reset-chat-e2e inventory\n'
  printf 'lease=%s\n' "${OPUTE_AGENT_WORK_COORDINATION_LEASE}"
  printf 'host=%s\n' "$(hostname)"
  date -u +%FT%TZ
  command -v ollama || true
  command -v cloudflared || true
  systemctl --user is-active ollama.service 2>/dev/null || true
  systemctl --user is-enabled ollama.service 2>/dev/null || true
  systemctl --user is-active opute-cloudflare-tunnel.service 2>/dev/null || true
  curl --max-time 3 --silent --show-error http://127.0.0.1:11434/api/version 2>/dev/null | sed -E 's/(token|password|secret|key)[^,}]*/\1:[redacted]/Ig' || true
  curl --max-time 3 --silent --show-error http://127.0.0.1:8080/health 2>/dev/null | sed -E 's/(token|password|secret|key)[^,}]*/\1:[redacted]/Ig' || true
} >"${evidence_dir}/baseline-inventory.txt"

if [[ "${OPUTE_PROVIDER_RESET_MODE:-}" != "validate-only" ]]; then
  cat >&2 <<'EOF'
provider-reset-chat-e2e: typed provider teardown is not available in this build.
No reset was attempted. Add provider-owned teardown operations and rerun with
OPUTE_PROVIDER_RESET_MODE=validate-only only for non-destructive canaries.
EOF
  exit 3
fi

if [[ -z "${OPUTE_LOCAL_CHAT_URL:-}" || -z "${OPUTE_PUBLIC_CHAT_URL:-}" ]]; then
  echo "provider-reset-chat-e2e: validate-only mode requires OPUTE_LOCAL_CHAT_URL and OPUTE_PUBLIC_CHAT_URL" >&2
  exit 2
fi

# Keep the integration proof in the application repository, where the shared
# SSE and trace parsers live. It is invoked only after the explicit gates above
# and secrets are sourced by that repository's own resolver.
opute_root="${OPUTE_ROOT:-${root_dir}/../opute}"
bun_path="${OPUTE_BUN_PATH:-/home/houman/.bun/bin/bun}"
if [[ ! -x "$bun_path" ]]; then
  echo "provider-reset-chat-e2e: Bun runtime not found at configured path" >&2
  exit 2
fi

OPUTE_WEB_URL="$OPUTE_LOCAL_CHAT_URL" "$bun_path" "$opute_root/scripts/validate-chat-host-llm.ts" >"${evidence_dir}/local-chat.txt"
OPUTE_WEB_URL="$OPUTE_PUBLIC_CHAT_URL" "$bun_path" "$opute_root/scripts/validate-chat-host-llm.ts" >"${evidence_dir}/public-chat.txt"

grep -q 'CHAT_PASS' "${evidence_dir}/local-chat.txt"
grep -q 'CHAT_PASS' "${evidence_dir}/public-chat.txt"
printf '%s\n' 'LOCAL_CHAT_CANARY_PASS' 'PUBLIC_CHAT_CANARY_PASS' 'PROVIDER_RESET_CHAT_E2E_PASS'
