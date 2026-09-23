# private-site-deployment-controller Specification

## Purpose

Defines the isolated private repository and its controller workflow for
deploying the public Host Agent site through the local Host Agent.

## ADDED Requirements

### Requirement: Deployment authority is isolated in a private repository

The workflow, host-local recipe, Kubernetes workload manifest, and deployment
controller for www.opute.io MUST live in private repository
wunderous/opute-site-deploy. The source repository MUST contain none of these
deployment authorities.

#### Scenario: A push to public source cannot execute on the deploy runner

- **WHEN** a public source commit or pull request runs its source workflow
- **THEN** it executes only on GitHub-hosted runners
- **AND** the private self-hosted runner receives no job from that repository

#### Scenario: Private controller is the sole deployment entrypoint

- **WHEN** the site is deployed or rolled back
- **THEN** the controller invokes the checked-in Host Agent recipe
- **AND** no raw kubectl, Helm, Incus, or provider CLI sequence replaces it

### Requirement: Private pull request checks stay off the deployment host

The private repository MUST validate pull requests on GitHub-hosted runners with
read-only permissions. The local self-hosted runner MUST receive no pull-request
job from either repository.

#### Scenario: Private controller change is proposed

- **WHEN** a pull request targets the private repository main branch
- **THEN** offline controller and boundary checks run on a GitHub-hosted runner
- **AND** the serving-host runner is not assigned that job

### Requirement: Controller accepts only an eligible source workflow run

The controller MUST verify the exact source repository, workflow path, main
branch, allowed event, completed state, successful conclusion, full commit
SHA, run ID, and run attempt before resolving or deploying an image.
Scheduled polling MUST defer while the newest source run is pending or failed;
it MUST NOT silently fall back to an older successful run. Manual rollback MAY
select an older successful main run.

#### Scenario: Newest main image build is still running

- **WHEN** the newest source workflow run is queued or in progress
- **THEN** scheduled deployment makes no Host Agent mutation
- **AND** records the deferred state and source run identity

#### Scenario: Source workflow fails

- **WHEN** the newest source workflow run concludes unsuccessfully
- **THEN** the controller records a non-pass state
- **AND** no older image is deployed automatically

### Requirement: Deploy is performed by a Host Agent recipe using an immutable digest

The controller MUST discover the current Host Agent tool catalog, verify the
exact opaque Host Agent process identity and admitted canonical cluster URI,
hash and validate the host-local recipe, run it, and poll durable plan state
to terminal. The checked-in recipe MUST consume a GHCR image reference pinned
to sha256 digest and MUST apply it with typed Host Agent capabilities.

#### Scenario: Successful recipe deploys the selected image

- **WHEN** the source workflow is successful and the recipe completes
- **THEN** the Kubernetes Deployment's image equals the exact selected digest
- **AND** the expected site Pod is Ready
- **AND** the terminal Host Agent plan result records success

#### Scenario: Identity or recipe validation is ambiguous

- **WHEN** the exact Host Agent identity, cluster URI, recipe hash, or catalog
  validation cannot be proven
- **THEN** the controller fails closed before workload mutation

### Requirement: An already-ready digest is not redeployed

After validating the selected image and recipe, the controller MUST inspect the
current site Deployment. When its image equals the selected digest and its
desired, updated, ready, and available replica counts are all one, the
controller MUST skip recipe execution while continuing Pod and external-route
checks. An unknown Deployment result MUST fail closed.

#### Scenario: Schedule sees the selected digest already ready

- **WHEN** a scheduled poll selects the digest already served by a Ready site Deployment
- **THEN** it does not start another Host Agent recipe run
- **AND** it still validates public build markers and Platform route separation

### Requirement: Credentials remain local to the Host Agent machine

Host Agent credentials MUST be read from the local Host Agent process
environment by a runner running as the same Unix user. They MUST NOT be
written into GitHub repository secrets or variables, workflow artifacts,
controller logs, or evidence. The deployment workflow MUST have minimum
read-only GitHub token permissions.

#### Scenario: Credential is needed for MCP

- **WHEN** the controller calls the local MCP endpoint
- **THEN** the bearer value stays only in process memory and the request
  environment
- **AND** logs and artifacts contain no bearer value

### Requirement: External serving-path proof matches the deployed build

After recipe completion, the controller MUST verify the exact Deployment
digest, Ready Pod state, www and apex route, and external build marker for the
selected source run. Platform ingress and MCP endpoints MUST remain separate
and healthy.

#### Scenario: Correct build is externally served

- **WHEN** external GET requests fetch www.opute.io/build.json and
  opute.io/build.json
- **THEN** both responses identify the selected commit SHA, run ID, and attempt
- **AND** controller evidence binds that run identity to the verified GHCR digest and exact Deployment image

#### Scenario: Platform route remains separate

- **WHEN** site deployment completes
- **THEN** platform.opute.io still serves Platform content and mcp.opute.io/mcp
  still behaves as the MCP endpoint, not as the site document root

### Requirement: Site readiness does not imply two-node write availability

A successful site rollout on a two-server K3s cluster MUST be reported as
application readiness and public reachability only. It MUST NOT claim
quorum-backed write availability or automatic tolerance of a voting member loss.

#### Scenario: Two Ready K3s servers host the site

- **WHEN** both cluster servers are Ready and the site passes its HTTP probes
- **THEN** membership and site readiness are reported separately
- **AND** the result does not describe the cluster as two-node write HA
