#!/usr/bin/env python3
from __future__ import annotations

import contextlib
import hashlib
import importlib.util
import io
import tempfile
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


class AssetCacheKeyTest(unittest.TestCase):
    def test_fingerprint_normalizes_checkout_line_endings(self) -> None:
        expected = hashlib.sha256(b"first\nsecond\n").hexdigest()
        with tempfile.TemporaryDirectory() as temporary_directory:
            asset = Path(temporary_directory) / "styles.css"
            asset.write_bytes(b"first\r\nsecond\r\n")
            self.assertEqual(VALIDATOR.content_fingerprint(asset), expected)

    def test_asset_url_must_use_the_current_content_fingerprint(self) -> None:
        expected = "a" * 64
        versions = {"/styles.css": expected}
        self.assertIsNone(VALIDATOR.asset_cache_key_error(f"/styles.css?v={expected}", versions))
        self.assertIsNotNone(VALIDATOR.asset_cache_key_error("/styles.css?v=20260924d", versions))


if __name__ == "__main__":
    unittest.main()
