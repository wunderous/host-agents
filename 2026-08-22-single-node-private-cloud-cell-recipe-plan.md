# Opute single-node private-cloud cell recipe

**Status:** Detailed implementation plan; plan-only artifact

**Date:** 2026-08-22

**Revision:** 2026-08-23 — provider ports, Incus compute, and tenant-scoped resource identity

**Repository:** `/home/houman/github/wunderous/opute-host-agent`

## Executive intent

Turn the clean-slate Opute Host Agent bootstrap into a durable, reviewable,
MCP-executed recipe that provisions one isolated private-cloud cell and leaves
it ready for the Opute control plane to onboard tenants and their applications.

When this work is complete, an operator should be able to point an authorized
MCP client at a clean Host Agent and submit one pinned recipe. The Host Agent
will then provision the virtualization target, install K3s, install shared
platform services, establish the tenant-isolation baseline, expose the control
plane when requested, and return durable readiness evidence. A client must not
need to SSH to the target, run Incus, invoke kubectl, run Helm, or call
Kubernetes APIs directly.

This is an infrastructure-cell recipe, not a user-management implementation.
Opute Platform remains responsible for accounts, authentication policy,
authorization policy, tenant records, namespace allocation, application
intent, and durable orchestration. The Host Agent executes the explicit typed
infrastructure assignments needed to make that control plane possible.

## Overarching vision

Opute Host Agent is the execution layer for a mini AWS:

```text
Opute Platform
  identity, signup, tenant policy, application intent, durable orchestration
                              │
                              │ authorized typed MCP recipe / plan
                              ▼
Opute Host Agent
  capability ports, provider binding, catalog, admission, durable run state,
  readiness, retry, resume, evidence
                              │
                              │ generic adapter + active provider generation
                              ▼
Provider implementations
  Incus compute | K3s/MicroK8s | registry | PostgreSQL/CNPG | Cloudflare | Ollama
                              │
                              ▼
Private-cloud cell and its external systems
  compute → Kubernetes → shared services → platform/edge → tenant cells
```

The first implementation is deliberately a single-node cell with a stable
upgrade path. It should establish the contracts and evidence model that later
allow the same Host Agent to reconcile a multi-node, highly available cell
without changing the Platform-facing ownership model.

The desired product property is not merely “K3s installed.” It is:

> A cell is ready only when its compute substrate, Kubernetes control plane,
> shared services, platform control plane, authentication boundary, tenant
> isolation baseline, and required ingress paths have each produced explicit,
> correlated readiness evidence.

## Grounded current state

The current repository already provides most of the execution foundation:

- `internal/plan` implements `host-plan.v1` validation and durable execution,
  including dependencies, readiness validation, polling, retries, convergence,
  recovery, compensation, interpolation, and resume.
- `internal/recipe/recipe.go` implements source loading, SHA-256 provenance,
  input defaults and validation, secret redaction, compatibility checks, and
  embedding of a host plan. Its current `runtime-recipe.v1` envelope is tied to
  `openai-chat.v1` and optional active-runtime activation.
- `internal/hostmcp/recipe_run.go` already loads, validates, persists, runs,
  resumes, and reports recipe-backed host plans.
- `internal/ops/service.go` already supports pinned K3s installation through
  `InstallK3sArgs`, including `Version` and `InstallArgs` internally.
- `internal/ops/helm_template.go` already renders a caller-selected Helm chart
  and returns a manifest, but its catalog output schema must be made explicit
  before a plan can safely interpolate that manifest into a later action.
- Existing typed capabilities cover VM provisioning, K3s status, OCI registry,
  K3s registry configuration, Kubernetes resource status, PostgreSQL service
  reconciliation, Kubernetes secrets, manifest application, Cloudflare
  connector deployment, and HTTP probes.
- Incus is already a substantial concrete implementation, but it is not yet a
  provider-port implementation. VM lifecycle methods in
  `internal/ops/service.go`, system-container provisioning in
  `internal/ops/incus_container.go`, and Incus-specific argument parsing in
  `internal/tools/dispatch.go` must be moved behind one neutral compute port.
  The current implementation has enough behavior to become the first Incus
  provider; the plan does not assume that this behavior can be deleted or
  replaced without conformance evidence.
- The Ollama implementation demonstrates the desired provider shape: its
  `plugins/llm/ollama` MCP server publishes a neutral capability manifest and
  typed operation schemas; `internal/cordis/mcp` connects and discovers it;
  `internal/hostmcp` registers the manifest as a dynamic catalog overlay and
  dispatches through the active provider adapter. The implementation-specific
  behavior remains in the plugin.
- K3s does not yet follow that shape. `internal/tools/dispatch.go` routes
  `install_k3s`, `get_k3s_status`, and `uninstall_k3s` directly into
  `internal/ops/service.go`, where K3s installer, artifact, target, and
  readiness behavior live. This is the concrete seam that prevents a future
  MicroK8s implementation from being installed by composition alone.

The important gaps are contract gaps, not a reason to bypass the architecture:

1. There is no infrastructure-specific recipe family.
2. The public `install_k3s` schema and dispatcher do not fully expose the
   implementation’s version/install-argument fields.
3. `render_helm_template` does not yet publish a typed manifest output schema.
4. There is no first-party cloud-cell recipe or cloud-cell recipe MCP surface.
5. The current generic Kubernetes mutation surface is suitable for an
   operator-only bootstrap recipe, but not as the long-term end-user
   application deployment interface.

The plan addresses these gaps explicitly instead of hiding them in prose or
using a runtime recipe with a misleading serving contract.

## Scope and anti-scope

### In scope

- A new `cloud-cell-recipe.v1` envelope built around `host-plan.v1`.
- MCP validation, execution, status, and resume for cloud-cell recipes.
- A first-party single-node private-cloud recipe.
- MCP-only clean-slate provisioning of the compute and Kubernetes substrate.
- Verified platform chart acquisition, extraction, Helm rendering, and
  manifest application through Host Agent capabilities.
- Pinned K3s installation with embedded-etcd initialization and secrets
  encryption.
- OCI registry and K3s registry configuration.
- SQL-gated CloudNativePG readiness and consumer-secret projection.
- Platform authentication/authz secret installation and platform deployment
  readiness.
- Tenant-isolation baseline application and an explicit readiness marker.
- Optional or required public edge, depending on the finalized recipe input
  profile; the first production profile should require explicit edge inputs if
  public endpoints are part of the cell contract.
- Durable evidence, secret redaction, retries, resume, failure stop points,
  and packaged-recipe validation.
- A clear v2 extension point for three-node high availability.
- A provider-port architecture for replaceable backend implementations:
  Kubernetes control plane first, followed by compute, registry, database,
  and edge backends using the same manifest, adapter, generation, and catalog
  rules already exercised by Ollama and Cloudflare.
- Tenant-scoped resource identity and registry-backed URI resolution, using
  the adjacent `2026-08-23-resource-uri-scheme-plan.md` as the detailed
  resource-addressing design. The cloud-cell plan makes that identity
  mandatory for provider targets and internal tool dispatch.

### Explicitly deferred

- Opute user signup, identity-provider enrollment, tenant billing, and tenant
  membership management. These belong to Platform.
- End-user arbitrary Kubernetes manifests.
- End-user application lifecycle APIs.
- Per-tenant application authentication/authz UX. The cell recipe installs the
  platform and its baseline; Platform later exposes the effortless tenant
  service-auth flow.
- Three-node HA implementation.
- A production MicroK8s implementation in this milestone. The Kubernetes port
  and conformance fixture are in scope; MicroK8s becomes a follow-on provider
  package that must pass the same contract without Host Agent code changes.
- Destructive clean-slate reset or data deletion. Any reset needs a separate,
  explicit, ownership-checked workflow with snapshot/discard confirmation.
- Direct shell, SSH, Incus, Helm, kubectl, Cloudflare, or Kubernetes mutation
  from clients.

## Decisions and motivation

| Decision | Choice | Motivation and intent |
| --- | --- | --- |
| Recipe family | Add `cloud-cell-recipe.v1` instead of reusing `runtime-recipe.v1` | The existing runtime family describes serving runtimes and active runtime activation. Infrastructure should not be represented as an OpenAI-compatible runtime or accidentally participate in runtime replacement. |
| Execution language | Reuse `host-plan.v1` | There must be one typed executor for dependencies, readiness, retry, resume, evidence, and catalog revision checks. A second recipe-specific executor would create drift and weaken safety. |
| Mutation boundary | Every target mutation is a Host Agent MCP tool call | This keeps authorization, admission, auditability, and durable state at the intended boundary. The client describes intent through a recipe; it does not receive shell authority. |
| Compute target | Dedicated Incus VM for the production profile, with VM and system-container support in the same compute port | A VM gives stronger tenant-cell isolation for the production cell, while system containers remain a supported, lower-overhead compute target. Both must use the same typed provider boundary so a future compute backend can replace Incus without Host Agent dispatch changes. |
| Provider boundary | Host Agent-owned, versioned capability ports with generic MCP adapters and provider generations | The Host Agent owns the contract, authorization, admission, lifecycle, catalog, and evidence; a concrete provider owns vendor/API/CLI behavior. This is the open/closed boundary: adding MicroK8s must add a provider package and composition data, not a Host Agent dispatch branch. |
| Kubernetes implementation | Bind `opute.capability.kubernetes.v1` to K3s for the first production profile | K3s remains the initial concrete implementation, but the recipe expresses cluster intent such as version, topology, secrets encryption, and node role rather than K3s installer flags. The same port can later bind to MicroK8s or another distribution. |
| Provider selection | Select an explicit provider generation as data, then resolve canonical operation IDs through the active binding | Provider identity is a deployment/composition choice, not a switch in `internal/tools/dispatch.go`. The Host Agent must reject missing, unauthorized, incompatible, or ambiguous bindings and must never silently fall back to another implementation. |
| Port granularity | Separate backend lifecycle ports from generic Kubernetes API operations | Installing or removing a Kubernetes distribution is provider-specific. Applying a manifest, reading a Secret without its data, and checking a Deployment are Kubernetes API operations that can remain generic and shared across distributions. This avoids both K3s leakage and unnecessary duplicate adapters. |
| Migration scope | Define and certify Kubernetes and Incus compute ports in the foundational milestone; migrate registry, PostgreSQL/CNPG, and edge in staged waves | The first cell must establish the two substrate provider boundaries it directly depends on. Later waves still remove backend-specific core dispatch only after a neutral port, provider implementation, migration adapter, and conformance evidence exist. |
| K3s mode | Single server with `clusterInit`, `secretsEncryption`, and a single-node topology request | Embedded etcd makes the single node structurally compatible with a later HA topology, while secrets encryption protects Kubernetes Secret data at rest. The K3s provider maps this intent to its own flags; the recipe must not claim HA from one node. |
| Artifact trust | HTTPS artifact plus required SHA-256, then Host Agent-owned extraction/rendering | The Host Agent must verify the exact chart archive before using it. Rendering and applying inside the Host Agent preserves the MCP-only constraint and avoids an untyped client-side manifest handoff. |
| Platform deployment | Rendered Helm manifest applied through a Host Agent capability | The Host Agent remains product-neutral while the recipe supplies the selected platform chart and values. A future typed Helm-release capability can replace the generic operator-only apply step. |
| Tenant boundary | Install a baseline and validate a marker, not individual tenants | The recipe creates the control-plane prerequisites for tenant isolation. Platform owns the later creation of tenant accounts, namespaces, RBAC, quotas, NetworkPolicies, and service-auth bindings. |
| Authentication/authz | Platform-owned policy, Host Agent-owned secret/material placement | Host Agent can safely place caller-supplied secrets and verify resources without deciding who may access a tenant or service. Authorization decisions remain durable Platform policy. |
| Resource identity | Tenant-scoped canonical URI plus a Host Agent resource registry | A display name such as `blog` is not globally unique. The active authenticated tenant and canonical URI must be carried into internal resolution so identical names in different tenants cannot select the wrong resource or leak its existence. |
| Database readiness | Require SQL-gated CNPG and consumer-secret readiness | Kubernetes object existence is insufficient. Platform and task-ledger consumers must not start against an unready database or missing projected Secret. |
| Public edge | Separate connector readiness from public endpoint readiness | A running connector is not proof that the public hostname reaches the intended service. The recipe must validate connector deployment, route reachability, and authenticated MCP readiness independently. |
| End-user deployment | Reserve raw `apply_manifest` for operator bootstrap | Tenants need scoped, typed application capabilities with ownership and namespace checks. Reusing the broad operator primitive for users would make isolation depend on client discipline. |
| HA evolution | Keep v1 single-node and define a versioned v2 | HA changes quorum, endpoint identity, join tokens, failure domains, and recovery semantics. Those changes should be explicit rather than hidden behind a boolean in the single-node recipe. |

