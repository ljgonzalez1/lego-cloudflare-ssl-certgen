#!/bin/sh
# Offline regression tests for the C++ program, POSIX scripts and downloader.
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf -- "$work"' 0
mkdir "$work/bin"
entry="$root/build/entrypoint.sh"
download="$root/build/download_lego.sh"

# entrypoint.sh executes run_lego.sh directly, so Git must keep its executable bit.
for script in "$root"/build/*.sh; do
    if [ ! -x "$script" ]; then
        printf 'Bail out! %s is not executable. Run: git update-index --chmod=+x build/*.sh\n' \
            "${script#"$root"/}" >&2
        exit 1
    fi
done

# Allow a precompiled executable when testing with a different Alpine toolchain.
if [ -n "${CERTGEN_CORE:-}" ]; then
    cp -- "$CERTGEN_CORE" "$work/certgen-core"
else
    "${CXX:-c++}" -std=c++17 -O2 -Wall -Wextra -Wpedantic \
        "$root"/build/src/*.cpp -lyaml -o "$work/certgen-core"
fi
export CERTGEN_TEST_CORE="$work/certgen-core"

cat > "$work/bin/certgen-core" <<'SH'
#!/bin/sh
if [ "$1" = identity ]; then
    printf '%s:%s\n' "${TEST_CALLER_UID:-1000}" "${TEST_CALLER_GID:-1000}"
    exit 0
fi
exec "$CERTGEN_TEST_CORE" "$@"
SH
cat > "$work/bin/su-exec" <<'SH'
#!/bin/sh
printf '%s\n' "$1" >> "$TEST_SU_LOG"
shift
exec "$@"
SH
cat > "$work/bin/lego" <<'SH'
#!/bin/sh
set -eu
{
    printf 'order\ntoken=%s\ntoken_file=%s\nlegacy_key=%s\ncname=%s\npropagation=%s\n' \
        "${CF_DNS_API_TOKEN:-}" "${CF_DNS_API_TOKEN_FILE:-}" "${CLOUDFLARE_API_KEY:-}" \
        "$LEGO_DISABLE_CNAME_SUPPORT" "$CLOUDFLARE_PROPAGATION_TIMEOUT"
    printf '%s\n' "$@"
} >> "$TEST_LEGO_LOG"
for argument do
    case "$argument" in
        fail.example.org) exit 17 ;;
        ratelimited.example.org)
            printf '[WARN] retry: acme: error: 429 :: urn:ietf:params:acme:error:rateLimited :: too many certificates\n' >&2
            exec sleep 30
            ;;
        hang.example.org) exec sleep 30 ;;
    esac
done
SH
cat > "$work/bin/curl" <<'SH'
#!/bin/sh
set -eu
while [ "$#" -gt 1 ]; do
    case "$1" in
        -o) output=$2; shift ;;
        -fsSL|--retry|3) ;;
        *) exit 90 ;;
    esac
    shift
done
printf '%s\n' "$1" >> "$TEST_CURL_LOG"
cp -- "$TEST_FIXTURES/${1##*/}" "$output"
SH
cat > "$work/bin/forbidden-dns" <<'SH'
#!/bin/sh
printf '%s\n' "$0" >> "$TEST_DNS_LOG"
exit 91
SH
chmod 755 "$work/bin/"*
for command in dig host nslookup getent; do
    ln -s forbidden-dns "$work/bin/$command"
done
export PATH="$work/bin:$PATH"
unset CLOUDFLARE_API_KEY CF_DNS_API_TOKEN CLOUDFLARE_API_TOKEN LEGO_DISABLE_CNAME_SUPPORT ORDER_TIMEOUT_SECONDS \
    CERT_UID CERT_GID PROPAGATION_SECONDS DNS_RESOLVERS TEST_CALLER_UID TEST_CALLER_GID \
    LEGO_TARBALL LEGO_URL LEGO_SHA256 TARGETARCH TARGETVARIANT
export UID=1000 GID=1000 EMAIL=test@example.com CLOUDFLARE_API_TOKEN=fixture-token \
    ACCEPT_LEGO_TOS=true PRODUCTION=false TZ=Etc/UTC LEGO_VERSION=v4.34.0

