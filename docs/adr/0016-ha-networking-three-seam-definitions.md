# ADR-0016: HA networking as three exclusive Service Definitions

Status: accepted
Date: 2026-09-20
Supersedes (in part): ADR-0015 single `network-overlay.v1` consumer contract

## Decision

HA networking is published as **three** provider-neutral Service Definitions.
Composition (membership -> private mesh -> public ingress) is a **recipe /
vendor-bundle**, not a fourth mega-definition.

| Service Definition | Job | Neutral ops |
| --- | --- | --- |
| `opute.capability.mesh-membership.v1` | Host joins a trust domain | enroll, status, leave |
| `opute.capability.private-mesh.v1` | East-west reachability | ensure, ensure-service, probe |
| `opute.capability.public-ingress.v1` | North-south stable HTTPS | ensure, promote, probe |

`opute.capability.network-overlay.v1` remains only as a **deprecated fan-out /
migration alias**. New consumers MUST bind to the three definitions.

## Roles (ADR-0015 / OpenSpec)

- **Service Definition** — the consumer-facing contract (ops, schemas, Requires/Produces).
- **Service Provider** — `com.opute.tailscale` / `com.opute.cloudflare` implement one or more definitions.
- **Consumer** — Host Agent recipes and MCP plans call definition ops only (no provider product names).

## Exclusivity

Exclusivity is **per Service Definition**, not per vendor company.
Activating a provider for a definition publishes that definition's full op set
and **displaces** any other provider's catalog entries for the same
`capabilityId`. Ambiguous dual ownership of a seam is fail-closed.

v1 **vendor-bundle** policy: operators activate one recommended bundle
(Tailscale for all three, or Cloudflare for membership + public-ingress where
parity exists). Mix-and-match across seams is not a supported product path yet.

## Provider honesty

- Tailscale MUST implement all three for HA.
- Cloudflare MUST implement at least membership + public-ingress; private-mesh
  only when honest. Incomplete seams are omitted or fail-closed — never
  split-catalog stubs sharing OperationIDs with another provider.

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
