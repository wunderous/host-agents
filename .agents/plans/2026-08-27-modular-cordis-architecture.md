# Host Agent decomposition safety and transport-contract corrections (Kitchen B)

**Status:** Proposed. Subordinate to the two active plans below — read those first.
**Date:** 2026-08-27
**Mode:** From-scratch rebuild with accepted downtime. No façades, no compatibility shims. §4 is the destination tree `ha-k1`–`ha-k4` should land on; §7 is what keeps it that way as capabilities land — and it corrects a defect in §4.
**Scope:** `internal/ops`, `internal/hostmcp`, `internal/tools`, `internal/transport`, `test/contract`.
**Siblings:**
- [Host Agent Cordis kernel](./2026-08-26-host-agent-cordis-kernel.md) — owns ha-k1…ha-k4. **Does the decomposition.**
- [Tool contract conformance](./2026-08-27-host-agent-tool-contract-conformance.md) — owns C-01…C-24 hardening across catalog, dispatch, effects, and result schemas.
- [Platform decomposition (Kitchen A)](../../../opute/.agents/plans/2026-08-27-modular-cordis-architecture.md) — the MCP seam contract is shared; neither plan changes it alone.

**Order:** §11. Start at **M0** — cross-repo, unblocked, and currently red.
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

This plan is that gap. It should be executed **inside** the `ha-k1`/`ha-k2` and
conformance Milestone 2 commits, not after them.

It adds one thing beyond that gap: a **destination tree** (§4). The active plans
each describe a move without stating where the tree ends up, and `internal/ops`
— 18,569 lines in one flat package, 239 methods on one receiver — is the largest
unit in the repo and appears in `ha-k4` only as "move Ollama out". With downtime
accepted there is no reason to extract one domain at a time, and no reason for
`ha-k1`'s façade step to exist at all. §4 states the endpoint; §6 attaches the
work to the milestones that get there.

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
decided (§9, D1), not carried through a refactor by accident.

> **Escalated 2026-08-27, reading the code to brief D1.** The flag does more
> than admit `initialize`, and the difference is the whole decision.
> [`http.go:190`](../../internal/transport/http.go) gates *all* modern-MCP
> validation behind it:
>
> ```go
> if !h.allowLegacyHandshake || (!isRetiredHandshake(envelope.Method) && isModernMCPRequest(r, envelope.Params)) {
>     if err := validateModernMCPRequest(r, envelope.Method, envelope.Params); err != nil {
> ```
>
> and `isModernMCPRequest` returns true **only** when the request body carries
> `_meta["io.modelcontextprotocol/protocolVersion"]`
> ([`http.go:226`](../../internal/transport/http.go)). So when the flag is on, a
> `tools/call` that simply omits that `_meta` key skips
> `validateModernMCPRequest` entirely — no `Mcp-Method` header check, no
> protocol-version check. **Conformance becomes client-elective, on every
> method, not just the handshake.**
>
> That reframes D1. It is not "do we keep a two-method compatibility shim"; it
> is "do we keep a switch that disables the transport contract this plan is
> being built to enforce". §4.2 files `legacy_handshake.go` as the one place
> `initialize` appears — still true, but retiring the *handshake* would not by
> itself remove this bypass, which lives in the main request path. W4 must cover
> both, and the §6 W4 exit criteria are amended accordingly.

### 2.2 `server/discover` is missing from the transport list

`ha-k3` lists protocol version, tasks SSE, no `initialize`, no session id. It
omits `server/discover` — the 2026-07-28 capability-discovery exchange that
*replaced* the retired handshake, implemented at
[`internal/transport/http.go:196`](../../internal/transport/http.go) and
[`internal/cordis/mcp/adapter.go:58`](../../internal/cordis/mcp/adapter.go), and
exercised by `test/compliance/mcp_test.go`, `test/standalone/codex_e2e_test.go`,
and `test/standalone/isolation_test.go`.

It is the single easiest thing to drop when a transport is rewritten, because
nothing else fails first. The complete seam contract is §5.

### 2.3 Two decision anchors sit inside `hostmcp/server.go`

*(Repo-wide the count is higher — §6 W1 verifies **eight** anchors across five
records pointing into files §4 moves. This subsection covers only the two the
active plans move first.)*

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
| `internal/ops` | **89 files, one flat `package ops`, 18,569 lines.** `*HostOperationsService` carries **239 methods** (121 exported) and 24 struct fields. Largest unit in the repo by 4×; the compiler enforces no boundary inside it. |
| `internal/tools/dispatch.go` | 1,601 lines, **104** `case` labels — not "40+". |
| `internal/hostmcp/server.go` | 1,428 lines. Carries both anchors in §2.3. |
| `internal/tools/catalog.go` | 1,058 lines. **Owned by conformance M2** ("Refactor `internal/tools/catalog.go` and `internal/hostmcp/server.go`"). Not re-planned here. |
| Kernel API | [`internal/cordis/context.go`](../../internal/cordis/context.go) provides `ServiceKey`, `Service`, `Effect`, `Plugin{ID, Inject, Apply}`, `Fiber`, and all four event modes. `Plugin.Apply` returning error disposes partial registrations. |
| Kernel adoption | Already real: `internal/hostmcp/{provider_service_plugin,provider_events,kubernetes_provider,resource_services,recipe_run,provider_install}.go` and `internal/ops/resource_resolver.go` import `internal/cordis`. |
| C-14 enforcement | `assertImportsExclude` in [`test/contract/architecture_test.go`](../../test/contract/architecture_test.go). |

## 4. Target shape

The two active plans describe *moves* — `ha-k1` makes `hostmcp.Server` a façade,
`ha-k2` folds the name switch into `builtinTools`, `ha-k4` moves Ollama out of
`internal/ops`. None of them states the **destination**. Downtime is accepted
and the refactor is scoped as a from-scratch rebuild, so this section fixes the
endpoint those moves are aiming at. Without it, `ha-k1`–`ha-k4` land in a tree
whose shape is whatever each step happened to produce.

### 4.1 The finding that sets the shape

`internal/ops` is 89 files and 18,569 lines in **one flat `package ops`**, and
all of it hangs off a single receiver:

| Measure | Value |
|---|---|
| Methods on `*HostOperationsService` | **239** (121 exported) |
| Files declaring `package ops` | 89 |
| Struct fields on `HostOperationsService` | 24, including four `*Manager` relay supervisors and five test-seam function fields |
| Lines | 18,569 — the largest unit in the repo by 4× |

Because it is one package, **the Go compiler enforces nothing inside it.** Incus
code can reach `postgresqlServiceRelayManager`; the OCI builder can call
`kubectlRunner`. C-14 is currently defended by
[`assertImportsExclude`](../../test/contract/architecture_test.go) — a test that
can only see *inter*-package edges, and `internal/ops` has almost none to see.

This is the difference from Kitchen A worth naming: in Go, a package boundary is
a compiler boundary. Splitting `internal/ops` does not merely make C-14 easier
to test — it converts most C-14 violations from a test failure into a **build
failure**. That is the return on this refactor, and it is why the target is
packages rather than files-in-folders.

### 4.2 Target `internal/` tree

