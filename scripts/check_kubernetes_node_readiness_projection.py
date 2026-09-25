#!/usr/bin/env python3
"""Verify the K3s provider's Kubernetes node readiness projection invariant."""
from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECISION = ROOT / ".agents" / "decisions" / "kubernetes-node-readiness-projection.json"


def fail(message: str) -> None:
    print("Kubernetes node readiness projection check failed: " + message, file=sys.stderr)
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
    if decision["id"] != "kubernetes-node-readiness-projection":
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

    membership = read("plugins/kubernetes/k3s/cmd/opute-provider-k3s/membership.go")
    snapshot_start = membership.find("func parseClusterNodeSnapshot(")
    if snapshot_start < 0 or "parseNativeMembership(raw)" not in membership[snapshot_start:]:
        fail("cluster node snapshots do not use the authoritative native membership parser")
    if 'node["ready"]' not in membership[snapshot_start:]:
        fail("aggregate readiness is not derived from parsed node readiness")

    main = read("plugins/kubernetes/k3s/cmd/opute-provider-k3s/main.go")
    if ".status.conditions[-1].type" in main or "custom-columns=NAME:.metadata.name,STATUS:" in main:
        fail("condition type is still projected as node readiness")
    list_start = main.find("func listClusters(")
    info_start = main.find("func getClusterInfoWithRunner(")
    version_start = main.find("func parseK3sVersion(")
    if min(list_start, info_start, version_start) < 0:
        fail("cluster list or cluster info projection is missing")
    list_projection = main[list_start:info_start]
    info_projection = main[info_start:version_start]
    for name, projection in (("list-clusters", list_projection), ("get-cluster-info", info_projection)):
        if '"-o", "json"' not in projection or "parseClusterNodeSnapshot" not in projection:
            fail(name + " does not use the shared JSON readiness projection")

    cluster_contract = read("internal/contract/clusterinfo/clusterinfo.go")
    if re.search(r'Roles\s+\[\]string\s+`json:"roles"`', cluster_contract) is None:
        fail("Host Agent cluster inventory does not preserve node roles as a string array")
    cluster_parser = read("internal/domain/cluster/cluster_discovery.go")
    if 'Roles: []string{"control-plane"}' not in cluster_parser:
        fail("cluster detail fallback does not use the shared role-array contract")
    kubernetes_provider = read("internal/domain/kubernetes/provider_test.go")
    if "TestListKubernetesClustersPreservesMultipleNodeRoles" not in kubernetes_provider:
        fail("missing Host Agent list-clusters role-array regression")
    cluster_tests = read("internal/domain/cluster/cluster_discovery_test.go")
    if "TestClusterRuntimeProjectionPreservesMultipleNodeRoles" not in cluster_tests:
        fail("missing Host Agent runtime-detail role-array regression")

    tests = read("plugins/kubernetes/k3s/cmd/opute-provider-k3s/main_test.go")
    for test_name in (
        "TestParseClusterNodeSnapshotUsesReadyCondition",
        "TestClusterInfoProjectsActualNodeReadiness",
    ):
        if test_name not in tests:
            fail("missing focused regression " + test_name)
    plugin = read("plugins/kubernetes/k3s/plugin.yaml")
    recipe = read("plugins/kubernetes/k3s/recipes/install.yaml")
    plugin_version = next((line.split(":", 1)[1].strip() for line in plugin.splitlines() if line.startswith("version:")), None)
    recipe_lines = recipe.splitlines()
    plugin_id_line = next((index for index, line in enumerate(recipe_lines) if "pluginId: ${vars.inputs.providerId}" in line), None)
    recipe_version = None
    if plugin_id_line is not None:
        recipe_version = next((line.strip().split(":", 1)[1].strip() for line in recipe_lines[plugin_id_line + 1:] if line.strip().startswith("version:")), None)
    if plugin_version is None or recipe_version is None or plugin_version != recipe_version:
        fail("plugin catalog and install recipe do not select the same provider version")
    if f'Provider: providercontract.ProviderRef{{ID: "com.opute.k3s", Version: "{plugin_version}"}}' not in main:
        fail("provider manifest code does not report the catalog provider version")
    wire_version = re.search(r'\bproviderVersion\s*=\s*"([^"]+)"', main)
    if (
        wire_version is None
        or wire_version.group(1) != plugin_version
        or "Version: providerVersion" not in main
        or '"version": providerVersion' not in main
    ):
        fail("MCP and HTTP provider metadata do not report the catalog provider version")
    if f'ProviderRef{{ID: "com.opute.k3s", Version: "{plugin_version}"}}' not in tests:
        fail("provider manifest regression does not validate the catalog provider version")
    if "TestK3sHTTPHandlerAdvertisesModernProviderProtocol" not in tests or "response.Result.Meta.ServerInfo.Version != providerVersion" not in tests:
        fail("HTTP discovery regression does not validate the wire provider version")
    package = json.loads(read("npm/local-host-agent/package.json"))
    makefile = read("Makefile")
    host_agent_version = next((line.split("?=", 1)[1].strip() for line in makefile.splitlines() if line.startswith("VERSION ?=")), None)
    if not isinstance(package.get("version"), str) or package["version"] != host_agent_version:
        fail("Host Agent release package version and Makefile default do not match")
    publish = read(".github/workflows/publish.yml")
    if 'test "$package_version" = "$version"' not in publish or "opute-provider-k3s-linux-x64" not in publish:
        fail("release workflow does not enforce the tag version and publish the K3s artifact")

    agents = read("AGENTS.md")
    if ".agents/decisions/kubernetes-node-readiness-projection.json" not in agents:
        fail("AGENTS.md does not route K3s readiness work to this decision")
    skill = read(".agents/skills/host-agent-boundaries/SKILL.md")
    if "../decisions/kubernetes-node-readiness-projection.json" not in skill:
        fail("host-agent-boundaries skill does not route readiness work to this decision")

    makefile = read("Makefile")
    if "check-kubernetes-node-readiness-projection" not in makefile.splitlines()[0]:
        fail("readiness verifier target is not declared phony")
    if "test: check-host-file-confinement check-provider-activation-recipe-provenance check-kubernetes-node-readiness-projection" not in makefile:
        fail("normal test target does not run the readiness verifier")
    if "check-kubernetes-node-readiness-projection:\n\tpython3 scripts/check_kubernetes_node_readiness_projection.py" not in makefile:
        fail("Makefile readiness verifier target is missing")
    ci = read(".github/workflows/ci.yml")
    if "make test-all-modules" not in ci:
        fail("CI no longer reaches the normal test target")
    print("Kubernetes node readiness projection: PASS")


if __name__ == "__main__":
    main()
