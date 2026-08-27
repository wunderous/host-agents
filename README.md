# Opute Host Agent (Go)

Go implementation of the Opute host agent (replaces `@opute/mcp-host-agent`).

## Standalone Go experience

`opute-host-agent` is a server-only MCP Host Agent. The deterministic TUI is a
separate provider-neutral Bun application in the sibling Opute repository at
`apps/opute-tui/` and communicates with this server over Streamable HTTP MCP.
Mutations remain denied by default.

Build and launch the standalone server:

```bash
make build
OPUTE_INFRA_PROVIDER_ID=incus \
OPUTE_STANDALONE_STATE_DIR="$HOME/.opute/standalone" \
./dist/opute-host-agent

# The bare invocation is also server-only; it never launches or discovers a TUI.
OPUTE_INFRA_PROVIDER_ID=incus ./dist/opute-host-agent
```

The same binary also supports explicit modes:

```bash
# MCP server only, for external clients; default HTTP endpoint is :3014/mcp
./dist/opute-host-agent serve --mode standalone --transport http

# Run the separate TUI from the sibling Opute checkout when desired:
# bun --cwd ../opute/apps/opute-tui run start -- --url http://127.0.0.1:3014/mcp
```

Or via the npm helper:

```bash
npx -y @opute/host-agent start --background
npx -y @opute/host-agent url   # http://127.0.0.1:3014/mcp
```

Generic Streamable HTTP client configuration:

```json
{
  "servers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp"
    }
  }
}
```

The separate TUI discovers the server's revisioned capability catalog, parses
typed commands, resolves explicit authorized entity references, validates
arguments against the catalog schema, and executes one MCP call per submitted
command. It does not infer operations from prose or require an LLM.

The following are verified Streamable HTTP MCP client examples for VS Code,
Claude Desktop, and Cursor (gate: `opute/scripts/validate-standalone-mcp-client.ts`
→ `tmp/bootstrap-m1/summary.json`). They are not named-product certifications beyond
that `server/discover` / `tools/list` / read-only tool canary.

Claude Desktop and Cursor use this equivalent `mcpServers` entry:

```json
{
  "mcpServers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp"
    }
  }
}
```

Bootstrap helper (WSL, does not touch production `~/.config/opute/host-agent.env`):

```bash
# from opute checkout
./scripts/start-standalone-bootstrap-agent.sh
# then from Windows Cursor, point MCP at http://127.0.0.1:3014/mcp
# (enable localhostForwarding / WSL portproxy if needed)
OPUTE_HOST_AGENT_MCP=http://127.0.0.1:3014/mcp bun scripts/validate-standalone-mcp-client.ts
```

Start the agent before connecting the client (`start` / `start --background`).
Set `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` in the agent process environment
only when infrastructure changes are intended. On Windows, run the Linux
binary inside WSL and point the Windows MCP client at the forwarded HTTP URL.
A WSL environment file can be supplied with the launcher's `--env-file`
argument when starting the agent.

The exact safe first run is: `server/discover` → `tools/list` →
`check_local_prerequisites` → `get_local_status` → `list_vms` (VM inventory).
The stable MVP claim covers generic Streamable HTTP behavior, Incus inspection,
and VM lifecycle; K3s, PostgreSQL/SQL, and Cloudflare Tunnel tools are
experimental until their end-to-end release gates pass. Native host execution
is Linux-only; Windows users must run the server inside WSL.

