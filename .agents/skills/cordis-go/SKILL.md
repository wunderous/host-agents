---
name: cordis-go
description: Use when changing Host Agent Cordis kernel code under internal/cordis, internal/cordis/mcp, internal/hostmcp, provider generations, MCP 2026-07-28 transport, or the C-01 through C-24 invariant catalog.
---

# Host Agent Cordis (Go)

Read [`docs/cordis-development-guide.md`](../../../docs/cordis-development-guide.md)
before editing `internal/cordis`, `internal/cordis/mcp`, `internal/hostmcp`,
or provider lifecycle code. That file is the normative invariant catalog
(C-01–C-24), MCP 2026-07-28 requirements, and E2E release gate.

The Host Agent is a typed MCP execution service. Opute owns intent, retrieval,
and semantic outcomes. This repository executes explicit typed assignments.

```text
LLM / Opute Platform
  intent, retrieval, planning, semantic outcome
             │ typed contract
             ▼
Host Agent Cordis kernel
  services, providers, effects, events, policy, lifecycle, evidence
             │ Streamable HTTP MCP 2026-07-28
             ▼
Provider MCP / external system
```

## Binding rules

- Arguments are not orchestrator state. Do not enrich, repair, or guess IDs.
- Intent satisfaction is model-owned. Do not infer `satisfied` from tool success.
- One plan executor: `internal/plan.Runner`. Providers cannot add runners.
- MCP opacity: MCP knowledge stops at `internal/cordis/mcp`.
- Candidate providers stay isolated until atomic activation.
- Durable evidence uses schema-driven redaction. Unknown projections fail closed.

When the change introduces or alters a durable invariant, follow the sibling
Opute skill `permanent-agentic-invariants` and update the owning decision.

## Additional resources

- Full catalog and workflow: [`docs/cordis-development-guide.md`](../../../docs/cordis-development-guide.md)
- Identity / runtime-kind / E2E evidence: `host-agent-boundaries`
- ADR: `docs/adr/0002-provider-extension-architecture.md`
