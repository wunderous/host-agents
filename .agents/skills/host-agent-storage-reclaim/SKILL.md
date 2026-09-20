---
name: host-agent-storage-reclaim
description: Use when reclaiming host Podman bytes, k3s/containerd guest images, in-cluster registry PVCs, guest TRIM, or WSL VHDX compact via Host Agent MCP tools.
---

# Host Agent storage reclaim

Two planes. Call the tools that match the bytes. Never `podman system prune`,
`crictl rmi --all`, or delete Postgres PVCs / Incus instances. Do not enable
WSL sparse VHDX (`--set-sparse` / `--allow-unsafe`).

Load this skill instead of treating host Podman cleanup as cluster reclaim.

## Host Podman (`~/.local/share/containers`)

`inspect_container_storage` then `cleanup_container_storage` `{ dryRun: true,
minAgeSeconds >= 3600 }`. Runtime is `auto`|`podman` only. Age floor 3600s.
Heavy class {2 cores, 2 GiB, 8 tasks}.

`cleanup_container_storage` skips a digest that still has two tags (release +
rollback). Retire a tag deliberately (`podman rmi <tag>`) then rerun.
Dangling images held by **buildah working containers** (`podman ps -a
--external`, status Storage) are out of that tool; `buildah rm -a` then
`podman image prune` is the operator path. Keep images a running build still
needs.

## k3s guest (Incus containerd + registry PVC)

Host Podman tools do not see this plane. Run on the **Host Agent that owns
the Incus guest**. Ubuntu cannot `incus exec` a guest that only exists inside
another WSL distro. A cell host-agent without a connected k3s provider is not
a substitute — call that node’s k3s provider capability tools
(`inspect-guest-storage`, `prune-unused-images`, `trim-guest-storage`) with
the guest `targetUri`.

Recipe `plugins/kubernetes/k3s/recipes/storage-reclaim.yaml` via
`run_host_local_recipe` (poll `get_host_plan_run`; mutating nodes have
readiness `validate` against `inspect_guest_storage`). Or call tools in
order:

1. `list_kubernetes_clusters` → cluster URI
2. `inspect_guest_storage` `{ uri, includeRegistry? }`
3. `prune_unused_cluster_images` `{ uri, dryRun: true, minAgeSeconds >= 3600 }`
4. Review keep-set vs reclaimable, then `dryRun: false`. No `crictl rmi --all`.
5. Optional `garbage_collect_cluster_registry` `{ uri, includeRegistry: true }`
   (default **off**). The GC Job omits a configMap volume when the live deploy
   has none; do not invent a ConfigName.
6. `trim_guest_storage` `{ uri }` — guest `fstrim` first; unprivileged Incus
   FITRIM EPERM falls back to host `sudo -n fstrim -v /` and reports
   `scope: guest|host`.

Two-node k3s etcd: stopping one node loses quorum until it returns.

## WSL VHDX compact

Not a recipe node. `compact_wsl_disk` `{ distro, dryRun? }` requires
**Stopped** and an unlocked VHDX. `terminate_wsl_distribution` is not enough
while a sibling distro keeps `vmmemWSL`. Fail closed (`isError` with
`structuredContent.code=wsl_compact_closed`): live distro, Running, or
locked — compact then needs `shutdown_wsl` (kills this agent). Actual compact
is `Optimize-VHD -Mode Full` and needs an elevated Hyper-V admin token.
WSL 2.7.x has no `wsl --manage --compact`.

Operator fallback when UAC is blocked: `wsl --export` to a volume with spare
capacity (C: often cannot hold the tar), `--unregister`, `--import --version
2`, restore `DefaultUid`, **delete the tar** after a successful boot. Leaving
the tar fills the export volume. Do not compact from inside the live distro
that hosts this agent.

Heavy prune/GC/compact/recipe runs are MCP tasks: poll `tasks/get` until
`status=completed` (ignore a premature `resultType=complete` while
`status=working`).
