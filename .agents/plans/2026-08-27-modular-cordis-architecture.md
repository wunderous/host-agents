# Host Agent decomposition safety and transport-contract corrections (Kitchen B)

**Status:** Proposed. Subordinate to the two active plans below — read those first.
**Date:** 2026-08-27
**Scope:** `internal/hostmcp`, `internal/tools`, `internal/transport`, `test/contract`.
**Siblings:**
- [Host Agent Cordis kernel](./2026-08-26-host-agent-cordis-kernel.md) — owns ha-k1…ha-k4. **Does the decomposition.**
- [Tool contract conformance](./2026-08-27-host-agent-tool-contract-conformance.md) — owns C-01…C-24 hardening across catalog, dispatch, effects, and result schemas.
- [Platform decomposition (Kitchen A)](../../../opute/.agents/plans/2026-08-27-modular-cordis-architecture.md) — the MCP seam contract is shared; neither plan changes it alone.

**Authority:** [C-01–C-24](../../docs/cordis-development-guide.md) · [ADR 0002](../../docs/adr/0002-provider-extension-architecture.md) · [ADR 0008](../../docs/adr/0008-product-neutral-kernel-transport.md) · [`cordis-go`](../skills/cordis-go/SKILL.md) · [`host-agent-boundaries`](../skills/host-agent-boundaries/SKILL.md)

---

## 1. Why this is a separate, small plan

The Host Agent's decomposition is already owned. `ha-k1` turns
[`hostmcp.Server`](../../internal/hostmcp/server.go) into a façade over a mounted
plugin graph; `ha-k2` kills the `DispatchTool` / `handleToolCall` duplication and
folds the remaining name switch into one `builtinTools` plugin; `ha-k3` closes
the transport edge; `ha-k4` moves Ollama out of `internal/ops` into a provider
generation. The conformance plan then hardens catalog authority, effects, and
result schemas across the same seams.

Between them, a general "split the monoliths" plan for this repo would be
duplicate work. What those two plans do **not** cover — and what will break
silently when they land — is the enforcement surface welded to the files they
are about to move, plus two statements about the wire that the shipped code
contradicts.

This plan is that gap. It adds no new architecture. It should be executed
**inside** the `ha-k1`/`ha-k2` and conformance Milestone 2 commits, not after
them.

## 2. Corrections to the active plans

### 2.1 "No `initialize`" is not what the code does

`ha-k3` states: *"MCP 2026-07-28 only: no `initialize`, no `Mcp-Session-Id`."*
The second half holds. The first does not:
[`internal/config/config.go:123`](../../internal/config/config.go) reads
`OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE`, and
[`internal/transport/http.go:179`](../../internal/transport/http.go) admits
`initialize` and `notifications/initialized` when it is set. The
[`codex-wsl`](../skills/codex-wsl/SKILL.md) skill documents
`AllowLegacyHandshake=true` as the fix for `-32020: HeaderMismatch`.

This collides with the conformance plan's **Milestone 6**, which validates via
headless `codex exec` in WSL — the exact client class the gate exists for. Two
plans currently point in opposite directions on the same flag.

The guide is explicit that compatibility "needs a separately approved migration
decision and an explicit contract," and no ADR covers this gate. It must be
decided (§5, D1), not carried through a refactor by accident.

### 2.2 `server/discover` is missing from the transport list

`ha-k3` lists protocol version, tasks SSE, no `initialize`, no session id. It
omits `server/discover` — the 2026-07-28 capability-discovery exchange that
*replaced* the retired handshake, implemented at
[`internal/transport/http.go:196`](../../internal/transport/http.go) and
[`internal/cordis/mcp/adapter.go:58`](../../internal/cordis/mcp/adapter.go), and
exercised by `test/compliance/mcp_test.go`, `test/standalone/codex_e2e_test.go`,
and `test/standalone/isolation_test.go`.

It is the single easiest thing to drop when a transport is rewritten, because
nothing else fails first. The complete seam contract is §4.

### 2.3 Two decision anchors sit inside `hostmcp/server.go`

Neither plan mentions re-anchoring, and both move this file:

| Record | Anchored symbol | Moved by |
|---|---|---|
| `host-agent-tool-contract-conformance` | `registerTools` | conformance M2 · `ha-k1` |
| `provider-neutral-cordis-mcp-boundary` | `recordCapabilityInvocation` | `ha-k1` |

