# Tentative RFC 0001: Host Agent Desktop MCP Client

**Status:** Unscoped exploration — recorded 2026-08-22; not scoped, sequenced,
or accepted (reclassified 2026-08-23)
**Date:** 2026-08-22

## Summary

Make `opute-host-agent` a single-purpose, durable Go MCP server managed by
systemd. Remove the Host Agent TUI as a product surface and add a companion
desktop application in this repository that operates the server as an MCP
client.

The desktop application should work against a Host Agent on the same machine,
over a private network, or across the public internet through an authenticated
Opute relay. The Host Agent remains the execution boundary; the desktop app is
only a supervision and user-experience layer.

> **Reclassification note (2026-08-23):** This document is an exploration of a
> possible future desktop client, not an accepted direction. The active
> terminal-client direction is the TUI v2 redesign
> (`.agents/plans/2026-08-host-agent-tui-redesign.md` in the Opute repository,
> authoritative per ADR 0002), implemented as the external Bun/TypeScript
> `apps/opute-tui` client. The former `clients/tui` module was only a
> migration source until parity is proven. Nothing in this
> RFC — including implementation phase 7's eventual TUI removal — gates or
> redirects TUI work absent a future explicit decision that scopes and accepts
> a desktop replacement.

## Motivation

An API-first boundary is easier to extend than a terminal-specific control
surface. It supports desktop, web, IDE, automation, and future mobile clients
without duplicating execution logic. MCP Streamable HTTP provides a standard
remote transport with HTTP requests and optional streamed responses; durable
operation records provide recovery when a connection disappears.

## Proposed architecture

```text
Desktop app on machine B
        |
        | MCP over authenticated HTTPS
        v
Opute Platform / relay (remote mode)
        ^
        | outbound authenticated connection from machine A
        |
Host Agent on machine A
        |
        v
systemd, Incus, host capabilities, durable state
```

### Host Agent

- Runs only as a systemd-managed MCP server.
- Exposes typed capabilities, catalog revisions, durable plans, tasks, and
  progress through MCP.
- Executes locally and never delegates host mutation decisions to the desktop
  renderer.
- Uses Streamable HTTP as the public transport; stdio and embedded TUI modes
  are not part of the product contract.
- Maintains a small administrative install/enrollment path separate from the
  long-running server process.

### Desktop companion

Create `apps/host-agent-desktop/` as an Electron application with a main
process responsible for MCP connections, authentication, reconnection, and
secure storage. The renderer provides the Host Agent-specific UX:

- host enrollment and connection status;
- capability catalog and provider health;
- setup-plan creation, approval, execution, and resume;
- live operation phases, logs, readiness checks, and errors;
- operation history and durable recovery after restart;
- explicit mutation approval and audit context.

The renderer must not execute shell commands, hold long-lived host secrets, or
implement a second capability dispatcher.

## Connectivity and authentication

Use one connection model with two routes:

1. **Local:** the desktop connects to a loopback Host Agent endpoint. The
   installer creates a per-user local credential stored in the operating
   system keychain and restricts the listener to loopback.
2. **Remote:** the Host Agent establishes an outbound TLS connection to the
   Opute relay. The desktop authenticates to Opute, selects a host identity,
   and receives a short-lived, host-scoped capability token.

Initial enrollment uses a short-lived pairing code or QR flow. The desktop
creates a device key pair; after pairing, subsequent connections use the
device identity and short-lived access tokens. No permanent password or raw
SSH private key is stored in the desktop application.

Direct public exposure of the Host Agent is an optional advanced deployment,
not the default. Every remote request remains authenticated, authorized, TLS
protected, origin-validated, rate-limited, and bound to a host identity.

## Progress and recovery contract

Long-running capabilities return a durable `runId` or `taskId` quickly. The
Host Agent records structured events such as:

```json
{
  "runId": "run-123",
  "sequence": 42,
  "phase": "install-incus",
  "stream": "stdout",
  "message": "Waiting for the service to become ready",
  "status": "running"
}
```

The desktop displays streamed progress while connected and resumes from the
last acknowledged sequence after reconnecting. Final state is read from the
durable operation record, not inferred from whether a socket remained open.
Raw output is redacted before persistence or display where it may contain
credentials.

Interactive PTY access, if required later, is a separate authenticated
WebSocket/PTY capability. It is not the default mechanism for setup workflows.

## Implementation phases

1. Freeze the server-only Host Agent contract and document the desktop/client
   boundary.
2. Extract shared MCP client contracts, catalog types, task state, and event
   schemas for TypeScript consumption.
3. Scaffold the Electron app with local loopback connection, keychain-backed
   credentials, catalog display, and read-only health/status screens.
4. Add pairing and remote relay connectivity with host-scoped authorization.
5. Add setup plans, mutation approval, durable progress streaming, reconnect,
   resume, cancellation, and audit display.
6. Package the desktop app for macOS, Windows, and Linux; keep the Host Agent
   service package independently installable on Linux/WSL hosts.
7. Remove Bubble Tea, embedded TUI, and attached-TUI modes once the desktop
   client passes the end-to-end replacement gates; the 2026-08 cutover has now
   completed that removal.

## Acceptance criteria

- A Host Agent starts under systemd with no interactive terminal requirement.
- The desktop pairs with and operates a local Host Agent without exposing it
  beyond loopback.
- A desktop on machine B can operate machine A through the relay without
  inbound SSH or an open host management port.
- Incus installation progress remains visible through transient disconnects.
- Completion, failure, cancellation, and unknown states are durable and
  inspectable after either process restarts.
- All mutations remain typed, authorized, explicitly approved, and auditable.
- A second MCP client can perform the same operation without depending on the
  desktop app.

## Non-goals

- Replacing the Host Agent with an Electron process.
- Making SSH the product API.
- Exposing arbitrary shell execution through the desktop app.
- Requiring Opute Platform for a local-only Host Agent deployment.
- Maintaining two independent implementations of host capability behavior.

## Open decisions

- Whether remote relay connectivity is mandatory for the first release or an
  optional Platform mode.
- Which OIDC provider and device-enrollment mechanism to use.
- Whether the first desktop release targets all three operating systems or
  starts with macOS and Windows.
- Whether Electron is retained after the first release or replaced with a
  lighter desktop shell once the client contract is stable.
