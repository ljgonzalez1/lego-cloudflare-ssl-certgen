#!/usr/bin/env python3
"""Regression tests for the prerelease tags shared by Python and the publisher."""

import csv
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

from tags_table import (
    COLUMNS, DOCKER_IMAGE, REPO_DIR, buildx_command, complete_table, make_release,
    short_table, TABLE_BEGIN, TABLE_END,
)


def make_fields(version, suffix=""):
    return dict(zip(COLUMNS, (
        "Alpine", "3.24.4", "scratch", "", "lego", "v4.34.0", "", "",
        f"v{version}", suffix, "",
    ), strict=True))


class TagPolicyTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "ci/versions").mkdir(parents=True)
        for name in (
            "ci/publish-image.sh", "ci/versions.sh", "ci/check-version.sh", "ci/is-version-branch.sh",
            "ci/versions/tags_table.py", "ci/versions/check_version.py",
        ):
            shutil.copy2(REPO_DIR / name, self.root / name)
        self.environment = os.environ.copy()
        self.environment.pop("RELEASE_TAG", None)

    def run_command(self, *args, **environment):
        return subprocess.run(
            args, cwd=self.root, env=self.environment | environment, check=False,
            text=True, capture_output=True,
        )

    def write_versions(self, current):
        (self.root / "ci/versions/versions.txt").write_text(
            f"APP_VERSION={current}\nLEGO_VERSION=4.34.0\nALPINE_VERSION=3.24.4\n"
        )

    def test_publisher_and_manual_command_agree(self):
        for suffix, expect_latest in (
            ("", True), ("-beta", False), ("-beta-1", False),
            ("-beta.1", False), ("-BETA1", False), ("-alpha.2", False),
            ("-alfa", False), ("-alphabet", True), ("-rc-1", True),
        ):
            with self.subTest(suffix=suffix):
                version = f"5.2.3{suffix}"
                self.write_versions(version)
                record = make_release(make_fields(version), 1)
                result = self.run_command("bash", "ci/publish-image.sh", "--print-tags")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    result.stdout.splitlines(), [f"{DOCKER_IMAGE}:{tag}" for tag in record.tags],
                )
                self.assertEqual("latest" in record.tags, expect_latest)
                self.assertEqual(record.tags[5:8], tuple(f"v5{part}{suffix}" for part in (".2.3", ".2", "")))
                command = buildx_command(record)
                self.assertIn(f"--build-arg APP_VERSION={version}", command)
                self.assertEqual(f"-t {DOCKER_IMAGE}:latest " in command, expect_latest)

    def test_latest_row_remains_visible_with_more_than_seven_prereleases(self):
        first = make_release(make_fields("5.2.3"), 1)
        prereleases = [
            make_release(make_fields("5.2.4", f"beta-{i}"), i + 1) for i in range(1, 10)
        ]
        for renderer in (complete_table, short_table):
            table = renderer([first, *prereleases])
            self.assertEqual(table.count("`latest`"), 1)
            latest_row, = (line for line in table.splitlines() if "`latest`" in line)
            self.assertIn("`v5.2.3`", latest_row)
            self.assertNotIn("`latest`", renderer(prereleases))
        self.assertEqual(len(short_table([first, *prereleases]).splitlines()), 9)

    def test_release_requires_the_configured_suffix(self):
        self.write_versions("5.2.3")
        result = self.run_command(
            "bash", "ci/publish-image.sh", "--print-tags", RELEASE_TAG="v5.2.3-beta-1",
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("does not match APP_VERSION", result.stderr)
        self.assertEqual(result.stdout, "")

    def test_release_checks_both_committed_tables_for_a_beta(self):
        self.write_versions("5.2.4-beta-1")
        with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
            writer = csv.DictWriter(file, fieldnames=COLUMNS)
            writer.writeheader()
            writer.writerows([make_fields("5.2.3"), make_fields("5.2.4", "beta-1")])
        for doc in ("README.md", "DOCKERHUB.md"):
            (self.root / doc).write_text(f"{TABLE_BEGIN}\n{TABLE_END}\n")
        result = self.run_command(sys.executable, "-B", "ci/versions/tags_table.py", "--buildx-version", "v5.2.4-beta-1")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn(f"-t {DOCKER_IMAGE}:latest ", result.stdout)
        result = self.run_command(sys.executable, "-B", "ci/versions/tags_table.py", "--buildx-version", "v5.2.3")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn(f"-t {DOCKER_IMAGE}:latest ", result.stdout)
        for doc in ("README.md", "DOCKERHUB.md"):
            result = self.run_command(
                "bash", "ci/check-version.sh", doc, GITHUB_EVENT_NAME="release",
                TARGET_BRANCH="v5.2.4-beta-1", VERSION_REF="v5.2.4-beta-1",
                GITHUB_OUTPUT=str(self.root / "outputs"),
            )
            self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main()