## Provider-port architecture

The existing Cordis provider direction in
`docs/adr/0002-provider-extension-architecture.md` is the reference model for
this plan. Ollama is the concrete example: the provider publishes
`opute.capability.llm-serving.v1`, the generic MCP adapter discovers and calls
its operations, and the Host Agent owns registration, lifecycle, authorization,
catalog revision, readiness, and evidence. The cloud-cell path must use that
same boundary for Kubernetes and, by migration wave, every replaceable
backend.

### The four parts of a port

1. **Neutral port contract:** `contracts/capability/` and versioned schemas
   define the capability ID, operation IDs, input/output JSON Schemas, effect,
   idempotency, resource kinds, readiness, streaming, cancellation, and
   redaction semantics. This is the interface that a provider implements; it
   is not an `internal/ops` type that a plugin must import.
2. **Host Agent provider kernel:** `internal/cordis/`,
   `internal/cordis/mcp/`, `internal/catalog/`, and `internal/hostmcp/` perform
   descriptor and manifest validation, MCP handshake, provider authorization,
   dynamic catalog registration, generation lifecycle, admission, dispatch,
   draining, and durable evidence. They must not know whether the provider is
   K3s, MicroK8s, Incus, Cloudflare, or Ollama.
3. **Concrete provider MCP:** an independently buildable package under
   `plugins/` implements the canonical operations using its own SDK, API, CLI,
   or typed remote-management logic. It owns vendor-specific flags, artifact
   handling, service behavior, and provider-native observations. It may not
   import Host Agent `internal` packages or call arbitrary shell on behalf of
   a caller.
4. **Composition and binding:** a trusted descriptor and provider install
   manifest select which implementation satisfies a port. The active
   generation binds canonical operation IDs to one provider adapter. Provider
   identity is recorded as metadata and evidence, but it must not be encoded
   as a Host Agent `switch` statement.

The process boundary is therefore:

```text
Opute Platform intent and authorization
                 │
                 ▼
Host Agent neutral port + host-plan executor
                 │  active provider generation
                 ▼
generic MCP adapter over Streamable HTTP
                 │
                 ▼
K3s / MicroK8s / Incus / CNPG / Cloudflare / Ollama provider MCP
                 │
                 ▼
provider-native API, SDK, CLI, or explicitly typed remote operation
```

The client and Platform still call only the Host Agent MCP surface. Provider
MCP is an internal implementation boundary behind Host Agent authorization and
durable execution; it is not a second user-facing control plane.

### Provider bootstrap invariant

Provider availability is a prerequisite to a cloud-cell run. A clean-slate
workflow must use the Host Agent MCP provider lifecycle operations
(`opute.provider.install`, `opute.provider.validate`, `opute.provider.status`,
and explicit activation through the existing provider-generation path) to mount
the selected provider. The lifecycle uses a pinned trusted descriptor and the
provider's Host Agent-owned recipe; it never launches an arbitrary executable
or accepts a client shell command.

The cloud-cell recipe then calls `opute.provider.status` during preflight and
requires the exact capability/version/provider binding declared by
`providerBindings`. If the product wants one user-visible “create cell” call,
Platform or a Host Agent-owned composition workflow must sequence provider
bootstrap before cloud-cell execution. It must not nest provider installation
inside an arbitrary recipe node or silently activate a different provider.

### Kubernetes port

Define the first infrastructure port as `opute.capability.kubernetes.v1`.
Its initial service and operation set is:

```text
service:  opute.capability.kubernetes
version:  1

opute.capability.kubernetes.provision
opute.capability.kubernetes.status
opute.capability.kubernetes.configure-registry
opute.capability.kubernetes.remove
```

The stable request expresses intent, not K3s commands:

```yaml
clusterId: <stable cell identity>
targetRef: <typed compute target reference>
targetKind: virtual-machine
version: <provider-compatible distribution release>
topology: single-node
nodeRole: server
clusterInit: true
secretsEncryption: true
```

The stable result contains bounded observations such as `clusterId`,
`distribution`, `version`, `status`, `ready`, `totalNodes`, and provider
metadata. It must not expose kubeconfig material, bootstrap tokens, or raw
credentials.

The K3s provider maps `clusterInit`, `secretsEncryption`, `topology`, and
`nodeRole` to `--cluster-init`, `--secrets-encryption`, and its own target
and service behavior. A future MicroK8s provider maps the same intent to
MicroK8s operations. Neither provider-specific flag arrays nor
`k3sInstallArgs` belong in the neutral cloud-cell contract.

Provider-specific extensions, if unavoidable, must be namespaced under the
provider ID and validated by that provider's declared schema. They are not
accepted by the generic first-party recipe unless the recipe explicitly binds
that provider. The normal path must remain portable across implementations.

### Incus compute port

Define the compute substrate as `opute.capability.compute.v1` and bind the
first production implementation to `com.opute.incus`. This is one port for
both Incus virtual machines and Incus system containers; they are different
resource kinds within the same provider contract, not two Host Agent
dispatchers.

The minimum lifecycle surface is:

```text
service:  opute.capability.compute
version:  1

opute.capability.compute.prepare-host
opute.capability.compute.host-status
opute.capability.compute.provision
opute.capability.compute.status
opute.capability.compute.start
opute.capability.compute.stop
opute.capability.compute.restart
opute.capability.compute.update-resources
opute.capability.compute.remove
opute.capability.compute.exec
```

`prepare-host` and `host-status` describe the Incus host/provider substrate;
the remaining operations are scoped to a compute resource. The neutral
provision request must include typed intent such as:

```yaml
resourceType: vm             # vm or container
targetKind: virtual-machine  # virtual-machine or system-container
resourceId: <canonical id>
image: <provider-compatible image reference>
cpu: <bounded quantity>
memory: <bounded quantity>
disk: <bounded quantity>
profile: <portable profile name>
```

The provider receives the active tenant context and/or a canonical resource
URI from Host Agent context. It must not derive tenant identity from a name,
free-form description, or provider-native label. A successful provision and
every list/detail/lifecycle result must return the canonical URI, for example
`vm:tenant-a:cell-vm` or `container:tenant-a:worker-01`, plus bounded provider
observations. A system container and a VM may have the same display name only
when their canonical resource types and tenant-scoped identities are distinct;
the resource registry remains the authoritative mapping. If the provider's
native namespace is host-global, the Incus provider must derive a stable,
tenant-safe provider instance name from the URI and retain the user's display
name separately; it must not force tenant isolation to depend on globally
unique user-facing names.

The Incus provider owns Incus API/CLI details, image normalization, QEMU versus
system-container launch behavior, profiles, storage and network devices,
autostart, guest-agent readiness, console/exec semantics, ownership labels,
and provider-native status parsing. It must not expose arbitrary Incus
commands. It may compose generic Host Agent host-plan, file, service, HTTP,
and MCP capabilities for provider setup, but all mutations still pass through
the Host Agent's authorization, admission, durable execution, and evidence
path.

The current `ProvisionVM`, `StartVM`, `StopVM`, `RestartVM`, `UpdateVMResources`,
`DeleteVM`, `ProvisionContainer`, and related Incus inventory/ownership
helpers are migration inputs for `plugins/compute/incus`. `internal/ops` may
retain typed generic coordinates and execution primitives, but the Host Agent
core must not select Incus through a product-specific `switch`. After parity,
the old VM/container tool names either disappear in the breaking catalog
revision or become short-lived adapters that resolve a URI and delegate to
the compute port.

The conformance suite must exercise both `targetKind: virtual-machine` and
`targetKind: system-container`, including provision, status, start/stop,
resource update, remove, ownership failure, URI output, retry/resume, and
provider-generation replacement. A fake compute provider must be able to
replace `com.opute.incus` in a recipe composition without changing Host Agent
source or canonical operation IDs.

### What stays generic in Host Agent

The open/closed boundary does not require a separate plugin for every helper.
Keep these as neutral Host Agent capabilities where their semantics are
backend-independent:

- `host-plan.v1` execution, retry, readiness, recovery, compensation, and
  evidence;
- artifact acquisition and digest inspection;
- host file and service supervision primitives used to install a provider;
- HTTP/MCP protocol probes;
- Kubernetes API operations such as applying a verified manifest, reading a
  resource without Secret data, and checking workload status, once a typed
  cluster connection is available.

Move a behavior behind a provider port when it owns a replaceable backend's
installation, lifecycle, protocol, credentials, or vendor-specific command
semantics. This prevents both vendor leakage into the Host Agent and needless
duplication of generic Kubernetes or host primitives.

### Migration waves

