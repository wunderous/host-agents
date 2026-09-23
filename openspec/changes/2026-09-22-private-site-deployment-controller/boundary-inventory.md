# Repository boundary inventory

Baseline: public `wunderous/host-agents` commit `c64e875`.

## Public source before the split

The following tracked deployment authority existed in the public repository:

- `site/recipes/www-opute-io.yaml`
- `site/recipes/www-opute-io-teardown.yaml`
- `site/manifests/workload.yaml`

## Public source after the split

The public source branch removes those three tracked paths. The repository
retains website content and its static-site Docker build context. Its new
`.github/workflows/publish-site-image.yml` uses GitHub-hosted CI to build and
publish the static-site OCI image; `scripts/check_site_release_boundary.py`
rejects tracked site recipes/manifests, self-hosted workflow runners, and
Host Agent credential references in workflows.

## Private deployment owner

The private `wunderous/opute-site-deploy` repository contains:

- `site/recipes/www-opute-io.yaml` (canonical Host Agent recipe path)
- `deployment/recipes/www-opute-io-teardown.yaml`
- `deployment/manifests/workload.yaml`
- `.github/workflows/deploy-site.yml`
- `.github/workflows/validate.yml` (hosted-only pull-request checks)
- `scripts/deploy_site.py`
- `scripts/with_production_rollout_lease.ts`
- `.agents/decisions/site-deployment-boundary.json`

The private runner is registered only to this repository. No source workflow or
public pull request is sent to the serving host. The public source repository
has no registered self-hosted runners.