```
cmd/
├── opute-host-agent/              # daemon entry: config → cordis.Context → ctx.Start()
└── opute-host/                    # CLI entry

internal/
├── cordis/                        # the kernel. Consumed, not edited.
│   ├── context.go generation.go
│   └── mcp/adapter.go             #   C-02: MCP terminates HERE. Nothing below sees JSON-RPC.
│
├── contract/                      # pure types + constants. Imports nothing from internal/.
│   ├── effect.go                  #   capability effect classification  ← anchor home
│   ├── toolname/names.go          #   the 104 dispatch tool names as typed constants
│   └── servicekey.go              #   every ServiceKey the agent registers
│
├── catalog/                       # AUTHORITATIVE capability descriptors + schemas (C-01).
│                                  # Unchanged in role. tools/list and dispatch derive from it.
├── capability/
│   └── invocation.go              #   recordCapabilityInvocation  ← anchor home
│
├── transport/                     # protocol only. §5 seam contract lives here.
│   ├── http.go                    #   POST /mcp, Origin, Mcp-Method, modern-request gating
│   ├── discover.go                #   server/discover  (§2.2 — currently missing)
│   ├── tasks.go                   #   Tasks extension envelope
│   └── legacy_handshake.go        #   initialize, behind the explicit gate. One file. (§6 W4)
│
├── mcpserver/                     # registration + routing. No domain logic.
│   ├── registration.go            #   registerTools  ← anchor home
│   ├── session.go
│   └── evidence.go                #   ← hostmcp/evidence_redaction.go (C-10)
│
├── dispatch/                      # a REGISTRY, not a switch. ← tools/dispatch.go (1,601 / 104 cases)
│   ├── registry.go                #   Register(name, handler); no name literals
│   └── registry_test.go           #   parity against contract/toolname — replaces source parsing (§6 W2)
│
├── domain/                        # one Go package per domain. Compiler-enforced boundaries.
│   ├── incus/                     #   ← ops/incus_*.go (7)
│   ├── llm/                       #   ← ops/{llama_server*,ollama,local_llm_*}.go (7); ha-k4 lands here
│   ├── postgres/                  #   ← ops/{platform_postgres*,generic_postgresql_service,sqlite_database}.go (4)
│   ├── kubernetes/                #   ← ops/{kubernetes_*,helm_*}.go (6) + hostmcp/kubernetes_provider.go
│   ├── oci/                       #   ← ops/{oci_*,build_and_push_oci_image,stage_build_context}.go (5)
│   ├── cluster/                   #   ← ops/{cluster_agent*,cluster_discovery,generic_agent_connection}.go (4)
│   ├── host/                      #   ← ops/{host_*,exec_command,http_probe,runtime_probe,container_runtime}.go (11)
│   └── serving/                   #   ← ops/{serving_assignment,generic_service_ingress}.go (2)
│
├── hostruntime/                   # the residue of ops/service.go that is genuinely shared.
│                                  # Membership is a three-part rule, not a judgement call (§9.2):
│                                  #   1. names no domain/* type   (compiler + depguard)
│                                  #   2. has >= 2 domain consumers (test)
│                                  #   3. identity/config/exec handle, never an operation
│                                  # Measured: 9 of today's 22 fields qualify. Absorbs the VM
│                                  # runtime handle from internal/provider (§9.1). No plugin.go,
│                                  # no dispatch table, no tool names -- those are the regression.
│
├── plan/  recipe/                 # declarative execution (2,569 + 1,097). Unchanged.
├── state/  session/  resource/  resourceid/  selectors/
├── provider/                      # PLUGIN lifecycle (ADR 0002): install/validate/status/reload,
│                                  # descriptors, generations.  ← hostmcp/provider_install.go
│                                  # Not a domain/*: providers are the extension mechanism, not a
│                                  # bounded capability area. The VM runtime handle that used to
│                                  # occupy this name moves to hostruntime (§9.1).
├── authz/  config/  console/  cli/  app/  exec/  fingerprint/  heartbeat/  version/
└── (internal/ops and internal/hostmcp/server.go no longer exist)
```

Every `domain/<x>` package has the same four files:

```
internal/domain/postgres/
├── plugin.go      # cordis.Plugin: ID(), Inject() []ServiceKey, Apply(*Context) (Effect, error)
│                  # Wiring only. NO descriptors, no capability table (§9.3 D4).
├── service.go     # the domain's methods, lifted off *HostOperationsService
├── dispatch.go    # this domain's capabilities: one typed registration each,
│                  # carrying handler + effect + admission + task. The DESCRIPTOR
│                  # stays in catalog/ per ADR 0009 and §4.3 rule 4 (§9.3 D4).
└── *_test.go
```

§7.1 is load-bearing here: without the single registration in `plugin.go`, this
partition spreads capability-adding from two packages to five and makes the most
common change in the repo *more* expensive.

### 4.3 Rules the tree encodes

1. **`domain/x` may not import `domain/y`.** Cross-domain needs go through an
   injected `cordis.ServiceKey`, resolved by the kernel. This is a compile
   error, not a test assertion — extend `assertImportsExclude` to assert the
   *absence* of the edge as a second line of defence, but the compiler is
   primary.
2. **`domain/*` may not import `transport/` or `mcpserver/`.** C-02: MCP
   terminates at `cordis/mcp`. A domain package that knows a JSON-RPC method
   name is a violation of the seam, not a convenience.
3. **`contract/` imports nothing from `internal/`.** That is what makes it a
   stable anchor path (§6 W1).
4. **`catalog/` stays authoritative and stays one package.** Per
   [ADR 0009](../../docs/adr/0009-tool-contract-conformance-and-catalog-authority.md),
   `tools/list` mirrors the immutable catalog snapshot 1:1. Per-domain
   descriptor tables would be a second source of truth — the exact drift the
   conformance plan exists to close. Domains register *handlers*, never
   descriptors.
5. **No struct accumulates a second time.** `HostOperationsService`'s 239
   methods partition across eight `domain/*/service.go` files plus
   `hostruntime`. A domain service that grows past its domain is the same
   failure at a smaller scale; the fix is a new domain package, never a
   `helpers.go`.
6. **The legacy handshake is one file.** `transport/legacy_handshake.go` is the
   only place `initialize` appears, so retiring it is a delete and its gate is
   readable in one place (§2.1, §6 W4, §9 D1).

### 4.4 What this changes about `ha-k1`–`ha-k4`

Nothing about their content; it fixes their landing sites, and one of them gets
easier:

| Milestone | Lands in |
|---|---|
| `ha-k1` — `hostmcp.Server` → mounted plugin graph | `mcpserver/` + eight `domain/*/plugin.go`. The "façade" step is unnecessary with downtime accepted: `hostmcp/server.go` is deleted, not wrapped. |
| `ha-k2` — kill `DispatchTool`/`handleToolCall` duplication, fold the name switch | `dispatch/registry.go` + eight `domain/*/dispatch.go`. There is no `builtinTools` plugin in the target — "builtin" is not a domain, and a catch-all plugin reproduces `internal/ops` one level up. |
| `ha-k3` — close the transport edge | `transport/`, with `discover.go` added (§2.2) and `legacy_handshake.go` isolated (§2.1). |
| `ha-k4` — Ollama out of `internal/ops` | `domain/llm/`. Under this plan it is not a special case: it is one of eight identical extractions, and doing it alone is more work than doing all eight. |

**Recommendation:** fold `ha-k4` into a single `internal/ops` partition commit
covering all eight domains. Extracting one domain from a flat package means
building the shared-seam boundary (`hostruntime`) anyway; having built it, the
other seven are mechanical.

---

## 5. The MCP seam contract (shared with the Platform plan)

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
  §9 D1.

`serverInfo`, tool descriptions, annotations, and tool names are not identity or
authorization; trust comes from the descriptor, endpoint policy, artifact
evidence, and validated provider manifest.

## 6. Work items

