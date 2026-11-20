# Local validation — v6.0.1-beta-1

Validated on 2026-11-19 with Go 1.27.4 on native Linux amd64, as an ordinary non-root user. Profile: Debug with Delve capability and explicit trace controls. These are source archives, not locally built Docker images.

## Executed checks

| Check | Result |
| --- | --- |
| Go unit tests, shuffled, all project packages | PASS |
| Go race detector, shuffled | PASS |
| Go statement coverage, unit run including build helpers | 43.3% |
| Compiled CLI tests | 23 passed; 2 intentional skips |
| Offline shell scenarios | 48 passed; no failures/skips |
| Docker Hub protocol/digest tests against local mock registry | 19 passed |
| Publisher/release identity tests | 9 passed |
| Workflow graph/required-check tests | 24 passed |
| Generated version/tag tests | 4 passed |
| Go vet and executable ELF cross-compilation/inspection | PASS for all five target architectures |
| gofmt, ShellCheck and Ruff | PASS |
| Source license copies, asset hashes and embedded font notices | PASS; artwork grants remain a separate open review |
| README and Docker Hub tables/identity | PASS; 55 historical CSV rows |
| FuzzParseOptions, FuzzLoadDomains and FuzzLegoLog | PASS; 15 seconds per target with 2 workers |

The five Linux targets are arm/v6, arm/v7, arm64, 386 and amd64. Cross-compiling and inspecting an ELF is not a native/emulated Docker execution. Two pulse checks are intentionally skipped because this snapshot predates the pulse introduced in beta-3.

The source history is a chronological prefix through this release. It restores v4.2.10 through v4.2.16 and includes the applicable alpha/beta identities. Later versions appear in their later snapshots, ending with all 62 records in v6.0.3.

## Size and RAM measurement scope

CI smoke jobs run ci/profile-image.sh for each final image. It reports Docker's uncompressed image layer size, elapsed offline --test time with actual lego --version, and Linux wait4 maximum task RSS including waited children. This is not simultaneous cgroup peak, production issuance RAM or a guarantee for all SAN sets. QEMU can add overhead. The probe is mounted for the measurement and never copied into scratch.

A local native v6.0.2 wrapper/compiled-fixture --test probe returned exit code 0 and 4,192 KiB wait4 maximum task RSS. This is a fixture measurement only, not a measurement of upstream lego or a Docker image.

## Not executed in this environment

Docker/Podman, a Docker daemon and QEMU are unavailable. The following remain pending on the required CI runners:

- Real Docker builds and scratch execution on all five platforms, including Go 1.27.4/Delve v1.27.2 probes for debug snapshots.
- Authenticated upstream lego/source downloads and generation/verification of the complete real-image third-party inventory. Unit tests exercise offline source/archive fixtures and tamper/missing-notice rejection, not the live upstream dependency graph.
- Final image storage size and RAM with actual upstream lego on each architecture.
- Registry publication and comparison of the resulting live Docker Hub index and five per-platform digests.
- Real ACME/DNS issuance, which requires the owner's credentials and domains. No live certificate was requested during these checks.

The workflows retain blocking final-image, license-inventory and registry-digest checks where applicable; live ACME issuance requires a separate owner-authorized run. A passed local mock test does not assert that an unpublished image is present in Docker Hub. Do not describe the Docker, upstream-license or live-issuance checks as passed until their actual runs succeed.

## License scope

Original project code remains MIT. Third-party modules, Go runtimes, CA/timezone data, fonts and marks retain their own terms. The build-time collector fails when version/source/notice evidence is missing and verifies notices against the assembled payload; it does not turn other licenses into MIT or certify legal compatibility. Supplied artwork/trademark grants remain explicitly unresolved. See LICENSES.md and licenses/assets.json.

The CLI and shell tests use local stand-ins; their certificate files are fixtures and are not trusted certificates. The reports describe the implemented behavior and observed checks, not a claim of universal error-free operation.
