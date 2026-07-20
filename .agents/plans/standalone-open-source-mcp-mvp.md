# Standalone Open-Source MCP Server MVP Plan

**Status:** Public repository, v0.1.1 GitHub release, and `@opute/host-agent` npm publication are live; named-client evidence remains
**Last status audit:** 2026-07-19  
**Estimated completion of plan exit criteria:** ~80%  
**Repository (local checkout):** `opute-host-agent`  
**GitHub remote today:** public `wunderous/host-agents`; Go module and release URLs are aligned to `github.com/wunderous/host-agents`  
**Product name (working):** Opute Local Host Agent  
**Primary interface:** MCP over Streamable HTTP (default `http://127.0.0.1:3014/mcp`)
**Amended:** 2026-07-19 — stdio transport removed from the public standalone profile.
**MVP host:** Linux x86_64 and arm64, including execution inside WSL  
**Target clients:** Visual Studio Code, Claude Desktop, Cursor, and other standards-compliant Streamable HTTP MCP clients

**Transport amendment:** Earlier checklist or historical audit references to
standalone stdio are superseded. The supported standalone transport is
Streamable HTTP; CI, lifecycle validation, and the npm launcher must use the
HTTP listener and the `type: "http"` client shape.

## 1. Outcome

Publish the host agent as an independently installable, open-source MCP server that can inspect and manage a user's local Incus host without an Opute control plane, account, onboarding token, Bridge, reverse tunnel, or hosted service.

The MVP is complete when a new user can discover the public repository, install the server with one documented command, connect it to a mainstream MCP client, safely inspect the host by default, explicitly enable mutations, complete a VM lifecycle, and upgrade or uninstall it without Opute-specific infrastructure.

## 2. Current State

### 2.1 Committed foundation (usable internal preview)

- `--mode standalone --transport http` starts an MCP Streamable HTTP server on `/mcp`.
- Standalone mode has an intentionally reduced tool catalog (`internal/tools/standalone.go`) and excludes platform registration, heartbeat, and routing tools at the hostmcp layer.
- Mutations are disabled by default (`OPUTE_STANDALONE_ALLOW_MUTATIONS=true` required).
- Long-running standalone mutations return operations and persist state in SQLite.
- An `@opute/host-agent` npm launcher exists in-repo and downloads a version-matched release binary.
- Linux x86_64 and arm64 artifacts are built by GitHub Actions (`make artifacts`).
- The general Go suite passes (`go test ./...`).
- Standalone profile does not start heartbeat when platform URLs/tokens are absent (`internal/app`).

### 2.2 Working-tree progress (2026-07-19 audit; release candidate)

The following land (or partially land) the plan’s first slice and parts of M1/M3. Treat as **WIP until committed, reviewed, and released**:

| Item | Evidence |
|------|----------|
| Strict config validation | `Config.Validate()` rejects invalid mode/transport/provider and standalone + reverse-tunnel / platform credential combinations |
| Build-injected version | `internal/version` + Makefile `-ldflags`; `--version` and MCP server info use it; heartbeat uses `go-host-agent/` + same version |
| Catalog isolation test | `TestStandaloneServerDoesNotExposePlatformTools` (thin; see §9) |
| Idempotent SQLite close | `Server.Close()` + `TestStandaloneServerCloseIsIdempotent` |
| Linux-only launcher | Native Windows/macOS fail before download with WSL/Linux guidance |
| Release `SHA256SUMS` | CI + Publish workflows generate manifest; launcher requires checksum by default |
| OSS stubs | Untracked `LICENSE` (MIT), `SECURITY.md`, `CONTRIBUTING.md` |
| npm metadata | `repository` / `homepage` / `bugs` / `keywords` on package.json |
| Standalone contract and diagnostics | Versioned `schemas/standalone-tools.json`, contract validation, `--check`, and Linux-shaped Streamable HTTP smoke |
| Focused safety/lifecycle gates | Mutation-denial coverage, contract metadata, SQLite restart→`unknown`, idempotent close, launcher cache-integrity tests |
| Release workflow hardening | npm test/package gate, SBOM upload, artifact provenance attestation, optional public npm publish job |

### 2.3 Public distribution reality

| Surface | Status |
|---------|--------|
| GitHub repo visibility | **Public** (`wunderous/host-agents`) |
| npm `@opute/host-agent` | **Published** at `0.1.1` with `latest`; GitHub Actions performed the publish |
| Public unauthenticated release download | **Live at GitHub Release `v0.1.1`; checksum/HTTP/MCP canary passed** |
| Org/module alignment | **Resolved** to `wunderous/host-agents` and `@opute`; Go/npm ownership is aligned |
| Production WSL services | **Healthy** — platform companion and enabled `opute-host-agent.service` active; host `host-zephyrus-e5059700` connected |

