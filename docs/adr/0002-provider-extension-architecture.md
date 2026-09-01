# ADR 0002: Cordis provider services over MCP

Status: accepted direction; implementation plan pending execution

Date: 2026-08-22

This document supersedes the earlier provider-extension draft. It records the
final boundary and the implementation plan for trusted Ollama and Cloudflare
providers.

## Context

Opute Host Agent needs first-party capabilities such as LLM serving and
tunneling, but the concrete implementations depend on external systems. The
Host Agent must own safety-critical behavior—admission, authorization,
recipes, durable operations, recovery, activation, and evidence—without
absorbing Ollama or Cloudflare lifecycle knowledge into its core.

The providers are trusted first-party components stored in this repository:

```text
plugins/
  llm/ollama/
  tunneling/cloudflare/
```

Each provider is implemented as an MCP server using Streamable HTTP. The
provider is aware of the Opute service contract and lifecycle rules. The
Cordis service layer is deliberately unaware that the implementation happens
to use MCP: it sees an injected service and its typed operations, not a
transport-specific object.

The current repository already has useful foundations:

- `internal/recipe/recipe.go` and `internal/recipe/source.go` resolve,
  canonicalize, hash, and validate recipes;
- `internal/plan/runner.go` executes validated plans with retries, readiness,
  recovery, cancellation, compensation, and durable state events;
- `internal/catalog/registry.go` provides guarded dynamic catalog overlays;
- `internal/hostmcp/server.go` already has `RegisterCapability`,
  `CatalogSnapshot`, `DispatchTool`, task handling, and the MCP dispatch
  boundary;
- `internal/hostmcp/recipe_run.go` and `internal/hostmcp/tunnel_run.go` own
  recipe operations and activation validation;
- `internal/ops/runtime_probe.go` contains a runtime-neutral OpenAI-compatible
  observation and streaming-chat probe.

The current Ollama and llama-server implementation code in
`internal/ops/ollama.go`, `internal/ops/llama_server*.go`, and
`internal/ops/local_llm_types.go` still mixes provider-specific behavior with
generic host operations. This plan moves that knowledge behind the provider
boundary. Ollama is the only LLM provider in the current scope. Llama and
llama.cpp are explicitly deferred.

The Host Agent's core functionality must remain usable when no LLM provider is
installed, configured, reachable, or healthy. The TUI, headless mode, MCP
server, host/infrastructure operations, status and diagnostics, approvals,
recipe validation, task inspection, cancellation, and recovery must not depend
on an LLM. LLM serving and application chat are optional capability layers.

Grounding note: the interactive-client material below records the former
terminal-client boundary for historical context. The active contract is the
public MCP surface; this ADR owns the provider, lifecycle, and Host Agent
boundaries that external clients consume.

## Decision

Opute will adopt a Cordis-style service architecture for provider composition:

1. The Host Agent defines stable service contracts, dependencies, lifecycle
   effects, typed events, and disposal semantics.
2. A generic MCP-backed adapter registers each provider into the Cordis-style
   service context.
3. The provider MCP server implements the Opute contract over Streamable HTTP.
4. Recipes remain Host Agent-owned declarative plans. Providers return a
   manifest; they do not install host software themselves.
5. The Host Agent validates and executes the manifest's recipes, validates the
   resulting capability, and only then activates the provider.

The precise dependency direction is:

```text
Host Agent Cordis-style service contract
                 ↓
       MCP provider adapter
                 ↓
       Streamable HTTP only
                 ↓
   Ollama or Cloudflare MCP server
                 ↓
       Ollama or Cloudflare API/CLI
```

The MCP server is an implementation of a remote Cordis service. The
Host-Agent-side MCP adapter is the Cordis plugin registered in the service
context. A provider does not need to embed the Cordis library to participate;
it must implement the Opute service contract. A provider may use Cordis
internally, but that is an implementation choice hidden behind the contract.

The Host Agent remains a Go application. It will implement the small,
Cordis-inspired service kernel needed by Opute rather than introducing an
unbounded runtime loader or making the Go binary depend on a Node runtime. The
upstream Cordis concepts are adopted at the architectural boundary:
service definition, provider/consumer separation, explicit injection,
reversible effects, typed events, and scoped cleanup.

The Host Agent core is not aware of the TUI or of the Ollama and Cloudflare
MCP implementations. The TUI is a client of the Host Agent public contract;
the provider MCPs are implementations of Host Agent-owned contracts. They are
separate modules and processes even when they are maintained in this
repository.

## Module and process boundaries

The repository layout must make the dependency direction visible and
enforceable. Co-location under one repository is not permission to import
across these boundaries:

```text
opute-host-agent/
  contracts/                         # versioned, neutral schemas only
    host-agent/
    provider/
    capability/
  pkg/hostagentclient/               # public Host Agent client/projections
  cmd/opute-host-agent/              # Host Agent core/server only
  internal/                          # Host Agent core implementation only
    cordis/
    hostmcp/
    recipe/
    plan/
    catalog/
    state/
    ops/
  # The user-facing terminal client is an external Bun/TypeScript application
  # in an external client repository.
  # This Go repository contains no supported TUI module or launcher.
  plugins/
    llm/ollama/                      # separate provider MCP module/process
      plugin.yaml
      go.mod or package manifest
      server/
      contract/
      recipes/
    tunneling/cloudflare/            # separate provider MCP module/process
      plugin.yaml
      go.mod or package manifest
      server/
      contract/
      recipes/
  bundles/first-party/               # generic composition manifests only
```

The target dependency rules are:

```text
compile-time dependencies:

  Host Agent core ──┐
  TUI client ────────┼──> neutral contracts/schemas
  provider MCPs ────┘

runtime calls:

  TUI client ── Streamable HTTP ──> Host Agent core
  Host Agent core ── generic adapter + Streamable HTTP ──> provider MCP
```

More precisely:

- `cmd/opute-host-agent` and `internal/` may import neutral contracts and
  generic Host Agent packages, but may not import the external TUI client,
  `plugins/llm/ollama`, `plugins/tunneling/cloudflare`, or any concrete
  provider package. Concrete provider IDs may appear only as data in an
  externally supplied, validated descriptor; provider-specific symbols and
  lifecycle code must not appear in the core.
- External clients may import only the public MCP contract,
  neutral schemas, and the shared Streamable HTTP MCP client. It may not import
  Host Agent `internal` packages, recipe/plan/state stores, or either provider
  MCP. The Host Agent repository must not grow a replacement in-process TUI.
- Each provider MCP may import neutral contracts and a public provider SDK,
  and may call the Host Agent's public contract where explicitly required. It
  may not import the Host Agent `internal` packages or another
  provider. It knows the Host Agent contract, not the TUI.
- `bundles/first-party` contains declarative composition data only. It may
  name provider descriptors as data, but it must not become a package that
  imports or embeds provider implementation code into the Host Agent core.
- Shared mutable state, direct Go calls, in-process callbacks, and UI/provider
  convenience imports are prohibited across these boundaries. Communication
  uses versioned contracts and the declared Streamable HTTP interfaces.

External interactive clients are separate applications. The Host Agent binary serves MCP
only and does not launch a client. The historical `internal/tui` package was
scheduled for deletion as an unreferenced migration artifact; it is not part
of the command path and no core package may import it. New terminal behavior
belongs in the external client and communicates over the public Streamable HTTP
MCP boundary.

## Cordis conformance profile

This section is normative for the Opute Cordis-style interface. It records the
official Cordis shape and the exact Opute adaptation. “Cordis-compatible” does
not mean that a remote MCP process is magically an in-process TypeScript
plugin; the Host Agent-side MCP adapter is the plugin that satisfies this
profile.

### Official Cordis shape we preserve

The official Cordis model has these concrete forms:

1. A plugin is an object implementing `Service`. It may be a function with an
   optional `inject` list and `apply(ctx)` function, or a `Service` subclass
   mounted into a context.
2. A `Context` is a repository of stable services addressed by keys such as
   `ctx.tools`, `ctx.llm`, or `ctx.sessions`. Consumers depend on the service
   key, not a concrete provider implementation.
3. `inject` declares required services. Cordis delays plugin activation until
   those services exist; load order is a consequence of dependencies, not
   hand-written boot sequencing.
4. `ctx.plugin(plugin)` mounts a plugin and returns a `Fiber`. The fiber owns
   the plugin's services, effects, and listeners.
5. `ctx.effect()` and `ctx.on()` create reversible registrations. Disposing
   the owning fiber removes those services, effects, and listeners.
6. Events are typed and their dispatch mode is part of the public contract:

   | Method | Awaited | Order | Return value |
   | --- | --- | --- | --- |
   | `emit` | no | registration order | none |
   | `waterfall` | no | registration order | yes |
   | `parallel` | yes | concurrent listeners | none |
   | `serial` | yes | registration order | yes |

7. `waterfall` listeners receive `(...args, next)`. Calling `next()` delegates
   to the next listener and propagates its result; returning without `next()`
   short-circuits. `prepend: true` is reserved for listeners that must run
   before ordinary registrations.
8. Direct capability calls use service methods. Interception and policy use
   events. Events must not become an untyped substitute for service methods.
9. Every registration has a disposer, and related effects are grouped when
   disposal order matters.
10. Event names are declared in a typed event map; the dispatch mode is part
    of each event's public contract and is checked against its dispatch sites.

These are acceptance properties, not merely terminology. The Opute test suite
must exercise the behavior of each method above.

### Opute equivalent shape

The Host Agent's Go implementation mirrors the semantics with an Opute-owned
kernel. The exact Go names may differ, but the shape is fixed:

```go
type Plugin interface {
	ID() string
	Inject() []ServiceKey
	Apply(*Context) (Effect, error)
}

type Service interface {
	Key() ServiceKey
	Descriptor() ServiceDescriptor
}

type Effect interface {
	Dispose(context.Context) error
}

type Fiber interface {
	Dispose(context.Context) error
}
```

The required semantics are:

```text
Context.Plugin(plugin)       -> mounts after Inject dependencies resolve
Context.Effect(effect)       -> registers an owned reversible effect
Context.On(event, listener)  -> registers an owned typed listener
Context.Emit(...)            -> observe-only event dispatch
Context.Waterfall(...)       -> around/policy dispatch with next()
Context.Parallel(...)        -> awaited concurrent dispatch
Context.Serial(...)          -> awaited ordered dispatch
Fiber.Dispose(ctx)           -> reverse-unwinds the plugin's owned scope
```

The name mapping is intentional and must remain visible in code review:

| Official Cordis API shape | Opute kernel equivalent |
| --- | --- |
| `ctx.plugin(plugin)` | `context.Plugin(plugin)` / `Mount` |
| plugin `inject` | `Plugin.Inject()` |
| plugin `apply(ctx)` | `Plugin.Apply(context)` |
| `ctx.effect(...)` | `context.Effect(...)` |
| `ctx.on(event, listener)` | `context.On(event, listener)` |
| `ctx.emit(...)` | `context.Emit(...)` |
| `ctx.waterfall(...)` | `context.Waterfall(...)` |
| `ctx.parallel(...)` | `context.Parallel(...)` |
| `ctx.serial(...)` | `context.Serial(...)` |
| `root.fiber.dispose()` | `fiber.Dispose(ctx)` |

The Opute names may follow Go capitalization, but they must preserve the
official method semantics and ownership rules. A rename that changes behavior
is a contract change, not a stylistic cleanup.

The MCP adapter implements `Plugin.Apply`: it connects over Streamable HTTP,
performs discovery, validates the provider manifest and service definitions,
registers typed service methods, and returns one effect whose disposer closes
tasks/connections, unregisters services, and stops a Host Agent-owned provider
process. The Cordis context does not receive MCP-specific objects.

### Three-role seam requirement

Every capability must have all three roles:

1. **Service Definition:** the stable Opute contract and schemas;
2. **Service Provider:** the Ollama or Cloudflare MCP-backed adapter;
3. **Consumer:** the Host Agent router, recipe/activation flow, or canonical
   client operation.

One role alone is not a seam. A provider tool list without a Host Agent
consumer is not an Opute capability; a Host Agent interface without a provider
and validation flow is not an implemented capability.

### Composition and privilege boundary

DeepSeek Harness composes a product as a plugin tree, with profiles and
bundles layering plugin configuration and reversible registrations. Opute
adopts the plugin-tree and service-seam ideas for trusted first-party
providers. It also defines the lifecycle abstractions needed for controlled
provider generations and reloads, but does not expose arbitrary user patching,
file-watcher HMR, or unbounded runtime code loading in this release.

Opute retains a privileged Host Agent policy kernel for admission,
authorization, durable state, recipe execution, and activation. “No
provider-specific privileged core” is the invariant; the security boundary
itself remains privileged by design.

## External client boundary and typed execution surface

The former terminal-client design is retained here as historical context. Its
ownership boundary was:

| Area | Owns | Must not own |
| --- | --- | --- |
| External client | Catalog-driven editing, typed lookup, review, rendering, operation/status projection, and explicit user approval | Natural-language intent classification, retrieval, authorization, operation ordering, recipe execution, Cordis graph reimplementation, or provider lifecycle |
| Host Agent | Current catalog/schema validation, approval/admission enforcement, execution, durable operation state, cancellation/recovery, and redacted evidence | Natural-language intent, retrieval, or silently choosing a provider for an ambiguous request |
| Opute Platform | Natural-language intent, retrieval, authorization, and multi-step planning; optional authorized context projections | Direct host mutation or bypassing Host Agent validation and admission |

The TUI has separate presentation and execution state. Editor states are
`typing`, `committed`, `pending`, and `gated`; execution states are
`submitted`, `working`, `succeeded`, `failed`, `cancelled`, `unknown`, and
`stale`. Completions, required fields, defaults, enums, descriptions, effects,
and approval requirements come from the current capability catalog and JSON
Schema. Product-specific category lists are forbidden.

The presentation-only `CommandDraft` retains the catalog revision and typed
`DraftValue` references. Entity and output bindings retain canonical values,
provider/source, selection evidence, producer operation identity, schema
identity, and observation revision. Rendered tokens such as `@vm:worker` are
not execution input. Stale catalog revisions, stale observations, unavailable
entities, and schema-incompatible bindings block visibly and fail closed.

Deterministic mode must operate without an LLM. Its flow is:

```text
draft
  -> resolve typed bindings
  -> validate current catalog and schema
  -> apply effect and approval policy
  -> execute exactly once
  -> poll durable operation or plan status
  -> record redacted result and observation
```

Agentic mode is optional. It may consume a trusted Platform adapter, but the
provider must return a validated `assistant-session.v1` command proposal or
`host-plan.v1` proposal. The TUI renders that proposal as deterministic calls;
the Host Agent validates and executes it. No prose, rendered text, or local TUI
heuristic may directly execute a mutation. UI approval and a capability
argument such as `confirm=true` remain separate; the TUI never inserts the
argument automatically.

The TUI does not initialize or inject `ctx.llm` for core navigation, status,
approvals, task logs, cancellation, recovery, or infrastructure operations. A
future live context panel may consume only an authorized Platform
snapshot-plus-delta projection with revisions, causes, ordering, and
redaction; it must not infer context from trace text.

## Scope and non-goals

### In scope

- Trusted in-repository provider packages under `plugins/`.
- Independently buildable Host Agent core, TUI client, and provider MCP
  modules/processes with neutral contract boundaries.
- `llm-serving.v1` implemented by Ollama only.
- `tunneling.v1` implemented by Cloudflare only.
- Provider MCP servers using Streamable HTTP only.
- Generic Host Agent MCP client, service adapter, lifecycle, catalog, and
  durable operation integration.
- Provider-generation tracking, bounded draining, atomic replacement,
  rollback, and fiber-owned disposal abstractions.
- Provider installation manifests and generic recipe execution.
- Managed and externally configured Ollama modes.
- Generic tunnel recipes using the existing tunneling plan direction.
- Full core Host Agent and TUI operation with no LLM provider available.

### Explicitly out of scope

- MCP stdio transport.
- Llama or llama.cpp provider support.
- Runtime loading of arbitrary Go shared libraries.
- Arbitrary user patching, file-watcher HMR, and silent live mutation of an
  active provider generation.
- Provider-owned installation engines or unrestricted shell scripts.
- A second plan executor inside a provider.
- Treating MCP `serverInfo`, tool descriptions, or annotations as trust
  authority.
- Making provider setup success equivalent to application-level `/chat`
  success.

## Normative invariants

These are implementation constraints, not recommendations. A change that
violates one requires an ADR update and an explicit decision; it must not be
smuggled in as a provider convenience.

### Boundary and dependency invariants

**I-01 — Contract direction.** The Host Agent defines the capability and
Cordis-style service contracts. Providers implement them. The Host Agent core
must never import the TUI, Ollama MCP, Cloudflare MCP, or another concrete
provider package.

**I-02 — MCP opacity.** The Cordis service context sees a typed service
adapter, not an MCP client, URL, tool name, or JSON-RPC detail. MCP knowledge
is isolated in the generic adapter layer.

**I-03 — Provider dependency inversion.** A provider may depend on public Opute
schemas and the provider SDK. It may not import `internal/hostmcp`,
`internal/state`, `internal/plan`, `internal/resource/admission`, or another
  provider package, `pkg/hostagentclient`, or the external TUI client.

**I-04 — Capability dependencies only.** Provider dependencies name versioned
capabilities such as `opute.capability.tunneling.v1`, never concrete providers
such as `com.opute.cloudflare`.

**I-05 — No hidden executor.** There is exactly one Host Agent plan executor:
`internal/plan.Runner`. Providers return manifests and typed operations; they
cannot introduce a second recipe, shell, workflow, or callback executor.

### Trust and transport invariants

**I-06 — Trusted package boundary.** Only explicitly registered first-party
providers under `plugins/` are enabled in this release. Repository presence
alone is not runtime registration, and no arbitrary Go shared library is
loaded.

**I-07 — Streamable HTTP only.** Every provider MCP connection uses Streamable
HTTP. Stdio, legacy HTTP+SSE, and an undocumented alternate transport are
rejected before provider registration.

**I-08 — Modern MCP only.** New providers implement MCP `2026-07-28`,
`server/discover`, per-request protocol metadata, tool schemas, cancellation,
and negotiated extensions. A compatibility fallback may be added only as an
explicit migration decision; it is not part of the first-party provider gate.

**I-09 — Self-description is not trust.** `serverInfo`, descriptions,
instructions, icons, annotations, and tool names cannot establish provider
identity or authorization. Identity comes from the trusted descriptor,
endpoint policy, artifact hash, and verified manifest.

### Installation and execution invariants

**I-10 — Manifest is read-only.** Calling
`opute.provider.get_install_manifest` cannot install software, start/stop a
service, change configuration, create a tunnel, change the active provider,
or create a task with side effects.

**I-11 — Host-owned mutation.** All provider prerequisites are applied through
Host Agent admission, authorization, recipes, `DispatchTool`, and
`internal/plan.Runner`. A provider MCP server cannot run arbitrary host shell
commands or call back into Host Agent MCP to request mutation.

**I-12 — Immutable recipe inputs.** A mutating recipe requires a validated
contract version, source policy, immutable revision where remote, expected
SHA-256, bounded size, resolved variables, compatible host catalog, and
redacted persisted inputs before its first action.

**I-13 — No recursive execution.** A provider, manifest, recipe node, or
validation node cannot create a nested host-plan or runtime-recipe run.

**I-14 — Typed readiness.** Readiness and activation use typed generic
observations and contract validation. A process existing, a health endpoint
returning HTTP 200, or a completed install command is insufficient.

### Registration and activation invariants

**I-15 — Atomic registration.** A provider is registered only after discovery,
manifest validation, dependency resolution, and adapter construction succeed.
Partial registrations are rolled back.

**I-16 — Catalog revision.** Every registration or unregistration changes the
catalog revision. Stale client snapshots cannot be used to execute a changed
capability without revalidation.

**I-17 — Activation is last.** The active-provider pointer changes only after
the candidate's neutral capability validation succeeds. A failed or cancelled
candidate cannot displace the previous active provider.

**I-18 — Stable public contract.** Provider identity and implementation details
are metadata. Canonical capability operation schemas remain provider-neutral;
provider-specific fields are namespaced and optional.

### State, cleanup, and evidence invariants

**I-19 — Host Agent is authoritative.** Host Agent operation state is the
source of truth. Provider process state, MCP Task state, and emitted events are
evidence inputs, not completion authority.

**I-20 — Restart is not success.** Any `working` provider/recipe operation
becomes `unknown` on restart. Resume uses persisted recipe and manifest data,
revalidates the current host and provider, and never reports automatic
success.

**I-21 — Disposers are mandatory.** Every service registration, MCP client,
task, catalog overlay, event subscription, temporary credential, and active
pointer effect has an idempotent disposer. Disposal runs in reverse ownership
order.

**I-22 — Redaction is end-to-end.** Secret values never appear in manifests,
expanded plans, logs, task arguments, operation records, evidence, structured
content, or text fallbacks. Persist references and redacted placeholders only.

