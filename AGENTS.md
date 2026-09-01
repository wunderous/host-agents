# Repository Guidelines

Always-on index for the Go Host Agent. Domain procedures live in
`.agents/skills/`. Cursor/cloud entrypoints are symlinks to this file.

## Project Structure & Module Organization

- `cmd/opute-host-agent/` contains the single Go executable entry point.
- `internal/app`, `internal/cli`, and `internal/hostmcp` own runtime startup,
  command modes, and MCP serving.
- `internal/domain/*` owns execution, one package per domain (`incus`, `host`,
  `kubernetes`, `llm`, `oci`, `postgres`, `cluster`, `serving`). A domain never
  imports another domain: cross-domain needs are declared as a `Deps` struct of
  narrow seams stated in primitives, and a `no-cross-domain-<name>` depguard rule
  enforces it. `internal/hostagent` is the composition root that builds them and
  owns no operations; `internal/hostruntime` is the shared seam they all take,
  bounded by the three-part membership rule its tests enforce.
- `internal/tools`, `internal/provider`, `internal/plan`, and `internal/session`
  implement typed capabilities, plans, and durable session contracts.
- `schemas/` stores versioned capability contracts; `test/` contains contract,
  integration, standalone, and mode tests. `npm/local-host-agent/` is the
  release launcher.

## Build, Test, and Development Commands

```bash
gofmt -w .                 # format Go sources
go vet ./...               # static checks
go test ./...              # full Go suite
make build                 # build dist/opute-host-agent
make artifacts             # build Linux release archives and checksums
make standalone-smoke      # validate isolated startup configuration
make standalone-http-smoke # run packaged HTTP smoke tests
make npm-test              # test the npm launcher
```

For local client work, connect an external MCP client to the Host Agent
endpoint. This repository's binary is server-only; use `serve` (or the bare
invocation) when an external MCP client needs the Host Agent.

## Skills

| Skill | When to load |
|-------|-------------|
| **reflect** | Concluding non-trivial work, after debugging obscure bugs/quirks, or when leaving explanatory inline comments and elevating permanent invariants. |
| **inline-context-discipline** | Reading/authoring code comments, handling edge cases, workarounds, or wire/protocol boundaries. |
| **codex-wsl** | Configuring, diagnosing, or executing OpenAI Codex CLI in WSL or across the Windows-WSL boundary, Host Agent MCP integration, and headless execution. |
| **cordis-go** | `internal/cordis`, `internal/cordis/mcp`, `internal/hostmcp`, provider generations, C-01–C-24 catalog. Normative guide: [`docs/cordis-development-guide.md`](docs/cordis-development-guide.md) |
| **host-agent-boundaries** | Identity, runtime-kind (`vm:` vs `container:`), provider-neutral MCP, E2E evidence, relay ownership. ADRs 0006 and 0007 |
| Sibling **opute/.agents/skills/host-agent** | Control-plane enrollment, HTTP liveness/reconciliation, dogfood recovery |
| Sibling **opute/.agents/skills/agent-work-coordination** | Beads ledger — native Windows is authoritative |
| Sibling **opute/.agents/skills/shared-runtime-leases** | Shared WSL/Incus/dev-stack/production-roll ownership |
| Sibling **opute/.agents/skills/permanent-agentic-invariants** | Capture or supersede durable invariants |

## Coding Style & Naming Conventions

Use idiomatic Go formatted by `gofmt`; use PascalCase for exported identifiers,
camelCase for locals, and wrap errors with context. Keep MCP schemas, catalog
metadata, authorization, and dispatch behavior typed and revision-consistent.
Use `opute-host-agent` for the binary and service name. Avoid product-specific
routing, hidden provider discovery, hard-coded credentials, and name-based ID
guessing.

## Testing Guidelines

Name tests `Test<Behavior>` and keep focused tests beside the package under
test. Preserve protocol and standalone tests for pipes and automation;
Interactive client coverage belongs in the owning client project. Capability
changes should include contract and standalone coverage where applicable.

## Commit & Pull Request Guidelines

Use concise conventional prefixes reflected in history, such as `feat:`,
`fix(host):`, and `docs:`. Pull requests should explain the behavior change,
list validation commands, call out configuration or schema changes, and include
terminal screenshots or recordings for significant client changes. Do not commit
generated `dist/` artifacts, secrets, or unrelated formatting changes.

## Architecture & Safety

Opute owns intent, authorization, and durable orchestration; this repository
executes explicit typed host capabilities. Host MCP inventory tools
(`list_vms`, `list_clusters` / `list_kubernetes_clusters`) omit caller
`hostId` — identity is `OPUTE_REMOTE_AGENT_ID` or a wire prefix. Preserve
standalone isolation and fail-closed validation. Shared WSL services, listeners, Incus capacity, and
production-like rollouts may be used by other worktrees — load
`shared-runtime-leases` before restarting or mutating shared runtime
resources. Canonical new-resource profile is **2 vCPU / 2 GiB**.

For the normative Cordis architecture, LLM/tool authority rules, MCP
2026-07-28 boundary, invariant catalog, five-whys debugging discipline, and
E2E release gate, read
[`docs/cordis-development-guide.md`](docs/cordis-development-guide.md) and load
`cordis-go` before changing `internal/cordis`, `internal/cordis/mcp`,
`internal/hostmcp`, or provider lifecycle code.

For every plan, refactor, new capability, schema change, lifecycle change, or
agentic boundary change, capture the invariant delta before implementation.
Read the
[permanent invariant capture guide](../opute/.agents/guides/permanent-agentic-invariants.md).

### Provider-neutral Cordis/MCP rules (2026-08-24)

These are routing rules; the authoritative invariant is the Host Agent
[Cordis development guide](docs/cordis-development-guide.md) and the sibling
Opute decision record `provider-neutral-cordis-mcp-boundary`. Load
`host-agent-boundaries` for identity, runtime-kind, E2E evidence, and relay
ownership.

- Concrete providers implement neutral typed capabilities. Kubernetes is the
  Host Agent surface; K3s is a provider implementation.
- A completed migration removes the retired name (`retired-capability-name-removal`).
- Completion on a chat or provisioning path needs production evidence
  (`production-completion-evidence`).
- Provider adapters are generation-bound. Durable evidence uses schema-driven
  redaction; unknown projections fail closed.
- Boundary claims require wire, lifecycle, cleanup, and durable-state evidence.
