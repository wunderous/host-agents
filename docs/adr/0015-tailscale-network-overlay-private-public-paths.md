# ADR-0015: Tailscale network overlay and private/public path separation

Status: accepted
Date: 2026-09-20

## Decision

The Host Agent publishes one provider-neutral `opute.capability.network-overlay.v1`
Service Definition. Cloudflare and Tailscale are replaceable Service Providers of
that definition. Consumers (HA recipes, public-ingress recipes, MCP tools) depend
on the definition only.

Private cluster traffic (Kubernetes API, control-plane, CNI, health, datastore)
MUST use an admitted private overlay path. Public Funnel / public-ingress traffic
is scoped to an application gateway or operator-managed Ingress and MUST NOT
satisfy a private-path prerequisite.

Two-node availability claims are gated by an explicit `datastoreMode`:

| Mode | Allowed claim |
| --- | --- |
| `external-datastore` | Control-plane write continuity, only with independent datastore evidence |
| `embedded-etcd-two-node` | Serving-continuity-only; writes require typed recovery acknowledgement |

A result with two ready servers and embedded two-member etcd MUST NOT project as
generic `ha=true`. Observations carry `availabilityClass`, `recoveryPolicy`,
`pathClass` (`private-mesh` or `public-ingress`), and `stable` for public
endpoints. Node-specific Funnel URLs are `stable=false` and MUST NOT be stored as
host-independent cluster endpoints.

Credentials are transient typed inputs. Credential kind is validated before use
(API credential is not a node auth key). Provider results, tasks, durable state,
logs, and evidence contain only schema-redacted projections. Teardown removes only
resources owned by the recorded provider generation.

Canonical Host Agent identity remains the opaque Host Agent ID (ADR-0006).
Tailscale IPs, MagicDNS names, and Funnel URLs are transport coordinates only.

## Invariant delta

| Field | Rule |
| --- | --- |
| Statement | Private cluster traffic uses an admitted provider-neutral overlay; public Funnel is scoped application ingress; datastore mode controls the two-node availability claim. |
| Owner | Host Agent capability/schema and provider lifecycle; Opute recipe/routing consumers. |
| Authority | This ADR, OpenSpec `2026-09-19-tailscale-private-mesh-funnel-ha`, Cordis development guide. |
| Evidence | Separate-process MCP tests, schema/redaction tests, lifecycle cleanup tests, private/public path probes. Live Tailscale mesh evidence is recorded separately when credentials and disposable targets are available. |

## Consequences

- `plugins/tunneling/tailscale/` implements `com.opute.tailscale` as a separate Streamable HTTP MCP process.
- Existing Cloudflare overlay operations remain valid; Tailscale also exposes the enroll / private-mesh / private-service / public-ingress / path-aware probe / promote operations defined by the OpenSpec design.
- Host Agent core must not import Tailscale packages or Tailscale-named tools.
