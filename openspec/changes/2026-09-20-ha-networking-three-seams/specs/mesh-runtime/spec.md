# mesh-runtime.v1

## Purpose

Provider-neutral install/configure seam for mesh agent software and control-plane
prerequisites. Membership, private-mesh, and public-ingress MUST NOT silently
install those prerequisites.

## Requirements

### Requirement: Separate prerequisite seam
The system SHALL publish `opute.capability.mesh-runtime.v1` with operations
`validate`, `ensure-agent`, `ensure-control-plane`, and `status`.

### Requirement: Agent install is ensure-agent
`ensure-agent` SHALL install and start the provider mesh agent on the target
(e.g. Tailscale `tailscaled`) when missing, and SHALL be idempotent when present.

### Requirement: Control-plane prerequisites are ensure-control-plane
`ensure-control-plane` SHALL record/verify operator or ingress-class readiness
for public ingress. It SHALL fail closed if `ensure-agent` has not succeeded.

### Requirement: Enroll does not install
`mesh-membership.enroll` SHALL NOT install the mesh agent. Missing agent SHALL
fail with guidance to call `mesh-runtime.ensure-agent`.

### Requirement: Vendor bundle orders runtime first
The HA vendor-bundle recipe SHALL call `ensure-agent` (and `ensure-control-plane`
when operator mode is enabled) before `mesh-membership.enroll`.