file="$work/domains.yaml"
output="$work/output"
stdout="$work/stdout"
stderr="$work/stderr"
export TEST_FIXTURES="$work/fixtures" TEST_SU_LOG="$work/su.log" \
    TEST_LEGO_LOG="$work/lego.log" TEST_CURL_LOG="$work/curl.log" TEST_DNS_LOG="$work/dns.log"

expect_status() {
    expected=$1
    shift
    actual=0
    "$@" > "$stdout" 2> "$stderr" || actual=$?
    if [ "$actual" -ne "$expected" ]; then
        printf 'Expected status %s, got %s from %s\n' "$expected" "$actual" "$*"
        cat -- "$stdout" "$stderr"
        return 1
    fi
}

assert_line() {
    if ! grep -Fx -- "$1" "$2"; then
        printf 'Missing line <%s> in %s\n' "$1" "$2"
        cat -- "$2"
        return 1
    fi
}

assert_contains() {
    if ! grep -F -- "$1" "$2"; then
        printf 'Missing text <%s> in %s\n' "$1" "$2"
        cat -- "$2"
        return 1
    fi
}

assert_absent() {
    if grep -F -- "$1" "$2"; then
        printf 'Unexpected text <%s> in %s\n' "$1" "$2"
        return 1
    fi
}

reset_case() {
    rm -rf -- "$output" "$work/downloaded"
    mkdir -- "$output"
    : > "$TEST_SU_LOG"
    : > "$TEST_LEGO_LOG"
    : > "$TEST_CURL_LOG"
    : > "$TEST_DNS_LOG"
    cat > "$file" <<'YAML'
first:
  - a.example.com
  - v6.example.com
  - alias.example.com
  - '*.nested.example.com'
  - planned.example.com
second: [second.example.org, '*.example.org']
YAML
}

cli() {
    /bin/sh "$entry" --config "$file" --output "$output" "$@"
}

read_domains() {
    certgen-core domains "$file" "$@"
}

invalid_yaml() {
    printf '%s\n' "$1" > "$file"
    shift
    expected_error=$1
    shift
    expect_status 2 read_domains "$@"
    [ ! -s "$stdout" ]
    assert_contains "$expected_error" "$stderr"
}

yaml_order() {
    expect_status 0 read_domains second first second
    printf 'first\ta.example.com,v6.example.com,alias.example.com,*.nested.example.com,planned.example.com\nsecond\tsecond.example.org,*.example.org\n' > "$work/expected"
    diff -u "$work/expected" "$stdout"
}

yaml_aliases_and_tags() {
    cat > "$file" <<'YAML'
!!map
!!str yes: &names !!seq [&name !!str example.com, '*.example.com']
"42": *names
third: [*name, ! api.example.org, '1.0e-2']
YAML
    expect_status 0 read_domains
    printf 'yes\texample.com,*.example.com\n42\texample.com,*.example.com\nthird\texample.com,api.example.org,1.0e-2\n' > "$work/expected"
    diff -u "$work/expected" "$stdout"
}

yaml_io_errors() {
    rm -- "$file"
    expect_status 2 read_domains
    assert_contains 'could not open configuration' "$stderr"
    invalid_yaml 'first: ["example.com"' 'YAML line'
    invalid_yaml "$(printf 'one: [example.com]\n---\ntwo: [example.org]')" 'single YAML document'
}

yaml_wrong_shapes() {
    invalid_yaml '' 'nonempty mapping'
    invalid_yaml '[]' 'nonempty mapping'
    invalid_yaml '{}' 'nonempty mapping'
    invalid_yaml 'first: example.com' 'nonempty domain list'
    invalid_yaml 'first: []' 'nonempty domain list'
    invalid_yaml 'first: [[example.com]]' 'invalid DNS name'
    invalid_yaml 'first: {example.com: true}' 'nonempty domain list'
}

