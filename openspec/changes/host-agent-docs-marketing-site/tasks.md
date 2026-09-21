# Tasks

## Planning authority

- [ ] Confirm this change does not revise `public-host-exposure` requirement
  text; dogfood consumes the existing contract (Cloudflare coexistence
  preserved).
- [ ] Record the availability-boundary statement from `design.md` Decision 3
  in the dogfood docs page copy checklist (site reachability ≠ two-node write
  HA).

## Static site package (`docs-marketing-site`)

- [ ] Produce the initial **context packet** (audience jobs, source inventory
  with precedence, Host Agent vs Platform boundaries, verification hooks,
  redaction rules) before drafting site copy.
- [ ] Capture a redacted live `tools/list` (or contract index) excerpt into
  the packet when documenting capability or tool names.
- [ ] Derive the route map from the packet’s audience jobs; drop topics with
  no verified source.
- [ ] Create the in-repo static site tree (e.g. `site/`) with routes for `/`,
  `/docs`, `/docs/install`, `/docs/mcp-clients`, `/docs/capabilities`, and
  `/docs/dogfood`.
- [ ] Implement marketing home as one composition: Host Agent brand primary,
  one headline, one supporting sentence, one CTA group, one dominant visual.
- [ ] Author install and MCP client docs only from packet sources (README /
  serve flags first); use placeholders for tokens and URLs.
- [ ] Label any Platform references as Platform context; never as Host Agent
  install truth.
- [ ] Add a pinned static build command that emits an artifact directory plus
  content digest.
- [ ] Add a content lint / secret-pattern gate over site sources, context
  packets, and built HTML.
- [ ] Add a CI or local gate that fails if the build requires Platform or Host
  Agent MCP to render pages.

## Dogfood deploy path (`site-dogfood-deploy`)

- [ ] Author a checked-in Host Agent recipe (`host-recipe.v1` preferred)
  with stable `recipeId`, inputs for exact Host Agent id and admitted
  cluster URI, and steps for publish → apply → expose → external GET.
- [ ] Reject / document non-conformance: scripts may invoke the recipe but
  MUST NOT sequence cluster mutations themselves.
- [ ] Wire typed publish of the site artifact via recipe steps (Host Agent
  build/push or equivalent) and capture digest + image reference in evidence.
- [ ] Apply a namespaced static-site workload via typed recipe capabilities —
  no consumer `kubectl`.
- [ ] Bind public HTTPS through provider-neutral public-host-exposure recipe
  steps; record origin, provider generation, and stability classification.
- [ ] Implement external GET acceptance as a recipe readiness/evidence step:
  HTTP success + expected content marker; treat apply-only success as
  non-pass.
- [ ] Implement ownership-scoped teardown as recipe teardown or a paired
  cleanup recipe for generation-owned workload, exposure binding, and
  artifact tag only.

## Validation and evidence

- [ ] Produce evidence artifact (JSON or markdown) with Host Agent id, cluster
  URI, artifact digest, public origin, external GET status, and redacted
  errors — no tokens.
- [ ] Run repository format/test baselines touched by the site package and
  any Go/script helpers introduced for dogfood.
- [ ] Record blocked/unavailable outcomes (missing cluster, exposure not
  ready, wrong content) as explicit non-pass states in the evidence file.

## Non-goals checklist (do not implement)

- [ ] Do not host Platform UI or authenticated apps on this origin.
- [ ] Do not expose Host Agent MCP on the public site origin.
- [ ] Do not claim two-node write HA from site dogfood pass.