**I-23 — Evidence is attributable.** A successful operation records provider
ID/version, manifest hash, recipe hash, plan run ID, catalog revision,
capability contract, validation observation, and active-provider transition.

### Scope invariants

**I-24 — Ollama only for LLMs.** `llm-serving.v1` is implemented by Ollama in
this release. No Llama/llama.cpp implementation, enum, recipe, or acceptance
gate may be added under this plan.

**I-25 — Tunneling is independent.** Cloudflare implements `tunneling.v1`; it
is optional and must not be required for local Host Agent MCP operation.

**I-26 — Chat is a separate gate.** Provider setup and Host Agent activation
must not be reported as application chat success. `/chat` requires its own
literal-prompt, parsed-stream, and trace-evidence canary.

**I-27 — Core availability is LLM-independent.** The Host Agent process,
Streamable HTTP MCP surface, TUI/headless controls, host/infrastructure
operations, status/diagnostics, approvals, recipe validation, task inspection,
cancellation, and recovery must start and remain usable when no LLM provider
is installed, configured, reachable, or healthy.

**I-28 — Optional capability failure is typed.** A core operation that truly
requires `llm-serving.v1` returns a typed capability-unavailable result with
provider state and remediation. It must not crash startup, hide unrelated
core tools, deadlock the TUI, or silently invoke a different provider.

**I-29 — UI composition is capability-scoped.** The TUI must not initialize,
resolve, or inject `ctx.llm` merely to render core navigation, status,
operations, approvals, task logs, or recovery controls. Chat/AI assistance is
an optional view/plugin with an explicit unavailable state.

**I-30 — TUI is presentation-only.** The TUI may edit, resolve typed
references, review, render, submit, and project Host Agent state. It may not
perform intent classification, retrieval, authorization, operation ordering,
recipe execution, or provider lifecycle work.

**I-31 — Catalog is authoritative.** TUI signatures, defaults, enums, effects,
approval requirements, and required fields must be derived from the current
catalog revision and JSON Schema. Hardcoded product categories and provider
specific argument knowledge are prohibited.

**I-32 — Deterministic mode is LLM-independent.** Drafting, typed lookup,
validation, approval, execution, status, cancellation, recovery, and core
infrastructure workflows must work without `ctx.llm`, an LLM endpoint, or LLM
credentials.

**I-33 — Agentic input is typed.** Agentic mode may execute only a validated
`assistant-session.v1` command proposal or `host-plan.v1` proposal. Prose,
rendered command text, and unvalidated provider output can never be an
execution input.

**I-34 — Binding provenance is preserved.** Entity and output bindings carry
their canonical value, source/evidence, producer identity, schema identity,
catalog revision, and observation revision. Stale or incompatible bindings
fail closed and are never silently rewritten.

**I-35 — Approval is separate from arguments.** A UI approval decision and a
capability argument such as `confirm=true` are distinct controls. The TUI and
agentic adapter must never synthesize a confirmation argument.

**I-36 — Reconnect does not replay mutation.** Lost operation status, MCP
session reconnect, or TUI reconnect may poll and reconcile durable state, but
must not automatically resubmit the original mutation.

**I-37 — Inspector output is redacted.** Credentials, secrets, hidden prompts,
and unauthorized output are absent from TUI inspector panels, typed-yank
actions, clipboard content, task projections, and operation evidence.

**I-38 — Context projection is authorized.** A future live context panel may
consume only an authorized Platform snapshot-plus-delta projection with
revision IDs, ordering, causes, and redaction. It must not reconstruct context
from trace text.

**I-39 — Provider generations are explicit.** Every mounted provider instance
is represented by an immutable `ProviderGeneration` containing provider
identity, implementation version, manifest/artifact hash, endpoint, Cordis
fiber, catalog revision, and lifecycle state.

**I-40 — One active generation per capability.** At most one generation may
serve new requests for a given capability and provider identity. A candidate
generation is not active merely because it is registered or reachable.

**I-41 — Readiness precedes activation.** A candidate generation must complete
MCP handshake, manifest/schema validation, dependency resolution, neutral
capability validation, and readiness probes before the active pointer changes.

**I-42 — Work is generation-affine.** New sessions use the active generation;
existing Streamable HTTP sessions and in-flight operations remain associated
with the generation that accepted them. Silent cross-generation migration is
forbidden.

**I-43 — Drain is bounded and observable.** Reload stops admission of new work
to the draining generation, permits bounded completion of existing work, and
records timeout, cancellation, and forced-disposal outcomes explicitly.

**I-44 — Activation is atomic and reversible.** The previous active generation
remains available until the candidate is ready. Failed startup or post-switch
health checks leave or restore the previous healthy generation; partial swaps
are not valid states.

**I-45 — Generation disposal is complete.** Disposing a generation's fiber
removes its services, catalog overlays, event listeners, MCP sessions, tasks,
credentials, supervised process, and active-pointer effects in reverse
ownership order.

**I-46 — Reload preserves durable truth.** Provider operations persist
generation identity and become `unknown` after restart or an unconfirmed
drain interruption. Recovery revalidates before acting and never infers
success from process existence or an old provider event.

**I-47 — Host Agent core is UI/provider-implementation blind.** The Host Agent
core may know generic provider descriptors, capability IDs, schemas, and
transport rules, but it must not import, link, instantiate, or contain
provider-specific lifecycle code for the TUI, Ollama MCP, Cloudflare MCP, or
any other concrete provider implementation.

**I-48 — TUI depends only on the public Host Agent surface.** The TUI may
depend on `pkg/hostagentclient`, neutral contracts, and Streamable HTTP MCP,
but it must not import Host Agent `internal` packages, provider MCP packages,
provider recipes, provider endpoints, or provider-specific tool names.

**I-49 — Provider MCPs depend only on public Host Agent contracts.** A provider
MCP may depend on neutral Opute contracts and a public provider SDK. It may
implement Host Agent-defined operations and use the declared Host Agent
interface, but it must not import TUI code, Host Agent `internal` packages,
another provider, or UI concepts.

**I-50 — Boundary communication is typed and directional.** The TUI talks to
the Host Agent; the Host Agent talks to provider MCPs; provider MCPs talk to
their concrete external systems. No participant bypasses its adjacent public
contract through shared mutable state, direct in-process callbacks, or
provider/TUI imports.

**I-51 — Separate module/process boundaries are mandatory.** The TUI and each
provider MCP are independently buildable modules/processes. A successful
single-repository build or co-located directory is not evidence of boundary
compliance; import-graph and runtime transport tests must prove it.

## Domain boundaries and ownership

The following table is normative. It defines what each area may know and what
it must not own.

| Domain | Owns | Must not own |
| --- | --- | --- |
| `contracts/` | Versioned neutral Host Agent, provider, capability, recipe, operation, and observation schemas | Runtime state, UI behavior, provider implementations, or transport-side lifecycle |
| `cmd/opute-host-agent/` and Host Agent `internal/` | Core daemon, generic MCP server/adapter, Cordis context, recipes, plans, catalog, state, admission, lifecycle, and evidence | TUI packages, concrete provider packages, provider-specific symbols, or UI rendering |
| `pkg/hostagentclient/` | Public Streamable HTTP client and typed Host Agent projections used by clients | TUI rendering, provider behavior, Host Agent internal state, or concrete provider knowledge |
| `internal/cordis/` | Service definitions, dependency resolution, scoped context, lifecycle, effects, disposers, typed events, provider generations, drain/reload coordination | MCP protocol details, Ollama/Cloudflare behavior, host shell execution |
| `internal/cordis/mcp/` | Streamable HTTP MCP client, discovery, tool/schema mapping, Tasks, cancellation, MCP auth | Provider-specific commands, recipe execution, activation policy |
| `internal/hostmcp/` | Public MCP server, dispatch/admission boundary, task projection, catalog exposure, provider orchestration | Concrete provider lifecycle, direct Ollama/Cloudflare assumptions |
| `internal/recipe/` | Decode, source policy, hashing, defaults, variables, compatibility, recipe validation | Side effects, MCP connections, provider-specific install logic |
| `internal/plan/` | Generic host-plan validation/execution, readiness, retry, recovery, compensation | Provider identity, package loading, capability-specific policy |
| `internal/catalog/` | Typed descriptors, registration validation, revisions, snapshots | Executing implementations, provider discovery, secrets |
| `internal/state/` | Durable operation, service, task, observation, provider-generation, catalog-revision, and active-pointer state | Provider behavior or transport logic |
| `internal/ops/` | Generic host primitives and neutral observations | Ollama lifecycle, Cloudflare lifecycle, provider-specific output fields |
| `plugins/llm/ollama/` | Ollama MCP implementation, Ollama manifest, contract mapping, Ollama recipe references, native metadata, and external Ollama API/CLI integration | Host Agent `internal` packages, admission, Host Agent state writes, generic plan execution, TUI, or other providers |
| `plugins/tunneling/cloudflare/` | Cloudflare MCP implementation, tunnel manifest, contract mapping, Cloudflare recipe references, native metadata, and external Cloudflare integration | Host Agent `internal` packages, admission, Host Agent state writes, generic plan execution, TUI, or other providers |
| External interactive clients | Catalog-driven typed drafts, lookup, review, rendering, explicit approval, and projection of Host Agent state | Host Agent `internal` packages, provider MCPs, provider endpoints/tool names, LLM-required startup, prose execution, setup paths, or reload decisions |
| `bundles/first-party/` | Declarative provider descriptors and composition data | Importing provider code into Host Agent core, UI behavior, or hidden lifecycle policy |
| Opute application `/chat` | Application-level chat orchestration and canary | Declaring Host Agent recipe success to be chat success |

The allowed compile-time dependency direction is:

```text
Host Agent core ────────────────> neutral contracts/schemas
TUI client ──> pkg/hostagentclient ──> neutral contracts/schemas
provider MCP ────────────────────> neutral contracts/schemas + provider SDK
```

The runtime call direction is:

```text
TUI client ── Streamable HTTP ──> Host Agent public MCP surface
Host Agent generic adapter ── Streamable HTTP ──> provider MCP
provider MCP ──> Ollama, Cloudflare, or another external system
```

The provider implementation must not point upward into Host Agent orchestration
or sideways into the TUI. The Host Agent may call a provider only through the
generic adapter, and the TUI may call only the Host Agent public surface. A
provider may not bypass the adapter to mutate Host Agent state, and the Host
Agent may not bypass the public client boundary to drive TUI behavior.

## Service contract