yaml_bad_groups() {
    invalid_yaml 'bad/path: [example.com]' 'invalid certificate folder'
    invalid_yaml 'yes: [example.com]' 'invalid certificate folder'
    invalid_yaml '42: [example.com]' 'invalid certificate folder'
    invalid_yaml '2026-11-17: [example.com]' 'invalid certificate folder'
    invalid_yaml "$(printf 'same: [example.com]\nsame: [example.org]')" 'duplicate certificate folder'
    invalid_yaml 'same: &self [*self]' 'recursive YAML alias'
    invalid_yaml 'same: *not_defined' 'undefined or recursive YAML alias'
}

yaml_bad_names() {
    for name in 'www.*.example.com' '*.*.example.com' 'single' '127.0.0.1' \
        'a.-example.com' 'a..com' 'a.example.com.' 'exa_mple.com' '-a.com' \
        'a.example.com/path' 'café.example.com' 'a.example.com,extra' 'a.example.com --debug'; do
        invalid_yaml "first: ['$name']" 'invalid DNS name'
    done
    invalid_yaml 'first: [example.com, EXAMPLE.COM]' 'duplicate DNS name'
    invalid_yaml 'first: [true]' 'invalid DNS name'
    invalid_yaml 'first: [1.0e-2]' 'invalid DNS name'
    invalid_yaml 'first: [!!int 111]' 'unexpected YAML type'
    invalid_yaml 'first: [!!python/object:example.Custom example.com]' 'unexpected YAML type'
}

yaml_length_limit() {
    label=$(printf '%063d' 0)
    # Four labels of length 63, 63, 63, 61 and the three dots make 253.
    printf 'first: [%s.%s.%s.%060da]\n' "$label" "$label" "$label" 0 > "$file"
    expect_status 0 read_domains
    invalid_yaml "first: ['$label.$label.$label.${label}a']" 'invalid DNS name'
    invalid_yaml "first: ['${label}a.example.com']" 'invalid DNS name'
}

yaml_name_limit() {
    {
        printf 'limit:\n'
        index=1
        while [ "$index" -le 100 ]; do
            printf '  - n%s.example.com\n' "$index"
            index=$((index + 1))
        done
    } > "$file"
    expect_status 0 read_domains
    assert_contains 'n100.example.com' "$stdout"
    printf '  - n101.example.com\n' >> "$file"
    expect_status 2 read_domains
    assert_contains '100-name limit' "$stderr"
}

yaml_unknown_selection() {
    expect_status 2 read_domains missing second missing other
    assert_contains 'unknown certificate folder(s): missing, other' "$stderr"
    [ ! -s "$stdout" ]
}

cli_help() {
    UID=invalid expect_status 0 cli --help
    assert_contains 'Usage: certgen [--list | --dry-run]' "$stdout"
}

cli_argument_errors() {
    expect_status 2 cli --list --dry-run
    assert_contains 'mutually exclusive' "$stderr"
    expect_status 2 cli --staging --production
    assert_contains 'mutually exclusive' "$stderr"
    expect_status 2 cli --invalid
    assert_contains 'Unknown argument' "$stderr"
    expect_status 2 cli --only second --config
    assert_contains '--config requires a value' "$stderr"
    expect_status 2 cli --only --list
    assert_contains '--only requires a value' "$stderr"
}

cli_listing_and_preview() {
    EMAIL= ACCEPT_LEGO_TOS=false CLOUDFLARE_API_TOKEN= expect_status 0 cli --only second --list --only first
    printf 'first: 5 domains\nsecond: 2 domains\n' > "$work/expected"
    diff -u "$work/expected" "$stdout"
    EMAIL= CLOUDFLARE_API_TOKEN= TZ=America/Santiago expect_status 0 cli --only first --production --dry-run
    assert_contains 'https://acme-v02.api.letsencrypt.org/directory' "$stdout"
    assert_contains '_GMT-' "$stdout"
    assert_contains '*.nested.example.com' "$stdout"
    [ ! -s "$TEST_LEGO_LOG" ]
    [ -z "$(ls -A -- "$output")" ]
}

