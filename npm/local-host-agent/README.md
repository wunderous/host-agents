# @opute/host-agent

Downloads and launches the checksum-verified Opute Go host agent as a local
**Streamable HTTP** MCP server compatible with standards-compliant MCP
clients. Cursor, VS Code, and Claude Desktop configurations below are
copy/paste examples, not named-client certifications.

## Quick start

```bash
# Start in the foreground (logs on stderr)
npx -y @opute/host-agent start

# Or run as a background daemon and print the MCP URL
npx -y @opute/host-agent start --background
npx -y @opute/host-agent status
npx -y @opute/host-agent stop
```

Point your MCP client at the printed URL (default port **3014**):

```json
{
  "servers": {
    "oputeLocal": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp"
    }
  }
}
```

stdio MCP transport is not supported.

The exact first-run flow is `initialize` → `tools/list` →
`check_local_prerequisites` → `get_local_status` → `list_vms` (VM inventory).

## Commands

| Command | Description |
|---------|-------------|
| `start` | Start standalone Streamable HTTP (foreground by default) |
| `start --background` / `-d` | Daemonize, write pid state, print MCP URL |
| `stop` | Stop a background daemon started by this launcher |
| `status` | JSON status (`running`, `healthy`, `url`) |
| `url` | Print the MCP URL |

## Environment

| Variable | Purpose |
|----------|---------|
| `HOST_MCP_PORT` | Listen port (default `3014`) |
| `HOST_MCP_BIND_HOST` | Bind host (default `127.0.0.1`) |
| `OPUTE_HOST_AGENT_BINARY` | Use a local binary instead of downloading a release |
| `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` | Enable mutating infrastructure tools |
| `OPUTE_STANDALONE_STATE_DIR` | Local SQLite / operation journal directory |

For development, set `OPUTE_HOST_AGENT_BINARY` to a locally built binary.
Released packages download the matching versioned artifact and verify it
against the release `SHA256SUMS` manifest. `OPUTE_HOST_AGENT_SHA256` may be
supplied to pin a development or mirror artifact explicitly.

The stable MVP claim covers local Incus inspection and VM lifecycle. K3s,
PostgreSQL, SQL execution, and Cloudflare Tunnel tools are exposed as
experimental capabilities and require the same explicit mutation opt-in.

The launcher supports Linux x64 and arm64. Native host execution is Linux-only;
native Windows and macOS are not supported by the Incus provider. Windows users
should run the Linux binary inside WSL and connect from the Windows MCP client
over HTTP to `http://127.0.0.1:3014/mcp` (with WSL port forwarding as needed).
The launcher does not install dependencies or run a postinstall hook.
