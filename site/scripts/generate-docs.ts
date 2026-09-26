#!/usr/bin/env bun
/**
 * Emits static Diátaxis docs under site/public/docs from audited operator truth.
 * Capability reference data comes from an allowlisted standalone catalog export.
 * Architecture facts track README.md + docs/adr/* (verify before changing).
 */
import { mkdirSync, writeFileSync, readFileSync, readdirSync, existsSync } from "fs"
import { createHash } from "crypto"
import { dirname, join } from "path"
import { fileURLToPath } from "url"

const scriptDir = dirname(fileURLToPath(import.meta.url))
const siteDir = dirname(scriptDir)
const root = join(siteDir, "public")
const contextDir = join(siteDir, "context")

type PublicCatalogTool = {
  name: string
  title?: string
  description: string
  version?: number
  capabilityId?: string
  effect: string
  idempotent: boolean
  requiresApproval?: boolean
  inputSchema: Record<string, unknown>
  outputSchema?: Record<string, unknown>
}

type ReleaseCatalog = {
  packageName: string
  packageVersion: string
  releaseChannel: "preview" | "stable"
  catalogRevision: string
  toolCount: number
  tools: PublicCatalogTool[]
  publishedCanary?: {
    packageVersion: string
    catalogRevision: string
    sourceSha: string
    runId: number
    runAttempt: number
    checks: Record<string, boolean>
  }
}

type LocalHAProof = {
  schemaVersion: number
  id: string
  evidenceDate: string
  hostAgentRuntime: string
  hostAgentCatalogRevision: string
  providerId: string
  providerVersion: string
  k3sVersion: string
  datastoreMode: string
  guestKind: string
  serverCount: number
  readyBefore: number
  readyWhileOneGuestStopped: number
  readyAfter: number
  failedGuestCount: number
  physicalHostCount: number
  setupThroughHostAgent: boolean
  failureAndRecoveryThroughHostAgent: boolean
  typedWriteDuringFailure: boolean
  typedReadDuringFailure: boolean
  externalEndpointConfigured: boolean
  publishedPackageHaCanary: boolean
  verifiedOperations: string[]
  notEstablished: string[]
}

const releaseCatalog = JSON.parse(
  readFileSync(join(contextDir, "release-catalog.json"), "utf8"),
) as ReleaseCatalog
const localHAProof = JSON.parse(
  readFileSync(join(contextDir, "ha-proof.json"), "utf8"),
) as LocalHAProof
const allowedCatalogKeys = new Set([
  "packageName", "packageVersion", "releaseChannel", "catalogRevision", "toolCount", "tools", "publishedCanary",
])
const allowedCanaryKeys = new Set([
  "packageVersion", "catalogRevision", "sourceSha", "runId", "runAttempt", "checks",
])
const allowedHAProofKeys = new Set([
  "schemaVersion", "id", "evidenceDate", "hostAgentRuntime", "hostAgentCatalogRevision",
  "providerId", "providerVersion", "k3sVersion", "datastoreMode", "guestKind",
  "serverCount", "readyBefore", "readyWhileOneGuestStopped", "readyAfter", "failedGuestCount",
  "physicalHostCount", "setupThroughHostAgent", "failureAndRecoveryThroughHostAgent",
  "typedWriteDuringFailure", "typedReadDuringFailure", "externalEndpointConfigured",
  "publishedPackageHaCanary", "verifiedOperations", "notEstablished",
])
const requiredHAOperations = [
  "provision_vm",
  "run_host_local_recipe",
  "opute.capability.kubernetes.get-cluster-info",
  "stop_vm",
  "opute.capability.kubernetes.apply-manifest",
  "opute.capability.kubernetes.get-resource",
  "start_vm",
]
const requiredHALimitations = [
  "physical-host or site failure",
  "network partition",
  "application workload continued serving",
  "durable application data survived",
  "new workload scheduling during failure",
  "external endpoint failover",
  "the published npm package HA setup path",
]
if (
  Object.keys(localHAProof).some((key) => !allowedHAProofKeys.has(key)) ||
  localHAProof.schemaVersion !== 1 ||
  localHAProof.id !== "local-k3s-single-guest-loss-2026-09-25" ||
  !/^\d{4}-\d{2}-\d{2}$/.test(localHAProof.evidenceDate) ||
  localHAProof.hostAgentRuntime !== "dev" ||
  !/^sha256:[0-9a-f]{64}$/.test(localHAProof.hostAgentCatalogRevision) ||
  localHAProof.providerId !== "com.opute.k3s" ||
  !/^\d+\.\d+\.\d+$/.test(localHAProof.providerVersion) ||
  !/^v\d+\.\d+\.\d+\+k3s\d+$/.test(localHAProof.k3sVersion) ||
  localHAProof.datastoreMode !== "embedded-etcd" ||
  localHAProof.guestKind !== "Incus system container" ||
  localHAProof.serverCount !== 3 ||
  localHAProof.readyBefore !== 3 ||
  localHAProof.readyWhileOneGuestStopped !== 2 ||
  localHAProof.readyAfter !== 3 ||
  localHAProof.failedGuestCount !== 1 ||
  localHAProof.physicalHostCount !== 1 ||
  localHAProof.setupThroughHostAgent !== true ||
  localHAProof.failureAndRecoveryThroughHostAgent !== true ||
  localHAProof.typedWriteDuringFailure !== true ||
  localHAProof.typedReadDuringFailure !== true ||
  localHAProof.externalEndpointConfigured !== false ||
  localHAProof.publishedPackageHaCanary !== false ||
  !Array.isArray(localHAProof.verifiedOperations) ||
  requiredHAOperations.some((operation) => !localHAProof.verifiedOperations.includes(operation)) ||
  !Array.isArray(localHAProof.notEstablished) ||
  requiredHALimitations.some((limitation) => !localHAProof.notEstablished.includes(limitation))
) {
  throw new Error("site/context/ha-proof.json is incomplete, overbroad, or outside its evidence boundary")
}
const allowedToolKeys = new Set([
  "name", "title", "description", "version", "capabilityId", "effect", "idempotent", "requiresApproval", "inputSchema", "outputSchema",
])
const releaseArchiveDir = join(contextDir, "release-archives")
const archivedReleaseCatalogs: Array<{ filename: string; catalog: ReleaseCatalog }> = (existsSync(releaseArchiveDir)
  ? readdirSync(releaseArchiveDir)
      .filter((name) => /^v\d+\.\d+\.\d+\.json$/.test(name))
      .sort()
      .map((filename) => ({
        filename,
        catalog: JSON.parse(readFileSync(join(releaseArchiveDir, filename), "utf8")) as ReleaseCatalog,
      }))
  : [])
const archivedVersions = new Set<string>()
for (const { filename, catalog: archive } of archivedReleaseCatalogs) {
  const version = archive.packageVersion
  const evidence = archive.publishedCanary
  const requiredChecks = [
    "explicitIdentity",
    "openHealth",
    "invalidTokenRejected",
    "authenticatedDiscovery",
    "authenticatedToolsList",
    "structuredGetHostInfo",
    "readOnly",
  ]
  if (
    Object.keys(archive).some((key) => !allowedCatalogKeys.has(key)) ||
    archive.packageName !== "@opute/host-agent" ||
    !/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version) ||
    filename !== "v" + version + ".json" ||
    (version === releaseCatalog.packageVersion && releaseCatalog.releaseChannel === "stable") ||
    archivedVersions.has(version) ||
    archive.releaseChannel !== "stable" ||
    !/^sha256:[0-9a-f]{64}$/.test(archive.catalogRevision) ||
    !Number.isSafeInteger(archive.toolCount) ||
    !Array.isArray(archive.tools) ||
    archive.toolCount !== archive.tools.length ||
    !evidence ||
    Object.keys(evidence).some((key) => !allowedCanaryKeys.has(key)) ||
    evidence.packageVersion !== version ||
    evidence.catalogRevision !== archive.catalogRevision ||
    !/^[0-9a-f]{40}$/.test(evidence.sourceSha) ||
    !Number.isSafeInteger(evidence.runId) ||
    evidence.runId < 1 ||
    !Number.isSafeInteger(evidence.runAttempt) ||
    evidence.runAttempt < 1 ||
    requiredChecks.some((check) => evidence.checks?.[check] !== true) ||
    Object.keys(evidence.checks ?? {}).some((check) => !requiredChecks.includes(check))
  ) {
    throw new Error("archived release catalog is invalid or missing matching published read-only canary evidence")
  }
  const names = new Set<string>()
  const orderedNames: string[] = []
  for (const tool of archive.tools) {
    if (
      Object.keys(tool).some((key) => !allowedToolKeys.has(key)) ||
      !tool.name ||
      names.has(tool.name) ||
      !new Set(["read", "mutation", "destructive", "credential_bearing"]).has(tool.effect) ||
      tool.inputSchema?.type !== "object" ||
      typeof tool.description !== "string" ||
      typeof tool.idempotent !== "boolean" ||
      (tool.version !== undefined && (!Number.isSafeInteger(tool.version) || tool.version < 1)) ||
      (tool.requiresApproval !== undefined && typeof tool.requiresApproval !== "boolean")
    ) {
      throw new Error("archived release catalog contains an invalid or unapproved descriptor")
    }
    names.add(tool.name)
    orderedNames.push(tool.name)
  }
  if (!names.has("get_host_info")) {
    throw new Error("archived release catalog omits get_host_info")
  }
  if (orderedNames.some((name, index) => name !== [...orderedNames].sort()[index])) {
    throw new Error("archived release catalog descriptors are not in deterministic name order")
  }
  archivedVersions.add(version)
}

if (Object.keys(releaseCatalog).some((key) => !allowedCatalogKeys.has(key))) {
  throw new Error("release-catalog.json contains fields outside the public catalog contract")
}
if (releaseCatalog.packageName !== "@opute/host-agent") {
  throw new Error("release-catalog.json has an unexpected packageName")
}
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(releaseCatalog.packageVersion)) {
  throw new Error("release-catalog.json has an invalid packageVersion")
}
if (!new Set(["preview", "stable"]).has(releaseCatalog.releaseChannel)) {
  throw new Error("release-catalog.json has an invalid releaseChannel")
}
if (!/^sha256:[0-9a-f]{64}$/.test(releaseCatalog.catalogRevision)) {
  throw new Error("release-catalog.json has an invalid catalogRevision")
}
if (!Number.isSafeInteger(releaseCatalog.toolCount) || releaseCatalog.toolCount < 1 || !Array.isArray(releaseCatalog.tools)) {
  throw new Error("release-catalog.json has an invalid toolCount or tools list")
}
if (releaseCatalog.tools.length !== releaseCatalog.toolCount) {
  throw new Error("release-catalog.json toolCount does not match tools")
}
if (
  releaseCatalog.publishedCanary &&
  Object.keys(releaseCatalog.publishedCanary).some((key) => !allowedCanaryKeys.has(key))
) {
  throw new Error("release-catalog.json publishedCanary contains fields outside the evidence contract")
}
const catalogNames = new Set<string>()
for (const tool of releaseCatalog.tools) {
  if (Object.keys(tool).some((key) => !allowedToolKeys.has(key))) {
    throw new Error(`release-catalog.json descriptor ${tool.name} contains non-public fields`)
  }
  if (!tool.name || catalogNames.has(tool.name)) {
    throw new Error("release-catalog.json contains a missing or duplicate tool name")
  }
  catalogNames.add(tool.name)
  if (!new Set(["read", "mutation", "destructive", "credential_bearing"]).has(tool.effect)) {
    throw new Error(`release-catalog.json descriptor ${tool.name} has an unknown effect`)
  }
  if (tool.inputSchema?.type !== "object") {
    throw new Error(`release-catalog.json descriptor ${tool.name} has no object input schema`)
  }
  if (
    typeof tool.description !== "string" ||
    typeof tool.idempotent !== "boolean" ||
    (tool.version !== undefined && (!Number.isSafeInteger(tool.version) || tool.version < 1)) ||
    (tool.requiresApproval !== undefined && typeof tool.requiresApproval !== "boolean")
  ) {
    throw new Error("release-catalog.json descriptor " + tool.name + " has invalid public metadata")
  }
}
if (releaseCatalog.releaseChannel === "stable") {
  if (!/^\d+\.\d+\.\d+$/.test(releaseCatalog.packageVersion)) {
    throw new Error("stable release catalog must use a final semantic version")
  }
  const evidence = releaseCatalog.publishedCanary
  const requiredChecks = [
    "explicitIdentity",
    "openHealth",
    "invalidTokenRejected",
    "authenticatedDiscovery",
    "authenticatedToolsList",
    "structuredGetHostInfo",
    "readOnly",
  ]
  if (
    !evidence ||
    evidence.packageVersion !== releaseCatalog.packageVersion ||
    evidence.catalogRevision !== releaseCatalog.catalogRevision ||
    !/^[0-9a-f]{40}$/.test(evidence.sourceSha) ||
    !Number.isSafeInteger(evidence.runId) ||
    Number(evidence.runId) < 1 ||
    !Number.isSafeInteger(evidence.runAttempt) ||
    Number(evidence.runAttempt) < 1 ||
    requiredChecks.some((check) => evidence.checks?.[check] !== true) ||
    Object.keys(evidence.checks ?? {}).some((check) => !requiredChecks.includes(check))
  ) {
    throw new Error("stable release catalog is missing matching published read-only canary evidence")
  }
}

