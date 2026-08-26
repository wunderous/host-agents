# Host Agent dated boundary rules

These are durable execution-boundary rules extracted from Host Agent
`AGENTS.md`. Case studies stay in sibling Opute `.agents/memories/LEARNING.md`.
Authorities: `docs/adr/0006-canonical-host-agent-identity.md`,
`docs/adr/0007-runtime-kind-and-e2e-target-boundaries.md`, and
`docs/cordis-development-guide.md` (C-23, C-24).

## Credentials and E2E

Keep CPC bearer, Host Agent tunnel bearer (`opha_*`), and public MCP user
session (`opsess_*`) distinct. Do not forward public credentials to the local
listener or interpret a local 401 as a public-auth failure. A Host Agent
health check or successful direct `tools/call` does not prove localhost web
credentials, Platform session validation, or public chat authentication.

When a caller may receive task-augmented `tools/call` results, advertise MCP
Tasks on every client construction path. Host Agent adapters preserve the
task-aware result, correlation, cancellation, and terminal state.

For agentic E2E require the literal user request, parsed outbound model/tool
trace, exact arguments, paired structured result, complete terminal SSE, and
zero SSE errors.

Cloudflare rejected Python `urllib`'s default `Python-urllib/*` User-Agent
with HTTP 403 Error 1010. Use an explicit browser-like User-Agent for public
E2E from WSL. Never print credentials.

## Relay and co-resident agents

A co-resident standalone Host Agent and platform-mode Host Agent can share
the physical machine, but only the process that owns the relay port can
reconcile its credentials and CIDRs. `address already in use` is an ownership
conflict. Include the actual Incus bridge source (`10.0.100.1/32`) and the
selected K3s cell CIDRs. After rotating the relay bearer, update the platform
Secret and restart every Secret consumer before a literal public `/chat`
canary.

Re-onboarding creates a new identity; retire the old systemd instance and its
watchdog before rebinding the shared LLM relay port. Relay sessions persist
per instance under `~/.config/opute/instances/<identity>/local-llm-relays/`.

## Recipes and host runtime

Recipe-managed user systemd units must use `%h` or an absolute path in
`ExecStart`; systemd does not expand `~`. `set_host_service_state` reloads
the relevant manager; providers must not hide an ad-hoc daemon-reload.
`inspect_host_file.expectedContent` is a write-only desired-content hash.
Provider teardown is two-phase: Host Agent `prepare`, then provider
`finalize`. An enabled-but-losing user unit races the port at every boot —
enabled state must match the runtime decision. A timer-driven oneshot is
healthy while `inactive (dead)` between ticks.

## Registry, Helm, CNPG

Provider/runtime E2E requires the runtime's configured registry endpoint.
Host-native K3s Helm tools share one resolver across sync and task paths.
Fresh K3s application cells must reconcile CNPG and wait for SQL-gated
readiness plus Secret `opute-platform-db` before Helm. Context-size is a
neutral shared-host provider capability
(`opute.capability.llm-serving.get/set-context-size`); evidence is
`persisted=true`, `applied=true`, and a literal streaming chat probe.

The chat inspector must not copy the model-facing catalog verbatim into
public SSE snapshots. Retain only the typed catalog identity/policy summary.
