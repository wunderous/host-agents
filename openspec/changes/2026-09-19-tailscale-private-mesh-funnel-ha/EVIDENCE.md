# Evidence record

## Contract / unit / MCP wire (fake backend)

- `go -C plugins/tunneling/tailscale test ./...` — pass (includes Streamable HTTP MCP wire)

## Live provider MCP + cluster-scoped Funnel ingress (2026-09-20)

**Status: verified.**

### Traefik vs Tailscale :443

- Traefik Service: LoadBalancer → **NodePort** (web `30950`, websecure `30565`).
- Removes host VIP on `:443` that previously presented `TRAEFIK DEFAULT CERT` on the Tailscale IP.

### Scoped application ingress

- Manifest: `plugins/tunneling/tailscale/recipes/manifests/public-demo-ingress.yaml`
- Namespace `opute-public`: Deployment/Service/Ingress for host `opute-ha-a.tail229553.ts.net`
- Funnel: `https://opute-ha-a.tail229553.ts.net/` → `http://127.0.0.1:30950` (Traefik → Ingress → Service)
- Node Funnel canary on `:8787` retired
- Endpoint recorded with **`stable=false`** (node-specific Funnel URL)

### Live MCP drive (`OPUTE_LIVE_DRIVE=1`, `OPUTE_TAILSCALE_BACKEND=live`)

Host agent: `host-zephyrus-ef47fbbf` (`OPUTE_HOST_AGENT_ENDPOINT`).

- enroll `container:local:opute-ha-a` → ready, meshIp `100.97.79.97`
- ensure-private-mesh (peer adopt ha-b `100.93.139.86`) → ready,
  `datastoreMode=embedded-etcd`, `availabilityClass=serving-continuity`,
  `reverseProbeMethod=tailscale-ping-from-source`
- ensure-public-ingress `localTarget=http://127.0.0.1:30950` → ready,
  endpoint `https://opute-ha-a.tail229553.ts.net`, **stable=false**
- probe `pathClass=public-ingress` → ready

### Public proof

- DNS A: `208.111.35.209`, `208.111.34.11`
- `curl https://opute-ha-a.tail229553.ts.net/` → HTTP 200 `opute-public-ingress ok`

### Failure injection

Stop k3s on ha-b: public Funnel/Ingress stayed HTTP 200; Kubernetes writes failed (etcd quorum loss); recovery restored both Ready + ConfigMap write. Serving-continuity-only — no durable-HA claim.

### Cleanup

HA topology retained as end state. No secrets in this file.
