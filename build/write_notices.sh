#!/bin/sh
# Install the license texts that must travel with the binaries in the image,
# and a NOTICE with each component's exact version and where its source is.
# Runs in the builder stage: write_notices.sh LICENSES_DIR RUNTIME_ROOT LEGO_VERSION
set -eu

licenses=$1
root=$2
lego_version=$3
target="$root/usr/share/licenses"
release_file=${ALPINE_RELEASE_FILE:-/etc/alpine-release}

# Installed version of an Alpine package, such as 1.2.6-r3. An empty root
# argument reads the builder's own packages.
package_version() {
    if [ -n "$1" ]; then
        apk --root "$1" info -v
    else
        apk info -v
    fi | while IFS= read -r entry; do
        case "$entry" in
            "$2"-[0-9]*)
                printf '%s\n' "${entry#"$2"-}"
                break
                ;;
        esac
    done
}

require_version() {
    version=$(package_version "$1" "$2")
    if [ -z "$version" ]; then
        printf 'Cannot find the installed version of %s.\n' "$2" >&2
        exit 1
    fi
    printf '%s\n' "$version"
}

musl=$(require_version "$root" musl)
su_exec=$(require_version "$root" su-exec)
bundle=$(require_version "$root" ca-certificates-bundle)
dash=$(require_version '' dash)
yaml=$(require_version '' yaml-static)
branch="$(cut -d. -f1,2 < "$release_file")-stable"
aports="https://gitlab.alpinelinux.org/alpine/aports/-/tree/$branch/main"

# Only components whose licenses ask for their text next to the binaries.
# tzdata is public domain, and the GCC runtime linked into certgen-core is
# covered by the GCC Runtime Library Exception, so neither needs a text.
mkdir -p "$target/musl" "$target/su-exec" "$target/dash" "$target/ca-certificates" "$target/libyaml"
cp "$licenses/musl.txt" "$target/musl/COPYRIGHT"
cp "$licenses/su-exec.txt" "$target/su-exec/LICENSE"
cp "$licenses/dash.txt" "$target/dash/COPYING"
cp "$licenses/GPL-2.0-or-later.txt" "$target/dash/GPL-2.0-or-later"
cp "$licenses/MPL-2.0.txt" "$target/ca-certificates/MPL-2.0"
cp "$licenses/libyaml.txt" "$target/libyaml/LICENSE"

cat > "$target/NOTICE" <<NOTICE
lego-cloudflare-certgen is MIT licensed. This image also contains the
following third-party software, each under its own license.

lego $lego_version: MIT, see lego/LICENSE
  Source: https://github.com/go-acme/lego/tree/$lego_version

musl $musl: MIT, see musl/COPYRIGHT
  Source: https://musl.libc.org/releases/musl-${musl%-r*}.tar.gz
  Alpine package: $aports/musl

dash $dash: BSD-3-Clause, see dash/COPYING. Its signal table is generated
by mksignames.c from GNU Bash under GPL-2.0-or-later, see dash/GPL-2.0-or-later.
  Source: http://gondor.apana.org.au/~herbert/dash/files/dash-${dash%-r*}.tar.gz
  Alpine package: $aports/dash

su-exec $su_exec: MIT, see su-exec/LICENSE
  Source: https://github.com/ncopa/su-exec/tree/v${su_exec%-r*}
  Alpine package: $aports/su-exec

ca-certificates-bundle $bundle: Mozilla's CA list under MPL-2.0, see
ca-certificates/MPL-2.0
  Source: $aports/ca-certificates

libyaml $yaml, statically linked into certgen-core: MIT, see libyaml/LICENSE
  Source: https://github.com/yaml/libyaml/tree/${yaml%-r*}
  Alpine package: $aports/yaml
NOTICE
find "$target" -type d -exec chmod 755 {} +
find "$target" -type f -exec chmod 644 {} +