| Current backend behavior | Neutral port | First concrete implementation | Plan treatment |
| --- | --- | --- | --- |
| K3s install/status/uninstall and K3s registry configuration | `opute.capability.kubernetes.v1` | `plugins/kubernetes/k3s` | Required now; remove K3s branches from core dispatch and move K3s-specific implementation there. |
| Incus VM/container/network substrate | `opute.capability.compute.v1` | `plugins/compute/incus` | Required now; implement VM and system-container targets behind one port and remove Incus-specific core dispatch after conformance parity. |
| Local OCI registry lifecycle | `opute.capability.container-registry.v1` | First-party Kubernetes registry provider | Define the port and migration adapter; keep generic Kubernetes resource operations underneath it. |
| CNPG/PostgreSQL service reconciliation | `opute.capability.postgresql-service.v1` | First-party CNPG provider | Preserve SQL-gated readiness and consumer-Secret evidence while removing CNPG-specific dispatch from the core. |
| Cloudflare connector and public edge lifecycle | `opute.capability.tunneling.v1` / `opute.capability.edge.v1` | Existing Cloudflare provider, extended as needed | Reuse the existing provider shape; the cloud-cell recipe must bind the edge operation instead of calling a Cloudflare-specific core tool. |
| Ollama serving and context management | `opute.capability.llm-serving.v1` | Existing Ollama provider | Treat as the reference implementation and conformance fixture for all later providers. |

Each wave is complete only when the neutral port, provider manifest,
implementation, migration adapter, conformance tests, lifecycle evidence, and
recipe composition are present. A compatibility shim may preserve an old tool
name temporarily, but it must delegate to the canonical port and have a
removal version; it must not become a second implementation path.

## Tenant-aware resource addressing and internal disambiguation

The adjacent plan
`2026-08-23-resource-uri-scheme-plan.md` is the normative detailed design for
resource URIs, tenant plumbing, registry-backed resolution, and breaking
catalog migration. This cloud-cell plan adopts those rules as a prerequisite
of provider dispatch rather than treating them as a later UI concern.

The canonical shape is:

```text
<resource-type>:<tenant-id>:<resource-id>
```

For tenant-owned application services, add `service` to the closed resource
type vocabulary used by that adjacent plan. `service:<tenant-id>:blog` means
a tenant application/service identity; `host-service:<tenant-id>:...` remains
reserved for a Host Agent or operating-system service. This distinction avoids
confusing a user's `blog` service with a systemd unit such as
`user/opute-ollama.service`.

The internal resolution contract is:

1. Platform authenticates the caller, authorizes the requested operation, and
   supplies a typed tenant context. `open_assistant_session` may bind the
   session to that tenant; `tenantId` is a runner-provided reserved variable,
   not a user-controlled recipe input.
2. Host Agent validates the session tenant and carries it through plan state,
   tool dispatch, provider calls, resource admission, audit/evidence records,
   and catalog provenance. A caller cannot switch tenant by changing an
   argument or interpolated string.
3. URI parsing validates resource type, tenant segment, resource ID, and
   expected type. A URI for another active tenant fails closed before the
   provider is called. Depending on the Platform policy, the outward error may
   be normalized to not-found or forbidden so it does not reveal that a
   foreign resource exists.
4. `resource_registry` maps the URI to opaque, typed provider coordinates.
   Provision/reconcile/ensure operations register the URI; list/detail
   operations return it; lifecycle operations resolve it. Name-only lookup is
   never an authoritative selection mechanism.
5. The resolved URI and tenant context are passed to the selected provider
   generation. The provider may use its native name internally, but it cannot
   choose a different tenant or replace the Host Agent registry mapping.

This makes the duplicate-name case deterministic. If tenant A and tenant B
both own a service named `blog`, their identities are distinct:

```text
service:tenant-a:blog
service:tenant-b:blog
```

The internal typed call envelope is conceptually:

```json
{
  "tenantId": "<active-session-tenant>",
  "uri": "service:<active-session-tenant>:blog",
  "operation": "rename",
  "newName": "journal"
}
```

`tenantId` in this envelope is Host Agent context derived from the authenticated
session, not an independently trusted argument. The resolver checks that the
URI tenant matches it before the operation reaches a provider or resource
adapter.

When a user says “rename my blog service,” Platform resolves “my” to the
authenticated tenant and emits a typed rename operation bound to that tenant's
URI. If the request reaches Host Agent without a URI or a typed
tenant-scoped binding, Host Agent must return an ambiguity/invalid-reference
error; it must not scan all tenants, choose the first `blog`, or infer a
tenant from natural-language text. A tenant-scoped list/search can first
return the candidate URI, after which the mutation is URI-based.

For compute and cluster resources, the same rule applies to provider targets:
the cell VM is created with a desired name, but later Kubernetes, registry,
database, edge, exec, and lifecycle operations consume the resulting
`vm:<tenant>:<id>` or `cluster:<tenant>:<id>` URI. The desired name remains
display metadata and a provider-native coordinate, never the cross-tenant
identity.

The implementation must follow the adjacent plan's order: add the typed URI
package, session tenant plumbing, SQLite resource registry, resolver and
registration hooks, dispatch/schema migration, recipe interpolation, TUI
bindings, and then conformance/E2E coverage. Add `service` to the canonical
resource-kind constants and update the adjacent URI plan in the same change so
the two plans cannot define different vocabularies.

## Proposed cloud-cell recipe contract

### Envelope

Add a typed document equivalent in shape to the existing recipe envelopes:

```yaml
contractVersion: cloud-cell-recipe.v1
recipeId: first-party-single-node-private-cloud
recipeVersion: 1.0.0
cell:
  topology: single-node
  targetKind: virtual-machine
  capabilities:
    - compute
    - kubernetes
    - private-registry
    - sql-service
    - tenant-isolation-baseline
    - platform-control-plane
    - http-ingress
providerBindings:
  compute:
    capability: opute.capability.compute.v1
    providerId: com.opute.incus
  kubernetes:
    capability: opute.capability.kubernetes.v1
    providerId: com.opute.k3s
  containerRegistry:
    capability: opute.capability.container-registry.v1
    providerId: com.opute.registry.local
  postgresqlService:
    capability: opute.capability.postgresql-service.v1
    providerId: com.opute.cnpg
  edge:
    capability: opute.capability.edge.v1
    providerId: com.opute.cloudflare

tenantContext:
  source: authenticated-session
  reservedVariable: tenantId
  requireSessionBinding: true
inputs: ...
compatibility:
  minHostAgentVersion: ...
  requiredTools: ...
plan:
  contractVersion: host-plan.v1
  ...
outputMapping: ...
```

The cloud-cell envelope should reuse the generic source and input machinery
from `internal/recipe/source.go` and `internal/recipe/recipe.go` without
duplicating hashing, path safety, remote pinning, or secret-redaction logic.
It must not have an activation block that updates the active serving-runtime
pointer.

### Inputs

Inputs must be typed, bounded, and grouped by responsibility:

- **Cell identity:** cell ID, VM name, VM image, CPU, memory, and disk.
- **Tenant/resource identity:** the runner-provided tenant context and
  canonical resource references. `tenantId` is reserved session state and is
  not accepted as an arbitrary caller input. Creation may accept a desired
  provider name, but every resulting entity must return a URI and every later
  entity-scoped operation must use that URI.
- **Kubernetes provider:** explicit capability binding, provider-compatible
  release, target kind, cluster identity, topology, node role, secrets
  encryption, and any stable TLS/API identity needed by the selected profile.
- **Registry:** namespace, deployment name, storage size, node port, registry
  endpoint, registry name, and insecure/TLS mode.
- **Database:** CNPG cluster name, namespace, storage size, database names,
  consumer Secret name, service ownership labels, and retention policy.
- **Platform artifact:** HTTPS archive URI, required archive SHA-256, archive
  path, extraction path, chart path, release name, namespace, and Helm values.
- **Platform identity:** Secret name and secret data marked `secret: true`.
- **Tenant baseline:** manifest or verified policy artifact, marker kind/name,
  marker namespace, and an integrity digest if the manifest is passed directly.
- **Edge:** explicit connector token marked secret, connector namespace/name,
  service targets, platform endpoint, and MCP endpoint.

Input validation must reject unknown inputs, missing required inputs, invalid
Kubernetes identifiers, non-HTTPS artifacts, invalid digests, invalid URLs,
and insecurely shaped secret values. Secret inputs must be redacted from
recipe metadata, plan evidence, task text, and error messages.

### Outputs

The output mapping should expose only bounded, non-secret evidence:

- `cellReady`
- `cellId`
- `computeUri`
- `kubernetesUri`
- `vmName`
- `kubernetesVersion`
- `kubernetesDistribution`
- `kubernetesProvider`
- `registryReady`
- `databaseReady`
- `platformReady`
- `tenantBoundaryReady`
- `platformEndpoint`
- `mcpEndpoint`
- `publicEdgeReady`
- `catalogRevision`
- `recipeHash`
- `runId`

The output must not return database passwords, connector tokens, bearer
tokens, Kubernetes Secret values, or full credentials embedded in manifests.

## Execution DAG and intent

The first-party recipe should use the following ordered nodes. Each mutating
node must have a read-only validation block because `internal/plan.Validate`
requires a readiness proof for non-read actions.

### 1. Preflight and host substrate

**Nodes:** `preflight`, `incus`, `cell-vm`

- `preflight` calls `check_local_prerequisites` and `get_local_status`.
  Intent: fail before claiming any mutation can proceed when the Host Agent’s
  provider or required local commands are unavailable. It must also assert that
  `opute.provider.status` reports the requested Kubernetes capability binding as
  authorized, installed,
  schema-compatible, and backed by a ready provider generation. A provider
  identity mismatch is a failed preflight, not a fallback opportunity.
- `incus` calls `opute.capability.compute.prepare-host` through the active
  `com.opute.incus` provider and validates with
  `opute.capability.compute.host-status`. Intent: make the virtualization
  layer a durable, resumable provider prerequisite, not an undocumented
  operator action or a direct Incus command path.
- `cell-vm` calls `opute.capability.compute.provision` with
  `targetKind: virtual-machine`, the explicit desired resource identity, and
  the resource profile. It validates with
  `opute.capability.compute.status`, requiring a running instance and
  guest-agent readiness, and stores the returned `vm:<tenant>:<id>` URI.
  Intent: establish one unambiguous cell target and prevent later operations
  from guessing between multiple VMs. The same compute port must also support
  a `system-container` profile in conformance tests, even though the first
  cloud-cell profile uses a VM.

### 2. Kubernetes control plane (K3s provider in v1)

**Node:** `kubernetes`

Call the canonical `opute.capability.kubernetes.provision` operation with the
`vm:<tenant>:<id>` target URI from `cell-vm`, version, cluster identity,
topology, node role, and secrets-encryption intent:

```text
targetKind: virtual-machine
topology: single-node
nodeRole: server
clusterInit: true
secretsEncryption: true
```

