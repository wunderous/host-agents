# site-dogfood-deploy Specification

## Purpose

Defines how the Host Agent static docs and marketing site is deployed and
validated by a private controller without transferring deployment authority
to the public source repository.

## MODIFIED Requirements

### Requirement: Website deployment is performed by a Host Agent recipe

Deploying the docs and marketing site MUST use the checked-in host-recipe.v1
document in private repository wunderous/opute-site-deploy. The recipe MUST
declare a stable recipeId, exact opaque Host Agent targeting, and an admitted
canonical cluster URI. Its steps MUST call typed Host Agent capabilities. A
controller script MAY discover, validate, run, and inspect the recipe, but
MUST NOT replace it with direct cluster mutation.

#### Scenario: Recipe is the deployment entrypoint

- **WHEN** an operator or scheduled controller deploys the site
- **THEN** it submits the private repository recipe through the exact local
  Host Agent
- **AND** recipe apply, exposure, and readiness operations execute as typed
  Host Agent plan nodes
- **AND** controller code performs read-only source selection and evidence

#### Scenario: Non-recipe deploy is non-conforming

- **WHEN** the site is applied only through a one-off script or raw kubectl
- **THEN** that path MUST NOT be accepted as deployment evidence
- **AND** the outcome is recorded as a non-pass state

### Requirement: The public source repository builds and versions the site image

The public Host Agent source repository MUST build and publish the static-site
image using GitHub-hosted CI. It MUST assign a unique run-and-attempt tag and
a commit-SHA tag, publish provenance for the resulting digest, and provide a
build marker that identifies the source commit and workflow run. The private
controller MUST resolve the unique run tag to a digest and pass that immutable
reference into the recipe. The recipe MUST NOT build or push the image.

#### Scenario: Build output crosses the repository boundary

- **WHEN** a completed source workflow run on main succeeds
- **THEN** the private controller resolves its image tag to one OCI digest
- **AND** the workload recipe consumes that digest without source checkout

#### Scenario: Mutable reference is rejected

- **WHEN** the controller receives a tag or non-digest image input
- **THEN** recipe validation fails before applying the workload

### Requirement: Public hostname is www.opute.io without platform collision

The dogfood recipe MUST expose the site at hostname www.opute.io using the
existing dedicated public-exposure binding and tunnel name
opute-www-opute-io. It MUST NOT mutate platform.opute.io, mcp.opute.io, the
opute-platform ingress named platform-opute, the in-cluster cloudflared
deployment in opute-platform, or tunnel name opute-platform-opute-io.

#### Scenario: www is bound on a dedicated tunnel

- **WHEN** the recipe completes exposure for the docs site
- **THEN** evidence records hostname www.opute.io and the dedicated site
  tunnel name
- **AND** the Platform ingress host list remains exactly platform.opute.io
  and mcp.opute.io

#### Scenario: Platform endpoints stay healthy after www deploy

- **WHEN** www dogfood deploy finishes
- **THEN** an external GET of https://platform.opute.io/ still succeeds
- **AND** https://mcp.opute.io/mcp still answers as the MCP endpoint, not as
  the docs site

### Requirement: Public exposure uses the provider-neutral contract

Public HTTPS for the site MUST be established by recipe steps that call the
existing provider-neutral public-host exposure capability. The recipe MUST
NOT shell out to Cloudflare or Tailscale CLIs. Cloudflare MUST remain a valid
provider; selecting Tailscale Funnel MUST NOT remove Cloudflare coexistence.

#### Scenario: External origin becomes ready

- **WHEN** a recipe exposure step reports the site binding ready for the
  current provider generation
- **THEN** evidence records the public origin and provider generation
- **AND** Host Agent MCP administration remains off the site origin

### Requirement: External HTTP proof is the dogfood acceptance gate

Dogfood success MUST include external GETs of the published www and apex
origins that return the build marker for the selected source run. Image digest,
source commit, run ID, and attempt MUST agree between controller evidence,
the running Deployment, and build.json. A successful apply or existing ingress
MUST NOT be treated as pass.

#### Scenario: External GET returns selected build

- **WHEN** probes run outside the private mesh after the recipe completes
- **THEN** https://www.opute.io/build.json and https://opute.io/build.json
  return status 200 and the selected source commit, run ID, and attempt
- **AND** evidence records the image digest, response status, and marker

#### Scenario: Apply succeeded but public path fails

- **WHEN** the workload runs but the external GET fails or serves a different
  marker
- **THEN** site dogfood remains a non-pass state
- **AND** failure evidence identifies source selection, workload, tunnel, or
  content mismatch

### Requirement: Dogfood success does not claim two-node write HA

Serving the site on the existing two-server K3s membership MUST be reported as
application readiness and public reachability. The dogfood gate MUST NOT
assert quorum write continuity or automatic survival of a voting member loss.

#### Scenario: Two servers host the site

- **WHEN** both servers are Ready and the site is reachable
- **THEN** membership and site HTTP readiness are reported separately
- **AND** success is not described as two-node consensus HA
