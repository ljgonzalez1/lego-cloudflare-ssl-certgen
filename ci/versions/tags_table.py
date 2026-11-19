#!/usr/bin/env python3
"""Update the Docker tag tables in the root Markdown files from versions.csv.

Run:
    python3 ci/versions/tags_table.py

Input:  versions.csv (UTF-8; comma-separated; 11 columns, with/without header)
Output: the marked blocks in ../README.md (complete) and ../DOCKERHUB.md (short)

Append each new release to the END of versions.csv. The last publisher of a
rolling tag owns it. Alpha, alfa and beta releases keep their suffix on every
tag and leave ``latest`` on the last eligible release. Both tables are displayed
newest-first. The short table keeps the row that owns ``latest`` visible and contains
at most seven releases. The COMPLETE table shows every currently owned tag,
without any display limit; the SHORT table shows at most four tags per row
(primary plus up to three more, counting ``latest``). The fully qualified
OS+lego+app tag is always the last visible alias.
No tag overrides are used.

Every release generates eight or nine *unique* Docker tags by this convention:
    1. OS(full)-lego(full)-app(full)
    2. OS(short)-lego(short)-app(full)
    3. OS(generic)-lego(short)-app(minor)
    4. lego(full)-app(full)
    5. lego(short)-app(full)
    6. app(full)
    7. app(minor)
    8. app(major)
    9. latest (omitted for alpha, alfa and beta)

For Alpine, OS(short) and OS(generic) both preserve major.minor, following
Alpine's established naming; for Debian, OS(generic) is ``debian[-suffix]``.
A component's nonempty suffix is appended with a hyphen. For Alpine,
``scratch`` is a display label for the runtime filesystem only.

Only Python's standard library is used (Python 3.10+).
"""

from __future__ import annotations

import argparse
import csv
import os
import re
import sys
import tempfile
import unicodedata
from dataclasses import dataclass
from pathlib import Path
from urllib.parse import urlsplit

BASE_DIR = Path(__file__).resolve().parent
REPO_DIR = BASE_DIR.parent.parent
CSV_PATH = BASE_DIR / "versions.csv"
TABLE_BEGIN = "<!-- BEGIN DOCKER TAG TABLE -->"
TABLE_END = "<!-- END DOCKER TAG TABLE -->"
DOCKERHUB_MAX_BYTES = 25_000
REPOSITORY_URL = "https://github.com/ljgonzalez1/lego-cloudflare-ssl-certgen"
DOCKER_IMAGE = "ljgonzalez/lego-cloudflare-certgen"
BUILDER = "cf-lego-certgen-multiarch"
PLATFORMS = "linux/arm/v6,linux/arm/v7,linux/arm64,linux/386,linux/amd64"
SHORT_MAX_RELEASES = 7
SHORT_MAX_DISPLAY_TAGS = 4  # Primary version + up to 3 additional tags

COLUMNS = (
    "Docker OS", "Docker OS Version", "Docker OS Suffix", "Docker OS URL",
    "Lego Name", "Lego Version", "Lego Suffix", "Lego URL",
    "App Version", "App Suffix", "App URL",
)
HEADER_ALIASES = {
    "Docker OS": ("OS imagen de docker", "OS", "Docker OS Image"),
    "Docker OS Version": ("Versión de la imagen de docker", "Docker OS Image Version"),
    "Docker OS Suffix": ("Apellido del os sólo en caso de tenerlo", "OS Surname"),
    "Docker OS URL": ("URL OS", "OS URL"),
    "Lego Name": ("LEGo", "Lego"),
    "Lego Version": ("Versión de lego",),
    "Lego Suffix": ("Apellido de lego", "Lego Surname"),
    "Lego URL": ("URL Lego",),
    "App Version": ("Versión", "Version", "Project Version"),
    "App Suffix": ("Apellido versión", "Version Suffix"),
    "App URL": ("URL versión", "Version URL", "Release URL"),
}