cli_mode_overrides() {
    PRODUCTION=true expect_status 0 cli --dry-run --staging
    assert_contains 'https://acme-staging-v02.api.letsencrypt.org/directory' "$stdout"
    PRODUCTION=invalid expect_status 0 cli --dry-run --production
    assert_contains 'https://acme-v02.api.letsencrypt.org/directory' "$stdout"
}

cli_identities() {
    UID=0 expect_status 2 cli --list
    assert_contains 'UID must be an integer' "$stderr"
    UID=4294967295 expect_status 2 cli --list
    GID=bad expect_status 2 cli --list
    assert_contains 'GID must be an integer' "$stderr"
    TEST_CALLER_GID=1011 expect_status 1 cli --list
    assert_contains 'must match the configured UID/GID' "$stderr"
    UID=0000001000 GID=001000 expect_status 0 cli --list
    TEST_CALLER_UID=0 expect_status 0 cli --list --only second
    assert_line '1000:1000' "$TEST_SU_LOG"
}

cli_yaml_before_issue() {
    printf 'broken: ["a b.com"]\n' >> "$file"
    expect_status 2 cli --only first
    assert_contains 'invalid DNS name' "$stderr"
    [ ! -s "$TEST_LEGO_LOG" ]
    [ -z "$(ls -A -- "$output")" ]
}

cli_dns_types() {
    touch "$work/a.nested.example.com"
    (cd "$work" && expect_status 0 cli --only first --only second)
    for name in a.example.com v6.example.com alias.example.com '*.nested.example.com' planned.example.com; do
        assert_line "$name" "$TEST_LEGO_LOG"
    done
    assert_absent 'a.nested.example.com' "$TEST_LEGO_LOG"
    assert_line 'cloudflare' "$TEST_LEGO_LOG"
    assert_line 'cname=true' "$TEST_LEGO_LOG"
    assert_line 'propagation=360' "$TEST_LEGO_LOG"
    assert_line 'token=fixture-token' "$TEST_LEGO_LOG"
    assert_line 'legacy_key=' "$TEST_LEGO_LOG"
    assert_line 'https://acme-staging-v02.api.letsencrypt.org/directory' "$TEST_LEGO_LOG"
    [ "$(grep -c '^order$' "$TEST_LEGO_LOG")" -eq 2 ]
    [ "$(grep -c '^--domains$' "$TEST_LEGO_LOG")" -eq 7 ]
    [ ! -s "$TEST_DNS_LOG" ]
    assert_contains 'Certificates: 2 attempted, 2 issued, 0 failed, 0 skipped.' "$stdout"
}

cli_delegation_and_resolvers() {
    CLOUDFLARE_API_KEY=legacy-token CF_DNS_API_TOKEN=other-token \
        LEGO_DISABLE_CNAME_SUPPORT=FaLsE PROPAGATION_SECONDS=00060 \
        DNS_RESOLVERS=' 1.1.1.1:53, [2606:4700:4700::1111]:53 ' \
        expect_status 0 cli --production --only first
    assert_line 'token=legacy-token' "$TEST_LEGO_LOG"
    assert_line 'legacy_key=' "$TEST_LEGO_LOG"
    assert_line 'cname=false' "$TEST_LEGO_LOG"
    assert_line 'propagation=60' "$TEST_LEGO_LOG"
    assert_line 'https://acme-v02.api.letsencrypt.org/directory' "$TEST_LEGO_LOG"
    assert_line '1.1.1.1:53' "$TEST_LEGO_LOG"
    assert_line '[2606:4700:4700::1111]:53' "$TEST_LEGO_LOG"
    awk '/^--dns.resolvers$/{getline;print}' "$TEST_LEGO_LOG" > "$work/resolvers"
    printf '1.1.1.1:53\n[2606:4700:4700::1111]:53\n' > "$work/expected"
    diff -u "$work/expected" "$work/resolvers"
}

cli_bad_settings() {
    EMAIL=broken ACCEPT_LEGO_TOS=false PRODUCTION=maybe PROPAGATION_SECONDS=2147483648 \
        TZ=../Etc/UTC DNS_RESOLVERS='1.1.1.1:0,1.0.0.1:65536' expect_status 2 cli
    for setting in EMAIL ACCEPT_LEGO_TOS PRODUCTION PROPAGATION_SECONDS TZ DNS_RESOLVERS; do
        assert_contains "$setting:" "$stderr"
    done
    assert_contains '0 attempted, 0 issued, 0 failed, 2 skipped' "$stdout"
    [ ! -s "$TEST_LEGO_LOG" ]
}

