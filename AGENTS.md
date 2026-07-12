# Agent guide — Opute host agent (Go)

Canonical Go host agent for [Opute](https://github.com/opute-io/opute). Module: `github.com/opute-io/host-agents`.

## Quick links

| Topic | Location |
|-------|----------|
| Build, CI, Phase 1/3 validation | [README.md](README.md) |
| **DDNS vs Cloudflare Tunnel** (when to use which; `blog.opute.io`; conflicts) | [docs/ddns-vs-cloudflare-tunnel.md](docs/ddns-vs-cloudflare-tunnel.md) |
| Exposure tunnel ops (Go) | `internal/ops/exposure_tunnel.go`, `internal/ops/exposure_tunnel_windows.go` |
| Tool schemas / catalog | `schemas/all-tools.json`, `internal/tools/catalog.go` |
| Opute monorepo host exposure + MCP plugins | `../opute/AGENTS.md` (Host public exposure) |

## Host public exposure

The host agent runs **`cloudflared`** and local exposure probes on the **execution host** — the machine where `localTarget` (e.g. `http://127.0.0.1:80`) is reachable. Catalog-excluded tools: `ensure_cloudflared_tunnel`, `probe_host_exposure`, `remove_host_exposure`, `ensure_host_firewall_rule`.

**DNS modes:** Tunnel exposure uses **CNAME** to Cloudflare Tunnel, not dynamic DNS A records. Do not run a DDNS updater on the same hostname as an active tunnel binding. See **[docs/ddns-vs-cloudflare-tunnel.md](docs/ddns-vs-cloudflare-tunnel.md)** for use cases, the `blog.opute.io` release path, and conflict avoidance.

After Go or schema changes: `cd ../opute && bun run build:host-agent`, then restart dev stack (`dev:stack:down && dev:stack:up`).

## Production host on this Windows machine

The production agent runs inside the default WSL2 distro as the persistent user service
`opute-host-agent.service`; do not start the Windows binary for this deployment. The
local production CPC companion is `opute-platform-opute-stack.service` (ports `919x`).

```powershell
wsl -e bash -lc 'systemctl --user start opute-platform-opute-stack.service'
wsl -e bash -lc 'systemctl --user start opute-host-agent.service'
wsl -e bash -lc 'systemctl --user is-active opute-host-agent.service; journalctl --user -u opute-host-agent.service -n 20 --no-pager'
```

Verify a `reverse tunnel connected` log line and that `http://127.0.0.1:9191/health`
answers from WSL. `opute-host-agent-tunnel-watchdog.timer` should remain active. If
the agent logs `Unauthorized agent tool 'host_agent_heartbeat'`, treat it as an
onboarding-token mismatch: `MCP_AUTH_TOKEN` must be the per-host `opha_*` token, not
the CPC bearer (see `../opute/AGENTS.md`, **Host Agent Registration And Heartbeat**).

## Provider / catalog

- Provider abstraction: `internal/provider`
- Incus catalog: `schemas/incus-tools.json`; full export: `schemas/all-tools.json`
- Schema export from monorepo: `cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas`
