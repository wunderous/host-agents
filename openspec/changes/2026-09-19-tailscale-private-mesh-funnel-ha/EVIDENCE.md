# Evidence record

## Contract / unit / MCP wire (fake backend)

- `go -C plugins/tunneling/tailscale test ./...` — pass (includes Streamable HTTP MCP wire)
- `go -C plugins/tunneling/cloudflare test ./...` — pass (Cloudflare unchanged)
- `go -C plugins/kubernetes/k3s test ./...` — pass (availabilityClass / recoveryPolicy)
- `go test ./test/contract/` — pass (architecture + network-overlay neutrality)
- `make standalone-smoke` / `make standalone-http-smoke` — recorded in commit validation

## Live Tailscale mesh + Funnel (2026-09-20)

**Status: verified.**

### Private mesh

- Nodes: `opute-ha-a` (TS `100.97.79.97`, Incus on Ubuntu-26.04) and
  `opute-ha-b` (TS `100.93.139.86`, Incus on Opute-HA-B).
- Bidirectional `tailscale ping` succeeded (sub-3ms over CGNAT / LAN path).
- K3s: both `Ready` control-plane,etcd,master (`v1.31.8+k3s1`), INTERNAL-IP
  `10.0.100.66` / `10.0.100.88`.

### Public Funnel

- ACL `nodeAttrs` grants `funnel` to `*` (toggled off/on to re-trigger public DNS).
- Local: `tailscale funnel` → `http://127.0.0.1:8787` canary;
  `AllowFunnel["opute-ha-a.tail229553.ts.net:443"]=true`.
- Public DNS A (Google DoH / authoritative): `208.111.35.209`, `208.111.34.11`.
- Public HTTPS: `curl https://opute-ha-a.tail229553.ts.net/` → **HTTP 200**,
  body `opute-ha-funnel-canary ok 2026-09-20T22:11:53Z` (forced via both Funnel
  edge IPs and plain public resolution).

### Failure injection (embedded 2-node etcd)

| Axis | Stop `k3s` on ha-b | Stop `k3s` on ha-a (brief) |
|------|--------------------|----------------------------|
| Public Funnel canary | HTTP 200 (unchanged) | HTTP 200 (canary independent of k3s) |
| Kubernetes API write | Unavailable (etcd quorum loss; apiserver refused/timeout) | Unavailable on peer (etcd readiness failed) |
| Durable-store continuity | Lost while one member down (expected for 2-node etcd) | Lost while one member down |
| Serving continuity claim | Public Funnel path stayed up | Public Funnel path stayed up |
| Recovery | Both nodes Ready; ConfigMap create/delete OK; Funnel 200; mesh ping OK | same |

This matches ADR-0015 / design: two-node embedded etcd is serving-continuity-only,
not durable HA; public Funnel must not satisfy private-path prerequisites.

### Cleanup posture

- Live HA Incus guests, Tailscale enrollment, and Funnel canary are **retained**
  as the requested end state (not disposable).
- Provider-generation disposal / foreign-ownership fail-closed covered by unit
  tests under `plugins/tunneling/tailscale`.
- No secrets written into this evidence file.