const compareReleaseVersions = (left: string, right: string) => {
  const leftParts = left.split(".").map(Number)
  const rightParts = right.split(".").map(Number)
  for (let index = 0; index < 3; index += 1) {
    const delta = (leftParts[index] ?? 0) - (rightParts[index] ?? 0)
    if (delta !== 0) return delta
  }
  return 0
}
const verifiedReleaseCatalogs = [
  ...archivedReleaseCatalogs.map(({ catalog }) => catalog),
  ...(releaseCatalog.releaseChannel === "stable" ? [releaseCatalog] : []),
].sort((left, right) => compareReleaseVersions(left.packageVersion, right.packageVersion))
const latestVerifiedCatalog = verifiedReleaseCatalogs.at(-1)
if (!latestVerifiedCatalog) {
  throw new Error("the public tutorial and canonical reference require a published stable catalog")
}
const tutorialCatalog = latestVerifiedCatalog
const catalogRouteFor = (catalog: ReleaseCatalog) =>
  "docs/" +
  (catalog.releaseChannel === "stable" ? "versions" : "previews") +
  "/v" +
  catalog.packageVersion +
  "/capabilities"
const currentCatalogRoute = catalogRouteFor(releaseCatalog)
const tutorialCatalogRoute = catalogRouteFor(tutorialCatalog)

type StaticAsset = "styles.css" | "search.js" | "i18n.js" | "docs-nav.js"

const assetURL = (asset: StaticAsset) => {
  // Normalize checkout line endings so WSL and Linux builds emit the same URL.
  const contents = readFileSync(join(root, asset), "utf8")
    .replaceAll("\r\n", "\n")
    .replaceAll("\r", "\n")
  const version = createHash("sha256").update(contents, "utf8").digest("hex")
  return `/${asset}?v=${version}`
}

const CSS = assetURL("styles.css")
const SITE_ORIGIN = "https://www.opute.io"

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

const SITE_SCRIPTS = `
<script src="${assetURL("search.js")}" defer></script>
<script src="${assetURL("i18n.js")}" defer></script>
<script src="${assetURL("docs-nav.js")}" defer></script>`

const escapeHTML = (value: string) =>
  value.replace(/[&<>"']/g, (character) => {
    switch (character) {
      case "&":
        return "&amp;"
      case "<":
        return "&lt;"
      case ">":
        return "&gt;"
      case '"':
        return "&quot;"
      case "'":
        return "&#39;"
      default:
        return character
    }
  })

const cloudflareProtectedPackageToken = (
  catalog: Pick<ReleaseCatalog, "packageName" | "packageVersion">,
) =>
  "<!--email_off-->" +
  escapeHTML(catalog.packageName + "@" + catalog.packageVersion) +
  "<!--/email_off-->"

const seoMeta = (title: string, description: string, canonicalUrl: string) => `<title>${escapeHTML(title)}</title>
  <meta name="description" content="${escapeHTML(description)}" />
  <link rel="canonical" href="${escapeHTML(canonicalUrl)}" />
  <meta property="og:type" content="website" />
  <meta property="og:site_name" content="Opute Host Agent" />
  <meta property="og:title" content="${escapeHTML(title)}" />
  <meta property="og:description" content="${escapeHTML(description)}" />
  <meta property="og:url" content="${escapeHTML(canonicalUrl)}" />
  <meta property="og:locale" content="en_US" />
  <meta property="og:image" content="https://www.opute.io/og-image.png" />
  <meta property="og:image:type" content="image/png" />
  <meta property="og:image:width" content="1200" />
  <meta property="og:image:height" content="630" />
  <meta property="og:image:alt" content="Opute Host Agent connects an authenticated MCP client to a Linux host." />
  <meta name="twitter:card" content="summary_large_image" />
  <meta name="twitter:image" content="https://www.opute.io/og-image.png" />
  <meta name="twitter:image:alt" content="Opute Host Agent connects an authenticated MCP client to a Linux host." />
  <meta name="twitter:title" content="${escapeHTML(title)}" />
  <meta name="twitter:description" content="${escapeHTML(description)}" />`

const nav = (current: string) => `
<header class="top">
  <a class="brand" href="/" lang="en">Opute Host Agent</a>
  <div class="top-tools">
    <div class="search">
      <input type="search" data-docs-search data-i18n-placeholder="search.placeholder" data-i18n-aria="nav.search" aria-label="Search documentation" placeholder="Search docs…" autocomplete="off" />
      <div class="search-results" data-docs-search-results role="region" aria-label="Search results" data-i18n-aria="search.results" aria-live="polite" aria-atomic="true" lang="en" hidden></div>
    </div>
    <div class="lang" role="group" aria-label="Language" data-i18n-aria="lang.label">
      <button type="button" data-lang-option="en" aria-pressed="true">EN</button>
      <button type="button" data-lang-option="fr" aria-pressed="false">FR</button>
    </div>
    <nav aria-label="Primary" data-i18n-aria="nav.primary">
      <a href="/docs/" data-i18n="nav.docs"${current === "docs" ? ' aria-current="page"' : ""}>Docs</a>
      <a href="/docs/get-started/" data-i18n="nav.getStarted">Get started</a>
      <a href="/docs/concepts/#host-agent-and-platform" lang="en">How Host Agent and Platform fit</a>
      <a href="https://platform.opute.io/" lang="en">Platform</a>
    </nav>
  </div>
</header>
<p class="i18n-banner" data-i18n-banner hidden></p>`

const side = (current: string) => `
<details class="doc-nav-toggle" open>
<summary data-i18n="nav.documentation">Documentation navigation</summary>
<aside class="doc-nav" aria-label="Documentation" lang="en">
  <h2>Tutorial</h2>
  <ul>
    <li><a href="/docs/get-started/"${current === "get-started" ? ' aria-current="page"' : ""}>Get started</a></li>
  </ul>
  <h2>How-to</h2>
  <ul>
    <li><a href="/docs/install/"${current === "install" ? ' aria-current="page"' : ""}>Install &amp; run</a></li>
    <li><a href="/docs/mcp-clients/"${current === "mcp-clients" ? ' aria-current="page"' : ""}>Connect an MCP client</a></li>
    <li><a href="/docs/dogfood/"${current === "dogfood" ? ' aria-current="page"' : ""}>Publish this site</a></li>
    <li><a href="/docs/troubleshooting/"${current === "troubleshooting" ? ' aria-current="page"' : ""}>Troubleshooting</a></li>
  </ul>
  <h2>Reference</h2>
  <p><a href="/${tutorialCatalogRoute}/">Latest verified catalog — v${tutorialCatalog.packageVersion}</a></p>
  ${releaseCatalog.releaseChannel === "preview" ? `<p><a href="/${currentCatalogRoute}/">Preview catalog — v${releaseCatalog.packageVersion}</a></p>` : ""}
  <ul>
    <li><a href="/docs/capabilities/"${current === "capabilities" ? ' aria-current="page"' : ""}>Capabilities</a></li>
    <li><a href="/docs/configuration/"${current === "configuration" ? ' aria-current="page"' : ""}>Configuration</a></li>
    <li><a href="/docs/recipe-primitives/"${current === "recipe-primitives" ? ' aria-current="page"' : ""}>Recipe &amp; plan primitives</a></li>
    <li><a href="/docs/openapi/"${current === "openapi" ? ' aria-current="page"' : ""}>OpenAPI</a></li>
  </ul>
  <h2>Compatibility</h2>
  <ul><li><a href="/docs/compatibility/"${current === "compatibility" ? ' aria-current="page"' : ""}>Compatibility and verification</a></li></ul>
  <h2>Explanation</h2>
  <ul>
    <li><a href="/docs/concepts/"${current === "concepts" ? ' aria-current="page"' : ""}>Concepts</a></li>
    <li><a href="/docs/architecture/"${current === "architecture" ? ' aria-current="page"' : ""}>Architecture</a></li>
    <li><a href="/docs/recipes/"${current === "recipes" ? ' aria-current="page"' : ""}>Recipes &amp; plans</a></li>
    <li><a href="/docs/availability/"${current === "availability" ? ' aria-current="page"' : ""}>Kubernetes availability</a></li>
    <li><a href="/docs/networking/"${current === "networking" ? ' aria-current="page"' : ""}>Networking</a></li>
    <li><a href="/docs/resources/"${current === "resources" ? ' aria-current="page"' : ""}>Resources &amp; safety</a></li>
    <li><a href="/use-cases/">Use cases</a></li>
  </ul>
</aside>
</details>`

const page = (
  opts: { title: string; description: string; current: string; body: string; mermaid?: boolean },
  canonicalPath: string,
) => `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  ${seoMeta(`${opts.title} — Opute Host Agent`, opts.description, `${SITE_ORIGIN}${canonicalPath}`)}
  <link rel="stylesheet" href="${CSS}" />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=DM+Sans:wght@400;600;700&family=Instrument+Serif&display=swap" rel="stylesheet" />
</head>
<body lang="en">
<a class="skip-link" href="#main-content" data-i18n="nav.skip">Skip to main content</a>
${nav(opts.current)}
<div class="doc-shell">
${side(opts.current)}
<main class="doc" id="main-content" lang="en">
${opts.body}
</main>
</div>
<footer>
  <span><span data-i18n="footer.facts">Facts track the repository</span> <a href="https://github.com/wunderous/host-agents/blob/main/README.md">README</a></span>
  <span><a href="/openapi.json" data-i18n="footer.openapi">OpenAPI</a> · <a href="/docs/architecture/" lang="en">Architecture</a></span>
</footer>
${opts.mermaid ? MERMAID : ""}
${SITE_SCRIPTS}
</body>
</html>
`

const versionedCapabilityPath = currentCatalogRoute + "/index.html"
const versionedCatalogDownload = "/" + currentCatalogRoute + "/catalog.json"
const tutorialCatalogPath = "/" + tutorialCatalogRoute + "/"
const tutorialCatalogDownload = tutorialCatalogPath + "catalog.json"
const tutorialStartCommand =
  tutorialCatalog.releaseChannel === "stable"
    ? `npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} start --background`
    : "make build VERSION=" +
      tutorialCatalog.packageVersion +
      "\n./dist/opute-host-agent serve --mode standalone --transport http"
const tutorialStopText =
  tutorialCatalog.releaseChannel === "stable"
    ? `Stop the background process with npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} stop.`
    : "Stop the foreground process with Ctrl+C in its terminal."
const tutorialPlatformPrerequisite =
  tutorialCatalog.releaseChannel === "stable"
    ? "Linux or WSL2, Node.js 18 or newer, npm, and VS Code with HTTP MCP support."
    : "Linux or WSL2, Go, and VS Code with HTTP MCP support."
const tutorialArtifactPrerequisite =
  tutorialCatalog.releaseChannel === "stable"
    ? `Network access to npm and the <a href="https://github.com/wunderous/host-agents/releases/tag/v${tutorialCatalog.packageVersion}">matching Linux binary in GitHub Releases</a>.`
    : "A Host Agent checkout and Go toolchain to build the preview."
