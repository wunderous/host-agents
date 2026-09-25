#!/usr/bin/env python3
"""Fail closed when public tutorial and catalog claims drift from a tested release."""
from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECISION_PATH = ROOT / ".agents" / "decisions" / "public-documentation-release-parity.json"
PACKAGE_PATH = ROOT / "npm" / "local-host-agent" / "package.json"
CATALOG_PATH = ROOT / "site" / "context" / "release-catalog.json"
REQUIRED_CHECKS = {
    "explicitIdentity",
    "openHealth",
    "invalidTokenRejected",
    "authenticatedDiscovery",
    "authenticatedToolsList",
    "structuredGetHostInfo",
    "readOnly",
}
ALLOWED_CANARY_KEYS = {
    "packageVersion",
    "catalogRevision",
    "sourceSha",
    "runId",
    "runAttempt",
    "checks",
}
ALLOWED_TOOL_KEYS = {
    "name",
    "title",
    "description",
    "version",
    "capabilityId",
    "effect",
    "idempotent",
    "requiresApproval",
    "inputSchema",
    "outputSchema",
}


def fail(message: str) -> None:
    print("site release parity failed: " + message, file=sys.stderr)
    raise SystemExit(1)


def load_json(path: Path, label: str) -> dict:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail(label + " is missing or invalid (" + type(error).__name__ + ")")
    if not isinstance(value, dict):
        fail(label + " must be a JSON object")
    return value


def verify_decision(decision: dict) -> None:
    required = {
        "schemaVersion",
        "id",
        "title",
        "claim",
        "decidedOn",
        "section",
        "owner",
        "authority",
        "scope",
        "exception",
        "evidence",
        "revisionBehavior",
        "anchors",
        "verify",
        "enforcedBy",
        "supersedes",
        "withdrawn",
        "lastVerified",
    }
    missing = sorted(required - decision.keys())
    if missing:
        fail("decision is missing required invariant fields: " + ", ".join(missing))
    if decision["schemaVersion"] != 1 or decision["id"] != "public-documentation-release-parity":
        fail("decision identity or schema version is invalid")
    if decision["verify"] != "python3 scripts/check_site_release_parity.py":
        fail("decision does not point to this verifier")
    if decision["withdrawn"] is not False:
        fail("active release parity decision is withdrawn")
    if not isinstance(decision["owner"], str) or not decision["owner"].strip():
        fail("decision has no named owner")
    if not isinstance(decision["authority"], dict) or not decision["authority"]:
        fail("decision has no named authority")
    for name, relative in decision["authority"].items():
        if not isinstance(relative, str) or not (ROOT / relative).is_file():
            fail("decision authority path is missing: " + str(name))
    if not isinstance(decision["scope"], dict) or not decision["scope"].get("included") or not decision["scope"].get("excluded"):
        fail("decision scope must list included and excluded responsibilities")
    if not isinstance(decision["exception"], str) or not decision["exception"].strip():
        fail("decision has no explicit exception or supersession path")
    if (
        not isinstance(decision["evidence"], dict)
        or not decision["evidence"].get("required")
        or decision["evidence"].get("siteVerifier") != "scripts/check_site_release_parity.py"
    ):
        fail("decision has no checkable evidence contract")
    if not isinstance(decision["revisionBehavior"], str) or not decision["revisionBehavior"].strip():
        fail("decision has no revision behavior")
    if not isinstance(decision["anchors"], list) or not decision["anchors"]:
        fail("decision has no content anchors")
    seen = set()
    for anchor in decision["anchors"]:
        if not isinstance(anchor, dict) or anchor.get("kind") != "file":
            fail("decision anchors must be whole-file anchors")
        relative = anchor.get("path")
        digest = anchor.get("digest")
        if not isinstance(relative, str) or not re.fullmatch(r"[0-9a-f]{64}", str(digest)):
            fail("decision anchor has an invalid path or SHA-256 digest")
        if relative in seen:
            fail("decision repeats anchor path " + relative)
        seen.add(relative)
        path = (ROOT / relative).resolve()
        try:
            path.relative_to(ROOT)
        except ValueError:
            fail("decision anchor escapes the repository: " + relative)
        if not path.is_file():
            fail("decision anchor is missing: " + relative)
        actual = hashlib.sha256(path.read_bytes().replace(b"\r\n", b"\n")).hexdigest()
        if actual != digest:
            fail("decision anchor changed: " + relative + " (review and re-anchor explicitly)")
    if not isinstance(decision["enforcedBy"], list) or not decision["enforcedBy"]:
        fail("decision has no enforcement paths")
    for relative in decision["enforcedBy"]:
        if not isinstance(relative, str) or not (ROOT / relative).is_file():
            fail("decision enforcement path is missing: " + str(relative))


