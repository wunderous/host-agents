# Declarative plan execution in the Opute host agent (`host-plan.v1`)

## Context

Bringing up `platform.opute.io` / `mcp.opute.io` currently means running a large family of
bespoke TypeScript scripts against the host agent — 131 of the 300 files in
`../opute/scripts` match `platform|dogfood|public|host-agent`. Each one re-implements the
same shape by hand: call a host-agent MCP tool, poll something to decide whether it worked,
and (sometimes) bounce a pod or restart a VM when it didn't.

`../opute/scripts/dogfood-public-edge-remediate.ts` is the clearest evidence — it already
hand-rolls the abstraction we want, as a hardcoded Go-less DAG:

```ts
export type EdgeRemediationStep =
  | { tool: 'get_vm_info'; args: { vmName: string; fast?: boolean } }
  | { tool: 'restart_cluster'; args: { vmName: string } }
  | ...
export function planEdgeRemediation(params): EdgeRemediationStep[]
```

The step list is data, but the sequencing, validation, and recovery around it are not — they
are re-coded per script. The knowledge of *how the stack comes up* is smeared across ~131
files with no shared retry, readiness, or rollback semantics.

**Outcome:** the host agent gains one generic capability — execute a caller-supplied,
versioned, declarative graph of MCP tool calls, where each node carries its action, its
validation, and its recovery/reversion. The bring-up knowledge moves out of imperative
scripts and into plan *documents* that live in the calling repo.

### Boundary decision (load-bearing)

`AGENTS.md` states the host agent "must not know that Opute Platform exists" and that
"product intent, durable orchestration, routing, target selection, and business-level
recovery" belong to the caller — while also blessing "versioned, declarative, idempotent
desired/observed contracts with generations, operation IDs, bounded retries, cancellation,
redacted diagnostics, and truthful partial/failure status" and naming "operation recovery"
a valid generic capability.

Both hold if the host agent ships **the executor and nothing else**. No plan is compiled
into the binary. No Opute hostname, port, VM name, or workflow name appears in
`internal/plan/`. Every plan document is supplied by the caller and lives in `../opute`.

## Design

### Tool surface (3 new generic tools)

| Tool | Kind | Purpose |
|---|---|---|
| `validate_host_plan` | read-only | Static validation only: schema, unique ids, resolvable `dependsOn`, cycle detection, known tool names, resolvable variable references, size caps. No side effects. |
| `run_host_plan` | mutation, task-aware | Execute the plan. Returns a `taskId` immediately; poll `get_operation`. |
| `get_host_plan_run` | read-only | Per-node run state projection for a run id (richer than the flat task record). |

### `host-plan.v1` document

```jsonc
{
  "contractVersion": "host-plan.v1",
  "planId": "platform-public-edge",
  "generation": 3,
  "idempotencyKey": "...",
  "variables": { "cellVm": "opute-cell-1", "edgeNamespace": "edge-system" },
  "defaults": { "timeoutMs": 600000, "retry": { "maxAttempts": 3, "backoffMs": 5000, "backoffFactor": 2 } },
  "converge": { "maxPasses": 3, "abortOnExhaustion": true, "maxConcurrency": 1 },
  "nodes": [
    {
      "id": "cell-running",
      "dependsOn": [],
      "when":     [ { "path": "/mode", "op": "eq", "value": "full" } ],
      "action":   { "tool": "start_vm", "args": { "vmName": "${vars.cellVm}" } },
      "validate": { "tool": "get_vm_info", "args": { "vmName": "${vars.cellVm}", "fast": true },
                    "assert": [ { "path": "/status", "op": "eq", "value": "Running" } ],
                    "pollIntervalMs": 5000, "timeoutMs": 180000 },
      "recover":    { "tool": "restart_vm", "args": { "vmName": "${vars.cellVm}" }, "maxAttempts": 2 },
      "compensate": { "tool": "stop_vm",    "args": { "vmName": "${vars.cellVm}" } }
    },
    {
      "id": "recycle-unhealthy-origin",
      "dependsOn": ["cell-running"],
      "forEach": { "source": "${nodes.list-origin-pods.output}", "path": "/pods", "as": "pod",
                   "filter": [ { "path": "/ready", "op": "eq", "value": false } ] },
      "action": { "tool": "delete_k8s_resource",
                  "args": { "vmName": "${vars.cellVm}", "kind": "Pod",
                            "resourceName": "${item.pod.name}", "namespace": "${vars.edgeNamespace}" } }
    }
  ]
}
```