The repository, tagged GitHub release, production host, and npm package are live. Named desktop-client execution remains unverified.

### 2.4 Milestone scorecard (audit)

| Milestone | Progress | Summary |
|-----------|----------|---------|
| **M0** Contract freeze | ~90% | ADR + versioned catalog/classifications + contract test; npm scope ownership remains |
| **M1** Strict isolation | ~90% | Validate + version + Close + exact contract metadata + mutation-denial + packaged-shaped HTTP smoke; public clean-machine/network-trap evidence remains |
| **M2** Tool/security contract | ~60% | Canonical artifact, stable/experimental classification, shell removed, destructive policy and redaction boundaries; generated reference docs and full threat-model review remain |
| **M3** Public distribution | ~95% | Public repo + aligned URLs + v0.1.1 checksums/SBOM/provenance/npm publication + hardened launcher + unauthenticated canaries; clean-machine evidence remains |
| **M4** Client onboarding | ~35% | VS Code, Claude, Cursor, and WSL-shaped snippets plus protocol-shaped client smoke; real-client matrix remains |
| **M5** Release gates | ~80% | Go/HTTP/npm/unit gates and disposable Incus create/list/inspect/delete gate are green; K3s/PostgreSQL/Cloudflare lifecycle gates remain |
| **M6** Publish & observe | ~45% | Public repo/release/npm and production host are live; named-client/clean-machine canaries remain |

## 3. Gap Assessment

Status legend: **Open** · **Partial** · **Done (WT)** · **Done**

### P0 — Release blockers

1. **The repository is not open-source ready.** **Partial**
   - Present (WT/untracked): MIT `LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`.
   - **Done in WT:** `CODE_OF_CONDUCT.md`, support policy, third-party notice stub, standalone-first README, and MIT license.
   - Still missing: third-party notice generation from a release dependency lock and npm scope authorization.
   - README still centers platform/dev-stack install paths more than independent OSS use.

2. **The advertised install path is incomplete.** **Partial**
   - GitHub binary and `npx -y @opute/host-agent@0.1.1` install paths are public and canary-verified; named-client execution remains.

3. **Artifact/launcher support is consistent.** **Done (WT)**
   - Launcher no longer advertises/downloads a Windows artifact, fails early with WSL guidance, and has a native-Windows test.

4. **Downloaded binaries are not verified securely by default.** **Partial**
   - **Done (WT):** `SHA256SUMS` on release artifacts; launcher requires manifest (or explicit `OPUTE_HOST_AGENT_SHA256` pin).
   - **Done in WT:** bounded launcher requests, maximum sizes, atomic cache writes, concurrent cache lock, binary re-verification, SBOM/provenance workflow steps.
   - **Done:** public `v0.1.1` release canary proves checksum, gzip, ELF, HTTP health, MCP initialize/tools/list, and auth rejection; npm package download/start/status/stop also passed from WSL.

5. **Runtime configuration fails open to defaults.** **Partial**
   - **Done (WT):** explicit `Validate()` for set-but-invalid mode/transport/provider and incompatible standalone/platform combinations.
   - **Done in WT:** `--check` validates startup and state access before protocol output; packaged invalid-config matrix E2E remains.
   - Unset vars defaulting to platform/http is intentional when not launching standalone — document the “explicit standalone env” contract rather than treating unset as a bug.

6. **Versioning is fragmented.** **Partial**
   - **Done (WT):** single injectable `version.Version` for CLI + MCP (+ heartbeat prefix).
   - **Done:** GitHub Actions verified package `0.1.1` against tag `v0.1.1`; the public npm package and release artifact share the version and passed the packaged HTTP gate.

### P1 — Product and compatibility gaps

1. **Standalone behavior lacks focused automated coverage.** **Partial**
   - Added: exact contract/isolation metadata, mutation denial for every mutating tool, Streamable HTTP framing, listener shutdown checks, Close idempotency, config validation, and operation persistence/restart → `unknown` coverage.
   - Still missing: shutdown/listener cleanup and an external network trap proving zero CPC contact.

2. **No packaged end-to-end release gate exists.** **Partial** — exact public npm package download/start/status/stop, public-release Streamable HTTP smoke, and disposable Incus lifecycle gate exist; named-client execution remains.

3. **No client compatibility matrix exists.** **Open**
   - Copy/paste snippets now cover VS Code, Claude Desktop, Cursor, and Windows+WSL; real client execution remains untested.

4. **The tool contract is not published as a standalone contract.** **Partial**
   - `schemas/standalone-tools.json` is versioned and drift-tested; generated reference docs remain.

5. **Safety controls are unclear or stale.** **Partial**
   - Host shell was removed from standalone configuration and contract; `agent_shell` remains platform-internal only.
   - No full threat-model document; the disposable local Incus create/list/inspect/delete lifecycle gate is now green.
   - K3s, PostgreSQL, `run_sql`, and Cloudflare tunnel tools are labeled experimental in the canonical contract.

