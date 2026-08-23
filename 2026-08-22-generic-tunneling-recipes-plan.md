# Remote TUI over Generic Tunneling Recipes

**Status:** Revised implementation plan aligned with ADR 0002

**Date:** 2026-08-22

**Related decision:** [`docs/adr/0002-provider-extension-architecture.md`](/home/houman/github/wunderous/opute-host-agent/docs/adr/0002-provider-extension-architecture.md)

## Decision in one paragraph

The terminal TUI is an independently buildable client that communicates with
the Host Agent exclusively through authenticated Streamable HTTP MCP. It must
work when the TUI runs on Machine B and the Host Agent runs on Machine A
behind a public HTTPS endpoint. A tunneling provider is an independently
buildable MCP provider whose pinned recipe installs and reconciles the tunnel
using generic Host Agent primitives. The Host Agent owns the neutral tunneling
contract, recipe validation, host-plan execution, authorization, admission,
durable operations, Cordis lifecycle, activation, recovery, and evidence. It
does not know Cloudflare, `cloudflared`, vendor accounts, DNS APIs, or vendor
CLI details.

The canonical remote path is:

```text
Machine B
  opute-host-agent-tui
      │
      │ authenticated Streamable HTTP MCP over public HTTPS
      ▼
Machine A public tunnel/HTTPS ingress
      │
      │ private forwarding to the approved Host Agent MCP binding
      ▼
Machine A
  Host Agent MCP server
      │
      ├─ Cordis-style provider lifecycle and active generation
      ├─ generic recipe + host-plan runner
      ├─ catalog, admission, tasks, state, and evidence
      └─ MCP provider adapter ──> tunneling provider MCP
                                      │
                                      └─ vendor API/CLI
```

The TUI never connects directly to a provider MCP or to a vendor endpoint.
The public tunnel exposes the Host Agent MCP surface, not an arbitrary local
port. Other local services may be exposed only through separate, explicitly
authorized tunnel bindings.

## Scope and non-goals

### In scope

- A first-class remote TUI connection from Machine B to Machine A.
- Streamable HTTP MCP for local and remote Host Agent access.
- A provider-neutral `opute.capability.tunneling.v1` contract.
- A `tunnel-recipe.v1` envelope containing an existing `host-plan.v1`.
- Provider installation through a read-only MCP manifest and generic recipe
  execution.
- Cordis-style provider generations, candidate validation, activation, drain,
  disposal, and rollback.
- Durable recipe/provider operations with cancellation, recovery, and status-only
  reconnect.
- Authenticated public exposure of the approved Host Agent `/mcp` binding.
- Generic local readiness and public route observations.
- Cloudflare as the first provider implementation, without Cloudflare knowledge
  in Host Agent core.

### Out of scope

- A second tunnel executor or provider-specific execution path.
- `install_cloudflared_connector` or any vendor-named lifecycle operation in
  Host Agent core.
- TUI natural-language intent classification, recipe execution, authorization,
  provider selection, or lifecycle decisions.
- Unauthenticated public MCP, raw public HTTP, quick tunnels, arbitrary local
  port forwarding, or credentials embedded in URLs.
- Application `/chat` success. Chat remains a separate application canary with
  parsed SSE and correlated runtime evidence.
- A repository-wide removal of transitional CLI/transport modes. That is a
  separate migration; this plan defines the boundary the final TUI and Host
  Agent must use.

## Architectural boundaries

The implementation must preserve the following ownership model:

