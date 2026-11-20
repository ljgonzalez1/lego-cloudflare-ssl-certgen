# CI and release checks

This snapshot is **v6.0.1-beta-5** (debug with traces and Delve probes). See the [event matrix](../docs/ci-policy.md) for the implemented routing and dependencies.

Static analysis finishes first. It is blocking for PRs to master and advisory for master pushes, version-branch activity, releases and prereleases. Advisory failures emit visible warnings; they do not suppress the offline tests. Failed, skipped or cancelled functional prerequisites never permit publication. Version branches (including their initial push), master activity, pull requests and releases check README.md and DOCKERHUB.md against the committed CSV and versions.txt after static analysis; only master activity and releases also run five parallel Docker smoke tests. Branch names are checked for valid version syntax (2–4 numeric components and alpha/alfa/beta/audit/rc/build suffixes, with -, . or + before the suffix), but do not need to equal the release version in the documents. Docker image tags cannot contain +; a valid version branch is not automatically a publishable Docker tag.

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
python3 -B ci/test_license_trace.py
python3 -B ci/test_license_bundle.py
bash ci/test-dockerhub.sh
python3 -B ci/versions/test_tags.py
python3 -B ci/test_go_binary.py
```

Go unit/race/fuzz tests are retained in the workflow. Test fixtures are offline and never submit ACME orders. Each image smoke job builds the actual target image, exercises its entrypoint and lego, verifies retained program/license inventory and records image size plus a process-resource sample. The resource probe is not a measurement of live issuance or a simultaneous cgroup peak. See this snapshot's [validation report](validation-6.0.1-beta-5.md) for what was actually executed here.

## Release identity

`ci/versions/versions.csv` is the release source. Its final row identifies this snapshot. The CSV includes its earlier alpha/beta/stable history, including restored v4.2.10 through v4.2.16. The publisher passes the row's application, lego and Alpine values to Docker. The Dockerfile and application defaults match this snapshot, even when built without explicit arguments.

`versions.txt` defines the application, lego and Alpine values checked against the final CSV row. Version branch names and pull-request base names do not replace these values. The publisher itself still requires a matching release tag, including its suffix; this restriction is separate from the Markdown checks. Alpha/alfa/beta aliases retain their suffix and do not replace latest. Tables are generated and committed before publication:

```sh
python3 -B ci/versions/tags_table.py
python3 -B ci/versions/check_version.py README.md --ref v6.0.1-beta-5
python3 -B ci/versions/check_version.py DOCKERHUB.md --ref v6.0.1-beta-5
```

No workflow automatically edits Markdown. A Git tag alone does not publish; the workflow subscribes to release.published, including prereleases. Only that event may run the publisher, after the mandatory offline suites and all five smokes pass. Pull requests cannot receive Docker Hub credentials. Configure DOCKERHUB_USERNAME, DOCKERHUB_PUSH_TOKEN and DOCKERHUB_DESCRIPTION_TOKEN as repository secrets for publication.

The publisher records the immutable Buildx index and all five platform digests. The tag/hash path waits 20 seconds, verifies the version tag and compares each architecture against its own Buildx digest. The description path updates DOCKERHUB.md, independently waits 20 seconds and verifies the remote description. Registry verification is a hard check. Published releases are queued to avoid racing rolling tags.

All builder/tool image versions are explicit: Go 1.27.4, Alpine 3.24.4, BuildKit 0.35.1, Dockerfile frontend 1.29.0 and binfmt qemu-v10.2.3-68. Debugger profiles pin Delve 1.27.2. This debug beta retains application sources and a debugger only when its execution probe passes. The final runtime receives no compiler or shell.

## Local licenses and build traces

Prepare the notice bundle once, then build all five platforms without publishing:

```sh
bash ci/update-license-bundle.sh
bash ci/trace-build.sh all
```

The preparation saves original license texts and authenticated provenance in
`build/licenses/third-party/`. Commit `bundle.json` and `texts/` there. No third-party
executables or source archives are exported. Each target consumes this common
bundle with `--network=none` and checks it against its actual retained payload.
When the bundle is absent, Docker prepares it once in a shared native stage;
that fallback alone does not save the texts into the host project.

Logs stay in the project root: `trace-license-bundle.log`, `trace.log`, or
`trace-licenses.log`. All three are ignored by Git. A failed build still returns
its real status through `tee`. Override the builder with `CERTGEN_BUILDER`.
Full tracing rebuilds the complete target; `bash ci/trace-licenses.sh all` reruns
only the `licensed` stage. Neither diagnostic script pushes or loads an image.

See the [beta-5 guide and validation report](validation-6.0.1-beta-5.md) for the
checksum collision fix, local preparation, trace output and verification limits.