**Node fields.** `action` (optional — a node may be pure assertion), `validate`
(tool + `assert` + poll/timeout), `recover` (forward-fix, then revalidate), `compensate`
(reverse action, abort-only), `when` (guard; false ⇒ `skipped`), `retry`, `timeoutMs`,
`continueOnFailure`, `forEach` (fan-out over a list in an upstream node's output).

**Assertions — structured JSON matcher, no new dependency.** `path` is an RFC 6901 JSON
Pointer into the tool's `structuredContent`; `op` ∈ `eq ne gt gte lt lte exists notExists
contains matches in empty notEmpty all any`. `matches` uses Go RE2 (linear time, no
catastrophic backtracking). `all`/`any` take a nested assertion list applied per element.
An `expr` field is reserved in the schema but unimplemented, so plans stay
forward-compatible if a CEL evaluator is ever added.

**Interpolation.** `${vars.X}`, `${nodes.<id>.output.<json-pointer>}`, and `${item.<as>...}`
inside `forEach`. Resolved at node start; unresolved references fail closed at
`validate_host_plan` time, not mid-run.

### Execution semantics — converge, recover, compensate-on-abort

1. **Static validate** (same code path as `validate_host_plan`). Cycle detection via Kahn's
   algorithm. Fail closed on anything unresolved.
2. **Topological levels.** Nodes within a level run sequentially by default
   (`maxConcurrency: 1`); concurrency is opt-in because the host `resource.Coordinator`
   already serializes heavy tools across co-resident agents.
3. **Per node:** evaluate `when` → `skipped`. Else evaluate `validate` *first* — if the
   assertions already pass, the node is `satisfied` and `action` never runs. This is what
   makes re-running a plan cheap and safe, and is the idempotency story every side-effecting
   node needs. Otherwise run `action`, then poll `validate` to timeout. On failure run
   `recover` and revalidate; then retry with exponential backoff.
4. **Converge (level-triggered).** After a full pass, re-evaluate every node's `validate`.
   If anything regressed, run another pass, up to `converge.maxPasses`. Converged when a
   pass changes nothing and all validations hold. This is deliberately level-triggered
   rather than strict forward-only saga: a flaky readiness probe must not tear down a
   healthy stack.
5. **Abort** (cancel, or passes exhausted with `abortOnExhaustion`): run `compensate` for
   every node that reached `applied`, in **reverse topological order**, under a *fresh*
   bounded context — the run's context is already cancelled, so compensation must not
   inherit it. Compensation failures are recorded as `compensation_failed` and never mask
   the original error.
6. **Result.** Typed `planRun`: per node `status` ∈ `pending skipped satisfied applied
   failed compensated compensation_failed`, attempts, durations, and on failure the
   *observed vs expected* value of the assertion that failed — the diagnostic the current
   scripts mostly don't produce.

### Safety

- The runner dispatches **only** through `hostmcp.Server.DispatchTool`, so admission
  (`internal/resource`) and the host lock are enforced exactly as for a direct MCP call.
  No new execution path, no shell escape beyond what the called tools already permit.
- `run_host_plan` (and `validate_host_plan`) reject plans containing a `run_host_plan` node
  — no recursion.
- Caps: max nodes, max fan-out width, max serialized document size, max total wall clock.
- Args and node outputs are redacted through the existing `redactTaskValue`
  (`internal/hostmcp/server.go`) before entering any result or log.

## Files

**Host agent — new package `internal/plan/`** (pure, no MCP or ops imports; takes a
`Dispatcher func(ctx, name, args, onData) (*mcp.CallToolResult, error)` so it is testable
with a fake):

- `schema.go` — `host-plan.v1` types, decode, static validation
- `assert.go` — JSON Pointer resolution + matcher operators
- `interpolate.go` — `${vars…}` / `${nodes…}` / `${item…}` resolution
- `graph.go` — topological levels, cycle detection, reverse order for compensation
- `runner.go` — converge loop, retry/backoff, recover, compensation
- `*_test.go` — table-driven, fake dispatcher

**Host agent — wiring:**

- `internal/hostmcp/plan_run.go` *(new)* — the three tool handlers plus the in-process run
  registry. These are handled in `handleToolCall` **before** generic dispatch, following the
  existing `stream_vm_console` precedent, because the runner needs the `Server`-level
  dispatcher (for admission) that `tools.DispatchTool` does not expose.
- `internal/hostmcp/server.go` — route the three names; reuse `createAsyncTask` /
  `redactTaskArgs` unchanged.
- `internal/tasks/registry.go` — add `run_host_plan` to `TaskAwareTools`.
- `internal/tools/catalog.go` — add the three `ToolDefinition`s to
  `appendGenericHostDefinitions` (keeps the Go catalog independent of the TS schema export).
- `internal/tools/standalone.go` — `StandaloneToolNames` for all three;
  `IsStandaloneMutation` for `run_host_plan` only.
- `internal/state/store.go` — reuse as-is; a plan run is an operation, so restart correctly
  reconciles it to `unknown` per the standalone convention.
- `test/contract/dispatch_coverage_test.go` — add the three names to `hostMCPBypassTools`
  (they intentionally have no `dispatch.go` case) and assert catalog + standalone parity.
- `schemas/standalone-tools.json` — add to `tools` and `smoke.requiredTools`. **Note:** this
  file is exported from the monorepo (`cd ../opute && bun scripts/export-host-agent-schemas.ts
  ../opute-host-agent/schemas`), so the source of truth must be updated there too.
- `AGENTS.md` — short subsection under *Generic execution guidelines* recording the
  executor/plan-document boundary and the no-embedded-playbooks rule.

**Opute repo — the one migrated plan:**

- `plans/platform-opute-public-edge.plan.json` *(new)* — the public-edge bring-up/remediation
  currently spread across `scripts/ensure-platform-opute-stack.ts` and
  `scripts/dogfood-public-edge-remediate.ts`, expressed declaratively.
- `scripts/lib/run-host-plan.ts` *(new)* — thin client: load the document, call
  `run_host_plan`, poll, render the per-node table. Reuses `connectHostAgentMcp`,
  `callHostAgentTool`, `waitHostAgentOperation` from `scripts/lib/host-agent-mcp.ts` —
  no new MCP client.
- `scripts/dogfood-public-edge-remediate.ts` — driver switches to the plan; the pure
  selectors (`selectOriginPodsForRemediate`, `isCpcOriginAppPod`, …) and their tests are
  **kept**, since dynamic pod selection is what `forEach` + `filter` expresses. `planEdgeRemediation`'s
  hardcoded step array is what the plan document replaces.

## Verification

1. `go test ./...` — unit + contract. Runner tests must cover: cycle rejection; a second
   run of the same plan reporting every node `satisfied` with zero `action` dispatches
   (idempotency); retry backoff bounds; `recover` followed by successful revalidation;
   compensation firing in reverse topological order; cancel mid-run triggering compensation
   under a fresh context; `forEach` fan-out with `filter`.
2. `make standalone-http-smoke` — the three tools appear in the standalone catalog, and
   `run_host_plan` is refused unless `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`.
3. `validate_host_plan` against the real `platform-opute-public-edge.plan.json` via the
   standalone agent (`http://127.0.0.1:3014/mcp`) — read-only, safe to run any time.
4. **Before any live `run_host_plan`:** `AGENTS.md` requires checking the shared runtime
   lease from the Opute checkout — `bun run dev:stack:status`. Per memory, the stack must be
   started with `OPUTE_PUBLIC_EDGE_TARGET` or the Origin allowlist drops `platform.opute.io`
   and Stack Health reads all-Offline on a healthy stack.
5. Live proof: run the migrated plan end-to-end and compare its outcome against the existing
   `bun scripts/dogfood-public-edge-remediate.ts` path; then re-run it and confirm every node
   reports `satisfied` (no side effects on an already-healthy stack).

## Out of scope

Migrating the remaining ~130 scripts; a CEL evaluator; cross-host plans; persisting plan
documents in the host agent for re-run by id.
