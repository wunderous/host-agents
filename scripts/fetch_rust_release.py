#!/usr/bin/env python3
"""Fetch a pinned Rust Host Agent release set and verify every file.

  python3 scripts/fetch_rust_release.py release/v0.3.0.json dist

From 0.3.0 the Host Agent is built from wunderous/opute-host-agent. A pin in
release/v<version>.json names that repository's release and the SHA-256 of
every file in it. This script downloads each file from that release, refuses
any file whose digest differs from the pin, checks SHA256SUMS, and copies the
x64 binary to dist/opute-host-agent for the launcher canary and the site's
catalog capture. Nothing is rebuilt.
"""
from __future__ import annotations

import hashlib
import json
import shutil
import subprocess
import sys
from pathlib import Path
from urllib.request import Request, urlopen

AGENT = "opute-host-agent"


def fail(message: str) -> None:
    raise SystemExit("rust release fetch failed: " + message)


def main() -> int:
    if len(sys.argv) != 3:
        fail("usage: fetch_rust_release.py PIN DEST")
    pin = json.loads(Path(sys.argv[1]).read_text())
    dest = Path(sys.argv[2])
    source = pin["source"]
    base = f"https://github.com/{source['repository']}/releases/download/{source['tag']}"
    dest.mkdir(parents=True, exist_ok=True)
    for name, expected in sorted(pin["assets"].items()):
        target = dest / name
        if target.is_file() and hashlib.sha256(target.read_bytes()).hexdigest() == expected:
            continue
        request = Request(f"{base}/{name}", headers={"User-Agent": "opute-release-fetch"})
        with urlopen(request, timeout=120) as response:
            data = response.read()
        actual = hashlib.sha256(data).hexdigest()
        if actual != expected:
            fail(f"{name} is {actual}, the pin says {expected}")
        target.write_bytes(data)
        if not name.endswith((".json", ".sha256")) and name != "SHA256SUMS":
            target.chmod(0o755)
    subprocess.run(["sha256sum", "-c", "--quiet", "SHA256SUMS"], cwd=dest, check=True)
    shutil.copy2(dest / "host-agent-linux-x64", dest / AGENT)
    print(f"verified {len(pin['assets'])} files of {source['repository']} {source['tag']} ({source['candidate']})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