const tutorialLaunchContext =
  tutorialCatalog.releaseChannel === "stable"
    ? "This starts the pinned published package shown on this page."
    : "Run source-build commands from the Host Agent repository root."
const tutorialReleaseNotice =
  tutorialCatalog.releaseChannel === "stable"
    ? '<p class="meta"><strong>Verified release.</strong> This path pins <code>' +
      cloudflareProtectedPackageToken(tutorialCatalog) +
      '</code>; its published authenticated read-only canary passed for catalog revision <code>' +
      escapeHTML(tutorialCatalog.catalogRevision) +
      '</code>. <a href="' + tutorialCatalogPath + '">View this exact capability snapshot</a> or <a href="' + tutorialCatalogDownload + '">download its JSON</a>.</p>'
    : '<div class="callout warn"><strong>Preview tutorial.</strong> The published-package canary has not passed for this candidate. Follow the local source-build path below; do not treat it as a verified published release.</div>'
const tutorialPortHelp =
  tutorialCatalog.releaseChannel === "stable"
    ? `Check the launcher with npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} status, stop it if needed, then choose an unused HOST_MCP_PORT and use that same port in curl and VS Code.`
    : "The preview process runs in the foreground. Read its terminal output, then set an unused HOST_MCP_PORT and use that same port in curl and VS Code."
const schemaDisclosure = (label: string, schema?: Record<string, unknown>) =>
  schema
    ? '<details class="schema-disclosure"><summary>' +
      label +
      "</summary><pre><code>" +
      escapeHTML(JSON.stringify(schema, null, 2)) +
      "</code></pre></details>"
    : ""

const capabilityCardsFor = (catalog: ReleaseCatalog) => {
  const toolGroups = new Map<string, PublicCatalogTool[]>()
  for (const tool of catalog.tools) {
    const group = tool.capabilityId || "Host operations"
    const groupTools = toolGroups.get(group) ?? []
    groupTools.push(tool)
    toolGroups.set(group, groupTools)
  }
  return [...toolGroups.entries()]
    .sort(([left], [right]) => left.localeCompare(right))
    .map(([group, groupTools]) => {
      const cards = groupTools
        .map((tool) => {
          const index = catalog.tools.indexOf(tool)
          const version = tool.version
            ? '<li>Capability version: <code>' + tool.version + "</code></li>"
            : ""
          const approval = tool.requiresApproval
            ? "<li>Requires an approval gate: <strong>yes</strong></li>"
            : ""
          return (
            '<details class="catalog-tool" id="catalog-tool-' +
            index +
            '"><summary><code>' +
            escapeHTML(tool.name) +
            '</code><span class="effect-tag">' +
            escapeHTML(tool.effect) +
            "</span></summary><p>" +
            escapeHTML(tool.description || "No description is published for this capability.") +
            '</p><ul class="catalog-facts">' +
            version +
            "<li>Idempotent: <strong>" +
            (tool.idempotent ? "yes" : "no") +
            "</strong></li>" +
            approval +
            "</ul>" +
            schemaDisclosure("Input schema", tool.inputSchema) +
            schemaDisclosure("Output schema", tool.outputSchema) +
            "</details>"
          )
        })
        .join("\n")
      return (
        '<section class="catalog-group"><h2>' +
        escapeHTML(group) +
        '</h2><div class="catalog-tools">' +
        cards +
        "</div></section>"
      )
    })
    .join("\n")
}

const catalogNoticeFor = (catalog: ReleaseCatalog, archived = false) =>
  archived
    ? '<p class="meta"><strong>Archived verified release.</strong> ' +
      cloudflareProtectedPackageToken(catalog) +
      " passed its published read-only package canary for catalog revision <code>" +
      escapeHTML(catalog.catalogRevision) +
      ".</code></p>"
    : catalog.releaseChannel === "stable"
      ? '<p class="meta">Published reference for <code>' +
        cloudflareProtectedPackageToken(catalog) +
        "</code>. Its read-only package canary passed for catalog revision <code>" +
        escapeHTML(catalog.catalogRevision) +
        ".</code></p>"
      : '<div class="callout warn"><strong>Preview catalog.</strong> This source candidate is <code>' +
        cloudflareProtectedPackageToken(catalog) +
        "</code> at revision <code>" +
        escapeHTML(catalog.catalogRevision) +
        "</code>. The published-package canary has not passed for this version, so this snapshot is not a verified release reference.</div>"

const capabilityReferenceBody = (
  catalog: ReleaseCatalog = latestVerifiedCatalog,
  versioned = false,
  archived = false,
) => {
  const downloadPath = "/" + catalogRouteFor(catalog) + "/catalog.json"
  const preview = catalog.releaseChannel === "preview"
  return [
    '<p class="badge">' + (preview ? "Preview reference" : versioned ? "Versioned reference" : "Reference") + "</p>",
    "<h1>" +
      (preview ? "Capabilities preview — v" + catalog.packageVersion : versioned ? "Capabilities — v" + catalog.packageVersion : "Capabilities") +
      "</h1>",
    catalogNoticeFor(catalog, archived),
    archived
      ? '<p><a href="/docs/capabilities/">Return to the current capability reference</a>.</p>'
      : archivedReleaseCatalogs.length > 0
        ? '<p>Archived release snapshots: ' +
          archivedReleaseCatalogs
            .map(({ catalog: archive }) => '<a href="/docs/versions/v' + archive.packageVersion + '/capabilities/">v' + archive.packageVersion + '</a>')
            .join(" · ") +
          ".</p>"
        : "",
    '<p class="meta">This standalone catalog contains ' +
      catalog.toolCount +
      " typed descriptors. Each entry shows its declared effect, idempotency, and available JSON schemas. A tool appearing here does not prove that its provider or required host service is ready.</p>",
    "<p>Catalog revision: <code>" +
      catalog.catalogRevision +
      "</code>. Download the allowlisted descriptor snapshot as <a href=\"" +
      downloadPath +
      '\">JSON</a>. At runtime, <code>tools/list</code> and <code>get_capability_catalog</code> remain authoritative; refresh them when a client connects.</p>',
    '<div class="callout"><strong>Ownership boundary.</strong> Host Agent executes explicit typed capabilities against one host. Opute Platform owns intent, authorization, and durable orchestration across hosts. See <a href="/docs/concepts/#host-agent-and-platform">how the products fit together</a>.</div>',
    capabilityCardsFor(catalog),
  ].join("\n")
}

