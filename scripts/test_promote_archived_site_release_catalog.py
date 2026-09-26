#!/usr/bin/env python3
"""Regression tests for canary-gated immutable release catalog snapshots."""
from __future__ import annotations

import json
import subprocess
import tempfile
import unittest
from pathlib import Path

from promote_archived_site_release_catalog import (
    CHECKS,
    archived_catalog,
    promote_archive,
)


class PromoteArchivedCatalogTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.archive_dir = self.root / "site" / "context" / "release-archives"
        (self.root / "site" / "context").mkdir(parents=True)
        (self.root / "npm" / "local-host-agent").mkdir(parents=True)
        subprocess.run(["git", "init", "-q", "-b", "main"], cwd=self.root, check=True)
        subprocess.run(["git", "config", "user.name", "Test"], cwd=self.root, check=True)
        subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=self.root, check=True)
        self.catalog = {
            "packageName": "@opute/host-agent",
            "packageVersion": "0.2.1",
            "releaseChannel": "preview",
            "catalogRevision": "sha256:" + "a" * 64,
            "toolCount": 1,
            "tools": [{"name": "get_host_info"}],
        }
        (self.root / "site/context/release-catalog.json").write_text(
            json.dumps(self.catalog, indent=2) + "\n", encoding="utf-8"
        )
        (self.root / "npm/local-host-agent/package.json").write_text(
            json.dumps({"name": "@opute/host-agent", "version": "0.2.1"}) + "\n",
            encoding="utf-8",
        )
        subprocess.run(["git", "add", "."], cwd=self.root, check=True)
        subprocess.run(["git", "commit", "-qm", "release snapshot"], cwd=self.root, check=True)
        self.source_sha = subprocess.run(
            ["git", "rev-parse", "HEAD"], cwd=self.root, check=True, capture_output=True, text=True
        ).stdout.strip()
        self.evidence = {
            "packageName": "@opute/host-agent",
            "packageVersion": "0.2.1",
            "catalogRevision": self.catalog["catalogRevision"],
            "sourceSha": self.source_sha,
            "runId": 42,
            "runAttempt": 1,
            "checks": {name: True for name in CHECKS},
        }

    def evidence_file(self, evidence: dict | None = None) -> Path:
        path = self.root / "evidence.json"
        path.write_text(json.dumps(evidence or self.evidence), encoding="utf-8")
        return path

    def test_archives_exact_source_snapshot_with_canary(self) -> None:
        destination = promote_archive(self.evidence_file(), self.root, self.archive_dir)
        archive = json.loads(destination.read_text(encoding="utf-8"))

        self.assertEqual(destination.name, "v0.2.1.json")
        self.assertEqual(archive["releaseChannel"], "stable")
        self.assertEqual(archive["tools"], self.catalog["tools"])
        self.assertEqual(archive["publishedCanary"]["sourceSha"], self.source_sha)
        self.assertTrue(all(archive["publishedCanary"]["checks"].values()))

    def test_same_evidence_is_idempotent_but_conflicts_fail(self) -> None:
        evidence = self.evidence_file()
        destination = promote_archive(evidence, self.root, self.archive_dir)
        self.assertEqual(promote_archive(evidence, self.root, self.archive_dir), destination)
        (self.archive_dir / destination.name).write_text("{}", encoding="utf-8")
        with self.assertRaises(SystemExit):
            promote_archive(evidence, self.root, self.archive_dir)

    def test_changed_catalog_revision_fails(self) -> None:
        evidence = {**self.evidence, "catalogRevision": "sha256:" + "b" * 64}
        with self.assertRaises(SystemExit):
            archived_catalog(evidence, self.root)

    def test_missing_canary_check_fails(self) -> None:
        evidence = {**self.evidence, "checks": {**self.evidence["checks"], "readOnly": False}}
        with self.assertRaises(SystemExit):
            archived_catalog(evidence, self.root)

    def test_source_revision_must_be_an_ancestor(self) -> None:
        evidence = {**self.evidence, "sourceSha": "0" * 40}
        with self.assertRaises(SystemExit):
            archived_catalog(evidence, self.root)

    def test_snapshot_must_be_the_source_revision_catalog(self) -> None:
        changed = {**self.catalog, "catalogRevision": "sha256:" + "b" * 64}
        (self.root / "site/context/release-catalog.json").write_text(
            json.dumps(changed, indent=2) + "\n", encoding="utf-8"
        )
        evidence = {**self.evidence, "catalogRevision": changed["catalogRevision"]}
        with self.assertRaises(SystemExit):
            archived_catalog(evidence, self.root)


if __name__ == "__main__":
    unittest.main()
