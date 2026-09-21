# Context packet — Host Agent docs / marketing site

## Documentation standard

Site docs follow [Diátaxis](https://diataxis.fr/). Operator facts must match
`README.md` and the live `tools/list` catalog after verifying against code.

### Writing rules (keep modes pure)

| Mode | Answers | Voice | Do not |
|------|---------|-------|--------|
| Tutorial | Can you teach me? | Guided steps only | Digress into architecture |
| How-to | How do I …? | Goal → steps | Teach from zero or dump schemas |
| Reference | What is …? | Dry, complete, neutral | Explain *why* or instruct |
| Explanation | Why / about …? | Mental model, analogy, judgment | Absorb field catalogs |

Progressive disclosure: one-sentence answer → diagram → choices → link to reference.
Explanation opens with *about* / *why*; reference opens with facts.

## Audience jobs

| Job | Route | Mode |
|-----|-------|------|
| First success | `/docs/get-started/` | Tutorial |
| Install & run | `/docs/install/` | How-to |
| Connect MCP client | `/docs/mcp-clients/` | How-to |
| Publish this site | `/docs/dogfood/` | How-to |
| Troubleshooting | `/docs/troubleshooting/` | How-to |
| Capability facts | `/docs/capabilities/` | Reference |
| Config facts | `/docs/configuration/` | Reference |
| Recipe & plan fields | `/docs/recipe-primitives/` | Reference |
| Mental model | `/docs/concepts/` | Explanation |
| Architecture + diagrams | `/docs/architecture/` | Explanation |
| Why recipes & plans | `/docs/recipes/` | Explanation |
| Networking seams | `/docs/networking/` | Explanation |
| URIs / admission / redaction | `/docs/resources/` | Explanation |

## Audited truths (2026-09-20)

- Standalone default: `127.0.0.1:3014`; platform default: `0.0.0.0:3004`
- `OPUTE_REMOTE_AGENT_ID` required; npm defaults to `local-host-agent`
- `/mcp` needs Bearer `MCP_AUTH_TOKEN` (or OAuth); `/health` is open
- Mutations denied until `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`
- Live catalog capture: 187 tools in `tools-list.redacted.json` (seams live; network-overlay=0)
- HA networking: mesh-runtime + three seams (ADR-0016); `network-overlay.*` deprecated alias
- Dogfood: dedicated tunnel `opute-www-opute-io`; hostnames `opute.io` + `www.opute.io`

## Boundaries

- Host Agent ≠ Platform (`platform.opute.io` / `mcp.opute.io`)
- Public site MUST NOT expose Host Agent MCP admin
- Deploy path = Host Agent recipe only

## Research

Peer synthesis and optimality checklist: `site/context/RESEARCH.md`
