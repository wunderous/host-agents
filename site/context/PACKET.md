# Context packet - Host Agent docs and marketing site

## Documentation source and invariant

The Bun generator in site/scripts/generate-docs.ts owns rendered pages, search index, sitemap, OpenAPI downloads, llms.txt, and this packet. Do not hand-edit generated HTML.

The active public-documentation-release-parity decision in .agents/decisions/public-documentation-release-parity.json is authoritative for release claims. The default tutorial and stable catalog require matching published-package read-only canary evidence. Unverified candidates are marked preview. The catalog snapshot is an allowlisted projection; live tools/list is authoritative at runtime.

## Audience jobs

- First success: /docs/get-started/
- Client setup: /docs/mcp-clients/
- Compatibility evidence: /docs/compatibility/
- Troubleshooting: /docs/troubleshooting/
- Capability reference: /docs/capabilities/
- Use cases: /use-cases/
- Product boundary: /docs/concepts/#host-agent-and-platform
- Architecture and trust: /docs/architecture/ and /docs/resources/

## Ownership boundary

Host Agent executes explicit typed capabilities against one host. Opute Platform owns intent, authorization, routing, and durable orchestration across hosts. Public content and image builds live in this repository; private deployment credentials and production rollout live in the private opute-site-deploy repository. Public workflows must not gain private deployment access.

## Release metadata

Generated reference metadata is read from site/context/release-catalog.json. Change its release channel to stable only with matching package version, source revision, catalog revision, and passing published read-only canary. Recompute decision anchors when an anchored authority file changes.