| Area | Owns | Must not own |
| --- | --- | --- |
| `contracts/` | Versioned capability, recipe, operation, provider, and observation schemas | Runtime state, UI behavior, vendor implementation |
| `internal/cordis/` | Service definitions, dependency resolution, scoped effects, lifecycle, generations, drain, disposal, typed events | MCP protocol details, tunnel vendor behavior, host shell commands |
| `internal/cordis/mcp/` | Streamable HTTP provider connection, MCP negotiation, discovery, schema/tool mapping, auth, tasks, cancellation | Recipe execution, activation policy, vendor commands |
| `internal/hostmcp/` | Public Host Agent MCP, dispatch/admission, task projection, recipe/provider orchestration, activation | Cloudflare or other vendor lifecycle |
| `internal/recipe/` | Source policy, decoding, canonical hashes, inputs/defaults, compatibility, validation, plan extraction | Side effects, MCP connections, provider behavior |
| `internal/plan/` | Generic `host-plan.v1` validation/execution, readiness, retry, recovery, compensation | Provider identity and vendor policy |
| `internal/state/` | Durable operations, plans, provider generations, active pointers, observations, evidence | Provider behavior and transport logic |
| `internal/ops/` | Generic host effects and neutral observations | Ollama, Cloudflare, or vendor-specific lifecycle |
| `clients/tui/` | Typed catalog-driven editing, lookup, approval, operation/status projection, rendering | Host Agent `internal/`, providers, recipes, setup, prose execution |
| `clients/bootstrap/` or the client CLI bootstrap package | Signed Host Agent artifact delivery, target identity, SSH/management adapter, private MCP port forwarding | Provider installation, recipe execution, Host Agent internal state, vendor commands |
| `plugins/tunneling/<provider>/` | Provider MCP, manifest, recipe references, vendor APIs/CLI, native evidence | Host Agent `internal/`, TUI, another provider |

The dependency and runtime directions are:

```text
compile time:
  Host Agent core ──> neutral contracts
  TUI client ───────> pkg/hostagentclient ──> neutral contracts
  provider MCP ────> neutral contracts + provider SDK

runtime:
  TUI/CLI ── Streamable HTTP ──> Host Agent public MCP
  Host Agent ── generic MCP adapter ──> provider MCP
  provider MCP ──> vendor system
```

MCP is the wire adapter for a remote service. The Cordis service context sees
an injected typed service and its effects, not an MCP URL, session, tool name,
or JSON-RPC object. Recipes and generic host operations must not depend on
MCP implementation details.

## Machine and network topology

### Machine A: Host Agent owner

Machine A runs:

- the Host Agent MCP server as the authoritative durable service;
- the local Host Agent `/mcp` listener bound privately, normally to loopback
  or another explicitly protected interface;
- the selected tunneling provider MCP, if the provider is locally supervised;
- the provider-owned connector/tunnel process, if managed mode is selected;
- the durable state, active capability pointer, catalog, and operation records.

The Host Agent MCP listener is never made public by binding it directly to an
unprotected interface. Public access is created by a recipe-managed ingress
whose destination is the approved Host Agent MCP service identity.

### Machine B: TUI and bootstrap client

In steady-state attach mode, Machine B runs only the independent TUI client.
It does not need:

- Go Host Agent internals;
- the provider module;
- the provider binary or vendor CLI;
- the recipe files;
- local systemd access to Machine A;
- an LLM provider.

The TUI receives an explicit endpoint such as:

```text
https://agent-a.example.com/mcp
```

and an external credential reference. Remote mode must never start, install,
or repair a local Host Agent service. It must fail with a typed connection
error if the endpoint, TLS certificate, or credential is invalid.

Machine B may also run a separate, explicitly requested bootstrap command. The
bootstrap command is not a provider installer and is not part of the TUI's
steady-state execution surface. It uses a privileged machine-management
channel to install the Host Agent on Machine A when no Host Agent MCP endpoint
exists yet.

### Bootstrap channel

Public MCP cannot bootstrap a machine that has no Host Agent service. Initial
installation therefore requires an out-of-band bootstrap transport. SSH is the
first adapter; WinRM, a cloud management agent, or another approved transport
can be added later.

The bootstrap adapter owns only Host Agent distribution and service startup:

- authenticate Machine A and verify its host identity;
- transfer a signed/pinned Host Agent release artifact and install metadata;
- install the Host Agent service with a private MCP listener;
- start the service and verify local authenticated MCP readiness;
- establish an ephemeral port forward to Machine A's private `/mcp` endpoint;
- hand the connection to the normal public Host Agent MCP client.

It must not install Cloudflare, run a vendor CLI, mutate provider state, or
execute an unreviewed recipe directly over SSH. Once the private MCP endpoint
is ready, provider and tunnel setup proceeds through the ordinary Host Agent
manifest, recipe, plan, admission, task, and Cordis lifecycle.

