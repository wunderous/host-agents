# docs-marketing-site Specification

## Purpose

Defines the static documentation and marketing website for the Opute Host
Agent: how authors gather and organize writing context, information
architecture, content obligations, static-build contract, and constraints
that keep the site suitable for Host Agent dogfood hosting.

## ADDED Requirements

### Requirement: Context is gathered before docs are written or reorganized

Authors MUST gather a written context packet before drafting new docs pages,
rewriting existing pages, or changing the documentation information
architecture. The packet MUST record audience jobs, source citations with
precedence, Host Agent vs Platform boundaries, verification hooks for
factual claims, and redaction rules. Docs MUST NOT be authored from memory,
from Platform-only marketing, or from unverified chat summaries.

#### Scenario: Context packet exists before first draft

- **WHEN** work starts on a new docs or marketing page
- **THEN** a context packet is present in the site package (or linked change
  evidence) naming the audience job, cited sources, and verification hooks
- **AND** drafting does not begin until that packet exists

#### Scenario: Reorganization re-runs audience mapping

- **WHEN** navigation, route map, or page merge/split is proposed
- **THEN** the context packet’s audience-job list is updated first
- **AND** every retained route maps to exactly one primary job
- **AND** pages with no job are removed or merged rather than left as orphans

### Requirement: Source precedence governs factual claims

Factual claims about install commands, flags, default ports, MCP client
shapes, capability names, and topology MUST cite sources in this precedence
order:

1. this repository’s README, build/`make` targets, and serve/entrypoint flags;
2. Host Agent contracts, schemas, and ADRs in this repository;
3. OpenSpec changes that define Host Agent behavior;
4. live Host Agent capability catalog (`tools/list` / equivalent) when naming
   tools;
5. sibling Opute / Platform documentation only when explicitly labeled as
   Platform context and never as Host Agent install truth.

A claim that cannot cite at least one source from items 1–4 MUST NOT ship.
Platform docs MUST NOT override Host Agent README or contract truth.

#### Scenario: Install steps cite repository truth

- **WHEN** the install page documents how to run or package the Host Agent
- **THEN** each command or flag cites README, Makefile, or entrypoint source
  paths from this repository
- **AND** Platform bootstrap scripts are not presented as the primary Host
  Agent install path unless clearly marked Platform-only

#### Scenario: Tool names match the live catalog or contracts

- **WHEN** a capabilities or MCP page names a tool or capability
- **THEN** the name matches a contract/schema in this repository or a
  captured live `tools/list` excerpt in the context packet
- **AND** invented or remembered tool names are rejected

#### Scenario: Platform context is labeled and non-authoritative for install

- **WHEN** the site references Platform URLs, recipes, or dogfood cells
- **THEN** the copy labels them as Platform context
- **AND** they do not replace Host Agent standalone install or MCP serve
  instructions

### Requirement: Information architecture follows audience jobs

The documentation information architecture MUST be derived from the context
packet’s audience jobs. Each docs route MUST complete one primary job
(install, configure an MCP client, understand capabilities, or understand
dogfood hosting). Marketing home (`/`) MUST sell the product job, not dump
the full docs outline into the first viewport.

#### Scenario: Docs hub mirrors job map

- **WHEN** a visitor opens `/docs`
- **THEN** links correspond to the context packet’s primary jobs
- **AND** there is no docs nav entry without a matching job in the packet

#### Scenario: Unsupported topics stay out of nav

- **WHEN** the context packet has no verified source for a proposed topic
- **THEN** the topic is omitted from navigation and pages
- **AND** a stub page that invents behavior MUST NOT be published

### Requirement: The site is a static documentation and marketing surface

The Host Agent website MUST be built as static assets (HTML, CSS, fonts,
images, and optional client-side scripts). The published artifact MUST NOT
require an application server, runtime database, Platform API, or Host Agent
MCP connection in order to render documentation or marketing pages.

#### Scenario: Local static build succeeds

- **WHEN** the repository site package is built with the pinned toolchain
- **THEN** the build emits a directory of static files with a content digest
- **AND** opening the built `index.html` (or serving the directory with a
  static file server) renders the marketing home without network calls to
  Platform or Host Agent MCP

#### Scenario: Dynamic backend dependencies are absent

- **WHEN** the built artifact is inspected for server entrypoints
- **THEN** no Node/Go server process, SSR handler, or database connection
  string is required to serve the pages
- **AND** a build that embeds Host Agent tokens or kubeconfigs is rejected

### Requirement: Marketing home presents Host Agent as the product

The marketing home (`/`) MUST present the Host Agent brand as the primary
hero-level signal, with one headline, one short supporting sentence, one CTA
group (install and/or docs), and one dominant visual. The first viewport MUST
NOT be a dashboard of metrics, schedules, or secondary promos.

#### Scenario: First viewport brand test

- **WHEN** a visitor loads `/`
- **THEN** the Host Agent product name is visibly primary above secondary nav
- **AND** removing the navigation would still leave a page that reads as the
  Host Agent product, not a generic template

### Requirement: Documentation covers install, MCP clients, and dogfood

The documentation surface MUST include at least:

- an install path for the Host Agent binary or published package;
- Streamable HTTP MCP client configuration examples;
- a capability overview that describes typed execution without exposing
  secrets; and
- a dogfood page that explains that this site is intended to be served from a
  K3s cluster configured through a Host Agent recipe.

Each of those pages MUST be backed by the context packet and source
precedence rules above.

#### Scenario: Docs hub links required topics

- **WHEN** a visitor opens `/docs`
- **THEN** navigation reaches install, MCP clients, capabilities, and dogfood
  pages
- **AND** each page is reachable as a static path in the built artifact

#### Scenario: Dogfood page states the availability boundary

- **WHEN** a visitor opens the dogfood documentation page
- **THEN** the page states that successful site hosting proves Host Agent
  deployability for a static workload
- **AND** the page MUST NOT claim that site hosting equals two-node write
  availability or Platform self-hosting

### Requirement: Content and artifacts remain non-secret

Site source, build output, context packets, and OpenSpec-adjacent examples
MUST NOT contain Host Agent bearer tokens, overlay credentials, kubeconfigs,
or private mesh addresses used as durable identity. Context gathering from a
live agent MUST redact credentials before anything is checked in.

#### Scenario: Token-like material is blocked

- **WHEN** the static build or content lint runs against site sources and
  context packets
- **THEN** checked-in examples use placeholders for tokens and URLs that are
  not durable cluster identity
- **AND** a match against known secret patterns fails the gate

#### Scenario: Live catalog capture is redacted

- **WHEN** authors capture `tools/list` or health output for the context
  packet
- **THEN** bearer tokens, auth headers, and secret-bearing env values are
  stripped before the capture is committed
- **AND** only tool names, schemas, and non-secret defaults remain
