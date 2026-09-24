#!/usr/bin/env python3
from __future__ import annotations

import contextlib
import importlib.util
import io
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("validate-generated-site.py")
SPEC = importlib.util.spec_from_file_location("validate_generated_site", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
VALIDATOR = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(VALIDATOR)


class ResolveLocalTargetTest(unittest.TestCase):
    def test_rejects_repository_file_outside_published_tree(self) -> None:
        page = VALIDATOR.SITE / "docs" / "index.html"
        with contextlib.redirect_stderr(io.StringIO()):
            with self.assertRaises(SystemExit):
                VALIDATOR.resolve_local_target(page, "../../../README.md")

    def test_accepts_target_inside_published_tree(self) -> None:
        page = VALIDATOR.SITE / "docs" / "index.html"
        target, fragment = VALIDATOR.resolve_local_target(page, "../index.html#main-content")
        self.assertEqual(target, VALIDATOR.SITE / "index.html")
        self.assertEqual(fragment, "main-content")


if __name__ == "__main__":
    unittest.main()
