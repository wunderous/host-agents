#!/usr/bin/env python3
"""Fail when generated site outputs differ from the checked-in source revision."""
from __future__ import annotations

import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GENERATED_PATHS = (
    "site/context/release-catalog.json",
    "site/context/PACKET.md",
    "site/public",
)


def changed_generated_paths(root: Path = ROOT) -> list[str]:
    tracked = subprocess.run(
        ["git", "diff", "--name-only", "HEAD", "--", *GENERATED_PATHS],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
    )
    untracked = subprocess.run(
        ["git", "ls-files", "--others", "--exclude-standard", "-z", "--", *GENERATED_PATHS],
        cwd=root,
        check=True,
        capture_output=True,
    )
    untracked_paths = untracked.stdout.decode("utf-8").split("\0")
    return sorted(set(tracked.stdout.splitlines()) | {path for path in untracked_paths if path})


def main() -> int:
    changed = changed_generated_paths()
    if changed:
        print("generated site outputs differ from the checked-in revision:")
        print("\n".join(f" {path}" for path in changed))
        return 1
    print("generated site outputs match the checked-in revision")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
