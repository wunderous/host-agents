# Proposal: Tailscale private mesh and Funnel ingress

## Why

The current public-mesh direction uses Cloudflare for Host Agent rendezvous
and private cluster connectivity. Tailscale is a viable alternate provider when
the operator wants an encrypted private mesh with node identities and a
Tailscale-managed public HTTPS ingress. The alternative must not become a
second, provider-specific orchestration path or quietly turn a two-node
membership result into a claim of consensus HA.

K3s permits two or more server nodes with an external datastore, but embedded
etcd HA requires three or more server nodes. The Tailscale distributed-network
mode is documented as experimental and does not support embedded etcd across
that deployment shape. The spec therefore separates:

- private Tailscale transport between explicitly selected nodes;
- the datastore/quorum mode and its measured availability boundary; and
- public Funnel exposure of an application service.

## What changes

- Define a provider-neutral `network-overlay.v1` service with typed mesh,
  private-service, public-ingress, probe, promotion, and teardown operations.
- Add a Tailscale provider implementation behind that service. It owns node
  enrollment, tags/grants, mesh readiness, Serve, Funnel, and cleanup.
- Keep Cloudflare as an interchangeable provider of the same neutral service.
- Add explicit K3s two-server admission: external datastore mode may claim
  control-plane write continuity only with independent datastore evidence;
  embedded-etcd mode is reported as serving-continuity-only and requires typed
  quorum recovery.
- Make public Funnel exposure explicit and scoped to a local gateway or a
  Tailscale Kubernetes Operator Ingress. Funnel is never used for etcd, the
  Kubernetes API, CNI traffic, or private Host Agent administration.
- Preserve exact opaque Host Agent identities, host-scoped pinning, generation
  affinity, schema-derived redaction, and durable evidence at every boundary.
- Include the architecture visualization as a repository-owned artifact.

## Non-goals

- Removing or weakening the Cloudflare provider.
- Making Funnel a general-purpose public TCP router or a replacement for a
  Kubernetes load balancer.
- Claiming that two K3s servers with embedded etcd automatically preserve
  Kubernetes writes after one server fails.
- Storing Tailscale API keys, auth keys, join keys, or node credentials in
  recipes, prompts, durable task state, logs, or OpenSpec artifacts.
- Implementing the provider in this planning change.

## Impact

- Host Agent: `contracts/`, `internal/cordis/`, `internal/hostmcp/`, provider
  generation/lifecycle, schemas, and a new
  `plugins/tunneling/tailscale/` provider.
- Opute: the provider-neutral recipe and consumer projections in the sibling
  repository; no Tailscale command or credential becomes a model-facing tool
  contract.
- Existing `public-host-exposure`, `two-node-ha-readiness`, and
  `cluster-management-routing` contracts gain explicit Tailscale and Funnel
  boundary requirements.
- Validation: real Streamable HTTP MCP evidence, lifecycle/cleanup evidence,
  two-node failure injection, and externally observed Funnel readiness.