6. **Operational lifecycle needs hardening.** **Partial**
   - **Done (WT):** application-owned `Close()` on shutdown.
   - **Done in WT:** restart→`unknown` automated proof and non-resume behavior documented in the ADR.
   - Still missing: schema migration policy, cache/state/log cleanup and backup expectations.

7. **Platform coupling remains in naming and development flow.** **Open**

### P2 — Post-MVP opportunities

- Native Windows providers and native macOS support.
- Additional VM/container providers beyond Incus.
- Remote Streamable HTTP deployment, OAuth, or multi-user service mode.
- A graphical installer or desktop application.
- Automatic recovery/resumption of interrupted mutations.
- Kubernetes distributions other than K3s and production-grade PostgreSQL operators.

These are explicitly outside the MVP unless required to satisfy a release-blocking compatibility issue.

## 4. MVP Product Decisions

1. **Linux is the supported execution environment.** Linux x86_64 and arm64 are first-class. Windows users run the Linux server in WSL; native Windows and macOS launch attempts fail early with a clear supported-platform message. Do not publish a native Windows artifact until a native provider is supported end to end.
2. **Streamable HTTP is the public MVP transport.** Platform reverse-tunnel remains available for Opute, but is not part of the standalone public support promise. stdio is not supported.
3. **Read-only is the secure default.** Mutations require an explicit opt-in. Shell access is either implemented behind its own tested opt-in or removed from standalone documentation/configuration for MVP.
4. **Incus is the only MVP provider.** Unknown providers are rejected; no silent normalization.
5. **The npm launcher is the recommended client install path.** Direct GitHub binary downloads remain supported and documented. Both resolve the same tagged version and verified artifact.
6. **Standalone and platform profiles share execution primitives, not lifecycle state.** Standalone must never call registration, heartbeat, Bridge, platform MCP, or reverse-tunnel code, and its SQLite state must never enter the Opute control-plane store.

**Decision recorded in ADR 0001:** VM inspection, prerequisites, and VM lifecycle are stable MVP; K3s / PostgreSQL / Cloudflare are experimental until gated.

## 5. Milestones

Per-milestone status reflects the 2026-07-19 audit. Deliverables and exit criteria remain the definition of done; annotations mark what is already satisfied vs remaining.

### M0 — Freeze the MVP contract

**Status:** Implemented in WT; public naming/ownership review remains

**Goal:** Establish the supported product boundary before changing release machinery.

Deliverables:

- Record the decisions in section 4 in a short ADR.
- Choose the public product/repository/package naming and document whether `@opute/host-agent` is final. **Done:** `@opute/host-agent` is the published name.
- Define the supported OS/architecture matrix: Linux x86_64, Linux arm64, and Windows clients invoking the Linux binary through WSL.
- Define the initial stable standalone tool catalog, separating read-only, mutating, destructive, credential-bearing, and long-running tools.
- Define semantic-versioning rules for tool schemas and operation-state migrations.
- Define MVP support expectations and clearly label K3s/PostgreSQL/Cloudflare capabilities as experimental if they will not be release-gated end to end.

Exit criteria:

- One reviewed ADR names the supported transport, provider, platforms, tool surface, security defaults, and non-goals.
- Every currently exposed standalone tool is intentionally classified and has an owner/test expectation.

E2E validation — **contract fixture gate**:

- Build the release candidate binary, launch it in standalone Streamable HTTP mode with all platform-related environment variables removed, and capture MCP `initialize` plus `tools/list` into a normalized, versioned fixture.
- Compare the observed tool names, schemas, mutation classifications, server identity, protocol version, and supported platform metadata with the M0 ADR and canonical catalog proposal.
- Invoke one representative read-only tool (`check_local_prerequisites`) and one mutation (`create_vm`) with mutations disabled; the read must return a structured result and the mutation must return the documented policy error without creating an Incus resource.
- Retain redacted request/response evidence and an `incus list` before/after inventory under the CI run artifacts.
- **Pass condition:** the runtime surface exactly matches the approved MVP contract, contains no platform-only tools, and performs no mutation during the contract probe.

### M1 — Make standalone a strict, isolated product profile

**Status:** Implemented in WT; clean-machine evidence remains

**Already satisfied (WT unless noted):** `Config.Validate()`; standalone rejects reverse tunnel + platform settings and non-HTTP transport; injectable `--version` / MCP version; SQLite `Close()` on shutdown; exact catalog/metadata and mutation-denial tests; packaged-shaped Streamable HTTP smoke with listener release.

