# GitHub-managed badge images

The separate **Update local badges** workflow generates and commits seven local
SVG images on the existing master branch. It creates no auxiliary branch, uses no
Shields.io/Pastebin/Pages badge service, and never edits a Markdown file. README
links are fixed once in this release and remain unchanged by automatic refreshes.
The images are visible in a Gitea clone/mirror that contains their referenced files.
No Gitea API, account, mirror configuration or secret is used by the updater.

## Activation and data

Install the project/workflow on master, the GitHub default branch, then optionally
initialize **Actions → Update local badges → Run workflow → master**. CI/CD events,
hourly polling at minute 23 and manual dispatch update the snapshots. Branch rules
must allow GITHUB_TOKEN to commit the generated assets. No PAT is needed by default;
a denied write fails visibly without bypassing branch rules or forcing a ref.

All badge generation, storage and automation runs through GitHub. There is no
additional hosted badge service. Docker Hub remains the existing project registry
and the original source of its public pull counter; GitHub has no equivalent Docker
pull metric. The workflow queries that existing counter directly and never sends
the GitHub token to Docker Hub. Other observations come from GitHub Actions.

| Badge | Meaning |
| --- | --- |
| Docker Hub | Fixed repository link. |
| Docker Pulls | Integer Docker Hub counter at the last successful poll. |
| Docker Image | Application version from the latest confirmed successful release publication. |
| CI/CD | Newest tests.yml push run on master, including a PR merge's push. |
| Gitea | Fixed supplied repository link. |
| lego version | Dependency from the same released commit as Docker Image. |
| License | MIT for original project code; other components keep their terms. |

The newest master run maps in_progress to Testing/orange, completed/success to
Passing/40dc4a and completed/failure to Failing/red. Every other state, no run or an
unavailable read is Unknown/lightgrey. PR checks before merging, other branches and
release runs cannot replace master's result. Selection uses run creation order and
attempt, so an old run finishing or rerunning late cannot overtake a newer run.

Docker Image/lego change together after the overall release workflow succeeds and
its publisher job succeeds. Functional/image/digest checks remain required; static
analysis jobs are advisory specifically on release and prerelease events. Versions
come from that run's immutable released CSV, with legacy TXT reading only for older
historical commits. Editing the current source version never advertises an
unpublished image. Confirmed data survives expired Actions history or a failed read.
The lego link targets the upstream releases list so no Markdown rewrite is needed.

These are dated snapshots, not a live number at every reader's exact second.
Scheduling, API reporting, runner startup and browser/image caches can delay updates.
The source ZIP starts at Unknown, without inventing a successful CI or publication.
assets/badges/state.json records run/attempt/commit and observation timestamps. A
failed Docker read keeps the prior value/date; a failed CI read displays Unknown.

## Two independent loop protections

The writer allowlist is exactly seven SVGs plus assets/badges/state.json and
assets/badges/manifest.json. Git tree/ref updates are atomic, preserve unrelated
files, are not forced, and are recomputed on a concurrent master update. Source/icon
changes require a fresh checkout. README.md, other Markdown and the global source
license inventory are excluded from the write allowlist.

First, the workflow uses the automatic GITHUB_TOKEN, whose push does not start
another push workflow. Second, the main CI push trigger independently ignores only
those nine exact generated paths. A commit changing any other path still runs CI;
PRs are not excluded by this filter. Tests enforce equality between this filter and
the writer allowlist. This also prevents a badge-only loop if the token strategy is
changed later. No broad assets or source-license exclusion hides other changes.

The generated-only manifest stores hashes/terms for SVGs and state JSON; it does
not attempt to hash itself. The static source inventory remains separate. Original
MIT renderer code and unmodified pinned Simple Icons CC0 paths keep their notices;
brand marks remain with their owners. No badge code or image enters scratch.

## Validation

Run python3 -B ci/test_badges.py, python3 -B ci/badges.py --check and
python3 -B ci/check_licenses.py. The render mode regenerates only image/metadata
files from saved state without network calls. The main CI tests state mapping,
publication provenance, failures, races, immutable inputs, restricted writes,
self-contained images, unchanged Markdown and both loop protections.

A real GitHub run is still needed to validate this repository's token/branch policy.
This source delivery does not publish to GitHub or configure a Gitea mirror.

Primary references:

- https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows
- https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/trigger-a-workflow
- https://docs.github.com/en/rest/actions/workflow-runs
- https://docs.docker.com/reference/api/hub/latest/operations/GetRepository/

## Event diagram policy

The newest master push or release/prerelease run drives CI/CD: queued/running is Testing (orange), successful is Passing (#40dc4a), failure is Failing (red), and other conclusions are Unknown (light grey). Pull requests and version-branch pushes do not change it. The Docker Image badge shows Testing while a release runs, then the tested version in blue (#2684d5), or red on failure. This is a test status; publication remains separately confirmed by the publish job. An older delayed notification cannot replace a newer run. The fixed Markdown link opens the release list, which includes the tested release; updating an exact per-release outer link would require editing Markdown. All images stay local and clone to Gitea.
