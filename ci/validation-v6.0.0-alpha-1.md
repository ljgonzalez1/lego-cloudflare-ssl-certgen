# v6.0.0-alpha-1: Go migration and validation

All project C++ application and build-tool sources have been replaced by Go.
The runtime still calls the official lego v4.34.0 executable for Cloudflare
DNS-01. GitHub Actions and the version-table updater retain their host scripts.

## Behavior preserved

- Interactive issuance offers Staging, Production and Cancel. Staging is
  selected initially; both confirmation screens initially select No.
- `--staging` and `--production` explicitly bypass the menu. Production prints
  a warning. Conflicting modes, unknown arguments and missing values fail.
- `--test` is unattended and offline: it checks lego and previews staging
  without submitting an order or creating an output directory.
- Compose uses `x-certgen-mode: "--staging"` inside its file. Editing that one
  value to `"--production"` selects unattended production. No production-mode
  environment variable is read by the application.
- Group ordering, aliases, wildcard quoting, normalization, cross-zone SAN
  certificates, group selection and the 100-name limit remain supported.
- Every certificate group has its own capped two-hour deadline. Definitive
  errors stop the affected process group early; ordinary progress, recoverable
  retries and cleanup warnings do not stop a working process.
- Interruptions stop the current process group, restore the menu terminal and
  prevent the next group from starting. Success requires saved nonempty
  certificate and private-key files; failed groups do not count as issued.
- ANSI live output and ASCII summary tables preserve their counts and layout.
  Emojis stay outside the summary grid. Color suppression is supported.

## Debug profile

The default image uses Go 1.27.4 and statically cross-compiles with:

```text
CGO_ENABLED=0
-gcflags="all=-N -l -d=checkptr=2"
-ldflags="-compressdwarf=false"
```

Symbols and uncompressed DWARF remain in the actual ELF. Source paths remain
available at `/src` in the image. The checker rejects a real build using
`-N=false`, which a substring-only flag check would incorrectly accept.

Beta diagnostics keep 64 bounded events in memory and print events and
goroutine stacks to stderr on failure. Known credential values are redacted.
No additional runtime trace file is created. `lego.log` retains its existing
per-certificate role. `CERTGEN_TRACE=always` enables immediate event output.

`GOGC=off` suppresses heap-growth-triggered collection. `GOMEMLIMIT=256MiB`
remains a soft memory backstop and can trigger collection. This does not promise
zero GC or impose a hard container memory limit.

The build installs Delve v1.27.2 for AMD64, ARM64 and i386 and copies its module
licenses. Delve does not support 32-bit ARM; those binaries keep full symbols,
DWARF and alpha diagnostics. The image build self-check runs `dlv version` where
supported. A separate race-enabled native test uses CGO and a C compiler;
32-bit ARM and i386 images do not require an unsupported race build.

## Executed locally on 2026-11-18

Host: Linux AMD64. Compiler: official Go 1.27.4.

| Check | Result |
|---|---|
| `gofmt` and `go vet` | Pass |
| Debug Go unit and seed tests | 83 cases pass, 0 failures |
| Same Go tests with the race detector and pointer checks | 83 cases pass, 0 failures |
| Real compiled Go executable tests | 13 tests pass |
| Real ELF builds for AMD64, i386, ARMv6, ARMv7 and ARM64 | All compile; architecture, debug sections and build flags verified |
| Existing runtime regression suite | 48 groups pass, 0 failures, 0 skips |
| Same runtime regression against a race-enabled application | 48 groups pass, 0 failures, 0 skips |
| Interactive pseudo-terminal tests, included in runtime regression | 15 tests pass, including signal cleanup and unattended overrides |
| CI event routing, Compose mode and job dependencies | 15 tests pass |
| Docker Hub verification logic with local mocked responses | 14 tests pass |
| Prerelease tag policy shared by Python and the publisher | 4 tests pass |
| YAML, lego log and CLI fuzzing | More than 700,000 generated executions; no failure reported |
| Version-document gates for version push, PR to master, master push and release | Both documents pass in all four scenarios |
| Publisher's alpha tag list | Eight suffixed tags; no `latest`, `v6` or unsuffixed `v6.0` |
| Python, shell and Dockerfile JSON instruction syntax | Pass |

Tests cover malformed, oversized and recursive YAML; missing files and values;
FIFO input; invalid identity and settings; credential precedence and secret-file
handling; malformed or corrupt downloads; HTTPS downgrade refusal; archive
links and traversal; concurrent output reservation; long and narrow terminal
output; partial DNS failures; fatal errors with no final newline; signal
forwarding; descendant cleanup; per-group timeouts and bounded output capture.

The generated tables keep `latest` owned by v5.2.4 while v6.0.0-alpha-1 owns
only its eight suffixed aliases. The publishing script's output agrees with
the Python tag generator.

## Execution limits

Docker, a Docker daemon and QEMU are unavailable in the local validation
environment. Its kernel also rejects the i386 executable with an execution
format error. Foreign-platform binaries were compiled and inspected, not
executed here. The five actual image smoke jobs remain required in GitHub
Actions after offline checks, using native AMD64/ARM64 and QEMU where needed.

The complete Docker build, Compose execution, actual Delve installation and
debugger sessions, live ACME issuance, registry publication and live Docker Hub
description synchronization were not executed locally. No real token or DNS
zone was used and no production quota was consumed. Registry tests use local
mock responses; they do not establish the live state of Docker Hub.

The Dockerfile includes a network-disabled check of the final runtime,
debugger availability, lego version/architecture, offline mode and rejected
unattended invocations. That check and the five smoke jobs validate these
image-specific conditions when the workflow runs.

## Release procedure

Use branch and Git tag `v6.0.0-alpha-1`. Commit the updated CSV, versions file,
both generated Markdown tables and the Go migration before publishing a
GitHub prerelease. The graph remains document gates -> offline -> five
parallel smokes -> release-only build/push. Image propagation and description
propagation each have a separate 20-second passing job before verification.

Replace the old source tree when applying the complete project snapshot.
The migration removes `build/src/`, `build/tools.cpp`, the C++ settings test
helper and the old standalone musl/libyaml license files. The Go YAML vendor
license retains the notices that apply to its own source.