Validate through `opute.capability.kubernetes.status` until `/status == ready`.
The active v1 provider is K3s, but the recipe must not invoke a K3s-specific
operation or pass a K3s flag array. Intent:

- Pin the exact Kubernetes distribution version.
- Make the single node an embedded-etcd server rather than an opaque default.
- Encrypt Kubernetes Secret data at rest.
- Leave the topology and node-role intent structurally compatible with future
  server joins.

The recipe must record the observed kubelet version and fail if the observed
version does not match the requested major/minor policy.

### 3. Registry and image-pull path

**Nodes:** `registry`, `registry-config`

- `registry` calls `opute.capability.container-registry.provision` with
  explicit namespace, name, storage, and node port. Validate through
  `opute.capability.container-registry.status` until `/status == ready`.
- `registry-config` calls
  `opute.capability.kubernetes.configure-registry` with the explicit endpoint,
  registry name, and TLS/insecure mode. Validate Kubernetes readiness again.

Intent: make image availability a first-class cell dependency and ensure the
K3s runtime can pull the images that the platform and tenant workloads use.
The registry endpoint must be stable and recorded in the result; the recipe
must not infer a live VM address by string matching.

### 4. SQL service and database consumer contract

**Node:** `database`

Call the canonical `opute.capability.postgresql-service.reconcile` operation
with a single-node PostgreSQL service profile, explicit cluster/database names,
consumer Secret metadata, and retention policy. Validate with
`opute.capability.postgresql-service.status` until `/ready == true`.

The v1 implementation may be CNPG, but CNPG-specific operator names and
reconciliation calls belong to that provider implementation, not to the Host
Agent dispatcher or the cloud-cell recipe.

Intent: prevent the known failure mode where Helm is applied before CNPG,
SQL-gated readiness, and the `opute-platform-db` consumer Secret exist. The
recipe must preserve the distinction between:

- Host-native prerequisite metadata
- CNPG operator readiness
- SQL readiness
- Consumer Secret projection
- Platform/task-ledger consumer readiness

The recipe must not accept Kubernetes Deployment readiness alone as proof that
the database contract is complete.

### 5. Verified platform artifact and rendering

**Nodes:** `platform-artifact`, `platform-extract`, `platform-render`

- `platform-artifact` calls `ensure_host_artifact` with an HTTPS URI and
  required SHA-256. Validate the installed artifact with
  `inspect_host_file` and the same expected digest.
- `platform-extract` calls `extract_host_archive` into a Host Agent-owned
  directory. Validate that the expected chart can be rendered.
- `platform-render` calls `render_helm_template` with the explicit chart path,
  release name, namespace, values files, and set values.

Intent: make the exact platform artifact auditable and keep all chart
rendering on the Host Agent side while still using generic capabilities. The
render output must have a typed schema containing `manifest`, so the plan can
reference `${nodes.platform-render.output.manifest}` without bypassing plan
validation.

If direct tenant-policy manifests are accepted as inputs, add a digest-aware
manifest application contract. Do not treat an arbitrary un-hashed string as
an immutable production policy artifact.

### 6. Platform authentication material

**Node:** `platform-secrets`

Call `put_k8s_secret` with the explicit platform namespace, Secret name, and
secret data. Validate with `get_k8s_resource` by asserting that the Secret
resource exists, without returning its data.

Intent: place the platform’s configured identity/authentication material
through the Host Agent’s credential-bearing MCP capability while keeping the
policy decision in Platform. The recipe may install a Secret; it must not
decide which user or tenant is authorized.

### 7. Platform control plane

**Node:** `platform-apply`

Call `apply_manifest` using the typed rendered manifest output from
`platform-render`. Validate one or more required Deployments with
`get_k8s_resource_status` and assert readiness for:

- Platform API/control plane
- Web/control-plane UI
- Platform MCP endpoint
- Task ledger

Intent: ensure the platform is not considered installed because a manifest was
accepted. It is installed only when the critical consumers are ready.

The operator recipe may use this broad mutation capability. The end-user
application path must not inherit it.

### 8. Tenant boundary baseline

**Node:** `tenant-boundary`

Apply the platform-supplied tenant baseline through a verified, explicit
manifest or a future typed tenant-boundary capability. Validate a marker
resource with `get_k8s_resource`.

The baseline is expected to establish the platform-owned mechanism for:

- Tenant namespace creation
- Tenant-scoped RoleBindings and ServiceAccounts
- Default-deny NetworkPolicies
- ResourceQuota and LimitRange defaults
- Service-to-service authentication hooks
- Separation between tenant workloads and platform namespaces
- Protection of Host Agent and control-plane credentials

Intent: prove that the cell has an isolation foundation before declaring it
ready for end users. The marker must be created only by the tenant-boundary
installation path; a generic pre-existing ConfigMap must not be enough.

### 9. Public edge and endpoint evidence

**Nodes:** `edge`, `platform-probe`, `mcp-probe`

- `edge` calls the canonical edge-provider operation for the selected
  `opute.capability.edge.v1` binding with the secret token and explicit local
  service targets. The v1 binding may be implemented by the existing
  Cloudflare provider; the recipe must not call a Cloudflare-specific core
  operation. Validate its Deployment readiness.
- `platform-probe` calls `probe_http_endpoint` against the platform endpoint.
- `mcp-probe` calls `probe_http_endpoint` against the MCP endpoint.

Intent: separate four claims that are often incorrectly collapsed:

1. The connector Deployment exists.
2. The connector is ready.
3. The public hostname reaches the intended service.
4. The authenticated MCP protocol can be initialized and used.

The final production acceptance should add an authenticated MCP canary outside
the recipe-runner’s generic HTTP probe when the Host Agent has a typed MCP
probe capability. A plain HTTP 200 is not proof of MCP or chat readiness.

### 10. Final cell gate

**Node:** `cell-ready`

Read back the key statuses and publish the output mapping only after every
required dependency is ready. If an optional edge profile is supported, the
recipe must make the edge requirement explicit in the input contract and the
final gate must branch only on a typed `enableEdge` input.

Intent: make “ready” a durable, reproducible claim rather than a client-side
interpretation of partial task output.

## MCP surface and implementation changes

### Recipe package

Add a cloud-cell family in `internal/recipe`, reusing:

- `SourceRequest` and source loading from `internal/recipe/source.go`
- input resolution and JSON-schema validation
- canonical recipe hashing
- redacted input and secret-name reporting
- compatibility and required-tool checks
- nested-plan rejection

Add cloud-cell-specific validation for:

- `cloud-cell-recipe.v1`
- single-node topology
- supported target kind
- required readiness/output mapping
- provider-binding capability/version/provider identity compatibility
- no runtime activation block
- bounded node count and tool surface

### Host MCP server

Add handlers alongside the existing runtime/tunnel recipe handlers:

- `loadCloudCellRecipe`
- `handleValidateCloudCellRecipe`
- `handleRunCloudCellRecipe`
- `handleGetCloudCellRecipeRun`
- cloud-cell recipe metadata persistence and redaction

The run path must use the existing durable host-plan storage and execution
path. Resume must use persisted plan and recipe metadata, preserve source and
catalog provenance, and reject redacted secret values exactly as the current
runtime recipe path does.

Validation must have no mutation side effects. Execution must return a durable
run ID and allow status polling and resume through MCP.

### Provider ports and concrete implementations

Add the neutral provider contracts before changing the cloud-cell dispatcher:

- `contracts/capability/capability.go` and versioned schemas for
  `opute.capability.kubernetes.v1`, `opute.capability.compute.v1`,
  `opute.capability.container-registry.v1`, and
  `opute.capability.postgresql-service.v1`;
- canonical operation schemas for provision/status/configure/remove behavior,
  including effect, idempotency, resource kinds, readiness, cancellation,
  redaction, and bounded observations;
- provider-binding metadata that selects an authorized provider generation by
  capability ID and version without putting provider names into core dispatch;
- conformance fixtures that implement the Kubernetes port twice: a K3s-shaped
  provider and a fake MicroK8s-shaped provider. The cloud-cell plan must run
  against either fixture without changing Host Agent source or canonical
  operation IDs.

Add `plugins/compute/incus` as an independently buildable Streamable HTTP MCP
provider. It must implement the compute port for both virtual machines and
system containers, including host preparation/status, provision, status,
start/stop/restart, bounded resource updates, remove, and scoped exec. Move
the current Incus image, profile, QEMU, system-container, device, autostart,
ownership, guest-agent, and status-parsing behavior from the current `ops`
seams into this provider or into generic typed primitives that the provider
composes. The provider manifest must declare `opute.capability.compute.v1`,
the canonical operation schemas, supported resource kinds, and URI-bearing
result schemas.

The Incus provider conformance fixture must run the same test matrix for
`targetKind: virtual-machine` and `targetKind: system-container`. It must
prove that a fake compute provider can replace Incus through a provider
binding without modifying Host Agent dispatch, recipe interpolation, or
canonical operation IDs. The production cell binds compute to Incus, while a
future provider can implement the same port for another VM/container backend.

Add `plugins/kubernetes/k3s` as an independently buildable Streamable HTTP MCP
provider. Move K3s-specific installer arguments, artifact pinning, host/VM/
container target behavior, service repair, status parsing, registry
configuration, and uninstall behavior into that package. Its manifest must
declare `opute.capability.kubernetes.v1` and the canonical operation schemas.
The provider may use typed provider-native execution, but it must never accept
arbitrary caller shell text or bypass Host Agent authorization and durable
operation tracking.

The Host Agent changes are generic only:

- extend `internal/cordis/mcp` and provider lifecycle registration so a
  canonical capability can be bound to one active provider generation;
- make `internal/catalog` validate capability-port compatibility and reject
  conflicting or ambiguous bindings;
- remove K3s-specific cases from `internal/tools/dispatch.go` and K3s-specific
  installer behavior from `internal/ops/service.go` after the provider is
  proven equivalent;
- remove Incus-specific VM/container selection from
  `internal/tools/dispatch.go` and the Incus lifecycle authority from
  `internal/ops/service.go` / `internal/ops/incus_container.go` after the
  compute provider is proven equivalent. Generic Host Agent code may retain
  typed URI resolution, provider-neutral admission, and bounded execution
  primitives;
- carry tenant context and canonical URI validation into provider dispatch.
  Every entity-scoped operation resolves `uri` through the tenant-aware
  resource registry; a raw `vmName`, `containerName`, or service name cannot
  select a resource in the final catalog.
- retain generic Kubernetes API tools and generic host primitives in the core,
  with explicit target/cluster references and no vendor assumptions;
- add atomic swap, drain, rollback, and evidence tests for K3s to fake
  MicroK8s provider generations.

The same provider contract is then applied in waves to local registry,
CNPG/PostgreSQL, and edge implementations. Incus is part of the foundational
provider milestone above, not a deferred exception. Each migration must delete
the backend-specific core dispatch path after the provider path is certified;
it must not leave two independently authoritative implementations.

