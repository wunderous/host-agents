# ADR 0012: Optional MCP tool-name prefix from canonical agent ID

Status: accepted

Date: 2026-08-30

Decision record: `../opute/.agents/decisions/host-agent-mcp-tool-name-prefix.json`

Bounds an exception to [ADR 0009](0009-tool-contract-conformance-and-catalog-authority.md) decision #2. Identity remains [ADR 0006](0006-canonical-host-agent-identity.md).

## Context

Every Host Agent advertises the same catalog names (`provision_vm`, `list_vms`, …) and the same MCP `Implementation.Name` (`host-agent`). Clients that flatten tools across MCP servers (Cursor, DSH) collide when two agents are added in one session. Co-resident agents on one machine already have distinct `OPUTE_REMOTE_AGENT_ID` values; the host fingerprint must not become a second namespace.

Opute Platform calls enrolled kernels with unprefixed catalog names. Prefixing the default `tools/list` would break that path.

## Decision

1. **`OPUTE_MCP_PREFIX_TOOL_NAMES` remains, default off.** The binary does not prefix unless the instance env opts in. Platform-enrolled instances keep the flag off.
2. **Prefix source.** UUID v5 of `OPUTE_REMOTE_AGENT_ID` under DNS name `opute.host-agent.mcp-tool-prefix`. Wire prefix = first 8 lowercase hex characters. The function takes only the agent ID. Fingerprint, hostname, instance ID, and display name are forbidden inputs.
3. **Wire projection, not a catalog rename.** Catalog `Name` / `OperationID`, dispatch, and capability edges stay unprefixed. `tools/list` publishes `{prefix}_{catalogName}` with a single `_` separator (never `__`).
4. **Prefixed-only listing.** When the flag is on, unprefixed catalog names are not registered on `tools/list`. The go-sdk `CallTool` surface matches the prefixed wire name. HTTP `tools/call` may rewrite a catalog name to that wire name after `Mcp-Name` / body agreement so an operator who opts in does not break Platform callers. The alias is never listed.
5. **Server identity.** When the flag is on, `Implementation.Name` and `server/discover` `serverInfo.name` are `host-agent-{prefix}`. `GET /health` advertises `mcpToolNamePrefix` only when the flag is on.
6. **Not a routing key.** C-21 is unchanged. The prefix is a publication projection of the canonical agent ID, never a substitute for it.

## Invariant delta

### Preserve

- C-21 canonical identity — the prefix is a publication projection, not a routing key.
- ADR 0009 catalog authority — dispatch keys remain catalog names.
- Platform enrollment wire names — install scripts do not set the flag.

### Amend

- ADR 0009 C-18: `tools/list` equals catalog names by default; this flag is the listed exception.

### Add

- Two agents with distinct `OPUTE_REMOTE_AGENT_ID` values advertise disjoint `tools/list` names when the flag is on.

## Consequences

- Cursor multi-agent: set `OPUTE_MCP_PREFIX_TOOL_NAMES=true` and `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE=true`, and use a unique `mcp.json` key such as `host-agent-{prefix}`.
- Do not enable the flag on Platform-enrolled instances.
- DSH attaches a prefix-on `oha_` kernel only and skips unprefixed catalogs. It must not send `opsess_` / `opha_` / `opit_` to Host Agent `/mcp`.