**Still open:** schema migration behavior; guardrails that standalone never initializes heartbeat/registration/tunnel (runtime path exists; **network-trap test does not**); packaged invalid-config matrix; version agreement across npm/tag/artifact on a real public release.

**Goal:** Ensure standalone cannot accidentally depend on or fall back to Opute Platform behavior.

Deliverables:

- Replace silent normalization with explicit validation for mode, transport, provider, state directory, and incompatible flag combinations.
- Reject standalone + reverse tunnel, standalone + onboarding/platform credentials where ambiguous, platform + stdio (must fail closed), and unsupported native platforms with actionable errors.
- Add a startup/config diagnostic command or `--check` path that validates Incus, state directory permissions, and required binaries without mutating the host.
- Make server/build version metadata injectable from a single source and expose it through `--version` and MCP server info.
- Connect `OPUTE_STANDALONE_ALLOW_HOST_SHELL` to a deliberately scoped tool and policy, or remove it entirely from the standalone configuration contract.
- Close the standalone state store during graceful shutdown and define schema migration behavior.
- Add guardrail tests proving standalone startup never initializes heartbeat, registration, reverse tunnel, platform URLs, or platform credentials.

Exit criteria:

- Invalid and incompatible configurations fail before any listener, MCP protocol output, network connection, or host mutation.
- Standalone starts successfully with all Opute platform URLs/tokens unset.
- A test proves the standalone catalog contains no registration, heartbeat, dispatch, platform, or reverse-tunnel tool.
- `--version`, npm version, MCP server version, release tag, and artifact version agree.

E2E validation — **profile-isolation matrix**:

- Run the packaged binary, not `go run`, through a table of valid and invalid startup configurations in a disposable Linux runner.
- Valid case: start `--mode standalone --transport http` (default port **3014**) with a temporary state directory and no Opute URLs, onboarding tokens, Bridge tokens, CPC tokens, or reverse-tunnel settings; initialize MCP over Streamable HTTP and call `get_local_status`.
- Invalid cases: unknown mode, unknown transport (including `stdio`), unknown provider, standalone plus reverse tunnel, platform plus stdio, unwritable state directory, and native unsupported OS where a runner is available.
- Place a local network trap/fake CPC beside the process and assert the valid standalone run makes zero registration, heartbeat, health, WebSocket, or platform HTTP requests.
- Terminate the MCP client / SIGTERM the listener, verify the child exits cleanly, SQLite can be reopened without recovery errors, and no listener remains bound.
- **Pass condition:** the valid profile completes the read-only MCP flow entirely offline; every invalid profile exits nonzero with its documented diagnostic before emitting MCP protocol output or contacting the trap; all exposed version values match the release tag.

### M2 — Define and secure the standalone MCP contract

**Status:** Partial; contract/policy gates are implemented, generated docs and full threat-model evidence remain

**Goal:** Make the exposed tools predictable, safe, and independently versioned.

Deliverables:

- Create a canonical standalone catalog/schema artifact rather than assembling the public contract from a manual allowlist plus a platform-wide catalog at runtime.
- Generate tool reference documentation from the canonical schemas, including examples, mutation class, prerequisites, expected duration, operation polling, and error shapes.
- Validate every tool input and structured output at the MCP boundary; add drift tests between catalog, handler, and documentation.
- Standardize long-running results around one operation envelope and statuses (`working`, `completed`, `failed`, `cancelled`, `unknown`).
- Document restart semantics: in-flight work becomes `unknown` and is not reported as successful.
- Threat-model host command execution, arbitrary SQL, downloads, Cloudflare tokens, local tunnel targets, file permissions, logs, and MCP-client prompt injection.
- Add secret-redaction tests covering tool results, operation records, errors, and logs.
- Decide whether destructive tools need a second explicit opt-in beyond the general mutation flag; implement the decision consistently.

Exit criteria:

- Catalog/schema drift fails CI.
- All standalone tools have validated input/output contracts and generated reference documentation.
- Read-only default, mutation denial, destructive policy, target restrictions, and secret redaction have automated tests.
- No platform-only schema or tool can become public merely by being added to `all-tools.json`.

E2E validation — **standalone contract and safety journey**:

- Launch the packaged Streamable HTTP server against a disposable Incus project and enumerate `tools/list`; validate the live response against the published standalone catalog artifact.
- Exercise every read-only tool with valid inputs or a documented prerequisite-not-ready fixture and validate its structured output schema.
- With mutations disabled, call every tool classified as mutating or destructive and verify uniform denial with no inventory, namespace, tunnel-process, operation, or state changes.
- With mutations enabled, start one disposable VM operation, poll it through `get_operation`, cancel a second operation, restart the MCP server during a third operation, and verify the documented `completed`, `cancelled`, and `unknown` terminal/recovery behavior.
- Supply unique canary secrets through PostgreSQL and Cloudflare-shaped inputs using fake/local dependencies; scan HTTP responses, stderr, SQLite rows, operation results, and retained logs to prove the values are absent.
- Attempt disallowed tunnel targets, oversized SQL, invalid identifiers, malformed schemas, and platform-only tool calls.
- **Pass condition:** all live inputs/outputs conform to the canonical schemas, policy gates prevent side effects, operation transitions match the contract, secrets are absent from every evidence channel, and unknown/platform tools are rejected.