### Catalog and typed capability contracts

Update the Host Agent catalog and schemas so the plan is statically valid:

- Add cloud-cell recipe MCP tools to the standalone catalog and dispatch.
- Add the reserved session/plan `tenantId` context, canonical URI schemas,
  the `service` resource kind, and resource-registry resolver to recipe and
  provider dispatch contracts. Creation operations may accept a desired name,
  but output schemas must include `uri`; retrieval and lifecycle schemas must
  accept the URI and reject foreign-tenant references.
- Add the canonical Kubernetes port operations to the provider manifest and
  catalog overlay; do not add `install_k3s`, `get_k3s_status`, or
  `configure_k3s_registry` as permanent Host Agent core operations.
- Add the canonical compute port operations and bind them to the Incus
  provider in the first-party recipe; do not add permanent `provision_vm`,
  `get_vm_info`, or container-name-only lifecycle routes to the final core
  catalog.
- Keep the cloud-cell recipe’s generic Kubernetes API operations separate from
  the provider-owned control-plane lifecycle operations.
- Add the `render_helm_template` output schema, including the string
  `manifest` field and release/chart identity.
- Add the operator-scoped `apply_verified_manifest` capability for direct
  policy manifests. It must verify the supplied SHA-256 before delegating to
  the existing generic Kubernetes application primitive.
- Persist successful action validation results in `NodeRunState.Observed`, so
  recipe output mappings can reference `nodes.<id>.observed.<field>` without
  adding a second validation-only node for the same readiness check. Add a
  focused runner test for this durable observation contract.
- Keep `get_operation` and other internal task primitives out of the public
  recipe plan unless they are intentionally part of the typed recipe contract;
  the plan runner should own operation waiting through normal dispatch.

Every new capability must declare its effect, idempotency, input schema,
output schema, approval classification, and whether it is available in the
standalone catalog.

Provider implementations must also declare their capability version,
implementation version, provider recipe sources and hashes, validation
operation, dependencies, and readiness evidence. A provider that only exposes
metadata or a reachable MCP endpoint is not an active implementation.

### First-party recipe artifact

Add the recipe under a first-party location such as:

`/home/houman/github/wunderous/opute-host-agent/recipes/single-node-private-cloud.yaml`

The recipe must include:

- Pinned contract and recipe versions
- Required tool list
- Input schemas and descriptions
- Secret flags
- Explicit plan generation and idempotency key
- Explicit capability/provider bindings for replaceable backends
- Dependency graph and readiness validations
- Output mapping
- No nested `run_host_plan` or recipe execution
- No direct shell, kubectl, Helm, Incus, or vendor API actions in the client or
  recipe; backend-specific work is reached only through an authorized
  canonical provider operation or a generic Host Agent capability.

The release packaging path must ship the recipe without following symlinks or
silently substituting a mutable working-tree source.

## Failure, recovery, and security behavior

### Failure boundaries

The recipe must stop at the first unsatisfied readiness boundary and retain:

- Node status
- Attempts
- Last observed readiness output
- Expected assertion
- Error text with secrets redacted
- Source/recipe hash
- Catalog revision
- Capability contract, provider identity, provider generation, and binding
  transition
- Run ID and timestamps

No later node may run after a required predecessor fails. A failed run may be
resumed only from persisted state after the target is rechecked; it must not
blindly repeat destructive or credential-bearing work.

### Idempotency

The plan idempotency key must include the cell identity, recipe version, and
immutable artifact identity. It must not contain raw secrets. Re-running the
same recipe against the same cell should converge rather than create a second
VM, registry, database, or edge connector.

### Secret handling

- Mark all tokens and credential values as secret inputs.
- Never include secret values in structured output, task text, logs, errors,
  plan snapshots, or endpoint URLs.
- Keep CNPG-generated credentials operator-owned.
- Do not return Kubernetes Secret data from readiness probes.
- Require explicit secret references or re-supply on resume when persisted
  state intentionally contains only redacted values.

### Isolation and admission

The recipe must execute only after the Host Agent’s normal authorization,
catalog revision, resource admission, and ownership checks succeed. It must
not create a hidden second mutation path through a generic command runner.

VM identity, cell identity, owner identity, and artifact identity must remain
explicit throughout the plan. No name-only inference may select a target.

Provider identity and generation must remain explicit as well. A disconnected,
stale, unauthorized, or schema-incompatible provider is unavailable; the Host
Agent must not silently route the operation to another backend.

## Beads milestone graph and coordination gates

Before implementation begins, materialize this plan as a dependency graph in
the shared Beads ledger. The graph is the execution coordination artifact; the
Markdown plan remains the architectural source of intent and acceptance
criteria. This prevents the provider, URI, recipe, and packaging work from
being started out of order or being declared complete from focused tests alone.

### Ledger boundary

Use the repository's `scripts/agent-work` launcher for the session record and
the pinned Beads client against the existing Windows-backed Dolt workspace.
Never run `bd init`, `bd dolt start`, or create `.beads` in this checkout or in
WSL. Before creating graph records:

```bash
./scripts/agent-work status --json --all
./scripts/agent-work start \
  --title="Implement single-node private-cloud cell recipe" \
  --touches="internal/recipe,internal/plan,internal/hostmcp,internal/ops,internal/tools,internal/state,internal/cordis,contracts,plugins,recipes,docs" \
  --may-affect="provider ports, tenant URI resolution, shared Beads milestone graph" \
  --next="materialize the cloud-cell milestone graph and validate its gates"
```

The returned session record is not a substitute for the milestone graph. It
tracks the coherent agent session; the graph below tracks implementation
milestones and sequencing. If the launcher or shared ledger cannot be read,
record the work as untracked and stop before creating a competing ledger.

### Milestone DAG

Create one root epic and the child milestones below. Use stable titles/spec
references so re-running graph setup can detect an existing graph rather than
creating duplicates. Create the records with `bd create` (or one reviewed
`bd create --graph` input), then add dependencies with `bd dep add
<blocked> <blocker>`. The arrow direction below is **blocker → blocked**.

| Logical milestone | Scope and completion gate | Dependencies |
| --- | --- | --- |
| `cloud-cell-foundation` | Root epic for this plan; closes only after every required child milestone and the final cell acceptance gate pass | none |
| `cloud-cell-contracts-and-tenant-uri` | `cloud-cell-recipe.v1`, `resourceid`, session tenant binding, registry/resolver, `service` URI kind, schemas, and foreign-tenant tests | none |
| `cloud-cell-provider-kernel` | Generic provider manifest, generation binding, catalog, adapter lifecycle, authorization, drain/swap/rollback, and provider conformance harness | `cloud-cell-contracts-and-tenant-uri` |
| `cloud-cell-incus-compute` | `plugins/compute/incus` implements VM and system-container operations with URI-bearing results and no Incus branch in core dispatch | `cloud-cell-provider-kernel` |
| `cloud-cell-k3s-provider` | `plugins/kubernetes/k3s` satisfies the Kubernetes port and removes K3s-specific core routing after parity | `cloud-cell-provider-kernel` |
| `cloud-cell-service-edge-providers` | Registry, PostgreSQL/CNPG, and edge provider bindings/adapters preserve SQL, Secret, and public-edge evidence | `cloud-cell-provider-kernel` |
| `cloud-cell-recipe-artifact` | First-party recipe, MCP handlers, URI interpolation, provider bindings, DAG validation, and manifest evidence | `cloud-cell-incus-compute`, `cloud-cell-k3s-provider`, `cloud-cell-service-edge-providers` |
| `cloud-cell-durable-execution` | Durable run/status/resume, redaction, catalog revision, retry/recovery, and persisted observations | `cloud-cell-recipe-artifact` |
| `cloud-cell-packaging-and-acceptance` | Packaged recipe, standalone checks, complete acceptance matrix, and operator documentation | `cloud-cell-durable-execution` |

The root epic is a parent-child relationship, not a sequencing dependency on
every child. Use `blocks` only for the dependencies in the table; use
`related` for adjacent design work such as the URI plan or future HA work.
The service/edge provider milestone may proceed in parallel with the Incus and
K3s provider milestones after the provider kernel gate passes.

### Graph creation and validation procedure

1. Read the shared ledger and confirm the repository/project identity, active
   native Windows Dolt endpoint, and pinned client versions. Capture the
   session record ID and root epic ID in the implementation notes.
2. Create the root epic and child milestones with descriptions containing
   `Scope`, `Dependencies`, `Acceptance Criteria`, and `Validation Evidence`.
   Include this plan path and the adjacent URI plan as specification references.
3. Add every `blocks` edge from the table. Do not hand-edit dependency fields
   or use `related` where work cannot safely start before another milestone.
4. Run the graph integrity gates before any implementation milestone starts:

   ```bash
   bd graph check
   bd dep cycles
   bd lint --status all
   bd graph <root-epic-id> --compact
   bd ready --json
   ./scripts/agent-work status --json --all
   bd dolt test
   test ! -e .beads
   ```

   The graph must be acyclic, every child must be reachable from the root,
   every task must have an acceptance section, and `bd ready --json` must show
   only the intended root milestones with no unmet dependency incorrectly
   marked executable. A Beads/Dolt connectivity check alone is not sufficient
   evidence; the launcher status and graph read must both succeed.
5. At each milestone boundary, update the Beads issue with implementation
   status, next milestone, exact validation command, redacted evidence path,
   and current commit. Close a milestone only after its gate passes and all
   dependent work can consume its declared outputs. If validation fails, keep
   the issue open or blocked, record the failure, and do not unlock dependents.
6. Before closing the root epic, re-run `bd graph check`, `bd lint`, the full
   acceptance validation, and the shared-ledger status check. End the session
   record with the final validation command and reason; do not prune graph
   history as part of this work.

### Milestone validation gates

The following gates are the minimum evidence recorded on the corresponding
Beads milestone. The same gates are expanded in the implementation phases
below; the graph is not allowed to weaken them:

| Gate | Required evidence before closing |
| --- | --- |
| `G0-ledger` | Shared launcher status, `bd dolt test`, `bd graph check`, `bd dep cycles`, `bd lint`, no checkout-local `.beads`, and a readable root graph |
| `G1-contracts` | Recipe/contract tests, URI parser and registry tests, session tenant tests, and proof that foreign-tenant URIs fail before dispatch |
| `G2-provider-kernel` | Provider manifest/schema compatibility, authorized generation binding, catalog revision behavior, conformance harness, and drain/rollback tests |
| `G3-compute-kubernetes` | Incus VM plus system-container conformance, K3s provider tests, fake-provider swaps, and no concrete-provider imports in Host Agent core |
| `G4-service-edge` | Registry/CNPG/edge provider tests, SQL-gated readiness, consumer Secret projection, and independent edge reachability evidence |
| `G5-recipe` | Final YAML/static graph validation, all action readiness validators, typed URI interpolation, provider-binding checks, and no legacy backend tool in the final manifest |
| `G6-durable` | Resume/retry/recovery tests, persisted observations, secret redaction, catalog-revision rejection, and stop-on-failure evidence |
| `G7-release` | Build, standalone smoke tests, complete acceptance criteria, packaged recipe validation, and operator documentation review |

