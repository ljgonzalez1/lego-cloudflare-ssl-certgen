# Local validation — v6.0.1-beta-2

This report retains the supplied beta-2 validation record and includes the additional inheritance checks performed on 2026-11-20 (Linux AMD64, Go 1.23.2 and Python 3). The original environment documented Go 1.27.4, Python 3.12, Ruff 0.17.5 and a ShellCheck 0.11 development build; those original checks were not all rerun here. Neither environment has established a successful GitHub/Docker publication.

| Check | Observed result |
| --- | --- |
| Language analyzers | PASS: Go, Python, Bash, POSIX sh, jq, awk, sed, Dockerfile, YAML, Actions expressions, CSV, JSON, modules and other formats |
| Go formatting and vet | PASS for ARMv6, ARMv7, ARM64, i386 and AMD64; recursive first-party discovery |
| Static checker regressions | 14 passed |
| Five-diagram workflow routing and failure gates | 19 passed after inheritance, including every static-failure/event combination |
| Go unit tests, shuffled | PASS |
| Go race detector, shuffled | PASS |
| Go statement coverage of instrumented packages | 43.3%; compiled CLI tests are separate and do not add to this number |
| Compiled CLI suite | 23 passed; 2 intentional pre-pulse skips |
| Offline shell scenarios | 48 passed, zero failures |
| Docker Hub protocol/digest mock tests | 19 passed |
| Publisher and release identity tests | 10 passed after inheritance |
| Version/tag policy tests | 10 passed after inheritance |
| Compressed application and lego alias CLI | Not part of this historical profile |
| Fuzzing | PASS: argument, YAML and lego log targets, 15 seconds each; two workers per campaign |
| Source license copies and asset fingerprints | PASS; artwork/trademark grants remain identified as unresolved source-asset rights |
| CSV and generated version tables | PASS; 56 historical release records |

Static format contracts check syntax, duplicate keys, workflow dependencies/cycles/step shape, expression structure, embedded shell, jq compilation, awk/sed programs, strict JSON, TOML, CSV, environment assignments, Markdown file targets, SVG XML and PNG integrity. These project checks do not claim the full feature set of actionlint/hadolint or an actual Docker engine. Unknown source formats fail inventory. The two SC2016 child-shell constructs from the screenshot are standalone analyzed fixtures, with their process-group regression tests preserved.

The event graph runs all static jobs before Markdown checks. PRs to master require static success; master pushes, version activity and release/prerelease static checks are advisory. Markdown checks, when required by the diagram, remain blocking. Offline suites, all five smokes, Buildx publication and registry verification remain blocking. Cancelled/skipped required work cannot authorize publishing. Badge commits update only an exact image/metadata allowlist on master, never Markdown or auxiliary branches.

## Integration that was not validated here

Docker, a Docker daemon, Podman and QEMU are unavailable in this execution environment. Actual scratch builds, target execution under QEMU, complete notice collection from all real upstream binaries, image size, live-issuance RAM and real registry publication were not executed. Local fixtures do not establish that the configured lego v4.34.0 image builds or issues certificates successfully.

The source build and actual target-image probes are mandatory GitHub Actions gates. Until they pass, v6.0.1-beta-2 must be treated as an integration candidate, not a fully certified production image. No live ACME request, Cloudflare DNS change or remote repository/publication write was performed by this delivery. No measured Docker image size is invented from fixture binary sizes.

## Distribution notes

The project code remains MIT. Third-party code, Go, CA data, fonts and marks keep their own terms. Production compact profiles retain complete required notices in a compressed archive outside the binaries. Removing notice copies is not justified by their lack of executable functionality. Source artwork with unresolved grants is excluded from runtime images; the audit does not grant third-party artwork or trademark rights.

## Additional beta-2 inheritance validation

- Workflow routing and required Markdown dependencies: 19/19 tests passed, including first-push and alpha/beta/rc version branches.
- Version grammar and Markdown/CSV rules: 10/10 tests passed.
- Simulated Docker Hub publication and release identity: 10/10 tests passed.
- `go test -mod=vendor ./...` passed with Go 1.23.2 in `build/`.
- `ci/check-version.sh` passed on both documents for beta-2, historical alpha-2, an uppercase RC branch and a branch using `+build`.
- Both tables were regenerated from the CSV; the alpha-2 history row is absent from DOCKERHUB.md and unlinked in README.md.
- Actual Buildx and Docker image smoke tests were not executed in this environment.
