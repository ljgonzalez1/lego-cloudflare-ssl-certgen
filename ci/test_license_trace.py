#!/usr/bin/env python3
"""Exercise the license diagnostic runner offline, including pipeline failures."""

import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class LicenseTraceTests(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.work = Path(directory.name)
        self.project = self.work / "project"
        scripts = self.project / "ci"
        scripts.mkdir(parents=True)
        for name in ("trace-build.sh", "trace-licenses.sh", "update-license-bundle.sh", "license_bundle.py"):
            shutil.copyfile(ROOT / "ci" / name, scripts / name)
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
            "TRACE_TEST_STATUS": "0",
            "CERTGEN_BUILDER": "cf-lego-certgen-multiarch",
            "TMPDIR": str(self.work),
        }

    def run_trace(self, *arguments, runner="trace-licenses.sh"):
        return subprocess.run(["bash", str(self.project / "ci" / runner), *arguments],
                              cwd=self.work, env=self.environment, capture_output=True,
                              text=True, timeout=10)

    def test_success_saves_both_streams_and_runs_only_the_license_target(self):
        result = self.run_trace("linux/amd64", str(self.log))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        arguments = self.arguments.read_text().splitlines()
        self.assertIn("license trace stdout fixture", self.log.read_text())
        self.assertIn("license trace stderr fixture", self.log.read_text())
        self.assertEqual(arguments[:2], ["buildx", "build"])
        self.assertEqual(arguments[arguments.index("--target") + 1], "licensed")
        self.assertEqual(arguments[arguments.index("--no-cache-filter") + 1], "licensed")
        self.assertEqual(arguments[-1], "./build")
        self.assertIn("--progress=plain", arguments)
        self.assertIn("--output=type=cacheonly", arguments)
        self.assertNotIn("--push", arguments)
        self.assertNotIn("--load", arguments)

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
        result = self.run_trace("all", str(self.log), runner="trace-build.sh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        arguments = self.arguments.read_text().splitlines()
        self.assertEqual(arguments[arguments.index("--target") + 1], "target")
        self.assertIn("--no-cache", arguments)
        self.assertIn("--progress=plain", arguments)
        self.assertIn("--output=type=cacheonly", arguments)
        self.assertNotIn("--push", arguments)
        self.assertNotIn("--load", arguments)

    def test_full_build_preserves_failure_status(self):
        self.environment["TRACE_TEST_STATUS"] = "23"
        result = self.run_trace("linux/amd64", str(self.log), runner="trace-build.sh")
        self.assertEqual(result.returncode, 23, result.stdout + result.stderr)

    def test_default_license_log_path_is_inside_the_project(self):
        result = self.run_trace()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        log = self.project / "trace-licenses.log"
        self.assertTrue(log.exists())
        self.assertIn(str(log), result.stdout)

    def test_default_full_trace_stays_inside_the_project(self):
        result = self.run_trace(runner="trace-build.sh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        log = self.project / "trace.log"
        self.assertTrue(log.exists())
        self.assertIn(str(log), result.stdout)

    def test_bundle_export_failure_keeps_build_status(self):
        self.environment["TRACE_TEST_STATUS"] = "19"
        result = self.run_trace(str(self.log), runner="update-license-bundle.sh")
        self.assertEqual(result.returncode, 19, result.stdout + result.stderr)
        arguments = self.arguments.read_text().splitlines()
        self.assertEqual(arguments[arguments.index("--target") + 1], "license-bundle-export")
        self.assertIn("REFRESH_LICENSE_BUNDLE=1", arguments)
        self.assertNotIn("--push", arguments)
        self.assertNotIn("--load", arguments)
        self.assertIn("license trace stderr fixture", self.log.read_text())

    def test_bundle_export_installs_texts_and_logs_the_installation(self):
        source = self.work / "export-fixture"
        (source / "texts").mkdir(parents=True)
        notice = b"Copyright offline fixture authors\nPermission is hereby granted.\n"
        digest = hashlib.sha256(notice).hexdigest()
        (source / "texts" / (digest + ".txt")).write_bytes(notice)
        bundle = {"schema": 1, "components": [{
            "id": "module:fixture@v1.0.0", "origin": "offline fixture",
            "documents": [{"source": "LICENSE", "sha256": digest}]}],
            "data_files": [{"path": "fixture"}]}
        (source / "bundle.json").write_text(json.dumps(bundle))
        self.environment["TRACE_TEST_EXPORT"] = str(source)
        result = self.run_trace(str(self.log), runner="update-license-bundle.sh")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        target = self.project / "build/licenses/third-party"
        self.assertTrue((target / "bundle.json").exists())
        self.assertEqual((target / "texts" / (digest + ".txt")).read_bytes(), notice)
        self.assertIn("Saved 1 third-party components", self.log.read_text())


if __name__ == "__main__":
    unittest.main(verbosity=2)
