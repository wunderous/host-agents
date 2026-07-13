# @opute/local-host-agent

Launches the signed Opute Go host agent as a local stdio MCP server for VS Code, Cursor, and other MCP clients.

For development, set `OPUTE_HOST_AGENT_BINARY` to a locally built binary. Released packages download the matching versioned artifact and can enforce `OPUTE_HOST_AGENT_SHA256`.

Mutating local infrastructure operations are disabled unless the launcher environment contains:

```text
OPUTE_STANDALONE_ALLOW_MUTATIONS=true
```

The launcher does not install dependencies or run a postinstall hook.
