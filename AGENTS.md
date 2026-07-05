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

## Provider / catalog

- Provider abstraction: `internal/provider`
- Incus catalog: `schemas/incus-tools.json`; full export: `schemas/all-tools.json`
- Schema export from monorepo: `cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas`
