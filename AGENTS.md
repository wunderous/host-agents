# Agent guide — Opute host agent (Go)

## Database provider boundary

The v1 default platform database is the existing CloudNativePG/PostgreSQL
lifecycle, which is also retained for user-managed PostgreSQL databases. The
host agent additionally retains a separate, explicit TiDB Operator lifecycle
for deployments that opt in with a TiDB/MySQL URL. That optional path owns
HelmChart/CRD ordering, `TidbCluster` readiness, persistent storage,
MySQL-protocol SQL readiness, and operator-owned consumer Secrets. Never
silently switch providers, translate SQL between them, or return credentials
in tool results.

Canonical Go host agent for [Opute](https://github.com/opute-io/opute). Module: `github.com/wunderous/host-agents`.

## Multi-agent runtime and branch coordination

This Go repository shares WSL listeners, host-agent services, Incus capacity, llama-server, and public dogfood infrastructure with the Opute control-plane repository. Separate worktrees do not isolate those runtime resources. Never start a second dev stack or second host-agent listener merely because the source checkout is different.

Before changing shared runtime state, inspect the Opute coordination lease from the Opute checkout with `bun run dev:stack:status`. If another LLM agent owns the dev/runtime lease, wait or use read-only checks; do not kill processes, take ports, restart systemd services, provision competing guests, or roll public services. Public and production-shaped host-agent mutations are serialized across repositories and branches.

Production image builds and Kubernetes applies are additionally serialized by the sibling Opute checkout’s shared `production-rollout` operation lease. Before any production-shaped build/roll, inspect `bun run production:roll:status`; invoke the Opute Bun entry point with `--reason="agent:<agent-id>:<task>"` (or `OPUTE_PRODUCTION_ROLLOUT_REASON`) and wait on a competing owner. The lease covers the host-agent MCP `build_and_push_oci_image` / `apply_manifest` sequence and post-roll checks; it is separate from the Go host agent’s local heavy-work admission lock. Do not bypass it with a direct production roll or delete the lease file manually. Set `OPUTE_OPERATION_COORDINATION_DIR` to the same translated Windows/WSL directory used by the Opute checkout when both environments participate.

Lease fairness is required: continue source work, tests, and read-only diagnostics while polling the coordination status every 30–60 seconds for at most 30 minutes. If the owner has not released the lease by then, report the blocker instead of taking it or waiting forever. An agent that owns a runtime lease must release it within 15 minutes and immediately after its runtime-dependent validation; split longer host-agent or infrastructure validation into separate windows.

## Generic serving and exposure contract

The host agent is product-neutral. It accepts explicit serving assignments with
target identity, runtime, artifact, named endpoints, readiness checks, and
exposure metadata. It rejects ambiguous targets and VM targets where the
assignment contract disallows them. Product-specific commands, environment,
domains, and deployment policy belong to the calling service and are passed as
data to generic host-agent tools. Host connectors accept caller-supplied
hostnames and loopback targets after generic safety validation; Kubernetes
connectors remain a separate in-cluster capability.

**Session closure (VM + lease):** from the Opute checkout, delete Incus guests you provisioned (`delete_vm` cascade or documented prefix cleanup + `incus list`), then release shared coordination metadata: `bun run dev:stack:down --reason="<active up-reason from dev:stack:status> ending, because …"` and confirm **`Active lease: none`**. VM teardown does not clear `~/.config/opute/dev-stack-coordination/dev-stack-lease.json` — only a prefix-matched down does. See **Release lease metadata when VMs and runtime work are done** in `../opute/AGENTS.md`.

Feature work is not complete while it exists only on a branch or worktree. Before handoff, merge/rebase current `main`, resolve conflicts, rerun Go/control-plane validation, merge the feature branch into `main`, and verify the merged result. If merge authority is unavailable, report the blocker explicitly.

## Quick links

| Topic | Location |
|-------|----------|
| Build, CI, Phase 1/3 validation | [README.md](README.md) |
| **DDNS vs Cloudflare Tunnel** (when to use which; `blog.opute.io`; conflicts) | [docs/ddns-vs-cloudflare-tunnel.md](docs/ddns-vs-cloudflare-tunnel.md) |
| Exposure tunnel ops (Go) | `internal/ops/exposure_tunnel.go`, `internal/ops/exposure_tunnel_windows.go` |
| Tool schemas / catalog | `schemas/all-tools.json`, `internal/tools/catalog.go` |
| Opute monorepo host exposure + MCP plugins | `../opute/AGENTS.md` (Host public exposure) |
| Neutral container runtime/storage plan | `../opute/.agents/plans/2026-08-neutral-container-runtime-storage.md` |
| MCP v2 breaking change (2026-07-28) | `../opute/docs/mcp-v2-breaking-change.md` |

## Control-plane intent boundary

Structured intent extraction, capability retrieval, dependency closure, and model-facing recovery belong to the Opute
control plane. The host agent receives an explicitly authorized operation and executes it against the assigned host;
do not add LLM retrieval, live topology discovery, entity-ID guessing, or provider rediscovery to the host fast path.
Keep host IDs and provider state in the control-plane/tool-result contract, and preserve the existing host authorization,
heartbeat, tracing, and execution boundaries.

The control-plane inference engine is Cordis-core with AI SDK v7 as the
preferred runtime adapter; Ax is a migration-only legacy boundary and must not
be added here. Cordis/Opute owns plugin orchestration, context, authorization,
entity resolution, requirements, dependency closure, and evidence; the runtime
adapter only runs the adapted model-facing projection. The host agent is below
both layers and must receive an explicit resolved assignment. Do not add
Cordis, AI SDK, Ax, model discovery, ranking, entity resolution, or
business-level recovery here. Return typed, truthful observations so the
control plane can update its evidence ledger.

## Generic execution guidelines

The Host Agent must not know that Opute Platform exists. It is a reusable host
executor for any control plane or service. Keep product intent, durable
orchestration, routing, target selection, and business-level recovery in the
caller; keep this repository focused on executing explicit assignments and
returning observed state.

New capabilities should be generic and typed: target inspection, workload
reconciliation, named endpoint bindings, readiness probes, exposure/connector
reconciliation, secret-reference projection, and operation recovery are valid
examples. Opute-specific domains, ports, repository paths, workflow names,
LLM/tool retrieval, entity guessing, and provider rediscovery are not.

Require explicit target identity and fail closed on ambiguity or incompatible
instance types. Prefer versioned, declarative, idempotent desired/observed
contracts with generations, operation IDs, bounded retries, cancellation,
redacted diagnostics, and truthful partial/failure status. Preserve generic
MCP schemas, dispatch/catalog parity, authorization, heartbeat, tracing, and
standalone-mode coverage for every capability change.

Before adding a workaround, perform five-whys root-cause analysis and repair
the owning layer. Do not hide product gaps behind Opute-specific shell
commands or hard-coded local ports.

Application domain tables, Task Ledger state, entity search, embeddings,
transactional outboxes, and business-level leases belong to the calling
control plane, not this executor. The host agent may provision or reconcile a
generic database technology when explicitly assigned, but it must not define
the caller's schema, infer application entities, implement semantic search,
or become a second durable source of truth. Return typed observed state and
secret references only; never return credentials or silently translate
provider semantics.

## Host-agent-exclusive generic setup (do not regress)

**Product contract:** All operations to publish a service at a customer domain — K3s bootstrap, ingress, in-cluster **`install_cloudflared_connector`**, image deploy, pod recycle, host exposure — must run **only through this host agent's MCP tools**. The caller supplies service identity, endpoint bindings, domains, and target data. Thin orchestration clients are MCP clients, not alternate control planes; this repository must not encode a caller's product name or public domains.

**Proper pattern:** `install_cloudflared_connector` + `configure_service_domain` + `apply_manifest` / `put_k8s_secret` via standalone (`:3014`) or platform-mode agent; poll `get_operation` for async work.

**Anti-pattern:** Operator `kubectl`, `incus exec`, Cloudflare console/API mutations, or loopback `cloudflared` sidecars outside `install_cloudflared_connector` / `ensure_cloudflared_tunnel`. **Read-only** probes are fine; setup mutations are not.

## Host public exposure

The host agent runs **`cloudflared`** and local exposure probes on the **execution host** — the machine where `localTarget` (e.g. `http://127.0.0.1:80`) is reachable. Catalog-excluded tools: `ensure_cloudflared_tunnel`, `probe_host_exposure`, `remove_host_exposure`, `ensure_host_firewall_rule`.

**DNS modes:** Tunnel exposure uses **CNAME** to Cloudflare Tunnel, not dynamic DNS A records. Do not run a DDNS updater on the same hostname as an active tunnel binding. See **[docs/ddns-vs-cloudflare-tunnel.md](docs/ddns-vs-cloudflare-tunnel.md)** for use cases, the `blog.opute.io` release path, and conflict avoidance.

After Go or schema changes: `cd ../opute && bun run build:host-agent`, then restart dev stack (`dev:stack:down && dev:stack:up`).

## Production host on this Windows machine

The production agent runs inside the default WSL2 distro as the persistent user service
`opute-host-agent.service`; do not start the Windows binary for this deployment. Public
CPC is the dogfood **K3s** cell (`mcp.opute.io` / `platform.opute.io`). The optional
local WSL CPC (`opute-platform-opute-stack.service` on `:919x`) is **retired/masked**
on this workstation — do not start it to recover public edge.

```powershell
wsl -e bash -lc 'systemctl --user start opute-host-agent.service'
wsl -e bash -lc 'systemctl --user is-active opute-host-agent.service; journalctl --user -u opute-host-agent.service -n 20 --no-pager'
```

Verify a `reverse tunnel connected` log line and, after HWP R1 rollout, **`host worker connected`** (`internal/transport/host_worker.go` — **`RunHostWorkerLoop`** runs **in parallel** with **`RunReverseTunnelLoop`**; do not disable the legacy tunnel until R2). R1 dual-path closure is journal **`host worker connected`** + legacy tunnel — MCP Host **`/health` `agents.active`** is **deduped unique host count** (same host on legacy+HWP → **`active: 1`**, optional **`tunnelPaths`** for debug), not raw path cardinality. Endpoint and environment selection belongs to the caller and is supplied through the generic **`configure_agent_connection`** capability; the host agent must not infer product hostnames or control-plane routes. **MCP 2026-07-28 heartbeat client:** `register_host_agent` / `host_agent_heartbeat` HTTP posts must include **`MCP-Protocol-Version`**, **`Mcp-Method: tools/call`**, **`Mcp-Name`**, and **`params._meta`** from **`internal/mcphttp`** (parity with **`schemas/streamable-http-client.json`**). **Symptom:** logs **`Unsupported protocol version`** or **`missing … _meta`** / **`Mcp-Method header is absent`** while tunnel is connected; chat UI **`Agent Disconnected`**. **Regression:** `internal/heartbeat/service_protocol_test.go`. If the agent logs `Unauthorized agent tool 'host_agent_heartbeat'`,
treat it as an onboarding-token mismatch: `MCP_AUTH_TOKEN` must be the per-host
`opha_*` token, not the CPC bearer (see `../opute/AGENTS.md`, **Host Agent Registration And Heartbeat**).

**Unit topology on this workstation (2026-08-03, do not regress).** The platform agent is
the **plain unit** `opute-host-agent.service` (binary `~/.local/share/opute/opute-host-agent`).
The per-instance template units `opute-host-agent@host-zephyrus-8a224c89.service` and
`opute-host-agent@standalone.service` are **disabled/masked** — do not re-enable them; the
standalone instance unit auto-restart-loops on a missing instance binary
(`~/.local/share/opute/instances/standalone/opute-host-agent`), and stale instance envs
(`OPUTE_MCP_HEALTH_URL=http://10.0.100.129/health`) pointed at the retired VM octet. The
standalone build/roll agent runs via `bash scripts/start-standalone-build-mcp.sh`
(`:3014`, `OPUTE_STANDALONE_ALLOW_MUTATIONS=true`); it must expose `get_k8s_resource`
(unredacted) for secret-backed rolls — `run_kubectl` is **not** implemented by the Go agent
(control-plane aggregator name only). After `bun run build:host-agent`, install the Linux
artifact into `~/.local/share/opute/opute-host-agent` and restart
`opute-host-agent.service`; recycle the standalone with
`scripts/start-standalone-build-mcp.sh`.

An explicit `hostId` is the durable execution assignment. The host agent should execute that assignment through the reverse tunnel without requiring control-plane provider rediscovery. Keep guest and provider probes bounded and cancellable so VM provisioning cannot starve heartbeats or operation polling.

When a host is onboarded to a second control plane on the same machine, verify the
control-plane WebSocket bearer matches the MCP Deployment's `remoteAgentAuthToken`.
An onboarding session may report connected while `list_vms` remains unavailable if
the reverse tunnel reaches the MCP endpoint but is rejected at its auth boundary.
Keep concurrent local-LLM relay instances on distinct configured ports; the agent
must not assume that a single machine-wide default port is conflict-free.
Host-agent recovery is driven by the generated onboarding installer; callers must
stop the managed user service narrowly, not `pkill -f opute-host-agent`, because the
installer command itself contains that string and can kill its own shell before it
replaces the service environment.

### Incus/WSL recovery

- `incus list` can report a VM as `RUNNING` while QEMU is still booting or the Incus guest agent is unavailable. Preserve the VM and its disks: use the host-agent `restart_vm` operation (or a clean Incus stop/start), bounded-retry `incus exec <vm> -- true`, and only then probe K3s. Never delete/reprovision solely because an operation reports `VM agent isn't currently running`.
- During recovery, keep the host-agent reverse tunnel under the persistent user-systemd/WSL lifecycle and confirm public MCP (`mcp.opute.io`) separately from any optional local CPC. One-shot WSL invocations can race service and session startup; verify the agent heartbeat, public MCP health, guest `exec`, and K3s API separately before declaring the host recovered.

## Provider / catalog

- Provider abstraction: `internal/provider`
- Incus catalog: `schemas/incus-tools.json`; full export: `schemas/all-tools.json`
- Schema export from monorepo: `cd ../opute && bun scripts/export-host-agent-schemas.ts ../opute-host-agent/schemas`
- **`LoadAllToolDefinitions` reads `incus-tools.json` only (do not regress).** Tools that live only in `all-tools.json` (CPC moved some locals to vm-exec) are **not** registered for `tools/call` unless listed in **`CatalogExcludedToolNames`** in `internal/tools/catalog.go` (loaded via `LoadCatalogExcludedDispatchToolDefinitions`). **Symptom:** tunnel/`tools/call` returns unknown tool for `exec_command` while the name appears in `all-tools.json`. **Proper pattern:** add the local name to `CatalogExcludedToolNames`, rebuild/install the Linux binary, restart `opute-host-agent.service`. **Anti-pattern:** fixing only the Opute TypeScript catalog or assuming `all-tools.json` alone registers dispatch.

## Standalone and platform profiles

- Platform mode remains the default and owns registration, heartbeat, reverse-tunnel, and host-dispatch behavior. Preserve explicit `hostId` routing; do not rediscover providers in the execution fast path.
- Standalone mode is opt-in and must not require Opute Platform, Bridge, onboarding tokens, a reverse tunnel, or `OPUTE_MCP_URL`. Its local tool surface is implemented in `internal/tools/standalone.go`; invalid profile combinations must fail explicitly rather than silently falling back to platform mode.
- User-launched agents may configure settings through inherited environment variables, `--env-file PATH`, or repeatable `--env KEY=VALUE` flags. Precedence is CLI override, existing process environment, then env-file value. Keep secrets such as `CLOUDFLARE_API_TOKEN` in the VS Code `env` block or a permission-protected env file; do not put long-lived tokens in process arguments.
- Exposure operations run on the execution host where `localTarget` is reachable. Cloudflare tunnel tokens are sensitive and must not appear in logs, tool results, operation metadata, or metric labels.

### Standalone local workflow

- The supported client boundary is standards-compliant MCP Streamable HTTP only (default `http://127.0.0.1:3014/mcp`). **`stdio` is rejected** (`OPUTE_TRANSPORT` / `--transport` accept only `http`). Direct development invocation is `opute-host-agent --mode standalone --transport http`; the npm launcher (`@opute/host-agent`) daemon commands are `start` / `stop` / `status` / `url` over HTTP. Clients connect with `"type": "http"` + `url`. Cursor, VS Code, and Claude examples are unverified documentation snippets, not certifications.
- Standalone catalog + smoke lists are owned by **`schemas/standalone-tools.json`** (`tools`, `smoke.requiredTools`, `smoke.forbiddenTools`). Wire client headers live in **`schemas/streamable-http-client.json`**. Do not hardcode those lists in Go or Node tests.
- Mutations are deliberately disabled unless `OPUTE_STANDALONE_ALLOW_MUTATIONS=true` is set. Host shell and insecure-download behavior are separate opt-ins; never enable them by default in a published client configuration.
- Long-running mutations (`provision_vm`, `install_k3s`, `install_postgresql`, tunnel creation, and deletion) must return a task/operation immediately. Poll `get_operation`; do not increase the MCP request timeout or synchronously repeat the underlying provider call.
- The local journal is SQLite-backed and operation state is authoritative for standalone recovery. On restart, previously working operations reconcile to `unknown`; clients must surface that state rather than pretending the work completed.
- K3s readiness is asynchronous even after the installer exits successfully. Validate both the reported version and `readyNodes == totalNodes` before installing PostgreSQL.
- Standalone `install_postgresql` reconciles a CloudNativePG `Cluster`; CNPG generates the `-app` Secret and no password argument is accepted or logged. PostgreSQL validation is not complete until the Cluster is healthy and `run_sql` successfully performs a table create/write/read cycle through the primary pod.
- **Standalone CNPG install ordering (do not regress).** Apply the operator `HelmChart`, wait until `clusters.postgresql.cnpg.io` exists, then apply the tenant `Cluster`; applying both resources in one fresh-cluster manifest can fail before the CRD is installed. Regression: `internal/ops/standalone_postgres_test.go`.
- **Standalone environment isolation (do not regress).** Standalone validation rejects platform settings including `OPUTE_MCP_URL`, `OPUTE_MCP_HEALTH_URL`, and `MCP_AUTH_TOKEN`. For co-resident HTTP isolation tests, use separate dynamic ports for the MCP listener and an ambient-health trap, pin the trap through `AGENT_PORT`, and never inject forbidden platform variables. Regression: `test/standalone/isolation_test.go`.
- Quick Tunnel startup must return a public URL only after Cloudflared has published it. The tunnel process must be detached from the MCP request and stopped through `delete_cloudflare_tunnel`.
- On WSL, validate that the selected `localTarget` is reachable from the Windows Cloudflared process; WSL/Windows localhost forwarding can fail independently of Cloudflare connectivity. Do not call the public tunnel lifecycle green when only Cloudflare reports `connected`.

## Release and validation

After Go, schema, or host-tool changes:

1. Run Linux-gated Go tests **inside WSL with a native Linux `go`** (user-local tarball under `~/.local/go-toolchain` is fine). **Anti-pattern:** relying on Windows `go.exe` leaked onto the WSL PATH via `/mnt/c/Program Files/Go/bin` — that builds `GOOS=windows` and causes `test/standalone` to skip (`runtime.GOOS != "linux"`) or fail. Prefer `go test ./internal/config ./test/contract ./test/standalone -count=1` in WSL over Windows cross-compile + Python HTTP smoke.
2. From the sibling Opute checkout, run `bun run build:host-agent` and export schemas when catalog changes are involved. Keep Accept/header parity with Opute via `OPUTE_REQUIRE_HOST_AGENT_SCHEMA=true bun test mcps/packages/shared/src/streamable-http-client.test.ts`.
3. Restart the owning WSL services only through the documented user-systemd path; do not start a second Windows binary for the same host identity.
4. Verify `opute-host-agent.service` is active, the reverse tunnel is connected, `http://127.0.0.1:9191/health` responds, and the Opute shell canary succeeds with an explicit host and VM fixture.

For standalone changes, additionally run the opt-in Go live lifecycle (`go test -tags=integration ./test/live` in WSL with Incus). Use disposable names such as `opute-standalone-e2e-*` / `go-live-*`; clean those resources through standalone MCP tools and verify `incus list` contains no matching instances. **Agent-owned cleanup:** any session that provisions Incus guests (standalone MCP, platform lifecycle, pilot canaries) must **`delete_vm`** (or **`incus delete --force`** only after product-path delete fails) and confirm **`incus list`** before ending — do not leave **STOPPED** QEMU guests with prefixes like **`platform-opute-lifecycle-*`** or **`pilot-cancel-*`**. See **`opute/AGENTS.md`** **Agent-owned Incus VM cleanup**. The npm launcher is validated with `npm test` (daemon ownership / foreign-port refuse). The published-package canary is opt-in via `npm run test:published-canary` (`RUN_PUBLISHED_NPM_CANARY=true`, Linux only). Preserve the production VM and platform-shaped services. A partial VM/K3s/DB/tunnel run is evidence for the first successful boundary only, not a green full-lifecycle result.

The optional production-shaped local CPC companion is `opute-platform-opute-stack.service` on the 919x ports (separate from the Opute dev stack on 909x). On this workstation that unit is **retired/masked** and public CPC is the dogfood K3s cell — diagnose failed heartbeat/tunnel against `mcp.opute.io` and the host-agent session before changing provider or VM code.

## Session learnings

- **`update_vm_resources` tool (do not regress).** `UpdateVMResources` (`internal/ops/service.go`) applies `limits.cpu` / `limits.memory` to existing VMs and system containers via `incus config set` with ownership checks; at least one of `cpus`/`memory` is required. Registered in `schemas/incus-tools.json` (tunnel catalog), `schemas/all-tools.json`, `schemas/standalone-tools.json` (mutation), `StandaloneToolNames`, and the standalone mutation allowlist. Contract coverage: `test/contract/dispatch_coverage_test.go` (`TestVmResourceToolsHaveDispatchCoverage`), `internal/ops/update_vm_resources_test.go`. CPU and memory limits are live-applied by Incus for containers; QEMU guests pick them up on the next start. **UI + deployed validation:** the Infrastructure VM card **Resize…** dialog (opute monorepo) calls the tool through the approval-required bridge host op; the durable public canary is `bun run validate:public-vm-resources` (`PUBLIC_UPDATE_VM_RESOURCES_PASS`) and the deployed browser flow is validated against `https://platform.opute.io/vms` (see `opute/AGENTS.md` — deployed web UI validation). The tool's effective limits surface in public `host__list_vms`; a UI round-trip must end with the inventory reflecting the new limits.
- **Host Worker Protocol R1 (do not regress).** **`RunHostWorkerLoop`** connects to **`/host/v1/connect`** while **`RunReverseTunnelLoop`** keeps **`/mcp-agent`** alive — both remain live for rollback. Fresh HWP sessions are now the preferred path for sync reads and durable assignments; expired/missing sessions use the legacy tunnel. **Proper pattern:** rebuild/install platform-mode binary after **`host_worker.go`** changes; journal must show **`host worker connected`** without rapid register **1008** loops (MCP Host **`session-bridge`** fail-open when platform lacks **`upsert_host_agent_session`**). **Anti-pattern:** deleting the legacy tunnel or claiming R1 green without cross-pod assign/cancel and shell attach evidence. **Plan:** `../opute/.cursor/plans/host_worker_big_bang_f7ba8b26.plan.md`. **Regression:** `internal/transport/host_worker_test.go` (`TestBuildHostWorkerURL`).
- **HWP streaming attach is not implemented by the current Go agent (2026-08-02).** The transport currently handles `sync_call`, `assign`, and `assign_cancel`; `internal/console` is a stub and the Go catalog/dispatch have no PTY stream tool. Do not claim HWP shell attach from a one-shot `agent_shell` result or add partial stream plumbing. Define one host-owned PTY contract first, then implement and test `stream_open` acknowledgement, chunk/close/cancel, input/resize, and VM-scoped exclusivity through `hostmcp.Server.DispatchTool` before changing the R1 gate.
- **Incus 7.2 container/GPU bootstrap is a distinct gate.** On Ubuntu 26.04, install Incus 7.2 from the signed Zabbly stable repository, then ensure the `default` storage pool, `incusbr0` bridge, and default-profile `root` disk exist before launching any container. Incus 7.2 rejects `--type=container`; containers are the default launch type. Host-agent mutations must check both process errors and non-zero provider exit codes, and OCI registry remotes must be registered non-interactively with `--accept-certificate`. For the no-QEMU profile, probe Incus 7.2 plus `/dev/dxg`, WSL CUDA libraries, and `nvidia-smi`, while reporting QEMU separately and not requiring it. A configured Incus GPU device is not proof of guest GPU inference: probe **llama-server** (`probe_local_llm`, e.g. **`qwen3.5-0.8b-base-llama`**) before declaring the GPU path green.

- **K3s service restart durability (do not regress).** Kubernetes Deployments and the `cloudflared` Deployment are durable only after the backing Incus guest (VM or system container) and K3s service return. **Proper pattern:** `provision_vm` / container provision persists `boot.autostart=true`; `InstallK3s` and `InstallCloudflaredConnector` repair autostart on existing guests through **`ensureIncusVMAutostart`** (QEMU) or **`ensureIncusInstanceAutostart`** (containers) in **`internal/ops/incus_launch.go`** / **`incus_container.go`**. After a host restart, verify the full chain through host-agent MCP: guest running → K3s `enabled`/`Ready` → `edge-system/cloudflared` Deployment `ready` → public `platform.opute.io` and `mcp.opute.io` HTTP 200. **Anti-pattern:** treating `boot.autostart` alone as proof of WSL/Incus/K3s/tunnel readiness, or adding a custom watcher before the declarative lifecycle is verified. **Regression:** `internal/ops/incus_launch_test.go` plus the production-shaped restart canary.
- **Rebuilt Go binaries require per-process rollout.** Every long-lived host-agent process loads the binary only at startup. After `bun run build:host-agent`, install the Linux artifact and restart `opute-host-agent.service`; if the standalone recovery MCP on `:3014` is running, stop only its recorded PID and restart it with `scripts/start-standalone-build-mcp.sh`. **Anti-pattern:** verifying the artifact hash on disk while an older standalone or platform process remains in memory.
- **Install the fresh build artifact, not a stale dist name (2026-08-05, do not regress).** `bun run build:host-agent` writes **`dist/host-agent-linux-x64`** (current build), while `dist/opute-host-agent-linux-amd64` is a **stale legacy artifact (mtime months old)**. Copying the old name "works" (hash matches the stale file) but ships an older binary — symptom: a new tool registered in source is absent from the running standalone `tools/list` even after restart. **Proper pattern:** verify the **installed** binary hash equals **`dist/host-agent-linux-x64`** (e.g. `sha256sum /proc/<pid>/exe` vs `dist`), and confirm the process **start time is after the copy** (a process started before the copy still executes the old inode). **Anti-pattern:** trusting `dist` filenames, or restarting only one of the two agents (platform-mode + standalone) after an install.
- **`list_vms` / `get_vm_info` / cluster discovery on tunnel (do not regress).** Dogfood executes inventory on the Go agent — not the Bun Incus MCP. **`buildVMInfoFromIncusListItem`** (`internal/ops/incus_inventory.go`) must unmarshal Incus **`config` / `expanded_config` / `devices` / `expanded_devices`** and fill **`CPUs` / `Memory` / `Disk` / `Release`**, **`K3sInstalled`** (`user.opute.k3s_installed` label + guest probe on slow path), and **`AgentReady`** only when probed (**omit on `fast: true`** via `*bool`). **`normalizeClusterIpv4`** mirrors TS preference sort. **`InstallK3s`** must **`incus config set`** the k3s label on success. **`list_clusters` / `get_cluster_details` / `get_cluster_runtime_details`** live in **`internal/ops/cluster_discovery.go`** with **`dispatch.go`** cases. **`list_vm_network_devices`** belongs in **`IncusOmittedToolNames`** (static Incus only). **Chat catalog note:** **`list_clusters`** may **`tools/call`** on tunnel but still be absent from agent **`tools/list`** when omitted from **`incus-tools.json`** — dogfood MCP Host then synthesizes **`host__list_clusters`** via **`hostOwnedInventoryTools`** (`../opute/AGENTS.md` **Host-owned MCP inventory catalog**). **Anti-pattern:** status/IPv4-only rows; explicit **`agentReady: false`** on fast list; schema tools in tunnel catalog without dispatch. **Regression:** `internal/ops/incus_inventory_test.go`, `test/contract/dispatch_coverage_test.go`. After changes: rebuild Linux binary → install → restart **`opute-host-agent.service`** → **`bun run validate:list-vms-resources`**.
- **Reconcile tracked stale relay listeners by port.** `remove_local_llm_relay` is session-scoped, but persisted legacy sessions may retain a requested port after a publication is removed. `ensure_local_llm_relay` should reclaim a tracked stale listener for the desired port before binding, while still failing for an unrelated external process.
- **Standalone vs platform LLM relays (do not regress).** `ensure_local_llm_relay` on **standalone** MCP (`:3014`) binds listeners in the standalone process; **platform-mode** `opute-host-agent.service` owns persisted relays under **`~/.config/opute/local-llm-relays`** and the Incus bridge listeners K3s pods use (`10.0.100.1:11437` / `:11435`). **Symptom:** `opute-cell-secrets` + web rollout look correct but relay returns **401** and chat **`reachable: false`**. **Fix:** after `agents.active >= 1`, call **`host__ensure_local_llm_relay`** with **`relayToken` = `remoteAgentAuthToken`** (same value as **`chatLlmApiKey`**). **Anti-pattern:** creating relays only via standalone `:3014` while platform agent still serves stale tokens on the bridge IP.
- **LLM relay must flush streaming chat (do not regress).** `httputil.ReverseProxy` defaults buffer upstream chunks; llama-server SSE on its OpenAI-compatible `/v1` surface then hangs until completion. **Proper pattern:** `proxy.FlushInterval = -1` in `internal/ops/local_llm_relay.go`; after Go changes rebuild/install/restart **`opute-host-agent.service`** so restore recreates the listener. **`ensure_local_llm_relay` must recreate** when **`AllowedSourceCIDRs`** change (stale listener without pod-net CIDR still returns 401 to K3s). **Regression:** `TestLocalLLMRelayFlushesStreamingChatChunks`.
- **K3s in Incus system containers (do not regress).** The pinned K3s **v1.31.x** path must pass **`--kubelet-arg=feature-gates=KubeletInUserNamespace=true`** through **`INSTALL_K3S_EXEC`** before start; its newer config-file kubelet-arg form is not sufficient. Keep writing **`/etc/rancher/k3s/config.yaml`** for newer K3s releases and registry recycling, but treat the direct install argument as canonical for v1.31. On an existing container, return immediately only when **`get_k3s_status`** reports Ready; if an executable remains from a failed/unready partial install, remove that incomplete install and retry with the canonical arguments. Always wait for **`get_k3s_status` node Ready`**, not only `systemctl is-active k3s`. **Symptom:** bootstrap **`install_k3s` readiness timeout** / kubelet **`open /dev/kmsg: no such file or directory`** or **`overcommit_memory: permission denied`**. **Regression:** `internal/ops/k3s_container_config_test.go`, `internal/ops/incus_launch_test.go`.
- **Provisioning storage is a guest-visible invariant.** A successful `provision_vm` result is not enough: validate the requested root size through the generic VM execution path (`lsblk`/`df`) before installing K3s. Profile-provided 10GiB roots can otherwise cause immediate K3s `DiskPressure`; resize/reconcile the Incus device inside the host-agent operation, never with direct host commands.
- **`provision_vm` container-first provisioning (do not regress).** `ProvisionVM` routes to **`ProvisionContainer`** (nesting + **`/dev/kmsg`**) unless **`instanceType`** normalizes to **`virtual-machine`** (`normalizeProvisionInstanceType` in **`internal/ops/incus_launch.go`**). **`create_vm`** dispatch forces **`virtual-machine`**. **`InstallK3s`** / **`UninstallK3s`** with empty **`target`** call **`resolveInstallK3sTarget`** from **`incus info`**. Optional **`instanceType`** on **`provision_vm`** in **`schemas/incus-tools.json`**. **Anti-pattern:** using **`launchIncusVMViaAPI`** in automation that only needs K3s; omitting **`instanceType: virtual-machine`** on paths that genuinely require QEMU (GPU gateway). **Regression:** `internal/ops/incus_launch_test.go` (`TestNormalizeProvisionInstanceType`).
- **Guest bootstrap Platform endpoints must be routable.** WSL loopback `:9193` is not reachable from Incus guests. **Container CPC (`opute-clean-k3s`):** sibling QEMU guests must use the **container bridge IP** + Platform **`hostPort:9093`** (see **`resolveCpcContainerBridgeIPv4`** in **`internal/ops/cluster_agent_bridge_relay.go`**) — not the Incus gateway **`10.0.100.1`**. Set **`OPUTE_PLATFORM_GUEST_HOST`** / **`OPUTE_K3S_VM`** on platform-mode **`host-agent.env`**; patch **`OPUTE_PLATFORM_PUBLIC_URL`** on the Platform Deployment via **`../opute/scripts/patch-dogfood-bridge-public-url.ts`**. Cluster-agent installation must verify a fresh cluster-agent heartbeat after dispatch.
- **Llama-server runtime contract (do not regress).** `renderLlamaSystemdUnit` renders the managed `opute-llama-server.service` with the pinned CUDA build, `CUDA_VISIBLE_DEVICES=0`, the configured port, Qwen chat-template settings, context size, and GPU layer count. `install_local_llm_model` and `start_local_llm_runtime` are exclusive generation-model transitions: they stop the managed service before applying the next manifest, then probe the requested GGUF until it is GPU-resident; CPU fallback is rejected. Tool embeddings are separate and remain resident. Regression: `internal/ops/llama_server_test.go`.
- **Llama-server artifact contract (do not regress).** The host agent builds or adopts only verified Q4_K_M GGUF artifacts and the pinned CUDA llama.cpp binary under `~/.local/share/opute/llama-server`. Model identity, source revision, tokenizer revision, chat-template hash, binary hash, and artifact hash are persisted together in the serving manifest. Regression: `internal/ops/llama_server_test.go`, `internal/ops/llama_server_build_test.go`.
- **WSL-native Llama-server for dogfood chat (do not regress).** The supported catalog contains only Qwen3.5 0.8B base and tuned variants (`qwen3.5-0.8b-base-llama`, `qwen3.5-0.8b-opute-llama`). They run through the managed `opute-llama-server.service` on the configured WSL loopback port; K3s pods use the authenticated `ensure_local_llm_relay` OpenAI-compatible `/v1` endpoint. Context is profile-driven; never hardcode `num_ctx: 2048`.
- **Standalone catalog contract (do not regress).** Tools that ship in `schemas/standalone-tools.json` must appear in **`StandaloneToolNames`** / mutation allowlists in `internal/tools/standalone.go` — not only `schemas/all-tools.json`. **`prepare_host_agent_artifacts`** and **`render_helm_template`** are standalone entries; **`render_helm_template`** is read-only (not in `standaloneMutationToolNames`). **Symptom:** `tools/call` unknown tool while the name exists in `all-tools.json`. **Regression:** `test/contract/dispatch_coverage_test.go`, `test/standalone`.

## Host resource admission and WSL co-residency

- **One host-wide admission boundary.** MCP HTTP, task-aware calls, legacy reverse-tunnel calls, and HWP `sync_call` / `assign` must execute through `hostmcp.Server.DispatchTool`. Do not call `tools.DispatchTool` directly from a transport or task goroutine; that bypasses resource policy.
- **The coordinator is shared across instances.** Platform and standalone agents on one WSL machine must use the same `OPUTE_HOST_RESOURCE_LOCK_DIR` (default: `~/.config/opute/host-resource-coordinator`), not their instance-specific state directories. The Linux coordinator uses a kernel-backed lock so a crashed process releases capacity automatically.
- **Control traffic is never blocked by guest work.** Heartbeat, health, host info, inventory, list/get, and cancellation tools use the control class. Normal guest work is bounded; heavy work (`build_and_push_oci_image`, host-agent artifacts, Incus/K3s bootstrap, OCI builder setup, and local LLM installation) is serialized and rejected only under critical pressure.
- **Pressure is retryable, not terminal.** `host_resource_pressure` and `host_capacity_saturated` results include `retryAfterMs` and must remain pending in the TypeScript durable queue. Never convert temporary host pressure into a permanent failed operation or kill already-running work.
- **Telemetry is periodic.** Heartbeats publish memory, disk, pressure, and admission counts. Do not add an independent cleanup loop to the heartbeat. Windows `C:` monitoring and elevated VHDX compaction remain a separate Windows maintenance task; the Go agent must not compact VHDX or delete arbitrary caches.
- **Build paths must initialize caches.** Host build operations must create/validate `GOCACHE`, `GOMODCACHE`, OCI storage, staging, and output directories before work begins. A missing cache is an operation error; it is not a reason to retry indefinitely.
- **Neutral OCI runtime/storage contract.** `inspect_container_storage` is the read-only host-agent inspection path; `cleanup_container_storage` is the approved age-gated, dry-run-capable mutation for unused images and supported build cache. Both use the internal runtime adapter and expose only `auto` or `podman` today. Docker is a future adapter design point, not a detected, selectable, invoked, or catalog-advertised runtime. Preserve `configure_oci_storage` for compatibility and preserve Buildah/BuildKit only as legacy build-only values. Cleanup must never remove containers, volumes, networks, or running image references; Podman build-cache unavailability must be returned as an explicit warning. Automatic storage enforcement is build-triggered only, never heartbeat-triggered. **Tag hygiene (do not regress):** `build_and_push_oci_image` untags the local image after a successful push by default (`untagAfterPush: false` opts out); only the tag is removed, layers stay for the age-gated policy, digest-pinned refs are skipped with `untagSkippedReason`, and an untag failure is surfaced as `untagWarning`, never as a push failure. See `../opute/.agents/plans/2026-08-neutral-container-runtime-storage.md` and `internal/ops/container_runtime.go`.
- **Storage validation order.** Before a runtime-backed image build, inspect current storage through MCP, use a cleanup dry-run when reclaiming space, review the returned candidate scope, and only then run the approved cleanup/build operation. Do not invoke runtime CLI prune commands directly from scripts or operator shells.
- **Validation order:** focused Go tests → focused TypeScript tests/typecheck → Linux host-agent build → one serialized local lifecycle. Do not run parallel lifecycle/build/Incus checks on this WSL host. After a binary or transport change, rebuild the Linux artifact and restart each long-lived agent process that may still hold an older binary.

## Shared agent-work coordination ledger

Beads coordination is hosted inside native Windows on this workstation. Run
the `agent-work` status/start/update/end commands from a native Windows shell
using the authoritative Windows Beads/Dolt installation and workspace. When
running repository scripts from WSL, use native Linux Bun for code only; do
not expect or install a local Linux `bd`/`dolt` stack, and do not initialize
`.beads`.
A WSL session may use the translated Windows-backed directory only with an
explicitly reachable Dolt endpoint; otherwise report it as untracked. See
`../opute/docs/agent-work-coordination.md`.

The authoritative native-Windows pairing is Beads `v1.1.2` and Dolt `v2.2.3`.
Windows sessions use the external `%LOCALAPPDATA%\opute\agent-work-coordination`
directory and persistent user-scope `OPUTE_BD_PATH` / `OPUTE_DOLT_PATH`
overrides. Verify with `bd --version`, `dolt version`, and `bd dolt show`.
The configured Windows server is loopback-only at `127.0.0.1:3308`, so WSL
must not target Windows loopback or start a competing server for this database.
Cross-OS use requires one explicitly configured Dolt endpoint reachable from
both environments; otherwise run the adapter only from native Windows.

The host-agent checkout participates in the same advisory ledger as the sibling Opute checkout. At session start and after compaction, inspect it before editing:

```text
make agent-work ARGS="status"
make agent-work ARGS="start --title=... --touches=internal/ops,schemas --may-affect=... --next=..."
```

For milestone text containing spaces, use the quoting-preserving wrapper directly: `./scripts/agent-work start --title="..." --may-affect="..."`.

Use one record for the session's coherent task. Create it before editing, re-read status before expanding into a new API, shared file, or cross-repository surface, and update implementation status, next milestone, validation, and current head at API-contract, implementation, and validation boundaries.

For a milestone with spaces, prefer the wrapper:

```text
./scripts/agent-work update <id> --implementation-status="host contract changed" --next="run focused Go tests" --validation="not run" --comment="Heartbeat/API payload changed; control-plane work should re-read this."
```

Use `--related=<id>` for possible impact and `--blocks=<id>` only for real sequencing dependencies. Finish with a final validation comment and `./scripts/agent-work end <id> --validation="go test ./..." --reason="validated"`. The Make target and wrapper both delegate to the canonical `../opute/scripts/agent-work-coordination.ts` adapter while marking the repository as `opute-host-agent` and preserving this checkout's current Git head.

The adapter uses one external Beads v1.1.2 database in shared Dolt-server mode with `BD_DISABLE_METRICS=1`; no `.beads` directory is written to this checkout. Native Windows is the normal execution environment for the adapter and owns the authoritative workspace. A WSL client may set `OPUTE_AGENT_WORK_COORDINATION_DIR` to the translated Windows-backed directory only when an explicitly configured Dolt server is reachable from WSL; do not install a second Linux Beads/Dolt stack. Overlap and two-hour stale warnings are advisory and never lock or block edits. If coordination fails, report it and explicitly recognize the session as untracked before continuing. For a `3308` port conflict, inspect the listener from native Windows and use `bd dolt status`/`bd dolt show`; never kill or reclaim an unverified process. A verified Dolt listener is a retryable diagnostic, while a non-Dolt listener or cross-OS endpoint mismatch must be repaired before tracking resumes. The ledger is separate from runtime/service ownership and does not replace the host-agent or dev-stack coordination rules.

## Codebase Knowledge Graph (agent discovery — mandatory)

`codebase-memory-mcp` is the local, read-only discovery backend for this checkout and the sibling `../opute` checkout. It is agent tooling only; it is not part of the host agent's product MCP transport, so do not introduce stdio into Go MCP server/client code.

- **Always sync before discovery.** At session start, after compaction, and before expanding into a new package or transport, call the MCP `index_repository` tool with this repository's absolute path. The background watcher keeps the index fresh between calls, but an explicit `index_repository` call is the required synchronization boundary; do not rely on a stale graph. Use `index_status` when diagnosing freshness.
- **Use graph tools where applicable.** Prefer `search_graph` for Go symbols and structure, `trace_path` for callers/callees, `get_architecture` for package orientation, `detect_changes` for uncommitted blast radius, `query_graph` for bounded relationship questions, and `get_code_snippet`/`search_code` for targeted evidence. Always pass the explicit `project` returned by `list_projects` when querying.
- **Verify exact source.** The graph accelerates discovery but is not the source of truth. Read the current Go file, schema, and focused tests directly before editing or making a negative claim. Fall back to `rg`/`Glob` for literals, configuration, generated schemas, docs, ignored paths, and cases the graph reports as skipped or incomplete.
- **Trace cross-repository contracts.** Keep both projects indexed in the shared local cache. Before changing a host tool, MCP wire contract, schema, heartbeat, transport, or lifecycle boundary, query the sibling Opute project as well and record the affected control-plane surface in the coordination ledger.
- **Do not commit graph state.** Keep the persistent index in the local Codebase Memory cache; do not add `.codebase-memory` databases, generated graph artifacts, secrets, or credentials to either repository.