- **Repository:** https://github.com/wunderous/host-agents
- **Go module:** `github.com/wunderous/host-agents`
- **Platform monorepo:** sibling checkout at `../opute` when developing against [opute](https://github.com/opute-io/opute)

## Phases

| Phase | Platform | Provider | Validation (from `opute/`) |
| ----- | -------- | -------- | --------------------------- |
| **1** | Linux / WSL | Incus | `bun scripts/validate-go-host-agent-phase1.ts` |
| **3** | Linux / WSL + dev stack | Incus | `bun scripts/validate-go-host-agent-phase3.ts` |

Phase 1 validates the agent in **isolation** (direct HTTP MCP). Phase 3 wires into dev-orchestrator, reverse tunnel, and onboarding.

## Build

From `opute/`:

```bash
bun run build:host-agent
```

Or from this directory:

```bash
make build
# builds the server-only dist/opute-host-agent
make test
make artifacts   # host-agent-linux-x64.gz, host-agent-linux-arm64.gz
make standalone-http-smoke
make standalone-lifecycle-gate   # explicit Incus integration gate
npm --prefix npm/local-host-agent test
```

Release artifacts use the platform onboarding names
`host-agent-linux-x64.gz` and `host-agent-linux-arm64.gz`. Each artifact
contains only the canonical server binary. The `opute` TUI is published and
verified separately by the sibling Opute repository.

## CI and releases

GitHub Actions:

| Workflow | Trigger | What it does |
| -------- | ------- | ------------ |
| **CI** (`.github/workflows/ci.yml`) | PR / push to `main` | `gofmt`, `go vet`, `go test`, `make artifacts` |
| **Publish** (`.github/workflows/publish.yml`) | push to `main`, `v*` tags, manual | build + upload artifacts; **GitHub Release** on version tags |

Publish a release:

```bash
git tag v0.2.0
git push origin v0.2.0
```

The release attaches the host-agent `.gz` binaries plus a `SHA256SUMS` manifest.
Download from the public GitHub Release or use the npm launcher:

```bash
gh release download v0.2.0 --repo wunderous/host-agents
```

Unauthenticated `curl` to GitHub release URLs returns **404**.

### Verify a release install

```bash
export RELEASE_TAG=v0.1.1          # optional; defaults to v0.1.1
bash scripts/verify-release-install.sh
```

Downloads the release artifact, verifies its checksum, installs to a temp path, starts the agent, checks `/health`, MCP `server/discover` / `tools/list`, and confirms unauthenticated `/mcp` returns **401**.

## Run (HTTP mode — Phase 1 local testing)

**Linux + Incus:**

```bash
export HOST_MCP_PORT=3004
export MCP_AUTH_TOKEN=dev-token
export OPUTE_INCUS_BINARY_PATH=/usr/bin/incus
export OPUTE_INFRA_PROVIDER_ID=incus
./dist/host-agent-linux-x64
```

Call MCP with a Bearer token:

```bash
curl -H "Authorization: Bearer dev-token" \
  -H "Content-Type: application/json" \
  -H "MCP-Protocol-Version: 2026-07-28" \
  -H "Mcp-Method: tools/list" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"client","version":"1.0.0"},"io.modelcontextprotocol/clientCapabilities":{}}}}' \
  http://127.0.0.1:3004/mcp
```

`/health` is always open. `/mcp` requires a host-issued bootstrap token (`MCP_AUTH_TOKEN` / `oha_*`). Product tokens (`opha_*`, `opsess_*`, `opit_*`) are rejected.

## VS Code / external MCP configuration

The agent accepts configuration from the process environment, an env file, or repeatable CLI overrides. Precedence is CLI `--env KEY=VALUE`, then variables already present in the process, then values loaded from `--env-file` / `OPUTE_HOST_AGENT_ENV_FILE`.

Prefer starting the standalone agent separately, then point VS Code at the
HTTP URL. Secrets for mutations belong in the agent process environment (or
`--env-file`), not in the MCP client spawn config:

```json
{
  "servers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp"
    }
  }
}
```

```bash
OPUTE_STANDALONE_ALLOW_MUTATIONS=true \
CLOUDFLARE_API_TOKEN=… \
npx -y @opute/host-agent start --background
```

For a reusable local file, use `--env-file /path/to/opute-host-agent.env` when
starting the binary. For one-off non-secret overrides, use
`--env OPUTE_INFRA_PROVIDER_ID=incus --env HOST_MCP_PORT=3014`. Environment
variables are inherited by host operations and Cloudflare tooling; never put
long-lived secrets directly in command-line arguments because process listings
can expose them. A Cloudflare API token configures account/API operations; a
Cloudflare Tunnel connection still requires the per-tunnel `runToken` passed
to the relevant tunnel tool.

On WSL hosts, set `OPUTE_CLOUDFLARED_MODE=wsl` to run a native Linux `cloudflared` binary beside Incus; optionally set `OPUTE_CLOUDFLARED_BINARY_PATH` to its absolute path. This is useful when the Windows artifact cannot execute or when the tunnel origin is only reachable inside WSL. Leave the mode unset to retain the Windows-cloudflared delegation path.

## Dev stack (Phase 3)

With `bun run dev` in `opute/`:

```bash
bun scripts/dev-host-mcp.ts
```

Then run `bun scripts/validate-go-host-agent-phase3.ts` from Linux/WSL. Default dev token is **`dev-token`** (aligned with port-guard / `MCP_AUTH_TOKEN` / `OPUTE_CPC_TOKEN` in the opute repo).

## Production install

Remote hosts are onboarded through the Opute platform UI (**Connect Remote Host**). The generated install script:

1. Downloads the binary from the **platform** artifact URL (session + `opit_*` install token) — not directly from GitHub releases
2. Writes `host-agent.env` with CPC bearer, per-host `opha_*` token, MCP/WS URLs, and `OPUTE_REVERSE_TUNNEL=true`
3. Starts `opute-host-agent.service` (or user-level equivalent)

GitHub releases are for CI distribution and manual smoke testing. Production credentials are issued by the platform during onboarding.

## Provider abstraction

The agent uses `internal/provider` for provider ID normalization, CLI `Runtime`, and per-provider tool catalogs (`schemas/incus-tools.json`). Linux-only today (Incus); additional providers can plug in via new catalog JSON and inventory/launch ops without changing the MCP surface.

## Documentation

- **[AGENTS.md](AGENTS.md)** — agent-oriented guide (build, exposure, catalog pointers).
- **[docs/ddns-vs-cloudflare-tunnel.md](docs/ddns-vs-cloudflare-tunnel.md)** — when to use dynamic DNS vs Cloudflare Tunnel; why they conflict on the same hostname; `blog.opute.io` tunnel path.

## Schema export

When tool schemas change in the opute monorepo:

```bash
cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas
```