The Opute service contract is the stable port between the Host Agent and a
provider. It is independent of MCP, Ollama, Cloudflare, and the language used
to implement the provider.

### Service definition

Each service definition contains:

- a stable service/capability ID and version;
- typed operations with input and output JSON Schemas;
- effect classification: read, mutation, destructive, or credential-bearing;
- resource categories and admission requirements;
- dependencies and compatible version ranges;
- validation and readiness semantics;
- cancellation, retry, recovery, and compensation behavior;
- evidence and redaction rules;
- optional streaming behavior.

Initial service definitions are:

```text
opute.capability.llm-serving.v1
opute.capability.tunneling.v1
```

The LLM contract includes model discovery, endpoint readiness, and streaming
chat validation. The tunneling contract includes connector readiness,
declared bindings, public endpoint evidence, and teardown semantics.

The contract must not contain provider enums such as `ollama`, `llama-cpp`, or
`cloudflare` as required fields. Provider identity is metadata attached to an
implementation, not the capability definition.

### Cordis-style roles

| Cordis role | Opute role |
| --- | --- |
| Service definition | Versioned capability contract and schemas |
| Service provider | MCP-backed Ollama or Cloudflare provider adapter |
| Consumer | Host Agent router, recipe validator, activation flow, and clients |
| `inject` dependency | Typed `requires` entry resolved before registration |
| `ctx.effect()` | Connection, process, catalog, task, and activation effects |
| Disposer | Ordered cancellation, unregister, shutdown, and compensation |
| Typed event | Host Agent lifecycle, policy, validation, and operation event |
| Scoped context | Provider connection plus operation-scoped credentials/evidence |

The Host Agent must never import an Ollama or Cloudflare implementation into
the generic contract package. Provider packages may import the public Opute
contract schemas and provider SDK, but not private Host Agent internals.

## MCP provider contract

MCP is the wire protocol for the provider implementation. Cordis does not
need to know that the adapter uses MCP, and MCP does not need to implement the
whole Cordis model. Opute defines the thin bridge between them.

### Required provider behavior

Every provider MCP server MUST:

1. Use Streamable HTTP. Opute does not support stdio.
2. Implement MCP protocol revision `2026-07-28`.
3. Implement `server/discover` before other provider calls.
4. Advertise the Opute provider extension and supported capability contracts.
5. Expose deterministic `tools/list` results with input and output schemas.
6. Expose the read-only tool:

   ```text
   opute.provider.get_install_manifest
   ```

7. Return structured content plus a serialized text fallback when structured
   content is returned.
8. Support cancellation and MCP Tasks where a provider operation is long
   running.
9. Keep provider-specific tools namespaced and separate from canonical Opute
   capability operations.

The provider server does not call back into the Host Agent MCP server to ask
for installation. It returns a manifest; the Host Agent resolves and executes
the manifest through its own Cordis context and recipe runner.

### Opute MCP extension

Use the vendor-prefixed extension ID:

```text
com.opute/cordis-provider
```

The extension describes the wire representation of a Cordis-style provider:

```yaml
extension: com.opute/cordis-provider
version: 1
pluginId: com.opute.ollama
pluginVersion: 1.0.0
provides:
  - id: opute.capability.llm-serving.v1
    version: 1
requires: []
events:
  - provider.ready
  - provider.degraded
  - provider.stopped
```

This metadata is advisory until the Host Agent verifies it against the
trusted in-repository package descriptor and the installation manifest.
MCP `serverInfo` remains informational and cannot establish identity.

MCP's standard capabilities map as follows:

| Opute/Cordis behavior | MCP mechanism |
| --- | --- |
| Provider discovery | `server/discover` |
| Operation registration | `tools/list` and output schemas |
| Service call | `tools/call` |
| Long-running provider operation | MCP Tasks plus Host Agent operation |
| Tool/catalog revision | `listChanged`, cache metadata, and Opute catalog revision |
| Cancellation | Streamable HTTP response cancellation and task cancellation |
| Typed provider event | Negotiated Opute notification or explicit status operation |

MCP does not define a complete dependency-injection or disposal protocol. The
Opute extension supplies the missing contract, while the Host Agent remains
authoritative for dependency resolution, lifecycle ownership, and cleanup.

## Trusted provider package

The repository layout is source organization and a trust boundary, not a
runtime directory scanner. New provider code is built and tested with the
release, then explicitly registered in the provider package registry.

```text
plugins/
  llm/
    ollama/
      plugin.yaml
      go.mod or package manifest
      cmd/opute-provider-ollama/
      server/
      contract/
      recipes/
      tests/
  tunneling/
    cloudflare/
      plugin.yaml
      go.mod or package manifest
      cmd/opute-provider-cloudflare/
      server/
      contract/
      recipes/
      tests/
```

Each provider directory is an independently buildable MCP module/process. Its
`contract/` directory may reference the neutral schemas, but it must not
reach into the Host Agent `internal/` tree or an external client. The
Host Agent receives the generic descriptor and endpoint as composition data;
it does not compile or instantiate these provider packages.

The descriptor contains generic bootstrap data only:

```yaml
schema: opute-provider-plugin.v1
pluginId: com.opute.ollama
version: 1.0.0
capability: opute.capability.llm-serving.v1
server:
  transport: streamable_http
  endpoint: http://127.0.0.1:4318/mcp
  executable: opute-provider-ollama
  args: [serve]
  sha256: <immutable-artifact-sha256>
```

For a local provider, the Host Agent may supervise the provider process, but
it connects to the advertised HTTP endpoint. Process supervision and MCP
transport are separate concerns; there is no stdio MCP mode.

The descriptor is not an installation script. It cannot grant a provider
permission to mutate the host. The provider manifest and recipe still pass
through Host Agent validation, admission, authorization, and task policy.

## Installation manifest

The provider's `opute.provider.get_install_manifest` tool is read-only. It
returns the prerequisites and service contract that the Host Agent needs to
install and validate the provider.

The logical shape is:

```yaml
schema: opute-provider-install-manifest.v1
provider:
  id: com.opute.ollama
  version: 1.0.0
provides:
  - id: opute.capability.llm-serving.v1
    version: 1
requires: []
hostAgent:
  minimumVersion: 1.0.0
recipes:
  - id: com.opute.ollama.managed-linux
    source:
      uri: https://example.invalid/recipes/ollama.yaml
      revision: <full-immutable-revision>
      sha256: <sha256-of-source>
    mode: managed
    inputs:
      endpoint: http://127.0.0.1:11434
      model: hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M
validation:
  capability: opute.capability.llm-serving.v1
  operation: validate
```

The manifest MUST support:

- provider identity and version;
- provided and required service contracts;
- minimum Host Agent compatibility;
- explicit inputs, defaults, secret references, and redaction metadata;
- immutable recipe references and expected SHA-256;
- managed and external modes;
- a neutral validation operation;
- provider-specific observation mappings under a namespaced field.

The manifest MUST NOT contain raw credentials, mutable branches/tags,
unbounded shell text, or a request to invoke Host Agent MCP recursively.

## Recipe execution and activation

Recipes remain `runtime-recipe.v1`, `tunnel-recipe.v1`, or a future
capability-specific sibling. They embed `host-plan.v1`; they do not define a
new executor.

The existing recipe path is extended rather than replaced:

- `internal/recipe/recipe.go` owns runtime recipe decoding, input resolution,
  compatibility, nested-plan rejection, canonical hashing, and validation;
- `internal/recipe/source.go` owns shared source loading and canonicalization;
- `internal/recipe/tunnel.go` owns tunnel-specific envelope mapping;
- `internal/plan/runner.go` remains the only plan executor;
- `internal/hostmcp/recipe_run.go` and `internal/hostmcp/tunnel_run.go` remain
  the durable MCP operation handlers.

Recipe sources follow these rules for mutation:

- local files are size-limited and hashed before execution;
- remote sources require full immutable revisions and expected SHA-256;
- branches, mutable tags, `main`, and `latest` are rejected;
- redirects remain within the approved HTTPS policy;
- source provenance, observed hash, canonical document, expanded plan, and
  redacted inputs are persisted before execution;
- schema, host-catalog, variable, compatibility, and nested-plan validation
  complete before any action runs.

The activation flow is:

```text
provider install --source <trusted-plugin> --activate
  -> verify trusted plugin descriptor
  -> start/connect provider MCP server over Streamable HTTP
  -> server/discover
  -> tools/list
  -> get_install_manifest
  -> validate manifest and recipe pins
  -> resolve Cordis dependencies
  -> execute generic host recipe
  -> validate neutral capability contract
  -> register service and atomically activate it
```

The idiomatic user-facing flag is `--activate`; the MCP argument is
`activate: true`. The previous active provider remains in place until the
candidate passes validation. Failed setup never makes the candidate active.

## Lifecycle, dependencies, events, and cleanup

### Lifecycle

The Cordis-style provider state machine is:

```text
declared
  -> connecting
  -> discovered
  -> manifest_validated
  -> dependencies_resolved
  -> registered
  -> ready
  -> active
  -> stopping
  -> stopped
```

Failures become `failed` or `degraded` with evidence. The active pointer is
updated only after neutral capability validation.

### Provider generations and controlled reload

The Host Agent plans for reload as a first-class lifecycle operation, even
when the first release triggers it only through an explicit administrative
operation rather than file-watcher HMR. A provider instance is never mutated
in place. It is mounted as a `ProviderGeneration` and replaced by a candidate
generation:

```text
active(generation-1)
  -> candidate(generation-2)
  -> ready(generation-2)
  -> draining(generation-1)
  -> active(generation-2)
  -> disposed(generation-1)
```

The core abstractions are:

- `ProviderGeneration`: immutable provider identity, implementation version,
  manifest/artifact hash, endpoint, Cordis fiber, catalog revision, session
  bindings, and lifecycle state;
- `ProviderActivation`: the durable active pointer and its validation
  evidence;
- `ProviderLifecycleManager`: mount, validate, activate, drain, rollback, and
  dispose transitions;
- `ProviderHandle`: generation-aware routing for new sessions, existing
  sessions, and durable operations;
- `DrainPolicy`: bounded deadlines, cancellation behavior, and explicit
  idempotent/non-idempotent retry classification.

Reload behavior is normative:

1. Start the candidate without changing the active pointer.
2. Perform MCP handshake, manifest/schema validation, dependency resolution,
   catalog registration, and neutral readiness validation.
3. Atomically publish the candidate as active and a new catalog revision.
4. Route new sessions to the candidate while existing sessions and in-flight
   operations remain pinned to the previous generation.
5. Stop admitting new work to the old generation and drain it within the
   configured deadline.