Each attaches to an existing milestone. None stands alone.

### W1 — Anchor strategy, before `ha-k1` moves anything

**Corrected 2026-08-27.** An earlier draft of this item said: move every
anchored symbol to a terminal home *before* any code moves, as the sibling plan
does with its `contracts/` directory. **That does not work in this repo**, and
the check is worth recording so it is not re-proposed:

| Symbol | Why it cannot move early |
|---|---|
| `registerTools` | `func (s *Server) registerTools()` — a **method on `hostmcp.Server`**. Moving it means moving `Server`, which is `ha-k1`. |
| `recordCapabilityInvocation` | Same — `func (s *Server) recordCapabilityInvocation(...)`, five call sites inside `server.go`. |
| `capabilityEffect` | Free function, but depends on `metaString`, `standaloneClassification`, and the `capabilityEffects` map — all **unexported**, all in `tools/capability.go` — plus `ToolDefinition` from `catalog.go`, which conformance M2 owns. Relocating it to `internal/contract` would make `contract` import `tools`, violating §4.3 rule 3. |
| `deriveCapabilityEdges` | Same entanglement, via `CapabilityDescriptor` and `CapabilityEdge`. |

The sibling repo's approach works because a TypeScript `contracts/` module can
hold pure constants that nothing else needs. Here the anchored symbols are
methods and unexported helpers woven into the packages being split. **Go anchors
re-anchor *with* their code, in the commit that moves it** — the same rule the
sibling plan applies to its source-scanning guardrails.

So W1 does not move symbols. It makes the breakage impossible to miss (M0, §11)
and lands the boundary lint. The eight anchors below are the inventory each
later commit is responsible for:

**Eight anchors across five records** point into files §4 moves — verified
against `.agents/decisions/*.json` in the sibling repo, not the two this plan's
§2.3 names:

| Record | Anchored symbol | Today | Terminal home |
|---|---|---|---|
| `host-agent-tool-contract-conformance` | `registerTools` | `hostmcp/server.go` | `mcpserver/registration.go` |
| `host-agent-tool-contract-conformance` | `capabilityEffect` | `tools/capability.go` | `contract/effect.go` |
| `provider-neutral-cordis-mcp-boundary` | `recordCapabilityInvocation` | `hostmcp/server.go` | `capability/invocation.go` |
| `provider-neutral-cordis-mcp-boundary` | `handleProviderInstall` | `hostmcp/provider_install.go` | `provider/lifecycle.go` — **D2 resolved, §9.1** |
| `typed-capability-edge-derivation` | `deriveCapabilityEdges` | `tools/capability.go` | `contract/effect.go` |
| `runtime-kind-defaults-and-nested-kvm` | `normalizeProvisionInstanceType` | `ops/incus_launch.go` | `domain/incus/` — **moves in W7** |
| `wsl-llama-gpu-pinning` | `Environment=CUDA_VISIBLE_DEVICES=0` | `ops/llama_server.go` | `domain/llm/` — **moves in W7** |
| `wsl-llama-gpu-pinning` | `n-gpu-layers` | `ops/llama_server_test.go` | `domain/llm/` — **a test file** |

Which commit owns which:

| Commit | Re-anchors |
|---|---|
| `ha-k1` | `registerTools`, `recordCapabilityInvocation`, `handleProviderInstall` |
| conformance M2 | `capabilityEffect`, `deriveCapabilityEdges` |
| **W7** | `normalizeProvisionInstanceType`, `Environment=CUDA_VISIBLE_DEVICES=0`, `n-gpu-layers` |

Two details that will bite otherwise:

- ~~**`handleProviderInstall` has no home in §4.2.**~~ **Resolved 2026-08-28
  (§9.1).** Its home is `internal/provider/lifecycle.go`. The name is freed by
  folding today's `internal/provider` — which is the *VM runtime handle*, an
  unrelated meaning of the word — into `hostruntime`, a move §4.2 already
  specifies. That fold must land in or before `ha-k1`.
- **One anchor is on a test file.** `llama_server_test.go` moves with its subject
  in W7, so that commit re-anchors `wsl-llama-gpu-pinning` twice over — the
  production file and the test. `bun scripts/decision-records.ts sweep --update`
  re-baselines digests, but the *paths* are hand-edited; do both in the commit.

`internal/contract/` imports nothing from `internal/`, which is what makes it a
path that will not move again. `registerTools` and `recordCapabilityInvocation`
are behavior, not constants, so they cannot live there — their homes are chosen
as terminal instead, and §4.3 rules 2 and 4 are what keep them terminal.

Coordinate with the sibling repo: the JSON records live there.

Land `.golangci.yml` in the same commit (§7.3) — there is none in this repo
today. `depguard` states the §4.3 boundary rules declaratively and catches the
transitive case the compiler misses (`domain/a` → `hostruntime` → `domain/b`);
the shape ratchet of §7.5 attaches to the same gate.

*Attaches to:* conformance M1 (baseline). Runs with **M1** (§11), after M0.
*Exit:* `golangci-lint run` green with the boundary and ratchet rules active,
baseline committed; the anchor inventory above recorded where each later commit
can see it.

### W2 — Re-express dispatch coverage against the registry

Convert `dispatch_coverage_test.go` from source parsing to registry enumeration
in the same commit that lands the `ToolHandler` table. Keep the three required
sets (`requiredTunnelInventoryDispatch`, `requiredPlatformPostgresDispatch`,
`requiredVmResourceDispatch`) and the catalog cross-check.

Add the partition assertion: the registry's key set equals
`internal/contract/toolname`, seeded from the current 104 `case` labels, and
every key is registered by exactly one `domain/*/dispatch.go`. That test is what
makes partitioning `internal/ops` in one commit safe — a tool that lands in no
domain, or in two, is a red test rather than a silent gap.

*Attaches to:* `ha-k2`.
*Exit:* test demonstrated to fail when a required tool is removed from the
registry.

**Done 2026-08-28.** `internal/contract/toolname` (102 constants) and a registry
in `internal/tools`; `runTool` is a lookup. Handlers are split into per-domain
files, the domain taken from the `ops/*.go` file defining the service method each
calls — so M3 is a file move, not a re-derivation. 34 handlers are parked in
`dispatch_unassigned.go` rather than guessed: their methods still live on
`ops/service.go` and `ops/standalone.go`, and that file is the M3 worklist.

The switch declared **102** distinct labels, not 104 — `grep -c 'case "'` also
counts nested switches inside case bodies. §4.2 and this section said 104.

Three guarantees, each demonstrated to fail (C-20): a required tool dropped from
the registry fails the coverage test; a tool registered by two domains panics at
init; a handler with no contract name, or a name with no handler, fails the new
parity test. Note that parity cannot catch a *rename* — both sides derive from
the same constant — which is what the literal required-sets are for.

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

**Amended.** Per §2.1's escalation, W4 covers two separable things, and a
decision that addresses only the first leaves the contract unenforced:

1. `initialize` / `notifications/initialized` in `legacy_handshake.go`.
2. The validation bypass at [`http.go:190`](../../internal/transport/http.go),
   which makes `Mcp-Method` and protocol-version checks elective for any client
   that omits `_meta["io.modelcontextprotocol/protocolVersion"]`.

Whichever way D1 goes, (2) should become **narrow and explicit** rather than
implied by (1): validation skipped for a named set of methods or a named client
audience, never for "any request that did not opt in".

*Attaches to:* `ha-k3`, conformance M6. ~~**Blocking for both.**~~

**Done 2026-08-27.** D1 resolved as *record the contract and narrow the bypass*.

