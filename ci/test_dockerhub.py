#!/usr/bin/env python3
"""Offline regression tests for exact-tag, digest, platform and Markdown checks."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
REPOSITORY = "ljgonzalez/lego-cloudflare-certgen"

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

    def test_exact_versions_and_suffixes(self):
        for tag in ("v4.2.12", "4.2.12", "V4.2.12", "v2.8.99-beta-5", "v5.2.4-alpha.1", "v5.2.4-alfa", "v344.26626.0"):
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
                self.run_check(scenario, 1, description=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