### M3 — Build a trustworthy public distribution

**Status:** Partial; GitHub/npm publication paths and v0.1.1 release canaries are green; named-client evidence remains

**Already satisfied (WT):** Linux-only launcher path; mandatory SHA256SUMS verification; OSS policy files; npm metadata; CI checksum/SBOM/provenance workflow; bounded cache-integrity launcher; npm tarball test.

**Still open:** transactional npm release rehearsal and named-client/clean-machine install canaries.

**Goal:** Provide a reproducible, verified installation from npm and GitHub Releases.

Deliverables:

- Add `LICENSE` (after owner/legal selection), `SECURITY.md`, `CONTRIBUTING.md`, `CODE_OF_CONDUCT.md`, support policy, and third-party license/notice generation.
- Rewrite the README around the independent standalone use case; move Opute Platform integration to a separate contributor/integration document.
- Make the GitHub repository public and align remote/module/repository metadata under the final organization. **Done:** public `wunderous/host-agents` and matching Go/npm metadata.
- Produce versioned Linux x86_64 and arm64 binaries, compressed artifacts, SHA-256 manifest, SBOMs, and GitHub build provenance/attestations.
- Make the npm launcher require verification against the release manifest/embedded per-platform checksums by default; remove the unsupported Windows download branch for MVP and document WSL invocation.
- Harden launcher downloads: bounded timeout, maximum response size, atomic cache writes, concurrent-launch locking, cache integrity re-check, proxy behavior, and signal forwarding.
- Complete npm metadata (`repository`, `homepage`, `bugs`, `keywords`, `funding` if applicable) and include license/readme files.
- Add an npm trusted-publishing/provenance workflow triggered by the same signed/tagged release as the binaries.
- Make release publication transactional: npm must never point to artifacts that do not exist, and a failed platform artifact must block release.

Exit criteria:

- A clean machine can install by npm and direct GitHub download without authentication.
- Every downloaded binary is verified by default before execution.
- `npm pack` contains only intended files and its launcher smoke test passes on supported architectures.
- The public repository has vulnerability reporting instructions and an explicit license.
- Release notes identify breaking tool/schema changes and supported platforms.

E2E validation — **public distribution rehearsal**:

- Publish a release candidate to an isolated GitHub prerelease and npm staging tag (for example `next`) using the same workflows and permissions intended for the final release.
- From clean Linux x86_64 and arm64 environments with no repository checkout or GitHub credentials, install via both `npx -y @opute/host-agent@<version>` and direct artifact download.
- For each path, verify checksum/provenance, run `--version`, initialize over Streamable HTTP, list tools, call `check_local_prerequisites`, and confirm the artifact, npm, MCP, and CLI versions match.
- Corrupt the cached binary and checksum manifest in separate runs; the launcher must refuse execution or redownload and reverify rather than run corrupted bytes.
- Simulate a missing architecture artifact, redirect loop, truncated download, concurrent first launch, network timeout, and termination signal; verify bounded failure, atomic cache behavior, and no orphan process.
- On native Windows and macOS, invoke the package and verify it fails before download with the documented Linux/WSL guidance rather than requesting a nonexistent artifact.
- Inspect the npm tarball, SBOM, attestation, checksum manifest, license files, and public unauthenticated download URLs as retained evidence.
- **Pass condition:** both supported architectures complete the packaged MCP smoke without credentials, verification is mandatory and fail-closed, unsupported systems fail clearly, and all release artifacts are mutually version-consistent.

### M4 — Prove client compatibility and user onboarding

**Status:** Partial; configuration snippets and WSL guidance are implemented, real-client matrix remains

**Goal:** Make setup repeatable for the named MCP clients.

Deliverables:

- Publish copy/paste configurations for VS Code, Claude Desktop, and Cursor using the recommended npm launcher.
- Provide separate Linux and Windows+WSL instructions, including how the client reaches the WSL command, where state is stored, and how environment variables are supplied securely.
- Document Incus installation/prerequisites, group/socket permissions, mutation opt-in, state reset, upgrade, uninstall, and troubleshooting.
- Add a safe first-run sequence: initialize → `check_local_prerequisites` → `get_local_status` → `list_vms`.
- Add client-specific smoke checks that capture initialize, tools/list, one read-only call, mutation-denied behavior, and operation polling.
- Verify protocol behavior against the current stable MCP specification supported by the Go SDK and publish the tested protocol/client versions.

