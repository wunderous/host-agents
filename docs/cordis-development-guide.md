# Host Agent Cordis Development Guide

**Status:** Normative living guide

**Applies to:** The current Host Agent implementation and all future Host
Agent work

**Decision history:** [`docs/adr/0002-provider-extension-architecture.md`](adr/0002-provider-extension-architecture.md)

This guide is the durable implementation reference for the Host Agent's
Cordis-style architecture. It is intentionally separate from dated or
superseded plans. The ADR records why the architecture was chosen; this guide
records the invariants that every change must preserve.

The Host Agent adopts the relevant Cordis semantics without pretending that a
remote MCP process is an in-process TypeScript plugin. The Go kernel under
`internal/cordis` owns typed service composition, dependency resolution,
effects, event dispatch, provider generations, drain, and disposal. MCP is the
transport adapter at the edge. The LLM is an optional semantic participant;
it is not replaced by deterministic intent heuristics in the Host Agent.

## The governing boundary

The Host Agent is a typed MCP execution service. It does not own natural-
language intent, retrieval, or semantic claims about whether a user request
has been satisfied. Those decisions belong to the LLM/Platform layer and must
arrive as typed, authorized inputs or model-visible outcomes.

```text
LLM / Opute Platform
  intent, retrieval, planning, semantic outcome
             │ typed contract
             ▼
Host Agent Cordis kernel
  services, providers, effects, events, policy, lifecycle, evidence
             │ Streamable HTTP MCP 2026-07-28
             ▼
Provider MCP / external system
  provider-owned validation and execution
```

The Host Agent may record, route, authorize, observe, and durably reconcile
facts. It must not turn a successful tool result, assistant sentence, name
match, or identifier shape into an inferred semantic result.

## Current implementation map

The following packages are the current seams. New code must fit one of these
owners or first introduce a contract and an ADR explaining a new owner.

| Area | Owns | Must not own |
| --- | --- | --- |
| `internal/cordis/` | Provider-neutral context, plugins, services, effects, typed event modes, fibers, generations, drain, and disposal | MCP details, recipes, host shell commands, UI, or concrete providers |
| `internal/cordis/mcp/` | Streamable HTTP MCP client, protocol negotiation, provider discovery, tool mapping, authentication, structured results, Tasks, and cancellation | Provider-specific policy, recipe execution, activation decisions, or semantic intent |
| `internal/hostmcp/` | Public Host Agent MCP, catalog projection, dispatch boundary, admission, durable operations, provider orchestration, and evidence | Concrete provider lifecycle knowledge or LLM intent classification |
| `internal/catalog/` | Typed capability descriptors, registration, revisions, and schema metadata | Provider discovery, secret storage, or execution implementation |
| `internal/recipe/` | Source policy, decoding, canonical hashing, variables, compatibility, and plan extraction | MCP sessions, side effects, or provider-specific installation logic |
| `internal/plan/` | The single generic host-plan executor, readiness, retry, recovery, cancellation, and compensation | A second workflow engine, provider identity, or semantic planning |
| `internal/state/` | Durable operation, plan, generation, activation, and observation projections | Provider runtime truth or unverified completion claims |
| `contracts/` | Versioned neutral capability, provider, operation, recipe, and observation schemas | Runtime state, UI behavior, or implementation loading |
| Opute Platform / LLM | Intent, retrieval, stochastic planning, semantic outcomes, and authorized context | Direct host mutation or bypassing Host Agent admission |

The TUI is a separate client in the sibling Opute repository. It consumes the
public Host Agent MCP contract and must not reimplement this kernel.

## Cordis semantics that are binding

The names may be Go-specific, but the behavior is not optional.

### Services, plugins, and fibers

- A service is identified by a stable `ServiceKey` and exposed through a
  provider-neutral contract.
- A plugin declares dependencies with `Inject`. Mounting fails until those
  dependencies are present; boot order must follow the dependency graph rather
  than a growing hand-written sequence.
- `Plugin.Apply` registers the plugin's services, listeners, and effects in an
  owned fiber. A failed apply disposes all partial registrations.
- A fiber owns every registration it creates. Disposal is idempotent and
  reverses ownership order until the fiber is quiescent.
- A provider MCP adapter is a plugin implementation. The Cordis context sees
  the typed service it provides, not its URL, MCP session, tool names, or JSON-
  RPC payloads.

### Typed events

Event mode is part of the event's public contract:

| Mode | Semantics | Appropriate use |
| --- | --- | --- |
| `emit` | Observe in registration order; no result | Telemetry and durable observation |
| `waterfall` | Ordered delegation with `next`; result-producing | Policy/interception where a listener may deliberately deny or short-circuit |
| `parallel` | Await listeners concurrently; no result | Independent observers or effects |
| `serial` | Await listeners in order; result-producing | Ordered transformations with an explicit contract |