6. Dispose the old generation's fiber and all owned resources in reverse order.
7. If candidate startup or post-activation validation fails, retain or restore
   the previous healthy generation and record the rollback evidence.

Existing Streamable HTTP sessions must not be silently moved between provider
generations. If a provider cannot drain a session, the Host Agent returns an
explicit retryable/session-ended result; it does not replay the call. Durable
operations preserve their generation identity and are reconciled to `unknown`
when completion is not confirmed.

The first implementation must exercise these transitions with a fake provider
that has delayed calls, active sessions, failed readiness, forced drain, and
rollback. A live file-watcher HMR mechanism is not required for the first
release; the lifecycle contract must be implemented before adding a reload
trigger.

### Dependency injection

The provider declares capability dependencies, not concrete provider names.
For example, a future plugin may require `opute.capability.tunneling.v1`; it
must not import the Cloudflare provider or call a provider-specific tool.

The Host Agent resolves dependencies in the Cordis context before registering
the provider. A remote MCP provider receives only the scoped contract it was
authorized to use; it does not receive unrestricted access to Host Agent MCP
tools.

### Effects and disposal

Every registration creates an owned effect with a disposer for:

- provider HTTP client and authentication state;
- supervised provider process, if Host Agent launched it;
- MCP task handles;
- capability catalog overlays;
- event subscriptions;
- active-provider changes;
- temporary credentials and files;
- recipe compensation and service teardown.

Disposal is idempotent and runs in reverse registration order. Provider
resource cleanup is implemented by typed provider operations or recipe
compensation, not by arbitrary Host Agent shell commands.

### Events

The Host Agent emits typed events such as:

```text
provider.discovered
provider.manifest.validated
provider.dependencies.resolved
provider.ready
provider.degraded
provider.activation.succeeded
provider.activation.failed
provider.stopping
provider.stopped
```

Provider-originated asynchronous events are optional and must use a
negotiated `com.opute/cordis-provider` extension. Durable operations and MCP
Tasks remain authoritative; an event cannot by itself mark an installation
successful.

Reload adds these lifecycle events:

```text
provider.generation.created
provider.reload.started
provider.draining
provider.reload.succeeded
provider.reload.failed
provider.rollback.succeeded
```

Each event carries provider identity, old/new generation IDs where applicable,
catalog revisions, operation ID, and a redacted reason. Events are evidence;
the durable lifecycle record remains authoritative.

## Public operations and names

Use these concise, namespaced Host Agent operations:

```text
opute.provider.install
opute.provider.validate
opute.provider.status
opute.provider.reload
opute.recipe.validate
opute.recipe.apply
opute.recipe.status
```

The provider-facing read-only tool is:

```text
opute.provider.get_install_manifest
```

`opute.provider.reload` is a generic, auditable lifecycle operation. It does
not accept provider-specific restart commands and must use the generation,
drain, activation, rollback, and disposal contract above. File-watcher HMR and
arbitrary live patching are not supported interfaces.

Existing names such as `validate_runtime_recipe`,
`run_runtime_recipe`, `get_runtime_recipe_run`, `validate_tunnel_recipe`, and
`run_tunnel_recipe` remain compatibility aliases during migration. They must
delegate to the same resolver, Cordis service context, plan runner, and
operation store; no new provider-specific lifecycle logic may be added to
them.

The CLI mirrors the MCP operations:

```bash
opute-host-agent provider install --source ./plugins/llm/ollama/plugin.yaml --activate
opute-host-agent provider status --provider com.opute.ollama
opute-host-agent provider reload --provider com.opute.ollama
opute-host-agent recipe validate --source ./plugins/llm/ollama/recipes/ollama.yaml
opute-host-agent recipe apply --source ./plugins/llm/ollama/recipes/ollama.yaml --input model=hf.co/LiquidAI/LFM2-2.6B-GGUF:Q4_K_M
```

The CLI and MCP handlers call the same internal packages.

## Implementation plan

### 1. Freeze the public contracts

Add versioned schemas and contract documentation for:

- `opute-provider-plugin.v1`;
- `opute-provider-install-manifest.v1`;
- `opute.capability.llm-serving.v1`;
- `opute.capability.tunneling.v1`;
- `com.opute/cordis-provider` metadata;
- neutral `RuntimeObservation` and `TunnelObservation` outputs.

Primary files:

- `schemas/` for JSON Schemas and exported catalog metadata;
- `docs/adr/0002-provider-extension-architecture.md` for this decision;
- provider contract fixtures under `test/fixtures/providers/`.

Establish the module boundaries at the same time:

- keep the Host Agent daemon in `cmd/opute-host-agent/` and Host Agent
  `internal/` packages;
- expose only neutral contracts and the generic `pkg/hostagentclient/` from
  the Host Agent module;
- build an external client against the
  Host Agent's public MCP contract; do not move `internal/tui` into a new Go
  module;
- make `plugins/llm/ollama/` and `plugins/tunneling/cloudflare/` independently
  buildable provider MCP modules;
- keep `bundles/first-party/` declarative and ensure it supplies descriptors as
  data rather than importing provider implementations into the daemon.

Proof:

- schema decoding rejects unknown contract versions and missing identities;
- generated/embedded schemas match runtime catalog output;
- provider and capability IDs pass the stable naming and version checks.
- the Host Agent core builds without the TUI and either provider module;
- the TUI module builds against only public Host Agent contracts/client APIs;
- each provider module builds without Host Agent `internal/` or TUI imports;
- architecture tests inspect imports and runtime endpoints, not only directory
  names.

### 2. Add the Cordis-style service kernel

Create an Opute-owned service package, likely under `internal/cordis/`, with:

- `ServiceDefinition` and typed operation descriptors;
- `ProviderPlugin` and `ServiceProvider` interfaces;
- dependency declarations and version resolution;
- scoped service context and injection;
- `Context.Plugin`/`Mount` semantics that return an owned fiber;
- `Context.Effect`/`Context.On` registration APIs with post-disposal rejection;
- `Context.Emit`, `Context.Waterfall`, `Context.Parallel`, and `Context.Serial`
  with the exact awaited/order/result semantics defined above;
- `Waterfall`'s `next()` delegation and short-circuit behavior;
- guarded `Register`, `Resolve`, and `Unregister` operations;
- effect/disposer ownership with reverse-order cleanup;
- typed lifecycle events;
- active-provider selection by capability;
- `ProviderGeneration`, `ProviderActivation`, `ProviderHandle`, and
  `ProviderLifecycleManager` abstractions;
- bounded `DrainPolicy` with explicit retry classification;
- generation/catalog revision tracking;
- candidate activation, atomic switch, rollback, and reverse-order disposal.

Do not use Go's `plugin` package or runtime directory scanning. In-repository
providers are trusted but remain independently built MCP processes. “Dynamic
registration” means registering and activating a generic provider descriptor
and MCP-backed provider handle supplied by the first-party bundle; it does not
mean importing or loading provider code into the Host Agent core.

Build on, rather than duplicate, the existing dynamic catalog behavior in:

- `internal/catalog/registry.go`: extend `Registration`, `Register`,
  `Unregister`, `Snapshot`, and `validate` to bind a service implementation
  and lifecycle disposer;
- `internal/hostmcp/server.go`: integrate `RegisterCapability`,
  `CatalogSnapshot`, `DispatchTool`, `addRegisteredCapability`, and
  `Close` with the Cordis context.

Proof:

- registry tests cover dependency conflicts, duplicate service IDs, invalid
  schemas, unauthorized implementations, revision changes, and idempotent
  disposal;
- Cordis conformance tests cover `Apply`, `Inject`, fiber ownership,
  `Effect`, `On`, all four event dispatch modes, and waterfall short-circuit;
- a fake provider can register, become ready, serve a typed operation, and be
  unregistered without leaving a catalog or event subscription behind.
- generation tests cover active/candidate isolation, session affinity,
  bounded drain, failed readiness, atomic activation, rollback, and complete
  fiber disposal without replaying a mutation.
- the core registry test uses a fake generic descriptor and never imports or
  names Ollama, Cloudflare, or TUI packages.

### 3. Implement the MCP-backed service adapter

Create a generic adapter, for example under `internal/cordis/mcp/`, that:

- connects to a provider over Streamable HTTP only;
- sends MCP 2026-07-28 request metadata and HTTP protocol headers;
- calls `server/discover` before trusting the provider surface;
- validates the `com.opute/cordis-provider` extension;
- calls `tools/list` with pagination/cache handling;
- validates tool input/output schemas;
- maps typed service calls to `tools/call`;
- handles structured content and text fallback;
- maps provider Tasks to child operation evidence;
- propagates cancellation and bounded timeouts;
- closes HTTP connections and disposes all owned effects.

Integrate with `internal/transport/http.go` and the existing MCP server/client
construction in `internal/hostmcp/server.go`. Do not add stdio support.

Proof:

- a fake Streamable HTTP provider passes discovery, tools, manifest, schema,
  cancellation, and task tests;
- a fake stdio endpoint is rejected before any provider operation;
- spoofed `serverInfo`, descriptions, and annotations do not bypass the
  trusted plugin descriptor or manifest validation;
- no MCP session ID is required by the modern path.

### 4. Wire installation manifests into recipe execution

Extend the provider adapter and recipe handlers so the Host Agent:

1. invokes `opute.provider.get_install_manifest` as a read-only call;
2. validates its `outputSchema` and provider identity;
3. verifies capability dependencies and Host Agent compatibility;
4. resolves local or pinned remote recipes;
5. persists provenance and redacted inputs;
6. validates the expanded plan against the current catalog;
7. runs it through `plan.Runner.Run`;
8. executes neutral capability validation;
9. commits activation only after validation succeeds.

Primary files/functions:

- `internal/recipe/recipe.go`: `Load`, `ResolveInputs`, `Validate`,
  `rejectNestedPlanRuns`;
- `internal/recipe/source.go`: `LoadRaw`, `CanonicalHash`, source policy;
- `internal/hostmcp/recipe_run.go`: `loadRuntimeRecipe`,
  `handleValidateRuntimeRecipe`, `handleRunRuntimeRecipe`,
  `validateRecipeActivation`, `ensureRuntimeRecipeActivation`;
- `internal/hostmcp/tunnel_run.go`: tunnel recipe parity;
- `internal/plan/runner.go`: `Run`, `validateNodeReady`, `compensateApplied`.

Proof:

- manifest validation performs no host mutation;
- mutable revisions, missing hashes, hash mismatches, oversized content,
  unresolved inputs, unknown tools, and nested plan runs are rejected;
