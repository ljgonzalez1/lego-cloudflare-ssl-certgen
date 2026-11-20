#!/usr/bin/env python3
"""Offline regressions for durable badge snapshots and their separate workflow."""

import base64
import copy
import csv
import io
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
from urllib.request import Request
import xml.etree.ElementTree as ET

import yaml

import badges as b
from test_workflows import accepts

ROOT = Path(__file__).resolve().parent.parent
NOW = "2026-11-19T23:00:00Z"
HEAD = "a" * 40
LATER = "b" * 40
RELEASE = "c" * 40


def run(number=10, *, event="push", status="completed", conclusion="success", attempt=1,
        branch="master", repository=b.REPOSITORY, sha=HEAD):
    return {"id": 1000 + number, "run_number": number, "run_attempt": attempt,
            "event": event, "head_branch": branch, "head_repository": {"full_name": repository},
            "head_sha": sha, "status": status, "conclusion": conclusion}


def versions(app="v6.0.1", suffix="beta-6", lego="v4.34.0"):
    out = io.StringIO()
    writer = csv.writer(out)
    writer.writerow(["App Version", "App Suffix", "Lego Version", "Lego Suffix"])
    writer.writerow([app, suffix, lego, ""])
    return out.getvalue().encode()


def publication(number=8, attempt=1):
    return {"run_id": 1000 + number, "run_number": number, "run_attempt": attempt,
            "head_sha": RELEASE, "app_version": "v6.0.1-beta-6", "lego_version": "v4.34.0",
            "confirmed_at": "2026-11-18T00:00:00Z"}


class FakeGitHub:
    repository = b.REPOSITORY

    def __init__(self):
        self.pushes = [run()]
        self.releases = []
        self.job_list = [{"name": b.PUBLISHER, "status": "completed", "conclusion": "success"}]
        self.refs = (HEAD, "d" * 40)
        self.files = {}
        for path in ["README.md", "licenses/assets.json", b.MANIFEST, *b.GENERATED, "ci/badges.py",
                     "licenses/badges/sources.json", "licenses/badges/SimpleIcons-LICENSE.md",
                     *("ci/badge-icons/" + name + ".svg" for name in ("docker", "github", "gitea", "go"))]:
            self.files[HEAD, path] = (ROOT / path).read_bytes()
        self.files[RELEASE, "ci/versions/versions.csv"] = versions()
        self.reads = []
        self.commits = []
        self.conflict = False
        self.errors = set()

    def current(self):
        return self.refs

    def file(self, path, sha):
        self.reads.append((path, sha))
        return self.files.get((sha, path))

    def runs(self, event, *, status=None):
        if event in self.errors:
            raise b.APIError(403, "Read denied")
        return self.pushes if event == "push" else self.releases

    def jobs(self, candidate):
        return self.job_list

    def commit(self, parent, tree, changes, message):
        if self.conflict:
            self.conflict = False
            for (sha, path), data in list(self.files.items()):
                if sha == HEAD:
                    self.files[LATER, path] = data
            self.files[LATER, "README.md"] += b"\nHuman-authored concurrent README change.\n"
            self.files[LATER, "licenses/assets.json"] = self.files[LATER, "licenses/assets.json"].replace(
                b'"schema": 1,', b'"schema": 1,\n  "preserve_user_metadata": true,')
            self.refs = (LATER, "e" * 40)
            raise b.APIError(409, "Concurrent update")
        self.commits.append((parent, tree, changes, message))
        for path, text in changes.items():
            self.files[parent, path] = text.encode()


