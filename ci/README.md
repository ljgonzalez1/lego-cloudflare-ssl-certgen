# CI and release checks

This snapshot is **v6.0.1-beta-1** (debug with traces and Delve probes). See the [event matrix](../docs/ci-policy.md) for the implemented routing and dependencies.

Static analysis finishes first. It is blocking for PRs to master and advisory for master pushes, version-branch activity, releases and prereleases. Advisory failures emit visible warnings; they do not suppress the offline tests. Failed, skipped or cancelled functional prerequisites never permit publication. Version branches run offline suites only after analysis. Master activity and releases also check README.md and DOCKERHUB.md versions, then run five parallel Docker smoke tests.

The analyzers cover Go, Python, Bash, POSIX sh, jq, awk, sed, Dockerfile, YAML, GitHub expressions, CSV, JSON and Go modules. Format inventory additionally checks TOML, environment templates, Markdown file targets, SVG and PNG integrity. Unknown file formats fail inventory instead of being silently ignored. ShellCheck analyzes standalone child-process fixtures, so intentionally deferred shell variables do not require global SC2016 suppression. The custom format checks validate project contracts; they are not a complete replacement for the Docker engine or external tool schemas.

## Run checks locally

Use Go 1.27.4, Python 3.12, PyYAML, Ruff (pinned in requirements-lint.txt), ShellCheck, jq, awk and sed. Native race tests also require a C compiler. From the repository root:

```sh
for language in go python bash posix jq awk sed dockerfile yaml actions csv json modules formats; do
    python3 -B ci/analyze.py "$language" || break
done
sh ci/tests/run.sh
python3 -B ci/test_workflows.py
python3 -B ci/test_publish.py
bash ci/test-dockerhub.sh
python3 -B ci/versions/test_tags.py
python3 -B ci/test_go_binary.py
```

Go unit/race/fuzz tests are retained in the workflow. Test fixtures are offline and never submit ACME orders. Each image smoke job builds the actual target image, exercises its entrypoint and lego, verifies retained program/license inventory and records image size plus a process-resource sample. The resource probe is not a measurement of live issuance or a simultaneous cgroup peak. See this snapshot's [validation report](validation-6.0.1-beta-1.md) for what was actually executed here.

## Release identity

`ci/versions/versions.csv` is the release source. Its final row identifies this snapshot. The CSV includes its earlier alpha/beta/stable history, including restored v4.2.10 through v4.2.16. The publisher passes the row's application, lego and Alpine values to Docker. The Dockerfile and application defaults match this snapshot, even when built without explicit arguments.

`versions.txt`, when retained in an early historical beta, is compatibility metadata. Newer snapshots remove it. Current release scripts read CSV. A published release/prerelease tag must match the complete application version, including its suffix. Alpha/alfa/beta aliases retain their suffix and do not replace latest. Tables are generated and committed before publication:

```sh
python3 -B ci/versions/tags_table.py
python3 -B ci/versions/check_version.py README.md --ref v6.0.1-beta-1
python3 -B ci/versions/check_version.py DOCKERHUB.md --ref v6.0.1-beta-1
```

No workflow automatically edits Markdown. A Git tag alone does not publish; the workflow subscribes to release.published, including prereleases. Only that event may run the publisher, after the mandatory offline suites and all five smokes pass. Pull requests cannot receive Docker Hub credentials. Configure DOCKERHUB_USERNAME, DOCKERHUB_PUSH_TOKEN and DOCKERHUB_DESCRIPTION_TOKEN as repository secrets for publication.

The publisher records the immutable Buildx index and all five platform digests. The tag/hash path waits 20 seconds, verifies the version tag and compares each architecture against its own Buildx digest. The description path updates DOCKERHUB.md, independently waits 20 seconds and verifies the remote description. Registry verification is a hard check. Published releases are queued to avoid racing rolling tags.

All builder/tool image versions are explicit: Go 1.27.4, Alpine 3.24.4, BuildKit 0.35.1, Dockerfile frontend 1.29.0 and binfmt qemu-v10.2.3-68. Debugger profiles pin Delve 1.27.2. The final production runtime receives no compiler, shell, source tree or debugger.
