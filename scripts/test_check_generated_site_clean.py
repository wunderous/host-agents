#!/usr/bin/env python3
"""Tests for the generated-site clean-tree gate."""
from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "scripts"))
import check_generated_site_clean


class GeneratedSiteCleanTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.git("init", "-q")
        self.git("config", "user.name", "Site test")
        self.git("config", "user.email", "site-test@example.invalid")
        self.git("config", "core.autocrlf", "true")
        generated = self.root / "site/public/index.html"
        generated.parent.mkdir(parents=True)
        generated.write_text("<h1>Generated</h1>\n", encoding="utf-8")
        self.git("add", "site/public/index.html")
        self.git("commit", "-qm", "baseline")

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def git(self, *args: str) -> str:
        result = subprocess.run(
            ["git", *args],
            cwd=self.root,
            check=True,
            capture_output=True,
            text=True,
        )
        return result.stdout

    def test_unchanged_generated_content_is_clean(self) -> None:
        generated = self.root / "site/public/index.html"
        generated.write_text(generated.read_text(encoding="utf-8"), encoding="utf-8")

        self.assertEqual([], check_generated_site_clean.changed_generated_paths(self.root))

    def test_tracked_changes_are_reported(self) -> None:
        (self.root / "site/public/index.html").write_text("<h1>Changed</h1>\n", encoding="utf-8")

        self.assertEqual(
            ["site/public/index.html"],
            check_generated_site_clean.changed_generated_paths(self.root),
        )

    def test_untracked_generated_files_are_reported(self) -> None:
        (self.root / "site/public/new-page.html").write_text("<h1>New</h1>\n", encoding="utf-8")

        self.assertEqual(
            ["site/public/new-page.html"],
            check_generated_site_clean.changed_generated_paths(self.root),
        )


if __name__ == "__main__":
    unittest.main()