cli_rejects_invalid_token() {
    CLOUDFLARE_API_TOKEN='sensitive.invalid.token' expect_status 2 cli
    assert_contains 'Provide a valid Cloudflare DNS API token' "$stderr"
    assert_absent 'sensitive.invalid.token' "$stderr"
    assert_absent 'sensitive.invalid.token' "$stdout"
    assert_contains '2 skipped' "$stdout"
    [ ! -s "$TEST_LEGO_LOG" ]
}

cli_routes_secret_file() {
    printf 'fixture-secret\n\n' > "$work/cloudflare-secret"
    CLOUDFLARE_API_TOKEN= expect_status 0 /bin/sh -eu -c '
        . "$1"
        VALIDATED_PROPAGATION=60
        configure_cloudflare "$2"
        [ -z "${CF_DNS_API_TOKEN:-}" ]
        [ "$CF_DNS_API_TOKEN_FILE" = "$2" ]
        [ "$LEGO_DISABLE_CNAME_SUPPORT" = true ]
        [ "$CLOUDFLARE_PROPAGATION_TIMEOUT" = 60 ]
    ' sh "$root/build/validate_env.sh" "$work/cloudflare-secret"
    [ ! -s "$stdout" ] && [ ! -s "$stderr" ]
}

cli_rejects_invalid_delegation() {
    LEGO_DISABLE_CNAME_SUPPORT=invalid expect_status 2 cli
    assert_contains 'LEGO_DISABLE_CNAME_SUPPORT must be true or false' "$stderr"
    assert_contains '2 skipped' "$stdout"
    [ ! -s "$TEST_LEGO_LOG" ]
}

cli_missing_output() {
    rmdir -- "$output"
    expect_status 1 cli
    assert_contains 'Output directory must be writable' "$stderr"
    assert_contains '2 skipped' "$stdout"
    [ ! -s "$TEST_LEGO_LOG" ]
}

cli_group_results() {
    cat >> "$file" <<'YAML'
bad: [fail.example.org, '*.apps.example.org']
blocked: [blocked.example.org]
last: [last.example.org]
YAML
    ln -s "$work" "$output/blocked"
    expect_status 1 cli --only last --only blocked --only bad --only first
    assert_contains 'bad: lego exited with code 17' "$stderr"
    assert_contains 'Cannot use certificate folder:' "$stderr"
    assert_line 'last.example.org' "$TEST_LEGO_LOG"
    assert_absent 'blocked.example.org' "$TEST_LEGO_LOG"
    assert_contains 'Certificates: 3 attempted, 2 issued, 1 failed, 1 skipped.' "$stdout"
    awk -F '|' '/^[|] (first|bad|blocked|last|TOTAL) / {
        for (n = 2; n < NF; n++) { gsub(/^ +| +$/, "", $n); printf "%s:", $n }
        print ""
    }' "$stdout" > "$work/rows"
    printf 'first:5:5:0:0:ISSUED:\nbad:2:0:2:0:FAILED:\nblocked:0:0:0:1:SKIPPED:\nlast:1:1:0:0:ISSUED:\nTOTAL:8:6:2:1::\n' > "$work/expected"
    diff -u "$work/expected" "$work/rows"
}

cli_stops_on_rate_limit() {
    printf 'limited: [ratelimited.example.org]\n' >> "$file"
    started=$(date +%s)
    expect_status 1 cli --only limited --only first
    [ $(($(date +%s) - started)) -lt 10 ]
    assert_contains 'urn:ietf:params:acme:error:rateLimited' "$stderr"
    assert_contains "limited: Let's Encrypt rate limit reached" "$stderr"
    assert_contains 'Certificates: 2 attempted, 1 issued, 1 failed, 0 skipped.' "$stdout"
}

