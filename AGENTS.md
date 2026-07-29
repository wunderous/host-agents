# Agent guide — Opute host agent (Go)

Canonical Go host agent for [Opute](https://github.com/opute-io/opute). Module: `github.com/wunderous/host-agents`.

## Quick links

| Topic | Location |
|-------|----------|
| Build, CI, Phase 1/3 validation | [README.md](README.md) |
| **DDNS vs Cloudflare Tunnel** (when to use which; `blog.opute.io`; conflicts) | [docs/ddns-vs-cloudflare-tunnel.md](docs/ddns-vs-cloudflare-tunnel.md) |
| Exposure tunnel ops (Go) | `internal/ops/exposure_tunnel.go`, `internal/ops/exposure_tunnel_windows.go` |
| Tool schemas / catalog | `schemas/all-tools.json`, `internal/tools/catalog.go` |
| Opute monorepo host exposure + MCP plugins | `../opute/AGENTS.md` (Host public exposure) |

## Host-agent-exclusive domain setup (do not regress)

**Product contract:** All operations to publish a platform (or any service) at a customer domain — K3s bootstrap, Traefik ingress, in-cluster **`install_cloudflared_connector`**, image deploy, pod recycle, host exposure — must run **only through this host agent's MCP tools**. Opute dogfood (`platform.opute.io` / `mcp.opute.io`) must use the same path so product gaps surface before customers do. Thin Bun scripts in the Opute monorepo are MCP clients, not alternate control planes.

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

Verify a `reverse tunnel connected` log line. When WSL **`/etc/hosts`** maps **`mcp.opute.io` → the dogfood K3s VM**, use **`http://mcp.opute.io/mcp`** and **`ws://mcp.opute.io`** in **`~/.config/opute/host-agent.env`** (`configure_platform_agent`); **`https://`** hits Traefik’s default cert and **`register_host_agent` fails**. For remote hosts without hairpin, keep **`wss://mcp.opute.io`**. From WSL, check **`http://mcp.opute.io/health`** → **`agents.active >= 1`**; from Windows, **`https://mcp.opute.io/health`** for public edge. **`opute-host-agent-tunnel-watchdog.timer`** should remain active. If the agent logs `Unauthorized agent tool 'host_agent_heartbeat'`,
treat it as an onboarding-token mismatch: `MCP_AUTH_TOKEN` must be the per-host
`opha_*` token, not the CPC bearer (see `../opute/AGENTS.md`, **Host Agent Registration And Heartbeat**).

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
- PostgreSQL validation is not complete until the deployment has a ready replica and `run_sql` successfully performs a table create/write/read cycle.
- Quick Tunnel startup must return a public URL only after Cloudflared has published it. The tunnel process must be detached from the MCP request and stopped through `delete_cloudflare_tunnel`.
- On WSL, validate that the selected `localTarget` is reachable from the Windows Cloudflared process; WSL/Windows localhost forwarding can fail independently of Cloudflare connectivity. Do not call the public tunnel lifecycle green when only Cloudflare reports `connected`.

## Release and validation

After Go, schema, or host-tool changes:

1. Run Linux-gated Go tests **inside WSL with a native Linux `go`** (user-local tarball under `~/.local/go-toolchain` is fine). **Anti-pattern:** relying on Windows `go.exe` leaked onto the WSL PATH via `/mnt/c/Program Files/Go/bin` — that builds `GOOS=windows` and causes `test/standalone` to skip (`runtime.GOOS != "linux"`) or fail. Prefer `go test ./internal/config ./test/contract ./test/standalone -count=1` in WSL over Windows cross-compile + Python HTTP smoke.
2. From the sibling Opute checkout, run `bun run build:host-agent` and export schemas when catalog changes are involved. Keep Accept/header parity with Opute via `OPUTE_REQUIRE_HOST_AGENT_SCHEMA=true bun test mcps/packages/shared/src/streamable-http-client.test.ts`.
3. Restart the owning WSL services only through the documented user-systemd path; do not start a second Windows binary for the same host identity.
4. Verify `opute-host-agent.service` is active, the reverse tunnel is connected, `http://127.0.0.1:9191/health` responds, and the Opute shell canary succeeds with an explicit host and VM fixture.

For standalone changes, additionally run the opt-in Go live lifecycle (`go test -tags=integration ./test/live` in WSL with Incus). Use disposable names such as `opute-standalone-e2e-*` / `go-live-*`; clean those resources through standalone MCP tools and verify `incus list` contains no matching instances. The npm launcher is validated with `npm test` (daemon ownership / foreign-port refuse). The published-package canary is opt-in via `npm run test:published-canary` (`RUN_PUBLISHED_NPM_CANARY=true`, Linux only). Preserve the production VM and platform-shaped services. A partial VM/K3s/DB/tunnel run is evidence for the first successful boundary only, not a green full-lifecycle result.

The optional production-shaped local CPC companion is `opute-platform-opute-stack.service` on the 919x ports (separate from the Opute dev stack on 909x). On this workstation that unit is **retired/masked** and public CPC is the dogfood K3s cell — diagnose failed heartbeat/tunnel against `mcp.opute.io` and the host-agent session before changing provider or VM code.

## Session learnings

- **Incus 7.2 container/GPU bootstrap is a distinct gate.** On Ubuntu 26.04, install Incus 7.2 from the signed Zabbly stable repository, then ensure the `default` storage pool, `incusbr0` bridge, and default-profile `root` disk exist before launching any container. Incus 7.2 rejects `--type=container`; containers are the default launch type. Host-agent mutations must check both process errors and non-zero provider exit codes, and OCI Docker remotes must be registered non-interactively with `--accept-certificate`. For the no-QEMU profile, probe Incus 7.2 plus `/dev/dxg`, WSL CUDA libraries, and `nvidia-smi`, while reporting QEMU separately and not requiring it. A configured Incus GPU device is not proof of guest GPU inference: probe **Ollama** (`probe_local_llm`, `gemma4:e2b`) before declaring the GPU path green.

- **K3s service restart durability (do not regress).** Kubernetes Deployments and the `cloudflared` Deployment are durable only after the backing Incus VM and K3s service return. **Proper pattern:** `provision_vm` persists `boot.autostart=true`; `InstallK3s` and `InstallCloudflaredConnector` repair that setting on existing VMs through the shared `ensureIncusVMAutostart` helper in `internal/ops/incus_launch.go`. After a host restart, verify the full chain through host-agent MCP: VM running → K3s `enabled`/`Ready` → `edge-system/cloudflared` Deployment `ready` → public `platform.opute.io` and `mcp.opute.io` HTTP 200. **Anti-pattern:** treating `boot.autostart` alone as proof of WSL/Incus/K3s/tunnel readiness, or adding a custom watcher before the declarative lifecycle is verified. **Regression:** `internal/ops/incus_launch_test.go` plus the production-shaped restart canary.
- **Rebuilt Go binaries require per-process rollout.** Every long-lived host-agent process loads the binary only at startup. After `bun run build:host-agent`, install the Linux artifact and restart `opute-host-agent.service`; if the standalone recovery MCP on `:3014` is running, stop only its recorded PID and restart it with `scripts/start-standalone-build-mcp.sh`. **Anti-pattern:** verifying the artifact hash on disk while an older standalone or platform process remains in memory.
- **`list_vms` / `get_vm_info` / cluster discovery on tunnel (do not regress).** Dogfood executes inventory on the Go agent — not the Bun Incus MCP. **`buildVMInfoFromIncusListItem`** (`internal/ops/incus_inventory.go`) must unmarshal Incus **`config` / `expanded_config` / `devices` / `expanded_devices`** and fill **`CPUs` / `Memory` / `Disk` / `Release`**, **`K3sInstalled`** (`user.opute.k3s_installed` label + guest probe on slow path), and **`AgentReady`** only when probed (**omit on `fast: true`** via `*bool`). **`normalizeClusterIpv4`** mirrors TS preference sort. **`InstallK3s`** must **`incus config set`** the k3s label on success. **`list_clusters` / `get_cluster_details` / `get_cluster_runtime_details`** live in **`internal/ops/cluster_discovery.go`** with **`dispatch.go`** cases. **`list_vm_network_devices`** belongs in **`IncusOmittedToolNames`** (static Incus only). **Anti-pattern:** status/IPv4-only rows; explicit **`agentReady: false`** on fast list; schema tools in tunnel catalog without dispatch. **Regression:** `internal/ops/incus_inventory_test.go`, `test/contract/dispatch_coverage_test.go`. After changes: rebuild Linux binary → install → restart **`opute-host-agent.service`** → **`bun run validate:list-vms-resources`**.
- **Reconcile tracked stale relay listeners by port.** `remove_local_llm_relay` is session-scoped, but persisted legacy sessions may retain a requested port after a publication is removed. `ensure_local_llm_relay` should reclaim a tracked stale listener for the desired port before binding, while still failing for an unrelated external process.
- **Standalone vs platform LLM relays (do not regress).** `ensure_local_llm_relay` on **standalone** MCP (`:3014`) binds listeners in the standalone process; **platform-mode** `opute-host-agent.service` owns persisted relays under **`~/.config/opute/local-llm-relays`** and the Incus bridge listeners K3s pods use (`10.0.100.1:11437` / `:11435`). **Symptom:** `opute-cell-secrets` + web rollout look correct but relay returns **401** and chat **`reachable: false`**. **Fix:** after `agents.active >= 1`, call **`host__ensure_local_llm_relay`** with **`relayToken` = `remoteAgentAuthToken`** (same value as **`chatLlmApiKey`**). **Anti-pattern:** creating relays only via standalone `:3014` while platform agent still serves stale tokens on the bridge IP.
- **LLM relay must flush streaming chat (do not regress).** `httputil.ReverseProxy` defaults buffer upstream chunks; Ollama NDJSON `/api/chat?stream=true` then hangs until completion and Bun/ai-sdk-ollama reports **`socket connection was closed unexpectedly`** while `/api/tags` still looks healthy. **Proper pattern:** `proxy.FlushInterval = -1` in `internal/ops/local_llm_relay.go`; after Go changes rebuild/install/restart **`opute-host-agent.service`** so restore recreates the listener. **`ensure_local_llm_relay` must recreate** when **`AllowedSourceCIDRs`** change (stale listener without pod-net CIDR still returns 401 to K3s). **Regression:** `TestLocalLLMRelayFlushesStreamingChatChunks`.
- **Provisioning storage is a guest-visible invariant.** A successful `provision_vm` result is not enough: validate the requested root size through the generic VM execution path (`lsblk`/`df`) before installing K3s. Profile-provided 10GiB roots can otherwise cause immediate K3s `DiskPressure`; resize/reconcile the Incus device inside the host-agent operation, never with direct host commands.
- **Guest bootstrap bridge endpoints must be routable.** WSL loopback `:9193` is not reachable from Incus guests. Cluster-agent installation must accept a guest-reachable bridge URL supplied by the control plane and then verify a fresh cluster-agent heartbeat.
- **Ollama systemd unit must pin the discrete CUDA GPU.** `RenderOllamaSystemdUnit` sets `CUDA_VISIBLE_DEVICES=0`, `NVIDIA_VISIBLE_DEVICES=0`, `OLLAMA_INTEL_GPU=false`, and `LD_LIBRARY_PATH` including `/usr/lib/wsl/lib` plus the tree’s `lib/ollama/cuda_v12` (Windows NVIDIA driver via WSL GPU-PV). `install_local_llm_model` / `start_local_llm_runtime` rewrite that unit every start. **Model tags for low-VRAM GPUs:** `install_local_llm_model` accepts `createAs`/`numGpu`/`numCtx` (Modelfile `PARAMETER num_gpu` / `num_ctx`); `configure_local_llm_model` creates a derived tag without re-pull. Prefer `numGpu=99` for full dGPU offload when hybrid splits crash (`GGML_SCHED_MAX_SPLIT_INPUTS`). **Diagnostics only for host GPU prep:** `check_local_llm_prerequisites` returns `readyForInstall`, `readyForGpuInference`, `blockers`, and `remediationHints` (driver / Eco-MUX / missing libcuda / low VRAM) — never installs NVIDIA drivers or changes laptop GPU modes. Proof after operator prep: `probe_local_llm` `gpuAccelerated` / `sizeVramBytes > 0`; warm-load via `modelRef` surfaces `loadError` + remediation. **Anti-pattern:** shipping a unit with only `OLLAMA_HOST`/`OLLAMA_MODELS` and declaring GPU ready because `libcuda.so` exists while `size_vram` stays `0`; leaving bare registry tags that hybrid-split on ≤6GiB VRAM. Regression: `internal/ops/ollama_test.go` (`TestRenderOllamaSystemdUnit`, `TestFinalizeLocalLLMPrerequisitesGpuBlockers`, Modelfile/remediation tests).
- **Ollama install on WSL (do not regress).** `ensureOllamaInstalled` downloads `ollama.tar.zst` — extraction requires **`zstd` or `unzstd`** (`ensureZstdForOllamaExtract` via `apt-get install -y zstd` when missing). **Symptom:** `extract Ollama archive failed` / `unzstd: Cannot exec`. **`startOllama`:** if `/api/tags` already responds on the configured port, **skip** `systemctl --user enable --now` (rewriting the unit while `opute-ollama.service` is healthy used to fail **`Ollama systemd operation failed`** and block `install_local_llm_model` for already-pulled tags like **`ibm/granite4.1:3b`**). After a fresh start, **`waitForOllamaReady`** polls `/api/tags` before `pullOllamaModel`. **`requireGpuInferenceReady`:** idle Ollama (`/api/ps` empty) must **not** block install — only loaded CPU models (`RuntimeLoadedModel` set with `size_vram=0`). **Anti-pattern:** treating idle `size_vram=0` as proof of CPU-only inference before the first model pull; using default MCP request timeout for `install_local_llm_model` (poll `get_operation` instead); fixing only the platform-mode binary while standalone `:3014` still runs the old `startOllama`. **Regression:** `internal/ops/ollama_test.go` (`TestOllamaReachable`, `TestValidateOllamaModelRef` includes `ibm/granite4.1:3b`).
- **WSL-native Ollama for dogfood chat (do not regress).** Qwen, Gemma, Granite (`ibm/granite4.1:3b`), and Phi catalog variants run through **Ollama** in WSL user systemd on loopback (`127.0.0.1:11434`) — **not** inside Incus OCI guests — so WSL GPU-PV applies. K3s pods reach Ollama through platform-mode **`ensure_local_llm_relay`** on the Incus bridge IP (`10.0.100.1:11435`). Gemma **`gemma4:e2b`** and Phi **`phi4-mini`** are Ollama registry tags. **LiteRT LM ops are retired** (`internal/ops/litert_lm.go` removed) — do not reintroduce `litert-lm` runtime paths in schemas or dispatch.
- **Standalone catalog contract (do not regress).** Tools that ship in `schemas/standalone-tools.json` must appear in **`StandaloneToolNames`** / mutation allowlists in `internal/tools/standalone.go` — not only `schemas/all-tools.json`. **`prepare_host_agent_artifacts`** and **`render_helm_template`** are standalone entries; **`render_helm_template`** is read-only (not in `standaloneMutationToolNames`). **Symptom:** `tools/call` unknown tool while the name exists in `all-tools.json`. **Regression:** `test/contract/dispatch_coverage_test.go`, `test/standalone`.
