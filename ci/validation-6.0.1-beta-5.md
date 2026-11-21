# Beta 5: checksum binding, local notices and build traces

## Cause and correction

The supplied beta-4 r3 logs fail at inventory verification, after upstream module
ZIP authentication and Alpine recipe/archive collection have completed. They do
not show a network failure at the fatal step.

The collector assigned `module:gopkg.in/yaml.v3@v3.0.1` to both certgen's partial
vendored source tree (no full-module `h1` sum) and lego's authenticated complete
upstream module. Its component cache returned the first record without comparing
source identity. Verification then compared lego's real module sum against that
earlier empty component sum and reported only `module source checksum binding changed`.

Beta 5 assigns the vendored subtree its own component identity and preserves the
authenticated upstream component's sum. Cache reuse rejects conflicting source
versions or checksums. Verification still rejects genuine checksum mismatches;
its error now identifies the binary, module, component and both checksum values.
Regression cases cover both collection orders, conflicting identities and a real
mismatch at the verifier boundary. They are committed, but could not be executed
without a Go compiler in the editing environment.

## Prepare and retain original third-party notices

From the extracted project root, with Docker Buildx and Python 3 installed:

```bash
bash ci/update-license-bundle.sh
```

If Docker requires sudo on your machine, prefix that command with sudo. This
explicit preparation authenticates upstream source evidence once, then exports
only `build/licenses/third-party/bundle.json` and `texts/*.txt`. Include those
files in Git. Original copyrights, licenses, NOTICE files, source header
attributions, module sums and exact Alpine recipe/source provenance are retained.
No executable, module ZIP, Go SDK archive or compiled helper is copied into that
project directory. The project license does not replace third-party terms.

**The delivered ZIP does not already contain the complete generated bundle.**
The editing environment could not retrieve the required upstream archives and
has no Docker daemon to run their authenticated collection. Existing project,
vendored YAML, MIT, Apache and MPL texts are included. The exporter supplies the
complete remaining set on your first preparation run, using the same original
sources that your supplied logs show were reachable.

A normal Docker build also works without a committed bundle: one shared native
`license-seed` stage prepares it for the entire build, including Delve evidence.
It inspects AMD64 metadata without executing the evidence binaries, so a target's
failed ptrace probe cannot omit debugger notices needed by another target.
BuildKit can cache this evidence stage. It writes no files into the host project;
use the export command above to retain and review the texts there.

With a prepared bundle, each target's `licensed` stage runs with `--network=none`.
It selects only notices for the retained executables, compares module versions
and available `h1` sums, rechecks notice SHA-256 values, and checks Alpine package
version/license/origin/aports commit plus every retained data fingerprint or link.
A new dependency, SDK version or changed Alpine payload fails with an explicit
refresh instruction. The initial evidence set includes the pinned AMD64 module
union; an architecture-specific module absent from that set also fails closed.
In the supplied r3 logs, all 217 modules observed for each other architecture are
included in AMD64's 235-module set. This is evidence for the current dependency
set, not a guarantee for future versions.
No checksum comparison is disabled to make the inventory pass.

## Build without pushing, with a local trace

```bash
bash ci/trace-build.sh all
```

This performs a complete uncached five-platform build, uses plain Buildx output,
does not push or load the image, and overwrites **`./trace.log`** in the project
root. Native AMD64 only:

```bash
bash ci/trace-build.sh linux/amd64
```

To rerun only the final license audit while retaining other cached stages:

```bash
bash ci/trace-licenses.sh all
```

That saves `./trace-licenses.log`. Bundle preparation saves
`./trace-license-bundle.log`. All three paths are in `.gitignore`; build-context
logs are excluded by `.dockerignore`. The static source inventory ignores only
those three designated root log paths. Scripts preserve Docker's failure status
through `tee` and report log-writing failures. To retain previous runs, pass an
explicit log path as the second trace argument (first export-script argument).

The default builder is `cf-lego-certgen-multiarch`. Select another with
`CERTGEN_BUILDER=builder-name`. The diagnostics retain compiler `-v -x`, command
start/end/status/duration, copy operations, HTTP and checksum evidence, dependency
counters, self-test outcomes and 15-second activity heartbeats. A heartbeat means
the command is alive; it does not mean an operation or audit has passed.
Expected CLI rejections inside `image-self-test` are negative assertions; their
PASS markers and the enclosing RUN exit status determine whether the test passed.

BuildKit may truncate very large step logs. If that occurs, create a separate
builder with increased log limits; do not delete your existing builder:

```bash
docker buildx create --name cf-lego-certgen-beta5-trace \
  --driver docker-container \
  --driver-opt image=moby/buildkit:v0.35.1 \
  --driver-opt env.BUILDKIT_STEP_LOG_MAX_SIZE=104857600 \
  --driver-opt env.BUILDKIT_STEP_LOG_MAX_SPEED=10485760
docker buildx inspect cf-lego-certgen-beta5-trace --bootstrap
CERTGEN_BUILDER=cf-lego-certgen-beta5-trace bash ci/trace-build.sh all
```

## Validation performed here

| Check | Result |
| --- | --- |
| Trace runner with fake Docker, project log paths and pipeline failures | PASS: 11 tests |
| Text-only bundle installation, corruption/link/executable rejection and preservation of earlier evidence on validation failure | PASS: 7 tests |
| Static contracts and recursive file discovery | PASS: 14 tests |
| Workflow routing and dependencies | PASS: 19 tests |
| Publisher and release identity mocks | PASS: 10 tests |
| Docker Hub verification mocks | PASS: 19 tests |
| Version/tag policy | PASS: 10 tests |
| README and Docker Hub beta-5 identity/table checks | PASS |
| Remaining file formats (excluding workflow ShellCheck), source notices/assets, Python AST and Bash syntax | PASS |
| Full workflow static analyzer | NOT RUN to completion: ShellCheck unavailable |
| Real Go compilation, formatting/vet, unit/race/fuzz tests | NOT RUN: Go compiler unavailable |
| Real Buildx/QEMU, final image smoke tests and completed upstream bundle export | NOT RUN: Docker unavailable; upstream fetches restricted |

The 90 offline Python/mock tests are not real container or issuance tests. Run
the Go regressions and actual image build in a suitable environment:

```bash
go test -C build -buildvcs=false -mod=vendor ./internal/licenseaudit ./internal/imagebuild
bash ci/update-license-bundle.sh
bash ci/trace-build.sh all
```

The full CI workflow retains the wider Go, race, fuzz, CLI and five-platform
smoke checks. No ACME orders are submitted by the offline fixtures or by these
build diagnostics.
