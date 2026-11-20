# Original third-party license evidence

This directory is for the **original upstream notice texts and provenance**,
not third-party executables or an assertion of ownership over third-party code.
The authors, copyright notices and original license terms remain unchanged.
Project MIT terms do not replace third-party terms.

Prepare or refresh the complete authenticated set from the project root:

```bash
bash ci/update-license-bundle.sh
```

This creates `bundle.json` and content-addressed `texts/*.txt` here. Include those
files in Git. They contain module versions and `h1` sums, original notice names,
SHA-256 fingerprints, exact Alpine source recipes and source archive provenance.
Only notice texts and metadata are exported. Downloaded binaries, module ZIPs,
Go toolchain archives and compiled tools stay outside this directory.

The delivered beta-5 archive contains the exporter and existing project, YAML,
MIT, Apache and MPL texts, **but not a completed `bundle.json`**: the upstream
archives could not be fetched in the editing environment. Run the command above
once before committing the new notice set. Do not substitute guessed notices.

If no local bundle exists, an ordinary Docker build prepares it once in a shared
native BuildKit stage. This fallback is cached by BuildKit but does not write into
the host project. The command above makes the texts persistent and reviewable.
Each target's license audit uses only the resulting local texts with the network
disabled. Stale versions, altered notices, mismatched module sums or changed
Alpine payloads fail the build and require an explicit refresh.
