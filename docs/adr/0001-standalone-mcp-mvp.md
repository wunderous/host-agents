# ADR 0001: Standalone MCP MVP boundary

Status: accepted for the public protocol-only MVP (amended 2026-07-19)

## Decision

The standalone product is a Linux-only, Incus-only MCP server using
**Streamable HTTP**. Linux x86_64 and arm64 are supported; Windows clients use
WSL and connect over HTTP to the loopback listener (default port **3014**).
Native Windows and macOS binaries are intentionally unsupported until the
provider works there end to end.

stdio MCP transport is not supported. The public compatibility contract is
generic standards-compliant Streamable HTTP; Cursor, VS Code, and Claude
Desktop snippets are documentation examples, not certification. Clients configure
`"type": "http"` with `url: http://127.0.0.1:3014/mcp` after starting the
agent process (binary or `@opute/host-agent`).

Standalone must not require Opute Platform, Bridge, onboarding credentials,
reverse tunneling, or a hosted MCP endpoint. It is read-only by default.
Infrastructure mutations require `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`.
Host shell is not part of the standalone surface. The versioned tool
classification and support levels live in `schemas/standalone-tools.json`
(`transport: "http"`).

VM inspection, prerequisites, and the VM lifecycle are stable MVP features.
K3s, PostgreSQL/SQL, and Cloudflare Tunnel tools remain experimental until
the disposable lifecycle gates in the release plan pass.

Standalone operations are asynchronous and persisted in a private SQLite
state directory. On restart, `working` operations become `unknown`; the
agent never reports them as completed automatically. Tool schema changes use
the standalone contract's schema version, and operation-state migrations must
be explicit and backward compatible before a release.

The public package name is `@opute/host-agent`, published from the public
`wunderous/host-agents` repository through GitHub Actions. The Go module,
release artifacts, and npm metadata use the same owner and release version.

The exact first-run flow is `initialize` → `tools/list` →
`check_local_prerequisites` → `get_local_status` → `list_vms` (VM inventory).
Native host execution is Linux-only; Windows uses WSL. VM inspection and the
Incus VM lifecycle are stable MVP features; K3s, PostgreSQL/SQL, and Cloudflare
Tunnel remain experimental.

## Consequences

The platform catalog remains available for platform mode, but it cannot define
the standalone public contract. Client documentation must use Streamable HTTP
and the npm launcher (daemon helper) or a directly built Linux binary. Public
release claims must not include experimental tools until their lifecycle gates
are green.
