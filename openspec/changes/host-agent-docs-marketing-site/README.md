This change specifies a **static** documentation and marketing website for the
Opute Host Agent, and a **dogfood deploy recipe** that serves that site from a
K3s cluster configured through Host Agent typed capabilities.

The point of the dogfood path is product honesty: the same agent that
operators install can publish and expose its own docs via a repository-owned
recipe. Success is an external HTTP GET of the published origin — not Platform
self-hosting and not two-node write HA.

Read [design.md](design.md) for architecture decisions (including Decision 6
on gathering writing context before drafting). Specs:

- [docs-marketing-site](specs/docs-marketing-site/spec.md) — context
  gathering, IA, static site content and build contract
- [site-dogfood-deploy](specs/site-dogfood-deploy/spec.md) — Host Agent
  recipe for publish, apply, expose, teardown, and evidence

This is a planning artifact until the site package and dogfood recipe land
with evidence.