## Implementation phases and proof

### Phase 0: Materialize and validate the Beads milestone graph

**Files and systems:** the shared Beads/Dolt ledger, the session record from
`scripts/agent-work`, this plan, the adjacent URI plan, and the planned graph
definition artifact `docs/beads/cloud-cell-milestones.json` if the team elects
to keep a reviewed graph source in the repository.

**Work:** Create the root epic, child milestones, dependency edges, acceptance
sections, and validation-gate metadata described above. Confirm that the graph
maps to the implementation phases and their proof gates, and that the only
parallel work is explicitly safe to parallelize. Do not create
repository-local Beads state.

**Proof:** `G0-ledger` passes. In particular, `bd graph <root> --compact`
shows the expected DAG, `bd dep cycles` is empty, `bd lint --status all`
passes, and the ready set contains no milestone whose blocker is incomplete.

### Phase 1: Contract, tenant context, and source machinery

**Files and symbols:** `internal/recipe/*`, new cloud-cell recipe types,
`internal/hostmcp/server.go`, `internal/hostmcp/session.go`,
`internal/session/contract.go`, new `internal/resourceid/*`, resource registry
state, resolver, and recipe dispatch registration.

**Work:** Add the envelope, source loading reuse, input resolution, validation,
metadata redaction, MCP tool contracts, session-carried tenant context,
canonical resource URI parsing, registry-backed resolution, and URI-bearing
entity output schemas. Extend the adjacent URI plan's resource-kind vocabulary
with tenant application `service` and reserve `tenantId` as runner state.

**Proof:**

```bash
go test ./internal/recipe ./internal/hostmcp
go test ./test/contract
```

Assert that validation resolves a local recipe, rejects wrong contract
versions and missing inputs, redacts secrets, and performs no host mutation.
Assert that a foreign-tenant URI is rejected before dispatch and that
`service:tenant-a:blog` and `service:tenant-b:blog` remain distinct registry
records.

### Phase 2: Provider ports, Incus compute, and K3s concrete implementations

**Files and symbols:** `contracts/capability/*`, new Kubernetes schemas,
`plugins/compute/incus/*`, `plugins/kubernetes/k3s/*`,
`internal/cordis/mcp/*`, `internal/catalog/*`,
`internal/hostmcp/*`, `internal/tools/dispatch.go`, `internal/ops/service.go`,
`internal/ops/incus_container.go`, and provider conformance fixtures.

**Work:** Define `opute.capability.compute.v1` and
`opute.capability.kubernetes.v1`, implement the Incus compute provider for
both VMs and system containers plus the K3s provider, bind both through the
generic provider lifecycle, and remove backend-specific core dispatch once
parity is proven. Keep generic Kubernetes API operations in the Host Agent. In
the same phase, publish the Helm `manifest` output and add any required
digest-aware manifest contract.

**Proof:**

```bash
go test ./contracts/... ./internal/cordis/... ./internal/catalog ./internal/hostmcp ./internal/tools ./internal/ops
go test ./plugins/kubernetes/k3s/...
go test ./plugins/compute/incus/...
go vet ./...
```

Assert that the cloud-cell canonical provision/status operations are served by
the selected providers, that the provider manifests are schema-compatible,
that Incus conformance covers both VM and system-container targets, that fake
MicroK8s and fake compute providers can satisfy the same ports, that no Host
Agent source imports a concrete provider package, and that the catalog
describes the Helm `manifest` output.

### Phase 3: Recipe plan and first-party artifact

**Files and symbols:** `recipes/single-node-private-cloud.yaml`, recipe
fixtures, output mapping and plan validation tests.

**Work:** Encode the ordered DAG described above with explicit inputs,
readiness assertions, bounded retries, convergence, and provenance.

**Proof:**

```bash
go test ./internal/recipe ./internal/plan ./test/contract
```

Assert that the actual Host Agent catalog accepts every canonical provider
operation and generic Kubernetes action, all mutating nodes have read-only
validation, all interpolation references are typed URI references,
provider bindings are authorized and version-compatible, tenant context is
runner-provided, and nested plan execution is rejected.

### Phase 4: Durable execution and resume

**Files and symbols:** `internal/hostmcp/*recipe_run.go`,
`internal/hostmcp/plan_run.go`, state persistence, server contract tests.

**Work:** Persist cloud-cell recipe provenance and expanded plan state, expose
status/resume, and maintain redaction and catalog-revision checks.

**Proof:**

```bash
go test ./internal/hostmcp ./internal/state ./internal/plan
```

Use a fake dispatcher to prove dependency order, polling, stop-on-failure,
resume after interruption, and refusal to execute persisted redacted secrets.

### Phase 5: Packaging and operational documentation

**Files and symbols:** release packaging, standalone contract tests, README or
cloud-cell runbook, recipe source documentation.

**Work:** Ship the recipe with the Host Agent distribution, document required
inputs and artifact layout, and document that validation precedes execution.

**Proof:**

```bash
make build
make standalone-smoke
make standalone-http-smoke
go test ./...
```

The packaged binary must list the cloud-cell recipe tools, validate the
first-party recipe, and preserve the existing standalone isolation tests.

## Acceptance criteria

The implementation is complete only when all of these are true. Before any
implementation milestone starts, the shared Beads graph must also pass `G0`
and remain aligned with the phase gates below:

1. A clean Host Agent can validate the pinned cloud-cell recipe through MCP.
2. A recipe run performs all target operations through Host Agent MCP tool
   dispatch; no client-side SSH, Incus, Helm, kubectl, or direct Kubernetes
   mutation is required after the Host Agent endpoint exists.
3. The run is durable, resumable, catalog-revision-bound, and secret-redacted.
4. The Kubernetes control-plane port is satisfied by the K3s provider without
   K3s-specific operation IDs or flag arrays in the Host Agent core or recipe.
5. A fake MicroK8s provider can satisfy the same port and replace the K3s
   generation through an explicit, drained, evidenced binding change without a
   Host Agent code change.
6. The compute port is satisfied by the Incus provider for both VM and system-
   container resource kinds, and a fake compute provider can replace Incus
   through composition without a Host Agent code change.
7. The active tenant context and resource registry resolve canonical URIs before
   any provider call; foreign-tenant URIs fail closed.
8. Two tenants may each own `service:<tenant>:blog`, while a typed rename
   operation can select only the authenticated tenant's URI and never a
   name-only or first-match resource.
9. K3s version, single-node topology, embedded-etcd initialization, and
   secrets encryption are explicit and observable through the neutral port.
10. CNPG SQL readiness and consumer-Secret readiness gate platform deployment.
11. Platform authentication material is installed without exposing its values.
12. Platform control-plane deployments are individually ready before success.
13. Tenant-isolation baseline installation has its own marker and evidence.
14. Public edge readiness is not confused with connector process readiness.
15. The final output maps to verifiable cell-ready evidence rather than a
   client-side boolean assembled from partial results.
16. No end-user receives arbitrary manifest authority from this bootstrap
   recipe.
17. A later HA recipe can reuse the same Host Agent and Platform boundaries
   without pretending that the single-node recipe is highly available.

## Assumptions and open implementation constraints

- The Host Agent MCP endpoint and its authorization are pre-installed or
  bootstrapped through a separate machine-management workflow. A recipe cannot
  call MCP before an MCP server exists.
- The platform chart archive is produced and published by a trusted release
  process, and its SHA-256 is supplied with the recipe invocation.
- The target machine has enough CPU, memory, disk, and network access for the
  selected compute target, Kubernetes provider, registry, database, and
  platform workloads.
- The first production profile prefers a VM. A container profile is a separate
  explicitly named profile, not an implicit fallback.
- Provider MCPs are trusted first-party artifacts selected through pinned
  descriptors and provider manifests. The cloud-cell recipe does not download
  or execute arbitrary provider code.
- Platform chart values and auth configuration must identify the required
  platform, web, MCP, task-ledger, and tenant-boundary resources. The Host
  Agent should validate generic resource readiness, not infer product topology
  from names outside the recipe’s explicit inputs.
- The recipe will not claim end-user readiness until the tenant-boundary
  marker is created by the intended policy installation path.
- Runtime chat canaries and authenticated application-tool traces remain
  separate acceptance layers. A successful cloud-cell recipe is necessary but
  not sufficient evidence that Opute chat is healthy.

## Appendix A: final first-party recipe manifest

The following is the target manifest to add as
`recipes/single-node-private-cloud.yaml`. It is intentionally shown in the
plan so implementation can be reviewed against one concrete artifact. The
manifest is not executable until the `cloud-cell-recipe.v1` MCP handlers,
catalog entries, and typed capability gaps described above are implemented.

The manifest uses only Host Agent capabilities. The client supplies the source
recipe and input values through MCP; it does not run any of the commands
represented by the plan itself.

