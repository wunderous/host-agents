# Agent guide — Opute host agent (Go)

Canonical Go host agent for [Opute](https://github.com/opute-io/opute). Module: `github.com/wunderous/host-agents`.

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

An explicit `hostId` is the durable execution assignment. The host agent should execute that assignment through the reverse tunnel without requiring control-plane provider rediscovery. Keep guest and provider probes bounded and cancellable so VM provisioning cannot starve heartbeats or operation polling.

## Provider / catalog

- Provider abstraction: `internal/provider`
- Incus catalog: `schemas/incus-tools.json`; full export: `schemas/all-tools.json`
- Schema export from monorepo: `cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas`

## Standalone and platform profiles

- Platform mode remains the default and owns registration, heartbeat, reverse-tunnel, and host-dispatch behavior. Preserve explicit `hostId` routing; do not rediscover providers in the execution fast path.
- Standalone mode is opt-in and must not require Opute Platform, Bridge, onboarding tokens, a reverse tunnel, or `OPUTE_MCP_URL`. Its local tool surface is implemented in `internal/tools/standalone.go`; invalid profile combinations must fail explicitly rather than silently falling back to platform mode.
- User-launched agents may configure settings through inherited environment variables, `--env-file PATH`, or repeatable `--env KEY=VALUE` flags. Precedence is CLI override, existing process environment, then env-file value. Keep secrets such as `CLOUDFLARE_API_TOKEN` in the VS Code `env` block or a permission-protected env file; do not put long-lived tokens in process arguments.
- Exposure operations run on the execution host where `localTarget` is reachable. Cloudflare tunnel tokens are sensitive and must not appear in logs, tool results, operation metadata, or metric labels.

### Standalone local workflow

- The supported client boundary is MCP Streamable HTTP (default `http://127.0.0.1:3014/mcp`). Direct development invocation is `opute-host-agent --mode standalone --transport http`; VS Code/Cursor users should start `@opute/host-agent` (or the binary) and connect with `"type": "http"` + `url`.
- Mutations are deliberately disabled unless `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` is set. Host shell and insecure-download behavior are separate opt-ins; never enable them by default in a published client configuration.
- Long-running mutations (`provision_vm`, `install_k3s`, `install_postgresql`, tunnel creation, and deletion) must return a task/operation immediately. Poll `get_operation`; do not increase the MCP request timeout or synchronously repeat the underlying provider call.
- The local journal is SQLite-backed and operation state is authoritative for standalone recovery. On restart, previously working operations reconcile to `unknown`; clients must surface that state rather than pretending the work completed.
- K3s readiness is asynchronous even after the installer exits successfully. Validate both the reported version and `readyNodes == totalNodes` before installing PostgreSQL.
- PostgreSQL validation is not complete until the deployment has a ready replica and `run_sql` successfully performs a table create/write/read cycle.
- Quick Tunnel startup must return a public URL only after Cloudflared has published it. The tunnel process must be detached from the MCP request and stopped through `delete_cloudflare_tunnel`.
- On WSL, validate that the selected `localTarget` is reachable from the Windows Cloudflared process; WSL/Windows localhost forwarding can fail independently of Cloudflare connectivity. Do not call the public tunnel lifecycle green when only Cloudflare reports `connected`.

## Release and validation

After Go, schema, or host-tool changes:

1. Run `go test ./...`.
2. From the sibling Opute checkout, run `bun run build:host-agent` and export schemas when catalog changes are involved.
3. Restart the owning WSL services only through the documented user-systemd path; do not start a second Windows binary for the same host identity.
4. Verify `opute-host-agent.service` is active, the reverse tunnel is connected, `http://127.0.0.1:9191/health` responds, and the Opute shell canary succeeds with an explicit host and VM fixture.

For standalone changes, additionally run `go test ./...`, start the Streamable HTTP server without platform credentials (`scripts/verify-standalone-http.py`), and use disposable names such as `opute-standalone-e2e-*` via `scripts/verify-standalone-lifecycle.py`. Clean those resources through standalone MCP tools and verify `incus list` contains no matching instances; preserve the production VM and platform-shaped services. A partial VM/K3s/DB/tunnel run is evidence for the first successful boundary only, not a green full-lifecycle result.

The production-shaped companion is `opute-platform-opute-stack.service` on the 919x ports. Keep it separate from the Opute dev stack on 909x. A failed heartbeat or tunnel must be diagnosed at the agent/session boundary before changing provider or VM code.