def verify_enforcement_wiring() -> None:
    makefile = (ROOT / "Makefile").read_text(encoding="utf-8")
    if "check-site:" not in makefile:
        fail("Makefile does not define check-site")
    check_site = makefile.split("check-site:", 1)[1].split("\n\n", 1)[0]
    required_make_steps = (
        "scripts/check_site_release_boundary.py",
        "scripts/check_site_release_parity.py",
        "scripts/test_promote_site_release_catalog.py",
        "scripts/test_validate_generated_site.py",
        "scripts/test_check_generated_site_clean.py",
        "scripts/validate-generated-site.py",
        "scripts/check_generated_site_clean.py",
    )
    for step in required_make_steps:
        if step not in check_site:
            fail("check-site no longer enforces " + step)
    for relative in (".github/workflows/ci.yml", ".github/workflows/publish-site-image.yml"):
        workflow = (ROOT / relative).read_text(encoding="utf-8")
        if "make check-site" not in workflow:
            fail(relative + " no longer runs make check-site")
    clean_checker = (ROOT / "scripts/check_generated_site_clean.py").read_text(encoding="utf-8")
    if any(
        token not in clean_checker
        for token in ('"git"', '"diff"', '"ls-files"', '"--others"', '"--exclude-standard"')
    ):
        fail("generated-output gate must detect untracked files as well as tracked drift")