Exit criteria:

- A fresh-user runbook has been completed on VS Code, Claude Desktop, and Cursor with evidence retained in release CI or a versioned compatibility record.
- All clients see the same canonical standalone tool catalog and structured outputs.
- Windows+WSL setup is validated as a real client path, not inferred from Linux-only testing.
- Common startup errors identify the failing prerequisite and remediation without exposing secrets.

E2E validation — **real-client compatibility matrix**:

- On native Linux, configure fresh profiles of VS Code, Claude Desktop, and Cursor from the published copy/paste instructions using the staged npm package.
- In each client, establish the MCP session, confirm the server reports healthy/connected, call `check_local_prerequisites`, `get_local_status`, and `list_vms`, then attempt a mutation with the default read-only policy and verify the denial is understandable to the user.
- Enable mutations explicitly, create a uniquely prefixed disposable VM, poll its operation to completion through the client, inspect it, delete it, and verify `incus list` contains no matching instance.
- Repeat the supported first-run and one disposable VM lifecycle from fresh Windows client profiles with the MCP command executing inside WSL; verify Windows-to-WSL quoting, environment propagation, state paths, process cleanup, and client restart/reconnect.
- For each client/platform pair, record client version, OS, MCP configuration with secrets removed, initialize protocol version, tool-count/catalog hash, operation IDs, cleanup inventory, screenshots or machine-readable logs, and first-failure timing.
- Run negative onboarding cases for missing Incus, insufficient socket permission, invalid state directory, and unsupported platform; verify each client surfaces the documented remediation rather than a generic disconnected state where the client permits server stderr display.
- **Pass condition:** every supported client completes the same standalone catalog and lifecycle flow, cleanup is empty, reconnect preserves completed operation history, and the compatibility record contains no credentials or host-identifying secrets.

### M5 — Add standalone release gates and complete an MVP canary

**Status:** Partial; Go, HTTP, npm, artifact, SBOM, provenance, and disposable Incus VM lifecycle gates are green; broader experimental lifecycles remain

**Goal:** Prevent the public standalone product from regressing behind platform-focused tests.

Deliverables:

- Add focused unit/integration tests for configuration validation, Streamable HTTP framing, catalog isolation, read-only defaults, async operations, cancellation, SQLite persistence/restart, graceful shutdown, and secret redaction.
- Add an npm launcher test suite using a local fake release server so redirects, checksums, cache corruption, concurrency, timeouts, and signal forwarding are deterministic.
- Add CI jobs for Linux x86_64 and arm64 build/test; run architecture-appropriate smoke tests (native or emulated where reliable).
- Add a packaged Streamable HTTP E2E that installs the npm tarball and performs MCP initialize/tools/list/tool-call against the downloaded release binary.
- Add a disposable Incus lifecycle gate on a self-hosted Linux runner: prerequisites, create/provision VM, poll operation, inspect/start/stop/restart, delete, and verify no matching resources remain.
- If K3s/PostgreSQL/Cloudflare remain in the MVP catalog, add their complete disposable lifecycle gates; otherwise mark them experimental and exclude them from the MVP release claim.
- Keep the existing platform-profile regression suite green to preserve Opute integration.

Exit criteria:

- Required public CI gates pass from a clean checkout and from the packaged artifacts, not only `go run` or a locally built binary.
- The full disposable VM lifecycle passes and cleanup proves no matching Incus instances remain.
- An interrupted operation is reported as `unknown` after restart and never as completed.
- `go test ./...`, MCP compliance tests, release install verification, npm package smoke, and platform-profile regression tests are green for the release commit.

E2E validation — **release-candidate lifecycle suite**:

- On an isolated self-hosted Linux runner with a dedicated Incus project, install the exact npm release-candidate tarball and allow mutations only for the test process.
- Execute the full MVP path solely through MCP Streamable HTTP: initialize, verify prerequisites, capture empty prefixed inventory, provision a VM, poll the operation, inspect it, start/stop/restart it, install K3s if included in the supported catalog, validate node readiness, install and round-trip PostgreSQL if included, create/probe/delete a Cloudflare Quick Tunnel if included, and cascade cleanup in reverse order.
- Inject process termination during a separate long-running operation, restart against the same SQLite state, and verify it becomes `unknown`; then clean its infrastructure using supported MCP tools.
- Run the packaged Streamable HTTP protocol/compliance suite and npm launcher failure suite against the same artifacts.
- Run the Opute platform-profile regression canary with explicit `hostId` routing to prove shared execution primitives still work without routing standalone state through the control plane.
- Always execute best-effort cleanup in teardown and retain before/after Incus, Kubernetes namespace, tunnel process, and SQLite operation inventories even on first failure.
- **Pass condition:** every capability claimed as MVP completes end to end, all operations reach documented states, standalone and platform profiles both pass their gates, and post-run inventories contain no test-prefixed VM, namespace, tunnel, child process, or active operation.

