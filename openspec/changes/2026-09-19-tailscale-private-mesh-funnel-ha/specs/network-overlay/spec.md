# network-overlay Specification

## Purpose

Defines the provider-neutral private overlay and public-ingress capability that
lets Opute select Cloudflare, Tailscale, or a future provider without exposing
provider lifecycle or credential details to the Host Agent core or consumer.

## ADDED Requirements

### Requirement: The overlay capability has a three-role seam

The capability MUST expose one provider-neutral Service Definition, one or more
replaceable Service Providers, and independent Consumers. The Service Provider
and Consumer MUST depend on the Service Definition and MUST NOT depend on each
other's package, operation names, or provider-specific metadata.

#### Scenario: Tailscale is selected

- **WHEN** the Platform selects the Tailscale implementation for a declared
  overlay capability
- **THEN** the Consumer invokes the neutral service and the active provider
  generation supplies the implementation
- **AND** no Tailscale command, URL, credential field, or display name appears
  in the neutral operation ID or model-facing projection

#### Scenario: A provider generation is replaced

- **WHEN** an alternate provider generation becomes ready
- **THEN** the candidate is activated only after manifest, catalog, and
  readiness validation
- **AND** the previous generation drains and disposes its owned effects without
  exposing stale bindings

### Requirement: Private mesh enrollment is exact and idempotent

The provider MUST enroll only the explicitly selected Host Agent/guest
resource, tailnet, tag/grant policy, and generation. Repeated enrollment MUST
converge on the same owned node and route state. The provider MUST fail closed
on an ambiguous peer, stale target identity, foreign ownership record, or
credential-kind mismatch.

#### Scenario: A target-bound node is enrolled

- **WHEN** the admitted operation names exact opaque Host Agent and guest URIs
  and a write-only enrollment credential reference
- **THEN** the provider enrolls that target and returns a typed overlay resource
  URI with redacted peer/readiness observations
- **AND** no secret value enters durable state or evidence

#### Scenario: Enrollment is repeated after a partial failure

- **WHEN** the target already has the provider-owned node marker but mesh
  readiness is incomplete
- **THEN** the provider adopts the marker and performs only missing work
- **AND** it does not create a second node or route

### Requirement: Private cluster traffic does not use public ingress

The provider and Consumer MUST prove that Kubernetes API, control-plane,
datastore, CNI, and health traffic use the admitted private overlay. A public
Funnel endpoint MUST NOT satisfy a private path prerequisite.

#### Scenario: Both servers have mesh reachability

- **WHEN** the source and destination server probes succeed over the declared
  overlay addresses and required ports
- **THEN** the private-mesh prerequisite passes
- **AND** the evidence records exact source/destination resource identities,
  path class, and provider generation

#### Scenario: Only Funnel is reachable

- **WHEN** a public Funnel endpoint answers but a required private peer probe
  fails
- **THEN** the prerequisite remains BLOCKED
- **AND** the cluster join or HA claim is not activated

### Requirement: Public ingress is scoped and stability is explicit

The provider MAY expose a local application gateway or operator-managed
Kubernetes Ingress through Funnel. It MUST reject remote arbitrary targets and
must return whether the endpoint is stable, node-specific, or ephemeral. Funnel
MUST NOT expose etcd, the Kubernetes API, CNI, or Host Agent administrative
control as an implicit consequence of mesh enrollment.

#### Scenario: A local workload is exposed

- **WHEN** the Consumer supplies a declared local gateway/Service binding and
  an approved Funnel policy
- **THEN** the provider exposes only that resource over TLS and returns the
  generated endpoint with its stability classification
- **AND** application authentication remains required where the workload is
  sensitive

#### Scenario: A node-specific Funnel is promoted

- **WHEN** the active gateway node becomes unavailable and the typed promotion
  operation selects the standby node
- **THEN** the provider records the endpoint change and new readiness evidence
- **AND** it does not claim stable public identity unless the selected ingress
  implementation proves it

### Requirement: Provider cleanup is ownership-proven

The provider MUST record the node, route, Serve/Funnel binding, service,
artifact, and state identities it creates. Teardown MUST act only on those
recorded identities and MUST fail closed when ownership or generation matching
cannot be proved.

#### Scenario: The active generation is torn down

- **WHEN** teardown receives the exact provider generation and binding
- **THEN** it removes only the generation-owned overlay and ingress resources
- **AND** unrelated tailnet nodes, routes, services, and shared runtime state
  remain intact
