# Generic Host-Agent Operations and Templated Recipes

## Summary

Add a provider-neutral `host.command` capability and Cordis service backed by the existing Host Agent command runner. Do not add WSL-specific capabilities.

Add `host-recipe.v1` for reusable and inline recipes with typed runtime inputs, CEL expressions, immutable hashes, policy enforcement, approvals, durable execution state, and redacted evidence.

CEL is suitable for bounded, side-effect-free expression evaluation. Use the current `cel.dev/cel-go` module with a pinned version. Recipe identity uses RFC 8785 canonical JSON before SHA-256 hashing.

## Invariant alignment

- C-01/C-02/C-11: shell execution and recipe semantics stay outside `internal/cordis`; Cordis only provides lifecycle, dependency, and disposal primitives.
- C-03: all recipes reuse `internal/plan.Runner`; no second workflow engine.
- C-04: template resolution occurs before dispatch; resolved arguments are passed unchanged. Policy rejects invalid requests but never rewrites or enriches them.
- C-05: command results remain observations. The Host Agent does not infer that a user’s broader intent was satisfied.
- C-06: no heuristic recovery or automatic interpreter fallback.
- C-07/C-22: Host Agent owns durable operation truth and task status; Opute stores recipe definitions and references to Host-Agent runs.
- C-08/C-21/C-23: every run binds to canonical Host Agent identity, typed resource URI, catalog revision, executor kind, and connection/generation session.
- C-09/C-24: mutating recipes declare compensation or are explicitly marked irreversible, with preflight, cleanup, and approval requirements.
- C-10: secret values never enter durable state, model projections, command text, or evidence.
- C-15/C-17/C-18/C-20: capability edges, lifecycle registration, activation, and real boundary evidence are explicit and testable.

## Host Agent changes

- Add a Host-Agent-owned `HostCommandService` outside `internal/cordis`.
- Support explicit shell kinds such as `posix`, `powershell`, and `cmd`; interpreter paths are host policy, not recipe input.
- Extend `run_host_command` compatibly with shell, working directory, environment, timeout, and output limits.
- Keep `agent_shell` internal-only.
- Require typed execution context: canonical Host Agent/resource URI, shell/executor kind, catalog revision, policy profile, and approval grant where required.
- Enforce bounded execution, cancellation, process-group cleanup, output truncation, no inherited secret environment, and no privileged escalation unless explicitly authorized.
- Use policy profiles for read-only allowlists and approval-required mutation mode. Reject unsupported or privileged operations.
- Register the service through Cordis with one owned disposer and no provider-generation coupling.
- Add `validate_host_recipe`, `run_host_recipe`, and `get_host_recipe_run`.

## Recipe contract and templating

Introduce `host-recipe.v1` with `recipeId`, `recipeVersion`, `template.language: cel.v1`, JSON-Schema input definitions, capability/policy requirements, an embedded `host-plan.v1`, output mapping, and compensation/approval declarations.

Use explicit tagged values:

```yaml
command:
  $template:
    language: cel.v1
    expression: "'wsl.exe -d ' + shell_quote('powershell', inputs.distro)"
```

The CEL environment contains only `inputs`, `nodes`, and `item`, plus pure formatting and shell-quoting functions. No filesystem, process, network, clock, randomness, or side-effect functions are available.

Validation must compile and type-check expressions, validate resolved values against tool schemas, reject unknown inputs/unsupported roots/excessive cost/nested recipes/catalog drift, enforce size/node/loop/timeout/output limits, preserve JSON types, and track secret taint.

Secret inputs may only enter approved ephemeral environment bindings. They cannot appear in command text, paths, durable metadata, model projections, or evidence. Resume requires re-supplying secret references.

Use RFC 8785 canonical JSON and SHA-256 for recipe hashes. Bind idempotency and approval to recipe hash, non-secret input fingerprint, Host Agent identity, resource URI, catalog revision, and generation/session.

## Registry and Opute Cordis integration

Add an Opute `InferenceRuntimeServices['host-agent']` service with typed `executeCapability`, `validateRecipe`, `runRecipe`, and `getRecipeRun` methods.

Add the corresponding Cordis plugin requiring host context, catalog registry, and evidence ledger. It must use the existing AI SDK v7 step-by-step runtime boundary and must not expose raw MCP clients to models.

Add model-facing capabilities: `host.command`, `host.recipe.validate`, `host.recipe.run`, and `host.recipe.status`.

Update shared schemas, catalog projection, retrieval documents, dynamic toolkit loading, MCP dispatch, service-key parity tests, capability edges, and model redaction tests.

Host selection must use the canonical Host Agent identity and typed resource binding. A model or recipe cannot substitute a display name or arbitrary host ID. Opute must resolve `OPUTE_REMOTE_AGENT_ID` through the existing host context.

Add tenant-scoped immutable recipe storage. Registry versions cannot be modified after publication. Registry and inline recipes use the same validation and execution path; inline recipes are content-addressed and retained only through run metadata.

Publication is immediate as a user-authored artifact, but execution still requires validation, policy, and approval.

## WSL recipes

Add examples/fixtures, not WSL-specific capabilities:

- `wsl-recovery-and-tuning.v1`: collect stats and swap usage, inspect configuration, back up and update configuration, shut down/restart the selected distribution, verify postflight state, and provide compensation for configuration changes.
- `wsl-storage-audit.v1`: perform read-only storage inventory and produce bounded findings.
- `wsl-storage-reclaim.v1`: remain separate and destructive, requiring audit output and explicit approval with dry-run and declared-path restrictions.

The recovery recipe must complete before storage reclamation is offered.

## Tests and acceptance

- Host Agent tests for shell selection, policy, approval binding, argument preservation, cancellation, cleanup, truncation, and compatibility.
- Cordis tests for service dependencies, duplicate registration, disposal, and provider-generation independence.
- Recipe tests for canonical hashing, typed CEL evaluation, schema validation, secret taint, compensation, idempotency, generation affinity, and resume behavior.
- Opute tests for service registration, canonical host selection, tenant isolation, projection redaction, and bridge mapping.
- Boundary E2E proving the actual Opute service → MCP transport → Host Agent → shell executor path, including catalog revision, run ownership, durable status, and redacted evidence.
- WSL acceptance using a disposable/test target with preflight, backup, restart, postflight, and cleanup evidence.
- Run Go tests, targeted Bun tests/typechecks, MCP wire tests, and invariant/decision verifiers.

## Assumptions

- Shell-only command payloads are retained, but all execution is typed, bounded, policy-controlled, and approval-gated where mutating.
- Users may author recipes immediately; they cannot bypass validation or approval.
- Registry and inline recipes are both supported.
- Existing `runtime-recipe.v1` remains serving-runtime-specific.
- Existing dirty work in both repositories is preserved.
