# Resource URI Scheme for Opute Entities — Implementation Plan (2026-08-23)

## Goal

Every managed entity in the host agent gets a globally unique resource identifier (URI) of the
form `<resource-type>:<tenant-id>:<resource-id>` (e.g. `vm:local:worker-01`,
`database:local:platform-db`, `tunnel:local:host-agent-mcp`). Retrieval/lifecycle tools take
**only** `uri` for entity identity; list/detail outputs carry `uri` per record; redundant
compound arguments (`vmName`, `clusterId`+`namespace`+`clusterName`, `bindingId`,
`consumerId`+`databaseName`, …) are removed from tool signatures in a **breaking cutover**, with
all in-repo call sites updated in the same change.

## Decisions (confirmed)

- **Tenant**: session-carried tenant anchored on `open_assistant_session` + process active
  tenant with fail-closed validation. Multi-tenant authn/authz is planned (see
  `2026-08-22-single-node-private-cloud-cell-recipe-plan.md` — Platform owns accounts, tenant
  records, authorization policy; the host agent validates and executes). The tenant segment
  lands now so the format never changes.
- **Resolution**: **registry-backed** — a new `resource_registry` table maps URIs to entity
  coordinates; reconcile/provision/ensure register; retrieval resolves; discoverable
  pre-existing resources are lazily adopted. Provider-native coordinates may be
  tenant-safe derived names and must not be confused with user-facing display names.
- **Compatibility**: breaking cutover. Old persisted plans fail closed on re-validation
  (existing fail-closed philosophy). catalogRevision bump is expected.
- **Scope**: all entity families, including bridge-inventory tools whose schemas exist but
  dispatch does not implement yet (schema-only update, flagged).

## 1. URI core: new package `internal/resourceid`

- `URI struct { ResourceType, TenantID, ResourceID string }` with `Parse`, `String`,
  `Validate`, and typed constructors (`VMURI(tenant, name)` etc.).
- Parsing: `strings.SplitN(s, ":", 3)` — type and tenant segments must match
  `^[a-z][a-z0-9-]{0,31}$`; the **resource-id may contain colons** (Ollama model refs like
  `qwen3.5:2b`) but must be non-empty and whitespace-free.
- Resource-type constants (closed vocabulary, kept in parity with `knownResourceKinds` at
  `internal/hostmcp/server.go:105`, extended): `vm`, `container`, `host`, `cluster`,
  `postgres-service`, `tidb-service`, `sqlite-database`, `database` (bridge inventory),
  `tunnel`, `llm-runtime`, `model`, `host-service`, `sql-connector`, `oci-registry`,
  `service-domain`, `service` (tenant-owned application/service identity).
- Typed errors: `ErrInvalidURI`, `ErrUnknownType`, `ErrForeignTenant`.
- Table-driven unit tests (colon-bearing model refs, foreign tenant, empty segments).

## 2. Tenant plumbing

- `internal/config`: new `OPUTE_TENANT_ID` env (default `local`), pattern-validated.
- `HostOperationsService` (`internal/ops/service.go`) gains `TenantID` + registry handle; the
  resolver rejects any URI whose tenant ≠ active tenant (`ErrForeignTenant`).
- `internal/hostmcp/session.go` `handleOpenAssistantSession`: input gains optional `tenantId`;
  the response echoes the **active** tenant; a requested tenant that doesn't match the
  configured active tenant is rejected fail-closed (same pattern as the existing
  catalog-revision mismatch). The binding is recorded in the session contract for provenance.
- `internal/session/contract.go`: `Request` gains `TenantID string json:"tenantId,omitempty"`;
  `EntityReference` gains `URI string json:"uri,omitempty"` as the canonical form
  (`canonicalField: "uri"`). Keep bounded validation.
- Documented constraint: MCP go-sdk v1.7.0 does not expose per-connection session IDs to tool
  handlers, so per-connection tenant switching is deferred to the authn/authz epic; all
  enforcement points (URI validation, registry tenancy) land now.

