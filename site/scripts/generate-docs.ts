#!/usr/bin/env bun
/**
 * Emits static Diátaxis docs under site/public/docs from audited operator truth.
 * Capability groups track site/context/tools-list.redacted.json (178 tools).
 * Architecture facts track README.md + docs/adr/* (verify before changing).
 */
import { mkdirSync, writeFileSync } from "fs"
import { join } from "path"

const root = "/home/houman/github/wunderous/opute-host-agent/site/public"

const CSS = "/styles.css?v=20260921h"

const MERMAID = `
<script src="https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js"></script>
<script>
  mermaid.initialize({
    startOnLoad: true,
    theme: "dark",
    securityLevel: "strict",
    themeVariables: {
      primaryColor: "#1a2e26",
      primaryTextColor: "#e8f0ea",
      primaryBorderColor: "#3d9b6e",
      lineColor: "#9bb0a3",
      secondaryColor: "#14201c",
      tertiaryColor: "#0c1210",
      background: "#0c1210",
      mainBkg: "#1a2e26",
      nodeBorder: "#3d9b6e",
      clusterBkg: "#14201c",
      titleColor: "#e8f0ea",
      edgeLabelBackground: "#0c1210"
    }
  });
</script>`

const nav = (current: string) => `
<header class="top">
  <a class="brand" href="/">Opute Host Agent</a>
  <nav>
    <a href="/docs/"${current === "docs" ? ' aria-current="page"' : ""}>Docs</a>
    <a href="/docs/get-started/">Get started</a>
  </nav>
</header>`

const side = (current: string) => `
<aside class="doc-nav" aria-label="Documentation">
  <h2>Tutorial</h2>
  <ul>
    <li><a href="/docs/get-started/"${current === "get-started" ? ' aria-current="page"' : ""}>Get started</a></li>
  </ul>
  <h2>How-to</h2>
  <ul>
    <li><a href="/docs/install/"${current === "install" ? ' aria-current="page"' : ""}>Install &amp; run</a></li>
    <li><a href="/docs/mcp-clients/"${current === "mcp-clients" ? ' aria-current="page"' : ""}>Connect an MCP client</a></li>
    <li><a href="/docs/dogfood/"${current === "dogfood" ? ' aria-current="page"' : ""}>Publish this site</a></li>
  </ul>
  <h2>Reference</h2>
  <ul>
    <li><a href="/docs/capabilities/"${current === "capabilities" ? ' aria-current="page"' : ""}>Capabilities</a></li>
    <li><a href="/docs/configuration/"${current === "configuration" ? ' aria-current="page"' : ""}>Configuration</a></li>
    <li><a href="/docs/recipe-primitives/"${current === "recipe-primitives" ? ' aria-current="page"' : ""}>Recipe &amp; plan primitives</a></li>
  </ul>
  <h2>Explanation</h2>
  <ul>
    <li><a href="/docs/concepts/"${current === "concepts" ? ' aria-current="page"' : ""}>Concepts</a></li>
    <li><a href="/docs/architecture/"${current === "architecture" ? ' aria-current="page"' : ""}>Architecture</a></li>
    <li><a href="/docs/recipes/"${current === "recipes" ? ' aria-current="page"' : ""}>Recipes &amp; plans</a></li>
    <li><a href="/docs/networking/"${current === "networking" ? ' aria-current="page"' : ""}>Networking</a></li>
    <li><a href="/docs/resources/"${current === "resources" ? ' aria-current="page"' : ""}>Resources &amp; safety</a></li>
  </ul>
</aside>`

const page = (opts: { title: string; current: string; body: string; mermaid?: boolean }) => `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>${opts.title} — Opute Host Agent</title>
  <meta name="description" content="Opute Host Agent documentation: ${opts.title}" />
  <link rel="stylesheet" href="${CSS}" />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=DM+Sans:wght@400;600;700&family=Instrument+Serif&display=swap" rel="stylesheet" />
</head>
<body>
${nav(opts.current)}
<div class="doc-shell">
${side(opts.current)}
<main class="doc">
${opts.body}
</main>
</div>
<footer>
  <span>Facts track the repository <a href="https://github.com/wunderous/host-agents/blob/main/README.md">README</a></span>
  <a href="/docs/architecture/">Architecture</a>
</footer>
${opts.mermaid ? MERMAID : ""}
</body>
</html>
`

const tools = (...names: string[]) =>
  `<div class="tool-list">${names.map((n) => `<code>${n}</code>`).join("")}</div>`

