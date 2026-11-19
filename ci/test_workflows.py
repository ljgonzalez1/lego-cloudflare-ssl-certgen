#!/usr/bin/env python3
"""Check CI event routing, job dependencies and separate propagation waits offline."""

import os
from pathlib import Path
import re
import subprocess
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parent.parent
SMOKE_JOBS = {
    "smoke_armv6": "linux/arm/v6", "smoke_armv7": "linux/arm/v7", "smoke_arm64": "linux/arm64",
    "smoke_i386": "linux/386", "smoke_amd64": "linux/amd64",
}


class Context(dict):
    def __getattr__(self, name):
        return self.get(name)


class ContextList(list):
    def __getitem__(self, index):
        return super().__getitem__(index) if 0 <= index < len(self) else None


def as_context(value):
    if isinstance(value, dict):
        return Context({key: as_context(item) for key, item in value.items()})
    if isinstance(value, list):
        return ContextList(as_context(item) for item in value)
    return value


def accepts(expression, github, needs):
    # Only evaluate expressions read from this repository's own workflow.
    expression = expression.removeprefix("${{").removesuffix("}}").strip()
    expression = expression.replace("&&", " and ").replace("||", " or ")
    expression = re.sub(r"(?<!['\"])\btrue\b(?!['\"])", "True", expression)
    expression = re.sub(r"(?<!['\"])\bfalse\b(?!['\"])", "False", expression)
    expression = re.sub(r"(?<!['\"])\bnull\b(?!['\"])", "None", expression)
    return bool(eval("(" + expression + ")", {"__builtins__": {}}, {
        "github": as_context(github), "needs": as_context(needs),
    }))


class WorkflowChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = yaml.safe_load((ROOT / ".github/workflows/tests.yml").read_text())
        cls.jobs = cls.workflow["jobs"]

    def route(self, event, branch="master", *, created=False, commits=None, deleted=False, failure=None):
        github = {"event_name": event, "ref": "refs/heads/" + branch,
                  "event": {"created": created, "deleted": deleted,
                            "commits": [] if commits is None else commits}}
        results = {}
        for name, job in self.jobs.items():
            needs = job.get("needs", [])
            if isinstance(needs, str):
                needs = [needs]
            ready = all(results[dependency]["result"] == "success" for dependency in needs)
            allowed = ready and accepts(job.get("if", "true").strip(), github, results)
            result = "failure" if allowed and name == failure else ("success" if allowed else "skipped")
            results[name] = {"result": result, "outputs": {"eligible": "true"}}
        return {name for name, result in results.items() if result["result"] == "success"}

    def test_push_version_runs_documents_and_offline(self):
        self.assertEqual(self.route("push", "v4.2.14", commits=[{"id": "new"}]),
                         {"version_readme", "version_dockerhub", "offline"})

    def test_master_and_pull_requests_run_five_parallel_smokes(self):
        expected = {"version_readme", "version_dockerhub", "offline", *SMOKE_JOBS}
        for event in ("push", "pull_request"):
            with self.subTest(event=event):
                self.assertEqual(self.route(event, commits=[{"id": "new"}]), expected)

    def test_release_runs_the_complete_graph(self):
        self.assertEqual(self.route("release", "v4.2.14"), set(self.jobs))

    def test_creation_without_commits_is_skipped_when_guard_is_installed(self):
        guard = self.jobs["version_readme"].get("if", "")
        if "github.event.created" not in guard:
            self.skipTest("The branch-creation guard starts with hotfix v4.2.14.")
        for branch in ("v4.2.14", "V344.26626.0-beta-1", "4.2.14", "v4.2.14/docs"):
            with self.subTest(branch=branch):
                self.assertEqual(self.route("push", branch, created=True), set())
        # An initial push that also creates the ref still tests its new commits.
        self.assertIn("offline", self.route("push", "v4.2.14", created=True, commits=[{"id": "new"}]))

    def test_branch_deletion_is_skipped(self):
        self.assertEqual(self.route("push", "v4.2.14", deleted=True), set())

    def test_create_event_has_no_subscription(self):
        triggers = self.workflow.get("on", self.workflow.get(True))
        self.assertNotIn("create", triggers)
        self.assertEqual(set(triggers), {"push", "pull_request", "release"})

    def test_failed_checks_block_dependents(self):
        for failed in ("version_readme", "version_dockerhub", "offline", *SMOKE_JOBS):
            with self.subTest(failed=failed):
                passed = self.route("release", failure=failed)
                self.assertNotIn("publish", passed)
                self.assertNotIn("verify_image", passed)
                self.assertNotIn("verify_hashes", passed)
                self.assertNotIn("verify_description", passed)
                if failed.startswith("version_"):
                    self.assertNotIn("offline", passed)
                if failed == "offline":
                    self.assertTrue(set(SMOKE_JOBS).isdisjoint(passed))

    def test_post_publish_branches_are_independent(self):
        passed = self.route("release", failure="verify_image")
        self.assertIn("verify_description", passed)
        passed = self.route("release", failure="update_description")
        self.assertIn("verify_image", passed)
        self.assertNotIn("wait_description", passed)
        self.assertNotIn("verify_description", passed)
        passed = self.route("release", failure="publish")
        self.assertTrue({"wait_image", "update_description", "wait_description"}.isdisjoint(passed))

    def test_dependencies_and_visible_architectures(self):
        self.assertEqual(set(self.jobs["offline"]["needs"]), {"version_readme", "version_dockerhub"})
        for job, platform in SMOKE_JOBS.items():
            with self.subTest(job=job):
                self.assertEqual(self.jobs[job]["needs"], "offline")
                self.assertEqual(self.jobs[job]["with"]["platform"], platform)
                self.assertEqual(self.jobs[job]["uses"], "./.github/workflows/smoke.yml")
                self.assertNotIn("strategy", self.jobs[job])
        self.assertEqual(set(self.jobs["publish"]["needs"]), set(SMOKE_JOBS))
        for job, dependency in {"wait_image": "publish", "verify_image": "wait_image",
                                "update_description": "publish", "wait_description": "update_description",
                                "verify_description": "wait_description"}.items():
            self.assertEqual(self.jobs[job]["needs"], dependency)

    def test_waits_are_separate_passing_jobs(self):
        for name in ("wait_image", "wait_description"):
            job = self.jobs[name]
            self.assertEqual(len(job["steps"]), 1)
            script = job["steps"][0]["run"]
            seconds, = re.findall(r"^\s*sleep (\d+)\s*$", script, re.M)
            expected_seconds = "20" if job["name"].startswith("Test:") else "30"
            self.assertEqual(seconds, expected_seconds)
            if seconds == "20":
                checker = (ROOT / "ci/check-dockerhub.sh").read_text()
                self.assertNotRegex(checker, r"(?m)^\s*sleep ")
            self.assertIn("echo Done waiting", script)
            self.assertEqual(script.strip().splitlines()[-1].strip(), "true")
            with tempfile.TemporaryDirectory() as directory:
                sleep = Path(directory) / "sleep"
                log = Path(directory) / "elapsed"
                sleep.write_text('#!/bin/sh\nprintf "%s" "$1" > "$WAIT_LOG"\n')
                sleep.chmod(0o755)
                result = subprocess.run(["bash", "-e", "-c", script], text=True, capture_output=True,
                                        env=os.environ | {"PATH": directory + os.pathsep + os.environ["PATH"],
                                                          "WAIT_LOG": str(log)}, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(log.read_text(), seconds)
                self.assertIn("Done waiting", result.stdout)

    def test_branch_name_filter(self):
        valid = ("v4.2", "V344.26626.0", "4.2.1.2", "2.5-build.75611152.21",
                 "2.5.build-75611152.21", "v1.2-alfa", "v1.2.beta", "v4.2.14/docs")
        invalid = ("random", "v1-beta.75611152.0", "1.beta", "v1.2.3.4.5", "v1.2-typo", "v1.2-beta.")
        for branch in valid + invalid:
            with self.subTest(branch=branch):
                result = subprocess.run(["bash", str(ROOT / "ci/is-version-branch.sh"), branch],
                                        capture_output=True, text=True, timeout=5)
                self.assertEqual(result.returncode, 0 if branch in valid else 1)

    def test_platform_hash_jobs_depend_on_tag_and_publisher(self):
        job = self.jobs["verify_hashes"]
        self.assertEqual(set(job["needs"]), {"publish", "verify_image"})
        self.assertFalse(job["strategy"]["fail-fast"])
        matrix = job["strategy"]["matrix"]["include"]
        self.assertEqual({row["platform"] for row in matrix}, set(SMOKE_JOBS.values()))
        self.assertEqual(len(matrix), 5)
        outputs = self.jobs["publish"]["outputs"]
        self.assertEqual(set(outputs), {"digest", *(row["output"] for row in matrix)})
        for row in matrix:
            self.assertEqual(outputs[row["output"]], "${{ steps.push.outputs." + row["output"] + " }}")
        step = job["steps"][-1]
        self.assertEqual(step["env"]["EXPECTED_DIGEST"], "${{ needs.publish.outputs[matrix.output] }}")
        self.assertEqual(step["env"]["TAG"], "${{ needs.verify_image.outputs.tag }}")
        self.assertIn('"$EXPECTED_DIGEST"', step["run"])
        self.assertNotIn("image", outputs)
        self.assertIn("verify_hashes", self.route("release", failure="update_description"))
        self.assertNotIn("verify_hashes", self.route("release", failure="verify_image"))

    def test_unrelated_branch_is_ineligible(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / "output"
            result = subprocess.run(["bash", "ci/check-version.sh", "README.md"], cwd=ROOT,
                                    env=os.environ | {"GITHUB_EVENT_NAME": "push", "TARGET_BRANCH": "feature/random",
                                                      "VERSION_REF": "feature/random", "GITHUB_OUTPUT": str(output)},
                                    text=True, capture_output=True, timeout=5)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(output.read_text(), "eligible=false\n")


if __name__ == "__main__":
    unittest.main(verbosity=2)