- [**ADR 0011**](../../docs/adr/0011-legacy-handshake-compatibility-gate.md) —
  audience (pre-2026 clients: Codex, Cursor), default off, enumerated bypass
  surface, and removal criteria (conformance M6 off Codex **and** no enrolled
  deployment setting the flag). Filed as a *bounded exception* to ADR 0008's
  "no dual-era compatibility layers" rather than a silent contradiction of it,
  which is what it had been.
- **The bypass is now a closed set.** `legacyCompatibleMethods` in
  [`http.go`](../../internal/transport/http.go) lists the pre-2026 client
  surface; `server/discover` and `tasks/*` are validated even in legacy mode.
  The old predicate keyed the bypass off whether the request carried
  `_meta["io.modelcontextprotocol/protocolVersion"]` — omitting it was enough to
  skip validation on any method.
- **`ha-k3` corrected** in
  [the kernel plan](2026-08-26-host-agent-cordis-kernel.md): "no `initialize`"
  → "no `initialize` by default". The unqualified claim was false and had been
  since the flag landed.
- **Proof.** `TestLegacyHandshakeDoesNotBypassModernSurface` covers
  `server/discover`, `tasks/get`, and a `tasks/list` carrying only a
  `progressToken` in `_meta`; it was **demonstrated red against the previous
  predicate** and green after. `TestMCPAllowsLegacyHandshakeWhenOptedIn` still
  passes unchanged, so the Codex path is intact.

**One behaviour change to watch.** A legacy client that previously reached
`tasks/*` or `server/discover` now gets `-32020`. That is the intent — those are
modern-only surfaces — but if a real client trips on it, the fix is a recorded
amendment to `legacyCompatibleMethods` in ADR 0011, **not** re-widening the
predicate.

### W5 — Boundary proof survives the split

`assertImportsExclude` must still reject cross-boundary imports once handlers
live in new packages. Extend the forbidden-import roots to cover every package
introduced by §4.2; directory layout is not proof of isolation (C-14).

The partition changes what this test is for. Today `internal/ops` is one package
with almost no inter-package edges, so `assertImportsExclude` has little to see
inside the largest 18,569 lines in the repo. After §4.2 the two rules that
matter — `domain/x` ⊬ `domain/y`, and `domain/*` ⊬ `transport`/`mcpserver` — are
**compile errors**. Keep the test as a second line of defence asserting the
absence of those edges, and say so in its comment: it is no longer the primary
proof, and treating it as such invites someone to relax a package boundary
because "the test still passes".

*Attaches to:* `ha-k1`, `ha-k2`, the `internal/ops` partition, conformance M2.

### W6 — Generation semantics across the `hostmcp` split

`ha-k1` moves provider lifecycle out of `server.go`. C-08 must hold across the
new seam: the previous active generation stays available until the candidate is
ready; in-flight work stays affine to the generation that accepted it; a failed
candidate cannot displace the active one; an unconfirmed transition produces
`unknown`, not success. Conformance M5 already plans the tests — this item only
ensures they are written against the *post-split* owner, not the current one.

*Attaches to:* `ha-k1`, conformance M5.

### W7 — Partition `internal/ops` ~~in one commit~~ as a strangler

**Revised 2026-08-28.** The eight `domain/*` packages and `hostruntime` from
§4.2 land one domain at a time, not in a single commit. `internal/ops` keeps
thin delegating methods and type aliases while each domain takes ownership, and
is deleted last.

The original reason for one commit was that extracting one domain means building
the shared-seam boundary anyway, and doing them separately means seven rounds of
deciding what is "shared". That reason is spent: §9.2 decided the membership
rule once, `hostruntime.Shared` exists, and nothing about domains two through
eight re-opens the question. What one commit would still cost is a single
unreviewable diff across 19k lines with no green state in between.

Progress: **complete 2026-08-28.** All eight domains extracted in cost order
(`serving`, `kubernetes`, `llm`, `oci`, `host`, `postgres`, `cluster`, `incus`),
then `internal/ops` deleted. Every domain declares what it needs from other
domains as `Deps`, stated in primitives rather than in another domain's types,
so no two domains ever import each other; a `no-cross-domain-<name>` depguard
rule enforces it per domain, each proven RED on a deliberate import.

What replaced `internal/ops` is `internal/hostagent`, a composition root that
owns no operations: it holds `hostruntime.Shared`, constructs each domain with
that domain's declared seams, and hands the domains out. Callers reach an
operation through its owner -- `s.Incus().StartVM`, `s.Kubernetes().ListPods`.
It is 937 lines, against 19,088 for the package it replaced.

Four contract packages absorbed types two domains speak and neither may own:
`clusterinfo`, `vminfo` (which now also holds `VMInfo` and the cluster IPv4
normalizer), `k8sname`, and `toolname`; `internal/tcprelay` absorbed the TCP
forwarder that postgres and cluster both run.

Sequenced **after** W1 (anchors already terminal) and **after** W2's partition
assertion exists (so the cut is verifiable). `ha-k4`'s Ollama move is one of the
eight, not a separate milestone.

~~`hostruntime`'s membership rule must be decided before this lands~~ —
**decided 2026-08-28, §9.2.** Three parts: names no `domain/*` type
(compiler + depguard), has two or more domain consumers, and is identity,
config, or an execution handle rather than an operation. Measured against
today's service, **9 of 22 fields qualify**; eleven are used in one file or
none. W7 additionally:

- **folds `internal/provider` (the VM runtime handle) into `hostruntime`**,
  which frees that name for plugin lifecycle per §9.1 — this must land in or
  before `ha-k1`, not here, if `ha-k1` re-anchors `handleProviderInstall` first;
- ~~**deletes `allowInsecureDownloads`**~~ — **done**; no occurrence remains in
  the repo. Per-call `insecureRegistry` is the live mechanism;
- ~~**enables the `hostruntime-knows-no-domains` depguard rule**~~ — **done**,
  alongside a `no-cross-domain-<name>` rule per domain, each proven RED;
- ~~**adds the rule-2 consumer test** and the `hostruntime` size ratchet~~ —
  **done**: `TestEveryExportedMemberHasTwoDomainConsumers` (proven RED on a
  deliberate lone member) and `budgetLines`. The rule-2 test found three real
  violations on its first run: `RequireLinux` had no consumer at all and is now
  unexported, `DefaultIncusPath` had none outside the package and is now
  unexported, and `DefaultSystemdRunPath` had only `host` and moved there;
- ~~**creates the `hostruntime-membership` decision record**~~ — **done**,
  anchored to `membership_test.go`, `ratchet_test.go`, and `shared.go`, with
  `verify` running the hostruntime tests. Five existing records that anchored
  into `internal/ops` were re-pointed to where their files now live.

*Attaches to:* `ha-k4`, absorbing it.
*Exit:* `internal/ops` does not exist; `go build ./...` green; W2 partition
assertion green; `go test ./test/contract/...` green; `hostruntime` inside its
ratchet budget; `AGENTS.md` and `.agents/skills/` updated in-commit, ADRs given
structure notes rather than edits (§7.6).

### W8 — Single capability registration

Collapse the five tool-name-keyed sites into one typed registration per
capability in `domain/*/plugin.go`, per §7.2. Effect, admission rule, and task
metadata become fields on the registration, so a capability missing its effect
fails to compile.

`catalog.Registry` already supports this — `Register`, `RegisterRegistration`,
`baseConflict`, `ValidateBase`, one revisioned `Snapshot()`. This is a change of
edit site, not of authority: ADR 0009's 1:1 `tools/list`↔snapshot property holds
unchanged.

