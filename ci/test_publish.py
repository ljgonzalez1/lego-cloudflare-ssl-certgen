#!/usr/bin/env python3
"""Offline regression tests for metadata collection, output transport and CSV release identity."""

import copy
import csv
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent
spec = importlib.util.spec_from_file_location("publish_result", ROOT / "ci/publish_result.py")
publish = importlib.util.module_from_spec(spec)
spec.loader.exec_module(publish)
from tags_table import PRERELEASE, read_releases

DOCKER_MOCK = r'''#!/usr/bin/env python3
import hashlib
import json
import os
from pathlib import Path
import sys
args = sys.argv[1:]
work = Path(os.environ["MOCK_WORK"])
with (work / "docker-commands.jsonl").open("a") as log:
    log.write(json.dumps(args) + "\n")
if args[0] == "login":
    assert sys.stdin.read() == "fixture-push-token"
elif args[:2] == ["buildx", "build"]:
    if os.environ.get("MOCK_NO_PUSH"):
        assert "--push" not in args and "--output=type=cacheonly" in args
    else:
        assert "--push" in args
    assert args[args.index("--platform") + 1] == "linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64"
    metadata = Path(args[args.index("--metadata-file") + 1])
    assert metadata.name == "build-metadata.json"
    digest = "sha256:" + hashlib.sha256((work / "index.json").read_bytes()).hexdigest()
    metadata.write_text(json.dumps({"containerimage.digest": digest, "containerimage.config.digest": "sha256:" + "f" * 64}))
    if os.environ.get("MOCK_FAIL") == "build": sys.exit(12)
elif args[:3] == ["buildx", "imagetools", "inspect"]:
    assert "--raw" in args
    digest = "sha256:" + hashlib.sha256((work / "index.json").read_bytes()).hexdigest()
    assert args[-1].endswith("@" + digest), "Must inspect the metadata digest, never a mutable tag"
    sys.stdout.buffer.write((work / "index.json").read_bytes() + b"\n")
elif args[:3] == ["buildx", "imagetools", "create"]:
    if os.environ.get("MOCK_FAIL") == "alias": sys.exit(13)
'''


def fixture_index():
    platforms = [
        {"os": "linux", "architecture": "arm", "variant": "v6"},
        {"os": "linux", "architecture": "arm", "variant": "v7"},
        {"os": "linux", "architecture": "arm64", "variant": "v8"},
        {"os": "linux", "architecture": "386"},
        {"os": "linux", "architecture": "amd64"},
    ]
    return {"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json",
            "manifests": [{"digest": "sha256:" + str(i) * 64, "size": 100,
                           "mediaType": "application/vnd.oci.image.manifest.v1+json", "platform": platform}
                          for i, platform in enumerate(platforms, 1)]}