The initial SSH sequence is:

```text
Machine B bootstrap CLI
  -> authenticated SSH to Machine A
  -> verify signed Host Agent artifact
  -> install/start private Host Agent MCP on Machine A
  -> SSH local port forward to Machine A's private MCP endpoint
  -> Host Agent MCP client calls install_provider / run_tunnel_recipe
  -> tunnel exposes the approved Host Agent MCP binding
  -> TUI switches to the resulting public HTTPS endpoint
```

The Host Agent core must not import or own SSH. The bootstrap client owns the
adapter, target authentication, artifact transfer, port forwarding, and
bootstrap-specific audit record. The Host Agent receives only normal MCP calls
after it is running.

Bootstrap must be explicit and fail closed:

- require an explicit target such as `ssh://user@machine-a`;
- require host-key or equivalent target identity verification;
- require an immutable release version plus signature/digest verification;
- require explicit user approval before remote service installation;
- never copy arbitrary user commands or provider secrets into the bootstrap
  shell;
- use an ephemeral port forward rather than exposing the private MCP listener;
- preserve a redacted bootstrap record and remote service status;
- provide rollback for a failed Host Agent installation without touching
  unrelated services.

### Public ingress contract

The public route must satisfy all of these conditions:

1. HTTPS with certificate and hostname verification.
2. Authentication before MCP initialization and tool discovery.
3. No bearer token, tunnel token, or credential in the URL or public logs.
4. Forwarding only to the approved Host Agent MCP binding.
5. Support for the Streamable HTTP request/response lifecycle, including
   streamed SSE responses where negotiated and JSON responses where selected.
6. Proxy idle, request, and response-size limits large enough for bounded MCP
   operations and task polling, or an explicit typed timeout visible to the
   TUI.
7. No assumption that a process being alive means the public route is ready.
8. Independent observations for local MCP readiness, connector readiness,
   public TLS/route readiness, and authenticated MCP readiness.

## User-facing flows

### Local bootstrap on Machine A

An administrator can bootstrap the Host Agent and tunnel locally using a
temporary local client connection:

```bash
opute-host-agent-tui \
  --url http://127.0.0.1:3014/mcp \
  --credential-ref host-agent-local
```

The local client invokes the generic provider installation flow:

```text
connect local Host Agent MCP
  -> install provider from trusted descriptor
  -> discover read-only provider manifest
  -> validate pinned tunnel recipe
  -> execute generic host plan
  -> validate neutral Host Agent MCP route
  -> activate tunneling generation
```

The exact installed binary/service is provider-owned. The Host Agent does not
contain a Cloudflare installation path.

### Remote bootstrap from Machine B

When Machine A has no Host Agent yet, the remote CLI uses the bootstrap
adapter, then delegates all provider work to MCP:

```bash
opute-host-agent bootstrap \
  --target ssh://user@agent-a \
  --host-agent-release <immutable-release> \
  --provider-source ./providers/tunnel-provider.yaml \
  --activate
```

The command performs this sequence:

1. Authenticate and verify Machine A through SSH host-key policy.
2. Install and start the signed Host Agent release on Machine A.
3. Verify Machine A's private authenticated MCP endpoint.
4. Open an ephemeral SSH port forward from Machine B to that endpoint.
5. Call the normal `install_provider --activate` flow through MCP.
6. Apply the pinned tunnel recipe targeting the approved `host-agent-mcp`
   binding.
7. Verify public TLS, route, authentication, and MCP readiness.
8. Close the bootstrap port forward and print the resulting public endpoint
   reference for the independent TUI.

If a public route cannot be established, the command leaves the Host Agent in
its private, inspectable state and reports a resumable operation. It does not
fall back to direct vendor installation or claim that the remote TUI is ready.

### Remote operation from Machine B

After the route is active, the same TUI binary connects remotely:

```bash
opute-host-agent-tui \
  --url https://agent-a.example.com/mcp \
  --credential-ref host-agent-machine-a
```

The TUI then performs normal catalog/status/operation calls over MCP. It does
not know whether the route uses Cloudflare, another tunnel provider, or a
private HTTPS reverse proxy.

### Direct recipe flow