VALID_TAG = re.compile(r"[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}\Z")
APP_VERSION = re.compile(
    r"(?P<prefix>[vV]?)(?P<major>[0-9]+)\.(?P<minor>[0-9]+)\.(?P<patch>[0-9]+)"
    r"(?P<suffix>-[A-Za-z0-9_][A-Za-z0-9_.-]*)?\Z"
)
PRERELEASE = re.compile(r"(?:^|[._-])(?:alpha|alfa|beta)[0-9]*(?:$|[._-])", re.IGNORECASE)


def normalize_header(value: str) -> str:
    return re.sub(r"[^a-z0-9]+", "", unicodedata.normalize("NFKD", value).encode("ascii", "ignore").decode().casefold())


HEADER_MAP = {
    normalize_header(alias): column
    for column in COLUMNS
    for alias in (column, *HEADER_ALIASES.get(column, ()))
}


def slug(value: str) -> str:
    """Normalize component names/suffixes to Docker-tag-safe lowercase text."""
    value = unicodedata.normalize("NFKD", value.strip()).encode("ascii", "ignore").decode().casefold()
    value = re.sub(r"\s+", "-", value)
    return re.sub(r"[^a-z0-9_.-]+", "-", value).strip("._-")


def with_suffix(text: str, suffix: str) -> str:
    return f"{text}-{suffix}" if text and suffix else text or suffix


def markdown_link(label: str, url: str) -> str:
    label = label.replace("|", "\\|").replace("[", "\\[").replace("]", "\\]")
    return f"[{label}]({url})" if url else label


def tag_link(tag: str, url: str) -> str:
    return markdown_link(f"`{tag}`", url)


def validate_url(url: str, line: int, column: str) -> None:
    if not url:
        return
    parts = urlsplit(url)
    if parts.scheme not in ("https", "http") or not parts.netloc or any(char.isspace() for char in url):
        raise ValueError(f"CSV line {line}: invalid {column}: {url!r}")


@dataclass(frozen=True)
class Release:
    fields: dict[str, str]
    line: int
    tags: tuple[str, ...]
    os_short: str
    os_full: str
    lego_short: str
    lego_full: str
    app_full: str

    def field(self, key: str) -> str:
        return self.fields[key]


def _short_version(version: str, *, os_name: str = "") -> str:
    pieces = version.split(".")
    if os_name == "debian":
        return pieces[0] if len(pieces) > 1 else version
    return ".".join(pieces[:-1]) if len(pieces) > 2 else version


