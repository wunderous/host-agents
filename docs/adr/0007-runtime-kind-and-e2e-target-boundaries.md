# ADR-0007: Runtime-kind admission and agentic E2E target boundaries

Status: Accepted

## Decision

Lifecycle callers must treat the canonical resource kind and the selected
execution layer as part of the typed contract. An Incus `vm:<tenant>:<id>` and
`container:<tenant>:<id>` are distinct resources; a caller must not pass one as
the other or infer support from an operation name alone.

`provision_vm` may create either an Incus system container or a QEMU virtual
machine, depending on the explicit instance type and the owning runtime
defaults. The canonical default is a system container with 2 vCPU and 2 GiB;
QEMU is an explicit `create_vm`/`instanceType=virtual-machine` choice. The
returned URI, kind, and observed instance type are the authority for the next
lifecycle step. An explicit operation target must agree with that observation.
A generic Host Agent operation that supports both kinds may omit the target so
its owning implementation resolves the observed type; an operation that
supports only one kind must reject the other before guest execution.

This Go Host Agent checkout owns generic Incus provisioning and exposes the
neutral Kubernetes provider boundary; it does not own a provider-specific
installer operation. K3s lifecycle work must therefore enter through the
typed Kubernetes capability (`opute.capability.kubernetes.provision`) and its
active provider generation, followed by the separate cluster-agent bootstrap
when the managed-cluster contract requires it. Provider MCP code owns concrete
K3s/container setup; the Host Agent owns admission, target URI resolution, and
generation-safe dispatch. Supporting a new kind in a layer requires changing
its typed descriptor/binding, admission, implementation, and boundary tests
together; an E2E-only argument rewrite or URI alias is forbidden.

## E2E target selection and cleanup

Before a destructive agentic E2E, preflight the target's runtime capability
and executor contract. On this WSL2/Hyper-V/Incus host, nested KVM is enabled
and healthy, but QEMU guest memory above about 2815MiB fails with
`KVM: entry failed, hardware error 0xffffffff`; a 2 vCPU / 2 GiB VM boots.
This is an infrastructure precondition failure, not evidence that K3s or
PostgreSQL provisioning is broken. K3s automation therefore uses a system
container by default.

If a fresh target is created and a later lifecycle step fails, the test must
delete that exact target through the product path where possible, verify it is
absent from inventory, and remove any temporary agent configuration or
projection. Do not create a second cluster-agent identity or re-point a
production cluster agent merely to make a localhost scenario appear fresh.
Reuse an existing managed cluster only when the local/prod authority, identity,
credential, and cleanup boundaries are explicit and independently verified.

## Consequences

- Resource-kind mismatches fail closed before provider execution.
- E2E harnesses record the actual executor, canonical URI, target kind, and
  preflight result rather than collapsing VM, container, and provider failures.
- The same capability name may exist at different MCP layers only when the
  layer-specific contract is discoverable and tested; an operation name is not
  proof of implementation ownership.
- A future container-to-K3s or VM-to-K3s contract change is a typed capability
  change with catalog/revision and boundary evidence, not a prompt or harness
  workaround.

## Evidence

The 2026-08-25 local probe confirmed WSL2 nested KVM (`/dev/kvm`, `kvm_amd`,
AMD-V) and found a local Incus/QEMU-on-Hyper-V boundary: 1 vCPU / 3 GiB
failed with `KVM: entry failed, hardware error 0xffffffff`, 1 vCPU / 2815MiB
worked, and 2 vCPU / 2 GiB worked. A separate container passed to a
VM-targeted K3s call was rejected at the typed boundary. All disposable
smoke resources and the temporary second agent projection were removed.
The evidence is retained as a failure-classification lesson; it is not a
green K3s/PostgreSQL lifecycle result.

The owning implementation and regression seams are:

- `internal/ops/incus_launch.go` for instance-type normalization and K3s
  target resolution;
- `internal/ops/kubernetes_generic.go` for the generic Kubernetes execution
  contract and provider-owned readiness handoff;
- `internal/hostmcp/server.go` for canonical resource admission; and
- the neutral Kubernetes provider contract and its provider-generation
  boundary tests.