Advanced users may apply a generic tunnel recipe directly through the public
Host Agent contract:

```bash
opute-host-agent recipe validate \
  --kind tunnel \
  --source ./tunnel.yaml

opute-host-agent recipe apply \
  --kind tunnel \
  --source ./tunnel.yaml \
  --activate

opute-host-agent recipe status \
  --kind tunnel \
  --run-id <id>
```

The CLI and MCP operations use the same resolver, validator, plan runner,
Cordis lifecycle, and durable state. There is no separate Cloudflare command
in the Host Agent CLI.

## Contracts

### Neutral tunneling capability

The stable capability identifier is:

```text
opute.capability.tunneling.v1
```

It describes operations and observations, not a provider enum. The neutral
observation must cover:

- `ready`;
- connector readiness;
- approved local service identity and local readiness;
- public endpoint and route readiness;
- TLS/authentication verification without secret values;
- binding identity and normalized path;
- provider generation ID and catalog revision;
- operation/evidence references and observation time;
- teardown or compensation state.

Provider-specific fields such as account ID, tunnel UUID, vendor ingress rule,
`cloudflared` version, or native API response belong only in provider evidence.

### Provider descriptor

The trusted descriptor contains generic bootstrap data only:

```yaml
schema: opute-provider-plugin.v1
pluginId: com.example.tunnel-provider
version: 1.0.0
capabilities:
  - id: opute.capability.tunneling.v1
    version: 1
server:
  transport: streamable_http
  endpoint: https://provider.example/mcp
```

The descriptor is trusted composition data, not an installation script. The
Host Agent validates its identity, endpoint, transport, and capability refs.
MCP `serverInfo` is informational and never establishes provider identity.

### Provider install manifest

The provider MCP must expose the read-only operation:

```text
opute.provider.get_install_manifest
```

The manifest declares:

- provider identity and capability versions;
- capability dependencies;
- immutable recipe references and supported modes;
- typed provider service operations and JSON Schemas;
- neutral validation operation identifiers;
- optional dynamic service definitions.

The Host Agent validates the manifest before any recipe mutation. Provider
service operations are published only as an authorized catalog overlay after
the candidate generation is ready and active.

### Tunnel recipe

`tunnel-recipe.v1` is a typed capability envelope over the shared recipe
loader and existing `host-plan.v1` runner. It is not a second execution
engine.

Required content:

- recipe ID and version;
- `tunnel-recipe.v1` contract version;
- declared serving contract, initially `http-exposure.v1`;
- bindings to approved local service identities;
- public hostname/path and authentication policy;
- typed inputs, defaults, and secret/reference declarations;
- minimum Host Agent compatibility;
- required generic Host Agent capabilities;
- embedded `host-plan.v1`;
- optional mapping to the neutral tunneling observation.

An input called `localTarget` may not by itself authorize public access. The
Host Agent resolves the target through an approved service identity or an
explicit policy binding. A recipe that requests an arbitrary local port,
unapproved path, or unapproved public hostname is rejected before execution.

Example shape:

```yaml
contractVersion: tunnel-recipe.v1
recipeId: com.example.tunnel-provider.managed
recipeVersion: 1.0.0
servingContract: http-exposure.v1
inputs:
  serviceId:
    required: true
    schema: {type: string}
  publicEndpoint:
    required: true
    schema: {type: string, format: uri}
  credentialRef:
    required: true
    secret: true
    schema: {type: string}
bindings:
  - id: host-agent-mcp
    serviceId: ${vars.inputs.serviceId}
    publicEndpoint: ${vars.inputs.publicEndpoint}
    path: /mcp
plan:
  contractVersion: host-plan.v1
  planId: com.example.tunnel-provider.managed
  generation: 1
  nodes: []
outputMapping:
  ready: nodes.route-ready.output.ready
```

The first-party Host Agent MCP binding must be represented as a stable
service identity such as `host-agent-mcp`, not as a Cloudflare-specific name.

## Cordis and MCP lifecycle

The complete installation and activation flow is:

