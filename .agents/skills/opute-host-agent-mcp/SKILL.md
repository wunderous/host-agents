---
name: opute-host-agent-mcp
description: >-
  Operate infrastructure through Opute Host Agent Streamable HTTP MCP as an
  agentic client. Use when calling Host Agent tools, connecting Cursor/Claude
  to :3014/:3004 /mcp, running recipes/plans, apply_manifest, Incus guests,
  K3s, tunnels, npx @opute/host-agent, or when the user mentions Host Agent MCP,
  tools/list, OPUTE_REMOTE_AGENT_ID, or MCP_AUTH_TOKEN against a host agent.
---

# Use Opute Host Agent (agentic MCP)

You are a **client** of the Host Agent execution plane. Discover tools, validate
args against schemas, call named tools. Do **not** invent shell folklore or
bypass MCP with ad-hoc `kubectl` / `incus` when a typed tool exists.

Public docs: https://www.opute.io/docs/ · https://www.opute.io/llms.txt  
Not Platform: `platform.opute.io` / `mcp.opute.io` are a different surface.

## Connect

| Mode | Default URL | Typical token |
|------|-------------|---------------|
| Standalone (laptop/IDE) | `http://127.0.0.1:3014/mcp` | `MCP_AUTH_TOKEN` (e.g. `dev-token`) |
| Platform-enrolled | `http://127.0.0.1:3004/mcp` (or enrolled URL) | host-issued `oha_*` |

- Transport: **Streamable HTTP only** (no stdio).
- Auth: `Authorization: Bearer <token>` when a bootstrap token is set.
- Probe: unauthenticated `GET /health`, then authenticated `tools/list`.

### Direct access to a same-machine instance

If Codex has no preconfigured MCP connector, the local Host Agent may still be
running. Use its local Streamable HTTP endpoint as an MCP client. The calling
shell may not inherit the service environment. Inspect the listener process
and its `OPUTE_HOST_AGENT_ENV_FILE`
path; enrolled instances use a protected per-instance `host-agent.env` under
`~/.config/opute/instances/<instance>/`. That file/process environment can hold
`MCP_AUTH_TOKEN` and the exact `OPUTE_REMOTE_AGENT_ID` even when those variables
are unset in the calling shell. Use the live service values without printing,
logging, committing, or hardcoding the token. Do not guess the instance ID.

The Host Agent uses the stateless `2026-07-28` MCP request contract. Each JSON-RPC
request must include `params._meta` with
`io.modelcontextprotocol/protocolVersion`, `io.modelcontextprotocol/clientInfo`,
and `io.modelcontextprotocol/clientCapabilities`. The
`MCP-Protocol-Version` and `Mcp-Method` HTTP headers must match the body; for
`tools/call`, `Mcp-Name` must match `params.name`.

Example `tools/list` request metadata (send with `Accept:
application/json, text/event-stream`, `Content-Type: application/json`, and the
runtime Bearer token):

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/list",
  "params": {
    "_meta": {
      "io.modelcontextprotocol/protocolVersion": "2026-07-28",
      "io.modelcontextprotocol/clientInfo": { "name": "codex", "version": "1" },
      "io.modelcontextprotocol/clientCapabilities": {}
    }
  }
}
```

Send matching headers `MCP-Protocol-Version: 2026-07-28` and
`Mcp-Method: tools/list`. A `401` indicates an auth problem; HTTP `400` with
`HeaderMismatch` indicates missing or mismatched request metadata. After a
successful fresh catalog request, call the exact listed tool and validate its
schema before acting.

Cursor / Claude Desktop shape:

```json
{
  "mcpServers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp",
      "headers": { "Authorization": "Bearer dev-token" }
    }
  }
}
```

Local launcher:

```bash
export MCP_AUTH_TOKEN=dev-token
npx -y @opute/host-agent start --background
npx -y @opute/host-agent url   # http://127.0.0.1:3014/mcp
```

## Mandatory workflow

1. **Discover** — `tools/list` or `get_capability_catalog`. Catalogs are revisioned; never treat memorized names as authority.
2. **Read first** — Prefer `get_host_info`, `list_vms`, `list_pods`, `get_k8s_resource` before mutations.
3. **Check gates** — Standalone mutations need `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`. Platform mode uses enrollment policy.
4. **Call by catalog name** — Exact tool id + schema-valid args. No free-form shell as a substitute.
5. **Handle tasks** — Long ops may return a task envelope; poll until terminal (use the repo MCP task client when available).
6. **Verify** — Structured result + follow-up read (pods Ready, recipe run status). HTTP 200 alone is not success.

## Recipes vs plans (ELI5)

- **Recipe** = cookbook page (pins, inputs, family).
- **Plan** = cooking steps; only `plan.Runner` executes.
- Host-local dogfood: `run_host_local_recipe` (mutating runs need `sha256`).
- Inspect host-local runs with `get_host_plan_run`.
- Why + field catalog: https://www.opute.io/docs/recipes/ · https://www.opute.io/docs/recipe-primitives/

## Safety invariants

- Canonical id: `OPUTE_REMOTE_AGENT_ID` on the process — do not invent/guess host ids.
- Resource URIs are `type:tenant:id` (e.g. `cluster:local:opute-ha-a`).
- Write-only fields return `[redacted]` — cannot paste back; resume in-process (e.g. `manageHostConnector: true`).
- Do not confuse Host Agent MCP admin with public dogfood hostnames (`opute.io`).
- Do not enable `OPUTE_MCP_PREFIX_TOOL_NAMES` on Platform-enrolled agents.

The production docs-site recipe and manifest are owned by private repository wunderous/opute-site-deploy; the public source repository only builds and publishes the static image.

## Anti-patterns

| Don't | Do |
|-------|-----|
| `kubectl` / `incus` for work covered by tools | Call the typed MCP tool |
| Memorize tool lists from old chats | Fresh `tools/list` |
| Paste Platform `:3004` into standalone laptop config | Use `:3014` for standalone |
| Submit Platform-distributed recipes to HA | Host-local only on HA; distributed → Platform |
| Treat site reachability as etcd HA | Keep claims separate |

## Quick troubleshooting

| Symptom | Fix |
|---------|-----|
| HTTP 401 | Matching Bearer vs `MCP_AUTH_TOKEN` |
| Nothing listening | Check mode/port; `GET /health` |
| Mutations denied | `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` (standalone) |
| Recipe refused | No `wait`/multi-host on host-local; need `sha256` to mutate |

Full table: https://www.opute.io/docs/troubleshooting/

## Progressive disclosure

- Tool groups & naming: [reference.md](reference.md)
- Public docs index: https://www.opute.io/docs/
- OpenAPI (HTTP edge only): https://www.opute.io/openapi.json
- Cordis / boundaries (editing HA code): skills `cordis-go`, `host-agent-boundaries`
- Platform enrollment / dogfood rolls: sibling Opute skill `host-agent` (different skill)
