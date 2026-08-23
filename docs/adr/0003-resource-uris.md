# ADR 0003: Tenant-scoped resource URIs

Status: Accepted for the `2026-08-host-agent-tui-redesign` cutover

## Decision

Host Agent entity identity is a canonical URI with the form
`<resource-type>:<tenant-id>:<resource-id>`. The type and tenant segments use
the bounded lowercase identifier grammar; the resource ID is opaque and may
contain colons, so parsing uses `SplitN(..., 3)`. A client may display a
friendly label, but it never fabricates or sends that label as entity identity.

The active tenant is configured by `OPUTE_TENANT_ID` (default `local`) and is
echoed by `open_assistant_session`. A URI for another tenant is rejected before
provider dispatch. Authentication and authorization remain Platform-owned;
this boundary is the Host Agent's fail-closed tenancy check.

## Registry and provider boundary

The Host Agent owns an additive SQLite `resource_registry` projection. It maps
each URI to provider coordinates, status, and timestamps. Provision/reconcile
operations register resources, removal deregisters them, and discoverable
single-key Incus resources may be lazily adopted only after the provider
confirms they exist. Compound resources require an explicit reconcile first.

Providers receive resolved typed coordinates and return observations carrying
the same URI. They do not mint tenant segments or infer identities from names.
This keeps a URI stable if the underlying compute or database provider changes.

## MCP projection distinction

MCP resource projection URIs such as `opute://tasks/{taskId}` identify
server-published status/data projections. They are not entity argument URIs
and must not be substituted for `vm:<tenant>:<id>` or another entity URI.

The server speaks MCP revision `2026-07-28` per request, advertises the Tasks
extension through `server/discover`, returns `resultType: "task"` for task-
augmented calls, and keeps final task results inline in `tasks/get`. There is
no local `tasks/result` compatibility method; terminal state is served only
through the standard `tasks/get` response.

## Consequences

- Catalog revisions change when URI schemas or binding metadata change; stale
  sessions and plans fail closed.
- List/detail results carry `uri`, and entity-scoped operations resolve it
  through the tenant-checked registry before provider execution.
- Existing name-based callers must migrate in the same breaking cutover; the
  legacy Go TUI only receives the minimum binding/fixture update before its
  scheduled retirement.
