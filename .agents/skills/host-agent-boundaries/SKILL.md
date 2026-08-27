---
name: host-agent-boundaries
description: Use when changing Host Agent identity, runtime kind (vm vs container), provider-neutral MCP publication, agentic E2E evidence, relay ownership, or recipe/runtime teardown. Complements cordis-go for dated patterns and anti-patterns.
---

# Host Agent boundary patterns

Keep the Host Agent an execution boundary. Opute owns user intent, retrieval,
authorization, and semantic outcomes. This repository preserves typed MCP
calls, provider results, task state, cancellation, and observations.

Load `cordis-go` for the C-01–C-24 catalog. Authorities for identity and
runtime kind are `docs/adr/0006-canonical-host-agent-identity.md` and
`docs/adr/0007-runtime-kind-and-e2e-target-boundaries.md`.

## Identity

Each running Host Agent has exactly one explicit, opaque, immutable
`OPUTE_REMOTE_AGENT_ID`. That exact value is the only routing, ownership,
session, inventory, and canonicalization key. Fingerprints, instance IDs,
hostnames, provider IDs, and display names are admission evidence only.
Missing, stale, conflicting, or ambiguous identity fails closed.

## Runtime kind

`vm:` and `container:` are not interchangeable. Inspect the resource returned
by `provision_vm` before chaining Kubernetes or workload operations. Canonical
default profile is **2 vCPU / 2 GiB**. `provision_vm` and K3s automation
default to an Incus system container; `create_vm` or
`instanceType=virtual-machine` is required for QEMU. On this WSL2 host,
nested KVM is healthy but local Incus/QEMU-on-Hyper-V fails above about
**2815MiB** of guest memory.

## Provider-neutral Cordis/MCP

- Kubernetes is the Host Agent surface; K3s is a provider implementation.
- Retired names leave the tree in the same change (`retired-capability-name-removal`).
- Completion needs production evidence: literal prompt/response, both terminal
  SSE stages, correlated trace free of provider/model/credential metadata
  (`production-completion-evidence`).
- Provider adapters are generation-bound. Candidate adapters stay isolated
  until readiness and catalog publication succeed.

## Agentic E2E

Require the literal user request, parsed outbound model/tool trace, exact
arguments, paired structured result, complete terminal SSE, and zero SSE
errors. Health, HTTP 200, assistant prose, or a successful Host Agent
`tools/call` alone is not proof of chat closure.

Keep CPC bearer, Host Agent host-issued tokens (`oha_*`), and public MCP user
session (`opsess_*`) distinct. Do not forward public credentials to the local
listener. Product `opha_*` tokens must not open host `/mcp`.

## Additional resources

- Dated Host Agent findings: [dated-boundaries.md](references/dated-boundaries.md)
- Opute-side 2026-08-23 findings: [opute-validation-findings.md](references/opute-validation-findings.md)
- Control-plane host integration: sibling `opute/.agents/skills/host-agent`