```text
trusted descriptor
  -> Streamable HTTP MCP connect
  -> protocol/auth negotiation
  -> tools/list and manifest discovery
  -> manifest, schema, dependency, and recipe-pin validation
  -> candidate ProviderGeneration mounted in Cordis context
  -> recipe source/input/plan/catalog validation
  -> durable host-plan execution
  -> neutral local + public MCP route validation
  -> generation marked ready
  -> active tunneling pointer atomically updated
  -> authorized catalog/service overlay published
  -> prior generation drained and disposed in reverse order
```

The provider MCP is a remote Cordis service implementation. The Host Agent
MCP adapter is the Cordis plugin that owns the connection and effects. The
provider does not call back into Host Agent MCP to request installation.

### Activation rules

1. Keep the previous active generation unchanged while setup runs.
2. Execute the expanded recipe plan once through `internal/plan.Runner`.
3. Run the Host Agent-owned validation flow selected by the declared serving
   contract, never by provider ID.
4. Require local MCP readiness, public route readiness, and authenticated MCP
   readiness before activating the remote TUI route.
5. Persist generation, recipe source/revision/hash, expanded-plan hash,
   redacted inputs, catalog revision, and neutral observation.
6. Publish provider-declared operations only through the authorized dynamic
   catalog overlay.
7. Drain existing sessions and dispose the old generation in reverse effect
   order after the new generation is active.
8. On failure, retain the old healthy generation and persist candidate failure
   evidence.

### Remote TUI session rules

The TUI client must:

- perform MCP initialize and discovery over the configured HTTPS endpoint;
- bind all calls to the catalog revision it observed;
- reject stale catalog/entity/output bindings visibly;
- stream MCP responses without assuming a local process or shared filesystem;
- reconnect after transport loss and refresh catalog/session state;
- poll durable operation status after reconnect;
- never replay the mutation that created an operation;
- use MCP cancellation and Host Agent operation cancellation explicitly;
- preserve operation/generation identity across reconnects;
- redact tokens, cookies, hidden prompts, and unauthorized provider output.

The Host Agent must not silently move an existing session between provider
generations. A session that cannot drain receives an explicit retryable or
session-ended result.

### Cordis effects and disposal

The owning generation/fiber must dispose, in reverse registration order:

- provider MCP session and authentication state;
- supervised provider process, if applicable;
- provider task handles and subscriptions;
- dynamic catalog/service overlay;
- tunnel connector/service effect;
- credential references and temporary files;
- recipe compensation and owned route state.

Disposal is idempotent. Process existence, route health, or an old event is not
proof that an operation completed.

## Generic Host Agent primitives

Managed tunnel recipes may use only typed generic primitives for host changes:

- verified HTTPS artifact download with SHA-256;
- managed file/configuration reconciliation under an ownership root;
- host service supervisor readiness and state reconciliation;
- bounded host command execution only where no typed primitive exists;
- HTTP/MCP readiness probes;
- TLS/authentication/public-route observations;
- typed teardown and compensation tied to resources owned by the generation.

Every mutation must be a visible plan node with durable status and evidence.
The recipe must not be an opaque vendor installer script.

Cloudflare-specific account, DNS, tunnel-token, connector, API, and CLI
behavior belongs in `plugins/tunneling/cloudflare`. The Host Agent consumes
only the provider-neutral manifest and observation.

## Security and source policy

- Local recipe files are size-limited, canonicalized, and hashed before action.
- Remote mutating recipes require HTTPS, an immutable full revision, and an
  expected raw-content SHA-256. Branches, mutable tags, `main`, and `latest`
  are rejected.
- Redirects must remain inside the approved HTTPS policy.
- Source URI, revision, raw hash, canonical recipe hash, expanded plan,
  catalog revision, redacted inputs, generation ID, and activation evidence are
  persisted before mutation.
- Secrets are typed references or secret inputs and are redacted from logs,
  events, tasks, operation records, result projections, and TUI inspectors.
- Secrets must not appear in URLs, durable idempotency keys, service names,
  catalog metadata, or public route observations.
- Public Host Agent MCP access requires TLS and authentication before
  initialize, discovery, or mutation. Health alone is not authorization.
- The TUI stores or resolves credentials on Machine B; it never prints or
  embeds them in the endpoint URL.
