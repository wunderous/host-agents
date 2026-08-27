# Host Agent Cordis-Boundary Tool Contract Conformance and Invariant Hardening

## Summary

This plan hardens the Host Agent MCP capability publication, dispatch, effect policy,
resource bindings, result schemas, and Cordis kernel boundaries across all normative
invariants C-01 through C-24.

Typed capability definitions in `internal/catalog` and `internal/tools` are the single
authority for `tools/list`, `get_capability_catalog`, dispatch, approval policy, and
output validation. The Host Agent is authoritative for Host-Agent-owned capabilities;
Opute derives its projections from the Host Agent catalog. All provider lifecycle,
dispatch, and transport seams are aligned with Cordis invariants and MCP 2026-07-28
Streamable HTTP requirements. Validation is performed end-to-end against Codex
non-interactive headless executions in WSL, along with contract, import-graph,
disposable-host, and lifecycle test suites.

## Normative Invariant Matrix (C-01 — C-24)

| Invariant | Status & Plan Coverage |
| --- | --- |
| **C-01 — Provider-neutral core** | Canonical dotted lifecycle names (`opute.provider.*`), remove underscore aliases; verify no concrete provider (e.g. K3s, Cloudflare) symbols leak into core packages via package ownership and import-graph tests. |
| **C-02 — MCP opacity** | MCP transport objects stop at `internal/cordis/mcp` and `internal/hostmcp`; Cordis context receives typed services and effects only. Enforced by import-graph verification. |
| **C-03 — One executor** | `internal/plan.Runner` remains the single plan executor. Providers cannot add runners. Proved by executor tests and architecture checks. |
| **C-04 — Tool-owned argument validation** | Raw model arguments are preserved across dispatch. The orchestrator never validates or rewrites tool-specific semantics; output validation checks structure against declared schema without becoming a semantic judge. |
| **C-05 — Model-owned satisfaction** | Explicit guard and tests proving Host Agent never sets, infers, or clears `satisfied`. |
| **C-06 — No heuristic recovery** | Reject URI guessing, name tables, regex guards, and fallback routes. Unknown resources or invalid inputs fail closed with typed errors. |
| **C-07 — Durable truth** | Host Agent state is authoritative for operations. Precedence and recovery behavior are explicit in `internal/state`. |
| **C-08 — Generation affinity** | Active, candidate, draining, and stopped states are explicit. In-flight work stays affine to its accepting generation. Failed candidate activation rolls back without displacing the active generation. |
| **C-09 — Reversible effects** | Every service, task, process, listener, and fiber registration has an idempotent disposer with partial-apply rollback proof and reverse-order teardown. |
| **C-10 — Redacted evidence** | Raw model arguments exist only in the transient execution path. Durable plan documents, task projections, and operation records store schema-redacted, secret-free projections. |
| **C-11 — LLM-independent core** | MCP serving, host operations, catalog, approval, task inspection, and plan validation remain fully operational with zero LLM providers mounted. |
| **C-12 — One system container** | Disposable validation and test environments create exactly one system container (`profile: 2 vCPU / 2 GiB`) with no implicit fallback container. |
| **C-13 — WSL-owned coordination** | WSL-owned Beads/Dolt coordination is authoritative; no checkout-local `.beads` ledger is created. |
| **C-14 — Boundary proof** | Verified by import-graph checks (`test/contract/architecture_test.go`) and separate-process Streamable HTTP MCP tests. |
| **C-15 — Generic provider callbacks** | Provider callbacks can invoke only admitted neutral host primitives (artifact, file, service, HTTP, Kubernetes, or typed Incus instance command) using canonical URIs. Admission cannot be bypassed. |
| **C-16 — Typed resource identity** | Canonical URI kinds (`vm:`, `container:`, `cluster:`, `host-service:`, etc.), tenant validation, and fail-closed rejection on wrong kind or foreign tenant. |
| **C-17 — Type-derived capability edges** | Requires/Produces bindings are explicit in descriptors. `argumentProducers` and edges are generated projections, never authored directly by providers. |
| **C-18 — Dynamic plugin independence** | Atomic activation, replacement, rollback, removal, catalog revision updates, and edge recomputation tested without producer/consumer cross-knowledge. |
| **C-19 — Opaque client identity** | TUI, Codex, and platform clients consume opaque resource URIs and never synthesize IDs or parse kind strings. |
| **C-20 — Typed boundary evidence** | Complete E2E evidence: static catalog, dynamic registry, MCP wire, Codex WSL execution, SSE termination, tenant boundaries, and external cleanup. |
| **C-21 — Canonical Host Agent identity** | Exact `OPUTE_REMOTE_AGENT_ID` equality; missing, stale, conflicting, or ambiguous identity fails closed. No alias fallback. |
| **C-22 — Provider task ownership** | Provider MCP adapters bridge tasks into Host Agent task contracts, or manifest enforces synchronous-only behavior. |
| **C-23 — Runtime-kind/executor agreement** | `vm:` (QEMU) and `container:` (Incus system container) are distinct. Kubernetes is the Host Agent surface; K3s is a provider implementation. |
| **C-24 — E2E target preflight and cleanup** | Preflight runtime, profile 2 vCPU / 2 GiB, exact reverse-order cleanup, external absence verification, and orphan checks. |