### M6 — Publish and observe the MVP

**Status:** Not started (0%)

**Goal:** Release publicly with a supportable feedback and rollback path.

Deliverables:

- Run a release candidate through all M5 gates and the three-client compatibility checklist.
- Publish the public repository, GitHub Release, checksums/SBOM/provenance, npm package, documentation, and changelog from one version tag.
- Verify installation from the public internet without repository credentials or developer-local overrides.
- Create issue templates for bugs, compatibility reports, provider requests, and security routing.
- Define a 30-day MVP observation checklist: install failures, checksum failures, unsupported-platform attempts, client compatibility issues, operation recovery defects, and security reports. Collection must be issue/support based unless users explicitly opt into telemetry.
- Document rollback/yank policy for a broken npm or binary release without silently replacing immutable artifacts.

Exit criteria:

- Two clean-environment canaries (one native Linux and one Windows client using WSL) complete the documented first-run flow from the public npm package.
- The release can be reproduced from its tag and its artifacts match the published checksum manifest.
- Public issue and private security-reporting paths are working.

E2E validation — **public launch canary and rollback drill**:

- After publication, provision two clean canary environments with no source checkout or release credentials: native Linux and a Windows desktop client invoking the server through WSL.
- Install from the public npm `latest` tag, verify the public checksum/provenance chain, connect from a named supported MCP client, run the safe first-read flow, enable mutations, complete a uniquely prefixed VM create/inspect/delete lifecycle, and prove empty cleanup.
- Independently download the public GitHub artifact and reproduce its checksum from the published manifest; rebuild from the public tag in a clean builder and compare the documented reproducibility outputs.
- Submit a non-sensitive test issue through the public bug template and validate the private security-reporting route without placing vulnerability details in a public issue.
- Rehearse the documented broken-release response using a disposable prerelease/staging npm tag: mark the release affected, prevent new recommended installs, restore the prior known-good version, and confirm existing immutable artifacts were not replaced.
- Repeat the read-only public install canary after rollback/restoration and record timings and artifact identifiers in the release evidence log.
- **Pass condition:** both public environments complete install and lifecycle without privileged repository access, public artifacts validate and reproduce as documented, support/security routes work, and the rollback procedure restores a known-good install without mutable artifact replacement.

## 6. Recommended Sequence and Dependencies

```text
M0 contract
  └─ M1 strict isolation
       ├─ M2 tool/security contract
       │    └─ M4 client onboarding
       └─ M3 public distribution
            └─ M4 client onboarding
                 └─ M5 release gates
                      └─ M6 public MVP
```

M2 and M3 can proceed in parallel after M1. M4 depends on both because client documentation must describe the final tool contract and install mechanism. M6 is blocked on all M5 gates.

**Practical next cut:** configure the authenticated `@opute` npm publisher → run the public npm canary → complete the real-client matrix and clean-machine canaries.

## 7. MVP Release Checklist

- [x] Public repository, module path, and npm scope metadata are final. *(public `wunderous/host-agents`; published `@opute/host-agent`)*
- [ ] OSI-compatible license selected and included. *(MIT is present in WT; not committed/tagged)*
- [ ] Security, contribution, conduct, support, and compatibility policies published. *(files are present in WT; public publication remains)*
- [x] Standalone mode has no runtime dependency on Opute Platform. *(runtime path and offline Streamable HTTP smoke; external network-trap evidence remains)*
- [x] Unknown/invalid configuration fails explicitly. *(Validate(), `--check`, and invalid-profile unit coverage)*
- [x] Canonical standalone tool catalog and classifications are versioned. *(generated reference docs remain)*
- [x] Read-only default and mutation/destructive policies are tested. *(unique-prefix disposable VM lifecycle is green)*
- [x] Linux x86_64 and arm64 artifacts, checksums, SBOMs, and provenance are published. *(GitHub Release `v0.1.1`; unauthenticated x64 canary passed)*
- [x] npm package is publicly installable and verifies binaries by default. *(`@opute/host-agent@0.1.1`; GitHub Actions publish and WSL npx canary passed)*
- [x] Native Windows/macOS behavior fails early with WSL/Linux guidance; no nonexistent artifacts are advertised. *(native Windows test passes; macOS remains CI-only)*
- [ ] VS Code, Claude Desktop, and Cursor compatibility is recorded.
- [x] Packaged Streamable HTTP and disposable Incus VM lifecycle gates are green. *(cross-compiled HTTP and unique-prefix create/list/inspect/delete gate passed; K3s/PostgreSQL/Cloudflare remain experimental)*
- [ ] Platform mode regression suite remains green.
- [ ] Public install canaries succeed without Opute credentials. *(GitHub binary and npm WSL canaries are green; named desktop-client canaries remain)*

