# Beta 7: fixed license files and passing smoke tests

## Why every beta-6 smoke test failed

All five smoke jobs stopped in the license check after loading the image:

```text
tarfile.AbsoluteLinkError: 'etc/mtab' is a link to an absolute path
```

`docker export` includes the engine's container init layer, such as
`/etc/mtab -> /proc/mounts`, which is not part of the image. Python's safe
extraction filter rejects that link. That check existed only to verify the
generated license inventory. Beta 7 removes the inventory and the check, so the
smoke test goes straight to the image checks: labels, debugger state, the
offline `--test` run, its log and the resource profile.

## Licenses

The license files are fixed and live in `build/licenses/`. The Dockerfile copies
them unchanged to `/usr/share/licenses/`:

| File | Why it is required |
| --- | --- |
| `NOTICE` | Copyright and permission notices that the MIT (lego, Delve, YAML) and BSD-3-Clause (Go) licenses require with every copy, the YAML library's Apache NOTICE, and where to get the source of the MPL-covered CA list |
| `Apache-2.0.txt` | Apache 2.0 requires a copy of the license (YAML library compiled into certgen) |
| `MPL-2.0.txt` | MPL 2.0 requires a copy of the license (Mozilla CA list) |

Program versions are pinned, so these files only change when a version does.
The project's own MIT license is not repeated in the image. tzdata is public
domain and needs no notice.

Removed: the hash-addressed bundle, the `licenseaudit` package, the
`license-seed` and `licensed` stages, the `licenses` and `verify-licenses`
build commands, `check-image-licenses.sh`, `check_licenses.py`, the asset
fingerprint inventory under `licenses/`, `LICENSES.md`, and every license test.
The downloader now extracts only `lego` from the release archive.

## Validation performed here

Each Dockerfile stage ran natively in an Alpine 3.24.4 root with the official
Go 1.27.4; ARM targets ran under QEMU user emulation. The real
`ci/tests/smoke.sh` and `ci/profile-image.sh` then ran unmodified as a
non-root runner (UID 1001), against a `docker` stand-in that runs the image
with certgen as PID 1 in its own PID, mount and network namespaces.

| Platform | Delve after probe | Build stages | Smoke test |
| --- | --- | --- | --- |
| linux/amd64 | kept (native ptrace) | PASS | PASS |
| linux/386 | removed (ptrace blocked as under QEMU) | PASS | PASS |
| linux/arm64 | removed (QEMU) | PASS | PASS |
| linux/arm/v7 | not built | PASS | PASS |
| linux/arm/v6 | not built | PASS | PASS |

| Check | Result |
| --- | --- |
| All `ci/analyze.py` languages, including Go `gofmt`/`vet` for five targets | PASS |
| `staticcheck` for amd64, 386, arm64 and arm (style-only checks excluded) | PASS after removing a deprecated `tar.TypeRegA` check |
| Shuffled Go unit tests with coverage, race detector, fuzzing (3 targets, 15 s each) | PASS |
| Offline regressions (`sh ci/tests/run.sh`, non-root) | PASS: 48 passed, 0 skipped |
| Static contracts, workflows, publisher, trace runner, tag policy, Docker Hub mocks, Go binaries | PASS: 14, 19, 10, 7, 10, 19, 25 tests |
| README and DOCKERHUB version checks for `v6.0.1-beta-7` | PASS |
| Real Docker Buildx and Docker Hub publication | NOT RUN: no Docker daemon here |

The emulation is not BuildKit or Docker: Docker's default seccomp profile and
capabilities were not reproduced. With `dlv` present and ptrace blocked,
`certgen --test` falls back to Go diagnostics and exits 0, so the resource
profile does not depend on them. In CI, arm64 runs natively and keeps Delve,
like amd64 here.

## Check on your machine

```bash
bash ci/trace-build.sh all
```
