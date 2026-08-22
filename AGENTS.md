# Repository Guidelines

## Project Structure & Module Organization

- `cmd/opute-host-agent/` contains the single Go executable entry point.
- `internal/app`, `internal/cli`, and `internal/hostmcp` own runtime startup,
  command modes, and MCP serving.
- `internal/tools`, `internal/ops`, `internal/provider`, `internal/plan`, and
  `internal/session` implement typed capabilities, execution, plans, and
  durable session contracts.
- `internal/tui/` contains the Bubble Tea interface and its headless fallback.
- `schemas/` stores versioned capability contracts; `test/` contains contract,
  integration, standalone, mode, and TUI tests. `npm/local-host-agent/` is the
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

For local TUI work, run `OPUTE_INFRA_PROVIDER_ID=incus ./dist/opute-host-agent`.
Use `serve` only when an external MCP client needs a separate server process.

## Beads coordination

Use `scripts/agent-work` for Beads status/start/update/end from WSL; it is
anchored to this checkout and works from either parent directory. The launcher
uses the translated Windows-backed workspace and the native Windows Dolt
listener through the WSL gateway. It never starts a second WSL Dolt server or
creates `.beads` in this checkout. `make agent-work` delegates to the same
launcher and is safe to invoke with `make -f` from a parent directory.

Patterns: keep the host agent and Opute on the same external ledger; use the
launcher or Makefile rather than cwd-relative paths; let WSL remain a client;
and verify the real adapter path with `status --json --all` plus `bd dolt test`.
If metadata is missing or identity mismatches, back up first and fail closed
until the active database has been inspected. Treat endpoint configuration,
the native Windows listener, and a successful adapter read as separate levels
of evidence.

Anti-patterns: do not run `bd init` or `bd dolt start` from WSL, create a
checkout-local `.beads`, use Windows `127.0.0.1` from WSL, hard-code the
gateway/password, rely on `bun.exe` or the caller's cwd, or kill an unverified
port owner. Do not claim native Windows-client parity when interop reports
`UtilAcceptVsock ... accept4 failed 110`; record that probe as pending. See
`../opute/.agents/workflows/agent-work-coordination.md` for the full repair and
verification matrix.

## Coding Style & Naming Conventions

Use idiomatic Go formatted by `gofmt`; use PascalCase for exported identifiers,
camelCase for locals, and wrap errors with context. Keep MCP schemas, catalog
metadata, authorization, and dispatch behavior typed and revision-consistent.
Use `opute-host-agent` for the binary and service name. Avoid product-specific
routing, hidden provider discovery, hard-coded credentials, and name-based ID
guessing.

## Testing Guidelines

Name tests `Test<Behavior>` and keep focused tests beside the package under
test. Preserve headless tests for pipes and automation while exercising the
interactive TUI through `test/tui` or a PTY-shaped test. Capability changes
should include contract and standalone coverage where applicable.

## Commit & Pull Request Guidelines

Use concise conventional prefixes reflected in history, such as `feat:`,
`fix(host):`, and `docs:`. Pull requests should explain the behavior change,
list validation commands, call out configuration or schema changes, and include
terminal screenshots or recordings for significant TUI changes. Do not commit
generated `dist/` artifacts, secrets, or unrelated formatting changes.

## Architecture & Safety

Opute owns intent, authorization, and durable orchestration; this repository
executes explicit typed host capabilities. Preserve standalone isolation and
fail-closed validation. Shared WSL services, listeners, Incus capacity, and
production-like rollouts may be used by other worktrees—inspect coordination
state before restarting or mutating shared runtime resources.
