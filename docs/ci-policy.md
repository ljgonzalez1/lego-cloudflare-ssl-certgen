# CI event policy

| Event | Static analysis | Markdown versions | Offline and Go suites | Five image smokes | Publish and verify |
| --- | --- | --- | --- | --- | --- |
| Branch creation or deletion | None | None | None | None | None |
| Other branch push/merge | None | None | None | None | None |
| Version branch push/merge or PR to version branch | Advisory | None | Required | None | None |
| PR to master | Required | Required, after static | Required | Required | None |
| Master edit/push/merge | Advisory | Required, after static | Required | Required | None |
| Published release or prerelease | Advisory | Required, after static | Required | Required | Required |

Static jobs explicitly cover Go, Python, Bash, POSIX sh, jq, awk, sed, Dockerfile, YAML, Actions expressions, CSV, JSON and Go modules. Format inventory also checks TOML, environment templates, Markdown links and image integrity. Embedded jq/awk/sed filters use their real parsers. Go vet covers ARMv6, ARMv7, ARM64, i386 and AMD64. The format validator checks this project's contracts; real Docker builds and image smoke tests remain separate hard gates.

All static jobs finish before Markdown jobs. Advisory diagnostics remain visible in warnings and the job summary. Failed, cancelled or skipped functional checks never authorize publication. Both Docker Hub propagation waits and all five architecture digest comparisons remain independent hard checks. The pipeline tests source, queues releases for publication, and never edits Markdown automatically. Image-only badge pushes are excluded by an exact allowlist.

These fixes are backported to the supplied older snapshots. Beta-7 records the complete event policy for subsequent releases. No remote branch or release is created by this source delivery.

Builder pins checked on 2026-11-20: Go 1.27.4, Alpine 3.24.4, BuildKit 0.35.1, Dockerfile frontend 1.29.0, binfmt qemu-v10.2.3-68; debug profiles use Delve 1.27.2. Pins are explicit and are not floating latest tags. Updating a pin is a reviewed source change, not an unbounded automatic update during a build.
