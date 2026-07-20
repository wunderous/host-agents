#!/usr/bin/env python3
"""Shared Streamable HTTP MCP helpers for standalone verification gates."""

from __future__ import annotations

import json
import os
import signal
import socket
import subprocess
import urllib.parse
import time
import urllib.error
import urllib.request
from typing import Any


ACCEPT = "application/json, text/event-stream"
PROTOCOL_VERSION = "2024-11-05"


def fail(message: str) -> None:
    raise RuntimeError(message)


def free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
        sock.bind(("127.0.0.1", 0))
        return int(sock.getsockname()[1])


def sanitize_env(extra: dict[str, str]) -> dict[str, str]:
    env = {
        key: value
        for key, value in os.environ.items()
        if not key.startswith("OPUTE_") and key not in {"MCP_AUTH_TOKEN", "BRIDGE_TOKEN"}
    }
    env.update(extra)
    return env


def wait_health(url: str, timeout: float = 15.0) -> None:
    deadline = time.time() + timeout
    last_error = ""
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=1.0) as response:
                body = response.read().decode("utf-8", errors="replace")
                if response.status == 200 and '"ok"' in body:
                    return
                last_error = f"status={response.status} body={body!r}"
        except Exception as error:  # noqa: BLE001 - probe until ready
            last_error = str(error)
        time.sleep(0.1)
    fail(f"timed out waiting for health at {url}: {last_error}")


def wait_port_closed(url: str, timeout: float = 5.0) -> None:
    """Require the listener to reject connections after process shutdown."""
    parsed = urllib.parse.urlparse(url)
    if parsed.hostname is None or parsed.port is None:
        fail(f"cannot probe listener URL: {url}")
    deadline = time.time() + timeout
    last_error = "listener still accepts connections"
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url, timeout=0.5) as response:
                response.read(1)
                last_error = f"status={response.status}"
        except Exception as error:  # noqa: BLE001 - closed-port probe
            if isinstance(error, urllib.error.URLError):
                reason = getattr(error, "reason", None)
                if isinstance(reason, ConnectionRefusedError) or isinstance(reason, OSError):
                    return
            else:
                return
        time.sleep(0.1)
    fail(f"listener did not release {parsed.hostname}:{parsed.port}: {last_error}")


def terminate(process: subprocess.Popen[bytes]) -> None:
    if process.poll() is not None:
        return
    process.send_signal(signal.SIGTERM)
    try:
        process.wait(timeout=5)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()


def parse_mcp_body(body: str) -> dict[str, Any]:
    if not body.strip():
        return {}
    if body.lstrip().startswith("{"):
        parsed = json.loads(body)
        if isinstance(parsed, dict):
            return parsed
        fail(f"unexpected MCP JSON body: {body[:500]!r}")
    result: dict[str, Any] | None = None
    for line in body.splitlines():
        if line.startswith("data:"):
            chunk = line[5:].strip()
            if not chunk or chunk == "[DONE]":
                continue
            parsed = json.loads(chunk)
            if isinstance(parsed, dict) and ("result" in parsed or "error" in parsed or "method" in parsed):
                result = parsed
    if result is None:
        fail(f"unable to parse MCP response: {body[:500]!r}")
    return result


class StreamableHttpSession:
    """Minimal Streamable HTTP client that preserves Mcp-Session-Id."""

    def __init__(self, url: str) -> None:
        self.url = url
        self.session_id: str | None = None
        self.protocol_version = PROTOCOL_VERSION
        self._next_id = 1

    def _headers(self) -> dict[str, str]:
        headers = {
            "Accept": ACCEPT,
            "Content-Type": "application/json",
            "Mcp-Protocol-Version": self.protocol_version,
        }
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        return headers

    def post(self, payload: dict[str, Any], timeout: float = 60.0) -> dict[str, Any]:
        data = json.dumps(payload).encode("utf-8")
        request = urllib.request.Request(
            self.url,
            data=data,
            method="POST",
            headers=self._headers(),
        )
        try:
            with urllib.request.urlopen(request, timeout=timeout) as response:
                session_header = response.headers.get("Mcp-Session-Id")
                if session_header:
                    self.session_id = session_header
                body = response.read().decode("utf-8")
                if response.status == 202:
                    return {}
                return parse_mcp_body(body)
        except urllib.error.HTTPError as error:
            detail = error.read().decode("utf-8", errors="replace")
            fail(f"MCP HTTP {error.code}: {detail}")

    def initialize(self, client_name: str, client_version: str = "1") -> dict[str, Any]:
        request_id = self._next_id
        self._next_id += 1
        result = self.post(
            {
                "jsonrpc": "2.0",
                "id": request_id,
                "method": "initialize",
                "params": {
                    "protocolVersion": PROTOCOL_VERSION,
                    "capabilities": {},
                    "clientInfo": {"name": client_name, "version": client_version},
                },
            }
        )
        if "error" in result:
            fail(f"initialize failed: {result['error']}")
        negotiated = result.get("result", {}).get("protocolVersion")
        if isinstance(negotiated, str) and negotiated:
            self.protocol_version = negotiated
        # notifications/initialized may return 202 with an empty body.
        self.post({"jsonrpc": "2.0", "method": "notifications/initialized"})
        if not self.session_id:
            fail("initialize did not return Mcp-Session-Id")
        return result.get("result", {})

    def rpc(self, method: str, params: dict[str, Any] | None = None) -> dict[str, Any]:
        request_id = self._next_id
        self._next_id += 1
        payload: dict[str, Any] = {"jsonrpc": "2.0", "id": request_id, "method": method}
        if params is not None:
            payload["params"] = params
        response = self.post(payload)
        if "error" in response:
            fail(f"MCP request {request_id} ({method}) failed: {response['error']}")
        return response.get("result", {})

    def call_tool(self, name: str, arguments: dict[str, Any] | None = None) -> dict[str, Any]:
        return self.rpc("tools/call", {"name": name, "arguments": arguments or {}})