const pages: Record<string, { title: string; current: string; body: string; mermaid?: boolean }> = {
  "docs/index.html": {
    title: "Documentation",
    current: "docs",
    body: `
<p class="badge">Diátaxis</p>
<h1>Documentation</h1>
<p class="meta">Organized by what you need to do — not by the shape of our repo. Structure follows <a href="https://diataxis.fr/">Diátaxis</a>: tutorials teach, how-tos solve goals, reference states facts, explanation builds understanding.</p>

<div class="doc-index-sections">
  <section>
    <h2>Tutorial</h2>
    <ul>
      <li><a href="/docs/get-started/"><strong>Get started</strong><span>Build, start, and complete a first authenticated tools/list.</span></a></li>
    </ul>
  </section>
  <section>
    <h2>How-to</h2>
    <ul>
      <li><a href="/docs/install/"><strong>Install &amp; run</strong><span>From-source, npm launcher, serve modes, mutations, WSL.</span></a></li>
      <li><a href="/docs/mcp-clients/"><strong>Connect an MCP client</strong><span>Cursor, Claude Desktop, VS Code — with Bearer auth.</span></a></li>
      <li><a href="/docs/dogfood/"><strong>Publish this site</strong><span>Recipe-hosted opute.io / www.opute.io without touching Platform.</span></a></li>
    </ul>
  </section>
  <section>
    <h2>Reference</h2>
    <ul>
      <li><a href="/docs/capabilities/"><strong>Capabilities</strong><span>All major tool groups from a live 178-tool catalog capture.</span></a></li>
      <li><a href="/docs/configuration/"><strong>Configuration</strong><span>Ports, bind hosts, required identity, auth, Cloudflare env.</span></a></li>
      <li><a href="/docs/recipe-primitives/"><strong>Recipe &amp; plan primitives</strong><span>Fields, statuses, assertion ops, caps — dry facts.</span></a></li>
    </ul>
  </section>
  <section>
    <h2>Explanation</h2>
    <ul>
      <li><a href="/docs/concepts/"><strong>Concepts</strong><span>Host Agent vs Platform, catalog authority, fail-closed identity.</span></a></li>
      <li><a href="/docs/architecture/"><strong>Architecture</strong><span>Planes, providers, Cordis kernel — with diagrams.</span></a></li>
      <li><a href="/docs/recipes/"><strong>Recipes &amp; plans</strong><span>Why both exist; when to use which family.</span></a></li>
      <li><a href="/docs/networking/"><strong>Networking</strong><span>Three HA seams, tunnels, public vs private paths.</span></a></li>
      <li><a href="/docs/resources/"><strong>Resources &amp; safety</strong><span>Canonical URIs, admission, effects, redaction.</span></a></li>
    </ul>
  </section>
</div>

<div class="callout">
  <strong>Source of truth.</strong> Operator facts in these pages must match
  <code>README.md</code> and the live <code>tools/list</code> catalog. If a page
  and the catalog disagree, trust the catalog — then update the page.
</div>
`,
  },

  "docs/get-started/index.html": {
    title: "Get started",
    current: "get-started",
    body: `
<p class="badge">Tutorial</p>
<h1>Get started</h1>
<p class="meta">A learning path: by the end you will have a running standalone Host Agent and a successful authenticated catalog listing. Prefer following steps in order; do not skip ahead to mutations.</p>

<h2>What you will need</h2>
<ul>
  <li>Linux or WSL2 (Incus host execution is Linux-only)</li>
  <li>Go toolchain for a from-source build, <em>or</em> network access for <code>npx @opute/host-agent</code></li>
  <li>About ten minutes</li>
</ul>

<ol class="steps">
  <li>
    <strong>Build or obtain the binary</strong>
    <pre><code>git clone https://github.com/wunderous/host-agents.git
cd host-agents
make build   # → dist/opute-host-agent</code></pre>
    <p>Alternatively skip the clone and use the npm launcher in the next step with a downloaded release binary.</p>
  </li>
  <li>
    <strong>Set identity and auth</strong>
    <pre><code>export OPUTE_REMOTE_AGENT_ID=local-host-agent
export OPUTE_INFRA_PROVIDER_ID=incus
export OPUTE_STANDALONE_STATE_DIR="$HOME/.opute/standalone"
export MCP_AUTH_TOKEN=dev-token</code></pre>
    <p><code>OPUTE_REMOTE_AGENT_ID</code> is required by configuration validation. <code>MCP_AUTH_TOKEN</code> is the Bearer bootstrap secret your client will send to <code>/mcp</code>.</p>
  </li>
  <li>
    <strong>Check configuration, then serve</strong>
    <pre><code>./dist/opute-host-agent --check
./dist/opute-host-agent
# listens on http://127.0.0.1:3014/mcp by default</code></pre>
    <p>Leave this process running. In another terminal, confirm health:</p>
    <pre><code>curl -sS http://127.0.0.1:3014/health</code></pre>
  </li>
  <li>
    <strong>Point an MCP client at the agent</strong>
    <p>Use the JSON in <a href="/docs/mcp-clients/">Connect an MCP client</a>. Include the Bearer header matching <code>MCP_AUTH_TOKEN</code>.</p>
  </li>
  <li>
    <strong>List tools</strong>
    <p>From the client, run <code>tools/list</code> (or your client’s equivalent). You should see a revisioned catalog of typed tools. Then try a read-only call such as <code>get_host_info</code>.</p>
  </li>
</ol>

<div class="callout warn">
  <strong>Do not enable mutations yet.</strong> Standalone mutating tools stay denied until
  <code>OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code>. Finish discovery first.
</div>

<p>Next: <a href="/docs/install/">Install options</a> · <a href="/docs/architecture/">Architecture</a> · <a href="/docs/capabilities/">Capabilities</a></p>
`,
  },

  "docs/install/index.html": {
    title: "Install & run",
    current: "install",
    body: `
<p class="badge">How-to</p>
<h1>Install &amp; run</h1>
<p class="meta">Goal-oriented procedures for getting a Host Agent listening. Choose the path that matches your environment.</p>

<h2>From source</h2>
<pre><code>make build
export OPUTE_REMOTE_AGENT_ID=local-host-agent
export OPUTE_INFRA_PROVIDER_ID=incus
export OPUTE_STANDALONE_STATE_DIR="$HOME/.opute/standalone"
export MCP_AUTH_TOKEN=dev-token
./dist/opute-host-agent --check
./dist/opute-host-agent serve --mode standalone --transport http</code></pre>
<p>Bare <code>./dist/opute-host-agent</code> is equivalent (defaults to standalone + http).</p>

<h2>npm launcher</h2>
<pre><code>export MCP_AUTH_TOKEN=dev-token
npx -y @opute/host-agent start --background
npx -y @opute/host-agent url      # http://127.0.0.1:3014/mcp
npx -y @opute/host-agent status
npx -y @opute/host-agent stop</code></pre>
<p>The launcher defaults <code>OPUTE_REMOTE_AGENT_ID</code> to <code>local-host-agent</code> when unset. It strips most other <code>OPUTE_*</code> values so a Platform-enrolled shell cannot leak enrollment secrets into standalone.</p>
<p>Development binary override:</p>
<pre><code>OPUTE_HOST_AGENT_BINARY="$PWD/dist/opute-host-agent" \\
  npx -y @opute/host-agent start --background</code></pre>

<h2>Serve modes</h2>
<table>
  <thead><tr><th>Mode</th><th>Default bind</th><th>Default port</th><th>Use</th></tr></thead>
  <tbody>
    <tr><td><code>standalone</code></td><td><code>127.0.0.1</code></td><td><strong>3014</strong></td><td>Local IDE / laptop</td></tr>
    <tr><td><code>platform</code></td><td><code>0.0.0.0</code></td><td><strong>3004</strong></td><td>Enrolled host beside Opute control plane</td></tr>
  </tbody>
</table>
<p>Override with <code>HOST_MCP_BIND_HOST</code> and <code>HOST_MCP_PORT</code>. Do not copy a platform <code>:3004</code> snippet into a standalone laptop config unless you intend that collision.</p>

<h2>Mutations</h2>
<pre><code>export OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code></pre>
<p>Without this, standalone mutation tools fail closed with an explicit error.</p>

<h2>WSL</h2>
<p>Run the <strong>Linux</strong> binary inside WSL. Point the Windows MCP client at <code>http://127.0.0.1:3014/mcp</code> (enable localhost forwarding / portproxy as needed). Native Windows is not an Incus host runtime.</p>

<h2>Production hosts</h2>
<p>Remote production installs come from the Opute platform UI (<strong>Connect Remote Host</strong>). The generated script writes <code>host-agent.env</code> with canonical <code>OPUTE_REMOTE_AGENT_ID</code> and <code>MCP_AUTH_TOKEN</code>, then starts the systemd unit. GitHub Releases are for CI and manual smoke — not the primary production credential path.</p>

<div class="note">Release artifacts from <code>make artifacts</code> include <code>host-agent-linux-*.gz</code>, Windows gzip, k3s/cloudflare/tailscale provider binaries, and <code>SHA256SUMS</code>. The day-to-day build product remains <code>dist/opute-host-agent</code>.</div>
`,
  },

  "docs/mcp-clients/index.html": {
    title: "Connect an MCP client",
    current: "mcp-clients",
    body: `
<p class="badge">How-to</p>
<h1>Connect an MCP client</h1>
<p class="meta">Wire Cursor, Claude Desktop, or VS Code to a running Host Agent over Streamable HTTP.</p>

<h2>Prerequisites</h2>
<ul>
  <li>Host Agent listening (see <a href="/docs/install/">Install</a>)</li>
  <li>Matching <code>MCP_AUTH_TOKEN</code> on the agent process</li>
</ul>

<h2>Cursor / Claude Desktop</h2>
<pre><code>{
  "mcpServers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp",
      "headers": {
        "Authorization": "Bearer dev-token"
      }
    }
  }
}</code></pre>

<h2>VS Code-style</h2>
<pre><code>{
  "servers": {
    "opute-local": {
      "type": "http",
      "url": "http://127.0.0.1:3014/mcp",
      "headers": {
        "Authorization": "Bearer dev-token"
      }
    }
  }
}</code></pre>

<div class="callout warn">
  <strong>Auth is not optional when a bootstrap token is configured.</strong>
  A config with only <code>url</code> against a token-gated agent returns HTTP 401.
  Omit headers only when the agent has no bootstrap token and the client completes OAuth for this resource.
</div>

<h2>What the client should do</h2>
<ol>
  <li>Connect over Streamable HTTP (stdio is not supported).</li>
  <li>Discover the revisioned capability catalog.</li>
  <li>Validate tool arguments against catalog schemas.</li>
  <li>Call tools by catalog name — the Host Agent does not interpret prose.</li>
</ol>

<h2>Multiple agents in one workspace</h2>
<p>Set <code>OPUTE_MCP_PREFIX_TOOL_NAMES=true</code> on each agent so tool names do not collide. <code>GET /health</code> exposes <code>mcpToolNamePrefix</code>. Wire names become <code>{prefix}_{catalogName}</code>. Do <strong>not</strong> enable this on Platform-enrolled instances — the control plane calls unprefixed catalog names.</p>
`,
  },

  "docs/dogfood/index.html": {
    title: "Publish this site",
    current: "dogfood",
    body: `
<p class="badge">How-to</p>
<h1>Publish this site</h1>
<p class="meta">How <code>opute.io</code> and <code>www.opute.io</code> are hosted as Host Agent dogfood — and how to tear them down without touching Platform.</p>

<h2>Goal</h2>
<p>Serve this static marketing/docs package from a K3s workload via a <strong>Host Agent recipe</strong>, exposed through a dedicated Cloudflare tunnel.</p>

<pre class="mermaid">
flowchart TB
  SRC[site/ source] --> BUILD[OCI image build]
  R[www-opute-io recipe] --> BUILD
  BUILD --> REG[(Cluster registry)]
  R --> HA[Host Agent id]
  HA --> K3S[Admitted K3s cluster]
  REG --> APPLY[apply_manifest]
  APPLY --> W[host-agent-www workload]
  W --> TUN[Dedicated CF tunnel<br/>opute-www-opute-io]
  CLIENT[Public browser] --> TUN
  TUN --> W
</pre>

<h2>Path of record</h2>
<ul>
  <li>Recipe: <code>site/recipes/www-opute-io.yaml</code></li>
  <li>Teardown: <code>site/recipes/www-opute-io-teardown.yaml</code></li>
  <li>Submit with <code>run_host_local_recipe</code> on the owning Host Agent</li>
</ul>
<p>Ad-hoc <code>kubectl</code> is not the deploy path of record.</p>

<h2>Isolation invariants</h2>
<table>
  <thead><tr><th>Own</th><th>Never touch</th></tr></thead>
  <tbody>
    <tr><td>Namespaces <code>host-agent-site</code>, <code>host-agent-www-tunnel</code></td><td>Namespace <code>opute-platform</code></td></tr>
    <tr><td>Tunnel <code>opute-www-opute-io</code></td><td>Tunnel <code>opute-platform-opute-io</code></td></tr>
    <tr><td>Hostnames <code>opute.io</code>, <code>www.opute.io</code></td><td><code>platform.opute.io</code>, <code>mcp.opute.io</code></td></tr>
  </tbody>
</table>

<h2>What “pass” means</h2>
<ul>
  <li>External HTTPS GET of <code>https://opute.io/</code> and <code>https://www.opute.io/</code> returns this site</li>
  <li><code>platform.opute.io</code> remains healthy; <code>mcp.opute.io</code> retains its role</li>
</ul>
<div class="note">Site reachability is not two-node write / etcd-quorum HA. Do not narrate dogfood pass as consensus HA.</div>

<h2>Cloudflare credentials</h2>
<p>Tunnel/DNS mutation needs <code>CLOUDFLARE_API_TOKEN</code>, <code>CLOUDFLARE_ACCOUNT_ID</code>, and <code>CLOUDFLARE_ZONE_ID</code> on the Cloudflare provider. Connector run tokens are minted by <code>opute.capability.tunneling.ensure-host-tunnel</code> and are write-only in MCP results (external clients see <code>[redacted]</code>). The recipe therefore installs the Kubernetes connector in-process with <code>manageHostConnector: true</code>.</p>
`,
    mermaid: true,
  },

  "docs/capabilities/index.html": {
    title: "Capabilities",
    current: "capabilities",
    body: `
<p class="badge">Reference</p>
<h1>Capabilities</h1>
<p class="meta">Facts about the typed tool surface from a redacted live capture (178 tools, 2026-09-20). Always prefer a live <code>tools/list</code> / <code>get_capability_catalog</code> — catalogs are revisioned. Grouping here is navigational, not a hard API boundary.</p>

<h2>Contract split</h2>
<ul>
  <li><strong>Host Agent</strong> owns provider-neutral capability contracts, provider lifecycle, canonical resource admission, redacted observations, and MCP transport.</li>
  <li><strong>Opute Platform</strong> owns intent, authorization, durable orchestration, and semantic outcomes.</li>
</ul>

<h2>Catalog &amp; session</h2>
${tools(
  "get_capability_catalog",
  "open_assistant_session",
  "list_agents",
  "configure_agent_connection",
)}

<h2>Host &amp; inventory</h2>
${tools(
  "get_host_info",
  "get_host_capacity",
  "detect_host_platform",
  "list_host_services",
  "inspect_host_file",
  "ensure_host_file",
  "remove_host_file",
  "ensure_host_tool",
  "ensure_host_artifact",
  "run_host_command",
  "probe_http_endpoint",
  "restart_host_service",
  "set_host_service_state",
  "compact_wsl_disk",
  "terminate_wsl_distribution",
  "shutdown_wsl",
)}

<h2>Guests (Incus)</h2>
<p>Default infra provider today: <code>OPUTE_INFRA_PROVIDER_ID=incus</code>. Guest URIs carry a runtime kind (<code>vm:</code> vs <code>container:</code>).</p>
${tools(
  "install_incus_stack",
  "uninstall_incus_stack",
  "reset_incus_stack",
  "list_vms",
  "create_vm",
  "provision_vm",
  "provision_container",
  "start_vm",
  "stop_vm",
  "restart_vm",
  "delete_vm",
  "get_vm_info",
  "update_vm_resources",
  "run_instance_command",
  "stream_vm_console",
  "send_console_input",
  "probe_incus_gpu",
  "inspect_guest_storage",
  "trim_guest_storage",
)}

<h2>Kubernetes workloads</h2>
<p>Neutral cluster ops appear both as underscore aliases and as dotted <code>opute.capability.kubernetes.*</code> provider ops. Prefer the live catalog name your client discovered.</p>
${tools(
  "apply_manifest",
  "list_pods",
  "list_namespaces",
  "list_deployments",
  "list_services",
  "get_k8s_resource",
  "get_k8s_resource_status",
  "delete_k8s_resource",
  "put_k8s_secret",
  "exec_kubernetes_command",
  "install_helm_chart",
  "render_helm_template",
  "list_k8s_events",
  "list_storage_classes",
  "list_ingress_classes",
  "register_kubernetes_cluster",
  "list_kubernetes_clusters",
  "opute.capability.kubernetes.apply-manifest",
  "opute.capability.kubernetes.get-resource",
  "opute.capability.kubernetes.exec-command",
)}

<h2>K3s provision &amp; membership</h2>
${tools(
  "opute.capability.kubernetes.provision",
  "opute.capability.kubernetes.validate",
  "opute.capability.kubernetes.status",
  "opute.capability.kubernetes.prepare-ha",
  "opute.capability.kubernetes.prepare-join",
  "opute.capability.kubernetes.redeem-join",
  "opute.capability.kubernetes.join-node",
  "opute.capability.kubernetes.remove-node",
  "opute.capability.kubernetes.inspect-membership",
  "opute.capability.kubernetes.recover-quorum",
  "opute.capability.kubernetes.ensure-ha-endpoint",
  "opute.capability.kubernetes.get-cluster-info",
)}

<h2>Cluster storage &amp; registry reclaim</h2>
${tools(
  "inspect_guest_storage",
  "prune_unused_cluster_images",
  "garbage_collect_cluster_registry",
  "trim_guest_storage",
  "opute.capability.kubernetes.configure-registry",
  "opute.capability.kubernetes.garbage-collect-registry",
  "opute.capability.kubernetes.prune-unused-images",
  "opute.capability.kubernetes.trim-guest-storage",
)}

<h2>OCI / containers</h2>
${tools(
  "stage_build_context",
  "ensure_oci_builder",
  "ensure_docker",
  "build_and_push_oci_image",
  "configure_oci_storage",
  "install_oci_registry",
  "get_oci_registry_status",
  "delete_oci_registry",
  "inspect_container_storage",
  "cleanup_container_storage",
  "probe_gpu_container",
)}

<h2>Recipes &amp; plans</h2>
<p>Why: <a href="/docs/recipes/">Recipes &amp; plans</a>. Fields: <a href="/docs/recipe-primitives/">primitives</a>.</p>
${tools(
  "validate_host_local_recipe",
  "run_host_local_recipe",
  "validate_host_plan",
  "run_host_plan",
  "get_host_plan_run",
  "validate_runtime_recipe",
  "run_runtime_recipe",
  "get_runtime_recipe_run",
  "validate_tunnel_recipe",
  "run_tunnel_recipe",
  "get_tunnel_run",
)}

<h2>Tunneling &amp; public exposure</h2>
${tools(
  "opute.capability.tunneling.validate",
  "opute.capability.tunneling.ensure-host-tunnel",
  "opute.capability.tunneling.probe-host-tunnel",
  "opute.capability.tunneling.remove-host-tunnel",
  "opute.capability.tunneling.install-kubernetes-connector",
  "opute.capability.tunneling.delete-kubernetes-connector",
  "ensure_public_mcp_tunnel",
  "ensure_public_mcp_quick_tunnel",
  "remove_public_mcp_quick_tunnel",
  "probe_host_exposure",
  "remove_host_exposure",
  "ensure_cloudflared_tunnel",
  "get_cloudflare_tunnel_status",
)}

<h2>HA networking seams</h2>
<p>Canonical contracts are <code>mesh-membership.v1</code>, <code>private-mesh.v1</code>, and <code>public-ingress.v1</code> (ADR-0016). Live catalogs may still expose deprecated <code>network-overlay.*</code> migration aliases — prefer the three seams for new work. Details: <a href="/docs/networking/">Networking</a>.</p>
${tools(
  "opute.capability.network-overlay.validate",
  "opute.capability.network-overlay.prepare-membership",
  "opute.capability.network-overlay.attach-target",
  "opute.capability.network-overlay.probe-reachability",
  "opute.capability.network-overlay.ensure-ha-endpoint",
  "opute.capability.network-overlay.remove-ha-endpoint",
  "opute.capability.network-overlay.remove-membership",
)}

<h2>Providers</h2>
${tools(
  "opute.provider.install",
  "opute.provider.validate",
  "opute.provider.status",
  "opute.provider.reload",
  "opute.provider.teardown",
  "install_provider_tools",
  "uninstall_provider_tools",
)}

<h2>LLM serving <span class="muted-tag">(optional)</span></h2>
<p>Core Host Agent works without an LLM provider. Ollama is an activated optional layer (<code>plugins/llm/ollama</code>).</p>
${tools(
  "probe_local_llm",
  "check_local_llm_prerequisites",
  "configure_local_llm_runtime",
  "start_local_llm_runtime",
  "stop_local_llm_runtime",
  "list_local_llm_models",
  "install_local_llm_model",
  "configure_local_llm_model",
  "remove_local_llm_model",
  "ensure_local_llm_relay",
  "ensure_local_llm_k3s_proxy",
  "probe_openai_compatible_server",
)}

<h2>Postgres &amp; SQLite</h2>
${tools(
  "reconcile_postgresql_service",
  "get_postgresql_service_status",
  "remove_postgresql_service",
  "release_postgresql_service_relay",
  "ensure_sqlite_database",
  "get_sqlite_database_status",
  "remove_sqlite_database",
)}

<h2>Serving &amp; cluster agent</h2>
${tools(
  "reconcile_serving_assignment",
  "discover_service_ingress",
  "list_ingress_classes",
  "list_certificate_issuers",
  "install_cluster_agent",
  "inspect_workload",
)}

<div class="callout">
  Mutating tools in standalone mode require <code>OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code>.
  Platform mode follows Platform enrollment policy instead of that env flag.
</div>
`,
  },

  "docs/configuration/index.html": {
    title: "Configuration",
    current: "configuration",
    body: `
<p class="badge">Reference</p>
<h1>Configuration</h1>
<p class="meta">Environment and CLI facts. Precedence: CLI <code>--env KEY=VALUE</code> → process environment → <code>--env-file</code> / <code>OPUTE_HOST_AGENT_ENV_FILE</code> (file fills unset keys only).</p>

<h2>Required</h2>
<table>
  <thead><tr><th>Variable</th><th>Notes</th></tr></thead>
  <tbody>
    <tr><td><code>OPUTE_REMOTE_AGENT_ID</code></td><td>Canonical agent id. Required by <code>Validate()</code>. npm launcher defaults to <code>local-host-agent</code>.</td></tr>
  </tbody>
</table>

<h2>Listen</h2>
<table>
  <thead><tr><th>Variable</th><th>Standalone default</th><th>Platform default</th></tr></thead>
  <tbody>
    <tr><td><code>HOST_MCP_PORT</code></td><td><code>3014</code></td><td><code>3004</code></td></tr>
    <tr><td><code>HOST_MCP_BIND_HOST</code></td><td><code>127.0.0.1</code></td><td><code>0.0.0.0</code></td></tr>
    <tr><td><code>OPUTE_AGENT_MODE</code></td><td><code>standalone</code></td><td><code>platform</code></td></tr>
  </tbody>
</table>

<h2>Auth &amp; mutations</h2>
<table>
  <thead><tr><th>Variable</th><th>Purpose</th></tr></thead>
  <tbody>
    <tr><td><code>MCP_AUTH_TOKEN</code></td><td>Bootstrap Bearer for <code>/mcp</code></td></tr>
    <tr><td><code>OPUTE_STANDALONE_ALLOW_MUTATIONS</code></td><td><code>true</code> enables standalone mutations</td></tr>
    <tr><td><code>OPUTE_STANDALONE_STATE_DIR</code></td><td>Standalone state / journal directory</td></tr>
    <tr><td><code>OPUTE_MCP_PREFIX_TOOL_NAMES</code></td><td>Prefix wire tool names for multi-agent IDE workspaces</td></tr>
  </tbody>
</table>

<h2>Provider / Incus</h2>
<table>
  <thead><tr><th>Variable</th><th>Purpose</th></tr></thead>
  <tbody>
    <tr><td><code>OPUTE_INFRA_PROVIDER_ID</code></td><td>Normalizes to <code>incus</code>; non-incus values are rejected today</td></tr>
    <tr><td><code>OPUTE_INCUS_BINARY_PATH</code></td><td>Optional path to <code>incus</code></td></tr>
  </tbody>
</table>

<h2>Cloudflare provider</h2>
<table>
  <thead><tr><th>Variable</th><th>Purpose</th></tr></thead>
  <tbody>
    <tr><td><code>CLOUDFLARE_API_TOKEN</code></td><td>API auth for tunnel/DNS mutation</td></tr>
    <tr><td><code>CLOUDFLARE_ACCOUNT_ID</code></td><td>Account scope</td></tr>
    <tr><td><code>CLOUDFLARE_ZONE_ID</code></td><td>DNS zone scope</td></tr>
  </tbody>
</table>

<h2>Retired (rejected)</h2>
<p>Do not set: <code>OPUTE_CPC_TOKEN</code>, <code>OPUTE_HOST_WS_URL</code>, <code>OPUTE_REMOTE_AGENT_AUTH_TOKEN</code>, <code>OPUTE_ONBOARDING_*</code>, <code>OPUTE_REVERSE_TUNNEL</code>. The Go agent fails closed if they appear.</p>

<h2>CLI</h2>
<pre><code>opute-host-agent --check
opute-host-agent serve --mode standalone|platform --transport http
opute-host-agent public-mcp …
opute-host-agent recipe …
opute-host-agent provider …
opute-host-agent help</code></pre>
`,
  },

  "docs/concepts/index.html": {
    title: "Concepts",
    current: "concepts",
    body: `
<p class="badge">Explanation</p>
<h1>Concepts</h1>
<p class="meta">Product boundaries and safety model — not a procedure. For diagrams and package layout see <a href="/docs/architecture/">Architecture</a>.</p>

<h2>Host Agent vs Platform</h2>
<p>The Host Agent is an execution plane on a single Linux host (or enrolled remote). It speaks Streamable HTTP MCP, admits canonical resource URIs, and runs typed tools. Opute Platform is a separate control plane: it decides <em>what</em> should happen, holds durable orchestration, and issues enrollment credentials.</p>
<p>Public marketing at <code>opute.io</code> / <code>www.opute.io</code> is Host Agent dogfood. <code>platform.opute.io</code> and <code>mcp.opute.io</code> are Platform surfaces. Collapsing those identities is how operators accidentally take down the control plane while “just fixing docs.”</p>

<pre class="mermaid">
flowchart LR
  Client[MCP client / Platform] -->|typed tools/list + call| HA[Host Agent]
  HA -->|ops| Host[Linux host]
  HA -->|plugins| Prov[Provider MCP processes]
  Platform[Opute Platform] -->|intent + enrollment| Client
  Platform -.->|does not own host exec| HA
</pre>

<h2>Typed catalog, not folklore</h2>
<p>Clients do not send free-form shell. They discover a revisioned catalog, validate arguments against schemas, and invoke named tools. That is why memorized tool lists go stale and why <code>tools/list</code> is authoritative.</p>

<h2>Recipes and plans</h2>
<p>A <strong>recipe</strong> is the cookbook page: which dish, which ingredients, which version. A <strong>plan</strong> is the cooking steps the kitchen actually follows. The Host Agent keeps both so work can be pinned and parameterized, while only one runner ever executes. More: <a href="/docs/recipes/">Recipes &amp; plans</a> · field list: <a href="/docs/recipe-primitives/">primitives</a>.</p>

<h2>Fail-closed identity</h2>
<p>Every process must carry a canonical <code>OPUTE_REMOTE_AGENT_ID</code>. Standalone mutations stay off until explicitly enabled. Observations that could contain secrets are redacted in MCP results (write-only schema fields become <code>[redacted]</code>).</p>

<h2>Providers as plugins</h2>
<p>Kubernetes, Cloudflare, Tailscale, Ollama, and Host OS surfaces arrive as provider plugins under <code>plugins/</code>, not as hard-wired product branches inside the MCP server. Shared host seams live in <code>internal/hostruntime</code>; there is no separate <code>internal/provider</code> package.</p>

<h2>Optional LLM layer</h2>
<p>Core infrastructure tools must work when no LLM provider is installed or healthy. LLM serving is an activated capability, not a dependency of the kernel.</p>

<h2>How this site is written</h2>
<p>Pages follow <a href="https://diataxis.fr/">Diátaxis</a>: tutorials teach a first success; how-tos accomplish a known goal; reference states facts without narrative; explanation answers <em>why</em> and builds mental models. Mixing those modes produces pages that intimidate without operating — so recipe <em>why</em> lives under Explanation, and the field catalog under Reference.</p>
`,
    mermaid: true,
  },

  "docs/architecture/index.html": {
    title: "Architecture",
    current: "architecture",
    body: `
<p class="badge">Explanation</p>
<h1>Architecture</h1>
<p class="meta">How the Host Agent is put together: planes, transport, providers, and the Cordis kernel. Normative ADRs live under <code>docs/adr/</code> in the repository.</p>

<h2>Two planes</h2>
<table>
  <thead><tr><th></th><th>Host Agent</th><th>Opute Platform</th></tr></thead>
  <tbody>
    <tr><td>Job</td><td>Execute typed assignments on one host</td><td>Intent, authz, durable orchestration</td></tr>
    <tr><td>Surface</td><td>Streamable HTTP MCP <code>/mcp</code></td><td>Web UI + control plane</td></tr>
    <tr><td>Public origin (dogfood)</td><td><code>opute.io</code> / <code>www.opute.io</code></td><td><code>platform.opute.io</code>, <code>mcp.opute.io</code></td></tr>
    <tr><td>Default listen</td><td>standalone <code>127.0.0.1:3014</code></td><td>platform mode <code>0.0.0.0:3004</code></td></tr>
  </tbody>
</table>

<pre class="mermaid">
flowchart TB
  subgraph control [Control plane]
    P[Opute Platform]
  end
  subgraph exec [Execution plane — one host]
    HA[Host Agent<br/>internal/hostmcp + cordis]
    HR[hostruntime]
    Dom[domain packages<br/>host / incus / k8s / oci / …]
    HA --> HR
    HA --> Dom
  end
  subgraph plugins [Provider plugins]
    CF[cloudflare]
    TS[tailscale]
    K3[k3s]
    OL[ollama]
  end
  P -->|typed MCP| HA
  IDE[Local IDE client] -->|Bearer /mcp| HA
  HA -->|lifecycle install/reload| plugins
  plugins -->|capability ops| Ext[External APIs / daemons]
</pre>

<h2>MCP edge</h2>
<ul>
  <li><code>POST /mcp</code> — Streamable HTTP; auth via bootstrap <code>MCP_AUTH_TOKEN</code> Bearer and/or OAuth.</li>
  <li><code>GET /health</code> — open; may expose <code>mcpToolNamePrefix</code> when name prefixing is enabled.</li>
  <li>stdio transport is not supported for the public Host Agent surface.</li>
</ul>

<pre class="mermaid">
sequenceDiagram
  participant C as MCP client
  participant HA as Host Agent
  C->>HA: POST /mcp Authorization Bearer
  HA-->>C: initialize / capabilities
  C->>HA: tools/list
  HA-->>C: revisioned CatalogSnapshot
  C->>HA: tools/call name + args
  Note over HA: admit URI · check effects · dispatch
  HA-->>C: typed result (redacted)
</pre>

<h2>Catalog → admission → dispatch</h2>
<p>The catalog is authoritative (ADR-0009). Tool dispatch validates against the revision the client saw, admits resource URIs, and applies effect gates before provider or domain code runs.</p>

<pre class="mermaid">
flowchart LR
  List[tools/list] --> Rev[Catalog revision]
  Call[tools/call] --> Rev
  Rev --> Admit[Resource URI admission]
  Admit --> Eff[Effect / mutation gate]
  Eff --> Dom[Domain or provider op]
  Dom --> Out[Observation + redaction]
</pre>

<h2>Providers</h2>
<p>Providers are separate Streamable HTTP MCP processes under <code>plugins/</code>. The Host Agent owns lifecycle (<code>opute.provider.*</code>), activation, and displace-on-activate exclusivity per Service Definition — not vendor CLI folklore.</p>
<ul>
  <li><code>plugins/tunneling/cloudflare</code> — public ingress / tunnels</li>
  <li><code>plugins/tunneling/tailscale</code> — mesh + Funnel bundle</li>
  <li><code>plugins/kubernetes/k3s</code> — cluster provision / membership</li>
  <li><code>plugins/llm/ollama</code> — optional LLM serving</li>
  <li><code>plugins/platform/hostos</code> — host platform identity</li>
</ul>

<h2>Package map</h2>
<table>
  <thead><tr><th>Path</th><th>Owns</th></tr></thead>
  <tbody>
    <tr><td><code>internal/hostmcp</code></td><td>MCP façade, catalog snapshot, tool dispatch</td></tr>
    <tr><td><code>internal/cordis</code></td><td>Kernel services / fibers (MCP is an edge)</td></tr>
    <tr><td><code>internal/catalog</code></td><td>Revisioned capability registry</td></tr>
    <tr><td><code>internal/recipe</code> → <code>internal/plan</code></td><td>Declarative expand then single runner</td></tr>
    <tr><td><code>internal/hostruntime</code></td><td>Shared host seams for providers</td></tr>
    <tr><td><code>internal/domain/*</code></td><td>host, incus, kubernetes, oci, llm, postgres, serving, cluster</td></tr>
    <tr><td><code>contracts/</code> + <code>schemas/</code></td><td>Neutral IDs and JSON schemas</td></tr>
    <tr><td><code>plugins/**</code></td><td>Provider MCP binaries</td></tr>
  </tbody>
</table>

<p>Related: <a href="/docs/recipes/">Recipes &amp; plans</a> · <a href="/docs/networking/">Networking</a> · <a href="/docs/resources/">Resources &amp; safety</a></p>
`,
    mermaid: true,
  },

  "docs/recipes/index.html": {
    title: "Recipes & plans",
    current: "recipes",
    body: `
<p class="badge">Explanation</p>
<h1>Recipes &amp; plans</h1>
<p class="meta">About why Host Agent has both recipes and plans — and how to choose a family. Field lists live under <a href="/docs/recipe-primitives/">Recipe &amp; plan primitives</a>.</p>

<h2>In one sentence</h2>
<p>A <strong>recipe</strong> is the cookbook page (which dish, which ingredients, which version). A <strong>plan</strong> is the cooking steps the kitchen actually follows. Only the plan runs.</p>

<pre class="mermaid">
flowchart LR
  Recipe[Recipe envelope] --> Expand[Pin · inputs · expand]
  Expand --> Plan[host-plan.v1]
  Plan --> Runner[plan.Runner]
  Runner --> Tools[Typed tools]
</pre>

<h2>Why not one thing?</h2>
<p>Without recipes, every caller would hand-write expanded plans and lose version pins, input schemas, and family-specific activation. Without plans, each recipe family would invent its own executor — Cordis C-03 forbids that. So recipes <em>package</em> work; plans <em>execute</em> it.</p>
<ul>
  <li><strong>Recipe owns</strong> identity (<code>recipeId</code>/<code>recipeVersion</code>), inputs (including secrets), compatibility, source pin (<code>sha256</code>), and an embedded plan.</li>
  <li><strong>Plan owns</strong> the DAG: nodes, dependsOn, validate/assert, retry, recover, compensate, converge.</li>
  <li><strong>Bare plans</strong> exist when something already expanded the envelope (for example provider teardown returning <code>host-plan.v1</code>).</li>
</ul>

<h2>Which family when?</h2>
<table>
  <thead><tr><th>You want…</th><th>Use</th><th>MCP entry</th></tr></thead>
  <tbody>
    <tr><td>Work on this Host Agent only (dogfood site, local install)</td><td><code>host-recipe.v1</code> local</td><td><code>run_host_local_recipe</code></td></tr>
    <tr><td>An already-expanded DAG</td><td>bare <code>host-plan.v1</code></td><td><code>run_host_plan</code></td></tr>
    <tr><td>Activate a serving runtime (LLM, mesh, HTTP exposure)</td><td><code>runtime-recipe.v1</code></td><td><code>run_runtime_recipe</code></td></tr>
    <tr><td>Public hostname → local target bindings</td><td><code>tunnel-recipe.v1</code></td><td><code>run_tunnel_recipe</code></td></tr>
    <tr><td>Multi-host coordination with waits</td><td>Platform-distributed host recipe</td><td>Submit to Platform — HA refuses</td></tr>
  </tbody>
</table>

<h2>What the runner is doing</h2>
<p>After expansion, one runner walks topo levels, applies mutating actions only when readiness fails, can recover then revalidate, and on abort runs compensate in reverse order. Host-local recipes cannot use durable <code>wait</code> nodes — those belong to Platform-distributed work.</p>

<pre class="mermaid">
flowchart TB
  N[Node] -->|already good| Sat[satisfied]
  N -->|needs work| Act[action]
  Act --> Val[validate / assert]
  Val -->|fail| Rec[recover?]
  Rec -->|abort| Comp[compensate]
</pre>

<h2>Where to go next</h2>
<ul>
  <li><a href="/docs/recipe-primitives/">Recipe &amp; plan primitives</a> — every field, status, assertion op, and cap</li>
  <li><a href="/docs/dogfood/">Publish this site</a> — host-local recipe in production</li>
  <li><a href="/docs/networking/">Networking</a> — seams recipes compose</li>
  <li><a href="/docs/resources/">Resources &amp; safety</a> — redaction and resume</li>
</ul>
`,
    mermaid: true,
  },

  "docs/recipe-primitives/index.html": {
    title: "Recipe & plan primitives",
    current: "recipe-primitives",
    body: `
<p class="badge">Reference</p>
<h1>Recipe &amp; plan primitives</h1>
<p class="meta">Dry field and status catalog for recipe envelopes and <code>host-plan.v1</code>. For <em>why</em> both exist, read <a href="/docs/recipes/">Recipes &amp; plans</a>.</p>

<h2>Plan vs recipe</h2>
<table>
  <thead><tr><th></th><th>Recipe</th><th>Plan</th></tr></thead>
  <tbody>
    <tr><td>Contract</td><td><code>host-recipe.v1</code>, <code>runtime-recipe.v1</code>, or <code>tunnel-recipe.v1</code></td><td><code>host-plan.v1</code></td></tr>
    <tr><td>Owns</td><td>Identity, inputs, compatibility, source pin, embedded plan</td><td>DAG of typed tool calls + readiness</td></tr>
    <tr><td>Runs?</td><td>No — expands into a plan</td><td>Yes — only via <code>plan.Runner</code></td></tr>
  </tbody>
</table>
<p>Nested <code>run_host_plan</code> / <code>run_*_recipe</code> inside a plan is forbidden. Providers may return recipe manifests; the Host Agent still validates and executes them.</p>

<pre class="mermaid">
flowchart LR
  Env["Recipe envelope<br/>*.v1"] --> Val[validate_*]
  Val --> Exp[Resolve inputs · hash · expand]
  Exp --> Plan["host-plan.v1"]
  Plan --> Run[plan.Runner]
  Run --> Tools[Typed tool calls]
  Tools --> State[Durable run state]
  State --> Get[get_*_run]
</pre>

<h2>Families &amp; MCP tools</h2>
<table>
  <thead><tr><th>Family</th><th>contractVersion</th><th>Validate</th><th>Run</th><th>Inspect</th><th>Extra envelope</th></tr></thead>
  <tbody>
    <tr>
      <td>Host-local</td>
      <td><code>host-recipe.v1</code></td>
      <td><code>validate_host_local_recipe</code></td>
      <td><code>run_host_local_recipe</code></td>
      <td><code>get_host_plan_run</code></td>
      <td><code>execution.coordinator</code> + <code>execution.mode</code></td>
    </tr>
    <tr>
      <td>Bare plan</td>
      <td><code>host-plan.v1</code></td>
      <td><code>validate_host_plan</code></td>
      <td><code>run_host_plan</code></td>
      <td><code>get_host_plan_run</code></td>
      <td>No recipe envelope</td>
    </tr>
    <tr>
      <td>Runtime</td>
      <td><code>runtime-recipe.v1</code></td>
      <td><code>validate_runtime_recipe</code></td>
      <td><code>run_runtime_recipe</code></td>
      <td><code>get_runtime_recipe_run</code></td>
      <td><code>runtime</code>, optional <code>activation</code></td>
    </tr>
    <tr>
      <td>Tunnel</td>
      <td><code>tunnel-recipe.v1</code></td>
      <td><code>validate_tunnel_recipe</code></td>
      <td><code>run_tunnel_recipe</code></td>
      <td><code>get_tunnel_run</code></td>
      <td><code>provider</code>, <code>bindings[]</code>, optional <code>activation</code></td>
    </tr>
  </tbody>
</table>
<p>Host-local inspect shares <code>get_host_plan_run</code> because expansion yields an ordinary plan run. Mutating <code>run_host_local_recipe</code> requires a content <code>sha256</code> pin.</p>

<h2>Shared recipe envelope</h2>
<table>
  <thead><tr><th>Field</th><th>Primitive</th></tr></thead>
  <tbody>
    <tr><td><code>contractVersion</code></td><td>Which family schema applies</td></tr>
    <tr><td><code>recipeId</code> / <code>recipeVersion</code></td><td>Stable identity + version string</td></tr>
    <tr><td><code>inputs</code></td><td>Map of <code>InputSpec</code>: <code>schema</code>, <code>default</code>, <code>required</code>, <code>secret</code>, <code>description</code></td></tr>
    <tr><td><code>compatibility</code></td><td><code>minHostAgentVersion</code>, <code>requiredTools[]</code></td></tr>
    <tr><td><code>plan</code></td><td>Embedded <code>host-plan.v1</code> (≥1 node)</td></tr>
    <tr><td><code>outputMapping</code></td><td>Named paths into node outputs for callers</td></tr>
  </tbody>
</table>

<h3>Source pin</h3>
<ul>
  <li>Kinds: local filesystem path (no symlink), <code>github:owner/repo/path@&lt;40-hex&gt;</code>, or raw GitHub URL at a commit.</li>
  <li>Request fields: <code>source</code>, optional <code>revision</code>, <code>sha256</code>. Mutations require <code>sha256</code>.</li>
  <li>Provenance records <code>kind</code>, <code>rawSha256</code>, <code>recipeHash</code>. Cap: 512&nbsp;KiB raw recipe.</li>
</ul>

<h2>Host-local vs Platform-distributed</h2>
<table>
  <thead><tr><th>execution</th><th>Who runs</th><th>Rules</th></tr></thead>
  <tbody>
    <tr>
      <td><code>coordinator: host-agent</code>, <code>mode: local</code></td>
      <td>Host Agent MCP tools</td>
      <td>No <code>wait</code> nodes; no raw <code>emits</code>; ≤1 distinct <code>target.hostRef</code>; targets optional (default this host) or exact <code>\${vars.inputs.*}</code></td>
    </tr>
    <tr>
      <td><code>coordinator: platform</code>, <code>mode: distributed</code></td>
      <td>Opute Platform</td>
      <td>Every action needs <code>target.hostRef</code> = <code>\${vars.inputs.*}</code>; HA <strong>refuses</strong> and redirects to Platform</td>
    </tr>
  </tbody>
</table>
<div class="note">Platform-distributed nodes are not evaluated by HA’s plan <code>validate</code> assertions the same way (ADR-0014). Do not rely on validate-only refusal for Platform-coordinated safety.</div>

<h2>Runtime &amp; tunnel extras</h2>
<h3>Runtime recipe</h3>
<ul>
  <li><code>runtime.id</code>, <code>runtime.servingContract</code>, optional <code>runtime.capabilities</code></li>
  <li>Serving contracts: <code>openai-chat.v1</code>, <code>http-exposure.v1</code>, <code>kubernetes.v1</code>, <code>mesh-runtime.v1</code>, <code>mesh-membership.v1</code>, <code>private-mesh.v1</code>, <code>public-ingress.v1</code> (plus deprecated <code>network-overlay.v1</code> alias)</li>
  <li>Optional <code>activation</code>: <code>capability</code>, <code>servingContract</code> (must match runtime), <code>inputBindings</code></li>
  <li><code>activate=true</code> on run only if activation is declared; success then commits an active runtime record</li>
</ul>
<h3>Tunnel recipe</h3>
<ul>
  <li><code>provider.id</code>, optional <code>provider.capabilities</code></li>
  <li><code>bindings[]</code>: <code>id</code>, <code>hostname</code>, <code>localTarget</code>, optional <code>path</code> (must fully resolve from inputs)</li>
  <li>Same optional <code>activation</code> pattern (typically <code>http-exposure.v1</code>)</li>
</ul>

<h2>Plan document header</h2>
<table>
  <thead><tr><th>Field</th><th>Primitive</th></tr></thead>
  <tbody>
    <tr><td><code>contractVersion</code></td><td>Must be <code>host-plan.v1</code></td></tr>
    <tr><td><code>planId</code></td><td>Stable plan identity</td></tr>
    <tr><td><code>generation</code></td><td>≥1; pairs with idempotency key for durable runs</td></tr>
    <tr><td><code>idempotencyKey</code></td><td>Required; recipes often interpolate <code>\${vars.inputs.*}</code></td></tr>
    <tr><td><code>catalogRevision</code></td><td>Optional pin against the live catalog</td></tr>
    <tr><td><code>variables</code></td><td>Declared vars; recipes inject inputs + reserved <code>tenantId</code></td></tr>
    <tr><td><code>defaults</code></td><td><code>timeoutMs</code>, <code>retry</code>, <code>maxPasses</code></td></tr>
    <tr><td><code>converge</code></td><td><code>maxPasses</code>, <code>abortOnExhaustion</code>, <code>maxConcurrency</code></td></tr>
    <tr><td><code>nodes[]</code></td><td>DAG; 1–256 nodes; document ≤512&nbsp;KiB</td></tr>
  </tbody>
</table>
<p>Idempotency identity is <code>(planId, generation, idempotencyKey)</code> plus document hash. In-flight/waiting reuses the existing run; terminal completed/failed starts a new run rather than replaying stale success.</p>

<pre class="mermaid">
flowchart TB
  H[planId · generation · idempotencyKey] --> Doc[host-plan.v1 document]
  Doc --> Levels[Topo levels]
  Levels --> Pass[Converge pass]
  Pass --> Level[Level fan-out ≤ maxConcurrency]
  Level --> Node[Node lifecycle]
  Node --> Sweep[Readiness sweep]
  Sweep -->|unstable| Pass
  Sweep -->|stable / exhaust| Terminal[completed · failed · waiting]
</pre>

<h2>Node primitives</h2>
<table>
  <thead><tr><th>Field</th><th>Meaning</th></tr></thead>
  <tbody>
    <tr><td><code>id</code></td><td>Unique node id</td></tr>
    <tr><td><code>dependsOn</code></td><td>DAG edges; cycles rejected</td></tr>
    <tr><td><code>when[]</code></td><td>Guard assertions; fail → <code>skipped</code></td></tr>
    <tr><td><code>target.hostRef</code> / <code>resourceRef</code></td><td>Execution binding (not a network address); checked against <code>HostAgentID</code></td></tr>
    <tr><td><code>action.tool</code> / <code>args</code></td><td>Tool call</td></tr>
    <tr><td><code>validate</code></td><td>Readiness: read-only tool + <code>assert[]</code> + optional <code>pollIntervalMs</code> / <code>timeoutMs</code>. <strong>Required on mutating actions</strong></td></tr>
    <tr><td><code>recover</code></td><td>Forward-fix then revalidate (<code>maxAttempts</code>)</td></tr>
    <tr><td><code>compensate</code></td><td>Reverse action on abort (reverse topo); statuses <code>compensated</code> / <code>compensation_failed</code></td></tr>
    <tr><td><code>retry</code></td><td><code>maxAttempts</code>, <code>backoffMs</code>, <code>backoffFactor</code>; non-idempotent mutations cannot auto-retry</td></tr>
    <tr><td><code>timeoutMs</code></td><td>Per-node wall clock</td></tr>
    <tr><td><code>continueOnFailure</code></td><td>Level continues after failure</td></tr>
    <tr><td><code>forEach</code></td><td>Fan-out: <code>source</code>, optional <code>path</code>, <code>as</code>, <code>filter[]</code>; needs <code>action</code>; ≤64 items</td></tr>
    <tr><td><code>wait</code></td><td>Durable barrier (XOR with action/validate/recover/compensate/forEach). <strong>Forbidden in host-local</strong></td></tr>
  </tbody>
</table>

<pre class="mermaid">
flowchart TB
  N{Node shape}
  N -->|action + validate| Mut[Mutating apply]
  N -->|validate only| Read[Readiness / observe]
  N -->|forEach + action| Fan[Fan-out ≤64]
  N -->|wait| Barrier[Durable wait — Platform]
  Mut --> Pre[when / preflight]
  Pre -->|already good| Sat[satisfied]
  Pre -->|needs work| Act[action → validate poll]
  Act -->|fail| Rec[recover?]
  Rec -->|abort| Comp[compensate reverse topo]
</pre>

<h3>Wait (Platform)</h3>
<p><code>trigger.kind</code> ∈ <code>operator</code> \| <code>event-or-operator</code>, plus <code>trigger.type</code>, <code>correlation</code>, bounded <code>inputSchema</code> (<code>additionalProperties: false</code>), <code>schemaRevision</code>, expiry via <code>expiresAt</code> XOR <code>expiresInMs</code>, and <code>contextDelta[]</code> (<code>name</code>, <code>\${input.*}</code> value, <code>schema</code>, <code>provenance</code>, <code>secret</code>). Resume fences on <code>waitRevision</code> / <code>schemaRevision</code> via MCP task input (<code>source</code> ∈ <code>operator</code> \| <code>authenticated-event</code>).</p>

<h2>Interpolation roots</h2>
<table>
  <thead><tr><th>Root</th><th>Where</th></tr></thead>
  <tbody>
    <tr><td><code>\${vars.*}</code></td><td>Plan variables + recipe inputs (+ reserved <code>tenantId</code>)</td></tr>
    <tr><td><code>\${nodes.&lt;id&gt;.output…}</code></td><td>Prior node outputs</td></tr>
    <tr><td><code>\${item.*}</code></td><td>Inside <code>forEach</code> only</td></tr>
    <tr><td><code>\${input.*}</code></td><td>Wait resume payload</td></tr>
    <tr><td><code>\${context.*}</code></td><td>Wait-produced context</td></tr>
  </tbody>
</table>

<h2>Assertions</h2>
<p>Used by <code>validate.assert</code>, <code>when</code>, and <code>forEach.filter</code>. <code>path</code> is an RFC&nbsp;6901 JSON Pointer.</p>
<p>Ops: <code>exists</code>, <code>notExists</code>, <code>empty</code>, <code>notEmpty</code>, <code>eq</code>, <code>ne</code>, <code>gt</code>, <code>gte</code>, <code>lt</code>, <code>lte</code>, <code>contains</code>, <code>matches</code>, <code>all</code>, <code>any</code>.</p>

<h2>Effects &amp; catalog coupling</h2>
<ul>
  <li>Catalog capabilities expose <code>Effect</code> (<code>read</code> vs mutation) and <code>Idempotent</code>.</li>
  <li>Validate tools must be read-only; mutating actions without <code>validate</code> are rejected.</li>
  <li>Retries require a read effect or an idempotent mutation.</li>
  <li>Optional <code>catalogRevision</code> pins the plan against the revision used at validate time.</li>
</ul>

<h2>Run &amp; node status</h2>
<table>
  <thead><tr><th>Scope</th><th>Values</th></tr></thead>
  <tbody>
    <tr><td>Run</td><td><code>working</code>/<code>running</code>, <code>waiting</code>, <code>expired</code>, <code>completed</code>, <code>failed</code>, <code>unknown</code> (cancel/timeout)</td></tr>
    <tr><td>Node</td><td><code>pending</code>, <code>skipped</code>, <code>satisfied</code>, <code>applied</code>, <code>failed</code>, <code>unknown</code>, <code>compensated</code>, <code>compensation_failed</code>, <code>waiting</code>, <code>expired</code></td></tr>
  </tbody>
</table>
<p><code>satisfied</code> means readiness already held (action skipped). <code>applied</code> means the action ran and validate passed. Per-node observations carry <code>attempts</code>, <code>output</code>, <code>observed</code>, <code>expected</code>, <code>error</code>, timestamps.</p>

<h2>Caps</h2>
<p>Document ≤512&nbsp;KiB · nodes ≤256 · forEach fan-out ≤64 · total attempts ≤256 · converge passes ≤32.</p>

<h2>Activation &amp; redaction</h2>
<p>Runtime/tunnel <code>activate=true</code> after success runs neutral serving validation, then records active runtime (<code>capability</code>, <code>servingContract</code>, recipe id/version/hash, bindings, observation). Networking seams used by recipes are documented under <a href="/docs/networking/">Networking</a>.</p>
<p>Write-only fields (for example Cloudflare connector run tokens) appear as <code>[redacted]</code> in MCP results. Resume cannot paste redacted secrets back — mint or hold them in-process (dogfood: <code>manageHostConnector: true</code>). See <a href="/docs/resources/">Resources &amp; safety</a>.</p>

<h2>Provider manifests</h2>
<p>Install manifests may list <code>Recipes[]</code> (<code>id</code>, <code>source.uri/revision/sha256</code>, optional <code>mode</code>/<code>inputs</code>). Teardown may return a <code>host-plan.v1</code> executed through the ordinary plan boundary — still no side-channel install path.</p>

<h2>Examples in-repo</h2>
<ul>
  <li>Host-local dogfood: <code>site/recipes/www-opute-io.yaml</code> — <a href="/docs/dogfood/">Publish this site</a></li>
  <li>Runtime: <code>plugins/llm/ollama/recipes/ollama.yaml</code></li>
  <li>Tunnel: <code>plugins/tunneling/cloudflare/recipes/</code></li>
  <li>Install host-recipe: <code>plugins/tunneling/tailscale/recipes/install.yaml</code></li>
</ul>

<div class="callout">
  Normative: Cordis C-03 (one executor), ADR-0002 (recipe ownership, immutable inputs, no recursion),
  ADR-0014 (Platform vs HA validate), ADR-0016 (networking seams as recipe composition).
</div>
`,
    mermaid: true,
  },

  "docs/networking/index.html": {
    title: "Networking",
    current: "networking",
    body: `
<p class="badge">Explanation</p>
<h1>Networking</h1>
<p class="meta">HA networking is three exclusive Service Definitions — not one mega “overlay” product. Tunneling and public MCP helpers sit beside those seams.</p>

<h2>Three seams (ADR-0016)</h2>
<table>
  <thead><tr><th>Service Definition</th><th>Job</th><th>Neutral ops</th></tr></thead>
  <tbody>
    <tr><td><code>mesh-membership.v1</code></td><td>Host joins a trust domain</td><td>enroll, status, leave</td></tr>
    <tr><td><code>private-mesh.v1</code></td><td>East-west reachability</td><td>ensure, ensure-service, probe</td></tr>
    <tr><td><code>public-ingress.v1</code></td><td>North-south stable HTTPS</td><td>ensure, promote, probe</td></tr>
  </tbody>
</table>
<p><code>network-overlay.*</code> tools in older catalogs are a <strong>deprecated migration alias</strong>. New consumers bind to the three definitions.</p>

<pre class="mermaid">
flowchart TB
  subgraph seams [Service Definitions]
    M[mesh-membership.v1]
    Pv[private-mesh.v1]
    Pu[public-ingress.v1]
  end
  TS[Provider: Tailscale<br/>all three]
  CF[Provider: Cloudflare<br/>membership + public-ingress]
  TS --> M
  TS --> Pv
  TS --> Pu
  CF --> M
  CF --> Pu
  HA[Host Agent recipes / MCP] --> seams
</pre>

<h2>Exclusivity</h2>
<p>Exclusivity is <strong>per Service Definition</strong>, not per vendor company. Activating a provider for a definition publishes that definition’s ops and displaces another provider’s catalog entries for the same <code>capabilityId</code>. Ambiguous dual ownership fails closed.</p>
<p>v1 vendor-bundle policy: activate one recommended bundle (Tailscale for all three, or Cloudflare where parity exists). Mix-and-match across seams is not a supported product path yet.</p>

<h2>Private vs public paths</h2>
<pre class="mermaid">
flowchart LR
  P[Platform] -->|typed MCP| A[Host Agent A]
  P -->|typed MCP| B[Host Agent B]
  A <-->|private mesh<br/>K3s API / CNI / probes| B
  Pub[Public client] --> Funnel[public-ingress / Funnel / CF]
  Funnel --> Gw[Scoped gateway / Ingress]
  Gw --> W[Workload]
</pre>
<p>Funnel and Cloudflare public ingress are application north-south paths. They are not substitutes for the private mesh, the Kubernetes datastore, or authenticated Host Agent MCP administration.</p>

<h2>Tunneling capability</h2>
<p><code>opute.capability.tunneling.*</code> owns dedicated host tunnels, probes, removal, and Kubernetes connector install. Dogfood for this site uses a dedicated tunnel <code>opute-www-opute-io</code> — never the Platform tunnel.</p>
<ul>
  <li>Stable public hostnames require DNS + TLS readiness probes before calling a surface “done.”</li>
  <li>Connector run tokens are write-only; MCP clients see <code>[redacted]</code>.</li>
  <li>Public MCP quick/stable tunnel helpers (<code>ensure_public_mcp_*</code>) are separate from Platform admin MCP.</li>
</ul>

<div class="note">Reachability of a marketing hostname is not two-node etcd-quorum HA. Keep those claims separate.</div>
`,
    mermaid: true,
  },

  "docs/resources/index.html": {
    title: "Resources & safety",
    current: "resources",
    body: `
<p class="badge">Explanation</p>
<h1>Resources &amp; safety</h1>
<p class="meta">Canonical resource URIs, typed edges, effect gates, quotas, and observation redaction — the fail-closed middle of every tool call.</p>

<h2>Canonical resource URIs</h2>
<p>Resources are identified as <code>type:tenant:id</code> (ADR-0003). Tools declare what they <strong>Require</strong> and <strong>Produce</strong> as typed edges (ADR-0004). Clients pass admitted URIs; the Host Agent does not invent cluster or guest identity from display labels.</p>

<pre class="mermaid">
flowchart LR
  Disc[Discovery / list tools] --> URI[Canonical URI]
  URI --> Admit[Admission]
  Admit --> Cap[Capacity / quota checks]
  Cap --> Op[Domain or provider op]
  Op --> Obs[Typed observation]
  Obs --> Red[Schema redaction]
  Red --> Client[MCP client]
</pre>

<h2>Effects</h2>
<p>Catalog entries carry effect classifications such as <code>read</code>, <code>mutation</code>, <code>destructive</code>, and <code>credential_bearing</code>. Standalone mode denies mutations until <code>OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code>. Platform mode applies enrollment policy instead of that env flag.</p>

<h2>Quotas and storage bounds</h2>
<ul>
  <li>Incus guest storage quotas are admission concerns (ADR-0010).</li>
  <li>In-cluster PVC / HA storage bounds are constrained so recipes cannot claim unbounded durability (ADR-0014).</li>
  <li>Reclaim tools (<code>prune_unused_cluster_images</code>, <code>garbage_collect_cluster_registry</code>, <code>trim_guest_storage</code>) are explicit ops — not silent background GC folklore.</li>
</ul>

<h2>Redaction (write-only)</h2>
<p>Schema-marked write-only fields never round-trip through MCP results. Tunnel connector tokens, for example, appear as <code>[redacted]</code>. In-process recipe steps that need those secrets must mint or hold them inside the Host Agent (see dogfood <code>manageHostConnector: true</code>), not ask an external client to paste them back.</p>

<pre class="mermaid">
flowchart LR
  Mint[ensure-host-tunnel] --> Tok[runToken writeOnly]
  Tok --> MCP[MCP result]
  MCP --> R["[redacted]"]
  Tok --> InProc[In-process connector install]
  InProc --> K8s[Kubernetes connector Secret]
</pre>

<h2>Identity</h2>
<p>Every Host Agent process carries <code>OPUTE_REMOTE_AGENT_ID</code>. Inventory tools omit caller-supplied host id overrides that would impersonate another agent (ADR-0006). Cross-host mutations target exact opaque agent identities — not Tailscale hostnames or display labels.</p>
`,
    mermaid: true,
  },
}

