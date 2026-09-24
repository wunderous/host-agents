#!/usr/bin/env python3
"""Fail when generated site outputs differ from the checked-in source revision."""
from __future__ import annotations

import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GENERATED_PATHS = (
    "site/context/release-catalog.json",
    "site/context/PACKET.md",
    "site/public",
)


def main() -> int:
    result = subprocess.run(
        [
            "git",
            "-c",
            "core.autocrlf=true",
            "-c",
            "core.filemode=false",
            "status",
            "--porcelain=v1",
            "--untracked-files=all",
            "--",
            *GENERATED_PATHS,
        ],
        cwd=ROOT,
        check=True,
        capture_output=True,
        text=True,
    )
    if result.stdout.strip():
        print("generated site outputs differ from the checked-in revision:")
        print(result.stdout.rstrip())
        return 1
    print("generated site outputs match the checked-in revision")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