class PublishChecks(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        self.index = fixture_index()

    def write_build(self, *, index=None):
        raw = json.dumps(self.index if index is None else index, separators=(",", ":")).encode()
        digest = "sha256:" + hashlib.sha256(raw).hexdigest()
        metadata = {"containerimage.digest": digest, "containerimage.descriptor": {"digest": digest},
                    "containerimage.config.digest": "sha256:" + "f" * 64}
        (self.work / "index.json").write_bytes(raw)
        (self.work / "metadata.json").write_text(json.dumps(metadata))
        return metadata

    def collect(self):
        return publish.collect(self.work / "metadata.json", self.work / "index.json")

    def test_collects_five_distinct_platform_digests_from_metadata_index(self):
        metadata = self.write_build()
        result = self.collect()
        self.assertEqual(result["digest"], metadata["containerimage.digest"])
        self.assertEqual(result["digests"], {key: "sha256:" + str(i) * 64 for i, key in enumerate(publish.PLATFORMS.values(), 1)})
        self.assertNotIn(metadata["containerimage.config.digest"], result["digests"].values())
        path = self.work / "index.json"
        path.write_bytes(path.read_bytes() + b"\n")
        self.assertEqual(self.collect(), result)

    def test_mismatched_or_invalid_metadata_is_rejected(self):
        for mutation in ("missing", "uppercase", "descriptor", "bytes", "config-only", "multiple-json"):
            with self.subTest(mutation=mutation):
                metadata = self.write_build()
                if mutation == "missing": metadata.pop("containerimage.digest")
                if mutation == "uppercase": metadata["containerimage.digest"] = "sha256:" + "A" * 64
                if mutation == "descriptor": metadata["containerimage.descriptor"]["digest"] = "sha256:" + "0" * 64
                if mutation == "config-only": metadata.pop("containerimage.digest"); metadata.pop("containerimage.descriptor")
                (self.work / "metadata.json").write_text(json.dumps(metadata) + ("\n{}" if mutation == "multiple-json" else ""))
                if mutation == "bytes":
                    path = self.work / "index.json"; path.write_bytes(path.read_bytes() + b" ")
                with self.assertRaises((ValueError, TypeError)):
                    self.collect()

    def test_missing_duplicate_unexpected_or_invalid_platforms_are_rejected(self):
        for mutation in ("missing", "duplicate", "wrong-arm", "wrong-arm64", "digest", "extra-linux"):
            with self.subTest(mutation=mutation):
                index = fixture_index()
                if mutation == "missing": index["manifests"].pop()
                if mutation == "duplicate": index["manifests"].append(index["manifests"][0])
                if mutation == "wrong-arm": index["manifests"][0]["platform"]["variant"] = "v5"
                if mutation == "wrong-arm64": index["manifests"][2]["platform"]["variant"] = "v9"
                if mutation == "digest": index["manifests"][0]["digest"] = "not-a-digest"
                if mutation == "extra-linux": index["manifests"].append({"platform": {"os": "linux", "architecture": "riscv64"}})
                self.write_build(index=index)
                with self.assertRaises(ValueError):
                    self.collect()

    def test_attestations_and_implicit_arm64_v8_are_allowed(self):
        self.index["manifests"].append({"platform": {"os": "unknown", "architecture": "unknown"}, "digest": "sha256:" + "a" * 64})
        self.index["manifests"][2]["platform"].pop("variant")
        self.write_build()
        self.assertEqual(len(self.collect()["digests"]), 5)

    def test_outputs_contain_only_validated_hashes_and_no_secret_username(self):
        self.write_build()
        valid = self.collect()
        result_file = self.work / "result.json"
        invalid = []
        for field, value in (("image", "elsewhere/repo:v1.0.0"), ("digest", "bad"), ("digests", {})):
            record = copy.deepcopy(valid); record[field] = value; invalid.append(record)
        record = copy.deepcopy(valid); record["digests"]["armv6"] = "bad"; invalid.append(record)
        invalid += [[], {}]
        for record in [valid, *invalid]:
            with self.subTest(record=record):
                result_file.write_text(json.dumps(record))
                result = subprocess.run(["bash", "ci/read-publish-result.sh", str(result_file)], cwd=ROOT,
                                        text=True, capture_output=True, timeout=5)
                if record == valid:
                    self.assertEqual(result.returncode, 0, result.stderr)
                    outputs = dict(line.split("=", 1) for line in result.stdout.splitlines())
                    self.assertEqual(outputs, {"digest": valid["digest"], **{"digest_" + key: value for key, value in valid["digests"].items()}})
                    self.assertNotIn("ljgonzalez", result.stdout)
                else:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertEqual(result.stdout, "")
        result_file.write_text(json.dumps(valid) + "\n{}")
        result = subprocess.run(["bash", "ci/read-publish-result.sh", str(result_file)], cwd=ROOT,
                                text=True, capture_output=True, timeout=5)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(result.stdout, "")

    def test_publisher_pushes_all_aliases_then_exports_platform_hashes(self):
        self.write_build()
        (self.work / "bin").mkdir()
        docker = self.work / "bin/docker"; docker.write_text(DOCKER_MOCK); docker.chmod(0o755)
        for failure in ("", "build", "alias"):
            with self.subTest(failure=failure):
                result_file = self.work / "publish.json"; result_file.unlink(missing_ok=True)
                commands = self.work / "docker-commands.jsonl"; commands.write_text("")
                result = subprocess.run(["bash", "ci/publish-image.sh"], cwd=ROOT, text=True, capture_output=True,
                                        env=os.environ | {"PATH": str(self.work / "bin") + os.pathsep + os.environ["PATH"],
                                            "MOCK_WORK": str(self.work), "MOCK_FAIL": failure,
                                            "DOCKERHUB_USERNAME": "ljgonzalez", "DOCKERHUB_PUSH_TOKEN": "fixture-push-token",
                                            "PUBLISH_RESULT_FILE": str(result_file)}, timeout=30)
                self.assertNotIn("fixture-push-token", result.stdout + result.stderr)
                if failure:
                    self.assertNotEqual(result.returncode, 0)
                    self.assertFalse(result_file.exists())
                else:
                    self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                    self.assertEqual(len(json.loads(result_file.read_text())["digests"]), 5)
                    calls = [json.loads(line) for line in commands.read_text().splitlines()]
                    aliases = [call for call in calls if call[:3] == ["buildx", "imagetools", "create"]]
                    _, tags = publish.release_config()
                    self.assertEqual(len(aliases), len(tags) - 1)
                    self.assertEqual([call[call.index("--tag") + 1].split(":", 1)[1] for call in aliases], list(tags[1:]))
                    self.assertEqual("latest" in tags, PRERELEASE.search(tags[5]) is None)
                    if "latest" in tags:
                        self.assertEqual(tags[-1], "latest")


    def test_no_push_runs_the_release_build_without_credentials_or_registry_writes(self):
        self.write_build()
        (self.work / "bin").mkdir()
        docker = self.work / "bin/docker"; docker.write_text(DOCKER_MOCK); docker.chmod(0o755)
        commands = self.work / "docker-commands.jsonl"; commands.write_text("")
        environment = {key: value for key, value in os.environ.items() if not key.startswith("DOCKERHUB_")}
        result = subprocess.run(["bash", "ci/publish-image.sh", "--no-push"], cwd=ROOT, text=True, capture_output=True,
                                env=environment | {"PATH": str(self.work / "bin") + os.pathsep + os.environ["PATH"],
                                                   "MOCK_WORK": str(self.work), "MOCK_NO_PUSH": "1"}, timeout=30)
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        calls = [json.loads(line) for line in commands.read_text().splitlines()]
        self.assertEqual([call for call in calls if call[0] == "login"], [])
        self.assertEqual([call for call in calls if call[:2] == ["buildx", "imagetools"]], [])
        builds = [call for call in calls if call[:2] == ["buildx", "build"]]
        self.assertEqual(len(builds), 1)
        _, tags = publish.release_config()
        self.assertIn("ljgonzalez/lego-cloudflare-certgen:" + tags[0], result.stdout)
        for arguments in (["--no-push", "--push"], ["--push"]):
            result = subprocess.run(["bash", "ci/publish-image.sh", *arguments], cwd=ROOT, text=True, capture_output=True,
                                    env=environment | {"PATH": str(self.work / "bin") + os.pathsep + os.environ["PATH"],
                                                       "MOCK_WORK": str(self.work)}, timeout=30)
            self.assertEqual(result.returncode, 2, arguments)


class ReleaseIdentityChecks(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory(); self.addCleanup(temporary.cleanup)
        self.work = Path(temporary.name)
        for filename in ("ci/versions/release_config.py", "ci/versions/tags_table.py", "ci/versions/versions.txt", "ci/versions/versions.csv",
                         "ci/versions/check_version.py", "ci/is-version-branch.sh", "README.md", "DOCKERHUB.md",
                         "ci/versions.sh", "ci/publish-image.sh"):
            target = self.work / filename; target.parent.mkdir(exist_ok=True, parents=True)
            shutil.copyfile(ROOT / filename, target)
        self.rows = list(csv.reader(io.StringIO((self.work / "ci/versions/versions.csv").read_text())))

    def write_csv(self, prefix="v", suffix="", embedded=""):
        rows = copy.deepcopy(self.rows)
        app = "97.64.13"  # A fixture version that cannot collide with release history.
        rows[-1][8] = prefix + app + embedded
        rows[-1][9] = suffix
        with (self.work / "ci/versions/versions.csv").open("w", newline="") as output:
            csv.writer(output).writerows(rows)
        full = app + embedded + ("-" + suffix if suffix else "")
        (self.work / "ci/versions/versions.txt").write_text(
            f"APP_VERSION={full}\nLEGO_VERSION=4.34.0\nALPINE_VERSION=3.24.4\n")
        return full

    def test_final_csv_row_controls_prefix_suffix_and_all_tags(self):
        for prefix in ("", "v", "V"):
            for embedded, suffix in (("", ""), ("", "beta-1"), ("-alpha.1", ""), ("", "build.42"), ("-rc.1", "")):
                with self.subTest(prefix=prefix, embedded=embedded, suffix=suffix):
                    app = self.write_csv(prefix, suffix, embedded)
                    result = subprocess.run(["bash", "ci/publish-image.sh", "--print-tags"], cwd=self.work,
                                            text=True, capture_output=True, timeout=5)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    tags = [line.split(":", 1)[1] for line in result.stdout.splitlines()]
                    release = read_releases(self.work / "ci/versions/versions.csv")[-1]
                    self.assertEqual(len(tags), len(release.tags))
                    self.assertEqual("latest" in tags, not any(word in app.lower() for word in ("alpha", "alfa", "beta", "rc")))
                    self.assertEqual(tags[5], prefix + app)
                    self.assertTrue(all(tag.endswith(prefix + app) for tag in (tags[0], tags[1], tags[3], tags[4])))
                    candidates = subprocess.check_output(["python3", "-B", "ci/versions/release_config.py", "--tag-candidates"], cwd=self.work, text=True).splitlines()
                    self.assertTrue({app, "v" + app, "V" + app}.issubset(candidates))
                    self.assertNotIn("latest", candidates)
                    self.assertNotIn(tags[6], candidates)

    def test_multiple_version_suffixes_are_rejected(self):
        # New grammar accepts at most one suffix, even when the CSV stores
        # the version suffix in a separate column.
        self.write_csv("v", "build.42", "-beta-1")
        for command in (("bash", "ci/publish-image.sh", "--print-tags"),
                        ("python3", "-B", "ci/versions/tags_table.py")):
            with self.subTest(command=command):
                result = subprocess.run(command, cwd=self.work, text=True,
                                        capture_output=True, timeout=5)
                self.assertNotEqual(result.returncode, 0)

    def test_conflicting_csv_or_versions_fail_before_any_build(self):
        for column, value in ((8, "v99.0.0"), (5, "v99.0.0"), (1, "99.0.0")):
            with self.subTest(column=column):
                rows = copy.deepcopy(self.rows); rows[-1][column] = value
                with (self.work / "ci/versions/versions.csv").open("w", newline="") as output:
                    csv.writer(output).writerows(rows)
                result = subprocess.run(["bash", "ci/publish-image.sh", "--print-tags"], cwd=self.work,
                                        text=True, capture_output=True, timeout=5)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(result.stdout, "")
                self.assertIn("differs", result.stderr)

    def test_both_document_checks_accept_csv_prefixes_and_prerelease_suffixes(self):
        for prefix, embedded, suffix in (("", "", ""), ("v", "", "beta-1"), ("V", "-alpha.1", ""), ("V", "", "build.42")):
            with self.subTest(prefix=prefix, embedded=embedded, suffix=suffix):
                app = self.write_csv(prefix, suffix, embedded)
                generated = subprocess.run(["python3", "-B", "ci/versions/tags_table.py"], cwd=self.work,
                                           text=True, capture_output=True, timeout=5)
                self.assertEqual(generated.returncode, 0, generated.stderr)
                for document in ("README.md", "DOCKERHUB.md"):
                    result = subprocess.run(["python3", "-B", "ci/versions/check_version.py", document, "--ref", prefix + app],
                                            cwd=self.work, text=True, capture_output=True, timeout=5)
                    self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
