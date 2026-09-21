# site-dogfood-deploy Specification

## Purpose

Defines how the Host Agent static docs and marketing site is published onto a
K3s cluster through a repository-owned Host Agent recipe that composes typed
capabilities, and what external evidence counts as dogfood success without
overstating HA availability.

## ADDED Requirements

### Requirement: Website deployment is performed by a Host Agent recipe

Deploying the docs and marketing site MUST be expressed as a versioned Host
Agent recipe checked into this repository (for example under a `recipes/`
tree beside the site package). The recipe MUST declare a supported recipe
contract (`host-recipe.v1` or another Host Agent–executed recipe contract
already admitted by this repository), a stable `recipeId`, inputs for exact
Host Agent targeting and cluster URI, and steps that call only typed catalog
capabilities. An operator script, ad-hoc MCP call sequence, or direct
`kubectl`/`incus` invocation MUST NOT be the deploy path of record.

#### Scenario: Recipe is the deploy entrypoint

- **WHEN** an operator deploys the website for dogfood
- **THEN** they submit or run the repository recipe with typed inputs
- **AND** publish, apply, expose, and evidence steps execute as recipe nodes
- **AND** no companion shell script is required to mutate the cluster

#### Scenario: Non-recipe deploy is non-conforming

- **WHEN** the site is applied only via a one-off script or raw kubectl
- **THEN** that path MUST NOT be accepted as dogfood deploy evidence
- **AND** the outcome is recorded as a non-pass for this capability

### Requirement: Dogfood deploy targets exact Host Agent and cluster identity

The recipe MUST require inputs for an exact opaque Host Agent identity and an
admitted canonical cluster URI. Node IPs, Tailscale addresses, Cloudflare
hostnames, and display labels MUST NOT be used as Host Agent identity or as
the durable cluster identity for apply operations.

#### Scenario: Typed deploy is accepted

- **WHEN** the recipe is submitted with an exact Host Agent id, an admitted
  cluster URI, a published site artifact digest, and a target namespace
- **THEN** the recipe applies the static-site workload through typed
  capabilities
- **AND** evidence records the recipe id/version, Host Agent id, cluster URI,
  namespace, and artifact digest without credentials

#### Scenario: Ambiguous identity is rejected

- **WHEN** the recipe is submitted without the Host Agent id or with a node
  IP substituted for the cluster URI
- **THEN** the recipe fails closed before cluster mutation
- **AND** the outcome is recorded as a non-pass state

### Requirement: The site artifact is published through Host Agent capabilities

The recipe MUST build or consume the static site as an immutable artifact and
publish it using Host Agent–owned build/push (or equivalent typed publish)
into a registry reachable by the target cluster. Ad-hoc copy of files onto a
node filesystem outside typed ownership MUST NOT satisfy this requirement.

#### Scenario: Artifact publish completes

- **WHEN** a recipe publish step runs for the static build digest
- **THEN** the step result includes an image or artifact reference and digest
- **AND** subsequent recipe apply steps reference that digest

### Requirement: Public hostname is www.opute.io without platform collision

The dogfood recipe MUST expose the site at hostname `www.opute.io` using a
dedicated public-exposure binding and tunnel name that is not the Platform
tunnel. The recipe MUST NOT mutate `platform.opute.io`, `mcp.opute.io`, the
`opute-platform` ingress named `platform-opute`, the in-cluster
`cloudflared` deployment in `opute-platform`, or tunnel name
`opute-platform-opute-io`.

#### Scenario: www is bound on a dedicated tunnel

- **WHEN** the recipe completes exposure for the docs site
- **THEN** evidence records hostname `www.opute.io` and a tunnel name other
  than `opute-platform-opute-io`
- **AND** the Platform ingress hosts list remains exactly
  `platform.opute.io` and `mcp.opute.io`

#### Scenario: Platform endpoints stay healthy after www deploy

- **WHEN** www dogfood deploy finishes
- **THEN** an external GET of `https://platform.opute.io/` still succeeds
- **AND** `https://mcp.opute.io/mcp` still answers as the MCP endpoint
  (authenticated or challenge), not as the docs site

### Requirement: Public exposure uses the provider-neutral contract

Public HTTPS for the site MUST be established by recipe steps that call the
existing provider-neutral public host exposure capability. The recipe MUST
NOT shell out to Cloudflare or Tailscale CLIs. Cloudflare MUST remain a valid
provider; selecting Tailscale Funnel MUST NOT remove Cloudflare coexistence.

#### Scenario: External origin becomes ready

- **WHEN** a recipe exposure step reports the site binding ready for the
  chosen provider generation
- **THEN** evidence records the public origin, provider generation, and
  endpoint stability classification
- **AND** Host Agent MCP administration remains off the public site origin

#### Scenario: Node-specific URL is not cluster identity

- **WHEN** the published origin is classified as node-specific
- **THEN** evidence records `stable=false` (or equivalent)
- **AND** the origin is not stored as the managed cluster’s host-independent
  API endpoint

### Requirement: External HTTP proof is the dogfood acceptance gate

Dogfood success MUST include an external HTTP GET of the published origin that
returns the marketing or docs content expected for the built digest. The GET
SHOULD be a terminal recipe readiness/probe step (or an evidence step the
recipe records). Cluster membership alone, a successful apply, or an ingress
object existing MUST NOT be treated as pass.

#### Scenario: External GET returns the site

- **WHEN** a probe from outside the private mesh requests the published origin
  after the recipe’s exposure step
- **THEN** the response is HTTP success and includes Host Agent marketing or
  docs content for the expected build
- **AND** evidence stores status code, origin, recipe id/version, and content
  digest or marker without secrets

#### Scenario: Apply succeeded but public path fails

- **WHEN** the workload is running but the external GET fails or returns the
  wrong content
- **THEN** the recipe’s dogfood readiness remains a non-pass state
- **AND** the failure reason distinguishes exposure vs workload vs content
  mismatch

### Requirement: Dogfood success does not claim two-node write HA

Serving the site on a one- or two-server K3s membership MUST be reported as
site reachability via Host Agent–managed ingress. The dogfood gate MUST NOT
assert etcd-quorum write continuity or automatic survival of a voting-member
loss.

#### Scenario: Two-server cluster hosts the site

- **WHEN** the site recipe deploys onto a cluster with two ready servers
- **THEN** evidence may record membership count separately from site HTTP
  readiness
- **AND** site dogfood pass MUST NOT be narrated as consensus HA write
  availability

### Requirement: Cleanup is ownership-scoped and recipe-driven

Teardown MUST be expressed as recipe teardown or a paired cleanup recipe that
removes only the site workload, exposure binding, and artifact references
owned by this dogfood generation. Shared Host Agents, unrelated guests, and
the cluster control plane MUST remain unless an explicit separate operation
targets them.

#### Scenario: Generation teardown

- **WHEN** recipe cleanup runs for a recorded generation
- **THEN** only generation-owned site resources are deleted
- **AND** foreign workloads and Host Agent instances remain