def make_release(fields: dict[str, str], line: int) -> Release:
    os_name = slug(fields["Docker OS"])
    os_suffix = slug(fields["Docker OS Suffix"])
    os_version = fields["Docker OS Version"]
    lego_name = slug(fields["Lego Name"])
    lego_suffix = slug(fields["Lego Suffix"])
    lego_version = re.sub(r"^[vV]", "", fields["Lego Version"], count=1)
    app_version = fields["App Version"]
    app_suffix = slug(fields["App Suffix"])

    for key, raw, normalized in (
        ("Docker OS", fields["Docker OS"], os_name),
        ("Docker OS Suffix", fields["Docker OS Suffix"], os_suffix),
        ("Lego Name", fields["Lego Name"], lego_name),
        ("Lego Suffix", fields["Lego Suffix"], lego_suffix),
        ("App Suffix", fields["App Suffix"], app_suffix),
    ):
        if raw and not normalized:
            raise ValueError(f"CSV line {line}: {key} is not usable as a Docker tag component.")

    if not os_name or not lego_name:
        raise ValueError(f"CSV line {line}: Docker OS and Lego Name are required.")

    match = APP_VERSION.fullmatch(app_version)
    if not match:
        raise ValueError(
            f"CSV line {line}: App Version must have major.minor.patch "
            f"(optionally prefixed with v and/or followed by -prerelease); got {app_version!r}. "
            "Three numeric components are necessary to generate distinct tags."
        )

    for key, value in (("Docker OS Version", os_version), ("Lego Version", lego_version)):
        if value and not re.fullmatch(r"[A-Za-z0-9_][A-Za-z0-9_.-]*", value):
            raise ValueError(f"CSV line {line}: invalid {key}: {value!r}")

    for key in ("Docker OS URL", "Lego URL", "App URL"):
        validate_url(fields[key], line, key)

    # Preserve the conventional leading 'v' exactly as written in App Version.
    prefix = match.group("prefix")
    minor_base = f"{prefix}{match.group('major')}.{match.group('minor')}"
    major_base = f"{prefix}{match.group('major')}"
    embedded_suffix = (match.group("suffix") or "").removeprefix("-")
    combined_suffix = "-".join(part for part in (embedded_suffix, app_suffix) if part)
    app_full = with_suffix(app_version, app_suffix)
    app_minor = with_suffix(minor_base, combined_suffix)
    app_major = with_suffix(major_base, combined_suffix)

    tag_os_suffix = "" if os_name == "alpine" and os_suffix == "scratch" else os_suffix

    def os_component(version: str) -> str:
        if os_name == "debian":
            return with_suffix(with_suffix(os_name, version), tag_os_suffix)
        return with_suffix(os_name + version, tag_os_suffix)

    os_full = os_component(os_version)
    os_short = os_component(_short_version(os_version, os_name=os_name))
    os_generic = os_component("") if os_name == "debian" else os_short

    lego_full = with_suffix(lego_name + lego_version, lego_suffix)
    lego_short = with_suffix(lego_name + _short_version(lego_version), lego_suffix)

    tags = (
        f"{os_full}-{lego_full}-{app_full}",
        f"{os_short}-{lego_short}-{app_full}",
        f"{os_generic}-{lego_short}-{app_minor}",
        f"{lego_full}-{app_full}",
        f"{lego_short}-{app_full}",
        app_full,
        app_minor,
        app_major,
    )
    if not PRERELEASE.search(combined_suffix):
        tags += ("latest",)

    for tag in tags:
        if not VALID_TAG.fullmatch(tag):
            raise ValueError(f"CSV line {line}: invalid Docker tag {tag!r} (allowed: [A-Za-z0-9_.-], maximum 128 characters).")
    if len(set(tags)) != len(tags):
        repeats = sorted({tag for tag in tags if tags.count(tag) > 1})
        raise ValueError(
            f"CSV line {line}: cannot generate unique tags from these versions; "
            f"collisions: {', '.join(repeats)}. Supply more specific OS/lego versions."
        )
    return Release(fields, line, tags, os_short, os_full, lego_short, lego_full, app_full)


def read_releases(path: Path) -> list[Release]:
    with path.open("r", encoding="utf-8-sig", newline="") as f:
        reader = csv.reader(f, delimiter=",", strict=True)
        raw = [(reader.line_num, [value.strip() for value in row]) for row in reader if any(value.strip() for value in row)]

    if not raw:
        raise ValueError("versions.csv is empty.")
    start_line, first = raw[0]
    found = [HEADER_MAP.get(normalize_header(value)) for value in first]
    header_count = sum(column is not None for column in found)
    has_header = header_count >= 5 or ("Docker OS" in found and "App Version" in found)

    if has_header:
        indices: dict[str, int] = {}
        for i, name in enumerate(found):
            if name:
                if name in indices:
                    raise ValueError(f"Duplicate CSV header: {name}.")
                indices[name] = i
        missing = [name for name in COLUMNS if name not in indices]
        if missing:
            raise ValueError(f"Missing required CSV headers: {', '.join(missing)}")
        if len(first) != 11:
            raise ValueError("CSV header must contain exactly 11 columns; remove legacy Runtime Display / Tag Overrides columns.")
        raw = raw[1:]
    else:
        indices = {name: i for i, name in enumerate(COLUMNS)}
        if len(first) != 11:
            raise ValueError(
                f"CSV line {start_line}: expected 11 fields separated by ',', "
                f"received {len(first)}. Check the delimiter and remove legacy columns."
            )

    releases: list[Release] = []
    seen: dict[str, int] = {}
    for line, cells in raw:
        if len(cells) != 11:
            raise ValueError(f"CSV line {line}: expected 11 fields; received {len(cells)}.")
        values = {column: cells[index] for column, index in indices.items()}
        record = make_release(values, line)
        key = record.app_full.casefold()
        if key in seen:
            raise ValueError(f"Duplicate App Version {record.app_full!r} on CSV lines {seen[key]} and {line}.")
        seen[key] = line
        releases.append(record)
    if not releases:
        raise ValueError("versions.csv contains a header but no release rows.")
    return releases