class BadgeBehavior(unittest.TestCase):
    def state(self, api=None, previous=None, pulls=lambda: 123456):
        return b.collect(api or FakeGitHub(), previous or b.default_state(), NOW, pulls, warning=lambda _: None)

    def test_exact_ci_state_mapping(self):
        cases = [("in_progress", None, "Testing", "orange"),
                 ("completed", "success", "Passing", "40dc4a"),
                 ("completed", "failure", "Failing", "red")]
        for status, conclusion, message, color in cases:
            with self.subTest(status=status, conclusion=conclusion):
                value = b.ci_badge(run(status=status, conclusion=conclusion), NOW)
                self.assertEqual((value["message"], value["color"]), (message, color))
        for status in ("action_required", "unknown"):
            self.assertEqual(b.ci_badge(run(status=status), NOW)["message"], "Unknown")
        for conclusion in ("cancelled", "timed_out", "skipped", "neutral", "action_required", None):
            self.assertEqual(b.ci_badge(run(conclusion=conclusion), NOW)["message"], "Unknown")
        self.assertEqual(b.ci_badge(None, NOW)["color"], "lightgrey")

    def test_release_updates_ci_and_image_during_testing_failure_and_success(self):
        for status, conclusion, message, color in [('queued', None, 'Testing', 'orange'),
                ('in_progress', None, 'Testing', 'orange'), ('completed', 'failure', 'Failing', 'red'),
                ('completed', 'success', 'Passing', '2684d5'), ('completed', 'cancelled', 'Unknown', '2684d5')]:
            api = FakeGitHub()
            api.releases = [run(11, event='release', status=status, conclusion=conclusion, sha=RELEASE)]
            state = self.state(api)
            self.assertEqual(state['ci']['message'], message)
            self.assertEqual(state['tested_release']['color'], color)
            self.assertEqual(state['tested_release']['app_version'], 'v6.0.1-beta-6')
            outputs = b.outputs(ROOT, state)
            self.assertIn('Testing' if color == 'orange' else 'v6.0.1-beta-6', outputs[b.BADGE_DIR+'docker-image.svg'])
            if conclusion != 'success':
                self.assertIsNone(state['publication'])

        api.pushes = [run(13)]
        api.releases = [run(12, event='release', conclusion='failure', sha=RELEASE)]
        self.assertEqual(self.state(api)['ci']['message'], 'Passing')

    def test_release_snapshots_survive_clone_and_expired_history(self):
        api = FakeGitHub()
        api.releases = [run(11, event='release', sha=RELEASE, conclusion='failure')]
        state = self.state(api)
        previous = b.previous_state(b.encode(state).encode(), b.REPOSITORY)
        self.assertEqual(previous['tested_release'], state['tested_release'])
        api.releases = []
        self.assertEqual(self.state(api, previous)['tested_release'], state['tested_release'])

    def test_newest_creation_beats_old_completion_and_reruns(self):
        api = FakeGitHub()
        api.pushes = [run(5, status="completed", conclusion="failure", attempt=9),
                      run(6, status="in_progress", conclusion=None), run(4, attempt=22)]
        value = self.state(api)["ci"]
        self.assertEqual(value["message"], "Testing")
        self.assertEqual(value["run"]["run_number"], 6)
        api.pushes.append(run(6, status="completed", conclusion="success", attempt=2))
        self.assertEqual(self.state(api)["ci"]["run"]["run_attempt"], 2)

    def test_pr_fork_version_and_malformed_runs_never_set_master_status(self):
        api = FakeGitHub()
        api.pushes = [run(100, event="pull_request"), run(101, branch="v6.0.3"),
                      run(102, repository="someone/fork"), run(103, sha="not-a-sha"),
                      dict(run(104), head_repository=None), None, {"event": "push"}]
        self.assertEqual(self.state(api)["ci"]["message"], "Unknown")

    def test_ci_read_failure_becomes_unknown_and_preserves_publication(self):
        api = FakeGitHub(); api.errors.add("push")
        previous = b.default_state(); previous["publication"] = publication()
        previous["ci"] = b.ci_badge(run(), NOW)
        value = self.state(api, previous)
        self.assertEqual(value["ci"]["message"], "Unknown")
        self.assertEqual(value["publication"], previous["publication"])
        self.assertEqual(previous["ci"]["message"], "Passing")

    def test_pull_snapshot_is_exact_integer_and_retained_on_failure(self):
        for number in (0, 1234567890123):
            self.assertEqual(self.state(pulls=lambda: number)["docker_pulls"], {"value": number, "checked_at": NOW})
        previous = b.default_state(); previous["docker_pulls"] = {"value": 73, "checked_at": "old"}
        for bad in (True, -1, "13", 1.4, 1 << 64):
            self.assertEqual(self.state(previous=previous, pulls=lambda: bad)["docker_pulls"], previous["docker_pulls"])
        def failed():
            raise b.APIError(429, "Rate limited")
        self.assertEqual(self.state(previous=previous, pulls=failed)["docker_pulls"], previous["docker_pulls"])

    def test_publication_pairs_versions_from_immutable_released_csv(self):
        api = FakeGitHub(); api.releases = [run(12, event="release", sha=RELEASE)]
        value = self.state(api)["publication"]
        self.assertEqual((value["app_version"], value["lego_version"]), ("v6.0.1-beta-6", "v4.34.0"))
        self.assertEqual(value["head_sha"], RELEASE)
        self.assertIn(("ci/versions/versions.csv", RELEASE), api.reads)
        self.assertFalse(any(path.endswith(".txt") for path, _ in api.reads))

    def test_failed_or_unverified_publications_never_advance_badges(self):
        previous = b.default_state(); previous["publication"] = publication()
        for status, conclusion in [("in_progress", None), ("completed", "failure"), ("completed", "cancelled")]:
            api = FakeGitHub(); api.releases = [run(12, event="release", sha=RELEASE, status=status, conclusion=conclusion)]
            self.assertEqual(self.state(api, previous)["publication"], previous["publication"])
        api = FakeGitHub(); api.releases = [run(12, event="release", sha=RELEASE)]; api.job_list = []
        self.assertIsNone(self.state(api)["publication"])
        api.job_list = [{"name": b.PUBLISHER, "status": "completed", "conclusion": "failure"}]
        self.assertIsNone(self.state(api)["publication"])

    def test_expired_history_and_query_errors_keep_last_confirmed_version(self):
        previous = b.default_state(); previous["publication"] = publication()
        api = FakeGitHub()
        self.assertEqual(self.state(api, previous)["publication"], previous["publication"])
        api.errors.add("release")
        self.assertEqual(self.state(api, previous)["publication"], previous["publication"])
        api.errors.clear(); api.releases = [run(7, event="release", attempt=9, sha=RELEASE)]
        self.assertEqual(self.state(api, previous)["publication"], previous["publication"])

    def test_same_publication_rerun_and_newer_release_advance_provenance(self):
        previous = b.default_state(); previous["publication"] = publication()
        api = FakeGitHub(); api.releases = [run(8, event="release", attempt=2, sha=RELEASE)]
        self.assertEqual(self.state(api, previous)["publication"]["run_attempt"], 2)
        api.releases = [run(13, event="release", sha=RELEASE), run(9, event="release", sha=RELEASE)]
        self.assertEqual(self.state(api, previous)["publication"]["run_number"], 13)

    def test_invalid_release_source_retains_old_pair_and_legacy_only_when_missing(self):
        previous = b.default_state(); previous["publication"] = publication()
        api = FakeGitHub(); api.releases = [run(12, event="release", sha=RELEASE)]
        api.files[RELEASE, "ci/versions/versions.csv"] = b"bad,header\nwrong,value\n"
        self.assertEqual(self.state(api, previous)["publication"], previous["publication"])
        del api.files[RELEASE, "ci/versions/versions.csv"]
        api.files[RELEASE, "ci/versions/versions.txt"] = b"APP_VERSION=6.0.1-beta-6\nLEGO_VERSION=4.34.0\n"
        self.assertEqual(self.state(api)["publication"]["app_version"], "v6.0.1-beta-6")

    def test_current_master_version_cannot_advertise_an_unpublished_image(self):
        value = self.state()
        self.assertIsNone(value["publication"])
        rendered = b.outputs(ROOT, value)
        for name in ("docker-image", "lego"):
            title = ET.fromstring(rendered[b.BADGE_DIR + name + ".svg"]).find("{" + b.SVG + "}title").text
            self.assertIn("Unknown", title)

    def test_bad_saved_metadata_resets_without_inventing_success(self):
        for data in (b"not json", b"[]", b"null", b'{"schema":1}', b'{"schema":99}'):
            self.assertEqual(b.previous_state(data, b.REPOSITORY), b.default_state())
        value = b.default_state(); value["publication"] = publication(); value["docker_pulls"] = {"value": 0, "checked_at": NOW}
        self.assertEqual(b.previous_state(b.encode(value), b.REPOSITORY), value)
        self.assertEqual(b.previous_state(b.encode(value), "different/repo"), b.default_state("different/repo"))

    def test_release_csv_uses_final_row_suffix_and_rejects_injection(self):
        self.assertEqual(b.release_versions(versions("V6.0.3", "")), ("v6.0.3", "v4.34.0"))
        for bad in ("6.0.3; echo secret", "$(anything)", "../6.0.3", "", "v1.2", "vV6.0.3"):
            with self.subTest(value=bad), self.assertRaises(ValueError):
                b.release_versions(versions(bad))
        with self.assertRaises(ValueError):
            b.release_versions(None, b'APP_VERSION=6.0.1\nAPP_VERSION=6.0.2\nLEGO_VERSION=4.34.0\n')

    def test_rendered_images_have_no_network_or_active_content(self):
        value = self.state()
        value["publication"] = publication()
        rendered = b.outputs(ROOT, value)
        self.assertEqual(set(rendered), set(b.WRITABLE))
        for name in b.NAMES:
            image = rendered[b.BADGE_DIR + name + ".svg"]
            doc = ET.fromstring(image)
            self.assertGreater(int(doc.attrib["width"]), 20)
            self.assertEqual(doc.attrib["height"], "20")
            for node in doc.iter():
                self.assertNotIn(node.tag.rsplit("}", 1)[-1], {"script", "image", "style", "foreignObject"})
                self.assertFalse(any("href" in key or key.lower().startswith("on") for key in node.attrib))
            self.assertNotIn("img.shields.io", image)
            self.assertNotIn("raw.githubusercontent", image)
        manifest = json.loads(rendered[b.MANIFEST])
        entries = {entry["path"]: entry for entry in manifest["assets"]}
        self.assertTrue(b.GENERATED <= entries.keys())
        self.assertEqual(entries[b.BADGE_DIR + "ci.svg"]["license"], "MIT AND CC0-1.0")

    def test_svg_text_is_xml_escaped(self):
        image = b.shield('label<&"', 'value<&"', "red")
        doc = ET.fromstring(image)
        self.assertIn('label<&"', doc.attrib["aria-label"])
        self.assertEqual(len(doc.findall(".//{" + b.SVG + "}script")), 0)

    def test_updates_never_write_markdown_or_the_global_asset_inventory(self):
        state = b.default_state(); state["publication"] = publication()
        rendered = b.outputs(ROOT, state)
        self.assertTrue(all(path.startswith("assets/badges/") for path in rendered))
        self.assertNotIn("README.md", rendered)
        self.assertNotIn("licenses/assets.json", rendered)
        api = FakeGitHub(); original = api.files[HEAD, "README.md"]
        b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42)
        self.assertEqual(api.files[HEAD, "README.md"], original)

    def test_publish_is_atomic_allowlisted_and_noop_when_unchanged(self):
        api = FakeGitHub()
        self.assertTrue(b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42))
        self.assertEqual(len(api.commits), 1)
        self.assertTrue(api.commits[0][2].keys() <= b.WRITABLE)
        self.assertIn(b.STATE, api.commits[0][2])
        self.assertFalse(b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42))
        self.assertEqual(len(api.commits), 1)

    def test_concurrent_master_commit_is_preserved_and_recomputed(self):
        api = FakeGitHub(); api.conflict = True
        self.assertTrue(b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42))
        parent, _, changed, _ = api.commits[0]
        self.assertEqual(parent, LATER)
        self.assertIn(b"Human-authored concurrent README change.", api.files[LATER, "README.md"])
        self.assertTrue(json.loads(api.files[LATER, "licenses/assets.json"])["preserve_user_metadata"])
        self.assertNotIn("README.md", changed)
        self.assertNotIn("licenses/assets.json", changed)
        self.assertTrue(all(b.SHA.fullmatch(sha) for _, sha in api.reads))

    def test_newer_badge_source_needs_fresh_checkout_instead_of_stale_write(self):
        api = FakeGitHub(); api.files[HEAD, "ci/badges.py"] += b"\n# changed on master\n"
        with self.assertRaisesRegex(ValueError, "sources changed"):
            b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42)
        self.assertEqual(api.commits, [])

    def test_denied_write_does_not_force_push_or_modify_any_source(self):
        api = FakeGitHub()
        before = copy.deepcopy(api.files)
        with patch.object(api, "commit", side_effect=b.APIError(403, "Branch rule denied")):
            with self.assertRaises(b.APIError):
                b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42)
        self.assertEqual(api.files, before)

    def test_github_client_uses_immutable_files_and_non_forced_reference_update(self):
        client = b.GitHub(b.REPOSITORY, "offline-token-never-store")
        calls = []
        def api(path, **kwargs):
            calls.append((path, kwargs))
            return {"sha": "f" * 40}
        with patch.object(client, "api", side_effect=api):
            client.commit(HEAD, "d" * 40, {b.STATE: "{}\n"}, "Refresh badges")
            with self.assertRaises(ValueError):client.commit(HEAD, "d" * 40, {"build/cmd/certgen/main.go": "bad"}, "not allowed")
        self.assertEqual(calls[-1][1]["payload"]["force"], False)
        self.assertEqual(calls[0][1]["payload"]["base_tree"], "d" * 40)
        self.assertNotIn("offline-token-never-store", repr(calls))
        content = {"type": "file", "encoding": "base64", "content": base64.b64encode(b"correct").decode()}
        with patch.object(client, "api", return_value=content) as request:
            self.assertEqual(client.file("ci/versions/versions.csv", RELEASE), b"correct")
            self.assertIn("ref=" + RELEASE, request.call_args.args[0])
            with self.assertRaises(ValueError):client.file("README.md", "master")

    def test_docker_public_counter_never_receives_github_credentials(self):
        with patch.object(b, "request_json", return_value={"pull_count": 723}) as request:
            self.assertEqual(b.docker_pulls(), 723)
            self.assertNotIn("token", request.call_args.kwargs)
        with patch.object(b, "request_json", side_effect=[b.APIError(404, "legacy"), {"pull_count": 9}]) as request:
            self.assertEqual(b.docker_pulls(), 9)
            self.assertEqual(request.call_count, 2)
        with self.assertRaises(ValueError):
            b.request_json("https://hub.docker.com/example", token="do-not-leak")
        with self.assertRaises(ValueError):b.request_json("http://api.github.com/example")

    def test_http_json_objects_and_response_bounds_are_checked(self):
        class Opener:
            def __init__(self, body):self.body = body
            def open(self, request, timeout):return io.BytesIO(self.body)
        with patch.object(b, "build_opener", return_value=Opener(b'{"pull_count":0}')):
            self.assertEqual(b.request_json("https://hub.docker.com/example"), {"pull_count": 0})
        for body in (b"[]", b"null", b"not-json", b"x" * ((2 << 20) + 1)):
            with patch.object(b, "build_opener", return_value=Opener(body)):
                with self.assertRaises(ValueError):b.request_json("https://hub.docker.com/example")

    def test_api_redirects_cannot_forward_a_token_to_another_host(self):
        handler = b.SameHostRedirect()
        original = Request("https://api.github.com/repos/test/repo", headers={"Authorization": "Bearer fixture"})
        for target in ("https://example.com/stolen", "http://api.github.com/insecure"):
            with self.assertRaises(ValueError):handler.redirect_request(original, None, 302, "Found", {}, target)
        target = handler.redirect_request(original, None, 302, "Found", {}, "https://api.github.com/repositories/42")
        self.assertEqual(target.host, "api.github.com")

    def test_publication_job_pagination_is_pinned_to_the_observed_attempt(self):
        client = b.GitHub(b.REPOSITORY, "fixture-token")
        pages = [{"jobs": [{"name": "other"}] * 100}, {"jobs": [{"name": b.PUBLISHER}]}]
        with patch.object(client, "api", side_effect=pages) as api:
            jobs = client.jobs(run(12, event="release", attempt=3))
            self.assertEqual(len(jobs), 101)
            for call in api.call_args_list:self.assertIn("/attempts/3/jobs?", call.args[0])
            self.assertIn("page=2", api.call_args.args[0])

    def test_repeated_write_conflicts_stop_without_a_forced_update(self):
        api = FakeGitHub()
        with patch.object(api, "commit", side_effect=b.APIError(422, "Rejected update")) as commit:
            with self.assertRaises(b.APIError):b.publish(api, ROOT, clock=lambda: NOW, pull_reader=lambda: 42)
            self.assertEqual(commit.call_count, 4)
        self.assertEqual(api.commits, [])

    def test_snapshot_checks_are_offline_and_committed_files_are_reproducible(self):
        with patch.object(b, "request_json", side_effect=AssertionError("Network used by offline check")):
            b.check(ROOT)
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            state = b.default_state()
            result = b.outputs(ROOT, state)
            for path, text in result.items():
                target = root / path;target.parent.mkdir(parents=True, exist_ok=True);target.write_text(text)
            self.assertTrue((root / b.STATE).exists())