for (const [rel, spec] of Object.entries(pages)) {
  const full = join(root, rel)
  mkdirSync(join(full, ".."), { recursive: true })
  writeFileSync(full, page(spec))
  console.log("wrote", rel)
}

writeFileSync(
  join(root, "index.html"),
  `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Opute Host Agent</title>
  <meta name="description" content="Typed MCP host agent for guests, Kubernetes, and public exposure — dogfood-hosted on opute.io." />
  <link rel="stylesheet" href="${CSS}" />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=DM+Sans:ital,opsz,wght@0,9..40,400;0,9..40,600;0,9..40,700;1,9..40,400&family=Instrument+Serif:ital@0;1&display=swap" rel="stylesheet" />
</head>
<body>
  <header class="top">
    <a class="brand" href="/">Opute Host Agent</a>
    <nav>
      <a href="/docs/">Docs</a>
      <a href="/docs/get-started/">Get started</a>
    </nav>
  </header>

  <main class="hero">
    <p class="eyebrow">Host Agent</p>
    <h1>Run infrastructure through typed MCP — not shell folklore.</h1>
    <p class="lede">
      Opute Host Agent is a Streamable HTTP MCP server that executes explicit
      assignments: guests, Kubernetes, registries, and public exposure — with
      fail-closed identity and redacted observations.
    </p>
    <div class="cta">
      <a class="btn primary" href="/docs/get-started/">Get started</a>
      <a class="btn" href="/docs/architecture/">Architecture</a>
    </div>
    <div class="visual" aria-hidden="true">
      <pre class="terminal">$ export OPUTE_REMOTE_AGENT_ID=local-host-agent
$ export MCP_AUTH_TOKEN=dev-token
$ npx -y @opute/host-agent start --background
$ npx -y @opute/host-agent url
http://127.0.0.1:3014/mcp</pre>
    </div>
  </main>

  <footer>
    <span>Dogfood on <code>opute.io</code> / <code>www.opute.io</code></span>
    <a href="/docs/dogfood/">How this site is hosted</a>
  </footer>
</body>
</html>
`,
)
console.log("wrote index.html")