- re-running an already satisfied recipe does not duplicate actions;
- cancellation, retry, compensation, and resume preserve the same operation
  identity and evidence.

### 5. Persist provider lifecycle and recovery state

Extend `internal/state/store.go` and its migrations to persist:

- trusted plugin descriptor and artifact hash;
- provider endpoint and transport policy;
- discovery and tool-list metadata;
- raw/redacted installation manifest;
- recipe source, revision, observed hash, canonical hash, and expanded plan;
- redacted input values;
- Host Agent operation ID and child MCP task ID;
- service lifecycle state and active-provider pointer;
- runtime/tunnel observation and validation evidence.

Use the existing plan and active-capability methods as the foundation:

- `CreatePlan`, `FindPlan`, `GetPlan`, `UpdatePlan`;
- `CompletePlanWithActiveRuntime`;
- `CompletePlanWithActiveCapability`;
- `GetActiveRuntime` and `GetActiveCapability`.

On restart, working provider operations become `unknown`. Resume uses the
persisted expanded plan, revalidates every node and dependency, and rechecks
the provider before taking action. A provider process still running is not
proof of success.

Persist generation identity and lifecycle transitions separately from the
active pointer so a reload can be recovered deterministically. During a drain,
new work is rejected or routed to the candidate; existing work remains pinned
to the old generation. A timeout records the incomplete drain and does not
silently retry a non-idempotent operation.

Proof:

- restart tests show `working -> unknown`;
- resume succeeds without refetching the recipe;
- failed activation leaves the previous active provider unchanged;
- reload recovery preserves old/candidate generation identity and catalog
  revisions;
- disposal removes catalog entries, tasks, events, and provider connections;
- no reconnect or reload path resubmits an original mutation automatically.

### 6. Make observations and validation provider-neutral

Retain `internal/ops/runtime_probe.go` as the common serving validation seam.
Refine `RuntimeObservation` so it contains only the neutral serving contract:

- endpoint readiness;
- model discovery;
- requested model visibility;
- streaming chat readiness;
- normalized errors and remediation hints.

Move Ollama-native fields such as native API details, loaded-model diagnostics,
and Ollama concurrency settings into the Ollama provider observation namespace.
Do not add Llama fields or a Llama runtime enum.

Migrate or retire provider-specific logic in:

- `internal/ops/ollama.go`;
- `internal/ops/local_llm_types.go`;
- `internal/ops/llama_server.go`;
- `internal/ops/llama_server_build.go`;
- `internal/ops/llama_server_prerequisites.go`;
- `internal/tools/dispatch.go` and `internal/tools/catalog.go`.

Compatibility tools may remain temporarily, but they delegate to the Ollama
provider service and cannot add new runtime lifecycle behavior. Llama tools
are not part of the supported catalog.

Proof:

- generic probe tests pass against a fake OpenAI-compatible server;
- Ollama-specific fields are absent from the common output schema;
- legacy tools route through the provider adapter and preserve their existing
  error/task semantics;
- no supported catalog or recipe requires Llama.

### 7. Build the first-party Ollama provider

Add `plugins/llm/ollama/` as an independently buildable MCP module/process
containing:

- the trusted plugin descriptor;
- a Streamable HTTP MCP server or provider adapter;
- `opute.provider.get_install_manifest`;
- the `llm-serving.v1` operation mappings;
- managed and external setup modes;
- pinned Ollama recipes and provider-specific observation mapping;
- provider tests using a fake Ollama-compatible API.

The provider module may import only neutral Opute contracts, its MCP/provider
SDK, and Ollama APIs/CLI bindings. It must not import Host Agent `internal/`
packages, `pkg/hostagentclient`, or the external TUI client. The Host Agent starts or
connects to the resulting MCP process from generic descriptor data.

Managed mode uses generic Host Agent primitives for verified artifact
installation, managed configuration, service reconciliation, model download,
HTTP readiness, model discovery, and streaming chat validation. The provider
contains the Ollama knowledge; the Host Agent contains the generic execution
and policy.

External mode skips installation and validates a user-supplied compatible
endpoint and model. It must not require an Ollama binary on the host.

Functional proof in disposable Linux/WSL:

- validate the local manifest without changing host state;
- apply the managed recipe and confirm service, endpoint, model, and streaming
  chat readiness;
- reapply idempotently;
- validate external-server mode;
- activate only after the literal streaming probe succeeds;
- send a real prompt through the application `/chat` canary separately when
  Opute application integration is in scope.

### 8. Build the first-party Cloudflare provider

Add `plugins/tunneling/cloudflare/` as an independently buildable MCP
module/process containing:

- the trusted plugin descriptor;
- a Streamable HTTP MCP server or provider adapter;
- the provider installation manifest;
- the `tunneling.v1` operation mappings;
- the generic tunnel recipe and provider-specific observation mapping;
- typed bindings for explicitly authorized local services.

The provider module may import only neutral Opute contracts, its MCP/provider
SDK, and Cloudflare APIs/CLI bindings. It must not import Host Agent
`internal/` packages, `pkg/hostagentclient`, the external TUI client, or the Ollama
provider.

Use `2026-08-22-generic-tunneling-recipes-plan.md` as the feature-specific
source of truth. Cloudflare-specific account, DNS, tunnel, credential, and
connector details remain in this provider. Host Agent still owns recipe
validation, authorization, public-exposure approval, readiness, recovery, and
cleanup.

Proof:

- fake-provider tests cover multiple bindings, duplicate hostname rejection,
  credential redaction, idempotent reconciliation, and teardown;
- disposable Cloudflare validation confirms connector readiness and each
  declared public-to-local route;
- Host Agent remains usable without the Cloudflare provider.

### 9. Update catalogs, authorization, and compatibility surfaces

Update in parity:

- `internal/tools/catalog.go` and standalone definitions;
- `internal/tools/dispatch.go` and registered capability dispatch;
- `internal/resource/admission.go`;
- `internal/tasks/registry.go`;
- `schemas/*.json` and `schemas/catalog-meta.json`;
- `test/contract/catalog_test.go`;
- `test/contract/dispatch_coverage_test.go`;
- `test/contract/standalone_catalog_test.go`;
- standalone HTTP catalog and schema exports;
- sibling Opute schema exports where required.

Provider registration must inherit the same approval, authorization, task,
resource, redaction, cancellation, and audit behavior as ordinary host
mutations. Dynamic catalog revisions must invalidate stale client snapshots;
the canonical public capability tools should remain stable where possible.

Proof:

- every new operation has catalog, dispatch, task, admission, schema, and
  standalone coverage;
- mutation classifications match resource and task policy;
- catalog revision changes are observable after registration/unregistration;
- the public standalone HTTP catalog exposes the intended operations only.

### 10. Add CLI and documentation parity

Add the client CLI commands in `clients/cli/` (migrating the current
`internal/cli/` command-mode code as needed) and wire them to the same public
Host Agent client/contract surface used by MCP. The CLI must not import
provider modules or TUI code. Document:

```bash
opute-host-agent provider install --source <plugin> [--activate]
opute-host-agent provider validate --provider <id>
opute-host-agent provider status --provider <id>
opute-host-agent provider reload --provider <id>
opute-host-agent recipe validate --source <path>
opute-host-agent recipe apply --source <path> --input key=value
opute-host-agent recipe status --run-id <id>
```

No CLI command may contain a separate Ollama or Cloudflare setup path.

Proof:

- CLI and MCP calls produce the same operation/Task IDs and final state;
- validation flags perform no mutation;
- `--activate` is atomic and leaves the previous provider active on failure.

### 11. Implement the typed, LLM-independent TUI client

Build an external typed client against the Host Agent's public MCP contract.
It is a presentation-only MCP client: it consumes the live catalog, binds only
server-issued canonical identities, validates drafts for UX, and sends typed
calls over Streamable HTTP. The Host Agent remains the validation,
authorization, execution, durability, recovery, and evidence authority.

The client adopts the Cordis shape of immutable context snapshots, declared
derivations, typed service ownership, and disposer-backed effects. It does not
load plugins, initialize Host Agent internals, infer execution arguments from
rendered prose, or move provider/authorization/orchestration logic into the
terminal. The historical Go TUI is a migration source only and is removed
after the external client passes the plan's parity gate.

Proof is owned by the authoritative TUI plan: typed binding, catalog revision,
schema, approval, task/reconnect, redaction, bounded rendering, MCP
2026-07-28 conformance, and packaged end-to-end evidence must pass before the
legacy client is retired.

### 12. Build the disposable provider-reset and chat E2E harness

Add a comprehensive Linux/WSL E2E harness that proves the new provider path
from a clean host state through application chat. The harness must be scoped
to a disposable test environment. It must inventory the current Ollama,
Cloudflare connector, services, processes, endpoints, configuration paths, and
active-provider records before cleanup. If the environment is shared or the
ownership of a resource is ambiguous, it must fail closed rather than stop or
delete it.

The reset flow is:

1. Acquire the existing coordination lease and record a baseline inventory.
2. Remove only the test-scoped Ollama and `cloudflared` installations,
   services, configuration, credentials, model state, connector state, and
   active-provider records through typed provider teardown/recipe operations.
   No broad process kill, unresolved path deletion, or ad hoc provider shell
   script is permitted.
3. Confirm the old binaries, services, endpoints, tunnel routes, and active
   records are absent, while preserving the baseline evidence.
4. Start Host Agent and the trusted Ollama and Cloudflare MCP providers over
   Streamable HTTP.
5. Invoke the generic provider installation flow with activation enabled for
   each capability. The Host Agent must obtain each manifest, execute the
   generic recipes, validate readiness, and atomically activate the provider.
6. Confirm the Ollama model, OpenAI-compatible endpoint, streaming probe,
   Cloudflare connector, and declared public-to-local route using typed neutral
   observations.
7. Send a fixed literal canary prompt through `localhost/chat` and parse the
   complete SSE stream. Require non-empty assistant content, the expected
   canary marker, selected provider/runtime evidence, and correlated trace
   evidence.
8. Send the same fixed prompt through `https://platform.opute.io/chat` and
   parse its SSE stream. Require the public route, selected provider/runtime,
   trace correlation, non-empty assistant content, expected marker, and no
   stream or tool errors.
9. Reconcile local and public evidence by request ID, operation ID, provider
   generation, catalog revision, model, and runtime trace. Do not treat HTTP
   200, health, setup completion, or assistant text without trace evidence as
   chat success.
10. Tear down the test-scoped resources and verify cleanup, or preserve the
    disposable environment and its evidence explicitly for diagnosis.

