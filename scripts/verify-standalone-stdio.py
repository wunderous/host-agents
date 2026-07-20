#!/usr/bin/env python3
"""Minimal packaged standalone MCP smoke test; no platform credentials required."""

import json
import os
import select
import subprocess
import sys
import tempfile
import time


def fail(message):
    raise RuntimeError(message)


def response(process, request_id, timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        ready, _, _ = select.select([process.stdout], [], [], max(0, deadline - time.time()))
        if not ready:
            break
        line = process.stdout.readline()
        if not line:
            break
        value = json.loads(line)
        if value.get("id") == request_id:
            if "error" in value:
                fail(f"MCP request {request_id} failed: {value['error']}")
            return value.get("result", {})
    fail(f"timed out waiting for MCP response {request_id}")


def send(process, request_id, method, params):
    process.stdin.write((json.dumps({"jsonrpc": "2.0", "id": request_id, "method": method, "params": params}) + "\n").encode())
    process.stdin.flush()
    return response(process, request_id)


def main():
    if len(sys.argv) != 2:
        fail("usage: verify-standalone-stdio.py PATH_TO_BINARY")
    state_dir = tempfile.mkdtemp(prefix="opute-standalone-smoke-")
    env = {key: value for key, value in os.environ.items() if not key.startswith("OPUTE_") and key not in {"MCP_AUTH_TOKEN", "BRIDGE_TOKEN"}}
    env.update({
        "OPUTE_AGENT_MODE": "standalone",
        "OPUTE_TRANSPORT": "stdio",
        "OPUTE_INFRA_PROVIDER_ID": "incus",
        "OPUTE_STANDALONE_STATE_DIR": state_dir,
    })
    process = subprocess.Popen([sys.argv[1]], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
    try:
        initialize = send(process, 1, "initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "standalone-release-smoke", "version": "1"},
        })
        if initialize.get("serverInfo", {}).get("name") != "host-agent":
            fail(f"unexpected server identity: {initialize.get('serverInfo')}")
        expected_version = os.environ.get("EXPECTED_VERSION")
        if expected_version and initialize.get("serverInfo", {}).get("version") != expected_version:
            fail(f"unexpected server version: {initialize.get('serverInfo')}")
        process.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
        process.stdin.flush()
        listed = send(process, 2, "tools/list", {})
        names = {tool["name"] for tool in listed.get("tools", [])}
        required = {"check_local_prerequisites", "get_local_status", "list_vms", "create_vm", "get_operation"}
        if not required.issubset(names):
            fail(f"missing standalone tools: {sorted(required - names)}")
        leaked = {"register_host_agent", "host_agent_heartbeat", "dispatch_host_operation", "agent_shell"} & names
        if leaked:
            fail(f"platform/shell tools leaked: {sorted(leaked)}")
        checked = send(process, 3, "tools/call", {"name": "check_local_prerequisites", "arguments": {}})
        if checked.get("isError") or "structuredContent" not in checked:
            fail(f"read-only tool failed: {checked}")
        denied = send(process, 4, "tools/call", {"name": "create_vm", "arguments": {"vmName": "opute-standalone-release-smoke"}})
        if not denied.get("isError"):
            fail(f"mutation was not denied: {denied}")
        print("standalone stdio smoke passed")
    finally:
        process.terminate()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


if __name__ == "__main__":
    try:
        main()
    except Exception as error:
        print(f"standalone stdio smoke failed: {error}", file=sys.stderr)
        sys.exit(1)
