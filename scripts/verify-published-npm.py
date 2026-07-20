#!/usr/bin/env python3
"""Clean public-package canary for @opute/host-agent.

This script intentionally uses npm exec from a temporary cache and never
imports code from the repository. It validates the public package, release
binary, Streamable HTTP contract, read-only default, and disposable Incus
lifecycle.
"""

from __future__ import annotations

import argparse
import json
import os
import platform
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


PACKAGE = "@opute/host-agent"
PROTOCOL_VERSION = "2024-11-05"


def fail(message: str) -> None:
    raise RuntimeError(message)


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def public_env(overrides: dict[str, str]) -> dict[str, str]:
    env = {
        key: value
        for key, value in os.environ.items()
        if not key.startswith("OPUTE_")
        and key not in {"MCP_AUTH_TOKEN", "BRIDGE_TOKEN", "NPM_TOKEN", "NODE_AUTH_TOKEN"}
    }
    env.update(overrides)
    return env


def run_launcher(
    npm: str,
    version: str,
    cache: Path,
    npmrc: Path,
    env: dict[str, str],
    *arguments: str,
) -> subprocess.CompletedProcess[str]:
    command = [
        npm,
        "exec",
        "--yes",
        "--cache",
        str(cache),
        "--userconfig",
        str(npmrc),
        "--package",
        f"{PACKAGE}@{version}",
        "--",
        "opute-host-agent",
        *arguments,
    ]
    return subprocess.run(command, env=env, text=True, capture_output=True, timeout=120)


def http_json(url: str, timeout: float = 2.0) -> dict[str, Any]:
    with urllib.request.urlopen(url, timeout=timeout) as response:
        payload = json.loads(response.read().decode("utf-8"))
    if not isinstance(payload, dict):
        fail(f"unexpected JSON response from {url}: {payload!r}")
    return payload


def wait_health(url: str, timeout: float = 20.0) -> None:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        try:
            payload = http_json(url)
            if payload.get("ok") is True:
                return
            last_error = repr(payload)
        except Exception as error:  # noqa: BLE001 - probe until ready
            last_error = str(error)
        time.sleep(0.1)
    fail(f"timed out waiting for {url}: {last_error}")


