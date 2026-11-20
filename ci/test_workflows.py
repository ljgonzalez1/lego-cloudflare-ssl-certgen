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
STATIC_JOBS = {"static_go", "static_bash", "static_python"}
OFFLINE_JOBS = {"offline", "go_tests"}
DOCUMENT_JOBS = {"version_readme", "version_dockerhub"}
PRE_SMOKE_JOBS = DOCUMENT_JOBS | STATIC_JOBS | OFFLINE_JOBS

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


def accepts(expression, github, needs, steps=None):
    # Only evaluate expressions read from this repository's own workflow.
    expression = expression.removeprefix("${{").removesuffix("}}").strip()
    expression = expression.replace("&&", " and ").replace("||", " or ")
    expression = re.sub(r"(?<!['\"])\btrue\b(?!['\"])", "True", expression)
    expression = re.sub(r"(?<!['\"])\bfalse\b(?!['\"])", "False", expression)
    expression = re.sub(r"(?<!['\"])\bnull\b(?!['\"])", "None", expression)
    return bool(eval("(" + expression + ")", {"__builtins__": {}}, {
        "github": as_context(github), "needs": as_context(needs), "steps": as_context(steps or {}),
    }))


class WorkflowChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = yaml.safe_load((ROOT / ".github/workflows/tests.yml").read_text())
        cls.jobs = cls.workflow["jobs"]

    def route(self, event, branch="master", *, created=False, commits=None, deleted=False, failure=None, version_failures=(), eligible=True):
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
            eligibility = "true" if eligible else "false"
            if allowed and name in version_failures:
                eligibility = ""  # check-version.sh exits before emitting eligible on failure.
                check = next(step for step in job["steps"] if step.get("id") == "check")
                if not accepts(check.get("continue-on-error", "false"), github, results):
                    result = "failure"
            results[name] = {"result": result, "outputs": {"eligible": eligibility}}
        return {name for name, result in results.items() if result["result"] == "success"}

    def test_version_failures_are_advisory_only_on_master_pushes(self):
        documents = ("version_readme", "version_dockerhub")
        for failures in ((documents[0],), (documents[1],), documents):
            for event, branch in (("push", "master"), ("pull_request", "master"),
                                  ("release", "master"), ("release", "v6.0.0-alpha-2"), ("push", "v6.0.0-alpha-2")):
                with self.subTest(failures=failures, event=event, branch=branch):
                    passed = self.route(event, branch, version_failures=failures)
                    if event == "push" and branch == "master":
                        self.assertTrue((STATIC_JOBS | OFFLINE_JOBS | set(SMOKE_JOBS)).issubset(passed))
                        self.assertNotIn("publish", passed)
                    else:
                        self.assertNotIn("offline", passed)
                        self.assertTrue(set(SMOKE_JOBS).isdisjoint(passed))
                        self.assertNotIn("publish", passed)
        # Only document validation is advisory; checkout/job and offline failures still block.
        for failure in (*documents, *STATIC_JOBS, *OFFLINE_JOBS):
            self.assertTrue(set(SMOKE_JOBS).isdisjoint(self.route("push", failure=failure)))

    def test_advisory_failures_emit_warnings_and_summaries(self):
        for name, document in (("version_readme", "README.md"), ("version_dockerhub", "DOCKERHUB.md")):
            job = self.jobs[name]
            check = next(step for step in job["steps"] if step.get("id") == "check")
            warning = next(step for step in job["steps"] if step.get("name") == "Report advisory version failure")
            for event, branch in (("push", "master"), ("pull_request", "master"), ("release", "master"), ("push", "v6.0.0-alpha-2")):
                github = {"event_name": event, "ref": "refs/heads/" + branch}
                advisory = event == "push" and branch == "master"
                self.assertEqual(accepts(check["continue-on-error"], github, {}), advisory)
                for outcome in ("success", "failure"):
                    self.assertEqual(accepts(warning["if"], github, {}, {"check": {"outcome": outcome}}),
                                     advisory and outcome == "failure")
            with tempfile.TemporaryDirectory() as directory:
                summary = Path(directory) / "summary.md"
                result = subprocess.run(["bash", "-e", "-c", warning["run"]], text=True, capture_output=True,
                                        env=os.environ | {"GITHUB_STEP_SUMMARY": str(summary)}, timeout=5)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("::warning file=" + document, result.stdout)
                self.assertIn(document, summary.read_text())
                self.assertIn("Subsequent tests continue", summary.read_text())

    def test_push_version_runs_documents_and_offline(self):
        self.assertEqual(self.route("push", "v4.2.14", commits=[{"id": "new"}]),
                         PRE_SMOKE_JOBS)

    def test_master_and_pull_requests_run_five_parallel_smokes(self):
        expected = PRE_SMOKE_JOBS | set(SMOKE_JOBS)
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
        for failed in (*DOCUMENT_JOBS, *STATIC_JOBS, *OFFLINE_JOBS, *SMOKE_JOBS):
            with self.subTest(failed=failed):
                passed = self.route("release", failure=failed)
                self.assertNotIn("publish", passed)
                self.assertNotIn("verify_image", passed)
                self.assertNotIn("verify_hashes", passed)
                self.assertNotIn("verify_description", passed)
                if failed in DOCUMENT_JOBS | STATIC_JOBS:
                    self.assertNotIn("offline", passed)
                if failed in OFFLINE_JOBS:
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
        for name in STATIC_JOBS:
            self.assertEqual(set(self.jobs[name]["needs"]), DOCUMENT_JOBS)
        for name in OFFLINE_JOBS:
            self.assertEqual(set(self.jobs[name]["needs"]), STATIC_JOBS)
        for job, platform in SMOKE_JOBS.items():
            with self.subTest(job=job):
                self.assertEqual(set(self.jobs[job]["needs"]), OFFLINE_JOBS)
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

    def test_ineligible_branches_cannot_reach_analysis_or_offline_jobs(self):
        for event in ("push", "pull_request"):
            passed = self.route(event, "feature/random", eligible=False)
            self.assertTrue((STATIC_JOBS | OFFLINE_JOBS | set(SMOKE_JOBS)).isdisjoint(passed))
            self.assertNotIn("publish", passed)

    def test_static_analysis_is_parallel_and_required_before_both_offline_suites(self):
        for name in STATIC_JOBS:
            job = self.jobs[name]
            self.assertEqual(set(job["needs"]), DOCUMENT_JOBS)
            self.assertNotIn("continue-on-error", job)
            self.assertFalse(any(step.get("continue-on-error") for step in job["steps"]))
            for event in ("push", "pull_request", "release"):
                with self.subTest(job=name, event=event):
                    passed = self.route(event, failure=name)
                    self.assertTrue(OFFLINE_JOBS.isdisjoint(passed))
                    self.assertTrue(set(SMOKE_JOBS).isdisjoint(passed))
                    self.assertNotIn("publish", passed)
                    self.assertTrue((STATIC_JOBS - {name}).issubset(passed))
        expected_scripts = {"static_go": "bash ci/analyze-go.sh",
                            "static_bash": "bash ci/analyze-bash.sh",
                            "static_python": "python3 -B ci/analyze-python.py"}
        for name, command in expected_scripts.items():
            self.assertIn(command, [step.get("run") for step in self.jobs[name]["steps"]])

    def test_offline_suites_run_in_parallel_and_each_blocks_all_smokes(self):
        for failed in OFFLINE_JOBS:
            passed = self.route("release", failure=failed)
            self.assertTrue((OFFLINE_JOBS - {failed}).issubset(passed))
            self.assertTrue(set(SMOKE_JOBS).isdisjoint(passed))
            self.assertNotIn("publish", passed)
        self.assertEqual(set(self.jobs["offline"]["needs"]), set(self.jobs["go_tests"]["needs"]))

    def test_go_debug_race_fuzz_and_compiled_binary_checks_are_required(self):
        job = self.jobs["go_tests"]
        scripts = "\n".join(step.get("run", "") for step in job["steps"])
        for command in ("go test", "go test -race", "-shuffle=on", "-coverprofile=coverage.out",
                        "FuzzParseOptions", "FuzzLoadDomains", "FuzzLegoLog",
                        "-fuzztime=15s", "-parallel=2", "ci/test_go_binary.py",
                        "all=-N -l -d=checkptr=2", "-compressdwarf=false"):
            self.assertIn(command, scripts)
        self.assertNotIn("continue-on-error", job)
        for name in ("static_go", *OFFLINE_JOBS):
            setup = next(step for step in self.jobs[name]["steps"]
                         if step.get("uses", "").startswith("actions/setup-go@"))
            self.assertEqual(setup["with"]["go-version"], "1.27.4")
        native_race = next(step for step in job["steps"] if "go test -race" in step.get("run", ""))
        self.assertEqual(native_race["env"]["CGO_ENABLED"], "1")
        regression = "\n".join(step.get("run", "") for step in self.jobs["offline"]["steps"])
        for command in ("sh ci/tests/run.sh", "ci/test_publish.py", "ci/test_workflows.py", "ci/versions/test_tags.py"):
            self.assertIn(command, regression)
        self.assertIn("go vet", (ROOT / "ci/analyze-go.sh").read_text())

    def test_reusable_smokes_use_native_runners_or_qemu_as_required(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/smoke.yml").read_text())
        job = workflow["jobs"]["staging"]
        self.assertIn("ubuntu-24.04-arm", job["runs-on"])
        qemu = next(step for step in job["steps"] if step.get("uses", "").startswith("docker/setup-qemu-action@"))
        self.assertIn("inputs.platform != 'linux/amd64'", qemu["if"])
        self.assertIn("inputs.platform != 'linux/arm64'", qemu["if"])
        self.assertEqual(job["steps"][-1]["run"], 'bash ci/run-smoke.sh "$PLATFORM"')
        self.assertIn("--network none", (ROOT / "ci/tests/smoke.sh").read_text())

    def test_release_only_publication_keeps_version_and_secret_boundaries(self):
        publish = self.jobs["publish"]
        self.assertEqual(publish["if"], "github.event_name == 'release'")
        step = next(step for step in publish["steps"] if step.get("id") == "push")
        self.assertEqual(step["env"]["RELEASE_TAG"], "${{ github.event.release.tag_name }}")
        self.assertIn('cat "$RUNNER_TEMP/certgen-outputs" >> "$GITHUB_OUTPUT"', step["run"])
        for name in STATIC_JOBS | OFFLINE_JOBS | set(SMOKE_JOBS):
            self.assertNotIn("secrets.", str(self.jobs[name]))
        for name in ("wait_image", "wait_description", "verify_image", "verify_description", "verify_hashes"):
            self.assertNotIn("continue-on-error", self.jobs[name])

    def test_compose_defaults_to_unattended_staging_and_keeps_secret_file(self):
        compose = yaml.safe_load((ROOT / "compose.yml").read_text())
        service = compose["services"]["certgen"]
        self.assertEqual(service["command"], ["--staging"])
        self.assertEqual(service["environment"]["CLOUDFLARE_API_TOKEN"], "")
        self.assertEqual(service["secrets"], ["cloudflare_api_token"])
        self.assertNotIn("PRODUCTION", service["environment"])
        self.assertEqual(compose["secrets"]["cloudflare_api_token"]["environment"], "CLOUDFLARE_API_TOKEN")
        # Editing the one documented value must update the actual command.
        changed = yaml.safe_load((ROOT / "compose.yml").read_text().replace('"--staging"', '"--production"'))
        self.assertEqual(changed["services"]["certgen"]["command"], ["--production"])

    def test_license_evidence_is_required_in_build_and_exported_image(self):
        dockerfile = (ROOT / "build/Dockerfile").read_text()
        self.assertIn('FROM tools AS licensed', dockerfile)
        self.assertIn('["/certgen-build", "licenses", "/runtime", "/licenses"]', dockerfile)
        self.assertIn('RUN --network=none ["/certgen-build", "verify-licenses", "/runtime"]', dockerfile)
        self.assertIn('bash ci/check-image-licenses.sh "$image" "$platform"', (ROOT / "ci/tests/smoke.sh").read_text())
        checker = (ROOT / "ci/check-image-licenses.sh").read_text()
        for required in ('docker export', 'filter="data"', 'verify-licenses'):
            self.assertIn(required, checker)
        self.assertIn('python3 -B ci/check_licenses.py',
                      [step.get("run") for step in self.jobs["static_python"]["steps"]])
        smoke = yaml.safe_load((ROOT / ".github/workflows/smoke.yml").read_text())
        self.assertTrue(any(step.get("uses", "").startswith("actions/setup-go@")
                            for step in smoke["jobs"]["staging"]["steps"]))

    def test_final_image_copies_only_the_execution_checked_filesystem(self):
        dockerfile = (ROOT / "build/Dockerfile").read_text()
        self.assertIn("FROM scratch AS runtime-check", dockerfile)
        self.assertIn('"image-self-test"', dockerfile)
        final = dockerfile.split("FROM scratch AS target", 1)[1]
        self.assertIn("COPY --from=licensed /runtime/ /", final)
        self.assertNotIn("COPY --from=builder", final)
        self.assertIn("CERTGEN_LOG_DIR=/ssl-certs/debug", final)
        smoke = (ROOT / "ci/tests/smoke.sh").read_text()
        self.assertIn("debugger.json", smoke)
        self.assertIn("target final snapshot", smoke)


if __name__ == "__main__":
    unittest.main(verbosity=2)
