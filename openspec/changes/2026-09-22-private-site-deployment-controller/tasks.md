# Tasks: Private site deployment controller

All implementation, review, merge, and end-to-end acceptance gates are
complete. The evidence links below point to the merged changes and successful
deployment. Credentials remain outside this repository.

## 1. Lock the repository boundary

- [x] 1.1 Add the invariant decision record to the private deployment repo and a
  verifier that checks its repository, workflow, recipe, manifest, and evidence
  anchors.
- [x] 1.2 Review the public source repo for self-hosted runner use, Host Agent
  credentials, deployment recipes, cluster manifests, or controller scripts;
  record the before/after file list in `boundary-inventory.md`.

## 2. Build and publish from the public source repository

- [x] 2.1 Add a GitHub-hosted image workflow that builds the static site only,
  publishes public GHCR image tags for source SHA and run/attempt, and emits
  provenance for the resulting digest.
- [x] 2.2 Add a safe build.json marker with source SHA, run ID, and attempt;
  confirm no secrets or private topology values enter the image.
- [x] 2.3 Remove deployment-owned recipe and manifest files from public source;
  update its documentation and local checks to enforce the boundary.
- [x] 2.4 Validate workflow syntax, source-boundary checks, image build, public
  anonymous digest pull, and provenance subject.

## 3. Create the private deployment controller

- [x] 3.1 Create private repository wunderous/opute-site-deploy and add its
  ownership, threat-boundary, bootstrap, rollback, and evidence documentation.
- [x] 3.2 Add a scheduled/manual controller that selects only an eligible
  successful main source run, with no implicit stale-success fallback.
- [x] 3.3 Resolve the run-and-attempt OCI tag to a sha256 digest and provide only
  that immutable image reference to the checked-in Host Agent recipe.
- [x] 3.4 Add the private host-recipe.v1 recipe and manifest, preserving the
  dedicated site tunnel and Platform ingress/MCP isolation.
- [x] 3.5 Add controller tests for source-run eligibility, digest resolution,
  exact commit/run-attempt provenance binding, fail-closed identity/recipe
  validation, secret redaction, and evidence shape.
- [x] 3.6 Add the private invariant decision record and verify its file anchors.
- [x] 3.7 Run the idempotent recipe on every eligible deployment attempt,
  including when the selected digest is already Ready, to reconcile workload,
  tunnel, and route state.

## 4. Connect the private runner to the local Host Agent

- [x] 4.1 Register a Linux x64 self-hosted runner to the private repo only,
  using the Host Agent Unix user and a repo-specific label.
- [x] 4.2 Run the private-only runner as a systemd user service under the
  non-root Host Agent Unix account, use read-only workflow permissions, and
  confirm the public source repository has no runner registration.
- [x] 4.3 Confirm controller logs and redacted evidence omit the Host Agent
  bearer value and exact private process environment.
- [x] 4.4 Point the private workflow at the host's shared Opute rollout lease
  module and verify it holds the lease across the controller child process.

## 5. Validate the real deployment path

- [x] 5.1 Run source CI on main and capture its commit, run/attempt, digest, and
  provenance evidence.
- [x] 5.2 Confirm GHCR permits anonymous pulls of the exact digest from K3s.
- [x] 5.3 Recheck the production rollout lease and current Host Agent
  tools/list; confirm exact opaque identity and canonical cluster URI.
- [x] 5.4 Run the private workflow and prove recipe hash validation, durable
  Host Agent terminal success, exact digest on the Deployment, and Ready Pod.
- [x] 5.5 Verify external www and apex build markers match source SHA, run, and
  attempt; verify Platform and MCP routes remain separate and healthy.
- [x] 5.6 Record two-node Ready membership separately from site readiness; make
  no two-node write-HA claim.
- [x] 5.7 Preserve redacted structured evidence in the private workflow summary.

## 6. Review, publish, and close

- [x] 6.1 Run repository checks and inspect both diffs for secrets, unrelated
  changes, workflow permissions, and deployment-boundary regressions.
- [x] 6.2 Commit and push the source and private-controller changes on reviewable
  branches; open and review pull requests.
- [x] 6.3 Merge approved changes, deploy from the merged controller, and confirm
  the served build marker matches the selected immutable digest.
- [x] 6.4 Record evidence below and close after every acceptance gate passed.

## Evidence

### Merged changes and source image

- Public source change: [PR #6](https://github.com/wunderous/host-agents/pull/6)
  merged to `main` as `5fb743cec710f99906f1eeb952ae52726daa0ba3`; hosted `verify`
  passed.
- Source image workflow:
  [run 35829774716](https://github.com/wunderous/host-agents/actions/runs/35829774716),
  attempt 1, completed successfully for that `main` commit. It published
  `ghcr.io/wunderous/host-agent-www@sha256:259b61002c02b34e693f4b8d4328a8fb75aea35959eedb38959abacfaf15aeca`.
  The digest's provenance was verified against the source commit and run
  invocation; the private deployment resolved the public digest anonymously.
- Private controller change: [PR #8](https://github.com/wunderous/opute-site-deploy/pull/8)
  merged as `12c0d46d74a22007d6ed6bd17ca489ece6580d97`; hosted controller
  validation passed.

### Successful deployment from merged main

- [Private deployment run 35838241860](https://github.com/wunderous/opute-site-deploy/actions/runs/35838241860)
  completed successfully from the merged controller commit and selected source
  run 35829774716, attempt 1.
- The validated recipe was exactly
  `site/recipes/www-opute-io.yaml`, SHA-256
  `d14e980d91cb831ac98df29f35f5ae06899bb0044974b988864448c2fdb0fee9`.
  The private run's redacted summary records the confirmed Host Agent identity,
  canonical cluster URI, and recipe path/hash without exposing credentials or
  process environment.
- Durable Host Agent plan run
  `fa0fb584-254d-4808-9d4e-e621fdfbff5c` reached terminal `completed`; `apply`,
  `tunnel`, `route`, and `probe-apex` all reported `satisfied`. The Deployment
  used the exact image digest above with desired, updated, ready, and available
  replicas all equal to one, and exactly one site Pod was Ready.
- External `www.opute.io` and `opute.io` responses returned HTTP 200 and their
  build markers matched the selected source SHA, run ID, and attempt. The
  Platform route remained HTTP 200; the MCP route remained separately protected
  (unauthenticated request returned HTTP 401), and Platform ingress hosts were
  unchanged.
- The private run recorded two Ready K3s server members separately from site
  readiness and made no two-node write-availability claim. The private runner
  was online only in the private repository; the public source repository had
  zero registered runners. Its active systemd user service ran as the non-root
  Host Agent Unix account, the workflow token had read-only permissions, and
  the lease wrapper held the shared rollout lease through the controller child
  process.
- The run summary contains the redacted structured evidence. The optional
  Actions artifact upload was rejected because of the account's artifact
  storage quota; the summary remained available and is the retained evidence.

### Validation and review

- Private controller tests: `python3 -m unittest discover -s tests -v` — 27
  passed.
- Private boundary verifier: `python3 scripts/verify_deployment_boundary.py` —
  10 anchors verified.
- Private CLI help and `git diff --check` passed. Public source CI and both
  merged pull-request checks passed.
- The source and controller diffs were reviewed for workflow permissions,
  credentials, boundary violations, and unrelated changes. The review caught
  and removed a stale required-tool dependency on the separate
  `probe-host-tunnel` callback; the merged recipe uses direct in-plan public
  route probes, and the real deployment passed afterward.
