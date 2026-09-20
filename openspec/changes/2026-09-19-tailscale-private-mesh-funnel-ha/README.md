# tailscale-private-mesh-funnel-ha

This change specifies a Tailscale implementation of the provider-neutral
network-overlay capability for a two-server Kubernetes deployment. Tailscale
provides private node-to-node transport; Funnel is limited to the explicitly
selected public application ingress. Cloudflare remains a supported alternate
provider and is not removed by this change.

The design follows the three-role capability seam from [DeepSeek Harness]
(https://deepseek-harness.github.io/deepseek-harness/en/develop/practice/):

1. **Service Definition** — neutral typed overlay and ingress contracts.
2. **Service Provider** — Tailscale lifecycle, mesh, Serve, and Funnel adapter.
3. **Consumer** — Opute recipes and Host Agent MCP consumers that know only the
   neutral contract.

Read [design.md](design.md) first. The interactive architecture visualization
is preserved at [visuals/tailscale-two-node-ha.html](visuals/tailscale-two-node-ha.html).

This is a planning artifact only. The Tailscale provider is not claimed to be
implemented until its separate-process MCP, lifecycle, cleanup, and live
failure evidence exists.
