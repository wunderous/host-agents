# Evidence record

## Contract / unit / MCP wire (fake backend)

- `go -C plugins/tunneling/tailscale test ./...` — pass
- `TestEnsurePublicIngressOperatorModeFailClosed` — pass (stable=true without evidence fails)

## 1. Host Agent Tailscale provider catalog install (2026-09-20)

- Binary: `~/.local/share/opute/providers/com.opute.tailscale/bin/opute-provider-tailscale`
- systemd user unit: `opute-provider-tailscale.service` (active), port **4323**, `OPUTE_TAILSCALE_BACKEND=live`
- Activation recipe: `plugins/tunneling/tailscale/recipes/activate.yaml` (`runtime-recipe.v1` / `network-overlay.v1`)
- Install recipe fixed to k3s-shaped `opute.provider.install` (`mode: activate`, `recipeSource`, `activate: true`)
- Host Agent gained `network-overlay.v1` serving-contract support + activation validation flow
- Live `opute.provider.status`:
  - `com.opute.tailscale` → **active=true, connected=true** (generation `com.opute.tailscale-210`)
  - beside `com.opute.cloudflare` and `com.opute.k3s` (both still active)
- Catalog publishes Tailscale-native overlay ops (non-overlapping with Cloudflare legacy overlay IDs):
  - enroll / ensure-private-mesh / ensure-private-service / ensure-public-ingress / promote-public-ingress / probe / report-two-node-readiness

## 2. Host Agent recipe + catalog overlay drive (primary path)

Primary path is Host Agent MCP capability tools (not gated Go tests / Incus scripts):

```
opute.capability.network-overlay.enroll
opute.capability.network-overlay.ensure-public-ingress (operatorMode=true)
opute.capability.network-overlay.probe (pathClass=public-ingress)
```

Recorded against `container:local:opute-ha-a`:
- enroll ready with meshIp `100.97.79.97`
- ensure-public-ingress **stable=true** with endpoint `https://opute-public.tail229553.ts.net/`
- probe ready=true, stable=true

Host-local recipe `com.opute.tailscale.public-ingress` completed with nodes
`agent/enroll/ingress/probe` all **applied** (run idempotency key `...-v5`).

Recipes: `public-ingress.yaml`, `overlay-mesh.yaml` (hostAgentId from `get_host_info`, readiness validate blocks).

## 3–5. Tailscale Kubernetes Operator + stable public ingress

- Helm `tailscale/tailscale-operator` in namespace `tailscale`
- Ingress `opute-public/public-demo-funnel`:
  - `ingressClassName: tailscale`
  - annotation `tailscale.com/funnel: "true"`
  - ADDRESS: **`opute-public.tail229553.ts.net`** (not `opute-ha-a.…`)
- Proxy pod `ts-public-demo-funnel-*` terminates Funnel (observed on ha-b)
- Manifest: `plugins/tunneling/tailscale/recipes/manifests/public-demo-operator-funnel.yaml`
- `operatorMode=true` without Operator evidence fails closed; with evidence records `stable=true`

### Public proof

- `curl -4 https://opute-public.tail229553.ts.net/` → **HTTP 200** `opute-public-ingress ok`

### Path separation

- Private mesh remains Tailscale CGNAT between ha-a/ha-b for cluster traffic
- Public Funnel terminates on Operator proxy, not node-local `tailscale funnel` on ha-a
- Funnel is not used for etcd/API/admin

## 6. Funnel-host loss (ha-a k3s stopped)

- Stopped `k3s` on `opute-ha-a`
- Public HTTPS remained **HTTP 200** on `opute-public.tail229553.ts.net`
- 2-node embedded etcd remains **serving-continuity-only** (writes may fail under quorum loss; not claimed fixed)
- Restored ha-a k3s; public HTTPS still 200

## 7. Secrets / commits

- OAuth client material only under `~/.config/opute/` (not in git)
- No secrets in this evidence file
