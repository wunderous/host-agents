---
name: codex-wsl
description: Use when configuring, diagnosing, or executing OpenAI Codex CLI within WSL or across the Windows-WSL boundary, connecting Codex to the local Host Agent MCP server, or running automated non-interactive Codex verification scripts.
---

# Codex in WSL: Configuration, MCP Integration & Execution

## When to load this skill

Load this skill whenever you are:
- Configuring or verifying OpenAI Codex inside a WSL2 distribution or Windows host.
- Connecting Codex to the Host Agent MCP endpoint (`http://127.0.0.1:3004/mcp`).
- Running headless/automated prompts with `codex exec` from scripts, CI, or parent agents.
- Troubleshooting MCP handshake errors (e.g., `-32020: HeaderMismatch`, MCP startup failures).
- Diagnosing WSL2 session bridge stalls (`Wsl/Service/0x8007274c`).

---

## 1. MCP Configuration Standards

Codex reads its server configuration from `~/.codex/config.toml` (WSL) or `%USERPROFILE%\.codex\config.toml` (Windows).

### Host Agent MCP Configuration
```toml
[mcp_servers.host-agent]
url = "http://127.0.0.1:3004/mcp"
http_headers = { "Authorization" = "Bearer oha_host-zephyrus-ef47fbbf" }
```

### Handshake & Protocol Requirements
- The Host Agent must have `OPUTE_MCP_ALLOW_LEGACY_HANDSHAKE=true` enabled in its instance environment (`host-agent.env`) to support standard client initialization.
- Do not pass `MCP-Protocol-Version: 2026-07-28` in `http_headers` unless the client explicitly formats modern metadata envelopes (`_meta.io.modelcontextprotocol/protocolVersion`).

---

## 2. Non-Interactive / Headless Execution Patterns

When invoking `codex exec` from automation, scripts, or parent agents:

### Always Close Stdin
`codex exec` reads from stdin if attached. Prevent hanging by redirecting `/dev/null` or piping:
```bash
# Direct in WSL
codex exec --dangerously-bypass-approvals-and-sandbox 'call list_vms from host-agent' </dev/null

# Piped input
echo "call list_vms from host-agent" | codex exec --dangerously-bypass-approvals-and-sandbox -
```

### PowerShell -> WSL Quoting Template
When driving WSL Codex from Windows PowerShell, avoid nested double quotes which PowerShell strips before `wsl.exe`:
```powershell
# Safe: Single quotes inside double quotes for bash
wsl.exe -d Ubuntu-26.04 -u houman -- bash -lc "codex exec --dangerously-bypass-approvals-and-sandbox 'call list_vms from host-agent' </dev/null"
```

---

## 3. Diagnostics & Recovery

| Symptom | Cause | Resolution |
|---|---|---|
| `Mcp error: -32020: HeaderMismatch` | Server inferred modern protocol on standard client request without required headers. | Verify `isModernMCPRequest` checks for explicit protocol version in `_meta`, and Host Agent has `AllowLegacyHandshake=true`. |
| `Wsl/Service/0x8007274c` | WSL VM socket bridge unresponsive. | Run `wsl.exe --shutdown`, wait 5s, verify with `wsl.exe -d Ubuntu-26.04 -u houman -- echo "up"`, then check `systemctl --user status opute-host-agent@...`. |
| `Ignored unsupported project-local config keys` | Setting user-level keys in workspace `.codex/config.toml`. | Move keys (e.g. `notify`) to `~/.codex/config.toml`. |