def runtime_cell(record: Release) -> str:
    components = (record.field("Docker OS"), record.field("Docker OS Version"), record.field("Docker OS Suffix"))
    if components[0].casefold() == "alpine" and components[2].casefold() == "scratch":
        components = (*components[:2], "minimal on `scratch`")
    name = " ".join(part for part in components if part)
    return markdown_link(name, record.field("Docker OS URL"))


def lego_cell(record: Release) -> str:
    version = with_suffix(record.field("Lego Version"), record.field("Lego Suffix"))
    return markdown_link(f"`{version or record.field('Lego Name')}`", record.field("Lego URL"))


def ownership(releases: list[Release]) -> dict[str, int]:
    """Last CSV occurrence wins every shared tag (Docker Hub overwrite behavior)."""
    return {tag: i for i, record in enumerate(releases) for tag in record.tags}


def selected_aliases(record: Release, index: int, owners: dict[str, int], limit: int | None) -> list[str]:
    """List all currently owned aliases, or cap them for the short table.

    A None limit means ALL owned aliases, rather than truncating the output.
    Older releases omit aliases that newer releases overwrote on Docker Hub.
    The fully qualified OS+lego+app tag is always appended at the end.
    """
    if limit is not None and limit <= 0:
        return []
    full_tag = record.tags[0]
    # The last candidate is always the most specific full OS+lego+app tag.
    # Position indices: 7 major; 6 minor; 4 short lego; 3 full lego;
    # 1 short OS+lego; 2 rolling OS+lego+minor; 0 full OS+lego.
    priority = (7, 6, 4, 3, 1, 2)
    chosen: list[str] = []
    for offset in priority:
        candidate = record.tags[offset]
        if candidate in (record.app_full, "latest", full_tag):
            continue
        if owners.get(candidate) != index or candidate in chosen:
            continue
        if limit is not None and len(chosen) >= limit - 1:
            break
        chosen.append(candidate)
    if owners.get(full_tag) == index:
        chosen.append(full_tag)
    return chosen


def complete_table(
    releases: list[Release],
    *,
    max_releases: int | None = None,
    max_display_tags: int | None = None,
) -> str:
    """Generate the original four-column table, uncapped unless requested.

    Alias owners are calculated from the ENTIRE CSV, including hidden rows.
    """
    owners = ownership(releases)
    lines = [
        "| tag | runtime | lego | other aliases |",
        "|---|---|---|---|",
    ]
    first_index = 0 if max_releases is None else max(0, len(releases) - max_releases)
    visible = list(range(first_index, len(releases)))
    latest_owner = owners.get("latest")
    if visible and latest_owner is not None and latest_owner < first_index:
        visible[0] = latest_owner
    for index in reversed(visible):
        record = releases[index]
        url = record.field("App URL")
        principal = [tag_link(record.app_full, url)]
        if owners.get("latest") == index:
            principal.insert(0, tag_link("latest", url or REPOSITORY_URL))
        available = None if max_display_tags is None else max_display_tags - len(principal)
        aliases = [tag_link(tag, url) for tag in selected_aliases(record, index, owners, available)]
        lines.append(
            f"| {', '.join(principal)} | {runtime_cell(record)} | {lego_cell(record)} | "
            f"{', '.join(aliases) if aliases else '-'} |"
        )
    return "\n".join(lines) + "\n"


def short_table(releases: list[Release]) -> str:
    """At most seven rows and four tags each, including the owner of latest."""
    return complete_table(
        releases,
        max_releases=SHORT_MAX_RELEASES,
        max_display_tags=SHORT_MAX_DISPLAY_TAGS,
    )


