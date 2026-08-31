# ADR 0009: Tool Contract Conformance, Catalog Parity, and Capability Authority

Status: accepted

Date: 2026-08-27

Decision record: `../opute/.agents/decisions/host-agent-tool-contract-conformance.json`

## Context

The Host Agent MCP interface is the execution boundary for host operations, provider lifecycle management, and durable planning. Previously, divergences existed between the internal capability catalog snapshot (`get_capability_catalog` returning 111 public capabilities) and the public MCP `tools/list` surface (which registered 122 tools including host-internal tools and legacy underscore aliases). Furthermore, capability effects relied on standalone-table inference instead of explicit typed declarations, `inspect_host_service` lacked a corresponding host-service discovery producer (`list_host_services`), and output schema validation was not uniformly enforced across all capability modules.

## Decision

1. **Authoritative Capability Registry**:
   `internal/catalog` and `internal/tools` are the canonical source of truth for Host Agent capabilities. Sibling Opute and external MCP clients derive their projections from this catalog.
2. **MCP / Catalog Dispatch Parity**:
   MCP `tools/list` and `tools/call` are strictly derived from `s.CatalogSnapshot().Tools`. No secondary loading of internal or excluded tools is permitted. Catalog names stay the dispatch authority. Public MCP names equal catalog snapshot names unless `OPUTE_MCP_PREFIX_TOOL_NAMES` is true, in which case public names are `{short}_{catalogName}` (see [ADR 0012](0012-mcp-tool-name-prefix.md)) and `tools/call` still dispatches on the catalog name. Unprefixed names are never advertised on `tools/list`.
3. **Canonical Provider Operation Names**:
   Provider lifecycle operations use canonical dotted identifiers (`opute.provider.install`, `opute.provider.validate`, `opute.provider.status`, `opute.provider.reload`, `opute.provider.teardown`). Retired underscore aliases (`opute_provider_*`) are completely removed.
4. **Explicit Capability Effects & Annotations**:
   Every capability descriptor declares an explicit effect (`read`, `mutation`, `destructive`, `credential_bearing`). All non-read capabilities require approval (`RequiresApproval = true`). Normal-path effect inference from standalone fallback is removed.
5. **Host Service Discovery**:
   `list_host_services` is introduced as the neutral systemd discovery producer, emitting canonical `host-service:<tenant>:<scope>/<serviceName>` URIs that `inspect_host_service` consumes, enabling typed edge derivation.
6. **Output Schema Validation & Error Envelope**:
   Tool executions validate structured outputs against the declared `OutputSchema`. Schema violations fail closed into typed capability errors (`invalid_result`) rather than recording malformed observations.
7. **Model-Owned Intent Satisfaction**:
   The Host Agent never infers or sets semantic satisfaction on capability results (C-05).

## Invariant Delta

### Preserved
- C-01 Provider-neutral core
- C-02 Opaque MCP boundary
- C-03 Single plan executor (`internal/plan.Runner`)
- C-08 Generation affinity
- C-10 Schema-redacted durable evidence
- C-11 LLM-independent core
- C-14 Import graph boundary separation
- C-21 Canonical Host Agent identity (`OPUTE_REMOTE_AGENT_ID`)
- C-23 Runtime-kind agreement (`vm:` vs `container:`)

### Strengthened
- C-04: Tool-owned argument validation; orchestrator passes raw arguments and validates structured results against output schemas without semantic tampering.
- C-16: Typed resource identity (`host-service` URIs emitted by discovery).
- C-17: Type-derived capability edges (`list_host_services` -> `inspect_host_service`).
- C-18: Public capability parity (`tools/list` equals `CatalogSnapshot` names by default; ADR 0012 is the opt-in wire projection).

## Validation Evidence

- Automated contract tests: `test/contract/tool_contract_conformance_test.go` verifying 1:1 parity, explicit effects, edge derivation, and non-satisfaction assertions.
- Architecture tests: `test/contract/architecture_test.go` asserting boundary isolation and absence of product hostnames.
- Standalone & E2E HTTP test: `test/standalone/codex_e2e_test.go` validating Streamable HTTP MCP discovery, listing, and execution under WSL.
