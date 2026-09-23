#!/usr/bin/env python3
"""Enforce the public source repository's site release boundary."""
from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
FORBIDDEN_PREFIXES = ("site/recipes/", "site/manifests/")
FORBIDDEN_PATHS = {
    "scripts/deploy_site_via_host_agent.py",
    "docs/ci-site-deployment.md",
}
FORBIDDEN_DEPLOYMENT_CAPABILITIES = (
    "run_host_local_recipe",
    "apply_manifest",
    "execute_host_recipe",
    "OPUTE_REMOTE_AGENT_ID",
)
DEPLOYMENT_COMMANDS = re.compile(
    r"(?im)\b(?:"
    r"kubectl\s+(?:apply|create|delete|patch|replace|rollout|scale|set)|"
    r"helm\s+(?:install|upgrade|rollback|uninstall)|"
    r"incus\s+(?:launch|start|stop|delete|exec)|"
    r"cloudflared\s+tunnel\s+(?:run|create|route|delete)|"
    r"tailscale\s+(?:up|down|serve|funnel|set|logout)"
    r")\b"
)
FORBIDDEN_CREDENTIAL_NAMES = (
    "MCP_AUTH_TOKEN",
    "OPUTE_REMOTE_AGENT_ID",
    "CLOUDFLARE_API_TOKEN",
    "KUBECONFIG",
)


def main() -> int:
    tracked = subprocess.run(
        ["git", "ls-files", "-z"],
        cwd=ROOT,
        check=True,
        capture_output=True,
    ).stdout.decode().split("\0")
    violations = [
        path for path in tracked if path and path.startswith(FORBIDDEN_PREFIXES)
    ]
    violations += [path for path in tracked if path in FORBIDDEN_PATHS]
    for prefix in FORBIDDEN_PREFIXES:
        root = ROOT / prefix.rstrip("/")
        if root.exists():
            violations += [
                path.relative_to(ROOT).as_posix()
                for path in root.rglob("*") if path.is_file()
            ]
    violations += [
        path for path in FORBIDDEN_PATHS if (ROOT / path).exists()
    ]
    if violations:
        print("deployment-owned paths remain in public source:", *violations, sep="\n  ")
        return 1

    workflow_files = sorted((ROOT / ".github/workflows").glob("*.yml"))
    workflow_files += sorted((ROOT / ".github/workflows").glob("*.yaml"))
    for path in workflow_files:
        content = path.read_text(encoding="utf-8")
        if "self-hosted" in content.lower():
            print(f"public workflow requests a self-hosted runner: {path.relative_to(ROOT)}")
            return 1
        for credential in FORBIDDEN_CREDENTIAL_NAMES:
            if credential in content:
                print(f"public workflow references a host deployment credential: {credential}")
                return 1
        if (
            any(capability.lower() in content.lower() for capability in FORBIDDEN_DEPLOYMENT_CAPABILITIES)
            or DEPLOYMENT_COMMANDS.search(content)
        ):
            print(f"public workflow contains a deployment capability: {path.relative_to(ROOT)}")
            return 1

    image_workflow = ROOT / ".github/workflows/publish-site-image.yml"
    if not image_workflow.is_file():
        print("missing .github/workflows/publish-site-image.yml")
        return 1
    content = image_workflow.read_text(encoding="utf-8")
    if "runs-on: ubuntu-latest" not in content or "packages: write" not in content:
        print("site image workflow must use GitHub-hosted CI and scoped package publishing")
        return 1
    print("public site release boundary: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
