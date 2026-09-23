# Docs research synthesis — peers → Opute criteria

Sources reviewed (2026-09-21): Diátaxis; Canonical / Incus / Pulumi hubs; MCP
server docs patterns; YC landing-page clarity guidance (first-five-seconds,
customer-as-hero, one primary CTA, show the product).

## YC-caliber clarity bar (funding-grade docs)

A skeptic partner should answer in five seconds: what it is, who it is for,
what to do next. Docs must also be accurate enough that an operator trusts them
with a production host.

| Bar | Evidence on this site |
|-----|------------------------|
| Plain-language headline (ICP, not protocol soup) | Home `h1` |
| One primary CTA | Get started; Docs secondary |
| Product shown in first viewport | Terminal launch snippet |
| Who / not-for stated early | Docs hub + pitch section |
| First success in ≤10 minutes | Get started npm path first |
| Modes pure (Diátaxis) | Tutorial / how-to / reference / explanation |
| Live catalog is authority | Capabilities + tools-list capture |
| Dogfood proof without Platform confusion | `/docs/dogfood/`, dedicated tunnel |
| Agent-readable index | `/llms.txt`, OpenAPI edge |

## Peer checklist (target ≥95%)

1. Modes pure
2. Hub pitch + map
3. First-success tutorial with explicit success check
4. Install + MCP client + dogfood how-tos
5. Troubleshooting how-to
6. Capabilities with group purpose + live-catalog primacy
7. Configuration complete
8. Recipe why + primitives reference
9. Architecture / networking / resources with diagrams
10. Security: auth, redaction, mutations, Platform isolation
11. Related links on pages
12. Live www + apex; Platform untouched
13. Writing rules in PACKET.md
14. `llms.txt`
15. In-site search
16. i18n chrome
17. OpenAPI HTTP edge

## Status (2026-09-21)

Structural peer checklist: **17/17**. YC clarity rewrite applied to home, docs hub,
get-started, and concepts. Confidence vs funding-grade bar: iterate until a
cold reader can restate the product and complete get-started without jargon
lookup.

## Follow-up audit (2026-09-22)

A content-accuracy pass against the current catalog snapshot and repository
protocol guide found four gaps that the structural checklist did not catch:

- The capabilities page had 159 entries but only 156 unique names; 31 of the
  snapshot's 187 tools were missing.
- The architecture diagram showed the legacy `initialize` handshake; the
  current Host Agent protocol uses MCP 2026-07-28 `server/discover`.
- Networking text referred to four definitions while its table listed three;
  `mesh-runtime.v1` handles runtime setup and the table covers three service
  definitions.
- The tutorial offered both npm and from-source paths, and the generator used
  checkout-specific paths with a silent zero-tools fallback.

The changes for this pass covered each finding. The generator now validates
the catalog snapshot and requires the capabilities page to list every captured
tool exactly once. It resolves source and output paths relative to itself.

Validation at that stage: generation succeeded when invoked from a temporary
directory; the rendered capabilities page matched all 187 captured names; all
local links in 16 generated HTML pages resolved; and the homepage, tutorial,
hub, architecture, capabilities, and networking pages were reviewed in a local
browser preview. The corrections were later pushed and published; see Final
verification below.

## Live-site and search metadata follow-up (2026-09-22)

A browser review of `https://www.opute.io` confirmed the deployed pages had not
incorporated the pushed content fixes:

- `/docs/capabilities/` still exposed a host identity and omitted the catalog
  capture date.
- `/docs/architecture/` still showed the retired `initialize / capabilities`
  exchange instead of the current stateless MCP discovery flow.
- `/docs/networking/` listed three Service Definitions but said “four
  definitions above.”
- `/docs/get-started/` still split the tutorial between npm and source-install
  paths.
- `robots.txt` returned nginx 404.

The source audit found every docs page advertised `/docs/` as its English
alternate, even for other routes, and advertised a Spanish alternate while only
the navigation chrome is translated. Pages also lacked canonical, Open Graph,
and Twitter metadata; descriptions were generic. The generator now emits a
canonical URL and unique description for each page, shares descriptions with
the search index, removes the inaccurate language alternates, and generates
`robots.txt` plus a sitemap for all 16 canonical HTML routes.

Validation: generation succeeded when invoked from `/tmp`; all 16 generated
HTML pages have unique descriptions and matching canonical / social URLs; all
local links resolve; the sitemap contains exactly the 16 canonical pages; and
the capabilities page contains no host identity. A focused `site:` web search
did not surface an Opute result, but that is not proof of non-indexing; Search
Console data was not available. At the time of this audit, production deployment
and live verification had not occurred; see Final verification below.

