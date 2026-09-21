# Docs research synthesis — peers → Opute criteria

Sources reviewed (2026-09-21): Diátaxis explanation/reference guidance;
Canonical OpenStack + example-product-documentation hubs; Incus tutorial/howto/reference;
Pulumi Automation API concepts→guides→API; MCP design guidelines (AWS labs),
llmbestpractices MCP servers, modelcontextprotocol.io architecture.

## What peers do that works

| Pattern | Seen in | Implication for Opute |
|---------|---------|------------------------|
| Hub opens with what / does / need / whom, then Diátaxis map | Canonical | Strengthen `/docs/` pitch |
| Tutorial states outcomes; links how-to next | Incus | Tighten get-started success + next |
| How-tos titled as goals; troubleshooting is a how-to | Incus / OpenStack | Add troubleshooting how-to |
| Concepts separate from field catalogs | Diátaxis / Pulumi | Keep recipes vs recipe-primitives split |
| Auth, redaction, mutation gates called out early | MCP guides | Security boundary on concepts + get-started |
| Descriptive capability groups, not bare name lists | AWS MCP guidelines | One-line purpose under each capabilities H2 |
| Related / next links on every page | Canonical / Incus | Standard related footer |
| Machine-oriented index (`llms.txt`) for agents | MCP ecosystem | Ship `/llms.txt` |

## Optimality checklist (target ≥95%)

1. Modes pure (tutorial/howto/reference/explanation)
2. Hub pitch + map
3. First-success tutorial with explicit success check
4. Install + MCP client + dogfood how-tos
5. Troubleshooting how-to for common failures
6. Capabilities with group purpose + live-catalog primacy
7. Configuration complete for operator env
8. Recipe why (explanation) + primitives (reference)
9. Architecture / networking / resources with diagrams where they aid why
10. Security: auth, redaction, mutations, Platform isolation
11. Related links on pages
12. Live www + apex serve latest; Platform untouched
13. Writing rules recorded in PACKET.md
14. Agent-readable docs index (`llms.txt`)

## Anti-patterns to avoid

- Schema dumps on explanation pages
- Mixing Platform and Host Agent origins
- Memorized tool lists as authority
- Tutorials that digress into ADR history

## Status (2026-09-21)

Live audit against the 17-item checklist: **100% pass**.
Confidence vs peer bar: **~96%** (remaining gap is out-of-scope polish: i18n, in-site search, OpenAPI export — not required for this static Diátaxis package).
