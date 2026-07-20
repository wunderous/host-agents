# @opute/local-host-agent

Launches the checksum-verified Opute Go host agent as a local stdio MCP server for VS Code, Claude Desktop, Cursor, and other MCP clients.

For development, set `OPUTE_HOST_AGENT_BINARY` to a locally built binary. Released packages download the matching versioned artifact and verify it against the release `SHA256SUMS` manifest. `OPUTE_HOST_AGENT_SHA256` may be supplied to pin a development or mirror artifact explicitly.

Mutating local infrastructure operations are disabled unless the launcher environment contains:

```text
OPUTE_STANDALONE_ALLOW_MUTATIONS=true
```

The stable MVP claim covers local Incus inspection and VM lifecycle. K3s,
PostgreSQL, SQL execution, and Cloudflare Tunnel tools are exposed as
experimental capabilities and require the same explicit mutation opt-in.

The launcher supports Linux x64 and arm64. Native Windows and macOS are not supported by the Incus provider; Windows users should launch the Linux binary inside WSL. The launcher does not install dependencies or run a postinstall hook.

For a local development binary, set `OPUTE_HOST_AGENT_BINARY`. Released
artifacts are fetched over HTTPS (or an explicitly configured mirror), checked
against `SHA256SUMS`, cached atomically, and re-verified on every launch.
