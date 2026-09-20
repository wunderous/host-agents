# Evidence record

## Contract / unit / MCP wire (fake backend)

- `go -C plugins/tunneling/tailscale test ./...` — pass (includes Streamable HTTP MCP wire)
- `go -C plugins/tunneling/cloudflare test ./...` — pass (Cloudflare unchanged)
- `go -C plugins/kubernetes/k3s test ./...` — pass (availabilityClass / recoveryPolicy)
- `go test ./test/contract/` — pass (architecture + network-overlay neutrality)
- `make standalone-smoke` / `make standalone-http-smoke` — recorded in commit validation

## Live Tailscale mesh

**Status: unverified in this environment.**

Set `OPUTE_TAILSCALE_BACKEND=live` with Tailscale API credentials and disposable
targets to produce live mesh, Funnel, and per-node failure-injection evidence.
Fake-backend tests intentionally do not substitute for that gate.
