# @opute/host-agent

Downloads and launches the checksum-verified Opute Go host agent as a local
**Streamable HTTP** MCP server. Cursor, VS Code, and Claude Desktop snippets
below are copy/paste examples, not named-client certifications.

## Quick start

```bash
# Optional but recommended for authenticated /mcp calls
export MCP_AUTH_TOKEN=dev-token
# Optional; defaults to local-host-agent
export OPUTE_REMOTE_AGENT_ID=local-host-agent

npx -y @opute/host-agent start --background
npx -y @opute/host-agent url   # http://127.0.0.1:3014/mcp
npx -y @opute/host-agent status
npx -y @opute/host-agent stop
```

Point your MCP client at the printed URL (default port **3014**). When
`MCP_AUTH_TOKEN` is set, send it as a Bearer token:

```json
{
  "servers": {
    "oputeLocal": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp",
      "headers": {
        "Authorization": "Bearer dev-token"
      }
    }
  }
}
```

stdio MCP transport is not supported.

Safe first-run sequence once the client is connected: `tools/list` → read-only
host/VM inspection tools (for example `get_host_info`, `list_vms`). Mutating
tools stay denied until `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`.

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
| `OPUTE_REMOTE_AGENT_ID` | Canonical agent id (default `local-host-agent`) |
| `MCP_AUTH_TOKEN` | Bearer bootstrap token for `/mcp` |
| `OPUTE_HOST_AGENT_BINARY` | Use a local binary instead of downloading a release |
| `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` | Enable mutating infrastructure tools |
| `OPUTE_STANDALONE_STATE_DIR` | Local SQLite / operation journal directory |

The launcher deliberately strips most other `OPUTE_*` variables so a
Platform-enrolled shell cannot leak enrollment secrets into standalone.

For development, set `OPUTE_HOST_AGENT_BINARY` to a locally built binary
(`make build` → `dist/opute-host-agent`). Released packages download the
matching versioned artifact and verify it against the release `SHA256SUMS`
manifest.

## Platform support

Linux x64 and arm64 only. Native Windows and macOS are not supported by the
Incus provider. On Windows, run the Linux binary inside WSL and point the
Windows MCP client at `http://127.0.0.1:3014/mcp` (enable WSL localhost
forwarding / portproxy as needed).