## 3. Resource registry (state store)

`internal/state/store.go` — new table, additive migration (follows the
`ensureActiveCapabilityTable` pattern; AGENTS.md rule: additive before reads):

```sql
CREATE TABLE IF NOT EXISTS resource_registry (
    uri TEXT PRIMARY KEY,
    resource_type TEXT NOT NULL,
    tenant_id TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    coordinates_json TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
```

Store API: `UpsertResource`, `GetResource(uri)`, `DeleteResource(uri)`,
`ListResources(resourceType, tenantID)`. Store tests beside `store_test.go`.

## 4. Resolver + registration in `internal/ops`

New `internal/ops/resource_resolver.go`:

- `Resolve(uri string, wantType) (Coordinates, error)`: parse → tenant check → registry lookup
  → typed coordinates (`VMCoordinates{VMName}`,
  `PostgreSQLServiceCoordinates{VMName, Namespace, ClusterName}`,
  `HostServiceCoordinates{Scope, ServiceName}`, `TunnelCoordinates{BindingID}`, …).
- **Lazy adoption** for discoverable single-key entities: on registry miss for
  `vm`/`container`/`cluster`/`host-service`/`tunnel`/`llm-runtime`, verify existence via the
  backing system (incus list, systemd unit, tunnel unit) and upsert. This keeps pre-existing
  resources addressable without a migration sweep.
- Compound entities (`postgres-service`, `tidb-service`) fail closed with a "reconcile first"
  error (coordinates cannot be guessed safely).
- Registration hooks: the canonical compute-provider operation
  `opute.capability.compute.provision` (Incus implements VM and system-container
  targets), the canonical Kubernetes/provider operations, the existing
  reconcile/ensure operations for database, tunnel, registry, and model
  resources, and any temporary compatibility adapters — each returns `uri` and
  upserts; `remove` operations deregister (two-phase provider teardown
  unchanged). Legacy names such as `provision_vm`, `provision_container`, and
  `install_k3s` are migration inputs only; they must not remain a second
  authoritative path after provider parity.

Entity→URI mapping (resource-id = canonical logical id; provider coordinates stored):

| type | resource-id | coordinates |
|---|---|---|
| `vm` / `container` | tenant-scoped logical resource id | providerInstanceName, displayName, instanceType |
| `host` | host agent instance id (`agentID`) | — |
| `cluster` | backing instance name | vmName, discovered cluster name |
| `postgres-service` | clusterName (per-tenant unique; collision fails closed) | vmName, namespace |
| `tidb-service` | clusterName | vmName, namespace |
| `sqlite-database` | `<consumerId>/<databaseName>` | path |
| `tunnel` | bindingId | — |
| `llm-runtime` | `ollama` (singleton) | runtime |
| `model` | modelRef (colons allowed) | runtime |
| `host-service` | `<scope>/<serviceName>` | scope |
| `service` | tenant-owned application/service id such as `blog` | tenant/application coordinates |
| `sql-connector` | databaseId | — |
| `oci-registry` | name | vmName, namespace |
| `database`/`storage`/bridge bindings | bridge-minted id (schema-only tools) | — |

Provider boundary: the resource registry is Host Agent-owned and provider
agnostic. The Incus provider receives a resolved URI plus typed coordinates and
returns provider-native observations with the same URI; it never mints or
changes the tenant segment. This keeps `vm:<tenant>:...` and
`container:<tenant>:...` stable if Incus is later replaced by another compute
provider. If Incus's native instance namespace is host-global, the provider
derives a stable tenant-safe instance name from the URI and stores it in the
coordinates; a user's display name is not required to be globally unique.
Kubernetes, registry, database, and edge providers follow the same rule for
their resource types.

## 5. Tool surface migration (`internal/tools/dispatch.go` + ops result structs)