cli_stops_at_time_limit() {
    printf 'stuck: [hang.example.org]\n' >> "$file"
    started=$(date +%s)
    ORDER_TIMEOUT_SECONDS=1 expect_status 1 cli --only stuck
    [ $(($(date +%s) - started)) -lt 10 ]
    assert_contains 'stuck: lego stopped after the 1s time limit' "$stderr"
    assert_contains 'Certificates: 1 attempted, 0 issued, 1 failed, 0 skipped.' "$stdout"
}

cli_time_limit_settings() {
    expect_status 0 cli --dry-run --only first
    assert_contains 'Time limit: 3600s' "$stdout"
    PROPAGATION_SECONDS=1000 expect_status 0 cli --dry-run --only first
    assert_contains 'Time limit: 5600s' "$stdout"
    ORDER_TIMEOUT_SECONDS=AUTO PROPAGATION_SECONDS=1000 expect_status 0 cli --dry-run --only second
    assert_contains 'Time limit: 3600s' "$stdout"
    ORDER_TIMEOUT_SECONDS=0 expect_status 0 cli --dry-run --only first
    assert_contains 'Time limit: none' "$stdout"
    ORDER_TIMEOUT_SECONDS=0090 expect_status 0 cli --dry-run --only first
    assert_contains 'Time limit: 90s' "$stdout"
    for value in -1 1h 2147483648; do
        ORDER_TIMEOUT_SECONDS=$value expect_status 2 cli
        assert_contains 'ORDER_TIMEOUT_SECONDS:' "$stderr"
    done
    [ ! -s "$TEST_LEGO_LOG" ]
}

cli_retries_and_timestamps() {
    cli --only first > "$work/concurrent-first" 2>&1 &
    first_pid=$!
    cli --only first > "$work/concurrent-second" 2>&1 &
    second_pid=$!
    wait "$first_pid"
    wait "$second_pid"
    [ "$(find "$output/first" -mindepth 1 -maxdepth 1 -type d | wc -l)" -eq 2 ]
    assert_contains '_GMT+0/requested_' "$TEST_LEGO_LOG"
    [ "$(find "$output/first" -mindepth 2 -maxdepth 2 -type d | wc -l)" -eq 2 ]
}

core_runtime() {
    expect_status 0 "$CERTGEN_TEST_CORE" identity
    assert_line "$(id -u):$(id -g)" "$stdout"
    expect_status 2 "$CERTGEN_TEST_CORE" own-output "$output" 0 1000
    assert_contains 'invalid UID or GID' "$stderr"
    expect_status 0 "$CERTGEN_TEST_CORE" output-path dry-run "$output" 'preview'
    assert_contains '_GMT+0/requested_' "$stdout"
    [ ! -e "$output/preview" ]
}

cli_minimal_runtime() {
    mkdir -- "$work/minimal-path"
    ln -s "$work/bin/certgen-core" "$work/minimal-path/certgen-core"
    ln -s "$work/bin/lego" "$work/minimal-path/lego"
    PATH="$work/minimal-path" expect_status 0 cli --only first
    assert_contains 'Certificates: 1 attempted, 1 issued' "$stdout"
}

summary_plain() {
    printf 'example-com\t4\t0\nexample-org\t4\t1\nnext\t1\t2\n' > "$work/report"
    expect_status 0 certgen-core summary < "$work/report"
    assert_contains 'Certificate' "$stdout"
    [ ! -s "$stderr" ]
    assert_contains 'Certificates: 2 attempted, 1 issued, 1 failed, 1 skipped.' "$stdout"
    assert_absent "$(printf '\033')" "$stdout"
}

summary_rejects_bad_input() {
    for input in 'missing' "$(printf 'x\t0\t0')" "$(printf 'x\t101\t0')" \
        "$(printf 'x\t1\t3')" "$(printf 'x\tbogus\t0')" "$(printf 'bad/name\t1\t0')"; do
        printf '%s\n' "$input" > "$work/report"
        expect_status 2 certgen-core summary < "$work/report"
        [ ! -s "$stdout" ]
    done
}

