#!/usr/bin/env python3
"""Exercise the full build trace runner offline, including pipeline failures."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class BuildTraceTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.work = Path(directory.name)
        self.project = self.work / "project"
        scripts = self.project / "ci"
        scripts.mkdir(parents=True)
        shutil.copyfile(ROOT / "ci/trace-build.sh", scripts / "trace-build.sh")
        fakebin = self.work / "bin"
        fakebin.mkdir()
        docker = fakebin / "docker"
        shutil.copyfile(ROOT / "ci/tests/fixtures/trace-docker.sh", docker)
        docker.chmod(0o755)
        self.arguments = self.work / "arguments.txt"
        self.log = self.work / "build.log"
        self.environment = os.environ | {
            "PATH": str(fakebin) + os.pathsep + os.environ["PATH"],
            "TRACE_TEST_ARGS": str(self.arguments),
            "TRACE_TEST_CREATE": str(self.work / "create.txt"),
            "TRACE_TEST_STATUS": "0",
            "CERTGEN_BUILDER": "cf-lego-certgen-multiarch",
            "TMPDIR": str(self.work),
        }

    def run_trace(self, *arguments):
        return subprocess.run(["bash", str(self.project / "ci/trace-build.sh"), *arguments],
                              cwd=self.work, env=self.environment, capture_output=True,
                              text=True, timeout=10)

    def test_build_failure_status_survives_successful_tee(self):
        self.environment["TRACE_TEST_STATUS"] = "17"
        result = self.run_trace("linux/386", str(self.log))
        self.assertEqual(result.returncode, 17, result.stdout + result.stderr)
        self.assertIn("Buildx exit status: 17", result.stdout)
        self.assertTrue(self.log.exists())

    def test_all_architectures_and_custom_builder_are_supported(self):
        self.environment["CERTGEN_BUILDER"] = "local-test-builder"
        result = self.run_trace("all", str(self.log))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        arguments = self.arguments.read_text().splitlines()
        self.assertEqual(arguments[arguments.index("--builder") + 1], "local-test-builder")
        self.assertEqual(arguments[arguments.index("--platform") + 1],
                         "linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64")

    def test_invalid_platform_is_rejected_before_docker(self):
        result = self.run_trace("linux/not-a-platform", str(self.log))
        self.assertEqual(result.returncode, 2)
        self.assertFalse(self.arguments.exists())
        self.assertFalse(self.log.exists())

    def test_log_write_failure_is_not_hidden_by_successful_build(self):
        result = self.run_trace("linux/amd64", str(self.work))
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("Buildx exit status: 0", result.stdout)

    def test_full_build_traces_the_final_target_without_cache_or_push(self):
        result = self.run_trace("all", str(self.log))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("trace stdout fixture", self.log.read_text())
        self.assertIn("trace stderr fixture", self.log.read_text())
        arguments = self.arguments.read_text().splitlines()
        self.assertEqual(arguments[:2], ["buildx", "build"])
        self.assertEqual(arguments[-1], "./build")
        self.assertEqual(arguments[arguments.index("--target") + 1], "target")
        self.assertIn("--no-cache", arguments)
        self.assertIn("--progress=plain", arguments)
        self.assertIn("--output=type=cacheonly", arguments)
        self.assertNotIn("--push", arguments)
        self.assertNotIn("--load", arguments)

    def test_full_build_preserves_failure_status(self):
        self.environment["TRACE_TEST_STATUS"] = "23"
        result = self.run_trace("linux/amd64", str(self.log))
        self.assertEqual(result.returncode, 23, result.stdout + result.stderr)

    def test_default_full_trace_stays_inside_the_project(self):
        result = self.run_trace()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        log = self.project / "trace.log"
        self.assertTrue(log.exists())
        self.assertIn(str(log), result.stdout)

    def test_existing_builder_is_reused(self):
        result = self.run_trace("linux/amd64", str(self.log))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse((self.work / "create.txt").exists())

    def test_missing_builder_is_created_with_the_pinned_buildkit(self):
        self.environment["TRACE_TEST_MISSING_BUILDER"] = "1"
        result = self.run_trace("linux/amd64", str(self.log))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        created = (self.work / "create.txt").read_text().splitlines()
        self.assertEqual(created[:2], ["buildx", "create"])
        self.assertEqual(created[created.index("--name") + 1], "cf-lego-certgen-multiarch")
        self.assertIn("image=moby/buildkit:v0.35.1", created)
        arguments = self.arguments.read_text().splitlines()
        self.assertEqual(arguments[arguments.index("--builder") + 1], "cf-lego-certgen-multiarch")


if __name__ == "__main__":
    unittest.main(verbosity=2)
