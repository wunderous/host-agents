# Opute Host Agent (Go)

Go implementation of the Opute host agent (replaces `@opute/mcp-host-agent`).

## Phases

| Phase | Platform | Provider | Validation |
| ----- | -------- | -------- | ---------- |
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
make artifacts   # linux-x64, linux-arm64 .gz
```

## Run (HTTP mode — Phase 1 local testing)

**Linux + Incus:**

```bash
export HOST_MCP_PORT=3004
export MCP_AUTH_TOKEN=dev-token
export OPUTE_INCUS_BINARY_PATH=/usr/bin/incus
export OPUTE_INFRA_PROVIDER_ID=incus
./dist/host-agent-linux-x64
```

## Dev stack (Phase 3)

With `bun run dev` in `opute/`:

```bash
bun scripts/dev-host-mcp.ts
```

Then run `bun scripts/validate-go-host-agent-phase3.ts` from Linux/WSL.

## Provider abstraction

The agent uses `internal/provider` for provider ID normalization, CLI `Runtime`, and per-provider tool catalogs (`schemas/incus-tools.json`). Linux-only today (Incus); additional providers can plug in via new catalog JSON and inventory/launch ops without changing the MCP surface.

