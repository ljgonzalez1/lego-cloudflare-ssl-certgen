# License inventory and verification

The original project code remains MIT licensed, with its copyright and grant preserved in LICENSE. The corrected snapshots collect original third-party notices by actual retained program, dependency, compiler and data version. The original alpha-1 ZIP is an unchanged archival reference.

## Publishing under MIT

You may keep MIT for your original code and distribute an image containing it alongside other components. Their independent licenses remain applicable: this does not relicense Go, lego's dependencies, CA data, fonts or trademarks exclusively under MIT. Retain their full copyright, LICENSE, NOTICE, patent and source-availability requirements. Modified MPL-covered files retain MPL source rights; the collector records corresponding immutable source recipes/archives. Apache/BSD/MIT notices stay with their respective components. The image label references the complete runtime inventory alongside the first-party MIT grant.

Primary references: https://opensource.org/license/mit ; https://www.mozilla.org/en-US/MPL/2.0/FAQ/ ; https://www.apache.org/licenses/LICENSE-2.0 . This does not certify permissions for artwork/trademarks whose grants were not supplied, or the compatibility of an unknown future dependency. Missing upstream evidence fails the build rather than being replaced by the project license.

## What is checked

The native, standard-library-only `build/internal/licenseaudit` helper scans the **assembled runtime after its execution probes**, not a hard-coded dependency list. It reads each retained ELF binary's Go build information, including module replacements and its actual Go compiler version. The application version and a verified official lego archive provide the two explicit mappings for upstream `(devel)` main-module metadata. Unversioned unknown programs or unmapped payload files stop the build. Module source versions and available embedded checksums must match. Public module archives are authenticated through Go's checksum mechanism and their actual ZIP contents are rehashed; `GOSUMDB=off` is refused for upstream collection.

The collector preserves upstream LICENSE, COPYING, NOTICE, COPYRIGHT, PATENTS, AUTHORS, license directories and distinct leading source copyright/license headers, including vendored/nested notices. It includes the full Apache 2.0 terms referenced by YAML and other modules and the full MPL 2.0 terms for Mozilla CA data. It conservatively retains a module/toolchain's notice files, including some platform notices that might not be linked; it does not discard terms based on an uncertain inference.

CA data and beta timezone files are matched to the actual installed Alpine payload. The installed package's version, declared license and exact aports commit identify its recipe and checksum-bound corresponding source archives. Aports packaging notices are retained too. Beta's retained Alpine release metadata has its own package attribution. Production uses the Go SDK's `zoneinfo.zip`, matched by SHA-256 to that exact SDK; its public-domain provenance replaces beta's Alpine timezone attribution. The native compiler's license is not assumed to cover lego compiled with a different Go release.

Notice collection runs again when executable contents/versions change. A retained Delve receives its own notices and transitive dependencies; when a probe removes Delve, its unused components and notices do not survive. Production includes no Delve. Source downloads, module caches, Go tools and the verifier remain in build stages.

## Locations in each final image

| Path | Contents |
| --- | --- |
| `/usr/share/licenses/NOTICE` | Readable per-component document/source index and provenance |
| `/usr/share/licenses/manifest.json` | Actual program/module versions, provenance, payload and notice SHA-256 values, file owners and notice aliases |
| `/usr/share/licenses/components/<component-hash>/...` | Original relative notice names, through links to exact retained text |
| `/usr/share/licenses/texts/<sha256>.txt` | One verbatim copy of each distinct notice body |
| `/usr/share/licenses/certgen/LICENSE` | Project MIT copyright and terms |
| `/usr/share/licenses/lego/LICENSE` | Upstream lego main-module license; dependency notices are listed separately |
| `/usr/share/licenses/Go/LICENSE` | Application compiler license; other compiler versions are listed separately |
| `/usr/share/licenses/go-yaml/LICENSE` and `NOTICE` | Vendored YAML notices, with full Apache terms in its component |
| `/usr/share/licenses/ca-certificates/MPL-2.0` | Full MPL terms, when the actual CA package declares MPL |

Text deduplication preserves every document-to-component mapping and original terms. It does not replace different copyright notices with a shared generic license. The OCI `LicenseRef-RuntimeInventory` refers to this index and manifest, alongside the first-party MIT license. It is a project-defined reference, not a new license grant or an assertion that every bundled component uses MIT.

The local verifier rejects missing or modified texts, changed links, incorrect module/version/compiler mappings, duplicate or orphaned records, changed executable sets and unlisted payload files. It rechecks actual binary metadata. Docker-injected hosts/hostname/resolver files and `.dockerenv` are the only ignored payload additions. It verifies internal consistency, not a cryptographic signature against an external attacker who can replace both an image and its inventory; authenticate the published image digest separately.

## CI and local use

- `python3 -B ci/check_licenses.py` verifies source license copies, asset hashes and embedded font notice copies.
- Every Docker build runs `certgen-build licenses /runtime /licenses`, then `verify-licenses /runtime` with networking disabled.
- Every architecture's smoke test exports the **final image**, safely extracts it and invokes the native verifier. No target Go compiler or license checker is added to scratch.
- Missing/unavailable upstream source evidence or an unsupported recipe/version fails the build. Fix the source mapping and notices; do not disable the check or substitute the project's license.
- Existing Go unit, race, fuzz, compiled CLI, shell, Python, version/tag, workflow and five-architecture checks remain required.

For an already assembled root, run `go build -C build -mod=vendor -trimpath -o /tmp/certgen-build ./cmd/certgen-build`, then `/tmp/certgen-build verify-licenses /path/to/root`. Collection is a native Alpine build-stage operation requiring the installed package database, matching SDK, `APP_VERSION`, `LEGO_VERSION`, vendored source working directory and network access to authenticated upstream sources. Verification itself needs neither network nor source downloads.

## Artwork, fonts and scope

The supplied PNG remains the displayed banner; the SVG files remain source assets. Their bytes are unchanged. `licenses/assets.json` records all supplied assets and their scope. The full existing font metadata from each SVG is preserved verbatim in `licenses/assets/*-fonts.txt`, including Liberation Fonts' SIL OFL and other copyright notices embedded by the authoring tools. None of these assets is copied into scratch.

No separate artwork grant or third-party trademark permission was supplied. That unresolved status is recorded explicitly: the project's MIT license is not used to claim ownership of the Cloudflare mark or to invent a license for supplied illustrations. The automated source check reports this open rights review separately from runtime software evidence. A passed integrity check does not certify these permissions.

Automated collection and integrity checks are not a legal compatibility opinion. They preserve attributable evidence and source availability links; review upstream obligations and any newly introduced/nonpermissive terms before distributing a changed program. No claim is made that all modules share one SPDX identifier.

## Primary references

- Go module authentication and ZIP rules: https://go.dev/ref/mod
- Go license: https://go.dev/LICENSE
- Apache 2.0 full terms: https://www.apache.org/licenses/LICENSE-2.0.txt
- MPL 2.0 source availability and notices: https://www.mozilla.org/MPL/2.0/
- Actual Alpine CA package licensing (not a generic curl bundle assumption): https://pkgs.alpinelinux.org/package/v3.24/main/x86_64/ca-certificates-bundle
- IANA timezone database: https://www.iana.org/time-zones
- Font notices: metadata embedded in the supplied SVG files, retained verbatim alongside the source
- Cloudflare brand guidance: https://www.cloudflare.com/trademark/

See the release's `ci/validation-6.0.1-beta-1.md` report for what was actually executed locally and what still requires Docker/network access. Source and fixture tests must not be presented as a completed audit of a real upstream lego image.