Both are `symbol` anchors carrying content digests in the sibling Opute
repository's `.agents/decisions/*.json`. Relocating the symbol invalidates the
anchor and fails `bun run decisions:check` on a change that altered no behavior.

### 2.4 The dispatch coverage test parses source

[`test/contract/dispatch_coverage_test.go`](../../test/contract/dispatch_coverage_test.go)
loads tool names by reading [`internal/tools/dispatch.go`](../../internal/tools/dispatch.go)
as text. `ha-k2` replaces that switch with a registration table, at which point
the test either fails or — worse — matches nothing and passes vacuously while
the coverage guarantee is gone.

The guarantee is real and worth keeping: every catalog tool has exactly one
dispatch path. It must be re-expressed against the registry, in the same commit,
and demonstrated to fail when a tool is dropped. A guardrail that cannot be
shown to fail is not evidence (C-20).

## 3. Verified baseline (2026-08-27)

| Claim | Reality |
|---|---|
| `internal/tools/dispatch.go` | 1,601 lines, **104** `case` labels — not "40+". |
| `internal/hostmcp/server.go` | 1,428 lines. Carries both anchors in §2.3. |
| `internal/tools/catalog.go` | 1,058 lines. **Owned by conformance M2** ("Refactor `internal/tools/catalog.go` and `internal/hostmcp/server.go`"). Not re-planned here. |
| Kernel API | [`internal/cordis/context.go`](../../internal/cordis/context.go) provides `ServiceKey`, `Service`, `Effect`, `Plugin{ID, Inject, Apply}`, `Fiber`, and all four event modes. `Plugin.Apply` returning error disposes partial registrations. |
| Kernel adoption | Already real: `internal/hostmcp/{provider_service_plugin,provider_events,kubernetes_provider,resource_services,recipe_run,provider_install}.go` and `internal/ops/resource_resolver.go` import `internal/cordis`. |
| C-14 enforcement | `assertImportsExclude` in [`test/contract/architecture_test.go`](../../test/contract/architecture_test.go). |

## 4. The MCP seam contract (shared with the Platform plan)

The complete set the transport edge must preserve through `ha-k3` and any
`hostmcp` split:

- `POST /mcp`, loopback-bound, protocol revision pinned `2026-07-28`;
- **`server/discover`** capability discovery (§2.2);
- `tools/list` / `tools/call` per the negotiated contract;
- the `Mcp-Method` header and `_meta["io.modelcontextprotocol/protocolVersion"]`
  modern-request gating (`validateModernMCPRequest` / `isModernMCPRequest`);
- Tasks extension envelope, structured content, typed errors, cancellation;
- request/response correlation and terminal stream events intact;
- Origin present-and-invalid → 403, per `ha-k3`;
- unsupported or incomplete protocol behavior rejected as a **typed failure**;
- **no** `Mcp-Session-Id` minting, stdio, legacy HTTP+SSE, invented task-result
  methods, or silent compatibility fallback;
- `initialize` served **only** behind `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE`, pending
  §5 D1.

`serverInfo`, tool descriptions, annotations, and tool names are not identity or
authorization; trust comes from the descriptor, endpoint policy, artifact
evidence, and validated provider manifest.

## 5. Work items

Each attaches to an existing milestone. None stands alone.

### W1 — Anchor strategy, before `ha-k1` moves anything

Decide per record: re-anchor to the new owning file, or promote the anchor to a
stable contract symbol that will not move again. Prefer the latter — `ha-k1`,
conformance M2, and any later split would otherwise each re-break it. Coordinate
with the sibling repo: the JSON records live there.

*Attaches to:* `ha-k1`, conformance M1 (baseline).
*Exit:* `bun run decisions:check` green in the sibling repo after the move.

### W2 — Re-express dispatch coverage against the registry

Convert `dispatch_coverage_test.go` from source parsing to registry enumeration
in the same commit that lands the `ToolHandler` table. Keep the three required
sets (`requiredTunnelInventoryDispatch`, `requiredPlatformPostgresDispatch`,
`requiredVmResourceDispatch`) and the catalog cross-check.

*Attaches to:* `ha-k2`.
*Exit:* test demonstrated to fail when a required tool is removed from the
registry.

### W3 — Complete the transport contract in `ha-k3`

Add `server/discover` and the `Mcp-Method` / `_meta` gating to the `ha-k3`
checklist and to whatever contract test covers the edge after the split. Extend
the existing coverage in `test/compliance/mcp_test.go`,
`internal/transport/modern_test.go`, and `internal/transport/http_test.go` to the
new seam rather than leaving it on the old handler.

