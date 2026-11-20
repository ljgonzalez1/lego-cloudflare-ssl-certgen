# Beta 6: Go formatting, Delve configuration and committed license evidence

## What the beta-5 logs show

The beta-5 bundle export (`trace-license-bundle.log`) completed: 670 files,
241 components and 333 unique notice texts were collected and verified, and
239 third-party components were exported. The complete five-platform trace
(`trace.log`) reused that bundle and finished every stage: all five
`image-self-test` runs printed `PASS all image checks`, all five `licensed`
audits ended with `status=0`, and all five `target` stages completed. Docker Hub
also lists `v6.0.1-beta-5` for all five platforms (pushed 2026-11-20 13:40 UTC),
so the release pipeline passed; Go static analysis is advisory for releases.
Beta 5 did not fail in the image build itself. The remaining defects were:

1. **Go static analysis.** `gofmt` rejected the struct-literal alignment in
   `build/internal/imagebuild/licenses.go`. That was the only Go finding;
   `go vet` passes for all five targets after formatting.
2. **Delve error on every debugged start.** The trace contains nine
   `error layer=debugger could not create config directory: mkdir /nonexistent:
   permission denied` lines. Delve creates `~/.config/dlv` before listening;
   the runtime account's home is `/nonexistent`. The run continued, but the
   same error line appeared in users' logs on AMD64 whenever Delve was retained.
3. **License evidence not in the repository.** The `v6.0.1-beta-5` branch only
   contains `build/licenses/third-party/README.md`, so every CI smoke build had
   to download and authenticate all upstream sources again.

The 386 and ARM64 probes report `fork/exec ... function not implemented` under
QEMU. That is the designed fallback: Delve is removed and Go diagnostics remain.
On native hardware the probe decides independently.

## Corrections

- `licenses.go` is formatted with Go 1.27.4 `gofmt`.
- Delve receives `XDG_CONFIG_HOME=<private 0700 session dir>/delve-config`.
  Delve passes its environment to the debugged program, so the caller's original
  `XDG_CONFIG_HOME` state travels in an internal variable. The worker restores
  it (or unsets it) before any lego process starts and removes the marker.
  Native runs without Delve are unchanged. Unit tests cover the unset, empty and
  set cases, stale inherited markers and the environment of a real child process.
- `build/licenses/third-party/` now contains `bundle.json` and 332 texts, made by
  `certgen-build prepare-license-bundle` from the authenticated upstream sources
  in an Alpine 3.24.4 root with the official Go 1.27.4 toolchain. All 241 source
  bindings (versions, `h1` sums and origins) and per-component document counts
  are identical to the beta-5 export log. `bundle.json` SHA-256:
  `030a2038bd7dfd2e143e8c23d2f3a6d90d55b33a6408751f8ba06ede8b0664c5`.
- `.gitattributes` marks those files `-text`, so no line-ending conversion can
  break their SHA-256 binding (one upstream text uses CRLF, ten have no final
  newline).
- Release identity is `6.0.1-beta-6`, appended to the CSV after the published
  beta 5 row. The README and Docker Hub tables were regenerated with
  `ci/versions/tags_table.py`.
- Build diagnostics report `revision=beta6`.

## Validation performed here

| Check | Result |
| --- | --- |
| Go 1.27.4 `gofmt` and `go vet` for ARMv6, ARMv7, ARM64, 386 and AMD64 (`ci/analyze.py go`) | PASS |
| Python, Bash, POSIX sh, jq, awk, sed, Dockerfile, YAML, Actions, CSV, JSON and Go module analyzers (ShellCheck 0.11.0, Ruff 0.17.5) | PASS |
| Format inventory and static contracts, including the committed bundle | PASS |
| Shuffled Go unit tests with coverage, race detector, fuzzing (3 targets, 15 s each) | PASS |
| Offline CA/runtime regressions (`sh ci/tests/run.sh`, non-root) | PASS: 48 passed, 0 failed |
| Workflow, publisher, license trace, license bundle, tag policy and Docker Hub mocks | PASS: 19, 10, 11, 7, 10, 19 tests |
| Real Go binaries, logs, dumps and five debug ELF targets (`ci/test_go_binary.py`) | PASS: 25 tests |
| README and DOCKERHUB version checks for a push to `v6.0.1-beta-6` | PASS |
| Bundle preparation from upstream (Alpine chroot, network) | PASS: 239 components exported |
| AMD64 stages emulated natively in that chroot: license-seed reuse, downloader, builder, Delve, `image-self-test` with Delve and no network, offline `licenses` and `verify-licenses` | PASS: 671 files, 241 components, 333 texts, same counts as beta-5 Docker AMD64; zero Delve config errors |
| Real Docker Buildx with QEMU for five platforms, smoke tests and publication | NOT RUN: no Docker daemon here |

The chroot emulation follows the Dockerfile commands and environment but is not
BuildKit. ARMv6, ARMv7, ARM64 and 386 images were not built here.

## Check on your machine

```bash
bash ci/trace-build.sh all
grep -c 'could not create config directory' trace.log    # expected: 0
```

The first command uses the committed bundle (`reuse 239 bundled third-party
components`). To confirm the committed evidence instead of trusting it:

```bash
bash ci/update-license-bundle.sh
git status --short build/licenses/third-party    # expected: no output
```

If Alpine publishes a new `tzdata` or `ca-certificates-bundle` revision for 3.24,
the build stops with `installed ... changed since license bundle preparation`.
Run the refresh command above and commit the result. The trace scripts and the
large-log builder from the [beta-5 guide](validation-6.0.1-beta-5.md) still apply.
