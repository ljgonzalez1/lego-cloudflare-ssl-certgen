#!/usr/bin/env python3
"""Offline regression tests for exact-tag, digest, platform and Markdown checks."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
REPOSITORY = "ljgonzalez/lego-cloudflare-certgen"
sys.path.insert(0, str(ROOT / "ci/versions"))
from release_config import release_config, tag_candidates
VALUES, RELEASE_TAGS = release_config()
CURRENT_TAG = VALUES["APP_TAG"]

CURL_MOCK = r'''#!/usr/bin/env python3
import hashlib
import json
import os
from pathlib import Path
import sys

args = sys.argv[1:]
url = args[-1]
scenario = os.environ["SCENARIO"]
work = Path(os.environ["MOCK_WORK"])
output = Path(args[args.index("--output") + 1])
headers = Path(args[args.index("--dump-header") + 1])
category = "auth" if url.startswith("https://auth.docker.io/") else (
    "registry" if url.startswith("https://registry-1.docker.io/") else (
        "tag" if "/tags/" in url else "description"))
requests = work / "requests.jsonl"
previous = [json.loads(line) for line in requests.read_text().splitlines()]
number = 1 + sum(item["category"] == category for item in previous)
with requests.open("a") as log:
    log.write(json.dumps({"url": url, "category": category, "args": args}) + "\n")
status = 200
payload = b""
exit_status = 0
index = (work / "index.json").read_bytes()
expected = "sha256:" + hashlib.sha256(index).hexdigest()
header_digest = expected

if category == "auth":
    assert "scope=repository:" + os.environ["MOCK_REPOSITORY"] + ":pull" in args
    payload = b'{"token":"fixture-pull-token"}'
    if scenario == "access-token": payload = b'{"access_token":"fixture-pull-token"}'
    if scenario == "auth-empty": payload = b''
    if scenario == "auth-invalid": payload = b'{"token":""}'
    if scenario == "auth-html": payload = b'<html>Blocked</html>'
    if scenario == "auth-401": status = 401
    if scenario == "auth-multiple": payload = b'{"token":"fixture-pull-token"}\n{}'
    if scenario == "auth-control": payload = b'{"token":"unsafe\\r\\nheader"}'
elif category == "registry":
    assert url.endswith("/manifests/" + os.environ["MOCK_TAG"])
    assert "Authorization: Bearer fixture-pull-token" in args
    assert any("application/vnd.oci.image.index.v1+json" in value for value in args)
    payload = index
    if scenario.startswith("registry-"):
        kind = scenario.removeprefix("registry-")
        if kind.isdecimal(): status = int(kind)
        if kind == "timeout": exit_status = 28
        if kind == "dns": exit_status = 6
        if kind == "tls": exit_status = 60
        if kind == "html": payload = b'<html>Docker sign-in</html>'
        if kind == "empty": payload = b''
        if kind == "malformed": payload = b'{"schemaVersion":'
        if kind == "multiple": payload = index + b'\n{}'
        if kind == "single": payload = b'{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[]}'
    if scenario in {"hub-fallback", "hub-legacy", "hub-wrong-name", "hub-wrong-digest", "hub-platforms", "hub-html"}:
        status = 404
    if scenario == "retry" and number == 1: status = 404
    if scenario == "refresh" and number == 1: status = 401
    if scenario == "poison":
        status = 503 if number == 1 else 200
        if number != 1: payload = b''
    if scenario == "wrong-digest":
        payload = index + b'\n'
        header_digest = "sha256:" + hashlib.sha256(payload).hexdigest()
    if scenario == "wrong-header": header_digest = "sha256:" + "0" * 64
else:
    legacy = "/v2/repositories/" in url
    if category == "tag":
        status = 404
        if scenario in {"hub-fallback", "hub-legacy", "hub-wrong-name", "hub-wrong-digest", "hub-platforms", "hub-html"}:
            status = 404 if scenario == "hub-legacy" and not legacy else 200
            tag = {"name": os.environ["MOCK_TAG"], "digest": expected,
                   "images": [item["platform"] for item in json.loads(index)["manifests"]]}
            if scenario == "hub-wrong-name": tag["name"] = "latest"
            if scenario == "hub-wrong-digest": tag["digest"] = "sha256:" + "0" * 64
            if scenario == "hub-platforms": tag["images"].pop()
            payload = json.dumps(tag).encode()
            if scenario == "hub-html": payload = b'<html>Sign in</html>'
        if scenario.startswith("tag-") or scenario.startswith("platform-"):
            requested = url.rstrip("/").rsplit("/", 1)[1]
            target = os.environ["MOCK_TAG"]
            status = 200 if requested == target else 404
            tag = {"name": requested, "digest": expected,
                   "images": [item["platform"] | {"digest": item["digest"]} for item in json.loads(index)["manifests"]]}
            if scenario == "tag-absent": status = 404
            if scenario == "tag-legacy" and not legacy: status = 404
            if scenario == "tag-empty": tag["images"] = []
            if scenario == "tag-wrong-name": tag["name"] = "latest"
            if scenario == "tag-429": status = 429
            if scenario == "tag-503": status = 503
            if scenario == "tag-retry" and number <= 2: status = 404
            if scenario == "platform-index-mismatch": tag["digest"] = "sha256:" + "0" * 64
            if scenario == "platform-no-index-digest": tag.pop("digest")
            if scenario == "platform-wrong-hash": tag["images"][0]["digest"] = "sha256:" + "0" * 64
            if scenario == "platform-missing-hash": tag["images"][0].pop("digest")
            if scenario == "platform-missing": tag["images"].pop(0)
            if scenario == "platform-duplicate": tag["images"].append(tag["images"][0])
            if scenario == "platform-wrong-arm": tag["images"][0]["variant"] = "v7"
            if scenario == "platform-wrong-arm64": tag["images"][2]["variant"] = "v9"
            if scenario == "platform-arm64-implicit": tag["images"][2].pop("variant")
            if scenario == "platform-swapped": tag["images"][0]["digest"], tag["images"][1]["digest"] = tag["images"][1]["digest"], tag["images"][0]["digest"]
            payload = json.dumps(tag).encode()
            if scenario == "tag-html": payload = b'<html>Sign in</html>'
            if scenario == "tag-multiple": payload += b'\n{}'
    else:
        payload = json.dumps({"full_description": "# Expected\r\n\r\nMarkdown.\r\n \t"}).encode()
        if scenario == "overview-retry" and number <= 2:
            payload = b'{"full_description":"Old Markdown"}'
        if scenario == "overview-wrong": payload = b'{"full_description":"Old Markdown"}'
        if scenario == "overview-empty": payload = b''
        if scenario == "overview-html": payload = b'<html>Sign in</html>'
        if scenario == "overview-missing": payload = b'{}'
        if scenario == "overview-multiple": payload += b'\n{}'
        if scenario == "overview-legacy" and not legacy: status = 404
        if scenario == "overview-503": status = 503

output.write_bytes(payload)
header = "HTTP/2 " + str(status) + "\r\n"
if category == "registry" and scenario != "no-digest-header":
    header += "dOcKeR-cOnTeNt-DiGeSt: " + header_digest + "\r\n"
headers.write_text(header + "\r\n")
if exit_status: sys.exit(exit_status)
sys.stdout.write(str(status))
'''


class DockerHubChecks(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        (self.work / "bin").mkdir()
        curl = self.work / "bin/curl"
        curl.write_text(CURL_MOCK)
        curl.chmod(0o755)
        sleep = self.work / "bin/sleep"
        sleep.write_text('#!/bin/sh\nprintf "%s\\n" "$1" >> "$MOCK_WORK/sleeps"\n')
        sleep.chmod(0o755)
        (self.work / "DOCKERHUB.md").write_text("# Expected\n\nMarkdown.\n")
        platforms = [
            {"os": "linux", "architecture": "arm", "variant": "v6"},
            {"os": "linux", "architecture": "arm", "variant": "v7"},
            {"os": "linux", "architecture": "arm64", "variant": "v8"},
            {"os": "linux", "architecture": "386"},
            {"os": "linux", "architecture": "amd64"},
        ]
        self.index = {
            "schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
            "manifests": [{"digest": "sha256:" + str(i) * 64, "size": 100,
                           "mediaType": "application/vnd.oci.image.manifest.v1+json", "platform": platform}
                          for i, platform in enumerate(platforms, 1)],
        }
        self.environment = os.environ | {
            "PATH": str(self.work / "bin") + os.pathsep + os.environ["PATH"],
            "MOCK_WORK": str(self.work), "MOCK_REPOSITORY": REPOSITORY,
            "DOCKERHUB_USERNAME": "", "DOCKERHUB_PUSH_TOKEN": "",
        }

    def run_check(self, scenario, expected_status, tag="v4.2.12", *, description=False, digest=None):
        payload = json.dumps(self.index, separators=(",", ":")).encode()
        (self.work / "index.json").write_bytes(payload)
        (self.work / "requests.jsonl").write_text("")
        (self.work / "sleeps").unlink(missing_ok=True)
        if digest is None:
            digest = "sha256:" + hashlib.sha256(payload).hexdigest()
        command = ["bash", str(ROOT / "ci/check-dockerhub.sh")]
        if description:
            command += ["description"]
        else:
            command += ["image", f"{REPOSITORY}:{tag}", digest]
        result = subprocess.run(command, cwd=self.work, env=self.environment | {
            "SCENARIO": scenario, "MOCK_TAG": tag,
        }, text=True, capture_output=True, timeout=15)
        self.assertEqual(result.returncode, expected_status, result.stdout + result.stderr)
        requests = [json.loads(line) for line in (self.work / "requests.jsonl").read_text().splitlines()]
        self.assertNotIn("fixture-pull-token", result.stdout + result.stderr)
        return result, requests

    def run_hub_check(self, scenario, expected_status, *, tag=CURRENT_TAG, platform=None,
                      digest=None, index_digest=None):
        payload = json.dumps(self.index, separators=(",", ":")).encode()
        (self.work / "index.json").write_bytes(payload)
        (self.work / "requests.jsonl").write_text("")
        output = self.work / "outputs"; output.write_text("")
        command = ["bash", str(ROOT / "ci/check-dockerhub.sh")]
        if platform is None:
            command += ["tag"]
        else:
            command += ["platform", platform, tag, digest or "sha256:" + "1" * 64]
            if index_digest is not None:
                command.append(index_digest)
        result = subprocess.run(command, cwd=self.work, env=self.environment | {
            "SCENARIO": scenario, "MOCK_TAG": tag, "GITHUB_OUTPUT": str(output),
        }, text=True, capture_output=True, timeout=30)
        self.assertEqual(result.returncode, expected_status, result.stdout + result.stderr)
        requests = [json.loads(line) for line in (self.work / "requests.jsonl").read_text().splitlines()]
        self.assertTrue(all(request["category"] == "tag" for request in requests))
        return result, output.read_text(), requests

    def test_discovers_prefixed_unprefixed_and_composite_tags(self):
        for tag in tag_candidates(VALUES, RELEASE_TAGS):
            with self.subTest(tag=tag):
                result, output, _ = self.run_hub_check("tag-valid", 0, tag=tag)
                self.assertEqual(output, "tag=" + tag + "\n")
                self.assertIn("Found release image", result.stdout)
                self.assertNotIn("ljgonzalez", output)

    def test_tag_absence_and_invalid_responses_fail_without_outputs(self):
        for scenario in ("tag-absent", "tag-empty", "tag-wrong-name", "tag-html", "tag-multiple", "tag-429", "tag-503"):
            with self.subTest(scenario=scenario):
                result, output, _ = self.run_hub_check(scenario, 1)
                self.assertEqual(output, "")
                self.assertIn("None of the exact full-version tags is visible", result.stderr)
        for scenario in ("tag-legacy", "tag-retry"):
            self.run_hub_check(scenario, 0)

    def test_each_platform_matches_its_own_build_digest(self):
        platforms = ("linux/arm/v6", "linux/arm/v7", "linux/arm64", "linux/386", "linux/amd64")
        for i, platform in enumerate(platforms, 1):
            with self.subTest(platform=platform):
                digest = "sha256:" + str(i) * 64
                result, _, _ = self.run_hub_check("platform-valid", 0, platform=platform, digest=digest)
                self.assertIn("expected: " + digest, result.stdout)
                self.assertIn("received: " + digest, result.stdout)
                self.assertIn("Verified SHA-256", result.stdout)
        self.run_hub_check("platform-arm64-implicit", 0, platform="linux/arm64", digest="sha256:" + "3" * 64)
        expected_index = "sha256:" + hashlib.sha256((self.work / "index.json").read_bytes()).hexdigest()
        self.run_hub_check("platform-valid", 0, platform=platforms[0], index_digest=expected_index)
        self.run_hub_check("platform-no-index-digest", 0, platform=platforms[0], index_digest=expected_index)

    def test_platform_mismatch_missing_or_duplicate_fails(self):
        for scenario in ("platform-wrong-hash", "platform-missing-hash", "platform-missing", "platform-duplicate", "platform-wrong-arm", "platform-swapped"):
            with self.subTest(scenario=scenario):
                self.run_hub_check(scenario, 1, platform="linux/arm/v6")
        self.run_hub_check("platform-wrong-arm64", 1, platform="linux/arm64", digest="sha256:" + "3" * 64)
        self.run_hub_check("platform-index-mismatch", 1, platform="linux/arm/v6", index_digest="sha256:" + "a" * 64)
        result, _, _ = self.run_hub_check("platform-wrong-hash", 1, platform="linux/arm/v6")
        self.assertIn("expected: sha256:" + "1" * 64, result.stdout)
        self.assertIn("received: sha256:" + "0" * 64, result.stdout)
        self.assertIn("SHA-256 MISMATCH", result.stderr)

    def test_invalid_platform_inputs_never_reach_docker_hub(self):
        for platform, tag, digest in (("linux/riscv64", CURRENT_TAG, "sha256:" + "1" * 64),
                                      ("linux/arm/v6", "latest", "sha256:" + "1" * 64),
                                      ("linux/arm/v6", CURRENT_TAG, "bad"),
                                      ("linux/arm/v6", "", "sha256:" + "1" * 64)):
            with self.subTest(platform=platform, tag=tag, digest=digest):
                _, _, requests = self.run_hub_check("platform-valid", 2, platform=platform, tag=tag, digest=digest)
                self.assertEqual(requests, [])

    def test_exact_versions_and_suffixes(self):
        for tag in ("v4.2.12", "4.2.12", "V4.2.12", "v2.8.99-beta-5", "v5.2.4-alpha.1", "v5.2.4-alfa", "v344.26626.0", "alpine3.24.4-lego4.34.0-v4.2.15", "lego4.34.0-v4.2.15-beta-1"):
            with self.subTest(tag=tag):
                result, requests = self.run_check("valid", 0, tag)
                self.assertIn("all five platforms", result.stdout)
                self.assertEqual([request["category"] for request in requests], ["auth", "registry"])

    def test_rejects_aliases_and_path_injection(self):
        for tag in ("latest", "v4", "v4.2", "v4.2.12/other", "v4.2.12?tag=latest", "v4.2.12#x", "v4.2.12-", "v4.2.12-" + "x" * 128):
            with self.subTest(tag=tag):
                _, requests = self.run_check("valid", 2, tag)
                self.assertEqual(requests, [])
        _, requests = self.run_check("valid", 2, digest="not-a-digest")
        self.assertEqual(requests, [])

    def test_supported_manifest_formats(self):
        self.run_check("access-token", 0)
        self.run_check("no-digest-header", 0)
        self.index["mediaType"] = "application/vnd.docker.distribution.manifest.list.v2+json"
        self.run_check("valid", 0)

    def test_transient_absence_and_expired_token(self):
        for scenario in ("retry", "refresh"):
            with self.subTest(scenario=scenario):
                _, requests = self.run_check(scenario, 0)
                self.assertEqual(sum(item["category"] == "registry" for item in requests), 2)
                expected_tokens = 2 if scenario == "refresh" else 1
                self.assertEqual(sum(item["category"] == "auth" for item in requests), expected_tokens)

    def test_transport_and_http_failures_are_diagnosed(self):
        for scenario, diagnostic in (("404", "HTTP 404"), ("429", "HTTP 429"), ("503", "HTTP 503"),
                                     ("timeout", "timed out"), ("dns", "DNS resolution"), ("tls", "TLS")):
            with self.subTest(scenario=scenario):
                result, requests = self.run_check("registry-" + scenario, 1)
                self.assertIn(diagnostic, result.stderr)
                self.assertEqual(sum(item["category"] == "registry" for item in requests), 5)

    def test_html_empty_or_invalid_json_never_pass(self):
        for scenario in ("html", "empty", "malformed", "multiple", "single"):
            with self.subTest(scenario=scenario):
                self.run_check("registry-" + scenario, 1)
        self.run_check("poison", 1)

    def test_invalid_authentication_never_passes(self):
        for scenario in ("auth-empty", "auth-invalid", "auth-html", "auth-401", "auth-multiple", "auth-control"):
            with self.subTest(scenario=scenario):
                _, requests = self.run_check(scenario, 1)
                self.assertFalse(any(item["category"] == "registry" for item in requests))

    def test_corrupt_or_unexpected_digest_blocks_fallback(self):
        for scenario in ("wrong-digest", "wrong-header"):
            with self.subTest(scenario=scenario):
                _, requests = self.run_check(scenario, 1)
                self.assertFalse(any(item["category"] == "tag" for item in requests))

    def test_missing_duplicate_or_wrong_architecture_is_rejected(self):
        original = json.loads(json.dumps(self.index))
        for mutation in ("missing", "duplicate", "wrong-arm", "wrong-arm64"):
            with self.subTest(mutation=mutation):
                self.index = json.loads(json.dumps(original))
                if mutation == "missing": self.index["manifests"].pop()
                if mutation == "duplicate": self.index["manifests"].append(self.index["manifests"][0])
                if mutation == "wrong-arm": self.index["manifests"][0]["platform"]["variant"] = "v5"
                if mutation == "wrong-arm64": self.index["manifests"][2]["platform"]["variant"] = "v9"
                _, requests = self.run_check("valid", 1)
                self.assertFalse(any(item["category"] == "tag" for item in requests))

    def test_attestations_are_allowed(self):
        self.index["manifests"].append({"platform": {"os": "unknown", "architecture": "unknown"}})
        self.run_check("valid", 0)
        self.index["manifests"][2]["platform"].pop("variant")
        self.run_check("valid", 0)

    def test_fallback_requires_the_exact_tag_digest_and_platforms(self):
        for scenario in ("hub-fallback", "hub-legacy"):
            with self.subTest(scenario=scenario):
                self.run_check(scenario, 0)
        for scenario in ("hub-wrong-name", "hub-wrong-digest", "hub-platforms", "hub-html"):
            with self.subTest(scenario=scenario):
                self.run_check(scenario, 1)

    def test_authenticated_registry_reads(self):
        self.environment.update(DOCKERHUB_USERNAME="fixture-user", DOCKERHUB_PUSH_TOKEN="fixture-secret")
        result, requests = self.run_check("valid", 0)
        self.assertIn("fixture-user:fixture-secret", requests[0]["args"])
        self.assertNotIn("fixture-secret", result.stdout + result.stderr)

    def test_description_normalization_and_legacy_route(self):
        for scenario in ("valid", "overview-retry", "overview-legacy"):
            with self.subTest(scenario=scenario):
                result, _ = self.run_check(scenario, 0, description=True)
                self.assertIn("description matches DOCKERHUB.md", result.stdout)

    def test_description_content_and_errors_fail(self):
        for scenario in ("overview-wrong", "overview-empty", "overview-html", "overview-missing", "overview-multiple", "overview-503"):
            with self.subTest(scenario=scenario):
                result, _ = self.run_check(scenario, 1, description=True)
                if scenario == "overview-wrong":
                    self.assertIn("--- expected/DOCKERHUB.md", result.stderr)
                    self.assertIn("+++ received/DockerHub.md", result.stderr)
                    self.assertIn("-# Expected", result.stderr)
                    self.assertIn("+Old Markdown", result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