class MCPClient:
    def __init__(self, url: str) -> None:
        self.url = url
        self.session_id: str | None = None
        self.protocol_version = PROTOCOL_VERSION
        self.next_id = 1

    def post(self, payload: dict[str, Any], timeout: float = 60.0) -> dict[str, Any]:
        headers = {
            "Accept": "application/json, text/event-stream",
            "Content-Type": "application/json",
            "Mcp-Protocol-Version": self.protocol_version,
        }
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        request = urllib.request.Request(
            self.url,
            data=json.dumps(payload).encode("utf-8"),
            method="POST",
            headers=headers,
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                session_id = response.headers.get("Mcp-Session-Id")
                if session_id:
                    self.session_id = session_id
                body = response.read().decode("utf-8")
        except urllib.error.HTTPError as error:
            fail(f"MCP HTTP {error.code}: {error.read().decode('utf-8', errors='replace')}")
        if not body.strip():
            return {}
        if body.lstrip().startswith("{"):
            parsed = json.loads(body)
            return parsed if isinstance(parsed, dict) else {}
        for line in body.splitlines():
            if line.startswith("data:"):
                data = line[5:].strip()
                if data and data != "[DONE]":
                    parsed = json.loads(data)
                    if isinstance(parsed, dict):
                        return parsed
        fail(f"unable to parse MCP response: {body[:500]!r}")

    def initialize(self) -> dict[str, Any]:
        result = self.post({
            "jsonrpc": "2.0",
            "id": self.next_id,
            "method": "initialize",
            "params": {
                "protocolVersion": PROTOCOL_VERSION,
                "capabilities": {},
                "clientInfo": {"name": "published-npm-canary", "version": "1"},
            },
        })
        self.next_id += 1
        if "error" in result:
            fail(f"initialize failed: {result['error']}")
        negotiated = result.get("result", {}).get("protocolVersion")
        if isinstance(negotiated, str) and negotiated:
            self.protocol_version = negotiated
        self.post({"jsonrpc": "2.0", "method": "notifications/initialized"})
        if not self.session_id:
            fail("initialize did not return Mcp-Session-Id")
        return result.get("result", {})

    def rpc(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        payload: dict[str, Any] = {"jsonrpc": "2.0", "id": self.next_id, "method": method}
        self.next_id += 1
        if params is not None:
            payload["params"] = params
        response = self.post(payload)
        if "error" in response:
            fail(f"{method} failed: {response['error']}")
        return response.get("result", {})

    def call_tool(self, name: str, arguments: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.rpc("tools/call", {"name": name, "arguments": arguments or {}})


def structured(result: dict[str, Any]) -> dict[str, Any]:
    value = result.get("structuredContent", {})
    return value if isinstance(value, dict) else {}


def tool(session: MCPClient, name: str, arguments: dict[str, Any] | None = None) -> dict[str, Any]:
    result = session.call_tool(name, arguments)
    if result.get("isError"):
        fail(f"{name} returned an error: {result}")
    return structured(result)


def operation_id(result: dict[str, Any]) -> str:
    value = result.get("taskId") or result.get("operationId")
    if not value:
        fail(f"operation response has no taskId/operationId: {result}")
    return str(value)


def wait_operation(session: MCPClient, identifier: str, timeout: float = 600.0) -> dict[str, Any]:
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = tool(session, "get_operation", {"operationId": identifier})
        status = str(result.get("status", "")).lower()
        if status in {"completed", "failed", "cancelled", "canceled", "unknown"}:
            if status != "completed":
                fail(f"operation {identifier} ended with {status}: {result}")
            return result
        time.sleep(2)
    fail(f"operation {identifier} timed out")


def vm_names(session: MCPClient) -> set[str]:
    result = tool(session, "list_vms", {"fast": True})
    return {
        str(item.get("name"))
        for item in result.get("vms", [])
        if isinstance(item, dict)
    }


def stop_launcher(npm: str, version: str, cache: Path, npmrc: Path, env: dict[str, str]) -> None:
    result = run_launcher(npm, version, cache, npmrc, env, "stop")
    if result.returncode != 0:
        fail(f"launcher stop failed: {result.stdout}\n{result.stderr}")


def assert_launcher_status(npm: str, version: str, cache: Path, npmrc: Path, env: dict[str, str]) -> None:
    result = run_launcher(npm, version, cache, npmrc, env, "status")
    if result.returncode != 0:
        fail(f"launcher status failed: {result.stdout}\n{result.stderr}")
    try:
        status = json.loads(result.stdout)
    except json.JSONDecodeError as error:
        fail(f"launcher status was not JSON: {result.stdout!r} ({error})")
    if status.get("running") is not True or status.get("healthy") is not True:
        fail(f"launcher status was not healthy: {status}")


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("version", nargs="?", help="published package version; defaults to npm latest")
    args = parser.parse_args()
    npm = shutil.which("npm")
    if not npm:
        fail("npm is required")
    if platform.system() != "Linux":
        fail("the public Incus canary must run on Linux or WSL")
    if not shutil.which("incus"):
        fail("incus is required for the mutation lifecycle")

    with tempfile.TemporaryDirectory(prefix="opute-published-npm-canary-") as temp:
        root = Path(temp)
        cache = root / "npm-cache"
        npmrc = root / "npmrc"
        npmrc.write_text("registry=https://registry.npmjs.org/\n", encoding="utf-8")
        base_env = public_env({
            "NPM_CONFIG_USERCONFIG": str(npmrc),
            "NPM_CONFIG_CACHE": str(cache),
            "OPUTE_HOST_AGENT_CACHE_DIR": str(root / "agent-cache"),
            "OPUTE_LOCAL_HOST_AGENT_RUNTIME_DIR": str(root / "runtime"),
            "OPUTE_STANDALONE_STATE_DIR": str(root / "state"),
            "HOST_MCP_BIND_HOST": "127.0.0.1",
            "HOST_MCP_PORT": str(free_port()),
        })
        version = args.version
        if not version:
            lookup = subprocess.run(
                [npm, "view", PACKAGE, "version", "--userconfig", str(npmrc)],
                env=base_env,
                text=True,
                capture_output=True,
                check=True,
                timeout=60,
            )
            version = lookup.stdout.strip()
        if not version:
            fail("npm latest returned an empty version")

        machine = platform.machine().lower()
        if machine in {"x86_64", "amd64"}:
            binary_name = "host-agent-linux-x64"
        elif machine in {"aarch64", "arm64"}:
            binary_name = "host-agent-linux-arm64"
        else:
            fail(f"unsupported Linux architecture for the public canary: {machine}")
        binary = root / "agent-cache" / version / binary_name

        try:
            start = run_launcher(npm, version, cache, npmrc, base_env, "start", "--background")
            if start.returncode != 0:
                fail(f"public npm launcher start failed: {start.stdout}\n{start.stderr}")
            health_url = f"http://127.0.0.1:{base_env['HOST_MCP_PORT']}/health"
            mcp_url = f"http://127.0.0.1:{base_env['HOST_MCP_PORT']}/mcp"
            wait_health(health_url)
            assert_launcher_status(npm, version, cache, npmrc, base_env)
            if not binary.is_file() or not Path(f"{binary}.verified.json").is_file():
                fail(f"launcher did not leave a verified public binary at {binary}")
            cli_version = subprocess.run([str(binary), "--version"], text=True, capture_output=True, check=True, timeout=10).stdout.strip()
            if cli_version != version:
                fail(f"CLI version {cli_version!r} does not match npm version {version!r}")

            session = MCPClient(mcp_url)
            initialize = session.initialize()
            server_info = initialize.get("serverInfo", {})
            if server_info.get("name") != "host-agent" or server_info.get("version") != version:
                fail(f"unexpected serverInfo: {server_info}")
            listed = session.rpc("tools/list", {})
            names = {str(item.get("name")) for item in listed.get("tools", []) if isinstance(item, dict)}
            required = {"check_local_prerequisites", "get_local_status", "list_vms", "create_vm", "get_operation"}
            if not required <= names:
                fail(f"public npm tools missing: {sorted(required - names)}")
            if {"register_host_agent", "host_agent_heartbeat", "agent_shell"} & names:
                fail("public npm standalone catalog leaked platform/shell tools")
            for name in ("check_local_prerequisites", "get_local_status", "list_vms"):
                tool(session, name)
            denied = session.call_tool("create_vm", {"vmName": "opute-published-npm-readonly-denied"})
            if denied.get("isError") is not True:
                fail(f"default mutation policy did not deny create_vm: {denied}")
            stop_launcher(npm, version, cache, npmrc, base_env)

            mutation_env = dict(base_env)
            mutation_env["OPUTE_STANDALONE_ALLOW_MUTATIONS"] = "true"
            start = run_launcher(npm, version, cache, npmrc, mutation_env, "start", "--background")
            if start.returncode != 0:
                fail(f"mutation-enabled launcher start failed: {start.stdout}\n{start.stderr}")
            wait_health(health_url)
            session = MCPClient(mcp_url)
            session.initialize()
            vm_name = f"opute-published-npm-e2e-{int(time.time())}"
            created = False
            removed = False
            try:
                if vm_name in vm_names(session):
                    fail(f"unique VM name already exists: {vm_name}")
                created = True
                create = tool(session, "create_vm", {"vmName": vm_name, "image": "ubuntu:22.04", "cpus": 1, "memory": "1GiB"})
                wait_operation(session, operation_id(create))
                if vm_name not in vm_names(session):
                    fail(f"created VM missing from list_vms: {vm_name}")
                if tool(session, "get_vm_info", {"vmName": vm_name, "fast": True}).get("name") != vm_name:
                    fail(f"get_vm_info did not return {vm_name}")
                delete = tool(session, "delete_vm", {"vmName": vm_name})
                wait_operation(session, operation_id(delete))
                removed = True
                if vm_name in vm_names(session):
                    fail(f"deleted VM remains in list_vms: {vm_name}")
                incus = subprocess.run(["incus", "list", "--format", "csv"], text=True, capture_output=True, check=True, timeout=30).stdout
                if vm_name in incus:
                    fail(f"deleted VM remains in incus list: {vm_name}")
                print(f"public npm canary passed: {PACKAGE}@{version}, {len(names)} tools, {vm_name}")
            finally:
                if created and not removed:
                    try:
                        delete = tool(session, "delete_vm", {"vmName": vm_name})
                        wait_operation(session, operation_id(delete), timeout=300)
                    except Exception as error:  # noqa: BLE001 - preserve original failure
                        print(f"cleanup failed for {vm_name}: {error}", file=os.sys.stderr)
                stop_launcher(npm, version, cache, npmrc, mutation_env)
        except Exception:
            try:
                stop_launcher(npm, version, cache, npmrc, base_env)
            except Exception:
                pass
            raise


if __name__ == "__main__":
    try:
        main()
    except Exception as error:  # noqa: BLE001 - top-level canary diagnostic
        print(f"public npm canary failed: {error}", file=os.sys.stderr)
        raise SystemExit(1)
