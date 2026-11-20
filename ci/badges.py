#!/usr/bin/env python3
"""Commit badge image snapshots without changing Markdown using only the Python standard library."""

from __future__ import annotations

import argparse
import base64
import copy
import csv
from datetime import datetime, timezone
import hashlib
from html import escape
import io
import json
import os
from pathlib import Path
import re
import sys
from urllib.error import HTTPError, URLError
from urllib.parse import quote, urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent
REPOSITORY = "ljgonzalez1/lego-cloudflare-ssl-certgen"
DOCKER_REPOSITORY = "ljgonzalez/lego-cloudflare-certgen"
GITEA = "https://git.ljgonzalez.cl/ljgonzalez/lego-cert-certgen"
BRANCH = "master"
WORKFLOW = "tests.yml"
PUBLISHER = "Buildx and push to Docker Hub"
BADGE_DIR = "assets/badges/"
STATE = BADGE_DIR + "state.json"
MANIFEST = BADGE_DIR + "manifest.json"
NAMES = ("docker-hub", "docker-pulls", "docker-image", "ci", "gitea", "lego", "license")
GENERATED = frozenset([STATE, *(BADGE_DIR + name + ".svg" for name in NAMES)])
WRITABLE = GENERATED | {MANIFEST}
SHA = re.compile(r"[a-f0-9]{40}")
REPO = re.compile(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+")
VERSION = re.compile(r"v[0-9]+\.[0-9]+\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9_.-]*)?")
SVG = "http://www.w3.org/2000/svg"


def encode(value):
    return json.dumps(value, indent=2, ensure_ascii=False) + "\n"


def positive(value):
    return isinstance(value, int) and not isinstance(value, bool) and value > 0


class APIError(Exception):
    def __init__(self, status, message):
        super().__init__(message)
        self.status = status


class SameHostRedirect(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        target = urlsplit(newurl)
        if target.scheme != "https" or target.netloc != urlsplit(req.full_url).netloc:
            raise ValueError("Refusing a cross-host or non-HTTPS API redirect")
        return super().redirect_request(req, fp, code, msg, headers, newurl)


def request_json(url, *, method="GET", token="", payload=None):
    target = urlsplit(url)
    if target.scheme != "https" or target.netloc not in {"api.github.com", "hub.docker.com"}:
        raise ValueError("Unsupported badge API host")
    if token and target.netloc != "api.github.com":
        raise ValueError("GitHub credentials may only be sent to api.github.com")
    headers = {"Accept": "application/json", "User-Agent": "certgen-local-badges"}
    if target.netloc == "api.github.com":
        headers["X-GitHub-Api-Version"] = "2022-11-28"
    if token:
        headers["Authorization"] = "Bearer " + token
    data = None if payload is None else encode(payload).encode()
    if data is not None:
        headers["Content-Type"] = "application/json"
    req = Request(url, data=data, headers=headers, method=method)
    try:
        with build_opener(SameHostRedirect()).open(req, timeout=20) as response:
            body = response.read((2 << 20) + 1)
    except HTTPError as error:
        raise APIError(error.code, f"{target.netloc}: HTTP {error.code}") from None
    except (URLError, OSError) as error:
        raise APIError(0, f"{target.netloc}: API unavailable ({type(error).__name__})") from None
    if len(body) > 2 << 20:
        raise ValueError("Oversized badge API response")
    value = json.loads(body)
    if not isinstance(value, dict):
        raise ValueError("Badge API response must be an object")
    return value


class GitHub:
    def __init__(self, repository, token):
        if not REPO.fullmatch(repository) or not token:
            raise ValueError("A valid GITHUB_REPOSITORY and GITHUB_TOKEN are required")
        self.repository = repository
        self.token = token

    def api(self, path, *, method="GET", payload=None):
        return request_json("https://api.github.com/repos/" + self.repository + "/" + path,
                            method=method, token=self.token, payload=payload)

    def current(self):
        sha = self.api("git/ref/heads/" + BRANCH)["object"]["sha"]
        if not SHA.fullmatch(sha):
            raise ValueError("Invalid master ref")
        tree = self.api("git/commits/" + sha)["tree"]["sha"]
        if not SHA.fullmatch(tree):
            raise ValueError("Invalid master tree")
        return sha, tree

    def file(self, path, ref):
        if not SHA.fullmatch(ref):
            raise ValueError("Content must be read from an immutable commit")
        try:
            data = self.api("contents/" + quote(path, safe="/") + "?" + urlencode({"ref": ref}))
        except APIError as error:
            if error.status == 404:
                return None
            raise
        if data.get("type") != "file" or data.get("encoding") != "base64":
            raise ValueError("Expected a regular base64-encoded repository file")
        if not isinstance(data.get("content"), str) or len(data["content"]) > 2 << 20:
            raise ValueError("Invalid repository file content")
        return base64.b64decode("".join(data["content"].split()), validate=True)

    def runs(self, event, *, status=None):
        params = {"event": event, "per_page": 100}
        if event == "push":
            params["branch"] = BRANCH
        if status:
            params["status"] = status
        data = self.api("actions/workflows/" + WORKFLOW + "/runs?" + urlencode(params))
        runs = data.get("workflow_runs")
        if not isinstance(runs, list):
            raise ValueError("Invalid workflow run list")
        return runs

    def jobs(self, run):
        result = []
        for page in range(1, 11):
            route = (f"actions/runs/{run['id']}/attempts/{run.get('run_attempt', 1)}/jobs?"
                     + urlencode({"per_page": 100, "page": page}))
            data = self.api(route)
            jobs = data.get("jobs")
            if not isinstance(jobs, list):
                raise ValueError("Invalid workflow job list")
            result.extend(jobs)
            if len(jobs) < 100:
                return result
        raise ValueError("Too many jobs in publication run")

    def commit(self, parent, base_tree, changes, message):
        if not changes or not changes.keys() <= WRITABLE:
            raise ValueError("Badge commits may only modify the generated allowlist")
        tree = self.api("git/trees", method="POST", payload={
            "base_tree": base_tree,
            "tree": [{"path": path, "mode": "100644", "type": "blob", "content": text}
                     for path, text in sorted(changes.items())]})
        commit = self.api("git/commits", method="POST", payload={
            "message": message, "tree": tree["sha"], "parents": [parent]})
        self.api("git/refs/heads/" + BRANCH, method="PATCH",
                 payload={"sha": commit["sha"], "force": False})


def default_state(repository=REPOSITORY):
    return {"schema": 1, "repository": repository,
            "ci": {"message": "Unknown", "color": "lightgrey", "run": None, "checked_at": None},
            "docker_pulls": {"value": None, "checked_at": None}, "publication": None, "tested_release": None}


def publication_valid(value):
    return (isinstance(value, dict) and positive(value.get("run_id"))
            and positive(value.get("run_number")) and positive(value.get("run_attempt"))
            and isinstance(value.get("head_sha"), str) and bool(SHA.fullmatch(value["head_sha"]))
            and isinstance(value.get("app_version"), str) and bool(VERSION.fullmatch(value["app_version"]))
            and isinstance(value.get("lego_version"), str) and bool(VERSION.fullmatch(value["lego_version"])))


def previous_state(data, repository):
    state = default_state(repository)
    if data is None:
        return state
    try:
        value = json.loads(data)
        if value.get("schema") != 1 or value.get("repository") != repository:
            return state
        if publication_valid(value.get("publication")):
            state["publication"] = value["publication"]
        if publication_valid(value.get("tested_release")):
            state["tested_release"] = value["tested_release"]
        pulls = value.get("docker_pulls", {})
        if isinstance(pulls, dict) and type(pulls.get("value")) is int and 0 <= pulls["value"] < 1 << 63:
            state["docker_pulls"] = pulls
        ci = value.get("ci", {})
        if isinstance(ci, dict) and ci.get("message") in {"Unknown", "Passing", "Failing", "Testing"}:
            state["ci"] = ci
    except (ValueError, TypeError, AttributeError):
        pass
    return state


def trusted_run(run, repository, event):
    return (isinstance(run, dict) and run.get("event") == event
            and positive(run.get("id")) and positive(run.get("run_number"))
            and positive(run.get("run_attempt", 1))
            and isinstance(run.get("head_sha"), str) and bool(SHA.fullmatch(run["head_sha"]))
            and isinstance(run.get("head_repository"), dict)
            and run["head_repository"].get("full_name") == repository
            and (event != "push" or run.get("head_branch") == BRANCH))


def run_order(run):
    return run["run_number"], run["id"], run.get("run_attempt", 1)


def ci_badge(run, checked_at):
    message, color = "Unknown", "lightgrey"
    if run and run.get("status") in {"requested", "queued", "pending", "waiting", "in_progress"}:
        message, color = "Testing", "orange"
    elif run and run.get("status") == "completed":
        if run.get("conclusion") == "success":
            message, color = "Passing", "40dc4a"
        elif run.get("conclusion") == "failure":
            message, color = "Failing", "red"
    brief = None if run is None else {key: run.get(key) for key in
                                     ("id", "run_number", "run_attempt", "head_sha", "status", "conclusion")}
    return {"message": message, "color": color, "run": brief, "checked_at": checked_at}


def release_versions(csv_data, legacy_data=None):
    if csv_data is not None:
        rows = list(csv.DictReader(io.StringIO(csv_data.decode("utf-8-sig"))))
        if not rows or len(rows) > 10000:
            raise ValueError("Missing or oversized released version history")
        row = rows[-1]
        app = row.get("App Version", "").strip()
        suffix = row.get("App Suffix", "").strip()
        lego = row.get("Lego Version", "").strip()
        lego_suffix = row.get("Lego Suffix", "").strip()
        app += ("-" + suffix) if suffix else ""
        lego += ("-" + lego_suffix) if lego_suffix else ""
    elif legacy_data is not None:
        # Compatibility only for old published commits; new releases use CSV.
        values = {}
        for line in legacy_data.decode().splitlines():
            match = re.fullmatch(r"(APP_VERSION|LEGO_VERSION)=([^\s]+)", line)
            if match:
                if match[1] in values:
                    raise ValueError("Duplicate legacy release version")
                values[match[1]] = match[2]
        app, lego = values.get("APP_VERSION", ""), values.get("LEGO_VERSION", "")
    else:
        raise ValueError("The published commit has no release identity")
    pair = tuple("v" + (value[1:] if value[:1] in {"v", "V"} else value) for value in (app, lego))
    if not all(VERSION.fullmatch(value) for value in pair):
        raise ValueError("Invalid published application or lego version")
    return pair


def docker_pulls():
    namespace, repository = DOCKER_REPOSITORY.split("/")
    modern = f"https://hub.docker.com/v2/namespaces/{namespace}/repositories/{repository}"
    try:
        data = request_json(modern)
    except APIError as error:
        if error.status not in {404, 410}:
            raise
        data = request_json("https://hub.docker.com/v2/repositories/" + DOCKER_REPOSITORY + "/")
    value = data.get("pull_count")
    if type(value) is not int or value < 0 or value >= 1 << 63:
        raise ValueError("Docker Hub did not return a valid pull count")
    return value


def collect(github, previous, checked_at, pull_reader=docker_pulls, warning=print):
    state = copy.deepcopy(previous)
    state["repository"] = github.repository
    try:
        runs = [run for run in github.runs("push") if trusted_run(run, github.repository, "push")]
        releases = [run for run in github.runs("release") if trusted_run(run, github.repository, "release")]
        runs.extend(releases)
        state["ci"] = ci_badge(max(runs, key=run_order) if runs else None, checked_at)
        if releases:
            run = max(releases, key=run_order)
            csv_data = github.file("ci/versions/versions.csv", run["head_sha"])
            legacy = github.file("ci/versions/versions.txt", run["head_sha"]) if csv_data is None else None
            app, lego = release_versions(csv_data, legacy)
            color = "orange" if run.get("status") != "completed" else ("red" if run.get("conclusion") == "failure" else "2684d5")
            state["tested_release"] = {"run_id":run["id"], "run_number":run["run_number"],
                "run_attempt":run.get("run_attempt",1), "head_sha":run["head_sha"],
                "app_version":app, "lego_version":lego, "color":color,
                "message":"Testing" if run.get("status") != "completed" else app, "checked_at":checked_at}
    except (APIError, ValueError, KeyError, TypeError) as error:
        state["ci"] = ci_badge(None, checked_at)
        warning("Master CI query failed; displaying Unknown (" + type(error).__name__ + ").")
    try:
        value = pull_reader()
        if type(value) is not int or not 0 <= value < 1 << 63:
            raise ValueError("Invalid pull count")
        state["docker_pulls"] = {"value": value, "checked_at": checked_at}
    except (APIError, ValueError, KeyError, TypeError) as error:
        warning("Docker Hub query failed; retaining the last pull snapshot (" + type(error).__name__ + ").")
    try:
        candidates = [run for run in github.runs("release", status="success")
                      if trusted_run(run, github.repository, "release")
                      and run.get("status") == "completed" and run.get("conclusion") == "success"]
        for run in sorted(candidates, key=run_order, reverse=True):
            old = state["publication"]
            if old and run_order(run) <= (old["run_number"], old["run_id"], old["run_attempt"]):
                break
            if not any(job.get("name") == PUBLISHER and job.get("conclusion") == "success"
                       and job.get("status") == "completed" for job in github.jobs(run)):
                continue
            csv_data = github.file("ci/versions/versions.csv", run["head_sha"])
            legacy = github.file("ci/versions/versions.txt", run["head_sha"]) if csv_data is None else None
            app, lego = release_versions(csv_data, legacy)
            state["publication"] = {"run_id": run["id"], "run_number": run["run_number"],
                                    "run_attempt": run.get("run_attempt", 1), "head_sha": run["head_sha"],
                                    "app_version": app, "lego_version": lego, "confirmed_at": checked_at}
            break
    except (APIError, ValueError, KeyError, TypeError, csv.Error) as error:
        warning("Publication query failed; retaining the last confirmed pair (" + type(error).__name__ + ").")
    return state


def icon_path(root, name):
    doc = ET.fromstring((root / "ci/badge-icons" / (name + ".svg")).read_bytes())
    if doc.tag != "{" + SVG + "}svg" or doc.attrib.get("viewBox") != "0 0 24 24":
        raise ValueError("Unsupported local icon")
    paths = [node.attrib["d"] for node in doc if node.tag == "{" + SVG + "}path"]
    if not paths:
        raise ValueError("Local icon has no paths")
    return "".join('<path fill="#fff" d="' + escape(path, quote=True) + '"/>' for path in paths)


def width(text):
    # Conservative 11px sans-serif widths; numeric '1' has a full digit advance.
    return max(1, sum(7 if ch.isdigit() else 10 if ch in "MWmw@%" else 4 if ch in "ilIj.,:| "
                      else 8 if ch.isupper() else 5 if ch in "/-()" else 7 for ch in text))


def shield(label, message, color, *, icon="", detail=""):
    colors = {"lightgrey": "#9f9f9f", "orange": "#fe7d37", "red": "#e05d44",
              "green": "#4c1", "40dc4a": "#40dc4a", "2496ED": "#2496ed", "2684d5": "#2684d5", "informational": "#007ec6"}
    if color not in colors or len(label) > 100 or len(message) > 160:
        raise ValueError("Invalid badge label, message or color")
    left, right = width(label) + 10 + (19 if icon else 0), width(message) + 10
    total = left + right
    title = label + ": " + message + (" · " + detail if detail else "")
    label_x = (left + (19 if icon else 0)) / 2
    message_x = left + right / 2
    mark = '<g transform="translate(5,3) scale(0.5833333333)">' + icon + '</g>' if icon else ""
    return (f'<svg xmlns="{SVG}" width="{total}" height="20" viewBox="0 0 {total} 20" '
            f'role="img" aria-label="{escape(title, quote=True)}">\n<title>{escape(title)}</title>\n'
            '<linearGradient id="s" x2="0" y2="100%"><stop offset="0" stop-color="#bbb" stop-opacity=".1"/>'
            '<stop offset="1" stop-opacity=".1"/></linearGradient>\n'
            f'<clipPath id="r"><rect width="{total}" height="20" rx="3" fill="#fff"/></clipPath>\n'
            f'<g clip-path="url(#r)"><path fill="#555" d="M0 0h{left}v20H0z"/>'
            f'<path fill="{colors[color]}" d="M{left} 0h{right}v20H{left}z"/>'
            f'<rect width="{total}" height="20" fill="url(#s)"/></g>\n' + mark + '\n'
            '<g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">'
            f'<text x="{label_x:g}" y="15" fill="#010101" fill-opacity=".3">{escape(label)}</text>'
            f'<text x="{label_x:g}" y="14">{escape(label)}</text>'
            f'<text x="{message_x:g}" y="15" fill="#010101" fill-opacity=".3">{escape(message)}</text>'
            f'<text x="{message_x:g}" y="14">{escape(message)}</text></g>\n</svg>\n')








def outputs(root, state):
    pub = state.get("publication")
    tested = state.get("tested_release")
    if not publication_valid(tested):
        tested = None
    pulls = state.get("docker_pulls", {})
    ci = state["ci"]
    specs = {
        "docker-hub": ("Docker Hub", DOCKER_REPOSITORY, "2496ED", "docker", ""),
        "docker-pulls": ("Docker Pulls", str(pulls["value"]) if pulls.get("value") is not None else "Unknown",
                         "2496ED" if pulls.get("value") is not None else "lightgrey", "docker",
                         "Docker Hub counter observed " + (pulls.get("checked_at") or "not yet fetched")),
        "docker-image": ("Docker Image", tested.get("message", tested["app_version"]) if tested else (pub["app_version"] if pub else "Unknown"),
                         tested.get("color", "2684d5") if tested else ("2496ED" if pub else "lightgrey"), "docker", "Current or most recently tested release; the release list link is fixed"),
        "ci": ("CI/CD", ci["message"], ci["color"], "github",
               "Newest master push or release; checked " + (ci.get("checked_at") or "not yet fetched")),
        "gitea": ("Gitea", "git.ljgonzalez.cl", "green", "gitea", "Repository mirror"),
        "lego": ("lego version", pub["lego_version"] if pub else "Unknown",
                 "informational" if pub else "lightgrey", "go", "Dependency from the same confirmed image publication"),
        "license": ("License", "MIT", "green", "", "Original project code; third parties retain their licenses"),
    }
    result = {BADGE_DIR + name + ".svg": shield(label, message, color,
              icon=icon_path(root, icon) if icon else "", detail=detail)
              for name, (label, message, color, icon, detail) in specs.items()}
    result[STATE] = encode(state)
    inventory = {"schema": 1, "assets": []}
    entries = []
    for path, data in sorted(result.items()):
        svg = path.endswith(".svg")
        embedded_icon = svg and path != BADGE_DIR + "license.svg"
        entry = {"path": path, "sha256": hashlib.sha256(data.encode()).hexdigest(), "runtime": False,
                 "license": "MIT AND CC0-1.0" if embedded_icon else "MIT",
                 "artwork_rights": ("Original badge layout MIT; embedded Simple Icons CC0-1.0; trademarks retain their owners' rights."
                                    if embedded_icon else "First-party generated MIT badge/metadata; no credentials included.")}
        if embedded_icon:
            entry["third_party_notice"] = "licenses/badges/SimpleIcons-LICENSE.md"
        entries.append(entry)
    inventory["assets"] = sorted(entries, key=lambda entry: entry["path"])
    result[MANIFEST] = encode(inventory)
    return result


def local_render(root):
    state_path = root / STATE
    state = previous_state(state_path.read_bytes() if state_path.exists() else None, REPOSITORY)
    for path, value in outputs(root, state).items():
        target = root / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(value)


def check(root):
    state = json.loads((root / STATE).read_text())
    expected = outputs(root, state)
    for path, value in expected.items():
        if (root / path).read_text() != value:
            raise ValueError("Badge snapshot is not reproducible: " + path)
    readme = (root / "README.md").read_text()
    for name in NAMES:
        if "](" + BADGE_DIR + name + ".svg)" not in readme:
            raise ValueError("README must link the fixed local badge path: " + name)
        doc = ET.fromstring(expected[BADGE_DIR + name + ".svg"])
        for node in doc.iter():
            if node.tag in {"{" + SVG + "}script", "{" + SVG + "}image", "{" + SVG + "}style"}:
                raise ValueError("Badge contains non-local display content")
            for key, value in node.attrib.items():
                if key.lower().startswith("on") or "href" in key.lower() or "http" in value.lower():
                    raise ValueError("Badge contains an external resource or active content")
    print("PASS: seven self-contained local SVG badges, snapshot metadata and README links.")


def publish(github, root, *, clock=None, pull_reader=docker_pulls):
    if clock is None:
        clock = lambda: datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")
    for attempt in range(4):
        parent, tree = github.current()
        # A concurrent source change needs a new trusted checkout, not stale rendering.
        inputs = ["ci/badges.py", "licenses/badges/sources.json", "licenses/badges/SimpleIcons-LICENSE.md",
                  *("ci/badge-icons/" + name + ".svg" for name in ("docker", "github", "gitea", "go"))]
        for path in inputs:
            if github.file(path, parent) != (root / path).read_bytes():
                raise ValueError("Badge sources changed on master; rerun with its current checkout")
        previous = previous_state(github.file(STATE, parent), github.repository)
        state = collect(github, previous, clock(), pull_reader=pull_reader)
        rendered = outputs(root, state)
        changes = {path: text for path, text in rendered.items()
                   if github.file(path, parent) != text.encode()}
        if not changes:
            print("Badge snapshots are already current.")
            return False
        try:
            github.commit(parent, tree, changes, "docs: refresh local badges (master " + state["ci"]["message"] + ")")
            print("Updated local badges on master; copy or mirror this branch to preserve them in Gitea.")
            return True
        except APIError as error:
            if error.status not in {409, 422} or attempt == 3:
                raise
            print("Master changed or rejected the write; recomputing without a force push.")
    return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    mode = parser.add_mutually_exclusive_group(required=True)
    mode.add_argument("--render", action="store_true", help="Render local committed state without network access")
    mode.add_argument("--check", action="store_true", help="Check local badge reproducibility and self-containment")
    mode.add_argument("--github", action="store_true", help="Run the authenticated GitHub Actions updater")
    args = parser.parse_args()
    try:
        if args.render:
            local_render(ROOT)
        elif args.check:
            check(ROOT)
        else:
            if os.environ.get("GITHUB_ACTIONS") != "true":
                raise ValueError("Live updates are restricted to the configured GitHub Actions workflow")
            github = GitHub(os.environ.get("GITHUB_REPOSITORY", ""), os.environ.get("GITHUB_TOKEN", ""))
            publish(github, ROOT)
    except (APIError, OSError, ValueError, KeyError, TypeError, ET.ParseError) as error:
        print("Badge update failed: " + str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
