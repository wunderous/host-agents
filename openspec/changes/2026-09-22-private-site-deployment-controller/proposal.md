# Proposal: Private deployment controller for www.opute.io

## Why

The Host Agent source repository is public. Giving a self-hosted runner access to
that repository would let public pull request workflows execute on the machine
that runs the Host Agent. Source image production and deployment authority must
therefore have separate repository and runner boundaries.

## What changes

- Public repository wunderous/host-agents owns site content and builds,
  versions, and publishes the static OCI image on GitHub-hosted runners.
- Private repository wunderous/opute-site-deploy owns the deployment workflow,
  Host Agent recipe, Kubernetes manifest, target selection, rollout, and
  evidence.
- The private workflow polls the public image workflow on main every five
  minutes. It accepts only a completed successful run and resolves its
  run-and-attempt image tag to a sha256 digest. A manual dispatch may select a
  successful main run for immediate deployment or rollback.
- The runner is registered only to the private repository and runs as the same
  WSL user as the Host Agent. Host Agent credentials remain in the local
  process environment and are never copied into GitHub secrets.
- Success requires terminal Host Agent recipe success, the exact digest on a
  Ready workload, and external www/apex build-marker checks. Platform routes
  remain separate.

## Invariant delta

1. Public source cannot deploy: no self-hosted runner, Host Agent credential,
   deployment recipe, or cluster manifest lives in the public source repo.
2. The private controller is the sole owner of recipe, manifest, source-run
   selection, rollout, and deployment evidence.
3. The controller passes the exact opaque OPUTE_REMOTE_AGENT_ID read from the
   local Host Agent process. Hostnames, machine IDs, node IPs, and display
   labels are not identity fallbacks.
4. All cluster mutations remain typed Host Agent recipe steps. The controller
   may discover, validate, run, and inspect the recipe, but cannot substitute
   kubectl, Helm, Incus, or provider CLIs.
5. A reachable site on two Ready K3s servers does not prove quorum-backed write
   availability.

**Owner:** private repository for deployment; public source repository for
image production; Host Agent for typed execution.
**Authority:** this OpenSpec change, the private repository decision record,
and its verifier.
**Evidence:** public workflow run and image digest; fresh tools/list; exact host
identity and cluster URI; recipe validation and terminal plan state; deployed
image and Pod readiness; external build markers; unchanged Platform ingress.
**Exception:** only a manual selection of an older successful main run can
deploy older content. Source run failures and pending states never fall back.
**Revision behavior:** deployments always consume a manifest sha256 digest.
Workflow, recipe, manifest, identity, or evidence changes require updating the
decision record and verifier.

## Explicit availability and ingress boundaries

The existing two Ready K3s servers are membership evidence only. Site
reachability is not etcd write HA. The site retains its dedicated public
exposure binding. Cloudflare remains a supported provider; this change does
not remove provider coexistence.

## Non-goals

- Deploying Platform UI or exposing Host Agent MCP on the site hostname.
- Building the image on the private deployment runner.
- Storing Host Agent, tunnel, registry, or cross-repository credentials in a
  repository, workflow artifact, or static output.