def verify_archived_catalogs(current_version: str) -> None:
    archive_dir = ROOT / "site" / "context" / "release-archives"
    paths = sorted(archive_dir.glob("v*.json"))
    if not paths:
        fail("no archived release catalog snapshots are maintained")
    versions = set()
    for path in paths:
        if not re.fullmatch(r"v\d+\.\d+\.\d+\.json", path.name):
            fail("archived release catalog filename is invalid: " + path.name)
        archive = load_json(path, "archived release catalog")
        version = archive.get("packageVersion")
        revision = archive.get("catalogRevision")
        if (
            archive.keys() - {"packageName", "packageVersion", "releaseChannel", "catalogRevision", "toolCount", "tools", "publishedCanary"}
            or archive.get("packageName") != "@opute/host-agent"
            or not isinstance(version, str)
            or path.name != "v" + version + ".json"
            or version == current_version
            or version in versions
            or archive.get("releaseChannel") != "stable"
            or not isinstance(revision, str)
            or not re.fullmatch(r"sha256:[0-9a-f]{64}", revision)
        ):
            fail("archived release catalog identity or channel is invalid: " + path.name)
        versions.add(version)
        tools = archive.get("tools")
        if not isinstance(tools, list) or not tools or type(archive.get("toolCount")) is not int or archive["toolCount"] != len(tools):
            fail("archived catalog toolCount does not match its descriptors: " + version)
        names = []
        for tool in tools:
            if not isinstance(tool, dict) or tool.keys() - ALLOWED_TOOL_KEYS:
                fail("archived catalog contains an unapproved descriptor: " + version)
            name = tool.get("name")
            if not isinstance(name, str) or not name:
                fail("archived catalog descriptor has no name: " + version)
            names.append(name)
            if (
                tool.get("effect") not in {"read", "mutation", "destructive", "credential_bearing"}
                or not isinstance(tool.get("description"), str)
                or not isinstance(tool.get("idempotent"), bool)
                or not isinstance(tool.get("inputSchema"), dict)
                or tool["inputSchema"].get("type") != "object"
            ):
                fail("archived catalog descriptor is missing public typed metadata: " + name)
        if names != sorted(names) or len(names) != len(set(names)) or "get_host_info" not in names:
            fail("archived catalog names are not unique, sorted, or missing get_host_info: " + version)
        evidence = archive.get("publishedCanary")
        if (
            not isinstance(evidence, dict)
            or evidence.keys() - ALLOWED_CANARY_KEYS
            or evidence.get("packageVersion") != version
            or evidence.get("catalogRevision") != revision
            or not re.fullmatch(r"[0-9a-f]{40}", str(evidence.get("sourceSha", "")))
            or type(evidence.get("runId")) is not int
            or evidence.get("runId", 0) < 1
            or type(evidence.get("runAttempt")) is not int
            or evidence.get("runAttempt", 0) < 1
            or not isinstance(evidence.get("checks"), dict)
            or evidence["checks"].keys() != REQUIRED_CHECKS
            or any(evidence["checks"].get(check) is not True for check in REQUIRED_CHECKS)
        ):
            fail("archived catalog lacks matching published read-only canary evidence: " + version)
        route = ROOT / "site" / "public" / "docs" / "versions" / ("v" + version) / "capabilities"
        published = load_json(route / "catalog.json", "generated archived catalog")
        if published != archive:
            fail("generated archived catalog differs from its immutable source snapshot: " + version)
        try:
            page = (route / "index.html").read_text(encoding="utf-8")
        except OSError:
            fail("generated archived capability page is missing: " + version)
        if "Archived verified release" not in page or version not in page or revision not in page:
            fail("generated archived capability page does not identify its verified release: " + version)
        search = load_json(ROOT / "site" / "public" / "search-index.json", "generated search index")
        search_routes = {
            item.get("url") for item in search.get("pages", []) if isinstance(item, dict)
        }
        public_route = "/docs/versions/v" + version + "/capabilities/"
        if public_route not in search_routes:
            fail("archived capability route is missing from search index: " + version)
        try:
            sitemap = (ROOT / "site" / "public" / "sitemap.xml").read_text(encoding="utf-8")
        except OSError:
            fail("generated sitemap is missing")
        if "https://www.opute.io" + public_route not in sitemap:
            fail("archived capability route is missing from sitemap: " + version)


