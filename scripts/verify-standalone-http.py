#!/usr/bin/env python3
"""Packaged standalone Streamable HTTP MCP smoke test; no platform credentials required."""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import tempfile

from standalone_http_mcp import (
    StreamableHttpSession,
    fail,
    free_port,
    sanitize_env,
    terminate,
    wait_port_closed,
    wait_health,
)


def main() -> None:
    if len(sys.argv) != 2:
        fail("usage: verify-standalone-http.py PATH_TO_BINARY")

    binary = sys.argv[1]
    if not os.path.isfile(binary):
        fail(f"binary not found: {binary}")

    port = free_port()
    state_dir = tempfile.mkdtemp(prefix="opute-standalone-http-smoke-")
    bind_host = "127.0.0.1"
    health = f"http://{bind_host}:{port}/health"
    mcp_url = f"http://{bind_host}:{port}/mcp"
    env = sanitize_env(
        {
            "OPUTE_AGENT_MODE": "standalone",
            "OPUTE_TRANSPORT": "http",
            "OPUTE_INFRA_PROVIDER_ID": "incus",
            "OPUTE_STANDALONE_STATE_DIR": state_dir,
            "HOST_MCP_BIND_HOST": bind_host,
            "HOST_MCP_PORT": str(port),
        }
    )
    process = subprocess.Popen(
        [binary, "--mode=standalone", "--transport=http"],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        env=env,
    )
    try:
        wait_health(health)
        session = StreamableHttpSession(mcp_url)
        initialize = session.initialize("standalone-release-smoke")
        if initialize.get("serverInfo", {}).get("name") != "host-agent":
            fail(f"unexpected server identity: {initialize.get('serverInfo')}")
        expected_version = os.environ.get("EXPECTED_VERSION")
        if expected_version and initialize.get("serverInfo", {}).get("version") != expected_version:
            fail(f"unexpected server version: {initialize.get('serverInfo')}")
        listed = session.rpc("tools/list", {})
        names = {tool["name"] for tool in listed.get("tools", [])}
        required = {
            "check_local_prerequisites",
            "get_local_status",
            "list_vms",
            "create_vm",
            "get_operation",
        }
        if not required.issubset(names):
            fail(f"missing standalone tools: {sorted(required - names)}")
        leaked = {
            "register_host_agent",
            "host_agent_heartbeat",
            "dispatch_host_operation",
            "agent_shell",
        } & names
        if leaked:
            fail(f"platform/shell tools leaked: {sorted(leaked)}")
        checked = session.call_tool("check_local_prerequisites")
        if checked.get("isError") or "structuredContent" not in checked:
            fail(f"read-only tool failed: {checked}")
        denied = session.call_tool("create_vm", {"vmName": "opute-standalone-release-smoke"})
        if not denied.get("isError"):
            fail(f"mutation was not denied: {denied}")
        print("standalone Streamable HTTP smoke passed")
    finally:
        try:
            terminate(process)
            wait_port_closed(health)
        finally:
            shutil.rmtree(state_dir, ignore_errors=True)


if __name__ == "__main__":
    try:
        main()
    except Exception as error:  # noqa: BLE001 - top-level gate
        print(f"standalone Streamable HTTP smoke failed: {error}", file=sys.stderr)
        sys.exit(1)
