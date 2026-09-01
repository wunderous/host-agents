# ADR 0004: Universal typed capability edges

Status: Accepted

## Decision

Every reusable Host Agent entity identity uses the canonical tenant-scoped
resource URI contract from ADR 0003. The resource kind is part of the typed
contract. The initial registry covers hosts, VMs, system containers, pods,
clusters, databases, services, host services, storage, runtimes, models,
tunnels, registries, domains, plans, operations, and the other provider-neutral
kinds declared by `internal/resourceid`.

Capability descriptors declare resource inputs with `Requires` bindings and
resource outputs with `Produces` bindings. An edge exists only when a producer
output path and consumer input argument declare the same resource kind. The
edge set, input edges, output edges, and `argumentProducers` are generated from
the immutable catalog snapshot; provider metadata cannot name a producer or
consumer tool.

The Go implementation is unaware of external clients. It owns kind validation, tenant
and URI parsing, resolver admission, descriptor validation, catalog revisions,
and dynamic overlay rebuilding. External clients consume the derived catalog and
preserves the selected URI as opaque data. It never parses a URI prefix or
maintains a second producer table.

Dynamic Cordis capabilities may declare an existing resource kind without
knowing which other capability produces or consumes it. Registering or
removing such a capability publishes a new catalog revision and recomputes the
compatible edges. Unknown kinds, malformed output paths, mismatched kinds,
foreign tenants, and stale revisions fail closed.

## Contract rules

- A generic string or a field named `uri` is not a resource binding by itself.
- Built-in schemas must declare typed `requires` and `produces` metadata.
- Provider and plugin descriptors must use only neutral bindings; tool-name
  maps and operation-name inference are forbidden.
- A capability may declare multiple compatible kinds for one argument when its
  implementation genuinely accepts them, such as VM and system-container
  identities. Admission resolves one matching typed binding before dispatch.
- Resource IDs remain opaque after parsing. Pod IDs include a host-issued
  Kubernetes identity coordinate and are never reconstructed by clients.
- All accepted kinds are registered in one host-owned resource-kind registry;
  catalog registration rejects a kind that the URI resolver cannot validate.

## Evidence and verification

The contract is verified at multiple boundaries:

- static catalog tests derive edges for host, VM, container, pod, cluster,
  database, and service kinds;
- dynamic registry tests prove producer/consumer independence, revision bumps,
  stale-revision rejection, and legacy producer metadata removal;
- MCP Streamable HTTP tests prove discovery, `tools/list`, typed `tools/call`,
  structured output, compatibility text, and complete terminal streams;
- Interactive-client tests prove opaque URI propagation, provenance, incompatible-action
  suppression, and catalog-revision handling;
- `/chat` evidence must include the literal request, parsed model tool call,
  exact arguments, paired result, complete SSE terminal event, and no SSE
  errors when the model boundary is exercised;
- mutating scenarios must prove external state and reverse-order cleanup.