- A recipe cannot invoke nested `run_host_plan`/recipe operations or bypass
  MCP dispatch, authorization, admission, task policy, or approval.
- Ambiguous ownership, non-disposable validation state, invalid TLS, missing
  authentication, an arbitrary local target, or an unapproved public binding
  fails closed.

## Public operations and catalog parity

The generic Host Agent surface is:

```text
validate_tunnel_recipe
run_tunnel_recipe
get_tunnel_run
install_provider
validate_provider
provider_status
reload_provider
```

`--activate` is the idiomatic client flag; the MCP field is `activate: true`.
These operations are thin adapters to existing recipe, plan, Cordis, state,
and MCP machinery. They contain no Cloudflare branch.

Update and validate in parity:

- Go catalog definitions and dispatch;
- standalone/HTTP catalogs and mutation policies;
- admission/resource categories and task-aware operation registry;
- embedded schemas and schema exports;
- dynamic catalog revision/list-changed behavior;
- approvals, cancellation, recovery, redaction, and audit projections;
- sibling Opute schema exports where required.

Provider overlays must be absent before activation and after generation
disposal. A stale TUI catalog must be rejected rather than silently adapted.

## Implementation workstreams

### 1. Remote TUI transport contract

- Complete `clients/tui` as an independently buildable module/process.
- Use only `pkg/hostagentclient`, neutral contracts, and a generic
  Streamable HTTP MCP client.
- Define explicit local attach, remote attach, and remote bootstrap modes.
- Keep remote attach incapable of local systemd bootstrap or provider setup.
- Add a separate generic bootstrap client/command with an SSH adapter first;
  keep that adapter outside the Host Agent core and provider modules.
- Install only the signed Host Agent artifact through the bootstrap channel;
  perform provider/tunnel setup through the private MCP port forward.
- Implement TLS verification, credential references, request/stream deadlines,
  reconnect, catalog refresh, operation polling, and cancellation.
- Add machine-separated tests where attach mode cannot read Machine A's files or
  access its loopback address, while bootstrap mode can use only the explicitly
  authorized management channel.

### 2. Cordis/MCP provider adapter

- Require Streamable HTTP and the negotiated MCP protocol revision.
- Require the read-only install manifest before recipe mutation.
- Validate descriptor/manifest identity, schemas, capability dependencies, and
  immutable recipe references.
- Mount a candidate generation with an owned adapter session and disposer.
- Route provider service calls by generation, not a global mutable adapter.
- Exercise delayed calls, failed readiness, task cancellation, drain,
  rollback, reconnect, and reverse disposal with a fake HTTP provider.

### 3. Generic tunneling contract and recipe

- Finalize `opute.capability.tunneling.v1`, `tunnel-recipe.v1`,
  `TunnelObservation`, and the `http-exposure.v1` serving contract.
- Replace arbitrary `localTarget` authority with approved service identity and
  Host Agent endpoint policy.
- Add an explicit first-party `host-agent-mcp` binding restricted to `/mcp`.
- Share source loading, canonical hashing, inputs, compatibility, redaction,
  and plan extraction with runtime recipes.
- Add typed teardown/compensation declarations for every managed effect.

### 4. First-party provider module

- Keep Cloudflare MCP, native API/CLI calls, provider manifest, provider
  recipe references, and native observations in
  `plugins/tunneling/cloudflare`.
- Support managed mode and external mode where meaningful.
- In managed mode, reconcile the connector through generic host effects and
  provider-owned recipe data.
- In external mode, validate a user-supplied public endpoint without installing
  a vendor binary.
- Expose a neutral validation operation through the provider contract; the
  Host Agent remains authoritative for activation.

### 5. Durable state, recovery, and cleanup

- Persist source provenance, hashes, expanded plan, redacted inputs, operation
  and task IDs, generation ID, activation transition, observation, and cleanup
  state.
- Reconcile in-progress work to `unknown` after restart.
- Resume only from the persisted expanded plan and revalidate each node before
  action; do not re-fetch the recipe for resume.
- Drain old generations before removing provider sessions and overlays.
- Restrict teardown to resources owned by the candidate generation. If
  ownership cannot be proven, preserve evidence and stop.

### 6. Catalog, policy, and release parity

