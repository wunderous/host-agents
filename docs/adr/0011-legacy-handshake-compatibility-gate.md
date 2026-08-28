# ADR 0011: Legacy handshake compatibility gate, and the bound on what it may bypass

Status: accepted

Date: 2026-08-27

Decision record: `../opute/.agents/decisions/legacy-handshake-compatibility-gate.json`

Supersedes nothing. **Bounds an exception to [ADR 0008](0008-product-neutral-kernel-transport.md).**

## Context

ADR 0008 states that the first-party Host Agent is a modern-only MCP
`2026-07-28` server and that "this process does not keep dual-era compatibility
layers." The code has kept one anyway, and it has never been written down:

- [`internal/config/config.go:126`](../../internal/config/config.go) reads
  `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE`, defaulting to off.
- [`internal/transport/http.go`](../../internal/transport/http.go) admits
  `initialize` and `notifications/initialized` when it is set.
- The `codex-wsl` skill documents `AllowLegacyHandshake=true` as the fix for
  `-32020: HeaderMismatch`.

Two active plans pointed in opposite directions on this flag: the decomposition
plan's `ha-k3` asserts "no `initialize`", while the conformance plan's Milestone 6
validates through headless `codex exec` — the exact client class the flag exists
for. The development guide requires compatibility to be backed by "a separately
approved migration decision and an explicit contract". Neither existed.

**The flag's real scope was larger than its name.** Validation was gated as:

```go
if !h.allowLegacyHandshake || (!isRetiredHandshake(m) && isModernMCPRequest(r, params)) {
    validateModernMCPRequest(...)
}
```

`isModernMCPRequest` is true only when params carry
`_meta["io.modelcontextprotocol/protocolVersion"]`. So with the flag on, **any**
method — `tasks/get`, `server/discover` — skipped `Mcp-Method` and
protocol-version validation merely by omitting that key. Conformance was
client-elective across the whole surface. The flag was named for the handshake
and was in practice a switch that disabled the transport contract.

## Decision

**The gate is kept, and bounded.** Retiring it now would break the conformance
plan's own validation path; keeping it unbounded would make every transport
invariant in the decomposition plan unenforceable. So:

1. **`OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE` remains, default off.** It is opt-in per
   deployment and is never set in Platform mode.
2. **Audience.** Pre-`2026-07-28` MCP clients that cannot emit `Mcp-Method` /
   `MCP-Protocol-Version` headers or the modern `_meta` keys — in practice Codex
   and Cursor IDE.
3. **The bypass surface is enumerated, not implied.** `legacyCompatibleMethods`
   in [`internal/transport/http.go`](../../internal/transport/http.go) lists the
   pre-2026 client surface: `initialize`, `notifications/initialized`,
   `notifications/cancelled`, `ping`, `tools/list`, `tools/call`,
   `resources/list`, `resources/read`, `resources/templates/list`,
   `prompts/list`, `prompts/get`. **Every other method is validated even when the
   flag is on** — notably `server/discover` and `tasks/*`, which are 2026-07-28
   additions no pre-2026 client can need, and where `tasks/*` carries client
   capability negotiation that the old bypass skipped wholesale.
4. **Adding an entry to that list is an amendment to this ADR**, with the client
   and the reason recorded. Widening the set is the only way the bypass can grow,
   and it now cannot happen silently.
5. **A client that negotiates modern MCP is held to it.** Sending the protocol
   version in `_meta` opts back into full validation on every method except the
   retired handshake itself, which is inherently pre-modern.

### Removal criteria

The gate is deleted, along with `legacy_handshake.go` and
`legacyCompatibleMethods`, when **both** hold:

- The conformance plan's Milestone 6 validates through a client speaking
  `2026-07-28`, so Codex is no longer the proof path; and
- No enrolled deployment sets `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE=true`.

Until then `ha-k3` reads **"no `initialize` by default; the compatibility gate is
bounded by ADR 0011"**, not "no `initialize`". The unqualified claim was false.

## Invariant delta

### Preserve

- C-01 provider-neutral core — the gate is client-era compatibility, not a
  provider special case.
- ADR 0008's modern-only default. The exception is opt-in, enumerated, and has
  removal criteria; that is what makes it an exception rather than a second era.

### Amend

- `ha-k3` — "no `initialize`" becomes "no `initialize` by default", as above.

### Add

- The bypass surface is a closed set. A request outside
  `legacyCompatibleMethods` is validated regardless of the flag.

## Consequences

- Codex and Cursor keep working; `TestMCPAllowsLegacyHandshakeWhenOptedIn` covers
  `initialize` + `tools/list` unchanged.
- `tasks/*` and `server/discover` are now rejected with `-32020` for unnegotiated
  requests in legacy mode. This is a **behaviour change**: a legacy client that
  reached those methods before will now be refused. That is the intent — those
  are modern-only surfaces — but it is the one way this ADR could break a client,
  and if it does, the fix is a recorded amendment to the list in (4), not
  re-widening the predicate.
- `TestLegacyHandshakeDoesNotBypassModernSurface` pins the bound, and was
  demonstrated to fail against the previous predicate.
