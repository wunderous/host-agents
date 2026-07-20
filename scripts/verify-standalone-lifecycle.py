#!/usr/bin/env python3
"""Disposable standalone MCP/Incus VM lifecycle gate over Streamable HTTP.

The binary is expected to be a Linux build. The test enables mutations only
for this process and always attempts to delete its uniquely named VM.
"""

from __future__ import annotations

import json
import shutil
import subprocess
import sys
import tempfile
import time

from standalone_http_mcp import (
    StreamableHttpSession,
    fail,
    free_port,
    sanitize_env,
    terminate,
    wait_health,
    wait_port_closed,
)


def wait_operation(session: StreamableHttpSession, operation_id: str, timeout: float = 600.0) -> dict:
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = session.call_tool("get_operation", {"operationId": operation_id})
        if result.get("isError"):
            fail(f"get_operation failed: {result}")
        content = result.get("structuredContent", {})
        status = str(content.get("status", "")).lower()
        if status in {"completed", "failed", "cancelled", "canceled"}:
            if status != "completed":
                fail(f"operation {operation_id} ended with {status}: {content}")
            return content
        time.sleep(2)
    fail(f"operation {operation_id} timed out")


def vm_names(session: StreamableHttpSession) -> set[str]:
    result = session.call_tool("list_vms", {"fast": True})
    if result.get("isError"):
        fail(f"list_vms failed: {result}")
    content = result.get("structuredContent", {})
    return {str(item.get("name")) for item in content.get("vms", []) if isinstance(item, dict)}


def tool_content(session: StreamableHttpSession, name: str, arguments: dict) -> dict:
    result = session.call_tool(name, arguments)
    if result.get("isError"):
        fail(f"{name} failed: {result}")
    return result.get("structuredContent", {})


def main() -> None:
    if len(sys.argv) != 2:
        fail("usage: verify-standalone-lifecycle.py PATH_TO_LINUX_BINARY")

    binary = sys.argv[1]
    vm_name = f"opute-standalone-e2e-{int(time.time())}"
    state_dir = tempfile.mkdtemp(prefix="opute-standalone-lifecycle-")
    port = free_port()
    bind_host = "127.0.0.1"
    health = f"http://{bind_host}:{port}/health"
    mcp_url = f"http://{bind_host}:{port}/mcp"
    env = sanitize_env(
        {
            "OPUTE_AGENT_MODE": "standalone",
            "OPUTE_TRANSPORT": "http",
            "OPUTE_INFRA_PROVIDER_ID": "incus",
            "OPUTE_STANDALONE_ALLOW_MUTATIONS": "true",
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
    created = False
    session: StreamableHttpSession | None = None
    try:
        wait_health(health)
        session = StreamableHttpSession(mcp_url)
        session.initialize("standalone-lifecycle-gate")

        before = vm_names(session)
        if vm_name in before:
            fail(f"unique lifecycle name already exists: {vm_name}")

        created_operation = tool_content(
            session,
            "create_vm",
            {
                "vmName": vm_name,
                "image": "ubuntu:22.04",
                "cpus": 1,
                "memory": "1GiB",
            },
        )
        created = True
        operation_id = created_operation.get("taskId") or created_operation.get("operationId")
        if not operation_id:
            fail(f"create_vm did not return an operation id: {created_operation}")

        wait_operation(session, str(operation_id))
        after_create = vm_names(session)
        if vm_name not in after_create:
            fail(f"created VM {vm_name} missing from list_vms")

        info = tool_content(session, "get_vm_info", {"vmName": vm_name, "fast": True})
        if info.get("name") != vm_name:
            fail(f"get_vm_info returned unexpected VM: {info}")

        delete_operation = tool_content(session, "delete_vm", {"vmName": vm_name})
        operation_id = delete_operation.get("taskId") or delete_operation.get("operationId")
        if not operation_id:
            fail(f"delete_vm did not return an operation id: {delete_operation}")
        wait_operation(session, str(operation_id))
        created = False

        after_delete = vm_names(session)
        if vm_name in after_delete:
            fail(f"deleted VM {vm_name} still appears in list_vms")
        incus_names = incus_vm_names(vm_name)
        if incus_names is not None and vm_name in incus_names:
            fail(f"deleted VM {vm_name} still appears in incus list")
        print(f"standalone Incus lifecycle passed over Streamable HTTP: {vm_name}")
    finally:
        if created and session is not None:
            try:
                delete_operation = tool_content(session, "delete_vm", {"vmName": vm_name})
                operation_id = delete_operation.get("taskId") or delete_operation.get("operationId")
                if operation_id:
                    wait_operation(session, str(operation_id), timeout=300)
            except Exception as cleanup_error:  # noqa: BLE001 - best-effort cleanup
                print(f"cleanup failed for {vm_name}: {cleanup_error}", file=sys.stderr)
        try:
            terminate(process)
            wait_port_closed(health)
            remaining = incus_vm_names(vm_name)
            if remaining is not None and vm_name in remaining:
                fail(f"standalone lifecycle left Incus instance {vm_name}")
        finally:
            shutil.rmtree(state_dir, ignore_errors=True)


def incus_vm_names(prefix: str) -> set[str] | None:
    """Best-effort provider-side orphan check; lifecycle cleanup remains MCP-owned."""
    try:
        completed = subprocess.run(
            ["incus", "list", "--format", "json"],
            check=False,
            capture_output=True,
            text=True,
            timeout=15,
        )
    except (FileNotFoundError, subprocess.SubprocessError) as error:
        print(f"warning: unable to run incus orphan check: {error}", file=sys.stderr)
        return None
    if completed.returncode != 0:
        print(f"warning: incus orphan check failed: {completed.stderr.strip()}", file=sys.stderr)
        return None
    try:
        entries = json.loads(completed.stdout)
    except json.JSONDecodeError as error:
        print(f"warning: invalid incus orphan-check JSON: {error}", file=sys.stderr)
        return None
    if not isinstance(entries, list):
        return set()
    return {
        str(entry.get("name"))
        for entry in entries
        if isinstance(entry, dict) and str(entry.get("name", "")).startswith(prefix)
    }


if __name__ == "__main__":
    try:
        main()
    except Exception as error:  # noqa: BLE001 - top-level gate
        print(f"standalone lifecycle failed: {error}", file=sys.stderr)
        sys.exit(1)
