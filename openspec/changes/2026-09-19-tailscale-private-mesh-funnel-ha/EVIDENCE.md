# Evidence record

## Contract / unit / MCP wire (fake backend)

- `go -C plugins/tunneling/tailscale test ./...` — pass
- `TestEnsurePublicIngressOperatorModeFailClosed` — pass (stable=true without evidence fails)

## Host Agent Tailscale provider install (2026-09-20)

- Binary: `~/.local/share/opute/providers/com.opute.tailscale/bin/opute-provider-tailscale`
- systemd user unit: `opute-provider-tailscale.service` (active), port **4323**
- Listed beside `com.opute.cloudflare` / `com.opute.k3s` under providers + `systemctl --user list-units opute-provider-*`
- `OPUTE_TAILSCALE_BACKEND=live` with env files for API/auth (not committed)

## Tailscale Kubernetes Operator + stable public ingress

- ACL: `tagOwners` for `tag:k8s-operator` / `tag:k8s`; `nodeAttrs` funnel for `tag:k8s` and `*`
- OAuth client created via API (`keyType=client`, scopes `devices`+`auth_keys`, tag `tag:k8s-operator`) stored only under `~/.config/opute/` (not in git)
- Helm: `tailscale/tailscale-operator` in namespace `tailscale` (operator pod Ready)
- IngressClass `tailscale` present
- Ingress `opute-public/public-demo-funnel`:
  - `ingressClassName: tailscale`
  - annotation `tailscale.com/funnel: "true"`
  - ADDRESS: **`opute-public.tail229553.ts.net`** (not `opute-ha-a.…`)
- Proxy pod `ts-public-demo-funnel-*` serves Funnel → Service `public-demo`
- Manifest: `plugins/tunneling/tailscale/recipes/manifests/public-demo-operator-funnel.yaml`

### Public proof

- `curl -4 https://opute-public.tail229553.ts.net/` → **HTTP 200** `opute-public-ingress ok`

### Fail-closed stable=

- `operatorMode=true` without `operatorEvidence` / `ingressClassName=tailscale` fails closed
- Live drive `TestLiveDriveOperatorStableIngress` records `stable=true` only with Operator evidence fields

### Funnel-host loss (ha-a k3s stopped)

- Proxy was on **opute-ha-b**; public HTTPS remained **HTTP 200** while ha-a k3s was down
- Kubernetes API/etcd writes failed (2-node embedded etcd quorum loss) — still **serving-continuity-only**
- After ha-a restart: both nodes Ready; public HTTPS 200

### Recipe wiring

- `plugins/tunneling/tailscale/recipes/public-ingress.yaml` accepts `operatorMode`, `ingressClassName`, `operatorEvidence`, `hostname`, `endpoint`
- Live MCP enroll + ensure-public-ingress (operatorMode) driven against host agent `host-zephyrus-ef47fbbf`

### Path separation

- Private mesh remains Tailscale CGNAT between ha-a/ha-b for cluster traffic
- Public Funnel terminates on Operator proxy, not node-local `tailscale funnel` on ha-a
- No secrets in this file
