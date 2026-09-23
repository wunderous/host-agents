# Tasks: Private site deployment controller

Tasks remain open until the named evidence is recorded. Keep credentials out of
the repository and final report.

## 1. Lock the repository boundary

- [ ] 1.1 Add the invariant decision record to the private deployment repo and a
  verifier that checks its repository, workflow, recipe, manifest, and evidence
  anchors.
- [ ] 1.2 Review the public source repo for self-hosted runner use, Host Agent
  credentials, deployment recipes, cluster manifests, or controller scripts;
  record the before/after file list.

## 2. Build and publish from the public source repository

- [ ] 2.1 Add a GitHub-hosted image workflow that builds the static site only,
  publishes public GHCR image tags for source SHA and run/attempt, and emits
  provenance for the resulting digest.
- [ ] 2.2 Add a safe build.json marker with source SHA, run ID, and attempt;
  confirm no secrets or private topology values enter the image.
- [ ] 2.3 Remove deployment-owned recipe and manifest files from public source;
  update its documentation and local checks to enforce the boundary.
- [ ] 2.4 Validate workflow syntax, source-boundary checks, image build, public
  anonymous digest pull, and provenance subject.

## 3. Create the private deployment controller

- [ ] 3.1 Create private repository wunderous/opute-site-deploy and add its
  ownership, threat-boundary, bootstrap, rollback, and evidence documentation.
- [ ] 3.2 Add a scheduled/manual controller that selects only an eligible
  successful main source run, with no implicit stale-success fallback.
- [ ] 3.3 Resolve the run-and-attempt OCI tag to a sha256 digest and provide only
  that immutable image reference to the checked-in Host Agent recipe.
- [ ] 3.4 Add the private host-recipe.v1 recipe and manifest, preserving the
  dedicated site tunnel and Platform ingress/MCP isolation.
- [ ] 3.5 Add controller tests for source-run eligibility, digest resolution,
  exact commit/run-attempt provenance binding, fail-closed identity/recipe
  validation, secret redaction, and evidence shape.
- [ ] 3.6 Add the private invariant decision record and verify its file anchors.
- [ ] 3.7 Skip recipe execution when the selected digest is already Ready, while retaining route and Platform checks.

## 4. Connect the private runner to the local Host Agent

- [ ] 4.1 Register a Linux x64 self-hosted runner to the private repo only,
  using the Host Agent Unix user and a repo-specific label.
- [ ] 4.2 Configure a user service with least privilege and confirm the public
  source repository has no self-hosted runner registration.
- [ ] 4.3 Confirm controller logs and artifacts omit the Host Agent bearer value
  and exact private process environment.
- [ ] 4.4 Point the private workflow at the host's shared Opute rollout lease
  module and verify it holds the lease across the controller child process.

## 5. Validate the real deployment path

- [ ] 5.1 Run source CI on main and capture its commit, run/attempt, digest, and
  provenance evidence.
- [ ] 5.2 Confirm GHCR permits anonymous pulls of the exact digest from K3s.
- [ ] 5.3 Recheck the production rollout lease and current Host Agent
  tools/list; confirm exact opaque identity and canonical cluster URI.
- [ ] 5.4 Run the private workflow and prove recipe hash validation, durable
  Host Agent terminal success, exact digest on the Deployment, and Ready Pod.
- [ ] 5.5 Verify external www and apex build markers match source SHA, run, and
  attempt; verify Platform and MCP routes remain separate and healthy.
- [ ] 5.6 Record two-node Ready membership separately from site readiness; make
  no two-node write-HA claim.
- [ ] 5.7 Preserve redacted structured evidence and the private workflow run
  artifact or summary.

## 6. Review, publish, and close

- [ ] 6.1 Run all repository checks and inspect both diffs for secrets,
  unrelated changes, workflow permissions, and deployment-boundary regressions.
- [ ] 6.2 Commit and push the source and private-controller changes on reviewable
  branches; open and review pull requests where branch policy allows.
- [ ] 6.3 Merge approved changes, rerun the deployment from the merged
  controller, and confirm the served build marker still matches the selected
  immutable digest.
- [ ] 6.4 Update this checklist with evidence links and close only after every
  acceptance gate above passes.
