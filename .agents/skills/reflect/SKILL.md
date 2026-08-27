---
name: reflect
description: Use after completing a feature, refactor, or complex debugging session, when fixing non-obvious failure modes or multi-client quirks, or when explicitly requested via /reflect. Inspects the session to leave explanatory inline comments in code and elevate durable design/requirement invariants into agentic files, skills, and decision records.
---

# Reflect: Session Inspection, Inline Comments & Invariant Elevation

## When to load this skill

Load this skill whenever you are:
- Concluding a substantial implementation, multi-file refactor, or debugging investigation.
- Fixing a multi-client incompatibility, protocol edge case, or OS/runtime constraint (e.g., WSL2 Hyper-V memory, MCP headers, streamable HTTP, Incus limits).
- Establishing a new architectural decision, safety rule, or lifecycle contract that future agent sessions must follow.
- Prompted with `/reflect`, "reflect on this session", or asked to capture learnings and comments.

**Do not load** for trivial, single-line mechanical edits (typos, simple formatting) that contain no architectural or operational learnings.

---

## Reflection & Elevation Protocol

```text
Session Complete / Feature Verified
            │
            ├───> Step 1: Code Review & Inline Comment Placement (inline-context-discipline)
            │     └─ Identify non-obvious branching, workarounds, or client quirks in git diff.
            │     └─ Add concise "why" / constraint comments; sync or remove stale comments.
            │
            └───> Step 2: Invariant Elevation & Codification (permanent-agentic-invariants)
                  └─ Classify durable learnings into 4-tier hierarchy.
                  └─ Update typed decisions (.agents/decisions/) / ADRs / domain skills.
                  └─ Maintain slim index pointers in AGENTS.md (never append essay prose).
```

---

### Step 1 — Code Review & Inline Comment Placement

Inspect `git diff` for all modified and untracked files against the rules in `inline-context-discipline`:

1. **Non-Obvious Branching & Workarounds:** If a change handles a multi-client edge case (e.g., standard vs modern MCP metadata) or platform workaround (WSL pipe buffering, systemd restart order), ensure a concise inline comment explains the *failure mode prevented* and the *external quirk*.
2. **Comment Cleanliness:** Remove comments that narrate syntax (`// parse json`). Verify existing comments in touched functions were updated or deleted if their underlying assumption changed.
3. **Self-Documenting Fixes:** Ensure bug fixes include a 1–2 line comment documenting the root cause or edge condition.

---

### Step 2 — Invariant Elevation & Tiering

Ephemeral chat context disappears when a session ends. Classify any discovered constraints or requirements into the 4-tier authority hierarchy:

| Tier | Category | Destination | Criteria / Action |
|---|---|---|---|
| **Tier 1** | Executable Proof | `schemas/`, `test/contract/`, `scripts/verify/` | Typed schemas and automated regression/contract tests that falsify the invariant. |
| **Tier 2** | Architectural Invariant | `.agents/decisions/<name>.json`, `docs/adr/` | Checkable cross-cutting rules with `rationale`, `scope`, and a verification script. |
| **Tier 3** | Domain Procedure | `.agents/skills/<name>/SKILL.md` or `references/` | Repeatable domain workflow, operational checklist, or component guide. |
| **Tier 4** | Global Index | `AGENTS.md` (Skill Table / Invariant Index) | Slim one-line routing pointer linking to the owning Tier 2/3 artifact. |

---

### Step 3 — Apply Edits & Summarize

1. Edit the code (inline comments), owning skills, decision records, or `LEARNING.md`.
2. Keep `AGENTS.md` lean: do not paste narrative case studies into `AGENTS.md`.
3. Run relevant test suites (`go test ./...`, `bun run test:invariants`) to verify no contracts were broken.
4. Report a concise summary of added/updated comments and elevated invariants to the user.
