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
        f"v{version}", suffix, f"https://example.org/releases/v{version}" + (f"-{suffix}" if suffix else ""),
    ), strict=True))


class TagPolicyTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        (self.root / "ci/versions").mkdir(parents=True)
        for name in (
            "ci/publish-image.sh", "ci/versions.sh", "ci/check-version.sh", "ci/is-version-branch.sh",
            "ci/versions/tags_table.py", "ci/versions/check_version.py", "ci/versions/release_config.py",
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
            ("-alfa", False), ("-alpha1", False), ("-alfa.3", False),
            ("-audit", True), ("-build-1", True),
        ):
            with self.subTest(suffix=suffix):
                version = f"5.2.3{suffix}"
                self.write_versions(version)
                with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
                    writer = csv.DictWriter(file, fieldnames=COLUMNS)
                    writer.writeheader()
                    writer.writerow(make_fields(version))
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

    def test_full_version_regex_for_branch_names(self):
        valid = (
            "1.2", "v1.2", "V1.2.3", "0.0", "v0.0.0.0", "v1.2.3.4",
            "v1.0.1645133646513", "1.2-alpha", "v1.2-alfa1", "V1.2-BETA.2",
            "v1.2.3-beta-1", "1.2-audit", "1.2-build1", "v1.2-build-3",
            "v1.2-BUILD.12", "v1.2.3.4-alpha-123", "v0.0-beta", "v1.2-alpha1",
            "v1.2+alpha", "v1.2.alpha", "v1.2alpha", "v1.2-ALFA", "v1.2.Beta",
            "v1.2+audit", "v1.2-rc1", "v1.2+build1", "v1.2-rc-1",
            "v1.2-rc.1", "v1.2+RC.15", "v1.2-build.1", "v1.2-BUILD999",
            "V1.2.3-ALPHA.2", "1.2.3.4+build-999", "0.0.0.0.audit1",
            "v1.2rc1", "v1.2+rc.1", "V1.2.3.RC-25",
        )
        invalid = (
            "", "v1", "v1.2.", "v1.2.3.4.5", "v01.2", "v1.02",
            "v1.2-beta0", "v1.2-beta-0", "v1.2-beta.01", "v1.2-beta--1",
            "v1.2-build", "v1.2-build0", "v1.2-build.0", "v1.2-rc",
            "v1.2-alphabet", "v1.2-beta-1/docs", "master", "refs/heads/v1.2-beta-1",
            "vv1.2", "vV1.2", "version1.2", "x1.2", "1", "1.", ".1",
            "1..2", "1.2.3.4.5", "1.2.3.04", "-1.2", "+1.2", "v1.2-",
            "v1.2+", "v1.2-dev", "v1.2-alpha-beta", "v1.2--alpha",
            "v1.2+-alpha", "v1.2..beta", "v1.2-alpha0", "v1.2-alpha-0",
            "v1.2-alpha.0", "v1.2-alpha01", "v1.2-alpha-01", "v1.2-alpha-",
            "v1.2-alpha.", "v1.2-beta1.2", "v1.2-audit--1", "v1.2-rc0",
            "v1.2-build0", "v1.2-rc-0", "v1.2-build.0", "v1.2-rc01",
            "v1.2-build-01", "v1.2-rc-", "v1.2-build.", "v1.2-rc1.2",
            "v1.2-build+1", "v1.2+metadata", "v1.2-snapshot1", "v1.2 -beta1",
            "v1.2-beta1 ", "v1.2+rc+1", "v1.2-rc1+build2", "v1.2-alpha1\n",
            "v1.2.3-beta-0", "v1.2\r", "v1.2\t", "v1.2\u0661",
        )
        for ref in valid:
            with self.subTest(valid=ref):
                result = self.run_command("bash", "ci/is-version-branch.sh", ref, "--print-version")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertRegex(result.stdout.strip(), r"^\d+\.\d+(?:\.\d+){0,2}$")
        for ref in invalid:
            with self.subTest(invalid=ref):
                result = self.run_command("bash", "ci/is-version-branch.sh", ref, "--print-version")
                self.assertNotEqual(result.returncode, 0, ref)

    def test_markdown_checks_follow_csv_not_name_of_branch_or_release(self):
        self.write_versions("5.2.4-beta-1")
        with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
            writer = csv.DictWriter(file, fieldnames=COLUMNS)
            writer.writeheader()
            writer.writerow(make_fields("5.2.4", "beta-1"))
        for doc in ("README.md", "DOCKERHUB.md"):
            (self.root / doc).write_text(f"{TABLE_BEGIN}\n{TABLE_END}\n")
        result = self.run_command(sys.executable, "-B", "ci/versions/tags_table.py")
        self.assertEqual(result.returncode, 0, result.stderr)
        for event, branch in (
            ("push", "v1.2-alpha"), ("push", "V10.20.30.40-BETA.4"),
            ("push", "v1.2+audit"), ("pull_request", "2.3-build1"),
            ("pull_request", "v1.2+rc.1"), ("pull_request", "master"),
            ("push", "master"), ("release", "not-the-version-of-the-docs"),
        ):
            for document in ("README.md", "DOCKERHUB.md"):
                with self.subTest(event=event, branch=branch, document=document):
                    result = self.run_command(
                        "bash", "ci/check-version.sh", document,
                        GITHUB_EVENT_NAME=event, TARGET_BRANCH=branch,
                        VERSION_REF="completely-different-version",
                        GITHUB_OUTPUT=str(self.root / "outputs"),
                    )
                    self.assertEqual(result.returncode, 0, result.stderr)
        for branch in ("v1.02", "v1.2-beta0", "feature/something"):
            result = self.run_command(
                "bash", "ci/check-version.sh", "README.md",
                GITHUB_EVENT_NAME="push", TARGET_BRANCH=branch,
            )
            self.assertNotEqual(result.returncode, 0, branch)
        # Content must still be checked, even when the branch name differs.
        (self.root / "README.md").write_text("bad README")
        result = self.run_command(
            "bash", "ci/check-version.sh", "README.md",
            GITHUB_EVENT_NAME="pull_request", TARGET_BRANCH="v1.2-alpha",
        )
        self.assertNotEqual(result.returncode, 0)

    def test_invalid_current_versions_cannot_be_published(self):
        for version in ("1.2.3-alphabet", "1.2.3-rc", "1.2.3-beta0", "1.2.3-build", "01.2.3", "1.2.3+rc1"):
            with self.subTest(version=version):
                self.write_versions(version)
                with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
                    writer = csv.DictWriter(file, fieldnames=COLUMNS)
                    writer.writeheader()
                    writer.writerow(make_fields(version))
                result = self.run_command("bash", "ci/publish-image.sh", "--print-tags")
                self.assertNotEqual(result.returncode, 0)

    def test_two_four_component_and_rc_docker_tag_generation(self):
        for version in ("5.2.3.4-rc.3", "5.2.3rc1", "5.2.3-build1"):
            with self.subTest(version=version):
                self.write_versions(version)
                with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
                    writer = csv.DictWriter(file, fieldnames=COLUMNS)
                    writer.writeheader()
                    writer.writerow(make_fields(version))
                record = make_release(make_fields(version), 1)
                result = self.run_command("bash", "ci/publish-image.sh", "--print-tags")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(result.stdout.splitlines(), [f"{DOCKER_IMAGE}:{tag}" for tag in record.tags])
        # Two components are valid *branches*, but 8 distinct aliases can't be generated
        # from a two-component release without changing the existing Docker tag policy.
        for version in ("v1.2", "V1.2+build-7"):
            result = self.run_command("bash", "ci/is-version-branch.sh", version)
            self.assertEqual(result.returncode, 0, result.stderr)

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

    def test_unpublished_branch_has_readme_history_but_no_docker_tags(self):
        published = make_fields("6.0.0", "alpha-1")
        unpublished = make_fields("6.0.0", "alpha-2")
        unpublished["App URL"] = ""
        current = make_fields("6.0.1", "beta-1")
        published["App URL"] = "https://example.org/releases/v6.0.0-alpha-1"
        current["App URL"] = "https://example.org/releases/v6.0.1-beta-1"
        records = [make_release(row, index) for index, row in enumerate(
            (published, unpublished, current), 1)]
        readme_table = complete_table(records)
        dockerhub_table = short_table(records)
        branch_row = next(line for line in readme_table.splitlines()
                          if "`v6.0.0-alpha-2`" in line)
        self.assertNotIn("](", branch_row)
        self.assertIn("Branch only; not published", branch_row)
        self.assertNotIn("alpha-2", dockerhub_table)
        self.assertIn("v6.0.1-beta-1", dockerhub_table)
        self.assertNotIn("alpha-2", buildx_command(records[-1]))

    def test_release_requires_the_configured_suffix(self):
        self.write_versions("5.2.3")
        with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
            writer = csv.DictWriter(file, fieldnames=COLUMNS)
            writer.writeheader()
            writer.writerow(make_fields("5.2.3"))
        result = self.run_command(
            "bash", "ci/publish-image.sh", "--print-tags", RELEASE_TAG="v5.2.3-beta-1",
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("does not match APP_VERSION", result.stderr)
        self.assertEqual(result.stdout, "")

    def test_unpublished_branch_is_not_eligible_for_buildx_generation(self):
        with (self.root / "ci/versions/versions.csv").open("w", newline="") as file:
            writer = csv.DictWriter(file, fieldnames=COLUMNS)
            writer.writeheader()
            unpublished = make_fields("6.0.0", "alpha-2")
            unpublished["App URL"] = ""
            writer.writerow(unpublished)
        for doc in ("README.md", "DOCKERHUB.md"):
            (self.root / doc).write_text(f"{TABLE_BEGIN}\n{TABLE_END}\n")
        result = self.run_command(sys.executable, "-B", "ci/versions/tags_table.py",
                                  "--buildx-version", "v6.0.0-alpha-2")
        self.assertEqual(result.returncode, 2)
        self.assertIn("has no published image", result.stderr)

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
