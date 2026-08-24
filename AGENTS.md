# Repository Guidelines

## Project Structure & Module Organization

- `cmd/opute-host-agent/` contains the single Go executable entry point.
- `internal/app`, `internal/cli`, and `internal/hostmcp` own runtime startup,
  command modes, and MCP serving.
- `internal/tools`, `internal/ops`, `internal/provider`, `internal/plan`, and
  `internal/session` implement typed capabilities, execution, plans, and
  durable session contracts.
- `schemas/` stores versioned capability contracts; `test/` contains contract,
  integration, standalone, and mode tests. The separate Bun TUI lives in the
  sibling Opute repository under `apps/opute-tui/`. `npm/local-host-agent/` is
  the release launcher.

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

For local TUI work, use the sibling Opute repository's
`apps/opute-tui/` Bun application against the Host Agent MCP endpoint. This
repository's binary is server-only; use `serve` (or the bare invocation) when
an external MCP client needs the Host Agent.

## Beads coordination

Use `scripts/agent-work` for Beads status/start/update/end from WSL; it is
anchored to this checkout and works from either parent directory. The launcher
uses the single WSL-owned workspace at
`$HOME/.config/opute/agent-work-coordination` and the local WSL Dolt listener
at `127.0.0.1:3308`. It starts only that one server when needed and never
creates `.beads` in this checkout. `make agent-work` delegates to the same
launcher and is safe to invoke with `make -f` from a parent directory.

Patterns: keep the host agent and Opute on the same WSL ledger; use the
launcher or Makefile rather than cwd-relative paths; let the launcher own the
local server; and verify the real adapter path with `status --json --all` plus
`bd dolt test`. If metadata is missing or identity mismatches, back up first
and fail closed until the active database has been inspected. Treat endpoint
configuration, the WSL listener, and a successful adapter read as separate
levels of evidence.

Anti-patterns: do not run `bd init` against an unexpected or remote endpoint,
run `bd dolt killall`, create a checkout-local `.beads`, recreate the retired
Windows ledger, hard-code credentials, rely on `bun.exe` or the caller's cwd,
or kill an unverified port owner. See
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
test. Preserve protocol and standalone tests for pipes and automation;
interactive TUI coverage belongs in the sibling Opute application. Capability
changes should include contract and standalone coverage where applicable.

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

For public Opute E2E validation from WSL, use the sibling Opute repository's
Bun auth resolver or Playwright. Cloudflare rejected Python `urllib`'s default
`Python-urllib/*` signature with HTTP 403 Error 1010 (`browser_signature_banned`)
even though the managed credentials were valid; an explicit browser-like
`User-Agent` succeeded. Keep this edge-client finding separate from Host Agent
capability health, never print credentials or bearer values, and require a
complete parsed SSE stream plus correlated trace before claiming chat success.

Keep rollout authentication domains separate. The local preflight runs against
`127.0.0.1:9090` with the local `dev@opute.local` identity, while public
post-roll checks resolve the managed public-E2E bearer against
`https://platform.opute.io`. Do not forward public credentials to the local
listener or interpret its 401 as a public-auth failure.

Fresh K3s application cells also require ordering beyond K3s/web/MCP readiness:
reconcile the host-agent-managed CNPG service and wait for SQL-gated readiness
plus the `opute-platform-db` consumer Secret (`platformDatabaseUrl` and
`taskLedgerDatabaseUrl`) before rendering/applying Helm. Applying Helm first
produces `CreateContainerConfigError` in Platform/Task Ledger pods. Keep
host-native imported-prerequisite metadata and operator readiness as separate
evidence; a passing `/api/chat` canary does not prove those projections.

Keep host-wide LLM context configuration outside the Host Agent core. The
active provider registers the neutral dynamic operations
`opute.capability.llm-serving.get-context-size` and
`opute.capability.llm-serving.set-context-size`; the Ollama provider persists
the value in the shared user runtime configuration and reconciles every known
user service unit before reporting `applied=true`. A setting read-back with
`changed=false`, the persisted `contextSize`, and a literal streaming chat
probe are required evidence. The WSL shared host is currently pinned to
`contextSize=32768`; per-instance config or model aliases are not substitutes
for the host-wide service setting.

The sibling chat inspector also has a transport-size invariant: the
model-facing authorized catalog must not be copied verbatim into public SSE
snapshots or delta `before`/`after` values. Full tool schemas caused a no-tool
canary to emit megabytes and terminate at the edge before terminal events.
Retain only the typed catalog identity/policy summary in the inspector and
validate complete SSE plus correlated trace evidence after any change.

Recipe/runtime E2E findings (2026-08-23):

- A recipe-managed user systemd unit must use `%h` or an absolute path in
  `ExecStart`; systemd does not expand `~` there. The generic
  `set_host_service_state` primitive reloads the relevant user/system manager
  before changing state, so providers must not hide an ad-hoc daemon-reload or
  shell service transition in a recipe.
- `inspect_host_file.expectedContent` is a write-only desired-content hash
  comparison for managed, potentially secret-bearing configuration. It can
  prove drift and trigger reconciliation without returning the file contents.
- Provider teardown is two-phase: Host Agent `prepare` executes the generic
  host plan (stop, disable, and remove the connector), then the provider's
  `finalize` operation deletes declared external resources. If finalization
  fails, the provider generation remains available so the operation can be
  retried; it must not be reported as fully removed.
