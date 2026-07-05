# Dynamic DNS vs Cloudflare Tunnel

Host public exposure in Opute can use different DNS strategies. This document explains when to use **dynamic DNS (DDNS)** versus **Cloudflare Tunnel** (`cloudflared`), and why they must not fight each other on the same hostname.

## Summary

| Approach | DNS record | Connector | Best for |
|----------|------------|-----------|----------|
| **Dynamic DNS** | **A** (or AAAA) → your public IP | None (traffic hits your IP / router) | Home IP, port forwarding, whole-machine exposure |
| **Cloudflare Tunnel** | **CNAME** → `{tunnelId}.cfargotunnel.com` | `cloudflared` on the execution host | Local services on loopback, NAT/CGNAT, no inbound ports |

**Rule of thumb:** hostname → local port on a machine behind NAT → **tunnel**. Public IP + port forwarding + “point DNS at my house” → **DDNS**. Do **not** run both on the same hostname.

---

## Dynamic DNS (e.g. `favonia/cloudflare-ddns`)

A DDNS updater periodically sets **A records** so names like `blog.example.com` point at your **current public IP**.

### Good fit

- Traffic should reach your **public IP** directly (home server, VPS with a routable address).
- You accept **router port forwarding** and host firewall rules (80/443 or other ports).
- You expose **many services by IP** without per-service tunnel ingress.
- You need **non-HTTP TCP** on fixed ports (SSH, game servers, custom protocols) without tunnel ingress rules.
- You only need to keep an A record in sync when your ISP changes your IP.

### Limitations

- Requires a **routable public IP** (problematic behind CGNAT).
- You manage **port forwarding, firewall, and often TLS** yourself (Cloudflare orange-cloud proxy helps HTTP only).
- One IP per machine — less clean than per-hostname → local port mapping.

### Example in this workspace

The sibling [`cloudflare-ddns`](../../cloudflare-ddns) compose updates `opute.io`, `www.opute.io`, and optionally `blog.opute.io` to the home public IP. That is a **separate** exposure model from tunnel-based `blog.opute.io`.

---

## Cloudflare Tunnel (`cloudflared`)

`cloudflared` maintains an **outbound** connection to Cloudflare. DNS is a **CNAME** to the tunnel, not an A record to your IP. Ingress rules map `hostname` → `http://127.0.0.1:port` (or another local target).

### Good fit

- Service listens on **localhost** (e.g. Docker on `http://127.0.0.1:80`).
- Host is behind **NAT**, residential ISP, or **no inbound ports** are desired.
- **Per-hostname routing** to different local ports without public IP plumbing.
- Control plane (Opute CPC) and app run on **different OS contexts** (e.g. WSL CPC + Windows Docker) — tunnel runs on the host where `localTarget` is reachable.

### Limitations

- Requires **`cloudflared`** (or Opute host agent tools such as `ensure_cloudflared_tunnel`).
- Cloudflare account, tunnel, and DNS configuration.
- Not a drop-in for “expose raw TCP on port 22” without explicit tunnel config.

### Opute host agent role

Native host tools (catalog-excluded) own the **local** side:

- `ensure_cloudflared_tunnel` — install/run `cloudflared` with run token
- `probe_host_exposure` — local target + tunnel health
- `remove_host_exposure` — stop connector

Upstream Cloudflare API work (tunnel create, ingress, DNS CNAME) is moving to a **host MCP plugin** runtime (Cloudflare MCP on the execution host), not the Opute CPC server. See the Opute monorepo plan for host MCP plugins.

### `blog.opute.io` release path

What worked for live acceptance:

1. Blog stack ([`blog-opute`](../../blog-opute)) serving on **Windows** `http://127.0.0.1:80`
2. Bridge host exposure binding → tunnel + **CNAME**
3. **`cloudflared`** on **Windows** (same machine as the blog)
4. Public HTTPS probe on `https://blog.opute.io`

The DDNS container was **not** required for that path. The release script only reused `cloudflare-ddns/.env` as a convenient API token file.

---

## Conflict: do not mix on one hostname

Tunnel exposure **replaces A/AAAA with CNAME** before syncing the tunnel CNAME (see Opute `HostExposureSyncService.replaceDnsWithCname`).

If a DDNS updater (e.g. `favonia/cloudflare-ddns`) remains configured for the same name, it will **recreate A records** on its interval (~5 minutes) and fight the tunnel CNAME.

**When enabling tunnel exposure for a hostname:**

1. Remove that hostname from DDNS `DOMAINS`, or stop the DDNS container.
2. Let tunnel sync create the CNAME to `{tunnelId}.cfargotunnel.com`.
3. Run `cloudflared` on the **execution host** (where `localTarget` resolves).

---

## How this maps to Opute product paths

| Concern | Owner |
|---------|--------|
| DDNS Docker / A record updaters | **Out of scope** for tunnel exposure v1; optional future “public IP exposure” mode |
| Tunnel + CNAME + ingress | Host MCP plugin (Cloudflare) + native `cloudflared` tools |
| Binding state, `runToken`, conflicts | Opute Bridge |
| `executionHostId` (WSL vs Windows) | `resolve_service_public_endpoint` on Bridge |

---

## Related reading

- Opute monorepo: `AGENTS.md` → **Host public exposure**
- Host agent ops: `internal/ops/exposure_tunnel.go`, `internal/ops/exposure_tunnel_windows.go`
- Tool catalog: `schemas/all-tools.json` (`ensure_cloudflared_tunnel`, `probe_host_exposure`, …)