Sequenced **with** W7 — the domain packages are the registration sites, so doing
it separately means writing the dispatch wiring twice.

*Attaches to:* conformance M2 (owns `catalog.go`). **Gated on §9 D4.**
*Exit:* adding a capability touches one file; a registration missing effect,
admission, or task metadata is a compile error; `go test ./test/contract/...`
green including tool contract conformance.

**Progress — done 2026-08-28.**

`register` in [`internal/tools/registry.go`](../../internal/tools/registry.go)
now takes `(name, effect, admission, task, handler)`. The three behaviour values
are **separate positional parameters, not fields of a struct literal**, because
a struct literal has a zero value for every field it omits; positionally,
omitting one does not compile. Demonstrated RED: dropping them from one
registration gives `not enough arguments in call to register`.

All 102 dispatch registrations carry the three values. The two sites that
registered one handler under two names were unrolled — the names differed in
behaviour (`agent_shell` inline vs `run_host_command` task-aware), which the
shared-loop form had hidden.

The four behavioural tables were pruned to the names that have **no** dispatch
registration — transport-owned and provider-owned tools, which is the honest
residue §9.3 predicted:

| Table | Before | After |
|---|---|---|
| `capabilityEffects` | 75 keys | 20 |
| `tasks.TaskAwareTools` | 48 keys | 12 |
| `resource.isHeavyTool` | 27 keys | 6 |
| `resource.ClassifyTool` prefix inference | every tool | unregistered names only |

`Coordinator.AcquireClass` is new: a caller that knows the class passes it
instead of having it inferred from the name. `hostmcp` passes the declared class
and falls back to `ClassifyTool` only when a name has no registration.

*Anti-drift:* [`test/contract/capability_registration_test.go`](../../test/contract/capability_registration_test.go)
fails if any residual-table key acquires a dispatch registration. Demonstrated
RED by adding `create_vm` back to `TaskAwareTools`.

**What the effect declaration found.** Ten registered capabilities had been
falling through `capabilityEffect`'s inference to `"read"` — and therefore
publishing `RequiresApproval: false` — despite changing host state:
`agent_shell`, `exec_command`, `run_instance_command`, `ensure_docker`,
`ensure_k3d`, `ensure_host_firewall_rule`, `ensure_sql_connector`,
`release_sql_connector`, `install_helm_chart` (now `mutation`) and
`uninstall_helm_chart` (now `destructive`). This is exactly the defect ADR
0009's Context paragraph names; most were catalog-excluded, which is why it had
gone unnoticed. Making the declaration mandatory is what surfaced them.

**Correction to §9.3's table.** "`standalone.go` — 181 tool-name keys → derived
from the registrations" is **not reachable and was not done.** Measured: 22
`StandaloneToolNames` entries have no dispatch registration (`list_operations`,
`opute.provider.*`, the plan/recipe families) and 19 registered names are absent
from the standalone set (`agent_shell`, the helm and SQL-connector families).
The two sets answer different questions — *what does this capability do* vs
*is it exposed in standalone mode* — so `standalone.go` stays its own table.
Four behavioural tables collapsed, not five.

---

## 7. Designing for change

The point of §4 is not eight tidy packages. It is that adding the next
capability stays cheap enough that nobody reaches for a workaround. `CLAUDE.md`
already carries the norm — *"before adding a workaround, apply five-whys and
repair the owning abstraction"* — so what is needed here is structure that makes
following it the cheap path.

Applied honestly, that test finds a defect in §4 itself.

### 7.1 The partition, as drafted, makes capability-adding worse

Measured. Adding one capability today touches these non-test files:

| Capability | Files | Packages |
|---|---|---|
| `remove_local_llm_model` | 5 — `tools/{capability,catalog,dispatch,standalone}.go`, `tasks/registry.go` | **2** |
| `create_vm` | 5 — `tools/{capability,dispatch,standalone}.go`, `resource/admission.go`, `tasks/registry.go` | **3** |

Four of the five sit in `internal/tools`. They are parallel tool-name-keyed
sites — effect classification, catalog descriptor, dispatch handler, standalone
surface, task registration, resource admission — but they are *adjacent*, so a
half-finished addition tends to get caught in review.

§4.2 as drafted scatters them: `contract/effect.go`, `catalog/`,
`domain/x/dispatch.go`, `tasks/registry.go`, `resource/admission.go` — **the same
five edits across five packages instead of two.** The partition is right for
`internal/ops`, whose 239 methods genuinely belong to eight different domains.
Applied to the capability path without 7.2, it converts a mildly annoying change
into a five-package one, and five-package changes are where shortcuts come from.

**This is a defect in §4, not an argument against it.** The fix is 7.2, and §4
is not complete without it.

### 7.2 One registration per capability

Target: a capability is added in **one file** — `domain/x/plugin.go` — as one
typed registration carrying descriptor, effect, handler, admission rule, and task
metadata together. Everything downstream derives.

**This does not weaken ADR 0009, and the mechanism already exists.**
[`catalog.Registry`](../../internal/catalog/registry.go) is already
registration-based: it holds a validated `base` snapshot plus typed overlays via
`Register` / `RegisterRegistration` / `RegisterCapability`, detects collisions in
`baseConflict`, validates through `ValidateBase` and `validate`, and exposes one
revisioned `Snapshot()`. ADR 0009 requires that `tools/list` mirror the
authoritative snapshot 1:1 and that there be exactly one source of truth for what
a tool is. A snapshot compiled from per-domain registrations satisfies both:
still one snapshot, still immutable after build, still 1:1 — the *edit site*
moves, the authority does not.

What changes:

- `domain/x/plugin.go` registers its capabilities at `Apply` time through the
  existing registry API. `catalog/` remains authoritative and remains one
  package.
- Effect, admission, and task metadata become **fields on the registration**,
  typed, rather than entries in three separate tool-name-keyed tables. A capability
  that forgets its effect fails to compile, not silently at runtime.
- `tools/standalone.go` (481) and the tool-name-keyed portions of
  `tasks/registry.go` (429) and `resource/admission.go` (309) become derived.
- Adding a capability: **one file, one package.** Down from five and two.

> **Corrected 2026-08-28 (§9.3, D4).** The paragraph above is wrong where it says
> the mechanism already exists. `catalog.Registry`'s overlay API cannot carry a
> built-in capability: `baseConflict` rejects any `OperationID` already in `base`,
> and every built-in is in `base`. Descriptors stay in the catalog; the domain
> registration carries handler, effect, admission and task only. The target is
> **two** edit sites, not one — see §9.3 for what that does and does not buy.

### 7.3 Boundary rules the compiler cannot state

§4.3 rules 1 and 2 are mostly compiler-enforced, which is the main return on the
partition. Two gaps remain:

**There is no `.golangci.yml` in this repo at all.** A `depguard` config states
the §4.3 rules declaratively — `domain/*` may not import `domain/*`, `transport`,
or `mcpserver` — and reports them as lint errors naming the rule, rather than as
an import cycle error that says nothing about why. It also catches the case the
compiler misses: `domain/a` → `hostruntime` → `domain/b` is legal to the
compiler and is exactly how the boundary erodes.

Add it in W1, alongside `importas` for consistent package aliases and the
standard correctness linters. This is cheap and it is the difference between a
rule the tree enforces and a rule the tree merely documents.

**`assertImportsExclude` changes role.** It stops being the primary C-14 proof
(the compiler is) and becomes a backstop for edges lint cannot see. Say so in its
comment — otherwise someone relaxes a package boundary because "the test still
passes."