- A disposable WSL Cloudflare route required a proxied DNS CNAME for reliable
  IPv4 reachability. The generic HTTP probe therefore has a bounded TCP/IPv4
  fallback for WSL environments where IPv6 resolution is present but unrouted.
- A public managed-cluster validator returning HTTP 401 is an authentication
  failure, not a skipped gate. Do not claim managed-cluster certification
  without a valid public bearer and complete host-native provenance,
  cert-manager, and runtime evidence.

- Public deployment validation on 2026-08-23 exposed two separate control-plane
  gaps: the advertised `k3s__install_helm_chart` route rejected execution as
  unknown, and `install_cluster_agent` returned `host-native cluster agent
  install is not implemented in the Go host agent`. Do not convert either into
  a passing prerequisite claim. If a disposable cluster must be certified
  meanwhile, record the typed operation failure separately and use an explicit,
  versioned upstream prerequisite manifest only as temporary validation setup.
- Re-onboarding a host creates a new identity; retire the old systemd instance
  and its watchdog before rebinding the shared LLM relay port. Otherwise the
  stale instance can reclaim the port with an expired bearer and cause public
  chat failures that look like model/runtime failures. Keep the active host
  tunnel, relay bearer, and public MCP user bearer as distinct credentials.
- The shared relay must allow the actual Incus bridge source (`10.0.100.1/32`)
  for host-side probes as well as the explicitly selected K3s cell CIDRs. After
  rotating the relay bearer, update the platform Secret and restart every
  Secret consumer before running a literal public `/chat` canary; HTTP 200 or
  model-runtime health alone is insufficient.

- **Relay ownership and source policy are separate boundaries.** A co-resident
  standalone Host Agent and platform-mode Host Agent can share the physical
  machine but only the process that owns the relay port can reconcile its
  credentials and CIDRs. A successful `ensure_local_llm_relay` on the wrong
  instance does not prove the public relay changed; `address already in use`
  identifies an ownership conflict. Route the mutation to the owning agent,
  retain the actual Incus/K3s source CIDRs (including the selected cell IP when
  traffic is NATed), and keep the source list configurable rather than hiding
  it in the application provider.
- **Generic host runtime settings are durable shared-host state.** The active
  provider exposes the neutral context-size read/write operations; the Ollama
  provider persists the value and reconciles every known user service unit.
  A successful set must be followed by a read-back showing `persisted=true`,
  `applied=true`, and the same value, plus a literal streaming chat probe.
- **Legacy state migration must be additive before reads.** When upgrading an
  older active-capability SQLite table, add missing columns before selecting
  them for migration. Otherwise the bootstrap service fails before it can
  expose recipe/provider tools.
- **Provider/runtime E2E requires the runtime's configured registry endpoint.**
  The disposable K3s cell's insecure registry is `10.0.100.240:30500`; pushing
  an image to the reachable-but-unconfigured `10.0.100.252:30500` makes the
  rollout fail with `http: server gave HTTP response to HTTPS client`. Keep
  image publication and the target runtime's registry configuration as
  separate, explicitly verified facts.
- **Host-native K3s Helm tools must share one resolver across sync and task
  paths.** A `vmName`-only `k3s__uninstall_helm_chart` call must resolve the
  managed-cluster projection, verify `source=k3s-host`, and only then execute
  through the host agent. The deployed public test now completes without an
  explicit `hostId`; VM-backed rows remain on their normal downstream path.

Shared-host runtime reconciliation findings (2026-08-23):

- **An enabled-but-losing user unit races the port at every boot.** Two user
  units binding `127.0.0.1:11434` (the provider bundle's `ollama.service` and
  the generated `opute-ollama.service`) with no `Conflicts=` produce a silent
  boot race; the loser crash-loops under `Restart=on-failure` (observed
  ~2,200 consecutive restarts across days). `ensureOllamaRuntime` now retires
  its own unit (`systemctl --user disable --now`) when the API probe succeeds
  and `is-active --quiet` shows the generated unit is not the serving owner.
  Note `is-active --quiet` is true only for `active` — `activating`
  (auto-restart) already identifies the loser. Runtime-skip alone is not
  reconciliation: enabled state must match the runtime decision or the race
  returns on the next boot.
- **Relay persistence is per-instance; re-onboarding strands it.** Relay
  sessions persist to `~/.config/opute/instances/<identity>/local-llm-relays/`
  (Go-default JSON field names) and restore on agent start. Re-onboarding
  creates a new instance directory, so desired-state left on the retired
  identity never restores — a healthy agent with no listener. The Opute-side
  tunnel watchdog now imports stranded sessions from retired instances and
  restarts the agent to rebind; relay mutations still route only to the
  owning platform-mode instance.
- **A timer-driven oneshot is healthy while `inactive (dead)`.** The tunnel
  watchdog service shows `inactive (dead)` between 3-minute ticks; verify the
  trigger timer (`systemctl --user list-timers`) and the journal before
  declaring the watchdog down. Diagnose shared-host regressions with the
  lease-holder PID, listener inventory (`ss -tln`), and journal restart
  counters — file mtimes across instance directories proved the stranding
  above where process state alone looked healthy.
