#!/usr/bin/env python3
"""Disposable standalone MCP/Incus VM lifecycle gate.

The binary is expected to be a Linux build.  The test enables mutations only
for this process and always attempts to delete its uniquely named VM.
"""

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


def call(process, request_id, name, arguments):
    result = send(process, request_id, "tools/call", {"name": name, "arguments": arguments})
    if result.get("isError"):
        fail(f"{name} failed: {result}")
    return result.get("structuredContent", {})


def wait_operation(process, request_id, operation_id, timeout=600):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = call(process, request_id, "get_operation", {"operationId": operation_id})
        status = str(result.get("status", "")).lower()
        if status in {"completed", "failed", "cancelled", "canceled"}:
            if status != "completed":
                fail(f"operation {operation_id} ended with {status}: {result}")
            return result
        time.sleep(2)
    fail(f"operation {operation_id} timed out")


def vm_names(process, request_id):
    result = call(process, request_id, "list_vms", {"fast": True})
    return {str(item.get("name")) for item in result.get("vms", []) if isinstance(item, dict)}


def main():
    if len(sys.argv) != 2:
        fail("usage: verify-standalone-lifecycle.py PATH_TO_LINUX_BINARY")

    vm_name = f"opute-standalone-e2e-{int(time.time())}"
    state_dir = tempfile.mkdtemp(prefix="opute-standalone-lifecycle-")
    env = {key: value for key, value in os.environ.items() if not key.startswith("OPUTE_") and key not in {"MCP_AUTH_TOKEN", "BRIDGE_TOKEN"}}
    env.update({
        "OPUTE_AGENT_MODE": "standalone",
        "OPUTE_TRANSPORT": "stdio",
        "OPUTE_INFRA_PROVIDER_ID": "incus",
        "OPUTE_STANDALONE_ALLOW_MUTATIONS": "true",
        "OPUTE_STANDALONE_STATE_DIR": state_dir,
    })
    process = subprocess.Popen([sys.argv[1]], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env)
    request_id = 0
    created = False
    try:
        request_id += 1
        send(process, request_id, "initialize", {
            "protocolVersion": "2024-11-05",
            "capabilities": {},
            "clientInfo": {"name": "standalone-lifecycle-gate", "version": "1"},
        })
        process.stdin.write(b'{"jsonrpc":"2.0","method":"notifications/initialized"}\n')
        process.stdin.flush()

        request_id += 1
        before = vm_names(process, request_id)
        if vm_name in before:
            fail(f"unique lifecycle name already exists: {vm_name}")

        request_id += 1
        created_operation = call(process, request_id, "create_vm", {
            "vmName": vm_name,
            "image": "ubuntu:22.04",
            "cpus": 1,
            "memory": "1GiB",
        })
        operation_id = created_operation.get("taskId") or created_operation.get("operationId")
        if not operation_id:
            fail(f"create_vm did not return an operation id: {created_operation}")
        created = True

        request_id += 1
        wait_operation(process, request_id, operation_id)
        request_id += 1
        after_create = vm_names(process, request_id)
        if vm_name not in after_create:
            fail(f"created VM {vm_name} missing from list_vms")

        request_id += 1
        info = call(process, request_id, "get_vm_info", {"vmName": vm_name, "fast": True})
        if info.get("name") != vm_name:
            fail(f"get_vm_info returned unexpected VM: {info}")

        request_id += 1
        delete_operation = call(process, request_id, "delete_vm", {"vmName": vm_name})
        operation_id = delete_operation.get("taskId") or delete_operation.get("operationId")
        if not operation_id:
            fail(f"delete_vm did not return an operation id: {delete_operation}")
        created = False
        request_id += 1
        wait_operation(process, request_id, operation_id)

        request_id += 1
        after_delete = vm_names(process, request_id)
        if vm_name in after_delete:
            fail(f"deleted VM {vm_name} still appears in list_vms")
        print(f"standalone Incus lifecycle passed: {vm_name}")
    finally:
        if created:
            try:
                request_id += 1
                delete_operation = call(process, request_id, "delete_vm", {"vmName": vm_name})
                operation_id = delete_operation.get("taskId") or delete_operation.get("operationId")
                if operation_id:
                    request_id += 1
                    wait_operation(process, request_id, operation_id, timeout=300)
            except Exception as cleanup_error:
                print(f"cleanup failed for {vm_name}: {cleanup_error}", file=sys.stderr)
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
        print(f"standalone lifecycle failed: {error}", file=sys.stderr)
        sys.exit(1)