Mechanics: every entity-scoped case reads `uri` via a new `uriFromArgs(args)` →
`svc.Resolve(...)` → existing ops methods receive coordinates (ops signatures mostly
unchanged); the `vmNameFromArgs` name-alias is removed. Ops result structs gain
`URI string json:"uri"` (`VMInfo`, `ClusterDetail`, tunnel results, model records, service
statuses, host info…).

- **VM/cluster**: `get_vm_info`, `start/stop/restart/delete_vm`, `update_vm_resources`,
  `get_cluster_details`, `get_cluster_runtime_details`, `restart_cluster`,
  `restart_cluster_agent`, `get_k3s_status`, `uninstall_k3s`, `exec_command`,
  `stream_vm_console`, `ensure_local_llm_k3s_proxy`, `discover_service_ingress` → `{uri}`;
  `provision_vm`/`create_vm`/`provision_container` keep creation args, output adds `uri`;
  `list_vms`/`list_clusters` records add `uri`.
- **Tenant application services**: service lifecycle and rename operations use
  `service:<tenant>:<serviceId>` as the canonical identity. A display name such
  as `blog` is resolved only within the active tenant; `service:tenant-a:blog`
  and `service:tenant-b:blog` are distinct resources. Natural-language
  requests such as “rename my blog service” must be resolved by Platform into
  a typed URI-bound mutation before Host Agent dispatch. Host Agent must not
  search all tenants or choose a first match.
- **K8s sub-resources are NOT separate entities**: `list_pods`, `get_k8s_resource`,
  `put_k8s_secret`, `apply_manifest`, helm tools, etc. take `uri` (of vm/cluster) **plus** k8s
  coordinates (kind/name/namespace) — documented decision.
- **Databases**: `get/remove_postgresql_service` `{uri}`; `reconcile_postgresql_service`
  keeps desired-state/placement args, registers + returns `uri`; same split for TiDB; SQLite
  ensure/get/remove take `uri` (`sqlite-database:<t>:<consumer>/<name>`);
  `get_sql_connector_status`/`release_sql_connector` take `uri`.
- **Tunnels**: `get_cloudflare_tunnel_status`, `probe_host_exposure`, `remove_host_exposure`,
  `delete_cloudflare_tunnel`, `remove_local_llm_cloudflared_tunnel` → `{uri}`
  (`tunnel:<t>:<bindingId>`); ensure/create keep binding config, return `uri`.
- **LLM**: `probe_local_llm`, `start/stop_local_llm_runtime`, `configure_local_llm_runtime`
  take `uri` (default `llm-runtime:<t>:ollama` when omitted); `list_local_llm_models` records
  add `uri` (`model:<t>:<ref>`); `remove_local_llm_model` takes `uri`.
- **Host/host-services**: `get_host_info` returns `uri` (`host:<t>:<agentID>`);
  `inspect_host_service`, `restart_host_service`, `set_host_service_state` → `{uri}`
  (`host-service:<t>:<scope>/<name>`).
- **Bridge-inventory tools** (`get_postgresql_database`, `create/resize/delete_postgresql_database`,
  `get/list_service_storage`, `get_service_domain_binding`, `register/list_kubernetes_clusters`,
  `configure_k3s_ha_servers`, …): schema-only switch of `databaseId`/`storageId`/`bindingId`/
  `clusterId` → `uri`; dispatch absence is pre-existing and flagged in code comments.
- Path-keyed host files/artifacts/archives (`ensure_host_file`, `inspect_host_file`, …) and
  ephemeral session-scoped relays keep current args — not inventory entities.

## 6. Schemas in lockstep (catalogRevision bumps — expected)

- Go fallbacks: `internal/tools/catalog.go`, `standalone.go`.
- Embedded JSON (canonical in this repo; the sibling exporter only writes `catalog-meta.json`):
  `schemas/incus-tools.json`, `schemas/all-tools.json`, plus `standalone-tools.json` entries
  that carry input schemas.