def main() -> None:
    decision = load_json(DECISION_PATH, "release parity decision")
    verify_decision(decision)
    verify_enforcement_wiring()

    package = load_json(PACKAGE_PATH, "npm package metadata")
    catalog = load_json(CATALOG_PATH, "public release catalog")
    version = package.get("version")
    if package.get("name") != "@opute/host-agent" or not isinstance(version, str):
        fail("npm package identity is invalid")
    verify_archived_catalogs(version)
    if catalog.get("packageName") != package["name"] or catalog.get("packageVersion") != version:
        fail("catalog package identity/version differs from npm/local-host-agent/package.json")
    if catalog.get("releaseChannel") not in {"preview", "stable"}:
        fail("catalog releaseChannel must be preview or stable")
    revision = catalog.get("catalogRevision")
    if not isinstance(revision, str) or not re.fullmatch(r"sha256:[0-9a-f]{64}", revision):
        fail("catalogRevision is invalid")
    tools = catalog.get("tools")
    if not isinstance(tools, list) or not tools or type(catalog.get("toolCount")) is not int or catalog.get("toolCount") != len(tools):
        fail("catalog toolCount does not match its descriptors")
    names = []
    for tool in tools:
        if not isinstance(tool, dict) or tool.keys() - ALLOWED_TOOL_KEYS:
            fail("catalog has a non-object descriptor or unapproved public fields")
        name = tool.get("name")
        if not isinstance(name, str) or not name:
            fail("catalog contains a descriptor without a public name")
        names.append(name)
        if tool.get("effect") not in {"read", "mutation", "destructive", "credential_bearing"}:
            fail("catalog descriptor " + name + " has an unknown effect")
        if not isinstance(tool.get("description"), str) or not isinstance(tool.get("idempotent"), bool):
            fail("catalog descriptor " + name + " has incomplete public metadata")
        if not isinstance(tool.get("inputSchema"), dict) or tool["inputSchema"].get("type") != "object":
            fail("catalog descriptor " + name + " has no object input schema")
    if len(names) != len(set(names)):
        fail("catalog contains duplicate tool names")
    if names != sorted(names):
        fail("catalog descriptors are not in deterministic name order")
    if "get_host_info" not in names:
        fail("catalog omits the documented first read-only operation")

    evidence = catalog.get("publishedCanary")
    if evidence is not None:
        if not isinstance(evidence, dict) or evidence.keys() - ALLOWED_CANARY_KEYS:
            fail("published canary evidence contains unapproved fields")
        if (
            evidence.get("packageVersion") != version
            or evidence.get("catalogRevision") != revision
            or not re.fullmatch(r"[0-9a-f]{40}", str(evidence.get("sourceSha", "")))
            or type(evidence.get("runId")) is not int
            or evidence.get("runId", 0) < 1
            or type(evidence.get("runAttempt")) is not int
            or evidence.get("runAttempt", 0) < 1
            or not isinstance(evidence.get("checks"), dict)
            or evidence["checks"].keys() != REQUIRED_CHECKS
            or any(evidence["checks"].get(check) is not True for check in REQUIRED_CHECKS)
        ):
            fail("published canary evidence does not prove this exact package and catalog")
    if catalog["releaseChannel"] == "stable" and evidence is None:
        fail("stable catalog lacks matching published read-only canary evidence")

    route = ROOT / "site" / "public" / "docs" / "versions" / ("v" + version) / "capabilities" / "catalog.json"
    published = load_json(route, "generated versioned catalog")
    if published != catalog:
        fail("generated versioned catalog differs from the source release catalog")
    tutorial_path = ROOT / "site" / "public" / "docs" / "get-started" / "index.html"
    try:
        tutorial = tutorial_path.read_text(encoding="utf-8")
    except OSError:
        fail("generated first-success tutorial is missing")
    if version not in tutorial:
        fail("generated tutorial does not name the selected package version")
    generator_source = (ROOT / "site/scripts/generate-docs.ts").read_text(encoding="utf-8")
    if "host://" in generator_source or '"uri": "host:example:host-01"' not in generator_source:
        fail("site examples must use a parseable canonical host resource id")
    if "npm launcher defaults to" in generator_source or "local-host-agent</code>" in generator_source:
        fail("site docs must not claim the launcher has an identity default")
    for required in ("OPUTE_REMOTE_AGENT_ID", "MCP_AUTH_TOKEN", "tools/list", "get_host_info", "HTTP 401", "lxcBinaryPath", "systemctlPath", "Optional fields such as", "intentionally-wrong", "A request with no Authorization header"):
        if required not in tutorial:
            fail("generated tutorial is missing first-success evidence for " + required)
    if catalog["releaseChannel"] == "preview" and "Preview" not in tutorial:
        fail("unverified package tutorial is not visibly labelled preview")
    if catalog["releaseChannel"] == "stable" and (
        "npx -y @opute/host-agent@" + version not in tutorial
        or "published-package canary has passed" in tutorial
    ):
        fail("stable tutorial does not use the tested pinned npm package")

    print(
        "Verified release parity for "
        + package["name"]
        + "@"
        + version
        + " ("
        + catalog["releaseChannel"]
        + ", "
        + revision
        + ", "
        + str(len(tools))
        + " tools)."
    )


if __name__ == "__main__":
    main()