writeFileSync(
  join("/home/houman/github/wunderous/opute-host-agent/site/context", "PACKET.md"),
  `# Context packet — Host Agent docs / marketing site

## Documentation standard

Site docs follow [Diátaxis](https://diataxis.fr/). Operator facts must match
\`README.md\` and the live \`tools/list\` catalog after verifying against code.

### Writing rules (keep modes pure)

| Mode | Answers | Voice | Do not |
|------|---------|-------|--------|
| Tutorial | Can you teach me? | Guided steps only | Digress into architecture |
| How-to | How do I …? | Goal → steps | Teach from zero or dump schemas |
| Reference | What is …? | Dry, complete, neutral | Explain *why* or instruct |
| Explanation | Why / about …? | Mental model, analogy, judgment | Absorb field catalogs |

Progressive disclosure: one-sentence answer → diagram → choices → link to reference.
Explanation opens with *about* / *why*; reference opens with facts.

## Audience jobs

| Job | Route | Mode |
|-----|-------|------|
| First success | \`/docs/get-started/\` | Tutorial |
| Install & run | \`/docs/install/\` | How-to |
| Connect MCP client | \`/docs/mcp-clients/\` | How-to |
| Publish this site | \`/docs/dogfood/\` | How-to |
| Capability facts | \`/docs/capabilities/\` | Reference |
| Config facts | \`/docs/configuration/\` | Reference |
| Recipe & plan fields | \`/docs/recipe-primitives/\` | Reference |
| Mental model | \`/docs/concepts/\` | Explanation |
| Architecture + diagrams | \`/docs/architecture/\` | Explanation |
| Why recipes & plans | \`/docs/recipes/\` | Explanation |
| Networking seams | \`/docs/networking/\` | Explanation |
| URIs / admission / redaction | \`/docs/resources/\` | Explanation |

## Audited truths (2026-09-20)

- Standalone default: \`127.0.0.1:3014\`; platform default: \`0.0.0.0:3004\`
- \`OPUTE_REMOTE_AGENT_ID\` required; npm defaults to \`local-host-agent\`
- \`/mcp\` needs Bearer \`MCP_AUTH_TOKEN\` (or OAuth); \`/health\` is open
- Mutations denied until \`OPUTE_STANDALONE_ALLOW_MUTATIONS=true\`
- Live catalog capture: 178 tools in \`tools-list.redacted.json\`
- HA networking: three seams (ADR-0016); \`network-overlay.*\` deprecated alias
- Dogfood: dedicated tunnel \`opute-www-opute-io\`; hostnames \`opute.io\` + \`www.opute.io\`

## Boundaries

- Host Agent ≠ Platform (\`platform.opute.io\` / \`mcp.opute.io\`)
- Public site MUST NOT expose Host Agent MCP admin
- Deploy path = Host Agent recipe only
`,
)
console.log("wrote context/PACKET.md")
