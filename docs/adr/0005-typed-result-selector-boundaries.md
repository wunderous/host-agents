# ADR 0005: Typed result selector boundaries

Status: Accepted

## Decision

Capability descriptors may declare reusable result types and pure selectors.
Selectors identify values by schema paths and cardinality; they are not tools,
provider callbacks, Cordis decisions, MCP metadata emitted by a provider, or
model-facing arguments. The Host Agent validates selector paths against the
declared output schema and evaluates them only during host-side produced
resource admission.

The descriptor remains the authoritative contract. A selector-aware descriptor
changes the immutable Host Agent catalog revision, preserves generation
affinity for existing calls, and causes stale interactive surfaces to fail
closed. Typed `Requires` and `Produces` remain the only capability-edge
authority; a selector refines a produced value and does not create a new edge.

Concrete providers may declare neutral result metadata in their operation
manifest. They do not import the selector evaluator, Host Agent internals, TUI
code, or Cordis code, and they never execute selector logic.

## Projection boundary

The interactive TUI projection receives the full result type and selector
metadata from live MCP capability descriptors. The shared Opute chat catalog
receives only sanitized typed bindings/edges plus an opaque Host Agent catalog
revision. Selector IDs, paths, cardinality, provider identity,
implementation identity, generation identity, and credentials are excluded
from model tool definitions, model projections, tool observations, prompts,
tool `_meta` forwarded to the model, and public context snapshots/deltas.

Agentic chat therefore stays ordinary typed execution: a model may call
`list_vms`, observe its sanitized structured result, and call `get_vm_info`
with a returned URI. There is no model `extract_result` tool or selector
continuation hidden in the chat kernel.

## Implementation ownership

- `contracts/capability` owns the neutral result type and selector contract.
- `internal/selectors` owns schema validation and pure evaluation.
- `internal/catalog` owns admission validation and immutable revision checks.
- `internal/hostmcp` owns produced-resource admission using the existing
  executor path.
- Concrete providers own declarations only.
- Opute owns the interactive selector overlay and chat/inspector projections;
  it does not duplicate Host Agent admission or resource identity parsing.

## Verification

Contract tests cover selector schema/cardinality validation, selector-only
revision changes, stale resolution rejection, provider-boundary validation,
TUI item-to-selector-to-follow-up interaction, repeated `^k` cycling, model
schema redaction, selector-free retrieval fingerprints, bounded inspector
snapshots, and tool-output `_meta` stripping. Release validation still
requires Go tests/vet, live MCP initialize/discover/list/call evidence, real
PTY key events, and a complete parsed `/chat` SSE trace with no selector or
provider metadata in model context.