- Add every generic operation to Go/standalone catalogs, dispatch, admission,
  task policy, schemas, and sibling exports.
- Verify list-changed/catalog revision behavior at activation and disposal.
- Keep Host Agent usable with no provider MCP or LLM installed.
- Add import-boundary tests proving the core does not depend on TUI or concrete
  providers and providers do not depend on Host Agent `internal/`.

## Validation plan

### Contract and recipe unit tests

Cover:

- JSON/YAML decoding and required envelope fields;
- input defaults, schemas, references, and unresolved variables;
- canonical recipe/source hashing and size/path limits;
- immutable remote revision and SHA-256 enforcement;
- unsupported versions, schemes, redirects, hash mismatches, and traversal;
- duplicate/ambiguous bindings and unauthorized service identities;
- recursive plan rejection and unsupported generic capabilities;
- neutral output mapping, teardown declarations, and secret redaction.

Required sentinel:

```text
RECIPE_SAFETY_PASS
```

### Cordis/MCP integration tests

Use a fake Streamable HTTP provider, not direct in-process provider calls, to
prove:

- protocol/auth/descriptor/manifest validation occurs before mutation;
- the provider must expose the read-only install manifest;
- recipe pins and provider schemas are enforced;
- dependency resolution and candidate mounting are deterministic;
- provider calls route through the generation-aware adapter;
- failed readiness leaves the previous generation active;
- activation publishes a catalog overlay and disposal removes it;
- task cancellation, drain, rollback, reverse disposal, and session affinity;
- restart turns unconfirmed operations into `unknown`;
- reconnect polls status and never replays a mutation.

Required sentinels:

```text
MCP_PROVIDER_CONTRACT_PASS
CORDIS_LIFECYCLE_PASS
ACTIVATION_CORRECTNESS_PASS
```

### Remote TUI tests

Prove with the TUI and Host Agent running as separate processes and separate
network identities:

1. The TUI connects to `https://<host>/mcp` using a valid credential reference.
2. Invalid TLS, invalid credentials, expired credentials, and an unauthenticated
   endpoint fail before tool discovery.
3. The TUI never starts a local service when given a remote endpoint.
4. MCP initialize, `tools/list`, catalog projection, typed lookup, and a
   read-only operation complete over the public route.
5. A long-running mutation returns a durable operation identity; transport
   loss followed by reconnect polls status without replay.
6. Cancellation, partial state, `unknown` recovery, and stale catalog revisions
   are visible and typed.
7. Streaming responses, JSON responses, proxy idle limits, and bounded timeout
   errors are handled without assuming a shared filesystem.
8. No provider name, vendor endpoint, recipe path, token, or secret appears in
   the TUI's public contract or inspector.

Required sentinel:

```text
TUI_REMOTE_STREAMABLE_HTTP_PASS
```

### Bootstrap tests

Use a disposable Machine A or an isolated remote test target to prove:

- a missing Host Agent can be installed from Machine B only with an explicit
  bootstrap command and approved target identity;
- an invalid host key, release signature/digest, credential, or target fails
  before remote mutation;
- the bootstrap adapter installs only the Host Agent artifact and service;
- the provider is not installed through SSH shell commands;
- the private MCP endpoint becomes reachable through an ephemeral port
  forward;
- provider installation, tunnel setup, activation, and route validation then
  occur through ordinary Host Agent MCP operations;
- the port forward can close after public route activation;
- a failed bootstrap leaves no orphaned service or credential and records
  redacted evidence;
- rerunning bootstrap is idempotent and resumes through durable MCP status
  rather than replaying a provider mutation.

Required sentinel:

```text
HOST_AGENT_REMOTE_BOOTSTRAP_PASS
```

### Security and catalog tests

Verify:

- public `/mcp` requires TLS/authentication before initialization;
- only the approved `host-agent-mcp` binding can target the Host Agent MCP;
- arbitrary local ports, public paths, and mutable recipe sources are rejected;
- tokens are absent from URLs, logs, tasks, events, results, and TUI output;
- generic operation effects match admission, approval, resource, and task
  policy;
- Go/standalone/runtime schema exports are identical;
- provider overlays and active pointers are generation-scoped;
- import audits find no concrete provider/TUI dependency in Host Agent core.

