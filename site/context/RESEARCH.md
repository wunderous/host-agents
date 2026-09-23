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

The current working-tree changes cover each finding. The generator now validates
the catalog snapshot and requires the capabilities page to list every captured
tool exactly once. It resolves source and output paths relative to itself.

Validation to date: generation succeeded when invoked from `/tmp`; the rendered
capabilities page matched all 187 captured names; all local links in 16 generated
HTML pages resolved; and the homepage, tutorial, hub, architecture, capabilities,
and networking pages were reviewed in a local browser preview. Production
publishing remains pending.
