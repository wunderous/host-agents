---
name: inline-context-discipline
description: Use when reading or authoring inline code comments, implementing compensating logic, debugging platform/protocol edge cases, or refactoring code with existing inline comments. Defines selective comment inspection, signal vs noise filtering, and atomic comment maintenance rules.
---

# Inline Context & Comment Discipline

## When to load this skill

Load this skill whenever you are:
- Reading or writing code with non-obvious conditional branching, safety guards, or compensating logic.
- Investigating multi-client compatibility issues, platform quirks (e.g. WSL, Incus, systemd), or wire transport protocols.
- Refactoring existing functions or files that contain inline comments.
- Reviewing a diff to verify that added comments document *why* and constraints rather than narrating syntax.

**Do not load** for mechanical edits (formatting, renaming variables) where no semantic constraints or comments are touched.

---

## 1. Selective Retrieval & Active Inspection

Inline comments encode localized intent, hidden constraints, and external quirks that contracts and types cannot express alone. Inspect and weigh inline context when:
- **Navigating Edge Cases & Compensating Logic:** Investigating conditional branching, compatibility shims, workarounds, or platform-specific fallbacks.
- **Encountering Counter-Intuitive Code:** Encountering checks, locks, bounds, or ordering that appear redundant or suboptimal at first glance.
- **Operating on System & Protocol Boundaries:** Interfacing with third-party clients, external runtimes, wire transports, serialization, or environment assumptions.
- **Debugging Regressions:** Tracing subtle failures or unexpected behavior in existing subsystems.

---

## 2. Signal vs. Noise Filter

Inline comments are secondary to executable truth. When evaluating comments:
- **Executable Truth Trumps Stale Text:** Always corroborate inline comments against current tests, types, and authoritative architectural decisions. If a comment describes retired architecture or contradicts verified behavior, prioritize live evidence and reconcile the discrepancy.
- **Ignore Pure Narrative:** Disregard comments that merely restate code syntax without providing rationale or constraints.
- **Avoid Mechanical Scans:** Do not broadly parse comments in unrelated modules unless investigating an explicit dependency or defect in that path.

---

## 3. Comment Authoring & Lifecycle Maintenance

- **Capture Intent and Non-Obvious Constraints Only:** Document *why* a decision was made, what failure mode it prevents, or what external ecosystem quirk necessitated it. Never narrate *what* the code does.
- **Atomic Synchronization:** Whenever code behavior, conditionals, or boundaries change, update or remove the associated inline comments in the same change to prevent context drift and hallucination traps for future agents and human engineers.
- **Self-Documenting Failures:** When fixing an obscure bug or multi-client incompatibility, leave a concise inline note explaining the exact scenario or standard deviation that required the fix.
