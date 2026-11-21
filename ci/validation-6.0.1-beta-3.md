# Local validation — v6.0.1-beta-3

This report describes checks on the corrected beta-3 source snapshot, not a GitHub Actions or Docker Hub publication. The beta-3 spinner, branding and application changes remain in place; the previously corrected version, license and CI policies have been inherited.

## Verified in this working environment

| Check | Result |
| --- | --- |
| Version branch / PR / release workflow routing and required Markdown gates | PASS: 19 tests |
| Version grammar, CSV and Docker tag policy | PASS: 10 tests |
| README.md and DOCKERHUB.md generated table checks | PASS, both scripts executed directly |
| Go unit tests (`go test -mod=vendor ./...`) | PASS, including spinner and license-audit packages |
| Compiled CLI integration tests, including terminal pulse and signal handling | PASS: 25 tests |
| YAML/JSON/CSV syntax and module graph | CSV, JSON and offline Go module graph: PASS |
| Bash syntax of inherited shell scripts | PASS |
| Source release configuration | APP_VERSION=6.0.1-beta-3, LEGO_VERSION=4.34.0, ALPINE_VERSION=3.24.4 |
| Release history | 57 CSV rows; v6.0.0-alpha-2 remains an unpublished branch-only row |

Local Go tests used Go 1.23.2. The workflow pins Go 1.27.4 for the actual CI build.

## Not verified here

Docker, Buildx, QEMU and a Docker Hub publication were not available in this environment, so the five real target-image smoke tests, final license inventory from upstream binaries, multiarchitecture image build and published image digests were not executed. Full static validation of GitHub Actions / YAML and Python could not finish because this environment does not have `shellcheck` and `ruff`; these dependencies are installed by the workflow runners.

The actual Docker build, required Markdown checks and smoke-test jobs remain defined as CI gates. A successful local unit or integration test does not certify those external jobs. No live ACME request, Cloudflare DNS change, registry publication or remote repository write was performed.

## Distribution notes

Project code and third-party programs retain their respective license requirements. The explicit Oracle OCI SDK split-module license exception is limited to the reviewed v1065.111.0 package version; other license-free modules still fail closed. Source artwork, fonts and logos retain their separate asset notices and are not automatically licensed merely by inclusion in this repository.
