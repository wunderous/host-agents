# ADR-0003: Cloudflare provider boundary and Incus system containers

Status: accepted

## Decision

Cloudflare tunneling is implemented exclusively by the independently served
`com.opute.cloudflare` provider MCP. The Host Agent publishes the neutral
`opute.capability.tunneling.v1` contract and dynamically registers the
provider-declared operations, including the legacy Cloudflare names for one
migration period. No Cloudflare operation is a built-in Host Agent dispatch
case.

Provider operations own Cloudflare validation, `cloudflared` service/API
behavior, and Kubernetes connector reconciliation. They may call only the
public `pkg/hostagentclient` boundary for generic Host Agent primitives. A
resource target is resolved by its canonical tenant-scoped URI before the
primitive executes; provider code cannot import `internal/*` packages.

Incus system containers are first-class compute resources. Their canonical
identity is `container:<tenant>:<id>`, provisioning registers that URI and
`instanceType=container`, and `run_instance_command` executes a typed argv only
after resolving a VM or container URI. Tenant mismatches, wrong resource types,
and implicit VM fallback are rejected.

Provider teardown remains two phase: the Host Agent executes the generic
cleanup plan first, then the Cloudflare provider finalizes external API
resources. A failed finalization leaves the generation retryable.

Kubernetes execution follows the same boundary. The public Host Agent exposes
only canonical cluster-URI primitives; an active provider implementing
`opute.capability.kubernetes.v1` supplies concrete control-plane execution.
The first implementation is the independently served `com.opute.k3s` provider,
which runs `k3s kubectl` through an explicitly resolved Incus VM or system
container and never imports Host Agent internals.

## Invariant delta

- Added C-15 to permit narrowly scoped public-MCP callbacks without weakening
  the provider-neutral core boundary.
- Replaced built-in Cloudflare routing with manifest-backed dynamic provider
  operations.
- Added URI and instance-type enforcement for container placement.
- Added the neutral Kubernetes provider executor and `com.opute.k3s` provider;
  generic Host Agent Kubernetes calls now delegate by capability, not product
  or display name.

## Evidence

- `test/contract/architecture_test.go` forbids provider imports of Host Agent
  internals while permitting only the public callback client.
- Provider manifest, operation, raw-argument, placement, and secret-redaction
  tests live beside the Cloudflare MCP executable.
- `internal/ops/exec_command_test.go` covers container URI execution,
  cross-tenant rejection, and no implicit VM fallback.
- `internal/hostmcp/kubernetes_provider.go` is the sole Host Agent adapter
  from generic Kubernetes primitives to an active provider MCP generation.
- `plugins/kubernetes/k3s` contains the provider manifest, pinned recipe,
  typed operation schemas, and concrete Incus/K3s execution.

## Operational validation record

Validation performed on 2026-08-24 from the WSL checkout:

- Launched the provider MCP on a disposable local port and completed both the
  legacy `initialize` exchange using `2025-11-25` and the negotiated
  `server/discover`, `tools/list`, and `tools/call` exchange using
  `2026-07-28`. `tools/list` returned 17 provider tools, including both
  dynamic operations and migration aliases. The validation call returned the
  raw `container:tenant-a:edge` binding and no secret.
- Launched the current Host Agent in isolated platform mode, provisioned the
  disposable Incus container `codex-cloudflare-incus-20260824b`, and verified
  the returned URI `container:local:codex-cloudflare-incus-20260824b`.
- Called `run_instance_command` through the real Host Agent MCP endpoint and
  observed `exitCode=0`, `instanceType=container`, and
  `instance-command-ok`. Incus externally reported `Type: container`,
  `Status: RUNNING`, and `boot.autostart=true`; the container was then deleted
  by exact name.
- Replayed the same command with a foreign tenant URI and a `vm:` URI for the
  container name. Both calls failed closed before guest execution.
- Pointed the provider at the isolated Host Agent MCP endpoint and executed a
  real provider `probe-host-tunnel` call. The provider callback reached the
  neutral `probe_http_endpoint` primitive and returned HTTP 200 for
  `http://127.0.0.1:8080` without exposing provider credentials.
- Executed provider teardown over MCP in both phases. The prepare response
  returned a `host-plan.v1` stop, disable, and remove-file sequence; a separate
  finalize call with no external IDs completed without attempting an API call.