Direct capability calls use service methods. Interception and policy use typed
events. Events must not become an untyped replacement for service methods.

For a waterfall, calling `next` delegates; returning without `next` is an
intentional short-circuit. A listener must not silently rewrite the event's
meaning. Policy listeners are deny-only: they may deny or request approval,
but cannot force allow, mutate arguments, or invent a successful result.

### Provider generations

Provider setup is candidate-based:

```text
descriptor
  -> MCP negotiation and discovery
  -> manifest/schema/dependency validation
  -> candidate generation
  -> provider-owned setup through the generic plan runner
  -> neutral capability/readiness evidence
  -> atomic activation
  -> bounded drain of the previous generation
  -> reverse-order disposal
```

The previous active generation remains available until the candidate is ready.
New work uses the active generation; existing sessions and in-flight work stay
affine to the generation that accepted them. Restart or an unconfirmed
transition produces `unknown`, not success. A failed candidate cannot displace
the previous active generation.

## LLM and tool authority

These rules are the most important protection against ad-hoc orchestration.

### Arguments are not orchestrator state

The Host Agent must pass model-generated tool arguments through without:

- enrichment from examples, prior turns, topology, or guessed producers;
- normalization or repair;
- product-specific field validation;
- identifier synthesis or name-to-ID guessing; or
- a hardcoded route to a different tool.

The owning tool/provider contract is authoritative for its input and semantic
validation. The Host Agent performs only the protocol work needed to invoke
the declared capability and preserves the tool's typed success or error. Raw
model JSON arguments must remain available in the durable call evidence so the
model request, audit record, UI, and execution input cannot diverge.

This does not prohibit a tool from rejecting invalid input. It prohibits the
orchestrator from pretending to know the internals of a tool and adding a
second validation system around it.

### Intent satisfaction is model-owned

The orchestrator must not set, infer, or clear `satisfied` from:

- a successful tool call;
- a non-empty assistant response;
- the number of completed tool steps;
- a name match or identifier pattern; or
- a fixed list of expected tools.

Only an explicit LLM/Platform typed semantic outcome may assert intent
satisfaction. The Host Agent records that outcome and exposes its evidence; it
does not manufacture the outcome.

### Root-cause rule for bad calls

When a model makes an invalid call, preserve the call and the owning tool's
typed error. Apply five whys across the actual boundaries:

1. What exact model argument and tool error occurred?
2. Which typed contract failed to provide or preserve the needed evidence?
3. Which owner should have supplied that evidence or semantic decision?
4. Why did the contract/test not detect the mismatch before dispatch?
5. What invariant and regression E2E proof prevents recurrence?

For example, a database inventory returning no rows was followed by a model
using managed-cluster ID `cluster:yj5z083j` as a database ID for `list_tables`.
The owning tool rejected the value. The root fix is producer evidence and
LLM/Cordis state projection, not an orchestrator regex or a special case for
that identifier.

## MCP 2026-07-28 requirements

