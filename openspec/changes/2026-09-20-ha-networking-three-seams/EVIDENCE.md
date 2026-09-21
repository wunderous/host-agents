# Evidence record

## 1. Definitions + ADR (landed)

- ADR-0016: `docs/adr/0016-ha-networking-three-seam-definitions.md`
- Capability IDs: `mesh-membership.v1`, `private-mesh.v1`, `public-ingress.v1`
- Schemas embedded: `schemas/mesh-membership.v1.json`, `private-mesh.v1.json`, `public-ingress.v1.json`
- `network-overlay.v1` retained as deprecated fan-out / activation serving-contract alias

## 2. Providers (landed in-tree)

- `com.opute.tailscale` Provides all three seams; recipes use neutral seam ops; `ha-network-bundle.yaml` vendor-bundle
- `com.opute.cloudflare` Provides mesh-membership + public-ingress; **omits** private-mesh (honesty test)
- Catalog descriptors carry `capabilityId`; `DisplaceCapabilityFamilies` wired into provider activation

## 3. Unit / contract tests

- `go test ./internal/catalog/` — pass (includes displace test)
- `go test ./test/contract/` — pass (three-seam neutrality)
- `go -C plugins/tunneling/tailscale test ./...` — pass
- `go -C plugins/tunneling/cloudflare test ./...` — pass
- `go test ./internal/recipe/ ./internal/hostmcp/` — pass

## 4. E2E `*.opute.io` guard (baseline pass)

Command: `OPUTE_E2E_JSON_OUT=/tmp/opute-io-guard.json ./scripts/e2e-opute-io-guard.sh`

```json
{
  "startedAt": "2026-09-21T01:00:05Z",
  "endedAt": "2026-09-21T01:00:07Z",
  "hosts": "www.opute.io,opute.io",
  "expectStatus": "200",
  "failures": 0,
  "results": [
    {
      "host": "www.opute.io",
      "dnsOk": true,
      "tlsOk": true,
      "httpOk": true,
      "negativeOk": true,
      "status": "200",
      "negativeStatus": "404",
      "dns": "104.21.91.7 172.67.163.253 2606:4700:3030::6815:5b07 2606:4700:3034::ac43:a3fd"
    },
    {
      "host": "opute.io",
      "dnsOk": true,
      "tlsOk": true,
      "httpOk": true,
      "negativeOk": true,
      "status": "200",
      "negativeStatus": "404",
      "dns": "104.21.91.7 172.67.163.253 2606:4700:3030::6815:5b07 2606:4700:3034::ac43:a3fd"
    }
  ]
}

```

Continuity (ha-a Funnel-host loss) still required under Tailscale-exclusive bundle before goal complete.

## 5. Still required for goal completion

- Live `opute.provider.status` + `get_capability_catalog` proving per-seam exclusive ownership after Tailscale activate-displace
- Re-run Tailscale vendor-bundle against opute-ha-a/b; public HTTPS + ha-a loss survival
- Guard re-run during ha-a loss window against `*.opute.io` (and any deploy-emitted hostnames)
- Commit/push host-agents only after those gates pass


## 6. Live catalog (host-zephyrus-ef47fbbf)

After rebuilding Host Agent + Tailscale provider and `opute.provider.reload` of Tailscale with plugin.yaml + activate recipe:

- `mesh-membership.v1` → **com.opute.tailscale** only
- `private-mesh.v1` → **com.opute.tailscale** only
- `public-ingress.v1` → **com.opute.tailscale** only
- Cloudflare still publishes deprecated `network-overlay.v1` legacy ops pending full CF recipe-input reload (in-tree Provides already mesh-membership + public-ingress, no private-mesh)

Funnel spot-check: `https://opute-public.tail229553.ts.net/` still reachable (see continuity section when ha-a loss is re-run).

## 7. E2E guard re-run

```json
{
  "startedAt": "2026-09-21T01:06:03Z",
  "endedAt": "2026-09-21T01:06:04Z",
  "hosts": "www.opute.io,opute.io",
  "expectStatus": "200",
  "failures": 0,
  "results": [
    {
      "host": "www.opute.io",
      "dnsOk": true,
      "tlsOk": true,
      "httpOk": true,
      "negativeOk": true,
      "status": "200",
      "negativeStatus": "404",
      "dns": "104.21.91.7 172.67.163.253 2606:4700:3030::6815:5b07 2606:4700:3034::ac43:a3fd"
    },
    {
      "host": "opute.io",
      "dnsOk": true,
      "tlsOk": true,
      "httpOk": true,
      "negativeOk": true,
      "status": "200",
      "negativeStatus": "404",
      "dns": "104.21.91.7 172.67.163.253 2606:4700:3030::6815:5b07 2606:4700:3034::ac43:a3fd"
    }
  ]
}

```


## 8. Continuity: ha-a Funnel-host loss (2026-09-21)

- Stopped k3s on opute-ha-a (systemctl is-active → inactive)
- During loss: Funnel opute-public.tail229553.ts.net → 200; www.opute.io/opute.io → 200
- E2E guard during loss exited 0:

`json
{
  "startedAt": "2026-09-21T01:07:36Z",
  "endedAt": "2026-09-21T01:07:37Z",
  "hosts": "www.opute.io,opute.io",
  "expectStatus": "200",
  "failures": 0,
  "results": [
    {
      "host": "www.opute.io",
      "dnsOk": true,
      "tlsOk": true,
      "httpOk": true,
      "negativeOk": true,
      "status": "200",
      "negativeStatus": "404",
      "dns": "104.21.91.7 172.67.163.253 2606:4700:3030::6815:5b07 2606:4700:3034::ac43:a3fd"
    },
    {
      "host": "opute.io",
      "dnsOk": true,
      "tlsOk": true,
      "httpOk": true,
      "negativeOk": true,
      "status": "200",
      "negativeStatus": "404",
      "dns": "104.21.91.7 172.67.163.253 2606:4700:3030::6815:5b07 2606:4700:3034::ac43:a3fd"
    }
  ]
}

`

- Restored k3s on ha-a afterward (active).


## 9. Live per-seam displace (CF -> Tailscale)

Sequence on host-zephyrus-ef47fbbf:

1. Cloudflare activate (`mode=activate`) published `mesh-membership.v1` + `public-ingress.v1` and retired deprecated `network-overlay.v1` ops.
2. Tailscale activate/reload then **displaced** Cloudflare from those families.
3. Final catalog owners (exclusive):
   - `mesh-membership.v1` → com.opute.tailscale
   - `private-mesh.v1` → com.opute.tailscale
   - `public-ingress.v1` → com.opute.tailscale
4. Cloudflare retains `tunneling.v1` (separate seam); does not claim `private-mesh.v1`.



## 10. Remaining live lifecycle

Catalog exclusivity for the three seams under Tailscale is observed after activate/displace.
`opute.provider.status` for Tailscale intermittently reports `active=false` after reload
when the activate recipe resumes an old idempotency key (`…-default`), leaving probe
dispatch with `provider generation is no longer active`. Follow-up: pass a unique
`activationNonce` through recipe inputs on every reload, and ensure Cordis displace +
catalog displace stay aligned when Cloudflare tunneling remains installed.

Unit/contract coverage for displace + three-seam providers is green.
`*.opute.io` e2e guard passed including during ha-a k3s stop.