class WorkflowContract(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.workflow = yaml.safe_load((ROOT / ".github/workflows/badges.yml").read_text())
        cls.trigger = cls.workflow.get("on", cls.workflow.get(True))

    def test_image_only_push_filter_prevents_loops_even_without_token_suppression(self):
        main=yaml.safe_load((ROOT / ".github/workflows/tests.yml").read_text())
        triggers=main.get("on",main.get(True))
        ignored=set(triggers["push"]["paths-ignore"])
        self.assertEqual(ignored,set(b.WRITABLE))
        self.assertNotIn("README.md",ignored)
        self.assertNotIn("licenses/assets.json",ignored)
        self.assertNotIn("paths-ignore",triggers["pull_request"])
        self.assertTrue(all(path.startswith("assets/badges/") for path in ignored))

    def test_independent_trigger_schedule_and_write_permissions(self):
        self.assertEqual(set(self.trigger), {"workflow_run", "workflow_dispatch", "schedule"})
        self.assertEqual(self.trigger["workflow_run"]["workflows"], ["CI/CD"])
        self.assertEqual(set(self.trigger["workflow_run"]["types"]), {"requested", "in_progress", "completed"})
        self.assertEqual(self.workflow["permissions"], {"actions": "read", "contents": "write"})
        self.assertFalse(self.workflow["concurrency"]["cancel-in-progress"])

    def test_workflow_filters_master_pushes_and_confirmed_releases_only(self):
        expression = self.workflow["jobs"]["update"]["if"]
        for event, branch, kind, action, conclusion, owner, allowed in [
            ("workflow_dispatch", "master", "push", "", "", b.REPOSITORY, True),
            ("workflow_dispatch", "v6.0.3", "push", "", "", b.REPOSITORY, False),
            ("schedule", "master", "push", "", "", b.REPOSITORY, True),
            ("workflow_run", "master", "push", "in_progress", "", b.REPOSITORY, True),
            ("workflow_run", "master", "push", "completed", "failure", b.REPOSITORY, True),
            ("workflow_run", "v6.0.3", "push", "completed", "success", b.REPOSITORY, False),
            ("workflow_run", "master", "pull_request", "completed", "success", b.REPOSITORY, False),
            ("workflow_run", "master", "push", "completed", "success", "fork/repo", False),
            ("workflow_run", "v6.0.3", "release", "completed", "success", b.REPOSITORY, True),
            ("workflow_run", "v6.0.3", "release", "completed", "failure", b.REPOSITORY, True)]:
            github = {"event_name": event, "ref": "refs/heads/" + branch, "repository": b.REPOSITORY,
                      "event": {"action": action, "workflow_run": {"event": kind, "head_branch": branch,
                                "conclusion": conclusion, "head_repository": {"full_name": owner}}}}
            with self.subTest(event=event, branch=branch, kind=kind, owner=owner):
                self.assertEqual(accepts(expression, github, {}), allowed)

    def test_privileged_writer_uses_trusted_master_without_pr_artifacts(self):
        steps = self.workflow["jobs"]["update"]["steps"]
        checkout = next(step for step in steps if step.get("uses", "").startswith("actions/checkout@"))
        self.assertEqual(checkout["with"]["ref"], "master")
        self.assertFalse(checkout["with"]["persist-credentials"])
        content = (ROOT / ".github/workflows/badges.yml").read_text()
        self.assertNotIn("download-artifact", content)
        self.assertNotIn("workflow_run.head_sha", content)
        self.assertIn("python3 -B ci/badges.py --github", content)
        self.assertNotIn("pull_request_target", content)

    def test_existing_ci_requires_badge_regressions_and_integrity_check(self):
        workflow = yaml.safe_load((ROOT / ".github/workflows/tests.yml").read_text())
        offline = str(workflow["jobs"]["offline"])
        self.assertIn("ci/test_badges.py", offline)
        self.assertIn("ci/badges.py --check", offline)


if __name__ == "__main__":
    unittest.main(verbosity=2)
