# ADR 0008: Product-neutral kernel transport and in-process authorization server

Status: accepted

Date: 2026-08-26

Decision record: `../opute/.agents/decisions/product-neutral-host-mcp.json`

## Context

The Host Agent is a product-neutral Cordis MCP kernel. Clients (Cursor, Claude,
external MCP clients, and Opute MCP Host) speak Streamable HTTP to **this** process.
Cloudflare is a provider for `opute.capability.tunneling.v1`. Reverse-tunnel
phone-home, Host Worker Protocol, and CPC heartbeat were production exceptions
that contradicted ADR 0001, ADR 0002 (I-25), ADR 0003, ADR 0006, and the generic
tunneling recipes plan.

Those exceptions are deleted. Dependents that still need `initialize`,
`Mcp-Session-Id`, DCR, WSS MCP, HTTP+SSE GET streams, or CPC self-registration
are refactored. This process does not keep dual-era compatibility layers.

## Decision

The kernel always serves one stateless `POST /mcp` resource with an in-process
OAuth 2.1 resource server plus co-located authorization server. Standalone mode
defaults to loopback `127.0.0.1:3014`. Platform mode defaults to
`0.0.0.0:3004` so a co-hosted Platform/MCP pod can reach the host across
the Linux bridge; network policy and Host Agent authorization still gate every
request. Opute discovers a host by an enrolled resource URL plus
`OPUTE_REMOTE_AGENT_ID`; the agent does not register itself. Public names are
caller-supplied `tunneling.v1` data, not product hostnames.

First-party Host Agent is a modern-only MCP `2026-07-28` server.

## Invariant delta

### Preserve

- C-01 provider-neutral core
- I-04 / I-18 / I-25 provider isolation
- C-08 generation affinity
- I-05 / C-03 one `plan.Runner`
- ADR 0006 opaque `OPUTE_REMOTE_AGENT_ID`
- I-22 redaction
- ADR 0003 two-phase teardown
- Unauthenticated `GET /health`

### Strengthen

- I-25: boot with no tunnel provider; architecture tests forbid Cloudflare
  lifecycle in `internal/ops` dispatch and product hostnames in `internal/`
  runtime (fixtures and docs may still use `opute.io` as example data).

### Introduce

| ID | Rule |
|----|------|
| T-1 | Process always serves `/mcp`. No reverse-tunnel loop, no HWP worker, no CPC heartbeat client. |
| T-2 | Single Streamable HTTP MCP endpoint. |
| T-3 | Spec transport headers and `HeaderMismatch` `-32020`. |
| T-4 | Bind loopback by default in standalone mode; platform mode uses an explicit local bridge bind so co-hosted Platform/MCP pods can reach the host. |
| T-5 | WAN exposure is a tunneling capability, not a kernel transport. |
| T-6 | HWP is not a client transport. Retiring reverse-tunnel without retiring HWP is incomplete. |
| P-1 | Core dispatch is default-unknown for vendor names. |
| P-2 | Hostnames are opaque strings; no `*.opute.io` special case. |
| P-3 | Cloudflare down must not take local `/mcp` down. |
| P-4 | Tunnel activation evidence is authenticated public `tools/list`, not HTTP 200. |
| A-1 | Host Agent is the resource server; co-located AS is this process. |
| A-2 | RFC 9728 PRM `authorization_servers` points at this AS, never Opute. |
| A-3 | Host Agent does not validate Platform sessions or product token formats. It accepts only its configured bootstrap token or an OAuth access token issued for this resource. |
| A-4 | RFC 7009 revoke; next `tools/list` fails for that grant. |
| A-5 | RFC 8707 audience is the canonical URI of **this** request. |
| A-6 | Bootstrap env token is first admin only; it is a host-issued secret, not a product session. |
| A-7 | Do not adopt EMA or point identity at a product IdP. |
| I-cli | First-party canaries are modern-only `2026-07-28`. |
| I-dns | DNS is only for zones the caller actually operates. |
| L-2 / L-3 | Two-phase teardown remains; leftovers are a failure. |
| C-enroll | Opute discovers a host by enrolled resource URL + `OPUTE_REMOTE_AGENT_ID`; in-cluster clients resolve loopback host endpoints through the runtime default gateway rather than a persisted address heuristic. |
| S-1 | Stateless `POST /mcp` only; GET/DELETE 405; no `Mcp-Session-Id`. |
| S-2 | Origin allowlist; invalid Origin → 403. |
| S-3 | `MCP-Protocol-Version` + `Mcp-Method` + `Mcp-Name` match body or `HeaderMismatch` `-32020`. |
| S-4 | CIMD + pre-registration; DCR is not implemented. |
| S-5 | RFC 8707 audience is the request’s canonical MCP URI; no token passthrough. |
| S-6 | RFC 9207 `iss` on authorize responses; PKCE S256 advertised. |
| S-7 | Reject `initialize`, `notifications/initialized`, and protocol versions other than `2026-07-28`. |
| S-8 | Advertise only implemented capabilities (`tools` + `io.modelcontextprotocol/tasks`). |

### Retire

- `OPUTE_REVERSE_TUNNEL` health-only fork
- `endpoint: tunnel://mcp-host`
- Go CPC heartbeat / `register_host_agent` phone-home
- Install-script WSS block
- Watchdog that treats `agents.active` on a product hostname as host liveness
- Standalone forbidding a host bootstrap `MCP_AUTH_TOKEN`
- Dual-era `initialize` / `Mcp-Session-Id` / HTTP+SSE GET stream
- DCR as an enrollment path

There is no silent permanent exception and no dual-run WSS+HTTP window. If a
named editor still cannot speak modern MCP, that is unverified — not a waiver
to reintroduce `initialize`.

## Consequences

Opute becomes a confidential HTTP MCP client of the enrolled host URL, using a
host-audience token minted for that client. Capacity and liveness are pulled
from `/health` (and typed host observations), not pushed by heartbeat.

Live Cloudflare or editor evidence that has not been run is recorded as
unverified, never as a pass.