## Accessibility and live experience follow-up (2026-09-23)

A fresh browser accessibility-tree review of the live homepage and tutorial
found each ordered step exposed two copies of its number (for example,
`1 1 Run the agent`). The HTML kept native `<ol>` semantics while CSS inserted
another visible number through `::before`. The source now keeps the ordered
lists and moves each decorative number into an `aria-hidden` span. Visual
numbering remains while assistive technology receives the list position once.

The homepage's first viewport presents one primary “Get started” action, a
secondary Docs link, and a terminal launch example. Its language banner explains
that the page prose remains English. At this stage, live checks still showed the
old content and robots.txt 404 responses. The corrected source and production
publish were pending at that point; see Final verification below.

## Final verification (2026-09-23)

The site updates are live on main at commit
680dcfe2d908db6d100740f5cabf38a2b7572d2d. CI passed in
[run 35824629522](https://github.com/wunderous/host-agents/actions/runs/35824629522)
and Publish passed in
[run 35824629472](https://github.com/wunderous/host-agents/actions/runs/35824629472).

The committed host-local recipe (site/recipes/www-opute-io.yaml, SHA-256
6d5ff4815ecb2c18a75fc2b6c2f20ca2b6c74fa2423da1bf96d43a9c33376966) validated
against the live 187-tool Host Agent catalog. Run
89f0e1cf-f784-4866-aaeb-bdc3c94fc8c3 completed; build, apply, tunnel, www probe,
and apex probe nodes were all satisfied. The Deployment is generation 15 with
the expected docs-9cdbc40 image and one updated, ready, available replica. The
site pod, dedicated tunnel connector, and registry pod were Running and Ready.

The initial tunnel-route plan node exposed a mismatch in its readiness check:
probe-host-tunnel calls a Host Agent HTTP probe against a Kubernetes-only
service name and tries to mint an MCP token for a static docs site. Its direct
read-only result reported routed: true, while those internal readiness probes
were not applicable; inside the plan, the nested call also hit a declared host
capacity rejection. The recipe now gates on the user-visible outcome instead:
after tunnel setup, both public site roots must pass probe_http_endpoint. The
run recorded HTTP 200 for https://www.opute.io/ and https://opute.io/.

Final public checks found no failures:

- The sitemap contains exactly 16 unique canonical routes. Every route returned
  HTTP 200, has one H1, a unique title and description, and matching canonical
  and Open Graph URLs.
- robots.txt and sitemap.xml returned HTTP 200 on both apex and www; each robots
  file names the www sitemap. llms.txt and openapi.json returned HTTP 200.
- The live capabilities page contains every name from a fresh 187-tool catalog
  comparison, with zero missing names. The architecture page documents
  server/discover; networking describes three Service Definitions and marks
  network-overlay.* as deprecated.
- The accessibility tree exposes each tutorial step number once. The homepage
  presents one primary Get started action, a secondary Docs link, and a terminal
  launch example in its first viewport.

Search Console and site analytics were not available. The earlier focused site:
search is not proof of indexing, and these checks do not establish search
ranking or conversion impact.

## French language follow-up (2026-09-23)

The documentation site's language selector now offers English and French. The
French locale translates the navigation, search placeholder, controls, footer,
and status banner; documentation page prose remains English, as the banner
states. A live browser check confirmed French was selected at `?lang=fr`, with
French navigation and search text. Opening the legacy `?lang=es` URL normalized
to `?lang=fr`; the live selector had no Spanish option.

The French source change is commit `04b0e3bed7b63d828a030463ebff895d5d14f5e0`.
It is live in image
`10.0.100.66:30500/opute/host-agent-www:sha-1a90be26c5409393c8b9b836fa503ecad3e8b31a`.
The `host-agent-www` Deployment is on generation 16 with one updated, ready,
available replica, and the namespace returned one ready site pod. Typed public
probes returned HTTP 200 for both `https://www.opute.io/` and `https://opute.io/`.
CI passed in [run 35828895146](https://github.com/wunderous/host-agents/actions/runs/35828895146)
and Publish passed in [run 35828895074](https://github.com/wunderous/host-agents/actions/runs/35828895074)
for the pushed main state.

The host-local site recipe's broad image-readiness checks had reported success
without applying the French image. An attempted exact-image assertion could not
work because recipe assertion values do not interpolate input variables; that
unsupported edit was reverted. The verified French rollout used task-aware,
typed Host Agent build and apply calls, followed by an exact Deployment image
read-back and public endpoint probes.
