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

Beta 6 **includes the completed bundle**: 239 third-party components and 332
original notice texts for Alpine 3.24.4, Go 1.27.4, lego v4.34.0 and Delve
v1.27.2. It was produced by this exporter from the same authenticated sources;
the source bindings and per-component document counts match the beta-5 export
log. Re-running the command above on unchanged inputs must leave `git status`
clean. Do not edit these files by hand or substitute guessed notices.
`.gitattributes` keeps them byte-exact because each text is SHA-256 bound.

If no local bundle exists, an ordinary Docker build prepares it once in a shared
native BuildKit stage. This fallback is cached by BuildKit but does not write into
the host project. The command above makes the texts persistent and reviewable.
Each target's license audit uses only the resulting local texts with the network
disabled. Stale versions, altered notices, mismatched module sums or changed
Alpine payloads fail the build and require an explicit refresh.
