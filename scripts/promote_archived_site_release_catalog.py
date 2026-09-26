#!/usr/bin/env python3
"""Create an immutable archive from a published release's exact catalog snapshot."""
from __future__ import annotations

import json
import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
ARCHIVE_DIR = ROOT / "site" / "context" / "release-archives"
CATALOG_PATH = "site/context/release-catalog.json"
PACKAGE_PATH = "npm/local-host-agent/package.json"
CHECKS = {
    "explicitIdentity",
    "openHealth",
    "invalidTokenRejected",
    "authenticatedDiscovery",
    "authenticatedToolsList",
    "structuredGetHostInfo",
    "readOnly",
}
CATALOG_KEYS = {
    "packageName",
    "packageVersion",
    "releaseChannel",
    "catalogRevision",
    "toolCount",
    "tools",
    "publishedCanary",
}
EVIDENCE_KEYS = {
    "packageName",
    "packageVersion",
    "catalogRevision",
    "sourceSha",
    "runId",
    "runAttempt",
    "checks",
}


def fail(message: str) -> None:
    raise SystemExit("catalog archive failed: " + message)


def parse_object(raw: str, label: str) -> dict:
    try:
        value = json.loads(raw)
    except json.JSONDecodeError as error:
        fail(label + " is invalid JSON (" + type(error).__name__ + ")")
    if not isinstance(value, dict):
        fail(label + " must be a JSON object")
    return value


def git_source_file(repository: Path, revision: str, path: str, label: str) -> str:
    try:
        result = subprocess.run(
            ["git", "show", revision + ":" + path],
            cwd=repository,
            check=True,
            capture_output=True,
            text=True,
        )
    except (OSError, subprocess.CalledProcessError):
        fail("published source revision does not contain " + label)
    return result.stdout


def verify_source_is_ancestor(repository: Path, revision: str) -> None:
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        fail("published source SHA is invalid")
    try:
        subprocess.run(
            ["git", "merge-base", "--is-ancestor", revision, "HEAD"],
            cwd=repository,
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
    except (OSError, subprocess.CalledProcessError):
        fail("published source SHA is not an ancestor of this checkout")


def archived_catalog(evidence: dict, repository: Path = ROOT) -> dict:
    if evidence.keys() != EVIDENCE_KEYS:
        fail("published evidence has missing or unapproved fields")
    version = evidence.get("packageVersion")
    revision = evidence.get("sourceSha")
    checks = evidence.get("checks")
    if (
        evidence.get("packageName") != "@opute/host-agent"
        or not isinstance(version, str)
        or not re.fullmatch(r"\d+\.\d+\.\d+", version)
        or not isinstance(revision, str)
        or type(evidence.get("runId")) is not int
        or evidence.get("runId", 0) < 1
        or type(evidence.get("runAttempt")) is not int
        or evidence.get("runAttempt", 0) < 1
        or not isinstance(checks, dict)
        or checks.keys() != CHECKS
        or any(checks.get(name) is not True for name in CHECKS)
    ):
        fail("published evidence does not contain the exact required release and canary checks")

    verify_source_is_ancestor(repository, revision)
    source_catalog = parse_object(
        git_source_file(repository, revision, CATALOG_PATH, "release catalog"),
        "published release catalog",
    )
    source_package = parse_object(
        git_source_file(repository, revision, PACKAGE_PATH, "npm package metadata"),
        "published npm package metadata",
    )
    if (
        source_catalog.keys() != CATALOG_KEYS - {"publishedCanary"}
        or source_catalog.get("packageName") != evidence.get("packageName")
        or source_catalog.get("packageVersion") != version
        or source_catalog.get("catalogRevision") != evidence.get("catalogRevision")
        or source_catalog.get("releaseChannel") != "preview"
        or "publishedCanary" in source_catalog
        or source_package.get("name") != evidence.get("packageName")
        or source_package.get("version") != version
        or not re.fullmatch(r"sha256:[0-9a-f]{64}", str(source_catalog.get("catalogRevision", "")))
        or not isinstance(source_catalog.get("toolCount"), int)
        or not isinstance(source_catalog.get("tools"), list)
        or source_catalog.get("toolCount") != len(source_catalog.get("tools", []))
    ):
        fail("published evidence does not match the exact source revision's preview catalog and package")

    source_sha = revision
    return {
        **source_catalog,
        "releaseChannel": "stable",
        "publishedCanary": {
            "packageVersion": version,
            "catalogRevision": evidence["catalogRevision"],
            "sourceSha": source_sha,
            "runId": evidence["runId"],
            "runAttempt": evidence["runAttempt"],
            "checks": checks,
        },
    }


def write_json_atomic(path: Path, value: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temporary = tempfile.mkstemp(prefix=path.name + ".", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8") as output:
            json.dump(value, output, indent=2)
            output.write("\n")
        os.replace(temporary, path)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


def promote_archive(evidence_path: Path, repository: Path = ROOT, archive_dir: Path = ARCHIVE_DIR) -> Path:
    evidence = parse_object(evidence_path.read_text(encoding="utf-8"), "published canary evidence")
    catalog = archived_catalog(evidence, repository)
    destination = archive_dir / ("v" + catalog["packageVersion"] + ".json")
    if destination.exists():
        existing = parse_object(destination.read_text(encoding="utf-8"), "existing release archive")
        if existing != catalog:
            fail("existing archive conflicts with published evidence for " + catalog["packageVersion"])
        return destination
    write_json_atomic(destination, catalog)
    return destination


def main() -> None:
    if len(sys.argv) != 2:
        fail("usage: python3 scripts/promote_archived_site_release_catalog.py EVIDENCE.json")
    destination = promote_archive(Path(sys.argv[1]).resolve())
    print("Archived verified published catalog at " + str(destination))


if __name__ == "__main__":
    main()
