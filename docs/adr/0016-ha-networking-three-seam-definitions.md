# ADR-0016: HA networking exclusive Service Definitions

Status: accepted
Date: 2026-09-20
Supersedes (in part): ADR-0015 single `network-overlay.v1` consumer contract

## Decision

HA networking is published as **four** provider-neutral Service Definitions.
Composition (runtime prerequisites -> membership -> private mesh -> public
ingress) is a **recipe / vendor-bundle**, not a mega-definition.

| Service Definition | Job | Neutral ops |
| --- | --- | --- |
| `opute.capability.mesh-runtime.v1` | Install/configure mesh agent + control-plane prerequisites | validate, ensure-agent, ensure-control-plane, status |
| `opute.capability.mesh-membership.v1` | Host joins a trust domain | enroll, status, leave |
| `opute.capability.private-mesh.v1` | East-west reachability | ensure, ensure-service, probe |
| `opute.capability.public-ingress.v1` | North-south stable HTTPS | ensure, promote, probe |

`mesh-runtime.v1` owns software install and control-plane readiness (e.g. node
agent package, Kubernetes Operator / ingress class). Membership and ingress
MUST NOT silently install those prerequisites; consumers call mesh-runtime
ops first (or via the vendor-bundle recipe).

`opute.capability.network-overlay.v1` remains only as a **deprecated fan-out /
migration alias**. New consumers MUST bind to the four definitions (including mesh-runtime).

## Roles (ADR-0015 / OpenSpec)

- **Service Definition** — the consumer-facing contract (ops, schemas, Requires/Produces).
- **Service Provider** — `com.opute.tailscale` / `com.opute.cloudflare` implement one or more definitions.
- **Consumer** — Host Agent recipes and MCP plans call definition ops only (no provider product names).

## Exclusivity

Exclusivity is **per Service Definition**, not per vendor company.
Activating a provider for a definition publishes that definition's full op set
and **displaces** any other provider's catalog entries for the same
`capabilityId`. Ambiguous dual ownership of a seam is fail-closed.

v1 **vendor-bundle** policy: operators activate one provider per seam (runtime → membership → private-mesh → public-ingress) via recipes; XOR exclusivity remains per Service Definition.

## Public ingress / stable

`stable=true` is meaningful only on **public-ingress**. Node-specific Funnel
URLs remain `stable=false`. Production dogfood surfaces under `*.opute.io`
MUST pass the automated e2e guard (DNS, TLS, HTTPS, negative path, ha-a
continuity) before goal completion.

## Consequences

- Catalog descriptors carry `capabilityId` so displace-on-activate can retire
  another provider's seam without requiring OperationID collisions first.
- Recipes ship a Tailscale vendor-bundle path; Cloudflare bundle only where
  declared.
- ADR-0015 private/public path separation and datastore-mode gates remain in force.