### Disposable local/public functional acceptance

Run only with the existing coordination lease, an explicit disposable marker,
and a verified cleanup scope:

1. On Machine A, inventory Host Agent, provider, connector, services,
   endpoints, generations, active pointers, and configuration without logging
   secrets.
2. Remove only test-scoped resources through typed provider teardown/recipe
   operations; fail closed on ambiguity.
3. Start Host Agent MCP privately on Machine A.
4. Start the provider MCP over Streamable HTTP and install it through the
   manifest and generic recipe flow.
5. Activate the tunnel recipe with `--activate`, targeting only the approved
   Host Agent MCP binding at `/mcp`.
6. Confirm independent observations for local MCP readiness, connector
   readiness, public TLS/route readiness, and authenticated public MCP
   readiness.
7. From Machine B, with no access to Machine A's filesystem or loopback,
   launch the separate TUI against the public HTTPS endpoint.
8. Complete catalog discovery, a typed read-only operation, a durable mutation,
   reconnect/status polling, cancellation, and a second idempotent apply.
9. Verify the active generation, catalog revision, operation IDs, and evidence
   correlate across Machine A and Machine B.
10. Tear down the test generation and verify the owned connector/service/files,
    credentials, route, overlay, and active pointer are gone.

For a real Cloudflare provider, the public route gate additionally requires a
disposable Cloudflare account/token and a declared route. Without those
inputs, pass the fake provider and local-ingress gates but do not claim live
Cloudflare success.

Required live sentinel:

```text
CLOUDFLARE_TUNNELING_PASS
```

### Opute application chat canary

If the tunnel is used by Opute application chat, run a separate canary after
the remote TUI/MCP gate:

- send a literal prompt through localhost `/chat`;
- send the same prompt through the public HTTPS application edge;
- parse the complete SSE stream;
- require non-empty assistant content and the expected marker;
- correlate request ID, provider generation, catalog revision, selected
  runtime, tool/runtime trace, and terminal acceptance;
- treat HTTP 200, health, recipe completion, or assistant text without full
  trace evidence as failure.

From WSL, use the sibling Opute Bun auth resolver or Playwright for protected
public-edge authentication. Never print credentials or bearer values, and do
not confuse a public MCP route pass with a chat pass.

## Failure, recovery, and rollback

- A failed candidate never changes the active pointer.
- A failed activation or drain retains/restores the previous healthy
  generation and records typed evidence.
- Unconfirmed work after restart is `unknown`, not success.
- A disconnected TUI may reconnect and poll durable status but must not replay
  a mutation.
- A provider session that cannot drain returns an explicit retryable or
  session-ended result; it is not silently moved to another generation.
- If cleanup ownership cannot be proven, stop and preserve evidence rather than
  deleting a guessed path or process.
- Roll back the complete provider generation and recipe state, not by adding a
  vendor-specific fallback branch to Host Agent core.

## Exit criteria

The plan is complete only when:

1. A separate TUI module connects to a separate Host Agent process over
   Streamable HTTP on both localhost and a public HTTPS endpoint.
2. An explicit bootstrap client can install the signed Host Agent on Machine A
   through an approved management adapter, then switch to MCP for provider and
   tunnel setup.
3. Machine B can operate Machine A in steady-state attach mode without local
   filesystem, loopback, provider, recipe, or LLM access.
4. The Host Agent MCP public route is authenticated, TLS-protected, and limited
   to approved bindings.
5. Tunneling is represented by a neutral capability and recipe contract with
   no Cloudflare-specific core lifecycle code.
6. Provider installation uses the read-only MCP manifest, generic host plan,
   Cordis candidate lifecycle, neutral validation, atomic activation, and
   generation-scoped catalog overlay.
7. Recovery, cancellation, reconnect, compensation, teardown, bootstrap
   rollback, and redaction
   tests pass with the required sentinels.
8. Fake-provider and local-ingress tests pass; live Cloudflare success is
   claimed only when disposable account and route evidence exist.
9. Application chat, if in scope, is independently certified using complete
   parsed SSE and correlated runtime/tool trace evidence.