## Scope

**In:**

1. Canonical registry consolidation under `internal/catalog` and public MCP adapter in `internal/hostmcp`.
2. Public catalog / `tools/list` / dispatch parity:
   - Wire names strictly match the catalog (canonical dotted `opute.provider.*`, zero underscore aliases).
   - Leaked internal helpers (`agent_shell`, `configure_host_network`, `ensure_host_firewall_rule`, `ensure_sql_connector`, `exec_command`, `get_operation`, `get_sql_connector_status`, `install_sql_forward_sidecar`, `list_operations`, `release_sql_connector`, `run_instance_command`) removed from public MCP registration.
3. Explicit typed capability descriptors:
   - Authority is in Host Agent `internal/catalog` and `internal/tools`.
   - Every public descriptor declares an explicit effect: `read`, `mutation`, `destructive`, or `credential_bearing` (no default-read fallback).
   - Explicit JSON Schema annotations for exact resource kinds, arguments, and required fields.
   - Non-read effects for `configure_network`, `exec_kubernetes_command`, `install_provider_tools`, `recover_bridge`, `register_kubernetes_cluster`, `remove_vm_network_device`, and console stream/input/resize operations (`stream_vm_console`, `send_console_input`, `resize_console`). `open_assistant_session` is `read`.
4. Resource discovery & result contracts:
   - Add typed host-service discovery (`list_host_services` / host-service producer) so `inspect_host_service` consumes an opaque URI.
   - `list_kubernetes_clusters` emits canonical `cluster` URIs in every item.
   - Kubernetes consumers describe and require `cluster` URIs.
   - Output validation verifies structured results against the declared output schema before returning to MCP client.
   - `diagnose_bridge` represents missing heartbeat as schema-valid null/unknown data.
5. Invariant & contract test suites:
   - Import-graph assertions for C-01, C-02, C-03, C-14, C-15.
   - Separate-process Streamable HTTP MCP tests.
   - Tests for C-05 (never sets `satisfied`), C-08 (generation affinity and rollback), C-09 (idempotent disposers and partial-apply rollback), C-10 (schema-redacted durable evidence vs transient raw execution arguments), C-11 (LLM-independent core execution), C-21 (`OPUTE_REMOTE_AGENT_ID` strict equality), C-22 (provider task bridging/sync enforcement), C-23 (runtime kind / executor agreement).
6. E2E validation against Codex non-interactive executions in WSL:
   - Execute headless `codex exec` in WSL connected to the local Host Agent MCP endpoint (`http://127.0.0.1:3004/mcp`).
   - Validate live tools list, capability catalog, negative calls, producer-consumer chains, and disposable container operations.
7. Invariant elevation:
   - Add ADR `docs/adr/0009-host-agent-tool-contract-conformance.md`.
   - Add sibling Opute decision `.agents/decisions/host-agent-tool-contract-conformance.json`.
   - Cross-cutting verifier referencing C-04, C-16, C-17, C-20 without duplicating essays.
   - Update `docs/cordis-development-guide.md`, `AGENTS.md`, and skill routing pointers.

**Out:**

- Rewriting unrelated Cordis kernel internals; changes stay at capability publication, registry, and dispatch boundaries.
- Making internal operation or shell helpers public.
- Production mutation or external rollout.

## Authority & Architecture Decisions

