#!/usr/bin/env python3
"""Static syntax and project-contract checks for all non-Go/Python/shell formats.

Uses PyYAML for YAML, jq's real compiler for jq, ShellCheck for workflow shell,
and standard-library parsers for JSON/TOML/XML/CSV and Dockerfile contracts.
It does not claim to replace a real Docker build or all of actionlint/hadolint.
"""

import collections
import csv
import io
import json
import re
import shlex
import struct
import subprocess
import sys
import tomllib
from urllib.parse import unquote
import xml.etree.ElementTree as ET
import zlib

import yaml
from source_files import ROOT, files, kind


class Loader(yaml.SafeLoader):
    yaml_implicit_resolvers = {key: [pair for pair in pairs if pair[0] != "tag:yaml.org,2002:bool"]
                              for key, pairs in yaml.SafeLoader.yaml_implicit_resolvers.items()}

    def construct_mapping(self, node, deep=False):
        seen = set()
        for key, _ in node.value:
            if key.tag == "tag:yaml.org,2002:merge":
                continue
            value = self.construct_object(key, deep=True)
            if value in seen:
                raise ValueError("Duplicate YAML key: " + str(value))
            seen.add(value)
        self.flatten_mapping(node)
        return super().construct_mapping(node, deep=deep)


Loader.add_implicit_resolver("tag:yaml.org,2002:bool", re.compile(r"^(?:true|false|True|False|TRUE|FALSE)$"), list("tTfF"))


def unique(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON key: " + key)
        result[key] = value
    return result


def json_data(text):
    def invalid(value):
        raise ValueError("Non-finite JSON number: " + value)
    return json.loads(text, object_pairs_hook=unique, parse_constant=invalid)


def expression(text):
    for match in re.finditer(r"\$\{\{(.*?)\}\}", text, re.S):
        body = match[1]
        stripped = re.sub(r"'(?:[^']|'')*'", "''", body)
        stack = []
        for char in stripped:
            if char in "([":stack.append(char)
            elif char in ")]":
                if not stack or stack.pop() != ("]" == char and "[" or "("):
                    raise ValueError("Unbalanced GitHub expression")
        if stack or not body.strip():raise ValueError("Invalid GitHub expression")
    if text.count("${{") != len(re.findall(r"\$\{\{.*?\}\}", text, re.S)):
        raise ValueError("Unclosed GitHub expression")


def workflow(data, path, root, shell_check=True):
    if not isinstance(data, dict) or not isinstance(data.get("jobs"), dict) or "on" not in data:
        raise ValueError("Workflow requires on and jobs mappings")
    jobs = data["jobs"]
    visiting, visited = set(), set()
    def visit(name):
        if name in visiting:raise ValueError("Cyclic workflow needs")
        if name in visited:return
        if name not in jobs:raise ValueError("Unknown needs job: " + name)
        visiting.add(name)
        needs = jobs[name].get("needs", [])
        if isinstance(needs, str):needs = [needs]
        if not isinstance(needs, list):raise ValueError("needs must be a string or list")
        for dependency in needs:visit(dependency)
        visiting.remove(name);visited.add(name)
    for name, job in jobs.items():
        if not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_-]*", name) or not isinstance(job, dict):
            raise ValueError("Invalid job identifier or definition")
        visit(name)
        if "uses" in job:
            if "steps" in job or "runs-on" in job:raise ValueError("Reusable jobs cannot have steps/runs-on")
            if job["uses"].startswith("./") and not (root / job["uses"]).is_file():
                raise ValueError("Missing reusable workflow")
            continue
        if not isinstance(job.get("runs-on"), (str, list, dict)) or not isinstance(job.get("steps"), list):
            raise ValueError("Executable jobs require runs-on and steps")
        ids = set()
        for step in job["steps"]:
            if not isinstance(step, dict) or ("run" in step) == ("uses" in step):
                raise ValueError("Each workflow step needs exactly one run or uses")
            if "id" in step:
                if step["id"] in ids:raise ValueError("Duplicate workflow step id")
                ids.add(step["id"])
            if "run" in step:
                if not isinstance(step["run"], str):raise ValueError("run must be text")
                script = re.sub(r"\$\{\{.*?\}\}", "github_expression", step["run"], flags=re.S)
                shell = step.get("shell", job.get("defaults", {}).get("run", {}).get("shell", "bash"))
                if shell not in {"bash", "sh"}:raise ValueError("Register a validator for workflow shell: " + shell)
                subprocess.run([shell, "-n"], input=script, text=True, check=True, capture_output=True)
                if shell_check:
                    result = subprocess.run(["shellcheck", "--shell=" + shell, "--external-sources", "--severity=style", "-"],
                                            input=script, text=True, capture_output=True, cwd=root)
                    if result.returncode:raise ValueError("Workflow shell: " + result.stdout + result.stderr)
    def strings(value):
        if isinstance(value, str):expression(value)
        elif isinstance(value, dict):
            for item in value.values():strings(item)
        elif isinstance(value, list):
            for item in value:strings(item)
    strings(data)


