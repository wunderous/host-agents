# Tasks

- [x] ADR-0016 + capability constants + three schemas + embed
- [x] Catalog `capabilityId` + `DisplaceCapabilityFamilies` wire-up
- [x] Tailscale provider: three services + new OperationIDs + dispatch aliases
- [x] Cloudflare provider: membership + public-ingress only (honest; no private-mesh)
- [x] Neutral vendor-bundle recipes; update overlay-mesh/public-ingress
- [x] Contract/neutrality tests for three-seam IDs
- [x] E2E `*.opute.io` guard script (baseline DNS/TLS/HTTPS/negative pass)
- [x] Live Tailscale-exclusive catalog displace proof
- [x] Live Tailscale seam ownership + ha-a continuity (Funnel + *.opute.io)
- [x] E2E guard during ha-a loss
- [x] EVIDENCE.md updated; commit/push next

- [x] Publish `mesh-runtime.v1` as separate install/configure seam
- [x] Wire Tailscale ensure-agent/ensure-control-plane; enroll fail-closed without agent
- [x] Vendor-bundle calls mesh-runtime before enroll

- [x] Stabilize Tailscale active generation after seam activate (idempotent activateProviderGeneration)
