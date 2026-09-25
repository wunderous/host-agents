#!/usr/bin/env python3
"""Verify pinned provenance across host-local provider bootstrap recipes."""
from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECISION = ROOT / ".agents" / "decisions" / "provider-activation-recipe-provenance.json"


def fail(message: str) -> None:
    print("provider activation recipe provenance check failed: " + message, file=sys.stderr)
    raise SystemExit(1)


def read(relative: str) -> str:
    try:
        return (ROOT / relative).read_text(encoding="utf-8")
    except OSError as error:
        fail("cannot read " + relative + ": " + str(error))


def main() -> None:
    try:
        decision = json.loads(DECISION.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as error:
        fail("cannot read decision record: " + str(error))

    required = (
        "schemaVersion",
        "id",
        "claim",
        "owner",
        "authority",
        "scope",
        "exception",
        "evidence",
        "revisionBehavior",
        "anchors",
        "enforcedBy",
    )
    for field in required:
        if field not in decision:
            fail("decision is missing " + field)
    if decision["id"] != "provider-activation-recipe-provenance":
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
        content = path.read_bytes().replace(b"\r\n", b"\n")
        if hashlib.sha256(content).hexdigest() != digest:
            fail("anchor changed; review and re-anchor: " + relative)

    for relative, wanted_version in (
        ("plugins/kubernetes/k3s/recipes/install.yaml", "2.0.1"),
        ("plugins/tunneling/tailscale/recipes/install.yaml", "2.0.0"),
    ):
        source = read(relative)
        if f"recipeVersion: {wanted_version}" not in source:
            fail(relative + " has an unexpected recipe version")
        if "activationRecipeRevision" not in source or 'pattern: "^[0-9a-f]{40}$"' not in source:
            fail(relative + " lacks the required immutable revision input")
        if "revision: ${vars.inputs.activationRecipeRevision}" not in source:
            fail(relative + " does not pass its revision to opute.provider.install")
        for argument in ("recipeSource", "sha256"):
            if argument + ": ${vars.inputs.activationRecipe" not in source:
                fail(relative + " does not pass activation recipe " + argument)

    k3s = read("plugins/kubernetes/k3s/recipes/install.yaml")
    plugin = read("plugins/kubernetes/k3s/plugin.yaml")
    plugin_version = next((line.split(":", 1)[1].strip() for line in plugin.splitlines() if line.startswith("version:")), None)
    if plugin_version is None:
        fail("K3s plugin descriptor has no version")
    if "serviceState:\n    default: start" not in k3s or "schema: {type: string, enum: [start, restart]}" not in k3s:
        fail("K3s install does not default to start and constrain explicit service restarts")
    if "state: ${vars.inputs.serviceState}" not in k3s:
        fail("K3s install does not pass the explicit service state to the typed action")
    if f"- {{path: /generation/Provider/version, op: eq, value: {plugin_version}}}" not in k3s:
        fail("K3s install does not wait for the active provider version selected by plugin.yaml")

    parser = read("internal/recipe/recipe.go")
    if "GitHub source revision disagrees with URL" not in parser:
        fail("explicit source revision mismatch rejection is missing")
    recipe_tests = read("internal/recipe/provider_install_recipe_test.go")
    if "TestProviderInstallRecipesPassPinnedActivationProvenance" not in recipe_tests:
        fail("resolved provider install provenance regression is missing")
    parser_tests = read("internal/recipe/recipe_test.go")
    if "TestRawGitHubRecipeSourceRequiresMatchingExplicitRevision" not in parser_tests:
        fail("source revision mismatch regression is missing")

    makefile = read("Makefile")
    if "test: check-host-file-confinement check-provider-activation-recipe-provenance" not in makefile:
        fail("normal test target does not run the provenance verifier")
    if "check-provider-activation-recipe-provenance:\n\tpython3 scripts/check_provider_activation_recipe_provenance.py" not in makefile:
        fail("Makefile verifier target is missing")
    ci = read(".github/workflows/ci.yml")
    if "make test-all-modules" not in ci:
        fail("CI no longer reaches the normal test target")
    print("provider activation recipe provenance: PASS")


if __name__ == "__main__":
    main()
