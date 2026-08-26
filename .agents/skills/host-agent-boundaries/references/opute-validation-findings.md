# Opute-side validation findings

Relay ownership, registry endpoint, context-size provider operations, and
inspector payload budget rules are in `SKILL.md` and
[dated-boundaries.md](dated-boundaries.md). Literal public chat evidence
remains the completion gate (`production-completion-evidence`).

- Host-native Kubernetes plans must write temporary manifests in the same
  target environment as the subsequent `kubectl` call.
- `k3s-host` can still have a managed guest runtime; carry `metadata.vmName`
  when present.
- Chat inspector deltas are delta-only after the initial snapshot.
- Disposable rollout images must match the K3s registry configuration.
- Operator readiness (active resources) is stronger evidence than retained
  Kubernetes event history.