### 7.4 `hostruntime` needs a membership rule before it exists

`hostruntime` is the residue of a 239-method god object. Left undefined it
re-accumulates by default: every method that does not obviously belong to a
domain lands there, and `internal/ops` returns under a new name within a year.
This is the single most likely way the refactor is undone.

The rule, decided before W7 (§9 D3), not during:

1. A symbol belongs in `hostruntime` only if it names **no domain type and no
   domain concept**. `runVMExec` names a VM; it belongs in `domain/incus`.
2. If two domains need it and it names neither, it goes in `hostruntime`. If it
   names either, it belongs to that one and the other injects a `ServiceKey`.
3. `hostruntime` gets a **line budget in the ratchet** (7.6) from day one. It is
   the one package where growth is the signal that matters.
4. The five test-seam function fields on `HostOperationsService`
   (`kubectlRunner`, `commandRunnerFn`, `containerLookPathFn`,
   `containerCommandFn`, `containerStreamingCommandFn`) do **not** move to
   `hostruntime` wholesale. Each is a seam for one domain's tests and belongs to
   that domain; collecting them centrally rebuilds the god object's test surface,
   which is how the original one justified its own growth.

### 7.5 A shape ratchet

Nothing in this repo observes growth: no `.golangci.yml`, no size gate, no
complexity budget. `internal/ops` reached 18,569 lines in one package and
`HostOperationsService` reached 239 methods without tripping anything.

Add to the W1 lint config and the `go test ./test/contract/...` gate:

| Rule | Threshold |
|---|---|
| Any `.go` file | 600 lines |
| Any package (non-test) | 3,000 lines |
| Methods on one receiver | **40** — `HostOperationsService` is at 239 |
| `hostruntime` package | its own tighter budget (7.4.3) |
| `domain/*` importing `domain/*`, `transport`, `mcpserver` | forbidden (7.3) |

**Ratchet, not a cliff.** Baseline committed from today's values; the gate fails
only on *increase*. A hard limit invites splitting coherent files to satisfy a
number; a ratchet makes growth the thing that needs an argument.

The receiver-method budget is the one that would have caught this. A file-size
limit would have been satisfied by `internal/ops`'s 89 files — every one of them
is individually reasonable. The 239-method receiver is the actual defect, and
only a per-receiver rule sees it.

### 7.6 Documentation drift

`AGENTS.md` and five files under `docs/` — including
[ADR 0002](../../docs/adr/0002-provider-extension-architecture.md),
[ADR 0003](../../docs/adr/0003-cloudflare-provider-incus-containers.md),
[ADR 0007](../../docs/adr/0007-runtime-kind-and-e2e-target-boundaries.md), and
[ADR 0008](../../docs/adr/0008-product-neutral-kernel-transport.md) — reference
`internal/ops` or `hostmcp/server.go`, both of which cease to exist under §4.

ADRs are decision records, not documentation: **they are not edited to match a
refactor.** The path they cite becomes wrong while the decision stays right, and
rewriting the path silently rewrites history. The correct move is a short
"structure note" appended to each affected ADR recording that the code moved,
where it moved, and under which plan — leaving the original text intact.

`AGENTS.md` and `.agents/skills/` **are** documentation and get updated in the
commit that moves the code — the same rule the [Platform plan](../../../opute/.agents/plans/2026-08-27-modular-cordis-architecture.md)
states for its skills. W7 is the
commit that invalidates the most, so it carries the most.

---

## 8. Invariant delta

Per the guide's [permanent invariant capture](../../docs/cordis-development-guide.md)
rule, this records the delta and links to authority rather than restating the
catalog.

**Introduced**

| Invariant | Enforcement point | Item |
|---|---|---|
| Every catalog tool has exactly one registered `ToolHandler`; registry enumeration is the coverage proof, not source text. | `test/contract/dispatch_coverage_test.go` (re-expressed) | W2 |
| The transport edge preserves the complete §5 seam; a missing `server/discover`, `Mcp-Method` gate, or terminal stream event is a typed failure, never a silent fallback. | transport contract tests | W3 |
| A domain package may not import another domain package, `transport`, or `mcpserver`; cross-domain access is an injected `cordis.ServiceKey`. | **Go compiler**, with `assertImportsExclude` as second line | W5, W7 |
| Capability descriptors have exactly one source: `internal/catalog`. Domains register handlers, never descriptors. | ADR 0009 + conformance M2 tests | W7 |
| `initialize` appears in exactly one file (`transport/legacy_handshake.go`), so its retirement is a delete. | transport contract tests | W3, W4 |
| Adding a capability edits one file; effect, admission, and task metadata are typed fields, not parallel tables. | compile error + tool contract conformance | W8 |
| Package shape does not regress; no receiver exceeds its method budget. | `.golangci.yml` + contract gate, committed baseline, fails on increase | W1 |
| `hostruntime` names no domain type or concept. | §7.4 rule + its own ratchet budget | W7 |

**Preserved, enforcement point moving**

| Invariant | Authority | From → to | Item |
|---|---|---|---|
| C-02 MCP opacity | `provider-neutral-cordis-mcp-boundary` | `hostmcp/server.go` → `cordis/mcp` termination, enforced by `domain/*` ⊬ `transport` | W1, W5, W7 |
| Tool contract conformance | `host-agent-tool-contract-conformance` | `hostmcp/server.go` → `mcpserver/registration.go` (terminal) | W1 |
| C-08 generation affinity | guide C-08 | same owner, split file | W6 |
| C-14 boundary proof | `architecture_test.go` | test assertion → compiler, test retained as backstop | W5, W7 |
| C-20 typed evidence | guide C-20 | source-text assertion → registry assertion | W2 |

**Retired:** none proposed. `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE` may become a
retirement under W4, which requires an explicit superseding decision — not a
deletion during a refactor.

## 9. Open decisions

| # | Item | Why it cannot be silently refactored | Gate |
|---|---|---|---|
| ~~D1~~ | ~~`OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE`~~ **RESOLVED 2026-08-27** — gate kept, default off, bypass surface enumerated. [ADR 0011](../../docs/adr/0011-legacy-handshake-compatibility-gate.md) · decision record `legacy-handshake-compatibility-gate`. | — | **W4 done; M2 unblocked** |
| ~~D2~~ | ~~Anchor placement strategy~~ **RESOLVED 2026-08-28** — terminal homes confirmed as written; `handleProviderInstall` lands in `internal/provider/`, freed by folding the VM runtime handle into `hostruntime` as §4.2 already specifies. See §9.1. | — | **`ha-k1` and W7 unblocked** |
| ~~D4~~ | ~~Capability registration site~~ | **Resolved 2026-08-28, §9.3.** §4.3 rule 4 stands: domains register behaviour, never descriptors. §7.2's premise that `catalog.Registry`'s overlay API can carry built-in capabilities is false — `baseConflict` rejects any registration whose `OperationID` is already in `base`, and the built-ins *are* `base`. | W8 |
| ~~D3~~ | ~~`hostruntime` scope~~ **RESOLVED 2026-08-28** — three-part membership rule, compiler- and ratchet-enforced. Measured: only 9 of 22 fields qualify. See §9.2. | — | **W7 unblocked** |

### 9.1 D2 resolved — anchor homes, and where `handleProviderInstall` lives

