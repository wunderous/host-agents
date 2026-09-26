# Context packet - Host Agent docs and marketing site

## Documentation source and invariant

The Bun generator in site/scripts/generate-docs.ts owns rendered pages, search index, sitemap, OpenAPI downloads, llms.txt, and this packet. Do not hand-edit generated HTML.

The active public-documentation-release-parity decision in .agents/decisions/public-documentation-release-parity.json is authoritative for release claims. The first-success tutorial and canonical capability reference use the newest stable catalog with matching published-package read-only canary evidence. Unreleased candidates live on a visibly labeled preview route. An archived release catalog is created from the exact catalog and package metadata at its published source SHA, matched to that release's canary evidence, and is immutable afterward. The catalog snapshot is an allowlisted projection; live tools/list is authoritative at runtime.

## Audience jobs

- First success: /docs/get-started/
- Client setup: /docs/mcp-clients/
- Compatibility evidence: /docs/compatibility/
- Troubleshooting: /docs/troubleshooting/
- Capability reference: /docs/capabilities/
- Use cases: /use-cases/
- Product boundary: /docs/concepts/#host-agent-and-platform
- Kubernetes availability and failure scope: /docs/availability/
- Architecture and trust: /docs/architecture/ and /docs/resources/

## Ownership boundary

Host Agent executes explicit typed capabilities against one host. Opute Platform owns intent, authorization, routing, and durable orchestration across hosts. Public content and image builds live in this repository; private deployment credentials and production rollout live in the private opute-site-deploy repository. Public workflows must not gain private deployment access.

## Release metadata

Generated candidate metadata is read from site/context/release-catalog.json. Change its release channel to stable only with matching package version, source revision, catalog revision, and passing published read-only canary. Catalog capture drops stable status and old canary evidence whenever either the package version or catalog revision changes. The tutorial selects the newest verified stable release while the current candidate remains preview. Recompute decision anchors when an anchored authority file changes.

Wrap every versioned @opute/host-agent@VERSION token emitted into HTML with Cloudflare's <!--email_off--> and <!--/email_off--> suppression comments using the shared generator helper. Cloudflare can otherwise rewrite this scoped package token as an email address, breaking what visitors see, hear, or copy. The generated-site validator checks active and archived releases; a public deployment must also be checked in a browser after edge transformation. Do not change zone-wide Cloudflare settings from this repository.
