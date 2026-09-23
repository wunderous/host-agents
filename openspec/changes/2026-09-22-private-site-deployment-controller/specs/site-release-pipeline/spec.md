# site-release-pipeline Specification

## Purpose

Defines the public source repository's static-site image build, immutable
versioning, and provenance boundary.

## ADDED Requirements

### Requirement: Public source CI builds and publishes the site image

The public wunderous/host-agents repository MUST build the static-site OCI
image from its checked-in site source on a GitHub-hosted runner. A successful
build MUST publish the image to the public GHCR package used by the site.
Source CI MUST NOT deploy to Kubernetes, connect to the local Host Agent, run
on a self-hosted runner, or require deployment credentials.

#### Scenario: Main commit produces an image

- **WHEN** a site-source commit reaches main and the image workflow succeeds
- **THEN** the workflow publishes an image derived from that exact commit
- **AND** the workflow result identifies the produced OCI digest
- **AND** the build uses no Host Agent, cluster, tunnel, or GitHub cross-repo credential

#### Scenario: Public pull request cannot reach the deployment host

- **WHEN** a public pull request adds or edits source workflow content
- **THEN** no source-repository workflow runs on the Host Agent machine
- **AND** no deployment token or local process environment is exposed to that workflow

### Requirement: Site images have immutable deployment identities

Every source workflow run MUST publish a unique OCI tag derived from the workflow
run ID and attempt, plus a commit-SHA tag for human discovery. Deployment MUST
resolve the unique run tag to a sha256 digest and apply the digest reference.
Mutable tags MUST NOT be the deployment input.

#### Scenario: A workflow attempt resolves to one image digest

- **WHEN** a controller processes a successful source workflow run
- **THEN** its run ID and attempt select exactly one published tag
- **AND** the resolved OCI manifest digest is recorded and used by the workload

#### Scenario: Re-run produces a distinct build identity

- **WHEN** a source workflow run is re-run
- **THEN** its incremented attempt produces a different run tag
- **AND** the controller can distinguish its image from the prior attempt

### Requirement: Public image carries a safe build marker and provenance

The image MUST include a machine-readable public build marker containing only
the source commit SHA and workflow run identity. The image build MUST emit a
provenance attestation for the exact OCI digest. No credentials or private
network identifiers may be embedded in the marker, labels, or image layers.

#### Scenario: External response identifies the deployed source

- **WHEN** the deployed site serves build.json
- **THEN** its commit SHA, run ID, and attempt match the selected successful
  source workflow run
- **AND** the marker contains no secret or private topology data