def dockerfile(text, context):
    valid = {"ADD", "ARG", "CMD", "COPY", "ENTRYPOINT", "ENV", "EXPOSE", "FROM", "HEALTHCHECK", "LABEL",
             "MAINTAINER", "ONBUILD", "RUN", "SHELL", "STOPSIGNAL", "USER", "VOLUME", "WORKDIR"}
    instructions, pending = [], ""
    for line in text.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):continue
        pending += line.strip()
        if pending.endswith("\\"):
            pending = pending[:-1] + " ";continue
        match = re.fullmatch(r"([A-Za-z]+)\s+(.+)", pending)
        if not match or match[1].upper() not in valid:raise ValueError("Invalid Dockerfile instruction: " + pending)
        instructions.append((match[1].upper(), match[2]));pending = ""
    if pending:raise ValueError("Unterminated Dockerfile continuation")
    stages = []
    for op, arg in instructions:
        if op == "FROM":
            words = shlex.split(arg)
            if words and words[0].startswith("--platform="):words = words[1:]
            if len(words) not in {1, 3} or (len(words) == 3 and words[1].upper() != "AS"):
                raise ValueError("Invalid FROM instruction")
            name = words[2] if len(words) == 3 else str(len(stages))
            if name in stages:raise ValueError("Duplicate Dockerfile stage")
            stages.append(name);continue
        if not stages and op != "ARG":raise ValueError("Only ARG may precede FROM")
        if op == "ARG" and not re.fullmatch(r"[A-Za-z_][A-Za-z0-9_]*(?:=.*)?", arg):raise ValueError("Invalid ARG")
        rest = re.sub(r"^(?:--[^\s]+\s+)+", "", arg)
        if op in {"RUN", "CMD", "ENTRYPOINT", "SHELL", "VOLUME", "COPY", "ADD"} and rest.startswith("["):
            array = json_data(rest)
            if not isinstance(array, list) or not array or not all(isinstance(item, str) for item in array):
                raise ValueError("Dockerfile JSON form must be a nonempty string array")
        if op == "RUN":
            script, shell = None, "sh"
            if rest.startswith("["):
                if len(array) >= 3 and array[0] in {"sh", "bash", "/bin/sh", "/bin/bash"} and array[1] == "-c":
                    shell, script = array[0].rsplit("/", 1)[-1], array[2]
            else:
                script = rest
            if script is not None:
                subprocess.run([shell, "-n"], input=script, text=True, check=True, capture_output=True)
                checked = subprocess.run(["shellcheck", "--shell=" + shell, "--severity=style", "-"],
                                         input=script, text=True, capture_output=True)
                if checked.returncode:raise ValueError("Dockerfile shell: " + checked.stdout + checked.stderr)
        if op in {"COPY", "ADD"}:
            source_stage = re.search(r"--from=([^\s]+)", arg)
            if source_stage and source_stage[1] not in stages[:-1]:
                raise ValueError("Unknown COPY source stage: " + source_stage[1])
            parts = json_data(rest) if rest.startswith("[") else shlex.split(rest)
            if len(parts) < 2:raise ValueError("COPY/ADD requires source and destination")
            if not source_stage:
                for source in parts[:-1]:
                    if "$" in source or "://" in source:continue
                    if not list(context.glob(source.rstrip("/"))):raise ValueError("Missing Docker context source: " + source)
        if op in {"ENV", "LABEL"}:
            if not all("=" in item for item in shlex.split(arg)):raise ValueError("Use explicit key=value in " + op)
    if not stages:raise ValueError("Dockerfile has no FROM")