```yaml
contractVersion: cloud-cell-recipe.v1
recipeId: first-party-single-node-private-cloud
recipeVersion: 1.0.0

cell:
  topology: single-node
  targetKind: virtual-machine
  capabilities:
    - compute
    - kubernetes
    - private-registry
    - sql-service
    - platform-control-plane
    - tenant-isolation-baseline
    - http-ingress

providerBindings:
  compute:
    capability: opute.capability.compute.v1
    providerId: com.opute.incus
  kubernetes:
    capability: opute.capability.kubernetes.v1
    providerId: com.opute.k3s
  containerRegistry:
    capability: opute.capability.container-registry.v1
    providerId: com.opute.registry.local
  postgresqlService:
    capability: opute.capability.postgresql-service.v1
    providerId: com.opute.cnpg
  edge:
    capability: opute.capability.edge.v1
    providerId: com.opute.cloudflare

tenantContext:
  source: authenticated-session
  reservedVariable: tenantId
  requireSessionBinding: true

compatibility:
  minHostAgentVersion: 0.1.0
  requiredTools:
    - opute.provider.status
    - check_local_prerequisites
    - get_local_status
    - opute.capability.compute.prepare-host
    - opute.capability.compute.host-status
    - opute.capability.compute.provision
    - opute.capability.compute.status
    - opute.capability.kubernetes.provision
    - opute.capability.kubernetes.status
    - opute.capability.container-registry.provision
    - opute.capability.container-registry.status
    - get_k8s_resource_status
    - opute.capability.kubernetes.configure-registry
    - opute.capability.postgresql-service.reconcile
    - opute.capability.postgresql-service.status
    - ensure_host_artifact
    - inspect_host_file
    - extract_host_archive
    - render_helm_template
    - put_k8s_secret
    - get_k8s_resource
    - apply_manifest
    - apply_verified_manifest
    - opute.capability.edge.provision
    - opute.capability.edge.status
    - probe_http_endpoint

inputs:
  cellId:
    required: true
    description: Stable Opute cell identity; it is never inferred from a VM name.
    schema:
      type: string
      minLength: 1
      maxLength: 63
      pattern: '^[a-z][a-z0-9-]*[a-z0-9]$'

  vmName:
    required: true
    description: Dedicated Incus VM name for this cloud cell.
    schema:
      type: string
      minLength: 1
      maxLength: 63
      pattern: '^[a-z][a-z0-9-]*[a-z0-9]$'

  vmImage:
    required: true
    description: Explicit Incus image alias or immutable image reference.
    schema:
      type: string
      minLength: 1
      maxLength: 256

  vmCpus:
    default: 4
    schema:
      type: integer
      minimum: 2
      maximum: 64

  vmMemory:
    default: 8GiB
    schema:
      type: string
      pattern: '^[1-9][0-9]*(MiB|GiB)$'

  vmDisk:
    default: 80GiB
    schema:
      type: string
      pattern: '^[1-9][0-9]*(GiB|TiB)$'

  kubernetesVersion:
    default: v1.31.8+k3s1
    description: Provider-compatible pinned Kubernetes release; channel aliases are not accepted.
    schema:
      type: string
      pattern: '^[A-Za-z0-9][A-Za-z0-9._+:-]{0,63}$'

  kubernetesTopology:
    default: single-node
    schema:
      type: string
      enum: [single-node]

  kubernetesNodeRole:
    default: server
    schema:
      type: string
      enum: [server]

  kubernetesClusterInit:
    default: true
    schema:
      type: boolean

  kubernetesSecretsEncryption:
    default: true
    schema:
      type: boolean

  registryNamespace:
    default: registry-system
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  registryName:
    default: local-registry
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  registryStorageSize:
    default: 40Gi
    schema:
      type: string
      pattern: '^[1-9][0-9]*(Mi|Gi|Ti)$'

  registryNodePort:
    default: 30500
    schema:
      type: integer
      minimum: 30000
      maximum: 32767

  registryEndpoint:
    required: true
    description: Registry endpoint reachable from the K3s VM, including scheme.
    schema:
      type: string
      format: uri
      pattern: '^https?://[^/]+$'

  registryHost:
    required: true
    description: Registry host key used by K3s registries.yaml.
    schema:
      type: string
      minLength: 1
      maxLength: 253

  registryInsecure:
    default: false
    schema:
      type: boolean

  databaseNamespace:
    default: opute-platform
    description: Namespace containing the platform CNPG service.
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  databaseClusterName:
    default: opute-platform-db
    schema:
      type: string
      pattern: '^[a-z][a-z0-9-]*[a-z0-9]$'

  databaseNames:
    default:
      - platform
      - task_ledger
    schema:
      type: array
      minItems: 2
      items:
        type: string
        pattern: '^[a-z][a-z0-9_-]*$'

  databaseStorageSize:
    default: 20Gi
    schema:
      type: string
      pattern: '^[1-9][0-9]*(Mi|Gi|Ti)$'

  databaseConsumerSecretName:
    default: opute-platform-db
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  platformNamespace:
    default: opute-platform
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  platformArtifactUri:
    required: true
    description: HTTPS tar.zst archive containing the platform Helm chart.
    schema:
      type: string
      format: uri
      pattern: '^https://.+$'

  platformArtifactSha256:
    required: true
    description: SHA-256 digest of the exact platform chart archive.
    schema:
      type: string
      pattern: '^(sha256:)?[0-9a-fA-F]{64}$'

  platformArtifactPath:
    default: .cache/opute/cloud-cell/platform-chart.tar.zst
    schema:
      type: string
      pattern: '^[^/].*$'

  platformExtractPath:
    default: .cache/opute/cloud-cell/platform-chart
    schema:
      type: string
      pattern: '^[^/].*$'

  platformChartPath:
    default: .cache/opute/cloud-cell/platform-chart/chart
    schema:
      type: string
      pattern: '^[^/].*$'

  platformReleaseName:
    default: opute-platform
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  platformValuesFiles:
    default: []
    schema:
      type: array
      items:
        type: string
        pattern: '^[^/].*$'

  platformHelmSet:
    default: []
    schema:
      type: array
      items:
        type: string
        minLength: 1

  platformAuthSecretName:
    default: opute-platform-auth
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  platformAuthSecretData:
    required: true
    secret: true
    description: Platform identity/authentication material; never returned.
    schema:
      type: object
      minProperties: 1
      additionalProperties:
        type: string
        minLength: 1

  platformApiDeployment:
    default: platform-opute-platform
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  platformWebDeployment:
    default: platform-opute-web
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  platformMcpDeployment:
    default: platform-opute-mcp
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  taskLedgerDeployment:
    default: platform-opute-task-ledger
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  tenantBoundaryManifest:
    required: true
    description: Rendered tenant-isolation baseline manifest supplied by Platform.
    schema:
      type: string
      minLength: 1

  tenantBoundaryManifestSha256:
    required: true
    description: Digest verified before the tenant baseline is applied.
    schema:
      type: string
      pattern: '^(sha256:)?[0-9a-fA-F]{64}$'

  tenantBoundaryMarkerKind:
    default: ConfigMap
    schema:
      type: string
      const: ConfigMap

  tenantBoundaryMarkerName:
    default: tenant-boundary-ready
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  cloudflaredToken:
    required: true
    secret: true
    description: Cloudflare connector token; never returned or logged.
    schema:
      type: string
      minLength: 1

  edgeNamespace:
    default: edge-system
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  edgeName:
    default: cloudflared
    schema:
      type: string
      pattern: '^[a-z0-9]([-a-z0-9]*[a-z0-9])?$'

  edgeLocalTargets:
    default:
      - localPort: 9190
        target: platform-opute-web.opute-platform.svc.cluster.local:9090
      - localPort: 9191
        target: platform-opute-mcp.opute-platform.svc.cluster.local:9091
    schema:
      type: array
      minItems: 2
      items:
        type: object
        required: [localPort, target]
        properties:
          localPort:
            type: integer
            minimum: 1
            maximum: 65535
          target:
            type: string
            minLength: 1

  platformEndpoint:
    required: true
    schema:
      type: string
      format: uri

  mcpEndpoint:
    required: true
    schema:
      type: string
      format: uri

plan:
  contractVersion: host-plan.v1
  planId: first-party-single-node-private-cloud
  generation: 1
  idempotencyKey: >-
    cloud-cell-${vars.tenantId}-${vars.inputs.cellId}-${vars.inputs.vmName}-
    ${vars.inputs.kubernetesVersion}-${vars.inputs.platformArtifactSha256}
  defaults:
    timeoutMs: 120000
    retry:
      maxAttempts: 1
      backoffMs: 1000
  converge:
    maxPasses: 3
    abortOnExhaustion: true

  nodes:
    - id: preflight
      action:
        tool: check_local_prerequisites
        args: {}
      validate:
        tool: get_local_status
        args: {}

    - id: incus
      dependsOn: [preflight]
      action:
        tool: opute.capability.compute.prepare-host
        args:
          providerScope: host
          channel: stable
          enableVirtualMachines: true
          enableSystemContainers: true
      validate:
        tool: opute.capability.compute.host-status
        args: {}
        assert:
          - path: /providerReady
            op: eq
            value: true

    - id: cell-vm
      dependsOn: [incus]
      action:
        tool: opute.capability.compute.provision
        args:
          resourceType: vm
          targetKind: virtual-machine
          resourceId: ${vars.inputs.vmName}
          image: ${vars.inputs.vmImage}
          cpus: ${vars.inputs.vmCpus}
          memory: ${vars.inputs.vmMemory}
          disk: ${vars.inputs.vmDisk}
      validate:
        tool: opute.capability.compute.status
        args:
          uri: ${nodes.cell-vm.output.uri}
        assert:
          - path: /agentReady
            op: eq
            value: true

    - id: kubernetes
      dependsOn: [cell-vm]
      action:
        tool: opute.capability.kubernetes.provision
        args:
          targetRef: ${nodes.cell-vm.output.uri}
          targetKind: virtual-machine
          clusterId: ${vars.inputs.cellId}
          version: ${vars.inputs.kubernetesVersion}
          topology: ${vars.inputs.kubernetesTopology}
          nodeRole: ${vars.inputs.kubernetesNodeRole}
          clusterInit: ${vars.inputs.kubernetesClusterInit}
          secretsEncryption: ${vars.inputs.kubernetesSecretsEncryption}
      validate:
        tool: opute.capability.kubernetes.status
        args:
          targetRef: ${nodes.cell-vm.output.uri}
          clusterId: ${vars.inputs.cellId}
        assert:
          - path: /status
            op: eq
            value: ready
          - path: /totalNodes
            op: eq
            value: 1
        timeoutMs: 600000
        pollIntervalMs: 2000
      timeoutMs: 900000

    - id: registry
      dependsOn: [kubernetes]
      action:
        tool: opute.capability.container-registry.provision
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          namespace: ${vars.inputs.registryNamespace}
          name: ${vars.inputs.registryName}
          storageSize: ${vars.inputs.registryStorageSize}
          nodePort: ${vars.inputs.registryNodePort}
      validate:
        tool: opute.capability.container-registry.status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          resourceKind: Deployment
          resourceName: ${vars.inputs.registryName}
          namespace: ${vars.inputs.registryNamespace}
        assert:
          - path: /status
            op: eq
            value: ready
        timeoutMs: 600000
        pollIntervalMs: 2000

    - id: registry-config
      dependsOn: [registry]
      action:
        tool: opute.capability.kubernetes.configure-registry
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          endpoint: ${vars.inputs.registryEndpoint}
          registryHost: ${vars.inputs.registryHost}
          insecure: ${vars.inputs.registryInsecure}
      validate:
        tool: opute.capability.kubernetes.status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          clusterId: ${vars.inputs.cellId}
        assert:
          - path: /status
            op: eq
            value: ready

    - id: database
      dependsOn: [kubernetes]
      action:
        tool: opute.capability.postgresql-service.reconcile
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          clusterName: ${vars.inputs.databaseClusterName}
          namespace: ${vars.inputs.databaseNamespace}
          instances: 1
          storageSize: ${vars.inputs.databaseStorageSize}
          retentionPolicy: retain
          databases: ${vars.inputs.databaseNames}
          consumerSecretName: ${vars.inputs.databaseConsumerSecretName}
          consumerSecretLabel: opute.io/database-consumer
          serviceOwner: opute
          servicePartOf: platform
          restartConsumers: false
      validate:
        tool: opute.capability.postgresql-service.status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          clusterName: ${vars.inputs.databaseClusterName}
          namespace: ${vars.inputs.databaseNamespace}
          databases: ${vars.inputs.databaseNames}
          consumerSecretName: ${vars.inputs.databaseConsumerSecretName}
          consumerSecretLabel: opute.io/database-consumer
          serviceOwner: opute
          servicePartOf: platform
        assert:
          - path: /ready
            op: eq
            value: true
          - path: /sqlReady
            op: eq
            value: true
          - path: /consumerSecretReady
            op: eq
            value: true
        timeoutMs: 900000
        pollIntervalMs: 2000
      timeoutMs: 900000

    - id: platform-artifact
      dependsOn: [database]
      action:
        tool: ensure_host_artifact
        args:
          uri: ${vars.inputs.platformArtifactUri}
          destination: ${vars.inputs.platformArtifactPath}
          sha256: ${vars.inputs.platformArtifactSha256}
          executable: false
      validate:
        tool: inspect_host_file
        args:
          path: ${vars.inputs.platformArtifactPath}
          expectedSha256: ${vars.inputs.platformArtifactSha256}
        assert:
          - path: /exists
            op: eq
            value: true
          - path: /regular
            op: eq
            value: true
          - path: /matches
            op: eq
            value: true

    - id: platform-extract
      dependsOn: [platform-artifact]
      action:
        tool: extract_host_archive
        args:
          archivePath: ${vars.inputs.platformArtifactPath}
          destination: ${vars.inputs.platformExtractPath}
          format: tar.zst
      validate:
        tool: render_helm_template
        args:
          chartPath: ${vars.inputs.platformChartPath}
          releaseName: ${vars.inputs.platformReleaseName}
          namespace: ${vars.inputs.platformNamespace}
          valuesFiles: ${vars.inputs.platformValuesFiles}
          set: ${vars.inputs.platformHelmSet}
        assert:
          - path: /manifest
            op: notEmpty

    - id: platform-render
      dependsOn: [platform-extract]
      action:
        tool: render_helm_template
        args:
          chartPath: ${vars.inputs.platformChartPath}
          releaseName: ${vars.inputs.platformReleaseName}
          namespace: ${vars.inputs.platformNamespace}
          valuesFiles: ${vars.inputs.platformValuesFiles}
          set: ${vars.inputs.platformHelmSet}
      validate:
        tool: render_helm_template
        args:
          chartPath: ${vars.inputs.platformChartPath}
          releaseName: ${vars.inputs.platformReleaseName}
          namespace: ${vars.inputs.platformNamespace}
          valuesFiles: ${vars.inputs.platformValuesFiles}
          set: ${vars.inputs.platformHelmSet}
        assert:
          - path: /manifest
            op: notEmpty

    - id: platform-namespace
      dependsOn: [platform-render]
      action:
        tool: apply_manifest
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          manifest: |
            apiVersion: v1
            kind: Namespace
            metadata:
              name: ${vars.inputs.platformNamespace}
      validate:
        tool: get_k8s_resource
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          kind: Namespace
          resourceName: ${vars.inputs.platformNamespace}
        assert:
          - path: /resource
            op: exists

    - id: platform-secrets
      dependsOn: [platform-namespace, database]
      action:
        tool: put_k8s_secret
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          namespace: ${vars.inputs.platformNamespace}
          name: ${vars.inputs.platformAuthSecretName}
          data: ${vars.inputs.platformAuthSecretData}
      validate:
        tool: get_k8s_resource
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          kind: Secret
          resourceName: ${vars.inputs.platformAuthSecretName}
          namespace: ${vars.inputs.platformNamespace}
        assert:
          - path: /resource
            op: exists

    - id: platform-apply
      dependsOn: [platform-render, platform-secrets, database]
      action:
        tool: apply_manifest
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          manifest: ${nodes.platform-render.output.manifest}
      validate:
        tool: get_k8s_resource_status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          resourceKind: Deployment
          resourceName: ${vars.inputs.platformApiDeployment}
          namespace: ${vars.inputs.platformNamespace}
        assert:
          - path: /status
            op: eq
            value: ready
      timeoutMs: 900000

    - id: platform-web
      dependsOn: [platform-apply]
      validate:
        tool: get_k8s_resource_status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          resourceKind: Deployment
          resourceName: ${vars.inputs.platformWebDeployment}
          namespace: ${vars.inputs.platformNamespace}
        assert:
          - path: /status
            op: eq
            value: ready
        timeoutMs: 600000
        pollIntervalMs: 2000

    - id: platform-mcp
      dependsOn: [platform-apply]
      validate:
        tool: get_k8s_resource_status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          resourceKind: Deployment
          resourceName: ${vars.inputs.platformMcpDeployment}
          namespace: ${vars.inputs.platformNamespace}
        assert:
          - path: /status
            op: eq
            value: ready
        timeoutMs: 600000
        pollIntervalMs: 2000

    - id: task-ledger
      dependsOn: [platform-apply]
      validate:
        tool: get_k8s_resource_status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          resourceKind: Deployment
          resourceName: ${vars.inputs.taskLedgerDeployment}
          namespace: ${vars.inputs.platformNamespace}
        assert:
          - path: /status
            op: eq
            value: ready
        timeoutMs: 600000
        pollIntervalMs: 2000

    - id: tenant-boundary
      dependsOn: [platform-web, platform-mcp, task-ledger]
      action:
        tool: apply_verified_manifest
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          manifest: ${vars.inputs.tenantBoundaryManifest}
          sha256: ${vars.inputs.tenantBoundaryManifestSha256}
      validate:
        tool: get_k8s_resource
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          kind: ${vars.inputs.tenantBoundaryMarkerKind}
          resourceName: ${vars.inputs.tenantBoundaryMarkerName}
          namespace: ${vars.inputs.platformNamespace}
        assert:
          - path: /resource
            op: exists

    - id: edge
      dependsOn: [tenant-boundary]
      action:
        tool: opute.capability.edge.provision
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          namespace: ${vars.inputs.edgeNamespace}
          name: ${vars.inputs.edgeName}
          token: ${vars.inputs.cloudflaredToken}
          replicas: 2
          localTargets: ${vars.inputs.edgeLocalTargets}
      validate:
        tool: opute.capability.edge.status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          name: ${vars.inputs.edgeName}
        assert:
          - path: /status
            op: eq
            value: ready
      timeoutMs: 900000

    - id: platform-probe
      dependsOn: [edge]
      action:
        tool: probe_http_endpoint
        args:
          endpoint: ${vars.inputs.platformEndpoint}
      validate:
        tool: probe_http_endpoint
        args:
          endpoint: ${vars.inputs.platformEndpoint}
        assert:
          - path: /ready
            op: eq
            value: true
        timeoutMs: 120000
        pollIntervalMs: 2000

    - id: mcp-probe
      dependsOn: [edge]
      action:
        tool: probe_http_endpoint
        args:
          endpoint: ${vars.inputs.mcpEndpoint}
      validate:
        tool: probe_http_endpoint
        args:
          endpoint: ${vars.inputs.mcpEndpoint}
        assert:
          - path: /ready
            op: eq
            value: true
        timeoutMs: 120000
        pollIntervalMs: 2000

    - id: cell-ready
      dependsOn: [platform-probe, mcp-probe]
      action:
        tool: opute.capability.kubernetes.status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          clusterId: ${vars.inputs.cellId}
      validate:
        tool: opute.capability.kubernetes.status
        args:
          targetRef: ${nodes.kubernetes.output.uri}
          clusterId: ${vars.inputs.cellId}
        assert:
          - path: /status
            op: eq
            value: ready
          - path: /totalNodes
            op: eq
            value: 1

outputMapping:
  cellReady: nodes.cell-ready.output.status
  cellId: vars.inputs.cellId
  computeUri: nodes.cell-vm.output.uri
  kubernetesUri: nodes.kubernetes.output.uri
  vmName: vars.inputs.vmName
  kubernetesVersion: nodes.kubernetes.observed.version
  kubernetesDistribution: nodes.kubernetes.observed.distribution
  kubernetesProvider: nodes.kubernetes.observed.provider.id
  registryReady: nodes.registry.observed.status
  databaseReady: nodes.database.observed.ready
  platformReady: nodes.platform-apply.observed.status
  tenantBoundaryReady: nodes.tenant-boundary.output.changed
  platformEndpoint: vars.inputs.platformEndpoint
  mcpEndpoint: vars.inputs.mcpEndpoint
  publicEdgeReady: nodes.edge.observed.status
```