**The terminal-homes table in §6 W1 stands as written.** W1's own correction
already settled the general strategy: Go anchors re-anchor *with* their code, in
the commit that moves it, because the anchored symbols are methods on
`hostmcp.Server` and unexported helpers woven into the packages being split.
Nothing about that needs revisiting. What was genuinely open is the one row W1
flagged: `handleProviderInstall` had no home in §4.2.

**The blocker was a name collision, not a missing package.** `internal/provider/`
already exists — and it is **not** about plugin providers at all. It is the
**VM runtime handle**: `provider.Runtime`, `RunProvider`, `RunHost`,
`RunVMExec`, the Incus binary path
([`internal/provider/provider.go`](../../internal/provider/provider.go), 184
lines). Meanwhile `handleProviderInstall` is plugin lifecycle in ADR 0002's
sense: `loadProviderDescriptor`, `provideradapter.Connect`,
`providerLifecycle.CreateCandidate`, `persistProviderGeneration`. Two unrelated
meanings of "provider", one package name.

Filing the handler under `internal/provider/` as it stands would conflate them,
and inventing a third word is worse — ADR 0002 explicitly *supersedes* the
"provider-extension" draft, so "extension" is retired vocabulary. The live
vocabulary is `provider`: `plugins/`, `contracts/provider`,
`ProviderLifecycleManager`, `providercontract`.

**Resolution, using a move §4.2 already specifies.** §4.2 lists the "provider
runtime handle" as `hostruntime` content. Acting on that frees the name:

| | Today | After |
|---|---|---|
| VM runtime handle | `internal/provider/` (`Runtime`, `Config`, `ID`, the exec wrappers) | `internal/hostruntime/` — it is a shared execution handle, which is exactly D3's rule 3 |
| Plugin lifecycle | `hostmcp/provider_install.go` (408 lines, on `*Server`) | `internal/provider/` — `lifecycle.go`, `descriptor.go`, `generation.go` |

`handleProviderInstall`'s terminal home is therefore
**`internal/provider/lifecycle.go`**, and the `provider_install` /
`provider_validate` / `provider_status` / `provider_reload` handlers move with
it as a set. `provider` is not a `domain/*` package: providers are the extension
mechanism, not a bounded capability area, so they are not subject to §4.3 rule 1
and do not get a `plugin.go`.

**Amends the §6 W1 table:** the `handleProviderInstall` row reads
`internal/provider/lifecycle.go`, not `domain/*/plugin.go`. `ha-k1` still owns
re-anchoring it.

**Ordering note.** The `internal/provider` → `hostruntime` fold must land in or
before `ha-k1`, since `ha-k1` is the commit that re-anchors
`handleProviderInstall` into the freed name. Doing it after would mean two
renames of the same path and two re-anchorings of the same record.

### 9.2 D3 resolved — the `hostruntime` membership rule

D3's fear is precise and correct: left undefined, `hostruntime` becomes
`internal/ops` under a new name. A prose boundary will not prevent that, so the
rule below is stated so a *test* can decide it, and paired with a ratchet.

**A thing belongs in `hostruntime` only if all three hold.**

1. **It names no `domain/*` type.** Compile-enforced — `hostruntime` must not
   import any `domain/*` package — and stated declaratively in `.golangci.yml`
   `depguard` so the transitive case is caught too. This is the rule that makes
   the other two hard to evade.
2. **It has two or more domain consumers.** One consumer means it belongs to
   that domain. This is the rule that actually shrinks the residue, and it is
   checkable by enumerating references per domain package.
3. **It is identity, configuration, or an execution handle — never an
   operation.** `hostruntime` supplies the means; it never performs a host
   operation. No method on a `hostruntime` type may run a provider, kubectl, or
   container command to effect a change; it hands back the runner and stops.

**Measured against today's `HostOperationsService` (242 methods, 22 fields,
19,088 lines across 54 files), the rule admits 9 fields:**

| Qualifies (files using it) | Goes to a domain instead |
|---|---|
| `resourceRegistry` (7) · `tenantID` (5) · `runtime` (3) · `agentID` (3) · `instanceID` (2) · `ownershipMode` (2) · `sharedHostOwnerInstance` (2) · `resourceSnapshot` (2) · `commandRunnerFn` (1, but it is the shared provider-exec test seam) | `kubernetesExecutor` (4) · `kubectlRunner` (1) → `domain/kubernetes` · `postgresqlServiceRelay` (2) · `sqlSupervisor` (1) · `sqliteDatabaseRoot` (1) → `domain/postgres` · `localLLMRelay` (2) → `domain/llm` · `guestBridgeRelay` (2) → `domain/host` · `ociStoragePolicyPath` · `ociStorageMu` (1 each) → `domain/oci` · `containerLookPathFn`/`CommandFn`/`StreamingCommandFn` (1) → `domain/host` · `resetCheckpointPath` (1) → `domain/host` · `toolsFn` (1) → `domain/host` |

**Eleven of the twenty-two fields are used in one file or none.** They were never
shared state; they sat on the god object because it was the object that existed.
That is the measurement that makes rule 2 worth having.

**One field is dead, and should be deleted rather than moved.**
`allowInsecureDownloads` is plumbed the whole way —
`OPUTE_STANDALONE_ALLOW_INSECURE_DOWNLOADS` → `config.go:107` →
`app/runtime.go:73` → `ops.Options` → the struct field — and then **never read**:
zero occurrences of `s.allowInsecureDownloads` in the repo. Insecure registries
*are* supported, but per-call via the `insecureRegistry` tool argument
([`build_and_push_oci_image.go`](../../internal/ops/build_and_push_oci_image.go)),
which is live and unrelated. So the env var is vestigial config that looks like
a security control and is not one. Delete the flag, the option, the field, and
the config plumbing in W7; do not carry it into `hostruntime`.

**Enforcement, so the rule survives contact.** The rule is only as good as what
fails when it is broken:

- Rule 1 is the compiler, plus `depguard`.
- Rule 2 gets a test enumerating each `hostruntime` field's consumers and failing
  at fewer than two. It should be written to name the offending field and the
  single domain it belongs to.
- **A size ratchet on `hostruntime`**, in the shape gate landed in M1 — baselined
  at whatever the W7 partition produces, and never allowed to grow. Rules 1 and 2
  bound what may enter; the ratchet is what notices the slow accumulation they
  do not catch. This is the same mechanism as the sibling repo's `module-shape.ts`
  and this repo's `new-from-rev` lint baseline, and it is chosen for the same
  reason: a fixed cap gets argued with, a ratchet gets banked.

**`hostruntime` has no `plugin.go` and registers no capabilities.** It is not a
domain and must not acquire a dispatch table; a tool name appearing in
`hostruntime` is the first symptom of the regression D3 names.