Every Host Agent/provider MCP seam must be reviewed against the
[official MCP specification](https://modelcontextprotocol.io/specification/2026-07-28)
and the repository's MCP compliance tests.

At minimum, a provider adapter must:

- use Streamable HTTP and negotiate the required protocol revision;
- perform the standard initialize and capability discovery exchange;
- use `tools/list` and `tools/call` according to the negotiated contract;
- preserve structured content, typed errors, cancellation, and task state;
- keep request/response correlation and terminal stream events intact; and
- reject unsupported or incomplete protocol behavior as a typed failure.

Do not add stdio, legacy HTTP+SSE, invented task-result methods, or silent
compatibility fallbacks to make a test pass. If compatibility is required, it
needs a separately approved migration decision and an explicit contract.

MCP `serverInfo`, tool descriptions, annotations, and tool names are not
identity or authorization. Trust comes from the descriptor, endpoint policy,
artifact/revision evidence, and validated provider manifest.

## Normative invariants

The following invariants apply to current code and future changes. A violation
requires either a root-cause fix or an explicit ADR update; it must not be
covered by a local heuristic.

**C-01 — Provider-neutral core.** Core packages may know typed contracts,
capability IDs, schemas, and generic transport rules, but not concrete
provider lifecycle behavior.

**C-02 — MCP opacity.** MCP knowledge stops at `internal/cordis/mcp`. The
Cordis context receives typed services and effects, never transport objects.

**C-03 — One executor.** `internal/plan.Runner` is the only Host Agent plan
executor. Providers cannot add shell, callback, recipe, or workflow runners.

**C-04 — Tool-owned argument validation.** The orchestrator does not validate,
rewrite, enrich, or reject tool-specific arguments. The owning tool returns
the typed input error.

**C-05 — Model-owned satisfaction.** The orchestrator never determines or sets
semantic intent satisfaction.

**C-06 — No heuristic recovery.** No name tables, regex-based product guards,
fixed tool sequences, or hardcoded fallback branches may compensate for a
missing typed contract or model decision.

**C-07 — Durable truth.** Host Agent state is authoritative for Host Agent
operations. Provider process state, MCP task state, old events, and assistant
prose are evidence, not completion authority.

**C-08 — Generation affinity.** Active, candidate, draining, and stopped
generations are explicit. Existing work never silently migrates between them.

**C-09 — Reversible effects.** Every service, listener, task, overlay,
credential reference, process, and connection has an idempotent disposer.

**C-10 — Redacted evidence.** Secrets never enter prompts, tool arguments,
events, task projections, operation records, traces, or inspector output.

**C-11 — LLM-independent core.** MCP serving, typed host operations,
catalog/status, approval, plan validation, task inspection, cancellation, and
recovery remain usable when no LLM is installed or healthy.

**C-12 — One system container.** Disposable validation and development
environments may create exactly one system container. A second container must
not appear as an implicit fallback; capacity is expressed by the environment
profile and coordination state, not by a hidden orchestrator guard.

**C-13 — WSL-owned coordination.** Beads/Dolt uses the WSL-owned workspace and
listener only. Do not recreate the retired Windows Dolt runtime or create a
checkout-local `.beads` ledger.

**C-14 — Boundary proof.** Directory layout is not proof of isolation. Import
graph checks and separate-process MCP tests must prove the dependency and
runtime boundaries.

## Permanent invariant capture

Every Host Agent plan, refactor, new capability, schema change, lifecycle
change, or transport change must state its invariant delta before milestone
closure. Use the Opute repository's
[permanent invariant guide](../../opute/.agents/guides/permanent-agentic-invariants.md)
for the shared capture protocol and authority hierarchy.

The Host Agent-specific rule is:

- preserve the owning typed contract and package boundary as the enforcement
  point;
- add a package-owned verifier/test that can falsify the invariant;
- add real MCP, model-request, external-state, or cleanup evidence when the
  change crosses that boundary; and
- treat a conflict as an implementation defect unless an explicit ADR and
  superseding decision changes the invariant.

Do not copy the full invariant catalog into plans, skills, or memories. Link to
the authoritative decision and record only the local evidence or failure lesson
needed by future work.

## Development workflow

For a new capability or provider change:

1. Define the neutral contract and identify its service definition, provider,
   and consumer.
2. State the dependency graph, event modes, ownership scope, and disposal
   behavior before implementing the provider.
3. Keep MCP negotiation and wire mapping in `internal/cordis/mcp`; keep
   provider semantics in the owning tool/provider.
4. Route host mutation through the existing admission, recipe, and
   `internal/plan.Runner` path.
5. Add package-owned invariant tests against authoritative state/events.
6. Add a real Streamable HTTP provider test; mock only nondeterministic
   external systems such as network, clock, or model sampling.
7. Add E2E evidence for the actual model request, raw tool arguments, tool
   result, durable events, and externally observed state.
8. Re-read this guide and the relevant plan/ADR before milestone closure. Mark
   the milestone only after its validation is complete, then make the required
   green commit and push before starting the next milestone.

## E2E release gate

A passing unit test, health endpoint, HTTP 200, assistant sentence, or live
process is insufficient. A Cordis/MCP milestone is green only when the
evidence includes all applicable items:

- real published Host Agent composition and entry path;
- MCP `initialize`, negotiated revision, `tools/list`, and `tools/call`;
- complete Streamable HTTP response/stream termination;
- the actual LLM request when an agentic path is under test;
- raw model tool-call arguments and the owning tool's structured result/error;
- durable session, operation, generation, catalog, and lifecycle evidence;
- authorization/approval and cancellation behavior;
- external world state after mutation and after cleanup;
- reverse-order disposal with no orphaned listener, process, task, or overlay;
- exactly one system container in the disposable environment; and
- WSL-only coordination evidence, including the active WSL Dolt adapter path.

When a gate fails, record the five-whys analysis and fix the owning contract or
lifecycle seam. Do not add a test-only bypass, a model-specific prompt hack,
or a deterministic orchestrator heuristic.

## Reference material

- [Cordis architecture](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md)
- [Cordis primer](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/cordis-primer.md)
- [Capability seams](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/capability-seams.md)
- [Tool execution pipeline](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/tool-execution-pipeline.md)
- [Cordis invariants](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/invariants.md)
- [Testing](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/testing.md)
- [DeepSeek Harness MCP client](https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/mcp/mcp-client/README.md)
- [MCP 2026-07-28 specification](https://modelcontextprotocol.io/specification/2026-07-28)
- [Host Agent provider architecture ADR](adr/0002-provider-extension-architecture.md)
