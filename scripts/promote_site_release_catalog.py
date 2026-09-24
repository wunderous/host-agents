#!/usr/bin/env python3
"""Promote the current preview catalog only from exact published canary evidence."""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PACKAGE = ROOT / "npm" / "local-host-agent" / "package.json"
CATALOG = ROOT / "site" / "context" / "release-catalog.json"
CHECKS = {
    "explicitIdentity",
    "openHealth",
    "invalidTokenRejected",
    "authenticatedDiscovery",
    "authenticatedToolsList",
    "structuredGetHostInfo",
    "readOnly",
}


def fail(message: str) -> None:
    raise SystemExit("catalog promotion failed: " + message)


def read_json(path: Path, label: str) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(label + " is missing or invalid (" + type(error).__name__ + ")")
    if not isinstance(value, dict):
        fail(label + " must be a JSON object")
    return value


def main() -> None:
    if len(sys.argv) != 2:
        fail("usage: python3 scripts/promote_site_release_catalog.py EVIDENCE.json")
    evidence = read_json(Path(sys.argv[1]).resolve(), "published canary evidence")
    allowed = {"packageName", "packageVersion", "catalogRevision", "sourceSha", "runId", "runAttempt", "checks"}
    if evidence.keys() - allowed:
        fail("published evidence contains unapproved fields")
    package = read_json(PACKAGE, "npm package metadata")
    catalog = read_json(CATALOG, "source release catalog")
    checks = evidence.get("checks")
    if (
        evidence.get("packageName") != package.get("name")
        or evidence.get("packageVersion") != package.get("version")
        or evidence.get("packageVersion") != catalog.get("packageVersion")
        or evidence.get("catalogRevision") != catalog.get("catalogRevision")
        or not re.fullmatch(r"[0-9a-f]{40}", str(evidence.get("sourceSha", "")))
        or type(evidence.get("runId")) is not int
        or evidence.get("runId", 0) < 1
        or type(evidence.get("runAttempt")) is not int
        or evidence.get("runAttempt", 0) < 1
        or not isinstance(checks, dict)
        or checks.keys() != CHECKS
        or any(checks.get(name) is not True for name in CHECKS)
    ):
        fail("evidence does not match the current package and catalog or lacks a required check")
    try:
        subprocess.run(
            ["git", "merge-base", "--is-ancestor", evidence["sourceSha"], "HEAD"],
            cwd=ROOT,
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except (subprocess.CalledProcessError, FileNotFoundError):
        fail("published source SHA is not an ancestor of this checkout")

    catalog["releaseChannel"] = "stable"
    catalog["publishedCanary"] = {
        key: evidence[key]
        for key in ("packageVersion", "catalogRevision", "sourceSha", "runId", "runAttempt", "checks")
    }
    descriptor, temporary = tempfile.mkstemp(prefix=CATALOG.name + ".", dir=CATALOG.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(catalog, output, indent=2)
            output.write("\n")
        os.replace(temporary, CATALOG)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise
    print(
        "Promoted "
        + evidence["packageName"]
        + "@"
        + evidence["packageVersion"]
        + " using published canary run "
        + str(evidence["runId"])
        + " attempt "
        + str(evidence["runAttempt"])
        + "."
    )


if __name__ == "__main__":
    main()