def markdown(text, path):
    for target in re.findall(r"!?\[[^\]\n]*\]\(([^)\s]+)\)", text):
        if ":" in target or target.startswith("#") or "<" in target or "*" in target:continue
        target = unquote(target.split("#")[0].split("?")[0])
        if target and not (path.parent / target).exists():raise ValueError("Missing Markdown target: " + target)


def png(data):
    if not data.startswith(b"\x89PNG\r\n\x1a\n"):raise ValueError("Invalid PNG signature")
    offset, chunks = 8, []
    while offset < len(data):
        if offset + 12 > len(data):raise ValueError("Truncated PNG chunk")
        size = struct.unpack(">I", data[offset:offset+4])[0]
        end = offset + 12 + size
        if end > len(data):raise ValueError("Truncated PNG payload")
        body = data[offset+4:end-4]
        if zlib.crc32(body) != struct.unpack(">I", data[end-4:end])[0]:raise ValueError("PNG checksum mismatch")
        chunks.append(body[:4]);offset = end
    if not chunks or chunks[0] != b"IHDR" or chunks[-1] != b"IEND":raise ValueError("Missing PNG header/end")


def validate(path, root=ROOT, shell_check=True):
    category = kind(path)
    if category in {"go", "python", "shell"}:return category
    data = path.read_bytes()
    if category == "png":png(data);return category
    text = data.decode("utf-8-sig")
    if "\0" in text:raise ValueError("NUL byte in text file")
    if category == "json":json_data(text)
    elif category == "toml":tomllib.loads(text)
    elif category == "svg":
        if ET.fromstring(text).tag != "{http://www.w3.org/2000/svg}svg":raise ValueError("Expected SVG root")
    elif category == "markdown":markdown(text, path)
    elif category == "yaml":
        value = yaml.load(text, Loader=Loader)
        if ".github/workflows" in str(path.relative_to(root)):workflow(value, path, root, shell_check)
        elif path.name == "compose.yml":
            if not isinstance(value, dict) or not isinstance(value.get("services"), dict):raise ValueError("Compose requires services")
        elif not isinstance(value, dict):raise ValueError("Domain YAML requires a mapping")
    elif category == "dockerfile":dockerfile(text, path.parent)
    elif category == "jq":
        result = subprocess.run(["jq", "-n", "try (\n" + text + "\n) catch null"], capture_output=True, text=True)
        if result.returncode:raise ValueError("jq compiler: " + result.stderr)
    elif category == "csv":
        rows = list(csv.reader(io.StringIO(text), strict=True))
        if not rows or len(set(rows[0])) != len(rows[0]) or any(len(row) != len(rows[0]) for row in rows):
            raise ValueError("Invalid CSV header/row width")
        if path.name == "versions.csv":
            names = [row[8] + ("-" + row[9] if row[9] else "") for row in rows[1:]]
            if len(names) != len(set(names)):raise ValueError("Duplicate release identity")
    elif category == "env":
        names = set()
        for line in text.splitlines():
            if not line.strip() or line.lstrip().startswith("#"):continue
            match = re.fullmatch(r"([A-Za-z_][A-Za-z0-9_]*)=(.*)", line)
            if not match or match[1] in names:raise ValueError("Invalid or duplicate template environment assignment")
            names.add(match[1])
    return category


def main():
    counts = collections.Counter()
    errors = []
    for path in files():
        try:counts[validate(path)] += 1
        except (ValueError, OSError, yaml.YAMLError, ET.ParseError, subprocess.SubprocessError) as error:
            errors.append(str(path.relative_to(ROOT)) + ": " + str(error))
    if errors:
        print("\n".join(errors), file=sys.stderr);return 1
    print("PASS: first-party format inventory " + json.dumps(dict(sorted(counts.items()))))
    print("Go/Python/shell are checked by their dedicated analyzers; workflows include shell parsing/ShellCheck.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
