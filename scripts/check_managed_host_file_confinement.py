#!/usr/bin/env python3
"""Verify the anchored user-scoped managed-file path boundary."""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECISION = ROOT / ".agents" / "decisions" / "managed-host-file-path-confinement.json"


def fail(message: str) -> None:
    print("managed host file boundary check failed: " + message, file=sys.stderr)
    raise SystemExit(1)


def main() -> None:
    try:
        decision = json.loads(DECISION.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail("cannot read decision record: " + str(error))
    for field in ("schemaVersion", "id", "claim", "owner", "authority", "scope", "exception", "evidence", "revisionBehavior", "anchors", "enforcedBy"):
        if field not in decision:
            fail("decision is missing " + field)
    if decision["id"] != "managed-host-file-path-confinement":
        fail("unexpected decision identity")
    if not isinstance(decision["anchors"], list) or not decision["anchors"]:
        fail("decision has no file anchors")
    seen: set[str] = set()
    for anchor in decision["anchors"]:
        if not isinstance(anchor, dict) or anchor.get("kind") != "file":
            fail("all anchors must identify whole files")
        relative = anchor.get("path")
        digest = anchor.get("digest")
        if not isinstance(relative, str) or relative in seen or not isinstance(digest, str) or len(digest) != 64:
            fail("anchor path or digest is invalid")
        seen.add(relative)
        path = (ROOT / relative).resolve()
        try:
            path.relative_to(ROOT)
        except ValueError:
            fail("anchor escapes the repository: " + relative)
        if not path.is_file():
            fail("anchor is missing: " + relative)
        if hashlib.sha256(path.read_bytes().replace(b"\r\n", b"\n")).hexdigest() != digest:
            fail("anchor changed; review and re-anchor: " + relative)
    source = (ROOT / "internal/domain/host/file.go").read_text(encoding="utf-8")
    tests = (ROOT / "internal/domain/host/file_test.go").read_text(encoding="utf-8")
    if "os.Lstat(current)" not in source or "ModeSymlink" not in source:
        fail("path resolver no longer inspects existing components without following links")
    if "TestUserScopedFileRejectsSymlinkedParent" not in tests:
        fail("symlink escape regression test is missing")
    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    if "test: check-host-file-confinement" not in makefile or "check_managed_host_file_confinement.py" not in makefile:
        fail("Makefile test target no longer runs the invariant verifier")
    if "test-all-modules: test" not in makefile or "make test-all-modules" not in (ROOT / ".github/workflows/ci.yml").read_text(encoding="utf-8"):
        fail("CI no longer reaches the invariant verifier")
    print("managed host file path boundary: PASS")


if __name__ == "__main__":
    main()
