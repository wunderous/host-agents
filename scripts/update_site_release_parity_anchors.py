#!/usr/bin/env python3
"""Recompute file anchors after reviewing an intentional invariant-source change."""
from __future__ import annotations

import hashlib
import json
import os
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DECISION = ROOT / ".agents" / "decisions" / "public-documentation-release-parity.json"


def main() -> None:
    record = json.loads(DECISION.read_text(encoding="utf-8"))
    anchors = record.get("anchors")
    if not isinstance(anchors, list) or not anchors:
        raise SystemExit("decision has no anchors to update")
    updated = []
    for anchor in anchors:
        if not isinstance(anchor, dict) or anchor.get("kind") != "file":
            raise SystemExit("only explicit file anchors can be re-baselined")
        path = (ROOT / anchor["path"]).resolve()
        try:
            path.relative_to(ROOT)
        except ValueError as error:
            raise SystemExit("anchor path escapes the repository: " + anchor["path"]) from error
        if not path.is_file():
            raise SystemExit("anchor file is missing: " + anchor["path"])
        digest = hashlib.sha256(path.read_bytes().replace(b"\r\n", b"\n")).hexdigest()
        updated.append({**anchor, "digest": digest})
        print(anchor["path"] + " " + digest)
    record["anchors"] = updated
    encoded = (json.dumps(record, indent=2) + "\n").encode("utf-8")
    descriptor, temporary = tempfile.mkstemp(prefix=DECISION.name + ".", dir=DECISION.parent)
    try:
        with os.fdopen(descriptor, "wb") as output:
            output.write(encoded)
        os.replace(temporary, DECISION)
    except BaseException:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass
        raise


if __name__ == "__main__":
    main()
