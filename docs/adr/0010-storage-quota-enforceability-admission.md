# ADR 0010: Storage quota enforceability is resolved at admission

Status: Accepted

## Context

Incus accepts a root disk `size` on every storage pool, but only some storage
drivers turn it into a real allocation. The `dir` driver enforces a size only
when its source filesystem is ext4 or XFS mounted with project quotas; without
them the size is recorded in instance config and never applied.

`create_vm`, `provision_vm`, and `provision_container` set a root disk size and
then reported it back — through the provision result and through the
`limits.disk` projection in Incus inventory — without ever establishing that
the pool could enforce it. `provision_vm` and `provision_container` also
fabricated defaults (10GiB and 20GiB) when the caller requested no bound, so
the false report was unconditional.

The validated WSL2 host runs a single `dir` pool sourced at
`/var/lib/incus/storage-pools/default` on ext4 mounted without `prjquota`.
Every disk bound this repository has ever set on that host was accepted and
discarded. Guests were free to consume the whole host filesystem while
inventory showed them bounded, which is how accumulated container images
exhausted host storage.

Reporting a limit that does not exist is a durable-truth defect (C-07): the
Host Agent's observation was not a validated observation. It must be repaired
at its root rather than compensated for by a caller-side heuristic.

## Decision

Root disk quotas are admitted, not assumed.

Before applying a root disk size, the Host Agent resolves the storage pool the
instance will actually use. A create/provision call uses the default profile's
root pool when it declares one, otherwise the resolved default pool. A
post-create `update_vm_resources` disk mutation uses the instance's own root
device pool, which may differ from the profile default. Admission then
determines whether that pool enforces quotas:

- `btrfs`, `zfs`, `lvm`, and `ceph` enforce a size unconditionally.
- `dir` enforces a size only when its source path resolves to an ext4 or XFS
  mount carrying project quotas.
- Any unrecognized driver is treated as non-enforcing, so a new or misreported
  driver fails closed instead of silently dropping the bound.

Admission then splits on whether the caller asked for the bound:

- An **explicitly requested** quota that cannot be enforced **fails closed**
  with an error naming the pool, the driver, and the remedy. Accepting it would
  promise a limit the guest does not have.
- An **implicit default** quota is **dropped** rather than applied. The caller
  asked for no bound, so provisioning omits the device size and observed
  inventory truthfully reports no limit. Defaults are requests that admission
  may decline, never guarantees.

A quota that admission declined is never written to instance config, and the
post-create resize is skipped, so no projection can restate it.

`update_vm_resources` is the post-create setter for the same bound. It takes
an optional `disk` field alongside `cpus` and `memory`, requires at least one
of the three, and runs the same admission function against the instance's
actual pool. Disk updates are grow-only: a requested size smaller than the
observed root device is refused rather than applied as a number Incus or the
backing driver will ignore. The applied result reports `disk`, `enforced`,
pool, driver, and reason so a recipe cannot claim a bound the host did not
obtain.

## Consequences

On a host whose only pool is a non-quota `dir` pool, callers that explicitly
request a disk size now receive an error where they previously received a
silent no-op. This is intended: the operation never did what it claimed. The
remedy is a quota-capable pool driver, or project quotas enabled on the dir
pool's filesystem.

Callers that request no disk size continue to provision, now without a
fabricated limit in their observed state.

Enforcement lives in one admission function shared by the VM and system
container create paths and by `update_vm_resources`, so a future runtime kind
cannot acquire an unchecked quota path. High-availability recipes that
provision Incus guests declare an explicit disk and fail closed when
`get_host_info.rootDiskQuota.enforced` is false; they do not omit `disk` to
bypass admission. The invariant is recorded as `storage-quota-enforceability`
in the Opute decision store, with driver, filesystem, mount-resolution,
post-create update, grow-only, and fail-closed tests as its verifier.

In-cluster PVC bounds (PostgreSQL, OCI registry) are a separate plane. See
ADR 0014.

## Related

- ADR-0007 — runtime-kind admission and target boundaries
- ADR-0014 — in-cluster PVC storage limits and HA recipe-declared bounds
- Cordis invariant C-07 — durable truth