- Loaded the disposable Cloudflare credentials from the ignored Opute
  `.env.cloudflare.local` source without printing their values. The current
  live canary (`runId=bf82533b`) ran with the connector-enabled Host Agent,
  reached `publication_status=active`, returned a public probe status of 200,
  and completed Host Agent prepare cleanup followed by the Go provider
  finalizer with zero cleanup errors. Cloudflare API queries for the exact
  disposable hostname and tunnel name returned zero active records/tunnels;
  recursive DNS still cached the name briefly, so authoritative API state is
  the deletion gate.
- Exercised the new provider operation
  `opute.capability.tunneling.install-kubernetes-connector` with
  `targetUri=cluster:local:opute-clean-k3s`. The provider invoked only Host
  Agent MCP `apply_manifest`; Host Agent resolved the cluster URI to the
  Incus-backed K3s instance. Readiness and event evidence came from Host Agent
  MCP `get_k8s_resource_status`, `get_k8s_resource`, and `list_k8s_events`; no
  local `kubectl` command was used. The image pulled and the Deployment became
  `ready` after correcting the provider-owned probe to execute
  `cloudflared --version` directly (the image has no `sh`).
- Exercised provider
  `opute.capability.tunneling.delete-kubernetes-connector`, which invoked Host
  Agent MCP `delete_k8s_resource`. A subsequent Host Agent status call returned
  `missing` for namespace `cf-codex-20260824`, proving cleanup in the shared
  K3s cell without modifying unrelated resources.
- Added neutral Host Agent cluster bindings for generic Kubernetes primitives
  and URI propagation on their results, so provider callbacks preserve the
  canonical target instead of falling back to VM-name arguments.
- Added explicit cluster bindings to the checked-in aggregate and Incus
  catalogs, and implemented `list_kubernetes_clusters` dispatch with a
  provider-backed inventory path.
- Tightened the local ignored Cloudflare credential and tunnel-token files to
  owner-only mode (`0600`). Values remain process inputs only.
- Built and exercised the standalone `com.opute.k3s` provider (manifest
  version `1.0.1`) with real MCP
  `initialize`, negotiated `server/discover`, `tools/list`, and `tools/call`
  exchanges. Host Agent installation and activation then exposed the dynamic
  Kubernetes operations without adding a Cloudflare or direct-kubectl route.
- Through Host Agent MCP, `list_kubernetes_clusters` discovered
  `cluster:local:opute-clean-k3s` with `instanceType=container`; a disposable
  Namespace and ConfigMap were applied, read, status-probed, event-listed,
  deleted, and verified absent. `cluster:other:opute-clean-k3s`, a missing
  `targetUri`, a display-name-only target, and a `container:` URI were all
  rejected before provider execution. The live inventory reported one Ready
  node on K3s `v1.31.8+k3s1`.
- The Go Cloudflare provider finalizer deleted real disposable tunnels and DNS
  records using their observed IDs. The provider now also rejects HTTP-200
  Cloudflare envelopes with `success=false`; the Host Agent recovery regression
  test proves a failed finalizer leaves the active generation and adapter
  connected, and a retry drains and stops that generation only after success.
- The validator now records secret, connection, binding, tunnel, and DNS
  creation state and performs best-effort finalization/revocation in `finally`
  while preserving the primary failure and sanitizing cleanup errors.
- The validator's cumulative dogfood gate reports three consecutive green live
  canaries but remains policy-blocked at `2/7` consecutive days; this is a
  monitoring-window requirement, not a failed connector or cleanup result.
- Ran a fresh active-generation lifecycle through the disposable Host Agent
  MCP endpoint. The managed connector reached `active` with one Cloudflare
  connection; Host Agent teardown completed generic stop/disable/file removal
  followed by provider finalization. The generation then reported inactive and
  disconnected, the service was inactive/disabled with no unit file, and exact
  authoritative API checks returned no tunnel and zero DNS records.
- Ran live finalization recovery with the same lifecycle. Removing provider
  credentials caused finalization to fail after prepare while the generation
  remained active and connected and both external resources remained. After
  restoring credentials, resuming the same durable teardown completed and
  removed the exact tunnel and DNS record. The provider phase is now carried in
  the declared `inputs` object, and the prepare plan uses the Host Agent's
  canonical tenant-scoped host-service URI.

Release checks for this change are `go test ./...`, `go vet ./...`, the
standalone K3s and Cloudflare provider suites, catalog JSON validation, the
product typecheck, and `git diff --check`.