1. **Host Agent Catalog Authority:** Host Agent `internal/catalog` and `internal/tools` are the source of truth for Host Agent capabilities. Sibling Opute derives its projection from this catalog.
2. **Canonical Wire & Provider Names:** Use dotted lifecycle names (`opute.provider.install`, etc.); completely remove underscore aliases.
3. **Registry Seam:** Canonical registry resides in `internal/catalog`; `internal/hostmcp` is the Streamable HTTP MCP protocol adapter.
4. **Transient vs Durable Evidence:** Raw arguments are used in the transient execution path only; all durable records in `internal/state` store schema-redacted, secret-free projections.
5. **No Heuristic / No Satisfied:** The Host Agent never guesses IDs and never sets `satisfied`.
6. **WSL / Dolt Coordination:** Work is coordinated using WSL Beads/Dolt; native Windows is authoritative when using the coordination ledger.

## Execution Milestones

### Milestone 1: Baseline, Invariant Delta & Architecture Checks
- Capture dirty worktree state, current catalog revision, and wire tool names.
- Verify existing import graph and architecture test boundaries.
- Formulate test cases for C-01 through C-24 gaps.

### Milestone 2: Registry Consolidation & Public Dispatch Parity (C-01, C-02, C-14, C-18)
- Refactor `internal/tools/catalog.go` and `internal/hostmcp/server.go`:
  - Public registration strictly reflects catalog snapshot.
  - Remove leaked internal tools from public MCP `AddTool`.
  - Remove underscore aliases.
  - Ensure `tools/list`, `get_capability_catalog`, dynamic refresh, and dispatch use the single registry.
- Add import-graph and package boundary tests.

### Milestone 3: Explicit Capability Definitions & Typed Effects (C-04, C-05, C-10, C-16, C-17)
- Update `internal/tools/capability.go` and `internal/tools/standalone.go`:
  - Remove default-read fallback; require explicit descriptor effect.
  - Annotate schemas with exact resource kinds and required fields.
  - Set non-read effects for network, console, bridge, and cluster registration operations.
  - Assert Host Agent never asserts intent satisfaction (C-05).
  - Enforce schema-based redaction in `internal/state` invocation and task logging (C-10).

### Milestone 4: Resource Discovery, Cluster URIs & Result Validation (C-06, C-16, C-23)
- Add host-service discovery capability (`list_host_services`).
- Update `list_kubernetes_clusters` to emit canonical `cluster` URIs.
- Add structured output schema validation on successful tool execution before returning `CallToolResult`.
- Fix `diagnose_bridge` nullability.

### Milestone 5: Invariant Regression Tests, Disposers & Generation Affinity (C-03, C-08, C-09, C-11, C-15, C-21, C-22)
- Add tests for:
  - Plan Runner single executor (C-03).
  - Generation affinity, candidate isolation, and activation rollback (C-08).
  - Idempotent disposers and partial-apply fiber rollback (C-09).
  - LLM-independent core execution without mounted LLM provider (C-11).
  - Provider callback admission and URI scope (C-15).
  - Exact `OPUTE_REMOTE_AGENT_ID` equality and fallback rejection (C-21).
  - Provider task bridging / synchronous-only enforcement (C-22).
  - Runtime kind separation between VM and container (C-23).
- Update and regenerate schema files (`schemas/incus-tools.json`, `schemas/all-tools.json`, `schemas/standalone-tools.json`).
- Verify all Go tests pass (`go test ./...`, `go vet ./...`).

### Milestone 6: E2E Validation via Headless Codex in WSL (C-12, C-13, C-19, C-20, C-24)
- Launch the compiled Host Agent binary on loopback `http://127.0.0.1:3004/mcp`.
- Execute non-interactive `codex exec` in WSL (`Ubuntu-26.04`):
  - `server/discover`, `tools/list`, and `get_capability_catalog` parity check.
  - Read-only producer chain (`get_host_info`, `list_vms`, cluster discovery, host-service discovery).
  - Negative calls for wrong resource kinds, stale revisions, and retired aliases.
  - Disposable target operations on exactly one system container (2 vCPU / 2 GiB).
  - Exact reverse-order disposal, absence verification, and zero orphan checks.
  - Capture literal Codex requests, tool call arguments, structured results, and SSE termination.

### Milestone 7: Invariant Elevation & Final Review
- Author `docs/adr/0009-host-agent-tool-contract-conformance.md`.
- Author sibling decision `.agents/decisions/host-agent-tool-contract-conformance.json`.
- Add cross-cutting verifier `../opute/scripts/verify/host-agent-tool-contract-conformance.ts`.
- Update `docs/cordis-development-guide.md`, `AGENTS.md`, and skill routing pointers.
- Run permanent invariant and decision verifiers.
