# ADR 0014: In-cluster PVC limits and HA recipe-declared storage bounds

Status: Accepted

## Context

ADR 0010 repaired Incus root-disk durable truth: a size is written only when
the pool can enforce it. Two adjacent gaps remained.

First, `update_vm_resources` could change CPU and memory but not disk, so an
already-running guest had no typed way to receive a bound. High-availability
recipes therefore omitted `disk` on `provision_vm` / `provision_container`.
On a non-enforcing `dir` pool that omission was the only way to provision; it
also left HA guests unbounded.

Second, Host Agent-managed in-cluster stores — CloudNativePG `storage.size`
and the in-cluster OCI registry PVC — take Kubernetes quantities. Those
requests are real once a StorageClass binds them, but a later smaller size is
ignored by CSI, and a grow on a class without `allowVolumeExpansion` is a
no-op that would still be reported as the new size. PostgreSQL reconcile also
treated a ready cluster as configured when only a resource-profile annotation
matched, so an explicit `storageSize` change never reapplied.

Host Podman reclaim, guest TRIM, and WSL VHDX compact are cleanup, not quota.
They stay out of this decision.

## Decision

Storage limits are set only through typed Host Agent capabilities, and only
when the backing system can enforce them.

- **Incus** remains ADR 0010: one `admitRootDiskQuota` seam for create and
  for `update_vm_resources.disk`, resolved against the pool the instance
  actually uses, grow-only after create.
- **PostgreSQL** (`reconcile_postgresql_service.storageSize`) and **OCI
  registry** (`install_oci_registry.storageSize`) admit a Kubernetes
  quantity. An omitted size may still apply the plane's documented default
  because kube will enforce a PVC request; that is not the Incus implicit-
  default case. An explicit shrink fails closed. A grow against an existing
  claim fails closed unless the StorageClass declares
  `allowVolumeExpansion: true`. Observed status reports the size that was
  admitted, not the caller's unenforced request.
- **HA recipes** that provision Incus guests (`k3s-two-node-public-mesh`,
  `k3s-two-node-real-join`, and any successor that still creates guests)
  require explicit disk inputs and observe `get_host_info` before provision
  so the run records `rootDiskQuota`. Fail-closed is Host Agent admission:
  an explicit `disk` on `provision_vm` / `provision_container` is refused
  when the pool cannot enforce it. Platform `host-recipe.v1` nodes are
  action XOR wait; the coordinator does not evaluate host-plan `validate`
  assertions, so recipes must not depend on those blocks to refuse an
  unbounded guest. They keep CPU and memory inputs. They do not omit
  `disk` to run on a non-enforcing pool. Preparing a quota-capable Incus
  pool is not an implicit side effect of provision; until the pool
  enforces, HA with Incus root-disk limits does not run.

Domains stay isolated: Incus owns Incus root disks, postgres owns CNPG PVC
size, oci owns registry PVC size. Kubernetes is the in-cluster surface; K3s
is not named on these tools.

## Consequences

The validated WSL2 host cannot run those HA recipes until its Incus pool
enforces quotas. That is the intended fail-closed outcome, not a reason to
restore fabricated `limits.disk` values.

A PostgreSQL or registry PVC grow on `local-path` (no expansion) fails
closed with an error naming the StorageClass, rather than applying a
manifest kube will ignore.

## Related

- ADR-0010 — Incus root-disk enforceability admission
- Cordis invariant C-07 — durable truth
