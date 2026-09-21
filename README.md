# Opute Host Agent (Go)

Go implementation of the Opute Host Agent — a **server-only** Streamable HTTP
MCP process that executes typed infrastructure assignments on a Linux host.

- **Repository:** https://github.com/wunderous/host-agents
- **Go module:** `github.com/wunderous/host-agents`
- **npm launcher:** [`@opute/host-agent`](./npm/local-host-agent) (`npm/local-host-agent/`)
- **Platform monorepo:** sibling checkout at `../opute` when developing against [opute](https://github.com/opute-io/opute)

This README is the operator-facing source of truth for standalone and local
dogfood. Prefer it over older blog posts or copied snippets.

## What it is (and is not)

| | Host Agent | Opute Platform |
|---|---|---|
| Surface | Streamable HTTP MCP (`/mcp`) | Web UI + control plane (`platform.opute.io`) |
| Job | Execute typed tools against this host / cluster | Intent, authorization, durable orchestration |
| Auth | Bootstrap `MCP_AUTH_TOKEN` and/or OAuth access tokens | Platform sessions |
| Default bind | Standalone: `127.0.0.1:3014` | Platform mode: `0.0.0.0:3004` |

The Host Agent does **not** infer operations from prose and does **not** require
an LLM. Clients discover the revisioned capability catalog, validate arguments
against schemas, and call tools on the public contract.

## Requirements

- Linux (or WSL2). Native Windows/macOS host execution is unsupported.
- A canonical agent id: `OPUTE_REMOTE_AGENT_ID` (**required** by `Validate()`).
- Incus for guest/VM work (`OPUTE_INFRA_PROVIDER_ID` defaults/normalizes to `incus`).
- For usable `/mcp` without an OAuth client: set `MCP_AUTH_TOKEN` and send
  `Authorization: Bearer …` from the MCP client.

`/health` is always open. `/mcp` rejects missing or invalid tokens.

## Quick start (standalone)

### Option A — from source

```bash
make build   # → dist/opute-host-agent

export OPUTE_REMOTE_AGENT_ID=local-host-agent
export OPUTE_INFRA_PROVIDER_ID=incus
export OPUTE_STANDALONE_STATE_DIR="$HOME/.opute/standalone"
export MCP_AUTH_TOKEN=dev-token   # optional but recommended

# Validate config without listening
./dist/opute-host-agent --check

# Serve (bare invocation defaults to --mode standalone --transport http)
./dist/opute-host-agent
# equivalent:
./dist/opute-host-agent serve --mode standalone --transport http
```

Default endpoint: **`http://127.0.0.1:3014/mcp`**.

### Option B — npm launcher

```bash
export MCP_AUTH_TOKEN=dev-token
# OPUTE_REMOTE_AGENT_ID defaults to local-host-agent inside the launcher
npx -y @opute/host-agent start --background
npx -y @opute/host-agent url
```

For a local binary during development:

```bash
OPUTE_HOST_AGENT_BINARY="$PWD/dist/opute-host-agent" \
  npx -y @opute/host-agent start --background
```

### MCP client configuration

Cursor / Claude Desktop (`mcpServers`):

```json
{
  "mcpServers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp",
      "headers": {
        "Authorization": "Bearer dev-token"
      }
    }
  }
}
```

VS Code-style (`servers`):

```json
{
  "servers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp",
      "headers": {
        "Authorization": "Bearer dev-token"
      }
    }
  }
}
```

Omit the `headers` block only when the agent has no bootstrap token and the
client completes OAuth for this resource. A bare URL against a token-gated
agent returns **401**.

### Mutations

Standalone mutations are **denied by default**. Enable deliberately:

```bash
export OPUTE_STANDALONE_ALLOW_MUTATIONS=true
```

### Safe first calls

Prefer read-only discovery before mutating:

1. `tools/list` (or `get_capability_catalog`)
2. `get_host_info` / `detect_host_platform`
3. `list_vms` (Incus inventory)

Do not treat memorized tool names as authoritative — always use the live catalog.

## Serve modes

| Mode | How | Default bind | Default port | Typical use |
|------|-----|--------------|--------------|-------------|
| **standalone** | bare binary, `serve --mode standalone`, or npm launcher | `127.0.0.1` | **3014** | Local IDE / laptop dogfood |
| **platform** | `serve --mode platform` or `OPUTE_AGENT_MODE=platform` | `0.0.0.0` | **3004** | Enrolled host next to Opute control plane |

Override with `HOST_MCP_BIND_HOST` and `HOST_MCP_PORT`.

Other CLI entrypoints: `public-mcp`, `recipe`, `provider`, `help`, `--check`,
`--env-file`, repeatable `--env KEY=VALUE`. Precedence: CLI `--env` → process
environment → `--env-file` / `OPUTE_HOST_AGENT_ENV_FILE` (file only fills unset keys).

### Multiple agents in one Cursor workspace

Set `OPUTE_MCP_PREFIX_TOOL_NAMES=true` (and `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE=true`
when needed) on each agent so tool names do not collide. `GET /health` then
includes `mcpToolNamePrefix`. Wire names become `{prefix}_{catalogName}`.
**Do not enable this on Platform-enrolled instances** — the control plane calls
unprefixed catalog names.

## Build and test

```bash
make build                 # dist/opute-host-agent
make test                  # go test ./...
make test-all-modules      # includes provider plugin modules
make standalone-smoke      # --check with a temp state dir
make standalone-http-smoke # packaged HTTP smoke
make npm-test              # npm/local-host-agent tests
```

From the sibling `opute/` checkout:

```bash
bun run build:host-agent
bun run validate:host-agent:phase1   # isolation / direct HTTP
bun run validate:host-agent:phase3   # wired into local opute dev stack
```

Phase validators and bootstrap helpers live under `opute/scripts/`. They still
require a valid `OPUTE_REMOTE_AGENT_ID` in the environment when they spawn the
Go binary.

## Release artifacts

```bash
make artifacts
```

Produces under `dist/`:

| Artifact | Role |
|----------|------|
| `host-agent-linux-x64.gz` | Canonical Linux amd64 server binary (gzip) |
| `host-agent-linux-arm64.gz` | Canonical Linux arm64 server binary (gzip) |
| `host-agent-windows-x64.gz` | Windows build (not a supported Incus host runtime) |
| `opute-provider-k3s-linux-x64` | K3s provider plugin |
| `opute-provider-cloudflare-linux-x64` | Cloudflare tunneling provider |
| `opute-provider-tailscale-linux-x64` | Tailscale provider |
| `SHA256SUMS` | Checksums |

`make build` is the **dev** binary (`dist/opute-host-agent`). Release `.gz`
names are what GitHub Releases and the npm downloader expect.

Publish a tagged release:

```bash
git tag v0.1.1
git push origin v0.1.1
```

Unauthenticated `curl` to private GitHub release URLs may return **404**. Prefer
`gh release download … --repo wunderous/host-agents` or the npm launcher.

## Platform / dogfood install

Production remote hosts are onboarded through the Opute platform UI (**Connect
Remote Host**). The generated install script downloads the binary from the
**platform** artifact URL, writes `host-agent.env` (including
`OPUTE_REMOTE_AGENT_ID` and `MCP_AUTH_TOKEN`), and starts the systemd unit.

GitHub releases are for CI distribution and manual smoke testing — not the
primary production credential path.

Local platform-mode dogfood (sibling `opute/` with `bun run dev`):

```bash
# from opute/
bun scripts/dev-host-mcp.ts
```

Default shared bootstrap token in that stack is **`dev-token`** (`MCP_AUTH_TOKEN`).
`OPUTE_CPC_TOKEN` is **retired** and rejected by the Go agent.

## Cloudflare / public exposure

Dedicated tunnels and DNS are owned by the Cloudflare provider plugin
(`plugins/tunneling/cloudflare`). Required API credentials for tunnel/DNS
mutation:

- `CLOUDFLARE_API_TOKEN`
- `CLOUDFLARE_ACCOUNT_ID`
- `CLOUDFLARE_ZONE_ID`

Connector run tokens are minted by
`opute.capability.tunneling.ensure-host-tunnel` (write-only in MCP results).
Do **not** put long-lived secrets on CLI argv — process listings can expose them.

This docs/marketing site is dogfooded on `opute.io` / `www.opute.io` via
`site/recipes/www-opute-io.yaml` (dedicated tunnel `opute-www-opute-io`). That
path must not mutate `platform.opute.io` / `mcp.opute.io`.

## Architecture pointers

- Composition root: `internal/hostagent`
- Shared host seam: `internal/hostruntime` (not a separate `internal/provider` package)
- Provider plugins: `plugins/**` + `contracts/provider/`
- Capability catalogs / schemas: `schemas/`
- Agent-oriented notes: [`AGENTS.md`](AGENTS.md)
- Cordis catalog guide: [`docs/cordis-development-guide.md`](docs/cordis-development-guide.md)
- Tunnel vs DDNS: [`docs/ddns-vs-cloudflare-tunnel.md`](docs/ddns-vs-cloudflare-tunnel.md)

## Schema export

When tool schemas change in the opute monorepo:

```bash
cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas
```

## Documentation site

Static docs for operators live under [`site/public/docs/`](./site/public/docs)
and are published to `https://www.opute.io/` / `https://opute.io/` via the Host
Agent recipe in `site/recipes/`. Content follows [Diátaxis](https://diataxis.fr/)
(tutorials, how-to guides, reference, explanation) and must track this README —
not the other way around.
