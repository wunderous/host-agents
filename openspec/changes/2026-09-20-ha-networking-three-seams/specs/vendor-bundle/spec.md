# vendor-bundle

## Requirement: Composition is a recipe

Tailscale vendor-bundle chains membership -> private-mesh -> public-ingress.

### Requirement: Runtime prerequisites first
The vendor-bundle SHALL invoke `mesh-runtime.ensure-agent` before membership enroll.