### Manifest review notes

The manifest above is intentionally strict in several places:

- The `opute-platform` defaults are exact Kubernetes identifiers. The recipe
  fixture test must reject accidental whitespace in any Kubernetes name and
  must preserve the same namespace across database, platform, edge-target,
  and readiness inputs.
- `providerBindings` are composition data, not implementation branches. The
  v1 fixture binds the Kubernetes port to K3s, but a MicroK8s composition may
  replace only that binding and provider-compatible release input. The
  canonical operation IDs and Host Agent source remain unchanged. The same
  composition rule applies to the `compute` binding: `com.opute.incus` can be
  replaced by a conformance-tested compute provider without changing this
  recipe's operation IDs.
- The compute, Kubernetes, registry, PostgreSQL-service, and edge operations in
  this manifest are provider-port operations. The compute provision result is
  the canonical `vm:<tenant>:<id>` URI; every later `targetRef` is a URI
  resolved under the active tenant. `vmName`, CNPG names, Cloudflare connector
  details, and vendor flags are implementation or composition data.
- `tenantContext` is intentionally not an ordinary input. The authenticated
  session and plan runner supply `tenantId`; a caller cannot use a recipe
  input to select another tenant. A tenant application named `blog` is
  addressed as `service:<tenant>:blog`, so an identically named service in a
  different tenant cannot be selected by a name-only request.
- `platform-extract` uses Helm rendering as its readiness validator because
  the current generic archive extractor has no directory-inspection
  capability. If that duplicate render is undesirable, add a typed
  `inspect_host_chart` read capability and replace only that validation node.
- Action validations are retained as `NodeRunState.Observed`; the output
  mapping uses that typed observation for registry, database, platform API,
  and edge readiness instead of repeating the same checks in validation-only
  nodes. The separate web, MCP, and task-ledger nodes prove the remaining
  platform consumers.
- `probe_http_endpoint` proves public HTTP reachability only. A separate
  authenticated MCP protocol canary remains required before claiming full
  public control-plane certification.
- `apply_verified_manifest` is deliberately distinct from the existing broad
  `apply_manifest`; it prevents a caller-supplied tenant policy from being
  applied without an integrity check.
- The final implementation must use `opute-platform` consistently. The
  manifest is a review artifact and its fixture test must parse it with the
  same YAML decoder used by the recipe loader.
