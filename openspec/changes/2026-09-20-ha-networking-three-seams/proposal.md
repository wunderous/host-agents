# Proposal: Three exclusive HA networking seams

## Why

`network-overlay.v1` forced Cloudflare and Tailscale into one mega-contract with
split OperationIDs. Split into three definitions so exclusivity, honesty, and
consumer neutrality are enforceable per concern.

## What changes

- Publish mesh-membership.v1, private-mesh.v1, public-ingress.v1.
- Per-seam activate-displace in the Host Agent catalog.
- Tailscale implements all three; Cloudflare membership + public-ingress (honest).
- Vendor-bundle recipes; network-overlay.v1 deprecated.
- Automated fail-closed e2e guard for *.opute.io.

## Non-goals

- Mix-and-match vendors across seams as a supported product path
- 3-node etcd / deleting Cloudflare / general Funnel TCP / Web Client UI
