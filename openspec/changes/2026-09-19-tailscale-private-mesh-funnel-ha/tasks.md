# Tasks

## Invariants and authority

- [ ] Record the invariant delta from `design.md` in the owning Host Agent
  decision record/ADR before implementation; include canonical opaque identity,
  private/public path separation, datastore-mode claims, credential kind, and
  redacted evidence.
- [ ] Run the repository's decision lookup for each touched seam and resolve
  any conflict with existing provider-neutral, identity, lifecycle, or evidence
  authorities before editing code.

## Service Definition

- [ ] Define the versioned `network-overlay.v1` service, request/result types,
  resource kinds, `Requires`/`Produces` bindings, task support, and write-only
  credential fields under the owning Host Agent contract/schema packages.
- [ ] Define explicit `datastoreMode`, `availabilityClass`, `stable`, and
  `recoveryPolicy` observations so two-node embedded etcd cannot project as
  generic HA.
- [ ] Add catalog and schema tests proving provider-neutral names contain no
  Tailscale, Cloudflare, K3s, or command-specific lifecycle vocabulary.

## Tailscale provider

- [ ] Add `plugins/tunneling/tailscale/` as a separate provider MCP process with
  explicit manifest, generation, service, and artifact identity.
- [ ] Implement typed validation for tailnet, credential kind, target Host
  Agent/guest identity, tags/grants, node approval, route policy, pod CIDRs,
  K3s version, and required ports.
- [ ] Implement idempotent enrollment, private mesh readiness, Serve, Funnel,
  promotion, probe, and teardown through provider-owned lifecycle effects.
- [ ] Ensure provider candidate → ready → active → draining behavior, reverse
  ordered disposal, cancellation, and no partial catalog publication.
- [ ] Add separate-process Streamable HTTP MCP tests for `server/discover`,
  `tools/list`, `tools/call`, structured results, cancellation, and typed
  provider errors.

## K3s and private data plane

- [ ] Implement the K3s integration path with explicit Tailscale transport and
  no Funnel dependency for API, CNI, etcd, or health traffic.
- [ ] Require external datastore evidence for `external-datastore` mode and
  expose embedded two-node etcd as serving-continuity-only with typed recovery.
- [ ] Add mesh probes for both directions and negative tests proving a public
  Funnel endpoint cannot satisfy a private path prerequisite.
- [ ] Validate exact Host Agent IDs, guest URIs, cluster URI, node identities,
  and endpoint bindings before any cross-host mutation.

## Public ingress

- [ ] Add the consumer path for a scoped local gateway or Tailscale Kubernetes
  Operator Ingress; keep application authentication separate from Funnel.
- [ ] Record `stable=false` for node-specific Funnel URLs and reject storing
  them as host-independent cluster endpoints.
- [ ] If stable application ingress is selected, deploy and validate operator
  proxies on both servers, their tagged Funnel permission, and Kubernetes
  Service routing before claiming continuity.
- [ ] Preserve the existing Cloudflare consumer/provider and prove both
  providers satisfy the same neutral contract without provider-specific
  consumer branches.

## Routing, readiness, and cleanup

- [ ] Extend two-node readiness to report membership, private mesh, datastore
  mode, public endpoint stability, serving continuity, durable-store
  continuity, and Kubernetes write availability as separate axes.
- [ ] Keep host-scoped provider installation, node enrollment, file, artifact,
  and service mutations pinned to the exact Host Agent; only cluster-scoped
  observations and admitted endpoint operations may use ranked candidates.
- [ ] Prove cleanup removes only recorded Tailscale nodes, routes, Serve/Funnel
  bindings, operator resources, artifacts, services, and state owned by this
  provider generation.
- [ ] Add fail-closed tests for stale identity, foreign ownership, ambiguous
  peer, wrong credential kind, public/private path confusion, and stale
  endpoint binding.

## Validation and evidence

- [ ] Run `gofmt -w .`, `go vet ./...`, and `go test ./...` in the Host Agent
  repository.
- [ ] Run contract, standalone, and mode tests, including
  `make standalone-smoke` and `make standalone-http-smoke` where the new
  provider path applies.
- [ ] Run a real provider MCP wire test against a disposable Tailscale-enabled
  target or record the environment as explicitly unverified; do not substitute
  a unit test for live mesh evidence.
- [ ] Exercise each server stop independently and record public service,
  durable-store, datastore, Kubernetes API-write, and Funnel endpoint results
  separately.
- [ ] Verify no secret value, raw provider error, node auth key, API key, or
  route credential appears in tasks, traces, durable state, logs, prompts, or
  evidence projections.
- [ ] Verify disposable targets and provider generations are fully cleaned up,
  while unrelated shared services and dirty worktree changes remain intact.
