# Evidence: Host Agent MCP capability exposure (2026-09-20)

## Objective

Every activated provider capability family must appear on Host Agent MCP
`tools/list` / catalog (dispatchable via `/mcp`).

## Gaps found

1. Live Tailscale binary predated `mesh-runtime.v1` → mesh-runtime ops missing.
2. Cloudflare inactive → `tunneling.v1` ops missing.
3. Ollama inactive / invalid Operation.Version → `llm-serving` ops missing.
4. Activating Cloudflare with full Services would displace Tailscale HA seams.

## Fixes

1. Rebuild/redeploy Tailscale + Host Agent; reload Tailscale activate recipe
   (capabilities include mesh-runtime).
2. Recipe-scoped publish: `activationPublishManifest` filters Services by
   `runtime.capabilities` and only displaces those families (fail-closed if
   allowlist matches nothing).
3. Cloudflare `activate-tunneling.yaml` publishes only `tunneling.v1`.
4. Ollama `activate.yaml` + Operation `Version: 1` + `llm-serving.v1` serving
   contract activator (manifest probe, no chat-model requirement).

## Live verification (host-zephyrus-ef47fbbf :3004)

Active providers: Tailscale, Cloudflare (tunneling), k3s, Ollama.

Via hostagentclient.ListTools with bearer auth:

- total_tools: 187
- capability_tools: 50
- families: kubernetes=28, llm-serving=3, mesh-membership=3, mesh-runtime=4,
  private-mesh=3, public-ingress=3, tunneling=6
- missing required (non-deprecated contract families with active providers): none
- RESULT: PASS

Declared contract families (contracts/capability/capability.go): llm-serving,
tunneling, kubernetes, mesh-runtime, mesh-membership, private-mesh, public-ingress.
Deprecated network-overlay.v1 is not required.

Unit: go test ./internal/hostmcp/ -count=1 PASS (78.674s).