## 8. First Implementation Slice

Start with M0 and the smallest vertical portion of M1/M3:

| # | Work item | Status (2026-07-19) |
|---|-----------|---------------------|
| 1 | Finalize Linux/WSL-only support decision and package name | **Done** — ADR records Linux/WSL and published package is `@opute/host-agent` |
| 2 | Strict config validation + single build-injected version | **Done (WT)** — still need release-tag agreement proof |
| 3 | Standalone catalog/isolation and mutation-denial tests | **Done (WT)** — versioned contract, metadata, isolation, and every-tool denial tests |
| 4 | Remove launcher native Windows artifact path | **Done (WT)** |
| 5 | Generate release checksums and require them in the launcher | **Done (WT)** |
| 6 | `LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`, standalone-first README | **Done (WT)** — policy files and standalone-first onboarding are present; publication remains |
| 7 | `npm pack` + local packaged Streamable HTTP smoke before public npm | **Done (WT)** — npm tarball test plus cross-compiled Linux HTTP smoke |

This slice resolved the highest-risk mismatch (an installable-looking path that could select a missing artifact or run an unverified download). **Checksum + hardened Linux-only launcher, public npm publish, npm package canary, Streamable HTTP smoke, and the disposable Incus VM lifecycle gate are green**; named-client canaries remain before claiming the full MVP complete.

## 9. Not fully captured (audit notes)

Items the original plan under-specified, or that current work only partially satisfies. Track these explicitly so “green-looking” diffs are not mistaken for MVP completion.

### 9.1 Org, remote, and naming resolution

- The public repository, Go module, artifact URLs, and npm repository metadata now use `wunderous/host-agents`.
- The published npm package is `@opute/host-agent`; the `@opute` organization scope and repository/module owner are aligned.

### 9.2 “Done in working tree” ≠ released

- LICENSE / SECURITY / CONTRIBUTING, Validate(), version injection, Close(), SHA256SUMS, and Linux-only launcher are largely **uncommitted or unreleased**.
- Plan progress must distinguish: code present locally · committed to default branch · tagged release · public npm.

### 9.3 Catalog isolation test status

- The implementation now validates equality against `schemas/standalone-tools.json`, rejects allowlist drift before startup, attaches classification metadata, and exercises the live Streamable HTTP catalog against platform/shell leaks.
- **Still not captured:** a retained public release fixture that deeply validates every schema against generated reference documentation.

### 9.4 Shell flag resolution

- Plan said “wire or remove”; this implementation removes `OPUTE_STANDALONE_ALLOW_HOST_SHELL` from standalone configuration and keeps `agent_shell` out of the standalone contract.

### 9.5 MVP scope vs exposed catalog

- Live standalone catalog includes VM ops **and** K3s, PostgreSQL/`run_sql`, Cloudflare tunnels.
- Plan allows marking those experimental, but does not force a default claim.
- **Not captured:** without an explicit M0 decision, release messaging will over-claim. Recommended default claim: **Linux/WSL Incus VM lifecycle + prerequisites/ops**; K3s/Postgres/tunnel **experimental** until M5 gates exist.

### 9.6 Fail-open vs unset defaults

- Original P0.5 treated silent normalization as always wrong.
- **Nuance:** unset → platform/http is valid for the platform profile; the bug is **set-but-invalid** and **standalone with platform credentials**.
- Capture the contract as: standalone launches must set mode/transport explicitly (launcher already forces them); typos must fail closed.

### 9.7 Version agreement is not proven

- Injected `version.Version` fixes the old hardcoded `1.0.0` heartbeat split **in code**.
- **Captured:** GitHub tag `v0.1.1` = npm package version `0.1.1` = artifact build version; GitHub Actions and public npm/release canaries passed.

### 9.8 Checksums ≠ supply-chain completeness

- SHA256SUMS closes the “optional single checksum” hole.
- **Not captured by checksums alone:** SBOM, provenance/attestations, launcher download hardening, cache corruption re-verify, transactional npm↔GitHub release.

### 9.9 Platform coupling and evidence locations

- `tmp/standalone_mcp_e2e.go` and similar local probes are not CI gates.
- Platform opute lifecycle dogfoods (separate repo) do **not** satisfy standalone packaged Streamable HTTP / npm install gates.
- **Not captured:** success of Opute control-plane E2E must not be counted as standalone MVP evidence.

### 9.10 Related plan

- Earlier foundations plan: `opute/.agents/plans/2026-07-host-agent-standalone-profile.md` (profile existence).
- This document owns **public OSS MVP** completion criteria; do not mark this plan done when only the profile plan’s internal preview criteria are met.