- Output schemas gain required `uri` on entity records; input schemas drop legacy entity
  params.
- Update `knownResourceKinds` and keep parity with `resourceid` constants (asserted in
  `test/contract`).
- Contract tests updated: catalog parity (`test/contract/catalog_test.go`), dispatch coverage
  regexes, standalone classification.

## 7. Call sites

- **Recipes/fixtures**: `test/fixtures/recipes/cloudflared.yaml` (`vmName` → `uri` input;
  idempotency key uses uri); `plugins/llm/ollama/recipes/*.yaml` and
  `plugins/tunneling/cloudflare/recipes/tunneling-managed.yaml`: `serviceName`-based
  `set_host_service_state`/`inspect_host_service` calls → `uri`. Recipes reference entities
  three ways only:
  1. full uri as recipe input,
  2. `${nodes.<id>.output.uri}` from a creating node,
  3. reserved runner-provided `${vars.tenantId}` + explicit canonical id
     (e.g. `host-service:${vars.tenantId}:user/opute-ollama.service`).

  The plan runner (`internal/plan/runner.go`, `schema.go` reference validation) injects
  `tenantId` as a reserved variable.
- **TUI** (final client: sibling Opute `apps/opute-tui`): consumes the server-provided
  canonical `uri` and never fabricates identity. The legacy `clients/tui` module is only a
  migration source for this prerequisite and receives the minimum mechanical binding and
  fixture fixup needed to compile and stay green; its parser/UX is not being fully migrated
  here because `TUI-109` retires it after the Bun client passes parity. No `@vm` or name-based
  token becomes a normative wire binding.
- **Tests**: `test/live/vm_lifecycle_test.go` + `reset_stack_test.go`
  (create→list→get→delete via uri), `test/modes/packaged_test.go`,
  `test/tui/packaged_test.go`, `test/standalone/http_test.go`,
  `test/compliance/mcp_test.go`, `internal/ops/*_test.go`,
  `internal/tools/dispatch_test.go`, `internal/hostmcp/server_test.go` + `session_test.go`,
  new registry/resolver tests.

## 8. Docs

- New `docs/adr/0003-resource-uris.md`: format, parsing rule (SplitN-3, colon-bearing ids),
  tenant semantics (session-carried anchor, active-tenant default, fail-closed foreign
  tenant, authn/authz follow-up), registry + adoption semantics, breaking-change /
  catalogRevision notes.
- Brief AGENTS.md architecture note.

## Implementation order

0. Materialize and validate the shared Beads milestone graph from the
   cloud-cell plan; preserve the Windows-backed Dolt/WSL client boundary and
   do not create checkout-local `.beads` state.
1. `internal/resourceid` + tests
2. config tenant + session contract fields
3. state-store registry table + tests
4. ops resolver/adoption/registration + result `uri` fields
5. dispatch migration + helper cleanup
6. catalog/standalone/JSON schemas + contract tests
7. recipes/fixtures/TUI
8. remaining tests
9. ADR + AGENTS.md

## Validation

`bd graph check`, `bd dep cycles`, `bd lint --status all`, shared launcher
status, and `bd dolt test` must pass before implementation begins. Then run
`gofmt -w .`, `go vet ./...`, `go test ./...`, `make standalone-smoke`,
`make standalone-http-smoke`, TUI build + `test/tui` packaged tests. Historical plan docs
(`2026-08-22-*.md`) are records and are not rewritten.

## Risks / notes

- catalogRevision bump invalidates stale sessions and persisted plan re-runs — fail-closed,
  intended.
- Pre-existing postgres/tidb services need one reconcile before uri-addressable (compound
  coordinates).
- `reset_incus_stack` and provider teardown must deregister affected rows to avoid registry
  drift.
- Ollama model refs contain colons — covered by SplitN(3) parsing; pattern tests pin this.