const pages: Record<string, { title: string; description: string; current: string; body: string; mermaid?: boolean }> = {
  "docs/install/index.html": {
    title: "Install and run",
    description: "Build or launch Opute Host Agent locally with explicit identity, authentication, and safe loopback defaults.",
    current: "install",
    body: `
<p class="badge">How-to</p>
<h1>Install &amp; run</h1>
<p class="meta">Choose a local source build or the published npm launcher. For a tested end-to-end setup, begin with the <a href="/docs/get-started/">first-success tutorial</a>.</p>

<h2>From source</h2>
<p>From the Host Agent repository root, build and start the standalone HTTP server. Host inspection does not require Incus or Kubernetes.</p>
<pre><code>make build VERSION=${releaseCatalog.packageVersion}
export OPUTE_REMOTE_AGENT_ID="local-$(openssl rand -hex 8)"
export MCP_AUTH_TOKEN="$(openssl rand -hex 32)"
./dist/opute-host-agent serve --mode standalone --transport http</code></pre>
<p>Keep both values in your local shell. The agent ID is an explicit opaque identity; the launcher does not invent a default. The token authenticates MCP requests.</p>

<h2>Published npm launcher</h2>
<pre><code>export OPUTE_REMOTE_AGENT_ID="local-$(openssl rand -hex 8)"
export MCP_AUTH_TOKEN="$(openssl rand -hex 32)"
npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} start --background
npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} url
npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} status
npx -y ${cloudflareProtectedPackageToken(tutorialCatalog)} stop</code></pre>
<p>This pins the newest published release with a passing read-only package canary. The launcher keeps the explicit identity and token in the child process. Stop the background process when you finish. The <a href="/docs/compatibility/">compatibility page</a> records verified combinations.</p>
${releaseCatalog.releaseChannel === "preview" ? `<p class="meta"><strong>Current source candidate:</strong> <code>${cloudflareProtectedPackageToken(releaseCatalog)}</code> is a preview until its published-package canary passes. The first-success and published launcher instructions above remain pinned to <code>${cloudflareProtectedPackageToken(tutorialCatalog)}</code>.</p>` : ""}

<h2>Local endpoint and modes</h2>
<table>
  <thead><tr><th>Mode</th><th>Default address</th><th>Purpose</th></tr></thead>
  <tbody>
    <tr><td><code>standalone</code></td><td><code>127.0.0.1:3014</code></td><td>Local editor or laptop</td></tr>
    <tr><td><code>platform</code></td><td><code>0.0.0.0:3004</code></td><td>Platform-enrolled host</td></tr>
  </tbody>
</table>
<p>Set <code>HOST_MCP_BIND_HOST</code> and <code>HOST_MCP_PORT</code> when needed. Keep a local tutorial on loopback and use the same port in the agent URL and client configuration.</p>

<h2>Mutations</h2>
<p>Standalone mutations are denied by default. This first-success path stays read-only. Only enable <code>OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code> after reviewing the capability effects, approval requirements, and host-local consequences.</p>

<h2>WSL and production</h2>
<p>For a first client connection, run the Linux agent and VS Code integration in the same WSL environment. Verify reachability before configuring a client across Windows and WSL.</p>
<p>Production hosts are connected through Opute Platform's <strong>Connect Remote Host</strong> flow. Platform owns enrollment and authorization; use the generated host configuration for that host rather than copying local tutorial credentials.</p>

<p>Related: <a href="/docs/get-started/">First success</a> · <a href="/docs/mcp-clients/">Client configuration</a> · <a href="/docs/troubleshooting/">Troubleshooting</a> · <a href="/docs/configuration/">Configuration</a></p>
`,
  },
  "docs/troubleshooting/index.html": {
    title: "Troubleshooting",
    description: "Diagnose Host Agent authentication, reachability, identity, discovery, and optional-provider problems.",
    current: "troubleshooting",
    body: `
<p class="badge">How-to</p>
<h1>Troubleshooting</h1>
<p class="meta">Use the symptom to identify whether the server is live, the client is authenticated, and the requested provider is available.</p>

<h2>HTTP 401 from <code>/mcp</code></h2>
<ol>
  <li>Confirm <code>MCP_AUTH_TOKEN</code> is set in the Host Agent process environment.</li>
  <li>Confirm the client sends <code>Authorization: Bearer</code> with that same token.</li>
  <li>Use the local check in the <a href="/docs/get-started/">first-success tutorial</a> to verify that a wrong token returns 401 and the correct token can call <code>get_host_info</code>.</li>
</ol>
<p><code>GET /health</code> is intentionally open. A successful health response proves liveness, not that MCP authentication works.</p>

<h2>Connection refused or the wrong port</h2>
<ul>
  <li>Standalone defaults to <code>http://127.0.0.1:3014/mcp</code>.</li>
  <li>Use the same value for <code>HOST_MCP_PORT</code>, the health URL, and the client's MCP URL.</li>
  <li>Keep <code>HOST_MCP_BIND_HOST=127.0.0.1</code> for a local client. Check that the port is free and the server is still running.</li>
  <li>For Windows/WSL, first verify the client can reach an agent running in the same WSL distribution.</li>
</ul>

<h2>Missing <code>OPUTE_REMOTE_AGENT_ID</code></h2>
<p>Set one explicit opaque ID before starting the process. The npm launcher fails before binary download or listener startup when it is absent; it does not default to a shared name. Do not use the local daemon ownership ID as the Host Agent identity.</p>

<h2>The client connects but the tool list is empty or stale</h2>
<ul>
  <li>Refresh the client's MCP tools after connecting. The live <code>tools/list</code> response is authoritative.</li>
  <li>Check that the client is connected to the same host and port where <code>/health</code> reports the expected <code>agentId</code>.</li>
  <li>Review the current <a href="/docs/capabilities/">capability reference</a>; provider readiness varies by host.</li>
</ul>

<h2>An optional provider tool is unavailable</h2>
<p>Host facts do not require Incus or Kubernetes. VM inventory requires an Incus provider; cluster discovery requires the relevant Kubernetes provider and host access. A listed tool does not prove the provider service is ready.</p>

<h2>Mutation denied</h2>
<p>Standalone mode denies mutating tools unless <code>OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code> is explicitly enabled. Keep it disabled during the first-success tutorial. Platform-enrolled hosts use Platform authorization policy.</p>

<h2>Launcher status does not match the health response</h2>
<p>The launcher uses <code>localInstanceId</code> to identify its managed local process. <code>agentId</code> is the canonical Host Agent identity and <code>instanceId</code> describes the execution mode; these fields have separate meanings.</p>

<p>Related: <a href="/docs/install/">Install and run</a> · <a href="/docs/mcp-clients/">Client setup</a> · <a href="/docs/configuration/">Configuration</a> · <a href="/docs/resources/">Resources &amp; safety</a></p>
`,
  },
  "docs/dogfood/index.html": {
    title: "How this site is hosted",
    description: "Understand the public image build and the private Host Agent deployment controller that serves Opute's website.",
    current: "dogfood",
    body: `
<p class="badge">How-to</p>
<h1>How this site is hosted</h1>
<p class="meta">The public repository builds a static-site image. A private deployment controller selects that image and asks the local Host Agent to deploy it through typed capabilities.</p>

<h2>Ownership boundary</h2>
<ul>
  <li><a href="https://github.com/wunderous/host-agents">Public Host Agent source</a> owns the website, docs generator, and GitHub-hosted image build. Its workflow publishes an immutable image digest without Host Agent deployment credentials.</li>
  <li><a href="https://github.com/wunderous/opute-site-deploy">Private site deployment</a> owns source-run selection, the controller, deployment recipe, and private rollout evidence. This repository is required to operate or change production deployment.</li>
  <li>Opute Platform and its MCP endpoint remain separate services with their own owners and route checks.</li>
</ul>

<h2>Delivery path</h2>
<pre class="mermaid">
flowchart LR
  Source[Public website source] --> Build[Public image build]
  Build -->|verified run and digest| Registry[Public image registry]
  Controller[Private deployment controller] -->|select successful source run| Registry
  Controller --> Recipe[Typed Host Agent recipe]
  Recipe --> Agent[Local Host Agent]
  Agent --> Workload[Website workload]
  Workload --> Routes[www and apex routes]
  Browser[Visitor] --> Routes
</pre>
<p class="diagram-alt"><strong>Diagram in words:</strong> Public CI builds and publishes the website image. A private controller verifies and selects that image, then uses a typed Host Agent recipe to update the website workload behind the two public website routes.</p>

<h2>What deployment evidence means</h2>
<ul>
  <li>The private recipe finishes successfully and the serving workload uses the selected image digest with a Ready Pod.</li>
  <li>Both <code>www.opute.io</code> and <code>opute.io</code> serve rendered pages and a <code>/build.json</code> marker matching the selected source SHA, run ID, and attempt.</li>
  <li>Critical docs routes, static assets, and search load and return useful content; HTTP 200 by itself does not prove that the client onboarding path works.</li>
  <li><code>platform.opute.io</code> and authenticated <code>mcp.opute.io</code> are checked separately and remain outside this site's deployment boundary.</li>
</ul>
<p>These checks establish evidence for a particular rollout. They do not create an uptime, high availability, or successful client-onboarding guarantee.</p>

<h2>Rollback and credentials</h2>
<p>Rollback uses the private controller's explicit selection of a previous successful public source run and repeats the deployment and route gates. Public site workflows do not contain deployment credentials; Host Agent and tunnel credentials remain in their owning runtime.</p>

<p>See the private <a href="https://github.com/wunderous/opute-site-deploy">deployment repository</a> for current operating instructions. This public page describes ownership and evidence, not a direct infrastructure procedure.</p>
`,
    mermaid: true,
  },
  "docs/index.html": {
    title: "Documentation",
    description: "Opute Host Agent docs: install the Linux MCP server, connect a client, browse its tools, and understand how it runs.",
    current: "docs",
    body: `
<p class="badge">Diátaxis</p>
<h1>Documentation</h1>
<p class="meta"><strong>Opute Host Agent</strong> gives an authenticated MCP client a read-only way to inspect Linux host facts and discover the capabilities available on that host. It executes explicit typed operations; Opute Platform owns intent, authorization, and durable orchestration across hosts.</p>
<p class="meta"><strong>For.</strong> Infrastructure operators and agent authors connecting a client to Linux or WSL2. Incus and Kubernetes are optional for the first host-facts check. Need cross-host coordination? <a href="https://platform.opute.io/">Visit Opute Platform</a>.</p>
<p class="meta">Choose a task below. These pages follow <a href="https://diataxis.fr/">Diátaxis</a>. This site is Host Agent dogfood; its current public availability is not an uptime claim.</p>

<div class="doc-index-sections task-start">
  <section><h2>Start</h2><ul><li><a href="/docs/get-started/"><strong>Complete a read-only first check</strong><span>Follow one VS Code path from launch through authenticated get_host_info.</span></a></li></ul></section>
  <section><h2>Connect</h2><ul><li><a href="/docs/mcp-clients/"><strong>Configure an MCP client</strong><span>Use an HTTP endpoint and a password-prompted Bearer token.</span></a></li></ul></section>
  <section><h2>Troubleshoot</h2><ul><li><a href="/docs/troubleshooting/"><strong>Find a symptom</strong><span>Check authentication, reachability, discovery, and provider availability.</span></a></li></ul></section>
</div>

<div class="doc-index-sections">
  <section>
    <h2>Tutorial</h2>
    <ul>
      <li><a href="/docs/get-started/"><strong>Get started</strong><span>Start a local agent and complete first authenticated discovery.</span></a></li>
    </ul>
  </section>
  <section>
    <h2>How-to</h2>
    <ul>
      <li><a href="/docs/install/"><strong>Install &amp; run</strong><span>From-source, npm launcher, serve modes, mutations, WSL.</span></a></li>
      <li><a href="/docs/mcp-clients/"><strong>Connect an MCP client</strong><span>VS Code HTTP setup, with other client paths labelled by verification status.</span></a></li>
      <li><a href="/docs/dogfood/"><strong>Publish this site</strong><span>Public image build; private Host Agent deployment controller.</span></a></li>
      <li><a href="/docs/troubleshooting/"><strong>Troubleshooting</strong><span>401s, mutations denied, wrong port, redacted resume, distributed refused.</span></a></li>
    </ul>
  </section>
  <section>
    <h2>Reference</h2>
    <ul>
      <li><a href="/docs/capabilities/"><strong>Capabilities</strong><span>${latestVerifiedCatalog.toolCount} descriptors from the newest verified catalog; provider readiness varies by host.</span></a></li>
      <li><a href="/docs/configuration/"><strong>Configuration</strong><span>Ports, bind hosts, required identity, auth, Cloudflare env.</span></a></li>
      <li><a href="/docs/recipe-primitives/"><strong>Recipe &amp; plan primitives</strong><span>Fields, statuses, assertion ops, caps — dry facts.</span></a></li>
      <li><a href="/docs/openapi/"><strong>OpenAPI</strong><span>HTTP surface for <code>/health</code> and Streamable HTTP <code>/mcp</code>.</span></a></li>
    </ul>
  </section>
  <section>
    <h2>Compatibility</h2>
    <ul><li><a href="/docs/compatibility/"><strong>Compatibility</strong><span>Separate documented configuration from combinations exercised end to end.</span></a></li></ul>
  </section>
  <section>
    <h2>Use cases</h2>
    <ul><li><a href="/use-cases/"><strong>Explore host inspection jobs</strong><span>See read-only workflows and the provider conditions they require.</span></a></li></ul>
  </section>
  <section>
    <h2>Explanation</h2>
    <ul>
      <li><a href="/docs/concepts/"><strong>Concepts</strong><span>Host Agent vs Platform, catalog authority, fail-closed identity.</span></a></li>
      <li><a href="/docs/architecture/"><strong>Architecture</strong><span>Planes, providers, Cordis kernel — with diagrams.</span></a></li>
      <li><a href="/docs/recipes/"><strong>Recipes &amp; plans</strong><span>Why both exist; when to use which family.</span></a></li>
      <li><a href="/docs/availability/"><strong>Kubernetes availability</strong><span>Quorum, failure scope, and what an HA test must prove.</span></a></li>
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
    description: "Start Opute Host Agent on Linux and verify authenticated MCP tool discovery.",
    current: "get-started",
    body: `<p class="badge">Tutorial</p>
<h1>Get started</h1>
<p class="meta"><strong>Outcome.</strong> Start Host Agent locally, connect VS Code over authenticated HTTP, refresh the live tool list, and read host facts with <code>get_host_info {}</code>. This tutorial does not enable mutations.</p>
${tutorialReleaseNotice}
<h2>Prerequisites</h2>
<ul><li>${tutorialPlatformPrerequisite}</li><li><code>curl</code> and <code>openssl</code>; loopback port <code>3014</code> must be free.</li><li>${tutorialArtifactPrerequisite}</li></ul>
<h2>Start the local agent</h2>
<ol class="steps">
<li><strong>Set an explicit identity and random secret</strong><p>Keep the token in your shell and VS Code prompt. Do not commit it or put it in screenshots.</p><pre><code>export OPUTE_REMOTE_AGENT_ID="local-$(openssl rand -hex 8)"
export MCP_AUTH_TOKEN="$(openssl rand -hex 32)"
${tutorialStartCommand}</code></pre><p>${tutorialLaunchContext} ${tutorialStopText}</p><pre><code>curl -i -sS http://127.0.0.1:3014/health</code></pre><p>Expect HTTP 200 and the exact <code>agentId</code> you set. This open endpoint proves liveness only, not MCP authentication.</p></li>
<li><strong>Connect VS Code</strong><p>Create <code>.vscode/mcp.json</code>. VS Code documents this HTTP server and password-prompt setup in its <a href="https://code.visualstudio.com/docs/agents/reference/mcp-configuration">MCP configuration reference</a>.</p><pre><code>{
  "inputs": [{"type":"promptString","id":"opute-host-token","description":"Local Opute Host Agent token","password":true}],
  "servers": {"opute-host-agent":{"type":"http","url":"http://127.0.0.1:3014/mcp","headers":{"Authorization":"Bearer \${input:opute-host-token}"}}}
}</code></pre><p>Start the server and enter the same shell token. Avoid committing the config if the client might persist the entered value.</p></li>
<li><strong>Refresh tools and read the host</strong><p>Confirm the server is connected, refresh its tools, and call <code>get_host_info</code> with <code>{}</code>. Its schema requires these host facts:</p><pre><code>{
  "uri": "host:example:host-01",
  "hostName": "…",
  "providerId": "…",
  "lxcBinaryPath": "…",
  "systemctlPath": "…",
  "supportedTools": ["…"]
}</code></pre><p>Those six fields are required by the current schema. Optional fields such as <code>agent</code>, <code>capacity</code>, and <code>system</code> appear only when observable. Values are redacted; this is an example shape, not captured host output. A listed capability does not prove its provider is ready.</p></li>
</ol>
<div class="callout warn"><strong>Keep this first check read-only.</strong> Do not set <code>OPUTE_STANDALONE_ALLOW_MUTATIONS=true</code> or call a tool whose declared effect is not <code>read</code>.</div>
<h2>Verify the authenticated path</h2>
<p>Check that authentication rejects a deliberately wrong token before completing the valid call in VS Code. This request uses the Host Agent's stateless MCP protocol version and matching request metadata:</p>
<pre><code>curl -i -sS http://127.0.0.1:3014/mcp -H 'Accept: application/json, text/event-stream' -H 'Content-Type: application/json' -H 'Authorization: Bearer intentionally-wrong' -H 'MCP-Protocol-Version: 2026-07-28' -H 'Mcp-Method: tools/list' --data '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"curl","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}'</code></pre>
<p>Expect HTTP 401. A request with no Authorization header also returns 401. Use the real token only in your local shell and VS Code password prompt.</p>
<ul><li>No token or a wrong token gets HTTP 401.</li><li>The configured token connects VS Code; non-empty <code>tools/list</code> includes <code>get_host_info</code>.</li><li><code>get_host_info {}</code> returns structured facts and the selected identity.</li><li>No mutation flag was enabled and no mutating tool was called.</li></ul>
<h2>Troubleshoot</h2>
<ul><li><strong>Port in use:</strong> ${tutorialPortHelp}</li><li><strong>401:</strong> match the VS Code input to <code>MCP_AUTH_TOKEN</code>; restart after changing shell environment.</li><li><strong>Empty tools:</strong> refresh the server, verify the URL ends in <code>/mcp</code>, and check <code>tools/list</code>.</li><li><strong>WSL reachability:</strong> run VS Code and the agent in the same WSL context first.</li><li>For other symptoms, see <a href="/docs/troubleshooting/">troubleshooting</a>.</li></ul>
<h2>Next</h2>
<p>Inspect <code>list_vms</code> only if Incus is configured. Otherwise read the <a href="/docs/capabilities/">capability reference</a> and <a href="/docs/concepts/#host-agent-and-platform">product boundary</a>. Keep the mutation gate closed.</p>
`,
  },

  "docs/mcp-clients/index.html": {
    title: "Connect an MCP client",
    description: "Configure VS Code to connect to a local Opute Host Agent over authenticated Streamable HTTP.",
    current: "mcp-clients",
    body: `
<p class="badge">How-to</p>
<h1>Connect an MCP client</h1>
<p class="meta">Connect VS Code to a Host Agent running in the same Linux or WSL context. The documented path uses Streamable HTTP and a password-prompted Bearer token.</p>
<h2>VS Code</h2>
<ol>
  <li>Start Host Agent on loopback and retain <code>MCP_AUTH_TOKEN</code> in the same shell.</li>
  <li>Create a workspace <code>.vscode/mcp.json</code> with an HTTP MCP server and a password input:</li>
</ol>
<pre><code>{
  "inputs": [{"type":"promptString","id":"opute-host-token","description":"Local Opute Host Agent token","password":true}],
  "servers": {"opute-host-agent":{"type":"http","url":"http://127.0.0.1:3014/mcp","headers":{"Authorization":"Bearer \${input:opute-host-token}"}}}
}</code></pre>
<ol start="3">
  <li>Start the server from VS Code and enter the local token at its prompt.</li>
  <li>Refresh tools and call <code>get_host_info {}</code> to confirm the authenticated path.</li>
</ol>
<p>See the official <a href="https://code.visualstudio.com/docs/agents/reference/mcp-configuration">VS Code MCP configuration reference</a>. This site has not yet recorded a human client run for a specific VS Code release; see <a href="/docs/compatibility/">compatibility evidence</a>.</p>
<h2>WSL and local networking</h2>
<p>Keep the editor MCP client and Host Agent in a networking context where <code>127.0.0.1:3014</code> reaches the agent. Start with both in the same WSL distribution. If VS Code runs on Windows while the agent runs in WSL, follow the editor's current WSL integration guidance and verify connectivity before configuring MCP.</p>
<h2>Other clients</h2>
<p>Other clients must support Streamable HTTP MCP and the required Bearer header for this setup. Client-specific recipes are not published as tested until a named version completes the read-only canary.</p>
<p>Next: <a href="/docs/get-started/">complete the full first-success tutorial</a>.</p>
`,
  },

  "docs/compatibility/index.html": {
    title: "Compatibility",
    description: "See Host Agent package targets and distinguish documented MCP client setup from tested client combinations.",
    current: "compatibility",
    body: `
<p class="badge">Reference</p>
<h1>Compatibility and verification</h1>
<p class="meta">This page records the evidence available for each compatibility claim. A configuration example documents a supported setup shape; it does not mean a named client/version has passed a live end-to-end test.</p>
<h2>Host Agent package</h2>
<ul>
  <li>Launcher runtime: Node.js 18 or newer.</li>
  <li>Packaged Host Agent binaries: Linux x64 and Linux arm64.</li>
  <li>On Windows, run the Linux binary in WSL and keep the editor and agent in a reachable networking context.</li>
  <li>Transport: authenticated Streamable HTTP at <code>/mcp</code>; public Host Agent does not support stdio.</li>
</ul>
<h2>Client evidence</h2>
<table><thead><tr><th>Client path</th><th>Evidence</th><th>State</th></tr></thead><tbody>
<tr><td>VS Code MCP configuration</td><td>Official editor documentation describes HTTP servers and password-prompt inputs. This site provides the corresponding configuration.</td><td>Documented; local human end-to-end run still required before claiming tested.</td></tr>
<tr><td>Other MCP clients</td><td>No client/version evidence is recorded here.</td><td>Unverified for this release. Follow that client vendor's HTTP, authentication, and local-network guidance.</td></tr>
</tbody></table>
<p>When a client combination is exercised, record client and version, OS/runtime, transport, auth method, package version, catalog revision, and the read-only result. Redact tokens, host identity, and private host facts.</p>
<p>Related: <a href="/docs/get-started/">First success</a> · <a href="/docs/mcp-clients/">Client configuration</a> · <a href="/docs/troubleshooting/">Troubleshooting</a></p>
`,
  },

  "use-cases/index.html": {
    title: "Use cases",
    description: "See how Opute Host Agent configures K3s clusters, verifies membership, and exposes read-only host facts.",
    current: "use-cases",
    body: `
<p class="badge">Use cases</p>
<h1>Configure, verify, then inspect</h1>
<p class="meta">Host Agent exposes typed operations through authenticated MCP. Available workflows depend on the live catalog and configured providers. Read the current catalog before acting; it is a capability description, not proof that a provider is ready.</p>
<h2>Understand the connected host</h2>
<p>Call <code>get_host_info {}</code> to read the host URI, hostname, provider identity, and supported tools. Use this result to confirm which Host Agent answered before asking for a resource inventory.</p>
<h2>Discover operations available now</h2>
<p>Call <code>get_capability_catalog {}</code> and inspect its revision and declared effect. Refresh <code>tools/list</code> after connecting; live discovery is authoritative for that process.</p>
<h2>Inspect VM inventory when Incus is configured</h2>
<p>Call <code>list_vms</code> only on a host with its Incus provider available. This is inventory discovery; creating, changing, or deleting a guest is a separate mutating operation and is outside the first-success tutorial.</p>
<h2>Discover Kubernetes clusters when configured</h2>
<p>Use <code>list_kubernetes_clusters</code> only when the host has an available Kubernetes provider and the intended source is known. An empty inventory and a failed inventory probe are different outcomes; read the structured response and error status.</p>
<h2>Plan cluster setup and availability</h2>
<p>Host Agent can provision and configure K3s server membership through typed operations. A local test used ${localHAProof.serverCount} Incus server containers with ${localHAProof.datastoreMode}; after one guest stopped, ${localHAProof.readyWhileOneGuestStopped} of ${localHAProof.serverCount} Kubernetes nodes remained Ready and a typed API write and read succeeded. Those guests shared one physical host. See <a href="/docs/availability/#local-host-agent-test">the test record and its exact scope</a>.</p>
<div class="callout"><strong>Product boundary.</strong> Host Agent executes explicit typed operations against one host. Platform owns intent, authorization, and durable orchestration across hosts. Read <a href="/docs/concepts/#host-agent-and-platform">how they fit together</a>.</div>
<p>This is local dogfood evidence, not a customer outcome, performance result, or availability guarantee. It does not establish physical-host failover, application serving, durable application data, or external endpoint failover.</p>
<p>Next: <a href="/docs/get-started/">complete the authenticated read-only check</a> or browse the <a href="/docs/capabilities/">release catalog</a>.</p>
`,
  },

  "docs/capabilities/index.html": {
    title: "Capabilities",
    description: "Browse the versioned Opute Host Agent catalog, including declared effects and JSON schemas.",
    current: "capabilities",
    body: capabilityReferenceBody(latestVerifiedCatalog),
  },
  [versionedCapabilityPath]: {
    title:
      (releaseCatalog.releaseChannel === "preview" ? "Capabilities preview — v" : "Capabilities — v") +
      releaseCatalog.packageVersion,
    description:
      releaseCatalog.releaseChannel === "preview"
        ? "Unreleased preview of Opute Host Agent capability descriptors and JSON schemas."
        : "Versioned Opute Host Agent capability descriptors and JSON schemas.",
    current: "capabilities",
    body: capabilityReferenceBody(releaseCatalog, true),
  },  "docs/configuration/index.html": {
    title: "Configuration",
    description: "Configure Opute Host Agent identity, bind address, port, authentication, providers, and mutation controls.",
    current: "configuration",
    body: `
<p class="badge">Reference</p>
<h1>Configuration</h1>
<p class="meta">Environment and CLI facts. Precedence: CLI <code>--env KEY=VALUE</code> → process environment → <code>--env-file</code> / <code>OPUTE_HOST_AGENT_ENV_FILE</code> (file fills unset keys only).</p>

<h2>Required</h2>
<table>
  <thead><tr><th>Variable</th><th>Notes</th></tr></thead>
  <tbody>
    <tr><td><code>OPUTE_REMOTE_AGENT_ID</code></td><td>Canonical opaque agent id. Set it explicitly; the npm launcher has no default.</td></tr>
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

  "docs/openapi/index.html": {
    title: "OpenAPI",
    description: "HTTP edge reference for Opute Host Agent health checks and the Streamable HTTP MCP endpoint.",
    current: "openapi",
    body: `
<p class="badge">Reference</p>
<h1>OpenAPI</h1>
<p class="meta">Machine-readable description of the Host Agent HTTP edge. MCP tool schemas remain in the live <code>tools/list</code> catalog — this document covers transport endpoints only.</p>

<h2>Download</h2>
<ul>
  <li><a href="/openapi.json"><code>openapi.json</code></a> (OpenAPI 3.1)</li>
  <li><a href="/openapi.yaml"><code>openapi.yaml</code></a></li>
</ul>

<h2>Endpoints</h2>
<table>
  <thead><tr><th>Method</th><th>Path</th><th>Auth</th><th>Notes</th></tr></thead>
  <tbody>
    <tr><td>GET</td><td><code>/health</code></td><td>None</td><td>Liveness / identity probe</td></tr>
    <tr><td>POST</td><td><code>/mcp</code></td><td>Bearer <code>MCP_AUTH_TOKEN</code></td><td>Streamable HTTP MCP (protocol <code>2026-07-28</code>)</td></tr>
  </tbody>
</table>

<h2>Defaults</h2>
<ul>
  <li>Standalone: <code>http://127.0.0.1:3014</code></li>
  <li>Platform mode: <code>http://0.0.0.0:3004</code></li>
</ul>

<div class="note">Tool names and JSON Schemas are revisioned in-process. Prefer <code>tools/list</code> / <code>get_capability_catalog</code> over baking tool lists into OpenAPI.</div>

<p>Related: <a href="/docs/configuration/">Configuration</a> · <a href="/docs/mcp-clients/">MCP clients</a> · <a href="/docs/capabilities/">Capabilities</a> · <a href="/llms.txt">llms.txt</a></p>
`,
  },

  "docs/concepts/index.html": {
    title: "Concepts",
    description: "Understand what Opute Host Agent does, how it differs from Opute Platform, and how its typed tools work.",
    current: "concepts",
    body: `
<p class="badge">Explanation</p>
<h1>Concepts</h1>
<p class="meta"><strong>Opute Host Agent connects authenticated MCP clients to Linux host facts and explicit typed operations.</strong> It runs beside a host, applies identity and effect gates, and returns structured observations.</p>
<p class="meta">Opute Platform coordinates authorized intent and durable work across hosts. For the execution flow and package layout, see <a href="/docs/architecture/">Architecture</a>.</p>

<h2 id="host-agent-and-platform">Host Agent and Platform</h2>
<p><strong>Host Agent owns execution on one host:</strong> it authenticates an MCP client, admits canonical resource URIs, checks typed capability effects, dispatches to the host or an available provider, and returns structured observations. <strong>Opute Platform owns intent, authorization, routing, and durable orchestration across hosts.</strong> Platform may assign work to a Host Agent; the Host Agent does not become the cross-host coordinator.</p>
<p>This website explains and onboards Host Agent. The public Host Agent site uses <code>opute.io</code> and <code>www.opute.io</code>; Opute Platform has separate <code>platform.opute.io</code> and <code>mcp.opute.io</code> routes. A site deployment does not authorize or mutate those Platform resources.</p>

<pre class="mermaid">
flowchart LR
  Client[MCP client / Platform] -->|typed tools/list + call| HA[Host Agent]
  HA -->|ops| Host[Linux host]
  HA -->|plugins| Prov[Provider MCP processes]
  Platform[Opute Platform] -->|intent + enrollment| Client
  Platform -.->|does not own host exec| HA
</pre>
<p class="diagram-alt"><strong>Diagram in words:</strong> A local MCP client or Platform sends typed requests to Host Agent. Host Agent executes against its Linux host or provider processes. Platform supplies intent and enrollment context while Host Agent retains host execution.</p>

<h2>Typed catalog, not folklore</h2>
<p>Clients do not send free-form shell. They discover a revisioned catalog, validate arguments against schemas, and invoke named tools. That is why memorized tool lists go stale and why <code>tools/list</code> is authoritative.</p>

<h2>Recipes and plans</h2>
<p>A <strong>recipe</strong> is the cookbook page: which dish, which ingredients, which version. A <strong>plan</strong> is the cooking steps the kitchen actually follows. The Host Agent keeps both so work can be pinned and parameterized, while only one runner ever executes. More: <a href="/docs/recipes/">Recipes &amp; plans</a> · field list: <a href="/docs/recipe-primitives/">primitives</a>.</p>

<h2>Fail-closed identity</h2>
<p>Every process must carry a canonical <code>OPUTE_REMOTE_AGENT_ID</code>. Standalone mutations stay off until explicitly enabled. Observations that could contain secrets are redacted in MCP results (write-only schema fields become <code>[redacted]</code>).</p>

<h2>Security boundary</h2>
<p>Treat the Host Agent like an API gateway on the host: authenticate at Streamable HTTP <code>/mcp</code> (Bearer bootstrap and/or OAuth), keep <code>/health</code> unauthenticated for liveness only, and never embed secrets in tool schemas. Prefer live catalog discovery over memorized names. Public dogfood hostnames must not expose Host Agent MCP administration.</p>

<h2>Providers as plugins</h2>
<p>Kubernetes, Cloudflare, Tailscale, Ollama, and Host OS surfaces arrive as provider plugins under <code>plugins/</code>, not as hard-wired product branches inside the MCP server. Shared host seams live in <code>internal/hostruntime</code>; there is no separate <code>internal/provider</code> package.</p>

<h2>Optional LLM layer</h2>
<p>Core infrastructure tools must work when no LLM provider is installed or healthy. LLM serving is an activated capability, not a dependency of the kernel.</p>

<h2>How this site is written</h2>
<p>Pages follow <a href="https://diataxis.fr/">Diátaxis</a>: tutorials teach a first success; how-tos accomplish a known goal; reference states facts without narrative; explanation answers <em>why</em> and builds mental models. Mixing those modes produces pages that intimidate without operating — so recipe <em>why</em> lives under Explanation, and the field catalog under Reference.</p>
`,
    mermaid: true,
  },

  "docs/availability/index.html": {
    title: "Kubernetes availability",
    description: "Review Opute Host Agent's local K3s setup and one-guest-loss evidence, including what the test does not prove.",
    current: "availability",
    body: `
<p class="badge">Explanation</p>
<h1>Kubernetes availability and failure scope</h1>
<p class="meta">“High availability” is a behavior under a named failure. State which behavior should continue, where the failed component lives, and how recovery works.</p>
<aside class="callout" id="local-host-agent-test"><strong>Verified local Host Agent test · ${escapeHTML(localHAProof.evidenceDate)}.</strong> Three fresh ${escapeHTML(localHAProof.guestKind)} server guests were provisioned and configured through typed Host Agent MCP operations as a K3s ${escapeHTML(localHAProof.k3sVersion)} cluster with ${escapeHTML(localHAProof.datastoreMode)}. The guests shared one physical host. With one guest stopped, ${localHAProof.readyWhileOneGuestStopped} of ${localHAProof.serverCount} nodes were Ready; a typed ConfigMap apply and read both succeeded through the surviving control plane. Starting the guest restored ${localHAProof.readyAfter} of ${localHAProof.serverCount} Ready nodes.</aside>
<p>The test used K3s provider <code>${escapeHTML(localHAProof.providerVersion)}</code>. The Host Agent process reported runtime <code>${escapeHTML(localHAProof.hostAgentRuntime)}</code> and catalog revision <code>${escapeHTML(localHAProof.hostAgentCatalogRevision)}</code>. The published <code>${cloudflareProtectedPackageToken(tutorialCatalog)}</code> canary separately proves the authenticated read-only first-success path; it did not test this HA setup flow.</p>
<p>This result covers one Incus guest/server failure on one physical host. It does not establish host or site failure recovery, network partition behavior, workload serving, durable application data, new workload scheduling, or external endpoint failover. The membership probe also reported that no external HA endpoint was configured.</p>

<h2>Separate the outcomes</h2>
<p>One healthy member list is not an availability test. Check each property that matters to the user:</p>
<ul>
  <li><strong>Control-plane writes:</strong> can the Kubernetes API accept a change while a server is down?</li>
  <li><strong>Existing workloads:</strong> do already-running Pods continue on a surviving node?</li>
  <li><strong>New scheduling:</strong> can the control plane place or replace workloads?</li>
  <li><strong>Application serving:</strong> does the user-facing endpoint remain reachable?</li>
  <li><strong>Durable state:</strong> does application data remain readable and writable?</li>
</ul>
<p>Record these separately. A service returning HTTP 200 does not prove Kubernetes write availability, and a healthy cluster status does not prove the application path is serving.</p>
<p>In this K3s provider, <code>get-cluster-info.readyNodeCount</code> and each node's <code>status</code> expose the per-node observation. Its aggregate <code>ready</code> field is true when at least one node is Ready; that boolean alone does not prove etcd quorum or application health. The typed write and read during the guest failure supply the API write-continuity evidence for this specific test.</p>

<h2>K3s datastore quorum</h2>
<p>For embedded etcd, K3s documents an HA cluster as <strong>three or more server nodes</strong>. Quorum requires a majority. With only two voting members, losing either member removes quorum, so automatic Kubernetes write continuity is not established.</p>
<p>With an external datastore, K3s uses two or more server nodes. The external datastore has its own availability requirements and must survive the failure being tested; two K3s servers alone do not make that database highly available.</p>
<ul>
  <li><a href="https://docs.k3s.io/datastore/ha-embedded">K3s: High Availability Embedded etcd</a></li>
  <li><a href="https://docs.k3s.io/datastore/ha">K3s: High Availability External DB</a></li>
</ul>

<h2>Failure domains matter</h2>
<p>Three guests on one physical computer can demonstrate guest-level setup and failure behavior. They share the computer’s power, storage, host networking, and physical failure domain, so that test cannot establish resilience to losing the host or site. State the tested boundary with the result.</p>

<h2>What remains untested</h2>
<ul>${localHAProof.notEstablished.map((limitation) => `<li>${escapeHTML(limitation)}</li>`).join("\n  ")}</ul>

<h2>What a clean setup test should record</h2>
<ol>
  <li>Start from isolated hosts with no K3s service, datastore, or prior cluster membership. If the claim includes installing and enrolling Host Agent itself, test that bootstrap as a separate step.</li>
  <li>Record each exact Host Agent identity, software revision, active provider/catalog revision, and the empty baseline.</li>
  <li>Use the exact setup capabilities exposed by the tested release. Platform may coordinate authorized cross-host intent and durable runs; each host change should remain attributable to its owning Host Agent. If any setup step uses a shell, CLI, cloud console, or other out-of-band path, record it and narrow the claim: that run did not prove Host Agent-only setup.</li>
  <li>Verify the expected server membership and the configured datastore mode before injecting failure.</li>
  <li>Stop or isolate each server in turn. Measure API reads and writes, workload serving, durable application state, and recovery separately.</li>
  <li>Restore the failed node through the supported typed path, verify it rejoins, and retain redacted run evidence.</li>
</ol>
<p>A successful join proves membership, not clean-room bootstrap or availability. Only the failure checks prove the stated behavior. Use disposable, isolated test hosts for destructive setup and failure injection.</p>

<h2>Opute ownership boundary</h2>
<p>Host Agent is the typed executor on one host. Opute Platform owns intent, authorization, routing, and durable orchestration across hosts. A multi-host result must show both the coordinator’s durable outcome and the Host Agent evidence for each affected host. See <a href="/docs/concepts/#host-agent-and-platform">how the products fit together</a> and <a href="/docs/recipes/">how recipes and plans work</a>.</p>
<p>This guide states the evidence required for an HA claim. Check the <a href="/docs/capabilities/">release reference</a> and live <code>tools/list</code> for the capabilities available in a particular installation.</p>
`,
  },

  "docs/architecture/index.html": {
    title: "Architecture",
    description: "Learn how Host Agent's transport, catalog, providers, domains, and Cordis kernel fit together.",
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
<p class="diagram-alt"><strong>Diagram in words:</strong> Platform and a local IDE client send typed MCP requests to Host Agent. Host Agent uses shared host seams, domain packages, and provider processes to perform their declared operations.</p>

<h2>MCP edge</h2>
<ul>
  <li><code>POST /mcp</code> — Streamable HTTP; auth via bootstrap <code>MCP_AUTH_TOKEN</code> Bearer and/or OAuth.</li>
  <li><code>GET /health</code> — open; may expose <code>mcpToolNamePrefix</code> when name prefixing is enabled.</li>
  <li>stdio transport is not supported for the public Host Agent surface.</li>
</ul>
<p>Host Agent uses MCP 2026-07-28 discovery: the client calls <code>server/discover</code>, then lists and calls tools. See the <a href="/docs/openapi/">HTTP reference</a> and <a href="/docs/mcp-clients/">client setup</a> for transport details.</p>

<pre class="mermaid">
sequenceDiagram
  participant C as MCP client
  participant HA as Host Agent
  C->>HA: POST /mcp Authorization Bearer
  C->>HA: server/discover (2026-07-28)
  HA-->>C: server capabilities
  C->>HA: tools/list
  HA-->>C: revisioned CatalogSnapshot
  C->>HA: tools/call name + args
  Note over HA: admit URI · check effects · dispatch
  HA-->>C: typed result (redacted)
</pre>
<p class="diagram-alt"><strong>Diagram in words:</strong> The client authenticates a POST to /mcp, discovers server capabilities, lists the current catalog, and calls a named tool. Host Agent admits the resource, checks its effect, and returns a typed result.</p>

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
<p class="diagram-alt"><strong>Diagram in words:</strong> A tool call is checked against the catalog revision, admitted for its resource URI, gated by its declared effect, dispatched to a domain or provider, and returned as a redacted observation.</p>

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
    description: "Learn when to use Host Agent recipes or plans and how the runner validates and executes each step.",
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
<p class="diagram-alt"><strong>Diagram in words:</strong> A recipe pins inputs and expands into a host plan. The shared plan runner executes its typed tool steps.</p>

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
    <tr><td>Execute a Host Agent-local task (site deployment is controlled by the private repository)</td><td><code>host-recipe.v1</code> local</td><td><code>run_host_local_recipe</code></td></tr>
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
<p class="diagram-alt"><strong>Diagram in words:</strong> A plan node that is already ready is marked satisfied. Otherwise its action runs, validation checks the result, recovery may retry, and compensation may run if the plan aborts.</p>

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
    description: "Reference Host Agent recipe and plan fields, statuses, assertions, and execution limits.",
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
<p class="diagram-alt"><strong>Diagram in words:</strong> A validated recipe resolves inputs and a source pin, expands to a host plan, and the runner stores durable run state for later inspection.</p>

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
<p class="diagram-alt"><strong>Diagram in words:</strong> The runner walks topological levels, fans out bounded node work, repeats readiness sweeps while state is unstable, then reaches a terminal completed, failed, or waiting status.</p>

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
<p class="diagram-alt"><strong>Diagram in words:</strong> Nodes can apply a validated mutation, perform a read-only readiness check, fan out over items, or wait for Platform input. Failed actions may recover; abort compensation runs in reverse order.</p>

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
  <li>Private site deployment: <a href="https://github.com/wunderous/opute-site-deploy">wunderous/opute-site-deploy</a> - <a href="/docs/dogfood/">Publish this site</a></li>
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
    description: "Understand Host Agent's membership, private mesh, public ingress, and tunnel capabilities.",
    current: "networking",
    body: `
<p class="badge">Explanation</p>
<h1>Networking</h1>
<p class="meta">HA networking uses three exclusive Service Definitions. <code>mesh-runtime.v1</code> installs and checks the selected network runtime; the definitions below describe membership, private traffic, and public ingress.</p>

<h2>Three seams (ADR-0016)</h2>
<table>
  <thead><tr><th>Service Definition</th><th>Job</th><th>Neutral ops</th></tr></thead>
  <tbody>
    <tr><td><code>mesh-membership.v1</code></td><td>Host joins a trust domain</td><td>enroll, status, leave</td></tr>
    <tr><td><code>private-mesh.v1</code></td><td>East-west reachability</td><td>ensure, ensure-service, probe</td></tr>
    <tr><td><code>public-ingress.v1</code></td><td>North-south stable HTTPS</td><td>ensure, promote, probe</td></tr>
  </tbody>
</table>
<p>Legacy <code>network-overlay.*</code> names are deprecated migration aliases. Use <code>mesh-runtime.v1</code> for network runtime setup and the three Service Definitions in this table for membership, private-mesh, and public-ingress operations.</p>

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
<p class="diagram-alt"><strong>Diagram in words:</strong> Tailscale provides membership, private mesh, and public ingress. Cloudflare provides membership and public ingress in the described bundle. Host Agent recipes compose the neutral service definitions.</p>

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
<p class="diagram-alt"><strong>Diagram in words:</strong> Platform connects to Host Agents over typed MCP; Host Agents reach one another through the private mesh. Public clients reach workloads through public ingress and a scoped gateway.</p>
<p>Funnel and Cloudflare public ingress are application north-south paths. They are not substitutes for the private mesh, the Kubernetes datastore, or authenticated Host Agent MCP administration.</p>

<h2>Tunneling capability</h2>
<p><code>opute.capability.tunneling.*</code> owns dedicated host tunnels, probes, removal, and Kubernetes connector install. Dogfood for this site uses a dedicated tunnel <code>opute-www-opute-io</code> — never the Platform tunnel.</p>
<ul>
  <li>Stable public hostnames require DNS + TLS readiness probes before calling a surface “done.”</li>
  <li>Connector run tokens are write-only; MCP clients see <code>[redacted]</code>.</li>
  <li>Public MCP quick/stable tunnel helpers (<code>ensure_public_mcp_*</code>) are separate from Platform admin MCP.</li>
</ul>

<div class="note">Reachability of a marketing hostname is not two-node etcd-quorum HA. Keep those claims separate.</div>

<p>Related: <a href="/docs/architecture/">Architecture</a> · <a href="/docs/recipes/">Recipes</a> · <a href="/docs/dogfood/">Publish this site</a> · <a href="/docs/resources/">Resources</a></p>
`,
    mermaid: true,
  },

  "docs/resources/index.html": {
    title: "Resources & safety",
    description: "Learn how Host Agent admits resource identities, applies effect gates, enforces quotas, and redacts secrets.",
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
<p class="diagram-alt"><strong>Diagram in words:</strong> Discovery produces a canonical resource URI. Admission and capacity checks precede a typed operation; its observation is schema-redacted before it reaches the MCP client.</p>

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
<p class="diagram-alt"><strong>Diagram in words:</strong> A tunnel token is write-only and redacted in the MCP result. The Host Agent can retain it in-process while installing a Kubernetes connector secret.</p>

<h2>Identity</h2>
<p>Every Host Agent process carries <code>OPUTE_REMOTE_AGENT_ID</code>. Inventory tools omit caller-supplied host id overrides that would impersonate another agent (ADR-0006). Cross-host mutations target exact opaque agent identities — not Tailscale hostnames or display labels.</p>
`,
    mermaid: true,
  },
}

for (const { catalog: archive } of archivedReleaseCatalogs) {
  const rel = "docs/versions/v" + archive.packageVersion + "/capabilities/index.html"
  if (pages[rel]) {
    throw new Error("archived release route collides with a generated page: " + rel)
  }
  pages[rel] = {
    title: "Capabilities — v" + archive.packageVersion,
    description: "Archived, verified Opute Host Agent capability descriptors and JSON schemas.",
    current: "capabilities",
    body: capabilityReferenceBody(archive, true, true),
  }
}

for (const [rel, spec] of Object.entries(pages)) {
  const full = join(root, rel)
  mkdirSync(dirname(full), { recursive: true })
  const canonicalPath = `/${rel.replace(/index\.html$/, "")}`
  writeFileSync(full, page(spec, canonicalPath))
  console.log("wrote", rel)
}

const versionedCatalogFile = join(root, versionedCapabilityPath)
mkdirSync(dirname(versionedCatalogFile), { recursive: true })
writeFileSync(
  join(root, versionedCatalogDownload.replace(/^\//, "")),
  JSON.stringify(releaseCatalog, null, 2) + "\n",
)
for (const { catalog: archive } of archivedReleaseCatalogs) {
  const archiveDownload =
    "docs/versions/v" + archive.packageVersion + "/capabilities/catalog.json"
  const archiveFile = join(root, archiveDownload)
  mkdirSync(dirname(archiveFile), { recursive: true })
  writeFileSync(archiveFile, JSON.stringify(archive, null, 2) + "\n")
}


writeFileSync(
  join(root, "index.html"),
  `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  ${seoMeta("Opute Host Agent — Configure and verify K3s clusters", "Configure K3s servers with authenticated typed MCP operations, inspect membership, and test one-guest recovery through Opute Host Agent.", `${SITE_ORIGIN}/`)}
  <link rel="stylesheet" href="${CSS}" />
  <link rel="preconnect" href="https://fonts.googleapis.com" />
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin />
  <link href="https://fonts.googleapis.com/css2?family=DM+Sans:ital,opsz,wght@0,9..40,400;0,9..40,600;0,9..40,700;1,9..40,400&family=Instrument+Serif:ital@0;1&display=swap" rel="stylesheet" />
</head>
<body lang="en">
  <a class="skip-link" href="#main-content" data-i18n="nav.skip">Skip to main content</a>
${nav("home")}
  <main class="hero home-hero" id="main-content" lang="en">
    <p class="eyebrow">Opute Host Agent · for Linux operators and agent authors</p>
    <h1>Configure and verify K3s clusters through Opute Host Agent.</h1>
    <p class="lede">
      Use authenticated, typed MCP operations to provision K3s servers, inspect membership, and test recovery. In a local three-server test, a typed API write and read succeeded after one guest stopped. The guests shared one physical host, so this proves guest-level behavior only. Opute Platform coordinates authorized work across hosts; Host Agent executes each host-scoped operation.
    </p>
    <div class="cta">
      <a class="btn primary" href="/docs/availability/#local-host-agent-test">See the K3s setup and one-guest test</a>
      <a class="btn ghost" href="/docs/get-started/">Start with an authenticated read-only check</a>
    </div>
    <p class="home-product-boundary"><strong>Test scope:</strong> ${localHAProof.readyWhileOneGuestStopped} of ${localHAProof.serverCount} nodes stayed Ready with one Incus guest stopped; the provider reported no external HA endpoint. <a href="/docs/availability/">Read all evidence and limits</a>.</p>
    <div class="home-hero-proof" aria-label="Local K3s test results">
      <span>${localHAProof.serverCount} K3s servers</span>
      <span>${localHAProof.datastoreMode}</span>
      <span>Typed API write/read during one guest loss</span>
    </div>
    <figure class="visual home-terminal">
      <figcaption>Read-only first call · example excerpt with redacted values, not live host output</figcaption>
      <pre class="terminal"><code>get_host_info {}
{
  "uri": "host:example:host-01",
  "hostName": "…",
  "providerId": "…",
  "lxcBinaryPath": "…",
  "systemctlPath": "…",
  "supportedTools": ["…"]
}</code></pre>
      <p><a href="/docs/get-started/">Install, authenticate, and run this check →</a></p>
    </figure>
  </main>

  <section class="home-section" aria-labelledby="what-it-does">
    <div class="home-section-heading">
      <p class="eyebrow">What you can operate</p>
      <h2 id="what-it-does">Build cluster membership, inspect hosts, and verify each step.</h2>
      <p>Discover the current tool catalog before every operation. Available capabilities depend on the host and its active providers.</p>
    </div>
    <div class="home-capabilities">
      <article><span class="home-card-index">01 / Compute</span><h3>Linux &amp; Incus</h3><p>Inspect host state, manage services, and provision or inspect guests through typed tools.</p></article>
      <article><span class="home-card-index">02 / Workloads</span><h3>Kubernetes &amp; images</h3><p>Provision and join K3s servers, inspect node readiness, apply manifests, and read resources through the configured provider.</p></article>
      <article><span class="home-card-index">03 / Reachability</span><h3>Networking &amp; tunnels</h3><p>Manage declared network and tunnel capabilities through provider plugins when configured.</p></article>
    </div>
    <a class="home-text-link" href="/docs/capabilities/">Browse the captured catalog and learn how to discover yours →</a>
    <a class="home-text-link" href="/docs/availability/#local-host-agent-test">K3s setup evidence: one guest failed, and typed API writes continued →</a>
  </section>

  <section class="home-section home-flow" aria-labelledby="why-it-matters">
    <div class="home-section-heading">
      <p class="eyebrow">The operating loop</p>
      <h2 id="why-it-matters">A tool call can become checked, recoverable work.</h2>
      <p>Host-local recipes package versioned inputs and a plan. The Host Agent runs that plan against this host, checks observed readiness when the plan declares it, and retains the run for inspection.</p>
    </div>
    <ol class="home-flow-steps">
      <li><span class="step-number" aria-hidden="true"></span><div><strong>Discover</strong><span>Read the live, revisioned MCP catalog and its schemas.</span></div></li>
      <li><span class="step-number" aria-hidden="true"></span><div><strong>Admit</strong><span>Authenticate, resolve the target, and check whether a write is allowed.</span></div></li>
      <li><span class="step-number" aria-hidden="true"></span><div><strong>Execute &amp; verify</strong><span>Run typed actions with readiness checks, retry, and compensation where the plan declares them.</span></div></li>
      <li><span class="step-number" aria-hidden="true"></span><div><strong>Inspect</strong><span>Read the durable plan result and the observed resource state.</span></div></li>
    </ol>
    <p class="home-section-note">A host-local recipe runs declared steps on one host. For work across hosts, Platform coordinates the plan and Host Agent executes each host-scoped action. The local K3s test covers one guest failure, not failure of the physical host or a user-facing serving path; see the <a href="/docs/availability/">availability guide</a>.</p>
    <a class="home-text-link" href="/docs/recipes/">How recipes and plans work →</a>
  </section>

  <section class="home-section home-proof" aria-labelledby="site-proof">
    <div class="home-proof-copy">
      <p class="eyebrow">Built with the same tools</p>
      <h2 id="site-proof">This site is deployed through Host Agent.</h2>
      <p>Public CI builds this site's image. A separate deployment controller selects its verified digest and asks a Host Agent to run a host-local recipe: apply the Kubernetes workload, ensure the dedicated tunnel, and probe both public URLs.</p>
      <p>The recipe and its observed results make the deployment reviewable beyond a successful command response.</p>
      <a class="home-text-link" href="/docs/dogfood/">See how this site is hosted →</a>
    </div>
    <div class="home-proof-trace" aria-label="Site recipe stages">
      <div><span>01</span><strong>Select image</strong><code>verified public CI digest</code></div>
      <div><span>02</span><strong>Apply workload</strong><code>apply_manifest</code></div>
      <div><span>03</span><strong>Open route</strong><code>ensure-host-tunnel</code></div>
      <div><span>04</span><strong>Probe site</strong><code>probe_http_endpoint</code></div>
    </div>
  </section>

  <section class="home-section home-safety" aria-labelledby="built-for-control">
    <div class="home-section-heading">
      <p class="eyebrow">Operator control</p>
      <h2 id="built-for-control">Clear boundaries for consequential work.</h2>
    </div>
    <div class="home-safety-grid">
      <div><strong>Explicit identity</strong><p>Each agent has one canonical host identity. Resource targets use typed URIs.</p></div>
      <div><strong>Writes opt in</strong><p>Standalone mutations are denied by default and require deliberate enablement.</p></div>
      <div><strong>Provider choice</strong><p>Providers publish neutral capabilities into a revisioned catalog. Clients use the capability contract.</p></div>
      <div><strong>Evidence with redaction</strong><p>Plans retain their outcomes; write-only fields are redacted in client-visible results.</p></div>
    </div>
    <div class="home-close">
      <p>Ready to connect your own MCP client?</p>
      <a class="btn primary" href="/docs/get-started/">Get started</a>
    </div>
  </section>

  <footer>
    <span>This site is dogfood — hosted by Host Agent on <code>opute.io</code></span>
    <span><a href="/docs/dogfood/">How it is hosted</a> · <a href="/llms.txt">llms.txt</a> · <a href="/openapi.json">OpenAPI</a></span>
  </footer>
  ${SITE_SCRIPTS}
</body>
</html>
`,
)
console.log("wrote index.html")

const sitemapPaths = ["/", ...Object.keys(pages).map((rel) => `/${rel.replace(/index\.html$/, "")}`)]
const sitemap = [
  '<?xml version="1.0" encoding="UTF-8"?>',
  '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">',
  ...sitemapPaths.map(
    (path) => `  <url>\n    <loc>${SITE_ORIGIN}${path}</loc>\n  </url>`,
  ),
  "</urlset>",
  "",
].join("\n")
writeFileSync(join(root, "robots.txt"), `User-agent: *\nAllow: /\nSitemap: ${SITE_ORIGIN}/sitemap.xml\n`)
writeFileSync(join(root, "sitemap.xml"), sitemap)
console.log("wrote robots.txt and sitemap.xml", sitemapPaths.length)

// Search index from generated page bodies
{
  const strip = (html: string) =>
    html
      .replace(/<script[\s\S]*?<\/script>/gi, " ")
      .replace(/<style[\s\S]*?<\/style>/gi, " ")
      .replace(/<[^>]+>/g, " ")
      .replace(/\s+/g, " ")
      .trim()
  const catalogByPage = new Map<string, ReleaseCatalog>([
    ["docs/capabilities/index.html", latestVerifiedCatalog],
    [versionedCapabilityPath, releaseCatalog],
  ])
  for (const { catalog: archive } of archivedReleaseCatalogs) {
    catalogByPage.set(
      "docs/versions/v" + archive.packageVersion + "/capabilities/index.html",
      archive,
    )
  }
  const searchPages = Object.entries(pages).map(([rel, spec]) => {
    const url = "/" + rel.replace(/index\.html$/, "").replace(/\.html$/, "")
    const catalog = catalogByPage.get(rel)
    return {
      url: url.endsWith("/") || url === "/docs" ? (url.endsWith("/") ? url : url + "/") : url + "/",
      title: spec.title,
      description: spec.description,
      body: catalog
        ? catalog.tools.map((tool) => tool.name + " " + tool.description).join(" ") +
          " " + strip(spec.body).slice(0, 12000)
        : strip(spec.body).slice(0, 12000),
    }
  })
  writeFileSync(join(root, "search-index.json"), JSON.stringify({ pages: [{ url: "/", title: "Opute Host Agent", description: "Inspect Linux hosts and discover typed infrastructure capabilities through MCP.", body: "Host Agent read-only host check get_host_info host-local recipes Kubernetes Incus network tunnels" }, ...searchPages] }, null, 2))
  console.log("wrote search-index.json", searchPages.length)
}

// OpenAPI 3.1 for HTTP edge
{
  const openapi = {
    openapi: "3.1.0",
    info: {
      title: "Opute Host Agent HTTP edge",
      version: latestVerifiedCatalog.packageVersion,
      description:
        "Streamable HTTP MCP transport for Opute Host Agent. Tool schemas are revisioned via tools/list — not frozen in this document. See https://www.opute.io/docs/openapi/",
      contact: { url: "https://www.opute.io/docs/" },
    },
    servers: [
      { url: "http://127.0.0.1:3014", description: "Standalone default" },
      { url: "http://127.0.0.1:3004", description: "Platform mode default (local)" },
    ],
    paths: {
      "/health": {
        get: {
          operationId: "getHealth",
          summary: "Liveness and agent identity probe",
          security: [],
          responses: {
            "200": {
              description: "Agent is listening",
              content: {
                "application/json": {
                    schema: {
                      type: "object",
                      properties: {
                        instanceId: {
                          type: "string",
                          description: "Host Agent execution instance configuration.",
                        },
                        localInstanceId: {
                          type: "string",
                          description: "Local launcher ownership identity; it is not canonical Host Agent identity.",
                        },
                        agentId: { type: "string" },
                      mcpToolNamePrefix: { type: "string" },
                    },
                    additionalProperties: true,
                  },
                },
              },
            },
          },
        },
      },
      "/mcp": {
        post: {
          operationId: "mcpStreamableHttp",
          summary: "Streamable HTTP MCP endpoint",
          description:
            "JSON-RPC MCP over Streamable HTTP (protocol 2026-07-28). Clients must send Accept: application/json, text/event-stream. Prefer live tools/list for tool schemas.",
          security: [{ bearerAuth: [] }],
          parameters: [
            {
              name: "MCP-Protocol-Version",
              in: "header",
              schema: { type: "string", example: "2026-07-28" },
            },
          ],
          requestBody: {
            required: true,
            content: {
              "application/json": {
                schema: {
                  type: "object",
                  required: ["jsonrpc", "method"],
                  properties: {
                    jsonrpc: { type: "string", const: "2.0" },
                    id: {},
                    method: { type: "string" },
                    params: { type: "object", additionalProperties: true },
                  },
                },
              },
            },
          },
          responses: {
            "200": { description: "JSON or SSE MCP response stream" },
            "401": { description: "Missing or invalid Authorization" },
          },
        },
      },
    },
    components: {
      securitySchemes: {
        bearerAuth: {
          type: "http",
          scheme: "bearer",
          description: "Bootstrap MCP_AUTH_TOKEN",
        },

      },
    },
    "x-opute-mcp": {
      protocolVersion: "2026-07-28",
      transport: "streamable-http",
      catalogAuthority: "tools/list",
      packageVersion: latestVerifiedCatalog.packageVersion,
      releaseChannel: latestVerifiedCatalog.releaseChannel,
      catalogRevision: latestVerifiedCatalog.catalogRevision,
      toolCount: latestVerifiedCatalog.toolCount,
      toolNames: latestVerifiedCatalog.tools.map((tool) => tool.name),
    },
  }

  // JSON is a YAML 1.2 subset; serialize one object so both downloads stay identical.
  const serializedOpenAPI = JSON.stringify(openapi, null, 2) + "\n"
  writeFileSync(join(root, "openapi.json"), serializedOpenAPI)
  writeFileSync(join(root, "openapi.yaml"), serializedOpenAPI)
  console.log("wrote openapi.json + openapi.yaml")
}

writeFileSync(
  join(root, "llms.txt"),
  `# Opute Host Agent

> Streamable HTTP MCP server for typed host infrastructure assignments (guests, Kubernetes, registries, tunnels).

Docs follow Diátaxis. Prefer live tools/list over memorized tool names.
Public site: https://www.opute.io and https://opute.io (Host Agent dogfood).
Host Agent documentation covers the execution server. Opute Platform: https://platform.opute.io / https://mcp.opute.io

## Tutorial
- [Get started](https://www.opute.io/docs/get-started/): first authenticated tools/list

## How-to
- [Install & run](https://www.opute.io/docs/install/)
- [Connect an MCP client](https://www.opute.io/docs/mcp-clients/)
- [Publish this site](https://www.opute.io/docs/dogfood/)
- [Troubleshooting](https://www.opute.io/docs/troubleshooting/)

## Compatibility
- [Client and transport verification](https://www.opute.io/docs/compatibility/)

## Use cases
- [Host inspection and optional provider workflows](https://www.opute.io/use-cases/)

## Explanation
- [Kubernetes availability and failure scope](https://www.opute.io/docs/availability/)

## Reference
- [Capabilities](https://www.opute.io/docs/capabilities/)
- [Latest verified capability catalog](https://www.opute.io/docs/versions/v${latestVerifiedCatalog.packageVersion}/capabilities/)
${releaseCatalog.releaseChannel === "preview" ? `- [Preview capability catalog](https://www.opute.io/${currentCatalogRoute}/)` : ""}
- [Configuration](https://www.opute.io/docs/configuration/)
- [Recipe & plan primitives](https://www.opute.io/docs/recipe-primitives/)

## Explanation
- [Concepts](https://www.opute.io/docs/concepts/)
- [Architecture](https://www.opute.io/docs/architecture/)
- [Recipes & plans](https://www.opute.io/docs/recipes/)
- [Networking](https://www.opute.io/docs/networking/)
- [Resources & safety](https://www.opute.io/docs/resources/)

## Research notes
- Peer synthesis: site/context/RESEARCH.md
`,
)
console.log("wrote llms.txt")

writeFileSync(
  join(contextDir, "PACKET.md"),
  `# Context packet - Host Agent docs and marketing site

## Documentation source and invariant

The Bun generator in site/scripts/generate-docs.ts owns rendered pages, search index, sitemap, OpenAPI downloads, llms.txt, and this packet. Do not hand-edit generated HTML.

The active public-documentation-release-parity decision in .agents/decisions/public-documentation-release-parity.json is authoritative for release claims. The first-success tutorial and canonical capability reference use the newest stable catalog with matching published-package read-only canary evidence. Unreleased candidates live on a visibly labeled preview route. An archived release catalog is created from the exact catalog and package metadata at its published source SHA, matched to that release's canary evidence, and is immutable afterward. The catalog snapshot is an allowlisted projection; live tools/list is authoritative at runtime.

## Audience jobs

- First success: /docs/get-started/
- Client setup: /docs/mcp-clients/
- Compatibility evidence: /docs/compatibility/
- Troubleshooting: /docs/troubleshooting/
- Capability reference: /docs/capabilities/
- Use cases: /use-cases/
- Product boundary: /docs/concepts/#host-agent-and-platform
- Kubernetes availability and failure scope: /docs/availability/
- Architecture and trust: /docs/architecture/ and /docs/resources/

## Ownership boundary

Host Agent executes explicit typed capabilities against one host. Opute Platform owns intent, authorization, routing, and durable orchestration across hosts. Public content and image builds live in this repository; private deployment credentials and production rollout live in the private opute-site-deploy repository. Public workflows must not gain private deployment access.

## Release metadata

Generated candidate metadata is read from site/context/release-catalog.json. Change its release channel to stable only with matching package version, source revision, catalog revision, and passing published read-only canary. Catalog capture drops stable status and old canary evidence whenever either the package version or catalog revision changes. The tutorial selects the newest verified stable release while the current candidate remains preview. Recompute decision anchors when an anchored authority file changes.

Wrap every versioned @opute/host-agent@VERSION token emitted into HTML with Cloudflare's <!--email_off--> and <!--/email_off--> suppression comments using the shared generator helper. Cloudflare can otherwise rewrite this scoped package token as an email address, breaking what visitors see, hear, or copy. The generated-site validator checks active and archived releases; a public deployment must also be checked in a browser after edge transformation. Do not change zone-wide Cloudflare settings from this repository.
`,
)
console.log("wrote context/PACKET.md")