def render_document(path: Path, table: str) -> tuple[str, str]:
    """Read both marker lines and replace only the text between them."""
    current = path.read_bytes().decode("utf-8")
    lines = current.splitlines(keepends=True)
    starts = [i for i, line in enumerate(lines) if line.rstrip("\r\n") == TABLE_BEGIN]
    ends = [i for i, line in enumerate(lines) if line.rstrip("\r\n") == TABLE_END]
    if len(starts) != 1 or len(ends) != 1 or starts[0] >= ends[0]:
        raise ValueError(f"{path.name} needs exactly one ordered pair of Docker tag table markers.")

    start, end = starts[0], ends[0]
    newline = "\r\n" if lines[start].endswith("\r\n") else "\n"
    block = f"\n{table}\n".replace("\n", newline)
    updated = "".join(lines[:start + 1]) + block + "".join(lines[end:])
    if path.name == "DOCKERHUB.md" and len(updated.encode("utf-8")) > DOCKERHUB_MAX_BYTES:
        raise ValueError(f"{path.name} exceeds the Docker Hub limit of {DOCKERHUB_MAX_BYTES} bytes.")
    return current, updated


def write_tables(releases: list[Release]) -> None:
    tables = {"README.md": complete_table(releases), "DOCKERHUB.md": short_table(releases)}
    pending = []
    for name, table in tables.items():
        path = REPO_DIR / name
        current, updated = render_document(path, table)
        if current != updated:
            pending.append((path, updated))

    staged = []
    try:
        for path, updated in pending:
            with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as handle:
                temporary = Path(handle.name)
                staged.append((temporary, path))
                handle.write(updated.encode("utf-8"))
            os.chmod(temporary, path.stat().st_mode & 0o777)
        for temporary, path in staged:
            temporary.replace(path)
    finally:
        for temporary, _ in staged:
            temporary.unlink(missing_ok=True)


def buildx_command(record: Release) -> str:
    app_version = record.app_full.removeprefix("v").removeprefix("V")
    build_args = [f"  --build-arg APP_VERSION={app_version} \\"]
    if record.field("Docker OS").casefold() == "alpine":
        lego_version = record.field("Lego Version").removeprefix("v").removeprefix("V")
        if lego_version:
            build_args.append(f"  --build-arg LEGO_VERSION=v{lego_version} \\")
        if os_version := record.field("Docker OS Version"):
            build_args.append(f"  --build-arg ALPINE_VERSION={os_version} \\")
    lines = [
        "docker buildx build \\",
        f"  --builder {BUILDER} \\",
        f"  --platform {PLATFORMS} \\",
        "  --progress=plain \\",
        *build_args,
        *(f"  -t {DOCKER_IMAGE}:{tag} \\" for tag in record.tags),
        "  --push ./build",
    ]
    return "\n".join(lines)


def main() -> int:
    parser = argparse.ArgumentParser(description="Update the Docker tag tables in README.md and DOCKERHUB.md.")
    parser.add_argument("--buildx-version", metavar="VERSION", help="Also print the Buildx command for an app version")
    parser.add_argument("--allow-old-latest", action="store_true", help="Permit Buildx for an older version that overwrites latest")
    args = parser.parse_args()
    try:
        releases = read_releases(CSV_PATH)
        selected = None
        if args.buildx_version:
            selected = next((r for r in releases if r.app_full == args.buildx_version), None)
            if selected is None:
                raise ValueError(f"Unknown App Version {args.buildx_version!r}.")
            latest_owner = ownership(releases).get("latest")
            if ("latest" in selected.tags and latest_owner is not None
                    and releases[latest_owner] is not selected and not args.allow_old_latest):
                raise ValueError(
                    f"{selected.app_full} does not own latest. Its Buildx command would overwrite latest. "
                    "Use --allow-old-latest if intentional."
                )
        write_tables(releases)
        print(f"README.md: {len(releases)} releases")
        print(f"DOCKERHUB.md: {min(len(releases), SHORT_MAX_RELEASES)} releases, up to {SHORT_MAX_DISPLAY_TAGS} visible tags each")
        if selected is not None:
            print("\n" + buildx_command(selected))
        return 0
    except (OSError, ValueError, csv.Error) as exc:
        print(f"ERROR: {exc}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
