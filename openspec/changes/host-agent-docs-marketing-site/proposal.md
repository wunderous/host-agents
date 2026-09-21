# Proposal: Host Agent docs and marketing site (dogfood-hosted)

## Why

The Host Agent is the product surface an operator meets first — a typed MCP
executor that can stand up guests, Kubernetes, and public ingress — yet it has
no public documentation or marketing site of its own. README fragments and
sibling Platform docs do not prove that an operator can install the agent,
create a cluster, and host a real workload with the same capabilities the
product advertises. A static docs-and-marketing site, served from a K3s
cluster that the Host Agent itself configured, is the smallest honest
demonstration of that claim.

## What Changes

- Author a static documentation and marketing website for the Opute Host
  Agent (product story, install, MCP client setup, capability overview, and
  dogfood topology narrative).
- Require a documented **context-gathering** process before writing or
  reorganizing docs: inventory repository-truth sources, live capability
  discovery, and audience jobs; forbid inventing flags, tools, or topology
  from memory or Platform-only docs.
- Keep the site fully static at build time: HTML/CSS/assets (and optional
  client-side navigation only). No application server, no runtime database,
  no Platform dependency for serving content.
- Specify a Host Agent **recipe** dogfood path: the recipe builds/publishes
  the site artifact, deploys it onto a K3s cluster admitted through Host
  Agent capabilities, exposes it through the existing provider-neutral
  public-host-exposure seam, and records external GET readiness. Scripts
  MUST NOT replace the recipe as the deploy path of record.
- Treat a successful external HTTP fetch of the published site as dogfood
  evidence that Host Agent can host its own documentation — not as a claim of
  two-node write availability or Platform parity.
- Preserve Cloudflare as an interchangeable public-ingress provider; this
  change does not prefer Tailscale Funnel or remove Cloudflare coexistence.

## Capabilities

### New Capabilities

- `docs-marketing-site`: the static Host Agent documentation and marketing
  website — context-gathering and IA rules, content obligations, static-build
  contract, and brand/accessibility constraints.
- `site-dogfood-deploy`: a repository-owned Host Agent recipe that publishes,
  applies, exposes, and proves the static site on a K3s cluster, including
  ownership-scoped teardown and external readiness evidence.

### Modified Capabilities

- (none) — public ingress continues to use existing `public-host-exposure`
  requirements; this change consumes them and does not revise their
  requirement text.

## Impact

- Host Agent repository: a new static site package (or `site/` / `docs-site/`
  tree), build tooling, and a checked-in Host Agent recipe that calls only
  typed catalog capabilities — no ad-hoc `kubectl` or shell cluster mutation
  as the deploy path.
- Existing K3s and public-host-exposure providers: reused as-is for cluster
  admission, workload apply, and public HTTPS binding.
- Validation: static build gate, artifact identity evidence, cluster deploy
  evidence, and an external GET of the published origin that returns the
  expected marketing/docs content.
- Availability boundary (explicit): hosting the site on one or two K3s
  servers does **not** claim etcd-quorum HA or write continuity after a
  member loss. Dogfood success is “site reachable via Host Agent–managed
  ingress,” not “two-node consensus HA.”
- Cloudflare coexistence (explicit): Cloudflare remains a valid
  public-host-exposure provider alongside any Tailscale Funnel path; the
  site recipe MUST select the provider-neutral exposure contract only.

## Non-goals

- Replacing `platform.opute.io` or shipping Platform UI through this site.
- Making the docs site a dynamic CMS, chat surface, or authenticated app.
- Storing Host Agent tokens, overlay credentials, or kubeconfigs in the
  static site or OpenSpec artifacts.
- Claiming that dogfood hosting proves full Platform self-hosting or
  two-node write availability.
- Using an operator script or raw kubectl as the website deploy path of
  record (the recipe is required).