**No decision record yet — deliberately.** The natural record here ("hostruntime
membership is the three-part rule") can only anchor to this plan section and a
commented `.golangci.yml` block until W7 creates the package. A record anchored
to a file that is about to move is the exact failure D2 spent its time on, so
**W7 creates it**, anchored to `internal/hostruntime/` and the enabled depguard
rule, with `verify` running the rule-2 consumer test. Until then this section is
the authority and §11's W7 row carries the obligation.

### 9.3 D4 resolved — descriptors stay in the catalog, domains register behaviour

**§4.3 rule 4 wins. §7.2's mechanism claim is wrong and is amended here.**

§7.2 argues the move is free because "the mechanism already exists":
`catalog.Registry` is registration-based, so a domain can register its own
capability through `Register` / `RegisterRegistration`. Checked against the
code, that is true only for *dynamic provider* capabilities and false for the
built-ins §7.2 is talking about:

- [`Registry.baseConflict`](../../internal/catalog/registry.go) rejects any
  registration whose `OperationID` already appears in `base`, and
  `RegisterRegistration` returns `"conflicts with the base catalog"` when it does.
- `base` is the compiled-in snapshot: the embedded JSON schemas
  (`schemas/all-tools.json`, `incus-tools.json`) plus the Go descriptors in
  [`internal/tools/catalog.go`](../../internal/tools/catalog.go). Every built-in
  host capability is in it.
- So the overlay API is ADR 0002's dynamic-provider path, deliberately walled off
  from the authoritative catalog. Routing built-ins through it would mean either
  removing them from `base` — which is exactly the second source of truth ADR 0009
  exists to prevent — or defeating `baseConflict`.

**The decision.** A capability's *identity* — name, input and output schema,
description, version — stays in the catalog, authored where ADR 0009 puts it and
validated by `ValidateBase`. A capability's *behaviour* — handler, effect,
admission rule, task metadata — becomes one typed registration in
`domain/x/dispatch.go`.

That is the split §4.3 rule 4 already states, and it is the one the code allows.

**What this actually buys, stated honestly.** §7.2's headline of "one file, one
package" is not reachable without breaking ADR 0009. What is reachable is **two**:
the authoritative descriptor, and one typed registration. The win is real and it
is not the descriptor — it is collapsing the four *behavioural* tool-name-keyed
tables that exist today into fields on one registration:

| Today | After |
|---|---|
| `capabilityEffects` map — [`internal/tools/capability.go`](../../internal/tools/capability.go) | `Effect` field on the registration |
| `standalone.go` — 181 tool-name keys | derived from the registrations |
| `tasks/registry.go` — 50 tool-name keys | `Task` field |
| `resource/admission.go` — 3 tool-name keys | `Admission` field |
| dispatch — 102 registrations (W2) | the registration itself |

Five edit sites become two, and a capability that forgets its effect fails to
compile rather than falling through `capabilityEffect`'s inference at runtime —
which is the standalone-table inference ADR 0009's Context paragraph already
names as a defect.

**Enforcement.** Extend W2's parity test: every catalog tool has exactly one
domain registration and every registration names a catalog tool. W2 already
proved that shape works — it is the same assertion, over a richer value.

**Consequence for W7, which is why this had to be decided first.** Domain
packages get the typed registration in `dispatch.go`. There is no descriptor
table in `plugin.go` and no per-domain catalog. W7 can land its cut without W8
reworking it.

**Amends §4.2:** the `plugin.go` line reading "descriptor + effect + handler +
admission + task, one typed registration into `catalog.Registry`" is wrong on
both the file and the mechanism. The registration lives in `dispatch.go`, carries
effect/handler/admission/task, and does not go through `catalog.Registry`.

## 10. Verification

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

---

## 11. Milestone order

The two plans share one dependency graph. This is the order both repos should
execute in; each milestone names the plan and section that owns it.

| # | Milestone | Repo | Owns | Gated on |
|---|---|---|---|---|
| **M0** | **Enforcement gate real and green** | both | this section | — **unblocked** |
| **M1** | **Baselines and boundary lint** | both | Platform §5 Phase 0.5 · Host Agent §6 W1 | — **done** |
| **M2** | **Transport contract + dispatch registry** | Host Agent | §6 W2, W3, W4 | — **W2, W4 done; W3 checklist done, seam coverage waits on the `ha-k3` split** |
| M3 | Partition `internal/ops` | Host Agent | §6 W7 | D2, D3 |
| ~~M4~~ | ~~Single capability registration~~ | Host Agent | §6 W8 | — **done 2026-08-28** |
| M5 | Platform kernel | Platform | §5 Phase 1 | D3, **D6** |
| M6 | Platform domain slices | Platform | §5 Phase 2 | D5 |
| M7 | MCP gateway | Platform | §5 Phase 3 | D2 (HWP EOL) |
| M8 | Chat turn | Platform | §5 Phase 4 | D4 (chat) |
| M9 | Capability projections | Platform | §5 Phase 5 | **M4** |

**The Go side leads, and that is a dependency rather than a preference.** M9 is
the milestone that takes capability-adding from 8–15 files to one, and it can
only derive projections from a descriptor that already carries effect, admission,
and task metadata. M4 is what puts them there. Running the Platform first means
building projections against a descriptor shape that M4 then changes.

**M0 is the precondition for treating any of the rest as verified.** Both plans
assert that `decisions:check` catches anchor breakage on a refactor that changed
no behavior. That claim is only worth as much as the gate behind it.

### M0 — Enforcement gate real and green

Owned by the [Platform plan's §11](../../../opute/.agents/plans/2026-08-27-modular-cordis-architecture.md),
because the decision records live in that repo. Summarised here because this
plan depends on it and because the Go half is local:

**Done 2026-08-27.** The Go half found a second broken gate:

- **This repo's CI was already red.** `ci.yml` runs `test -z "$(gofmt -l .)"`,
  and `gofmt -l .` returned three files — `internal/ops/host_service_probe.go`,
  `internal/tools/capability.go`, `internal/tools/catalog.go`. All three were
  pure whitespace (a trailing blank line, map-key alignment). Reformatted;
  `go build`, `go vet`, and all 31 decision anchors still green afterwards —
  worth checking, since two of the three carry anchored symbols.
- **`.golangci.yml` landed**, ratcheted. A first lint config over 33k previously
  unlinted lines finds **50** pre-existing issues (21 `unused`, 19 `staticcheck`,
  6 `ineffassign`, 3 `gofmt`, 1 `errcheck`). Failing on all of them would mean
  either a 50-fix commit bolted onto unrelated work or a config nobody enables,
  so `new-from-rev` pins commit `5945ba7` as the baseline: existing issues are
  reported, new ones fail. Demonstrated both directions — a deliberately bad new
  file exits 1, removing it exits 0.
- **`depguard` is configured but not enabled.** It errors when no rule carries a
  non-empty deny list, and the packages §4.3's rules name do not exist until W3
  and W7. The rules are written out as comments with the correct module path
  (`github.com/wunderous/host-agents`); each is uncommented by the commit that
  creates its package. That is W1's standing instruction, not a follow-up.
- A ratcheted `Lint` step added to `ci.yml`, with `fetch-depth: 0` so the
  baseline commit is reachable.

Note what M0 did **not** achieve: `decisions:status` and `render --check` still
cannot run in the sibling repo's CI, because 10 records anchor into this repo's
`internal/**` and CI has no non-sparse checkout of it. So the eight anchors in
§6 W1 remain protected by a local hook only. That is a smaller gap than before
and it is now written down rather than assumed.

### M1 — Baselines and boundary lint

**Done 2026-08-27.** This repo's half — [`.golangci.yml`](../../.golangci.yml)
with the `issues.new-from-rev` ratchet at `5945ba7`, wired into
[`ci.yml`](../../.github/workflows/ci.yml) with `fetch-depth: 0` — landed during
M0 and is unchanged. The Platform half (four baseline gates) is recorded in the
[sibling plan's M1 section](../../../opute/.agents/plans/2026-08-27-modular-cordis-architecture.md).

Both repos now defend structure the same way: a committed baseline, failing only
on an increase. That symmetry is deliberate. A hard threshold would be red on day
one in either repo — 50 pre-existing lint issues here, a 9,046-line `store.ts`
there — and a gate that is red on day one gets switched off.

**Next: M2** (transport contract + dispatch registry, §6 W2–W4), which is gated
on **D1** — the legacy handshake question. That is a decision, not an
implementation step, and it is still open.
