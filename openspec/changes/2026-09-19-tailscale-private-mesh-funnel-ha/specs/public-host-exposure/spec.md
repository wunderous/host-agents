# public-host-exposure Specification

## Purpose

Adds Tailscale Funnel as an alternate public exposure implementation while
preserving authenticated MCP readiness, provider ownership, and explicit
endpoint stability.

## ADDED Requirements

### Requirement: Tailscale Funnel exposure is explicitly scoped

The public exposure capability MUST accept a typed local gateway or
operator-managed Kubernetes Ingress binding and a write-only provider
credential reference. It MUST reject arbitrary remote targets and MUST NOT
expose private Host Agent MCP, Kubernetes API, etcd, or CNI paths through
Funnel.

#### Scenario: A workload gateway is exposed

- **WHEN** the caller supplies an admitted local gateway binding and approved
  Funnel policy
- **THEN** the provider creates the owned public HTTPS binding and returns the
  exact endpoint plus `stable` classification
- **AND** the result contains no credential value or raw provider output

#### Scenario: A private control endpoint is requested

- **WHEN** the caller attempts to expose Host Agent administration, Kubernetes
  API, etcd, or CNI traffic through Funnel
- **THEN** the operation is rejected before provider mutation
- **AND** the private mesh or tailnet-only Serve path remains the only eligible
  path

### Requirement: Public readiness validates the complete Funnel path

Tailscale exposure MUST verify tailnet policy permission, MagicDNS/HTTPS
prerequisites, the provider generation, the local target, the generated
endpoint, and the application-level authentication contract. A Funnel relay
answer alone MUST NOT activate the public binding.

#### Scenario: External public probe succeeds

- **WHEN** a probe from outside the tailnet reaches the generated endpoint and
  the application returns the expected authenticated result
- **THEN** the binding may be marked ready
- **AND** evidence records the exact target, endpoint, generation, and
  `stable` status without secrets

#### Scenario: Funnel answers but application authentication is absent

- **WHEN** the relay is reachable but a sensitive service accepts an unauthenticated
  request
- **THEN** readiness remains non-passing
- **AND** the provider does not claim a secure public exposure

### Requirement: Node-specific Funnel endpoints are not cluster endpoints

The capability MUST mark a host-level Funnel URL as node-specific unless a
typed ingress implementation proves an endpoint identity independent of one
node. A node-specific URL MUST NOT be stored as the managed cluster's
host-independent API endpoint.

#### Scenario: Standby promotion changes the URL

- **WHEN** promotion changes the Funnel target from Host Agent A to Host Agent
  B and the generated URL changes
- **THEN** the operation records an endpoint rotation
- **AND** callers are not told that the original URL remained stable
