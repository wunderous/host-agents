# Opute Host Agent (Go)

Go implementation of the Opute host agent (replaces `@opute/mcp-host-agent`).

## Standalone local MCP server

The standalone profile is an independently runnable local MCP server for Linux
with Incus. It uses **Streamable HTTP** and does not require Opute Platform,
Bridge, an onboarding token, or a reverse tunnel. Mutations are denied by
default. Default listen address is `http://127.0.0.1:3014/mcp`.

Run a local build:

```bash
OPUTE_INFRA_PROVIDER_ID=incus \
OPUTE_STANDALONE_STATE_DIR="$HOME/.opute/standalone" \
./dist/opute-host-agent --mode standalone --transport http
```

Or via the npm helper:

```bash
npx -y @opute/local-host-agent start --background
npx -y @opute/local-host-agent url   # http://127.0.0.1:3014/mcp
```

Recommended client configuration:

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

VS Code uses the `servers` shape above. Claude Desktop and Cursor use this
equivalent `mcpServers` entry:

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

Start the agent before connecting the client (`start` / `start --background`).
Set `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` in the agent process environment
only when infrastructure changes are intended. On Windows, run the Linux
binary inside WSL and point the Windows MCP client at the forwarded HTTP URL.
A WSL environment file can be supplied with the launcher's `--env-file`
argument when starting the agent.

The safe first run is: `initialize` → `check_local_prerequisites` →
`get_local_status` → `list_vms`. The stable MVP claim covers Incus inspection
and VM lifecycle; K3s, PostgreSQL/SQL, and Cloudflare Tunnel tools are
experimental until their end-to-end release gates pass.

- **Repository:** https://github.com/wunderous/host-agents
- **Go module:** `github.com/wunderous/host-agents`
- **Platform monorepo:** sibling checkout at `../opute-host-agent` when developing against [opute](https://github.com/opute-io/opute)

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
make test
make artifacts   # host-agent-linux-x64.gz, host-agent-linux-arm64.gz
```

Release artifacts match platform onboarding names: `host-agent-linux-x64.gz` and `host-agent-linux-arm64.gz`.

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

The release attaches both `.gz` binaries and a `SHA256SUMS` manifest. Download from the public GitHub Release or use the npm launcher:

```bash
gh release download v0.2.0 --repo wunderous/host-agents
```

Unauthenticated `curl` to GitHub release URLs returns **404**.

### Verify a release install

```bash
export RELEASE_TAG=v0.1.0          # optional; defaults to v0.1.0
bash scripts/verify-release-install.sh
```

Downloads the release artifact, verifies its checksum, installs to a temp path, starts the agent, checks `/health`, MCP `initialize` / `tools/list`, and confirms unauthenticated `/mcp` returns **401**.

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
  -H "Accept: application/json, text/event-stream" \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"client","version":"1.0.0"}}}' \
  http://127.0.0.1:3004/mcp
```

`/health` is always open. `/mcp` requires `Authorization: Bearer <token>` when any of these env vars are set: `MCP_AUTH_TOKEN`, `BRIDGE_TOKEN`, `OPUTE_BRIDGE_TOKEN`, `OPUTE_REMOTE_AGENT_AUTH_TOKEN`, `OPUTE_CPC_TOKEN`. Omit all of them only for local dev without auth.

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
npx -y @opute/local-host-agent start --background
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

Then run `bun scripts/validate-go-host-agent-phase3.ts` from Linux/WSL. Default dev token is **`dev-token`** (aligned with port-guard / `BRIDGE_TOKEN` / `OPUTE_CPC_TOKEN` in the opute repo).

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
