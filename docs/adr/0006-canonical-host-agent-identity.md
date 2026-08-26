# ADR 0006: Canonical Host Agent identity

Status: accepted

Date: 2026-08-24

## Context

Host Agent and Platform previously carried several representations for the
same runtime: local development labels, platform bridge labels, hostnames,
fingerprints, and persisted IDs. Matching those values in different paths made
co-resident agents collide, left stale operations attached to the wrong agent,
and caused inventory and chat routing to select a host that was not actually
connected.

## Decision

`OPUTE_REMOTE_AGENT_ID` is the single canonical Host Agent identity. It is an
explicit opaque value, required at Host Agent configuration validation, and is
preserved exactly through registration, heartbeat, MCP routing, durable
operation ownership, session state, inventory canonicalization, and TUI
selection.

The following are evidence, not identity keys: machine fingerprint, runtime
instance ID, hostname, provider ID, endpoint, and display name. They may reject
an admission that conflicts with the selected canonical ID, but they cannot
find or replace it. Different canonical IDs remain distinct even when every
other fact matches. Missing, stale, conflicting, or ambiguous resolution fails
closed. Retired values are not translated at runtime; migration is explicit
re-onboarding or an offline durable-state rewrite.

Concrete providers publish neutral descriptors and results. They do not import
Host Agent internals, resolve aliases, or access TUI/platform state. Opute owns
intent, authorization, and durable orchestration; the Host Agent executes the
explicit typed assignment.

## One-time state cleanup

The completed canonical-identity cleanup was run once per local persistent
Platform SQLite store on 2026-08-24 through the existing `BridgeStateStore`
bootstrap path. The authoritative standalone store and the separate local-dev
store both verified zero remaining top-level or nested retired identity fields,
including `aliasIds` and `hostMerge`. The startup rewrite was then removed.
No public PostgreSQL store is claimed by this local evidence; any separately
deployed database requires its own explicit operator migration and evidence.

## Consequences

- There is no alias field, alias lookup, hostname fallback, fingerprint lookup,
  preferred-ID constant, or first-connected fallback in production routing.
- Co-resident platform and standalone agents require distinct explicit IDs and
  retain independent operations and tunnel sessions.
- Existing installations using retired IDs must be re-onboarded or migrated
  explicitly; compatibility downtime is preferable to silent misrouting.
- The one-time local alias-field cleanup is complete and no runtime bootstrap
  migration remains. Future identity changes use explicit re-onboarding or an
  separately reviewed offline durable-state migration.
- TUI, MCP, chat, and public inspector projections carry the selected exact ID
  only. They do not expose identity-resolution metadata as a second authority.

## Verification

The typed decision record
`../opute/.agents/decisions/canonical-host-agent-identity.json` and
`../opute/scripts/verify/canonical-host-agent-identity.ts` anchor the
cross-repository checks. Closure requires the Host Agent Go tests and vet,
Opute typecheck and identity tests, plus the verifier's forbidden-production
scan. Live MCP/chat evidence must use an explicitly configured ID and correlate
the exact tool argument with the connected Host Agent row.