summary_tty_width() {
    width=$1
    label=$2
    report="$work/report"
    printf 'very_long_certificate_label_that_must_wrap_on_small_terminals\t100\t0\nexample-org\t55\t1\nskipped\t13\t2\n' > "$report"
    export CERTGEN_TEST_REPORT="$report"
    COLUMNS="$width" script -q -e -c '"$CERTGEN_TEST_CORE" summary < "$CERTGEN_TEST_REPORT"' /dev/null > "$stdout"
    assert_contains "$(printf '\033[32m')" "$stdout"
    assert_contains "$(printf '\033[31m')" "$stdout"
    assert_contains "$(printf '\033[33m')" "$stdout"
    sed 's/\x1b\[[0-9;]*m//g; s/\r$//' "$stdout" > "$work/no-color"
    assert_contains "$label" "$work/no-color"
    awk -v columns="$width" '
        /^[+|]/ {
            if (!row++) width = length($0)
            if (length($0) != width || length($0) > columns) exit 1
            if (substr($0, length($0), 1) != substr($0, 1, 1)) exit 1
        }
        END { if (row < 5) exit 1 }
    ' "$work/no-color"
}

archive_fixtures() {
    mkdir -- "$TEST_FIXTURES" "$work/archive"
    printf '#!/bin/sh\nprintf "lego fixture\\n"\n' > "$work/archive/lego"
    printf 'Test license\n' > "$work/archive/LICENSE"
    for architecture in amd64 386 armv7 arm64; do
        archive="lego_v4.34.0_linux_$architecture.tar.gz"
        tar -czf "$TEST_FIXTURES/$archive" -C "$work/archive" lego LICENSE
    done
    (
        cd -- "$TEST_FIXTURES"
        sha256sum -- *.tar.gz > lego_4.34.0_checksums.txt
    )
}

test_download() {
    arch=$1 variant=$2 asset=$3
    TARGETARCH="$arch" TARGETVARIANT="$variant" expect_status 0 /bin/sh "$download" "$work/downloaded"
    assert_contains '/lego_4.34.0_checksums.txt' "$TEST_CURL_LOG"
    assert_contains "/lego_v4.34.0_linux_$asset.tar.gz" "$TEST_CURL_LOG"
    [ -x "$work/downloaded/lego" ]
    [ -r "$work/downloaded/LICENSE" ]
    expect_status 0 "$work/downloaded/lego"
    assert_line 'lego fixture' "$stdout"
}

unsupported_targets() {
    for target in arm/v5 arm/v6 arm/ riscv64/ amd64/v3; do
        TARGETARCH="${target%/*}" TARGETVARIANT="${target#*/}" \
            expect_status 2 /bin/sh "$download" "$work/downloaded"
        assert_contains 'Unsupported target platform' "$stderr"
    done
    [ ! -s "$TEST_CURL_LOG" ]
}

archive_checksum_failures() {
    TARGETARCH=amd64 LEGO_SHA256=invalid expect_status 1 /bin/sh "$download" "$work/downloaded"
    assert_contains 'Missing or invalid SHA-256' "$stderr"
    [ ! -s "$TEST_CURL_LOG" ]
    TARGETARCH=amd64 LEGO_SHA256=abcd expect_status 1 /bin/sh "$download" "$work/downloaded"
    assert_contains 'Invalid SHA-256 length' "$stderr"
    [ ! -s "$TEST_CURL_LOG" ]
    TARGETARCH=amd64 LEGO_TARBALL=missing.tar.gz expect_status 1 /bin/sh "$download" "$work/downloaded"
    assert_contains 'Missing or invalid SHA-256' "$stderr"
    [ "$(wc -l < "$TEST_CURL_LOG")" -eq 1 ]
    TARGETARCH=amd64 LEGO_SHA256="$(printf '%064d' 0)" expect_status 1 /bin/sh "$download" "$work/downloaded"
    [ ! -e "$work/downloaded/lego" ]
}