The harness must keep credentials, cookies, tokens, hidden prompts, and raw
provider secrets out of logs and artifacts. It must record source/revision and
recipe hashes, expanded-plan hash, generation IDs, activation transitions,
parsed SSE events, selected runtime evidence, tool/runtime trace evidence,
and cleanup results.

Add a deterministic entry point such as `make provider-reset-chat-e2e` and
make it refuse to run without an explicit disposable-environment marker and
coordination lease. The required sentinel is
`PROVIDER_RESET_CHAT_E2E_PASS`; the local and public canaries additionally
emit `LOCAL_CHAT_CANARY_PASS` and `PUBLIC_CHAT_CANARY_PASS`.

## Validation plan

### Cordis kernel tests

Test:

- service registration and duplicate/conflicting IDs;
- dependency resolution and version incompatibility;
- required versus optional dependencies;
- scoped context and injection;
- effect ordering and idempotent disposal;
- lifecycle transitions and invalid transitions;
- typed event ordering and failure propagation;
- active-provider replacement and rollback;
- catalog generation/revision changes;
- provider failure isolation;
- generation-affine sessions and durable operations;
- bounded drain, forced disposal, failed candidate readiness, atomic switch,
  rollback, and complete fiber cleanup.

### MCP contract tests

Use a fake Streamable HTTP provider to verify:

- modern `server/discover` negotiation;
- request metadata and `MCP-Protocol-Version` handling;
- provider extension and manifest validation;
- deterministic `tools/list`, pagination, cache, and list-change behavior;
- input/output schema validation;
- structured-content and text fallback;
- cancellation and MCP Tasks mapping;
- HTTP authentication and redaction;
- existing-session affinity during provider generation replacement;
- rejection of stdio, unsupported schemes, mutable endpoints, and spoofed
  `serverInfo` identity.

### Recipe and operation tests

Verify:

- manifest validation performs no mutation;
- recipe source pinning, hashes, size limits, path safety, and variables;
- no recursive plan runs;
- existing host-plan runner is the only executor;
- readiness checks avoid unnecessary actions;
- reapplication is idempotent;
- retries, recovery, compensation, cancellation, and restart/resume work;
- activation waits for neutral capability validation;
- previous active provider survives failed candidate activation;
- reload preserves generation identity, drains without mutation replay, and
  rolls back when candidate or post-switch readiness fails;
- secrets are absent from logs, operation records, task resources, and MCP
  results.

### Catalog and policy tests

Verify:

- provider operations appear in Go and standalone catalogs;
- every mutation has admission and task classification;
- schemas match runtime catalog output;
- dynamic catalog revisions invalidate stale snapshots;
- legacy local-LLM operations remain compatible aliases;
- no Llama provider or runtime enum is required;
- no provider can create nested plan runs or bypass `DispatchTool`;
- TUI projections use the current catalog and preserve typed binding
  provenance.

### End-to-end acceptance

Run the unit and standalone gates in disposable Linux/WSL:

```bash
go test ./...
go test ./...
cd ../opute && bun test packages/shared packages/web
go -C plugins/llm/ollama test ./...
go -C plugins/tunneling/cloudflare test ./...
make standalone-http-smoke
```

The repository must provide an equivalent `make test-all-modules` target so
CI cannot accidentally omit the nested TUI or provider modules.

Then run the provider reset/chat harness described in implementation step 12:

1. Inventory and remove the test-scoped existing Ollama and `cloudflared`
   installations through typed teardown, failing closed if the environment is
   not disposable.
2. Reinstall and activate both providers through their manifests and generic
   recipes.
3. Verify Ollama serving and Cloudflare tunnel readiness.
4. Exercise local and public `/chat` canaries with parsed SSE and trace
   evidence.
5. Reapply idempotently, restart during an operation, verify `unknown` then
   safe resume, cancel a run, and verify partial state and cleanup.
6. Run external-server mode without requiring an Ollama binary.

If Opute chat is tested, use a separate application-level canary with a
literal prompt, parsed SSE output, selected runtime evidence, and tool/runtime
trace evidence. Host Agent recipe success alone must never be reported as
chat success.

## Acceptance criteria and evidence gates

Every gate below must be executable and must produce the named evidence. A
green unit test without the required boundary or runtime evidence is not a
pass.

### Gate A — Architecture boundary

Pass only when:

- `cmd/opute-host-agent` and Host Agent `internal/` packages have no import of
  `internal/tui`, `plugins/llm/ollama`,
  `plugins/tunneling/cloudflare`, or any concrete provider package;
- the core `internal/cordis` package (excluding its explicit `mcp` adapter
  subpackage) has no import of provider implementations, TUI, MCP transport,
  recipe, or host mutation packages;
- External clients have no import of Host Agent `internal/` packages, provider
  modules, provider endpoints, or provider-specific tool names;
- provider modules have no import of client code, `internal/tui`,
  `internal/hostmcp`, `internal/state`, `internal/plan`,
  `internal/resource/admission`, `pkg/hostagentclient`, or another provider;
- each provider and the external TUI build as separate modules/processes against only
  neutral contracts and their public SDK/client boundary;
- no supported build path imports Go's `plugin` package;
- no provider-specific lifecycle symbol is reachable from the generic Host
  Agent catalog or service kernel;
- the supported catalog contains no Llama/llama.cpp operation or enum;
- the only provider transport implementation is Streamable HTTP.
- the core daemon starts and serves its public MCP surface when the TUI and
  provider executables are absent;
- the TUI can complete a catalog/status interaction against a fake Host Agent
  without the Ollama or Cloudflare MCPs being installed;
- a fake provider can complete its Host Agent contract without the TUI module
  being present or referenced.

Required evidence:

```text
ARCHITECTURE_BOUNDARY_PASS
```

Implement as deterministic architecture/contract tests under
`test/contract/architecture_test.go`; do not make this a reviewer-only grep.

### Gate B — Cordis service lifecycle

Pass only when a fake provider proves:

- the plugin has a stable ID, declared `Inject` dependencies, an `Apply`
  mount function, and a returned fiber/effect owner;
- `requires` dependencies are resolved before `registered`;
- failed dependency resolution leaves no registration or effect;
- `ready` cannot precede manifest and capability validation;
- `Context.Plugin` returns an owner whose disposal removes every service,
  effect, listener, and task created by that plugin;
- `Context.Effect` and `Context.On` registrations are reversible and cannot
  be created after the owning fiber is disposed;
- `Emit` is non-awaited and has no return value;
- `Waterfall` passes `next`, propagates the delegated result, and supports
  deliberate short-circuiting;
- `Parallel` awaits all listeners without a result and `Serial` awaits
  listeners in registration order with a result;
- direct capability calls use service methods, while policy/interception uses
  typed events;
- activation is a separate final transition;
- events are ordered and carry provider/service/operation identity;
- disposal is reverse ordered, idempotent, and removes catalog entries,
  subscriptions, tasks, and connection state;
- each capability has a service definition, provider, and consumer;
- a failed provider does not corrupt another provider for the same capability.

Required evidence:

```text
CORDIS_LIFECYCLE_PASS
```

### Gate C — MCP provider contract

Pass only when a fake Streamable HTTP provider proves:

- `server/discover` is called first;
- the request declares MCP `2026-07-28` metadata and the HTTP protocol header;
- `com.opute/cordis-provider` is negotiated and schema-validated;
- `opute.provider.get_install_manifest` is present, read-only, and conforms
  to its output schema;
- all capability tools have deterministic input/output schemas;
- structured content and text fallback are both handled;
- cancellation closes the operation and records the correct durable state;
- provider Tasks are recorded as children, never as the Host Agent source of
  truth;
- a fake stdio endpoint and legacy HTTP+SSE endpoint are rejected.

Required evidence:

```text
MCP_PROVIDER_CONTRACT_PASS
```

### Gate D — Recipe safety and idempotence

Pass only when tests prove:

- manifest validation performs zero host mutations;
- unpinned remote sources, missing hashes, mismatched hashes, unsupported
  schemes, unsafe redirects, oversized documents, traversal, unresolved
  variables, and unsupported host capabilities are rejected;
- no recipe can create a nested plan or recipe run;
- every mutation is dispatched through Host Agent admission;
- an already satisfied recipe performs no duplicate installation/action;
- retry, recovery, cancellation, reverse compensation, and resume preserve
  the same operation identity and redaction guarantees.

Required evidence:

```text
RECIPE_SAFETY_PASS
```

### Gate E — Activation correctness

Pass only when a candidate provider run proves:

- the previous active provider remains active during candidate setup;
- the active pointer changes exactly once after validation succeeds;
- validation failure, cancellation, timeout, or restart leaves the previous
  active provider unchanged;
- the committed record contains provider identity, manifest/recipe hashes,
  catalog revision, contract ID, validation observation, and operation ID;
- a restart converts in-progress work to `unknown` and resume revalidates
  before acting.

Required evidence:

```text
ACTIVATION_CORRECTNESS_PASS
```

### Gate F — Ollama managed serving

In disposable Linux/WSL, pass only when the same `provider install --activate`
flow produces all of the following:

```json
{
  "providerId": "com.opute.ollama",
  "contract": "opute.capability.llm-serving.v1",
  "servingContract": "openai-chat.v1",
  "endpointReady": true,
  "openAiModelsReady": true,
  "requestedModelVisible": true,
  "streamingChatReady": true,
  "active": true,
  "operationStatus": "completed"
}
```

The evidence must include the parsed streaming probe, not merely process
status, health status, or HTTP 200. Required sentinel:

```text
OLLAMA_MANAGED_SERVING_PASS
```

### Gate G — Ollama external mode

Pass only when a preconfigured OpenAI-compatible server can be activated
without an Ollama binary, while preserving the same neutral observation and
activation invariants. Required sentinel:

```text
OLLAMA_EXTERNAL_SERVING_PASS
```

### Gate H — Cloudflare tunneling

When a disposable Cloudflare account is available, pass only when every
declared binding has independently observable readiness and the operation
records the public-to-local mapping, credential reference, provider identity,
and cleanup result. Required sentinel:

```text
CLOUDFLARE_TUNNELING_PASS
```

Failure to run a live Cloudflare test is reported as unverified, not passed;
it does not block the core local Host Agent gate.

### Gate I — Catalog and policy parity

Pass only when Go, standalone, runtime, schema, task, resource, dispatch, and
authorization surfaces agree. Every new mutation must have:

- one canonical descriptor;
- one dispatch path through `DispatchTool`;
- one admission/resource classification;
- one task projection;
- one redaction policy;
- one schema export;
- one contract test.

Required evidence:

```text
CATALOG_POLICY_PARITY_PASS
```

### Gate J — Application chat, if enabled

This is a separate gate from Host Agent setup. It requires:

- a literal prompt sent through the application `/chat` path;
- HTTP and SSE parsing;
- non-empty assistant content;
- selected provider and capability evidence;
- runtime/tool trace evidence tied to the same operation/catalog revision.

Required evidence:

```text
CHAT_CANARY_PASS
```

No Host Agent-only sentinel may be substituted for this gate.

### Gate K — Core operation without LLM

In a disposable environment with no Ollama binary, no active LLM provider, no
LLM endpoint, and no LLM credentials, pass only when:

- the Host Agent starts normally;
- the Host Agent core starts and serves its public MCP surface without the TUI
  client process or module being installed;
- the Streamable HTTP MCP endpoint serves discovery, catalog, status, and core
  host/infrastructure operations;
- the TUI and headless mode start and support navigation, status, approvals,
  task logs, cancellation, and recovery;
- recipe validation, task inspection, and non-LLM host operations remain
  available;
- LLM-dependent operations return a typed capability-unavailable result with
  remediation instead of crashing, blocking, or hiding core operations;
- provider failure or provider shutdown does not terminate the Host Agent or
  corrupt the TUI state;
- no code path initializes or injects `ctx.llm` for core-only views.

Required evidence:

```text
CORE_WITHOUT_LLM_PASS
```

This gate must include a headless test and a TUI-shaped test. A successful
binary build alone is not evidence of core usability.

### Gate L — TUI typed authorized execution

Pass only when the TUI redesign contract is exercised end to end:

- deterministic editing, completion, typed lookup, validation, approval,
  execution, polling, cancellation, and recovery work with no LLM provider;
- `list_vms -> select entity -> get_vm_info` sends canonical arguments with
  binding provenance, the current catalog revision, and exactly one tool call
  per submission;
- stale catalog/observation revisions, unavailable entities, and incompatible
  output bindings are visibly blocked;
- agentic mode accepts only validated `assistant-session.v1` or `host-plan.v1`
  proposals and rejects prose execution and stale proposals;
- the TUI never inserts `confirm=true` and never exposes secrets or
  unauthorized output through the inspector or clipboard;
- reconnecting to a lost operation polls durable state without replaying the
  mutation;
- narrow, long-command, horizontal-scroll, keyboard-only, and non-color
  states remain usable.

Required evidence:

```text
TUI_TYPED_EXECUTION_PASS
```

### Gate M — Clean provider reset and application chat E2E

This is the required full-chain acceptance for the new provider architecture.
It must run only in a disposable Linux/WSL environment with the coordination
lease and an explicit test-environment marker. It passes only when the harness:

1. inventories the existing Ollama and `cloudflared` binaries, services,
   processes, configuration, endpoints, tunnel routes, credentials, and
   active-provider records;
2. removes only those test-scoped installations and state through typed
   provider teardown/recipe operations, then proves the old setup is absent;
3. starts Host Agent and the first-party Ollama and Cloudflare MCP providers
   over Streamable HTTP;
4. installs and activates both providers through their read-only manifests,
   generic Host Agent recipes, neutral readiness validation, and atomic
   activation flow;
5. proves Ollama endpoint/model/streaming readiness and Cloudflare connector
   plus public-route readiness;
6. sends a fixed literal canary prompt to `localhost/chat`, parses the full
   SSE stream, and proves non-empty assistant content, the expected canary
   marker, selected runtime evidence, and correlated tool/runtime traces;
7. sends the same prompt to `https://platform.opute.io/chat`, parses the full
   SSE stream, and proves public-route evidence, selected runtime evidence,
   non-empty assistant content, the expected marker, and no stream/tool
   errors;
8. correlates both canaries to provider generation, operation, catalog
   revision, model, and trace identity without exposing credentials;
9. re-applies the setup idempotently and verifies cleanup of the disposable
   environment at the end.

HTTP 200, process status, health status, recipe completion, or assistant text
without parsed SSE and runtime/tool trace evidence is insufficient. If the
environment is not disposable or a resource owner cannot be established, the
harness must fail closed rather than clean up or report a pass.

Required evidence:

```text
LOCAL_CHAT_CANARY_PASS
PUBLIC_CHAT_CANARY_PASS
PROVIDER_RESET_CHAT_E2E_PASS
```

## Drift controls

The following changes require updating this ADR and the relevant contract
tests before implementation:

- adding a provider-specific field to a common capability schema;
- importing TUI code or a concrete provider package into the Host Agent core;
- allowing the TUI to import Host Agent `internal` packages or provider MCPs;
- allowing a provider MCP to import TUI code, Host Agent `internal` packages,
  `pkg/hostagentclient`, or another provider;
- collapsing the TUI or a provider MCP into the Host Agent core process/module
  without an explicit boundary decision;
- adding Llama/llama.cpp support in the current release scope;
- adding stdio, legacy HTTP+SSE, or another MCP transport;
- allowing a provider to execute host installation directly;
- adding a second plan/workflow executor;
- allowing a provider to call Host Agent MCP recursively;
- bypassing `DispatchTool`, admission, task registration, or redaction;
- changing the activation commit point or restart-to-unknown rule;
- changing provider-generation affinity, drain deadlines, rollback semantics,
  or fiber disposal ownership;
- adding file-watcher HMR, arbitrary live event-graph patching, or silent
  cross-generation session migration;
- allowing `serverInfo` or tool descriptions to establish trust;
- loading runtime Go code or scanning plugin directories dynamically;
- making Cloudflare or another tunnel provider a prerequisite for local MCP;
- making an LLM provider, `ctx.llm`, or application chat a prerequisite for
  Host Agent startup, TUI navigation, core operations, task management, or
  recovery;
- making the TUI execute prose, bypass current catalog/schema validation, lose
  binding provenance, or replay mutations after reconnect;
- treating recipe completion, health, or HTTP 200 as application chat success.

Code review must require an invariant ID, affected domain boundary, and a
new/updated executable gate for any exception. The invariant list is part of
the implementation contract, not explanatory commentary.

## Options considered

### Direct provider code inside the Host Agent

Rejected as the primary implementation boundary. It would reduce transport
overhead but would put Ollama and Cloudflare lifecycle knowledge into the Go
core and make provider replacement harder.

### Runtime-loaded Go plugins

Rejected. Go's shared-library plugin mechanism is platform/toolchain-coupled,
has no safe general unload model, and would create a second trust and failure
boundary. Trusted in-repository providers are explicitly registered at build
or startup; external code uses Streamable HTTP.

### Provider MCP servers without a Cordis/Opute contract

Rejected. Each provider would invent different tools, dependencies, lifecycle
semantics, and cleanup behavior. MCP is retained as the transport, but
providers must implement the Host Agent-owned service contract.

### Provider-owned installation scripts

Rejected. Installation, authorization, recovery, and evidence would diverge
between providers. Providers return manifests; the Host Agent executes
validated recipes.

### Embedding the upstream TypeScript Cordis runtime in the Go binary

Deferred. Opute adopts Cordis's service architecture and exposes a stable
Cordis-compatible provider contract. The Host Agent implements the minimal
typed kernel in Go; a provider may use the upstream Cordis library internally.
Embedding a second language runtime is not required to achieve the desired
provider/service boundary and can be reconsidered if the Host Agent itself
becomes a TypeScript runtime.

## Consequences

### Benefits

- Provider authors implement one stable Opute service contract.
- MCP transport details remain hidden behind the Cordis service adapter.
- Host Agent recipes, admission, task policy, recovery, and evidence stay
  centralized.
- Host Agent core can run, be tested, and be deployed without the TUI or a
  concrete provider MCP.
- The TUI and provider MCPs can evolve and release independently against
  versioned public contracts.
- Ollama and Cloudflare can evolve independently inside trusted provider
  packages.
- New providers can be added without adding provider-specific lifecycle code
  to the Host Agent core.
- Dynamic registration and controlled provider replacement are available
  without unsafe runtime code loading.

### Costs

- The Host Agent must implement and maintain a Cordis-style service kernel.
- Every provider needs a manifest, contract mapping, lifecycle behavior, and
  conformance tests.
- Streamable HTTP adds a local process/transport boundary.
- Separate TUI and provider modules require explicit multi-module build and
  release orchestration.
- Catalog revisions and provider lifecycle state require careful persistence.
- Generation-affine sessions, bounded draining, rollback, and reload evidence
  add operational complexity.
- Provider authors need a shared SDK or adapter template to keep MCP plumbing
  small.

## Completion gate

Implementation is complete only when all of the following are true:

- the Cordis-style provider contract and MCP profile are versioned;
- the Host Agent core, TUI client, and each provider MCP build as separate
  modules/processes with the declared import graph;
- provider-generation, drain, rollback, and disposal abstractions pass their
  fake-provider lifecycle tests;
- Ollama and Cloudflare providers implement the manifest and capability
  contracts over Streamable HTTP;
- recipes install prerequisites through the existing Host Agent plan runner;
- activation is gated by neutral validation and is recoverable;
- dynamic registration, catalog, task, admission, and schema parity tests pass;
- the full Host Agent and TUI core gate passes with no LLM installed or
  reachable;
- the typed TUI execution gate passes with deterministic and agentic review
  paths;
- Ollama managed and external modes work end to end in disposable WSL;
- the disposable provider-reset harness removes and reinstalls test-scoped
  Ollama and `cloudflared` through the new providers;
- both `localhost/chat` and `https://platform.opute.io/chat` pass literal
  prompt, parsed SSE, assistant-content, selected-runtime, and trace-evidence
  canaries;
- no stdio transport or Llama support is required;
- `go test ./...` and `make standalone-http-smoke` pass;
- `make test-all-modules` passes for the Host Agent, TUI, and provider modules;
- any live Cloudflare or shared-runtime validation follows the existing lease,
  cleanup, and coordination rules.

## References

### MCP

- [MCP 2026-07-28 specification](https://modelcontextprotocol.io/specification/2026-07-28)
- [MCP versioning](https://modelcontextprotocol.io/specification/2026-07-28/basic/versioning)
- [MCP server discovery](https://modelcontextprotocol.io/specification/2026-07-28/server/discover)
- [MCP tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools)
- [MCP transports](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports)
- [MCP authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)
- [MCP extensions](https://modelcontextprotocol.io/extensions/overview)
- [MCP Tasks](https://modelcontextprotocol.io/extensions/tasks/overview)

### Cordis and project plans

- [Cordis primer](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/cordis-primer.md)
- [Cordis architecture](https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/architecture.md)
