# ADR 0001: Standalone MCP MVP boundary

Status: accepted for the internal release candidate

## Decision

The standalone product is a Linux-only, Incus-only MCP server using stdio.
Linux x86_64 and arm64 are supported; Windows clients use WSL. Native Windows
and macOS binaries are intentionally unsupported until the provider works
there end to end.

Standalone must not require Opute Platform, Bridge, onboarding credentials,
reverse tunneling, or a hosted MCP endpoint. It is read-only by default.
Infrastructure mutations require `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`.
Host shell is not part of the standalone surface. The versioned tool
classification and support levels live in `schemas/standalone-tools.json`.

VM inspection, prerequisites, and the VM lifecycle are stable MVP features.
K3s, PostgreSQL/SQL, and Cloudflare Tunnel tools remain experimental until
the disposable lifecycle gates in the release plan pass.

Standalone operations are asynchronous and persisted in a private SQLite
state directory. On restart, `working` operations become `unknown`; the
agent never reports them as completed automatically. Tool schema changes use
the standalone contract's schema version, and operation-state migrations must
be explicit and backward compatible before a release.

The working package name is `@opute/local-host-agent`. The repository/module
owner and public visibility must be finalized before publication because the
current checkout and advertised URLs do not yet agree.

## Consequences

The platform catalog remains available for platform mode, but it cannot define
the standalone public contract. Client documentation must use stdio and the
npm launcher or a directly built Linux binary. Public release claims must not
include experimental tools until their lifecycle gates are green.