archive_override() {
    name=lego_v4.34.0_linux_amd64.tar.gz
    sha=$(sha256sum -- "$TEST_FIXTURES/$name")
    TARGETARCH=amd64 LEGO_SHA256="${sha%% *}" LEGO_URL="https://example.test/custom/$name" \
        expect_status 0 /bin/sh "$download" "$work/downloaded"
    assert_line "https://example.test/custom/$name" "$TEST_CURL_LOG"
    [ "$(wc -l < "$TEST_CURL_LOG")" -eq 1 ]
}

count=0
failed=0
skipped=0
check() {
    description=$1
    shift
    count=$((count + 1))
    set +e
    (set -eu; reset_case; "$@") > "$work/case.log" 2>&1
    status=$?
    set -e
    if [ "$status" -eq 0 ]; then
        printf 'ok %s - %s\n' "$count" "$description"
    else
        failed=$((failed + 1))
        printf 'not ok %s - %s\n' "$count" "$description"
        cat -- "$work/case.log"
    fi
}

archive_fixtures
check 'YAML order, duplicate selections and literal wildcards' yaml_order
check 'YAML anchors, aliases, tags and quoted folder keys' yaml_aliases_and_tags
check 'Missing file, malformed YAML and multiple documents' yaml_io_errors
check 'Empty documents and wrong YAML structures' yaml_wrong_shapes
check 'Unsafe folders, duplicate keys and dangling aliases' yaml_bad_groups
check 'Invalid, duplicate and non-string DNS names' yaml_bad_names
check 'DNS label and total name lengths' yaml_length_limit
check '100-name certificate limit' yaml_name_limit
check 'Distinct missing selections' yaml_unknown_selection
check 'Help works before environment validation' cli_help
check 'Conflicting and missing CLI arguments' cli_argument_errors
check 'List and dry-run preview without credentials' cli_listing_and_preview
check 'CLI staging/production overrides' cli_mode_overrides
check 'Identity validation and privilege-drop command' cli_identities
check 'Validate all YAML before any issue request' cli_yaml_before_issue
check 'A, AAAA, CNAME, wildcard and planned names all use DNS-01' cli_dns_types
check 'Credential precedence, CNAME delegation, timeout and resolvers' cli_delegation_and_resolvers
check 'Multiple malformed settings abort before issuing' cli_bad_settings
check 'Invalid token is redacted' cli_rejects_invalid_token
check 'Compose secret is passed through lego’s token-file option' cli_routes_secret_file
check 'Invalid CNAME delegation setting skips issuance' cli_rejects_invalid_delegation
check 'Missing output path skips issuance' cli_missing_output
check 'Mixed group outcomes, continuation, order and precise totals' cli_group_results
check 'Rate limit stops lego without waiting for Retry-After' cli_stops_on_rate_limit
check 'Per-certificate time limit stops a stalled lego' cli_stops_at_time_limit
check 'Automatic, disabled and invalid time limits' cli_time_limit_settings
check 'Concurrent runs reserve distinct timestamp paths' cli_retries_and_timestamps
check 'Actual Linux UID/GID and output directory preview' core_runtime
check 'Issuance with only the shell, C++ binary and lego on PATH' cli_minimal_runtime
check 'Non-TTY summary and exact certificate totals' summary_plain
check 'Summary rejects invalid internal input' summary_rejects_bad_input
if command -v script >/dev/null; then
    check 'ANSI and aligned full-width grid' summary_tty_width 80 Certificate
    check 'ANSI and aligned compact grid' summary_tty_width 48 Tried
    check 'ANSI and aligned narrow vertical grid' summary_tty_width 28 'Attempted 100'
else
    skipped=3
    printf '# Skipped three PTY cases. Install util-linux script to run them.\n'
fi
check 'Official amd64 release asset' test_download amd64 '' amd64
check 'Official i386 release asset' test_download 386 '' 386
check 'Official ARMv7 release asset' test_download arm v7 armv7
check 'Official ARM64 release asset' test_download arm64 v8 arm64
check 'Unsupported architectures fail before network requests' unsupported_targets
check 'Malformed, missing and mismatching checksums' archive_checksum_failures
check 'Explicit checksum and custom archive URL' archive_override
printf '%s passed, %s failed, %s skipped\n' "$((count - failed))" "$failed" "$skipped"
[ "$failed" -eq 0 ]