*Attaches to:* `ha-k3`.

### W4 — Resolve the legacy handshake gate

Either record the contract (an ADR describing the gate, its clients, its
audience restrictions, and its removal criteria) or schedule its retirement and
reconcile conformance M6's Codex validation path with it. Do not leave `ha-k3`'s
"zero `initialize`" validation and M6's Codex run asserting opposite things.

*Attaches to:* `ha-k3`, conformance M6. **Blocking for both.**

### W5 — Boundary proof survives the split

`assertImportsExclude` must still reject cross-boundary imports once handlers
live in new packages. Extend the forbidden-import roots to cover the new handler
and service packages introduced by `ha-k1`/`ha-k2`; directory layout is not
proof of isolation (C-14).

*Attaches to:* `ha-k1`, `ha-k2`, conformance M2.

### W6 — Generation semantics across the `hostmcp` split

`ha-k1` moves provider lifecycle out of `server.go`. C-08 must hold across the
new seam: the previous active generation stays available until the candidate is
ready; in-flight work stays affine to the generation that accepted it; a failed
candidate cannot displace the active one; an unconfirmed transition produces
`unknown`, not success. Conformance M5 already plans the tests — this item only
ensures they are written against the *post-split* owner, not the current one.

*Attaches to:* `ha-k1`, conformance M5.

---

## 6. Invariant delta

Per the guide's [permanent invariant capture](../../docs/cordis-development-guide.md)
rule, this records the delta and links to authority rather than restating the
catalog.

**Introduced**

| Invariant | Enforcement point | Item |
|---|---|---|
| Every catalog tool has exactly one registered `ToolHandler`; registry enumeration is the coverage proof, not source text. | `test/contract/dispatch_coverage_test.go` (re-expressed) | W2 |
| The transport edge preserves the complete §4 seam; a missing `server/discover`, `Mcp-Method` gate, or terminal stream event is a typed failure, never a silent fallback. | transport contract tests | W3 |

**Preserved, enforcement point moving**

| Invariant | Authority | From → to | Item |
|---|---|---|---|
| C-02 MCP opacity | `provider-neutral-cordis-mcp-boundary` | `hostmcp/server.go` → provider coordinator service | W1, W5 |
| Tool contract conformance | `host-agent-tool-contract-conformance` | `hostmcp/server.go` → catalog publisher service | W1 |
| C-08 generation affinity | guide C-08 | same owner, split file | W6 |
| C-14 boundary proof | `architecture_test.go` | package roots widen with the split | W5 |
| C-20 typed evidence | guide C-20 | source-text assertion → registry assertion | W2 |

**Retired:** none proposed. `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE` may become a
retirement under W4, which requires an explicit superseding decision — not a
deletion during a refactor.

## 7. Open decisions

| # | Item | Why it cannot be silently refactored | Gate |
|---|---|---|---|
| D1 | `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE` serving `initialize` | The guide requires compatibility to be decision-backed with an explicit contract; none exists, and two active plans currently assume opposite behavior. | W4 — blocks `ha-k3` closure and conformance M6 |
| D2 | Anchor placement strategy | Re-anchoring to another moving file defers the break rather than fixing it. | W1 — blocks `ha-k1` |

## 8. Verification

```bash
go test ./...
go vet ./...
go test -v ./test/contract/...
make standalone-smoke
```

From the sibling repo, after any anchored symbol moves:

```bash
bun run decisions:check
bun scripts/verify/provider-neutral-cordis-mcp-boundary.ts
bun scripts/verify/product-neutral-host-mcp.ts
```

**C-24 E2E gate.** A passing unit test, health endpoint, HTTP 200, `tools/list`,
or live process is insufficient. A Cordis/MCP milestone is green only with: the
real published composition and entry path; `server/discover`, `tools/list`, and
`tools/call`; complete Streamable HTTP stream termination; the actual LLM request
when an agentic path is under test; raw model tool-call arguments and the owning
tool's structured result or error; durable session, operation, generation,
catalog, and lifecycle evidence; authorization/approval and cancellation
behavior; external world state after mutation **and after cleanup**;
reverse-order disposal with no orphaned listener, process, task, or overlay;
exactly one system container in the disposable environment; and WSL-only
coordination evidence.

When a gate fails, record the five-whys across the actual boundaries and fix the
owning contract or lifecycle seam. Never a test-only bypass, a model-specific
prompt hack, or a deterministic orchestrator heuristic.
