#!/usr/bin/env python3
"""Regression tests for immutable stable capability catalog archiving."""
from __future__ import annotations

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

SCRIPT = Path(__file__).with_name("promote_site_release_catalog.py")
SPEC = importlib.util.spec_from_file_location("promote_site_release_catalog", SCRIPT)
assert SPEC is not None and SPEC.loader is not None
PROMOTER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROMOTER)


class ArchivePreviousStableCatalogTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.archive_dir = Path(self.temporary.name) / "release-archives"
        self.catalog = {
            "packageName": "@opute/host-agent",
            "packageVersion": "0.2.1",
            "releaseChannel": "stable",
            "catalogRevision": "sha256:" + "1" * 64,
            "toolCount": 1,
            "tools": [{"name": "get_host_info"}],
            "publishedCanary": {"packageVersion": "0.2.1"},
        }

    def test_archives_previous_stable_catalog_before_version_advance(self) -> None:
        created = PROMOTER.archive_previous_stable_catalog(
            self.catalog, "0.2.2", self.archive_dir
        )
        archive = self.archive_dir / "v0.2.1.json"

        self.assertTrue(created)
        self.assertEqual(json.loads(archive.read_text(encoding="utf-8")), self.catalog)

    def test_identical_existing_archive_is_idempotent(self) -> None:
        self.archive_dir.mkdir()
        archive = self.archive_dir / "v0.2.1.json"
        archive.write_text(json.dumps(self.catalog), encoding="utf-8")

        created = PROMOTER.archive_previous_stable_catalog(
            self.catalog, "0.2.2", self.archive_dir
        )

        self.assertFalse(created)
        self.assertEqual(json.loads(archive.read_text(encoding="utf-8")), self.catalog)

    def test_conflicting_existing_archive_fails_closed(self) -> None:
        self.archive_dir.mkdir()
        (self.archive_dir / "v0.2.1.json").write_text("{}", encoding="utf-8")

        with self.assertRaises(SystemExit):
            PROMOTER.archive_previous_stable_catalog(
                self.catalog, "0.2.2", self.archive_dir
            )

    def test_preview_catalog_and_same_version_promotion_do_not_archive(self) -> None:
        preview = {**self.catalog, "releaseChannel": "preview"}

        self.assertFalse(
            PROMOTER.archive_previous_stable_catalog(preview, "0.2.2", self.archive_dir)
        )
        self.assertFalse(
            PROMOTER.archive_previous_stable_catalog(self.catalog, "0.2.1", self.archive_dir)
        )
        self.assertFalse(self.archive_dir.exists())


if __name__ == "__main__":
    unittest.main()
