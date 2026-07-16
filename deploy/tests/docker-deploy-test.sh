#!/bin/bash

set -euo pipefail

TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY_DIR="$(cd "${TEST_DIR}/.." && pwd)"
DEPLOY_SCRIPT="${DEPLOY_DIR}/docker-deploy.sh"
TEMP_DIR="$(mktemp -d)"

cleanup() {
    rm -rf "$TEMP_DIR"
}
trap cleanup EXIT

fail() {
    printf 'FAIL: %s\n' "$1" >&2
    exit 1
}

assert_equal() {
    local expected="$1"
    local actual="$2"
    local message="$3"

    if [ "$expected" != "$actual" ]; then
        printf 'FAIL: %s\nexpected:\n%s\nactual:\n%s\n' "$message" "$expected" "$actual" >&2
        exit 1
    fi
}

assert_file_content() {
    local expected="$1"
    local file_path="$2"
    local message="$3"
    local actual=""

    actual="$(cat "$file_path")"
    assert_equal "$expected" "$actual" "$message"
}

assert_rejected_domains() {
    local raw="$1"
    local primary="$2"
    local apex="$3"
    local message="$4"

    if normalize_additional_domains "$raw" "$primary" "$apex" >/dev/null 2>&1; then
        fail "$message"
    fi
}

if ! grep -Fq 'if [ "${DOCKER_DEPLOY_SOURCE_ONLY:-0}" != "1" ]; then' "$DEPLOY_SCRIPT"; then
    fail "source-only guard missing; refusing to source docker-deploy.sh"
fi

export DOCKER_DEPLOY_SOURCE_ONLY=1
# shellcheck source=../docker-deploy.sh
source "$DEPLOY_SCRIPT"

for required_function in normalize_domain_name normalize_additional_domains render_managed_caddyfile; do
    if ! declare -F "$required_function" >/dev/null; then
        fail "missing required function: ${required_function}"
    fi
done

normalized="$(normalize_additional_domains "" "www.example.com" "example.com")"
assert_equal "" "$normalized" "empty additional domain input should produce no output"

normalized="$(normalize_additional_domains "YUNDU.Example.COM" "www.example.com" "example.com")"
assert_equal "yundu.example.com" "$normalized" "additional domains should be lowercased"

normalized="$(normalize_additional_domains $'YUNDU.Example.COM, api.Example.com\nstatus.EXAMPLE.com' "www.example.com" "example.com")"
assert_equal $'yundu.example.com\napi.example.com\nstatus.example.com' "$normalized" "comma and whitespace separators should preserve input order"

assert_rejected_domains "One.Example.com, one.example.COM" "www.example.com" "example.com" "case-insensitive duplicates should be rejected"
assert_rejected_domains "WWW.EXAMPLE.COM" "www.example.com" "example.com" "the primary domain should not be repeated"
assert_rejected_domains "EXAMPLE.COM" "www.example.com" "example.com" "the apex domain should not be repeated"
assert_rejected_domains "https://bad.example.com" "www.example.com" "example.com" "URL input should be rejected"

preflight_dir="${TEMP_DIR}/main-preflight"
preflight_stub_dir="${preflight_dir}/bin"
download_log="${preflight_dir}/download.log"
mkdir -p "$preflight_stub_dir"

cat > "${preflight_stub_dir}/curl" <<'EOF'
#!/bin/bash
printf 'curl\n' >> "$DOWNLOAD_LOG"
output_file=""
while [ "$#" -gt 0 ]; do
    if [ "$1" = "-o" ]; then
        shift
        output_file="$1"
    fi
    shift
done
if [ "$(basename "$output_file")" = ".env.example" ]; then
    printf 'JWT_SECRET=\nTOTP_ENCRYPTION_KEY=\nPOSTGRES_PASSWORD=\n' > "$output_file"
else
    printf 'services: {}\n' > "$output_file"
fi
EOF

cat > "${preflight_stub_dir}/wget" <<'EOF'
#!/bin/bash
printf 'wget\n' >> "$DOWNLOAD_LOG"
exit 1
EOF

cat > "${preflight_stub_dir}/openssl" <<'EOF'
#!/bin/bash
printf '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n'
EOF

cat > "${preflight_stub_dir}/caddy" <<'EOF'
#!/bin/bash
if [ "${1:-}" = "version" ]; then
    printf 'test-caddy\n'
fi
EOF

cat > "${preflight_stub_dir}/id" <<'EOF'
#!/bin/bash
if [ "${1:-}" = "-u" ]; then
    printf '1000\n'
else
    exec /usr/bin/id "$@"
fi
EOF
chmod +x \
    "${preflight_stub_dir}/curl" \
    "${preflight_stub_dir}/wget" \
    "${preflight_stub_dir}/openssl" \
    "${preflight_stub_dir}/caddy" \
    "${preflight_stub_dir}/id"

if (
    cd "$preflight_dir"
    export PATH="${preflight_stub_dir}:${PATH}"
    export DOWNLOAD_LOG="$download_log"
    DOMAIN="www.example.com"
    APEX_DOMAIN="example.com"
    ADDITIONAL_DOMAINS="https://bad.example.com"
    main > "${preflight_dir}/main-output.log" 2>&1
); then
    fail "main should reject invalid additional domains"
fi

if [ -e "$download_log" ]; then
    fail "main should validate requested domains before calling download tools"
fi
for artifact in docker-compose.yml .env .env.example data postgres_data redis_data; do
    if [ -e "${preflight_dir}/${artifact}" ]; then
        fail "invalid requested domains should not create ${artifact}"
    fi
done

collision_dir="${TEMP_DIR}/main-domain-collision"
collision_download_log="${collision_dir}/download.log"
mkdir -p "$collision_dir"
if (
    cd "$collision_dir"
    export PATH="${preflight_stub_dir}:${PATH}"
    export DOWNLOAD_LOG="$collision_download_log"
    DOMAIN="www.example.com"
    APEX_DOMAIN="WWW.EXAMPLE.COM"
    ADDITIONAL_DOMAINS=""
    main > "${collision_dir}/main-output.log" 2>&1
); then
    fail "main should reject case-insensitive DOMAIN and APEX_DOMAIN collisions"
fi

if [ -e "$collision_download_log" ]; then
    fail "domain collisions should be rejected before calling download tools"
fi
for artifact in docker-compose.yml .env .env.example data postgres_data redis_data; do
    if [ -e "${collision_dir}/${artifact}" ]; then
        fail "domain collisions should not create ${artifact}"
    fi
done

if (
    DOMAIN="www.example.com"
    APEX_DOMAIN="www.example.com"
    ADDITIONAL_DOMAINS=""
    preflight_requested_domains >/dev/null 2>&1
); then
    fail "preflight should reject identical DOMAIN and APEX_DOMAIN values"
fi

(
    CONFIGURED_PRIMARY_DOMAIN=""
    CONFIGURED_APEX_DOMAIN=""
    CONFIGURED_ADDITIONAL_DOMAINS=""
    DOMAIN="WWW.Example.COM"
    APEX_DOMAIN="Example.COM"
    ADDITIONAL_DOMAINS="API.Example.COM"
    preflight_requested_domains
    assert_equal "www.example.com" "$CONFIGURED_PRIMARY_DOMAIN" "preflight should store a normalized primary domain"
    assert_equal "example.com" "$CONFIGURED_APEX_DOMAIN" "preflight should store a normalized apex domain"
    assert_equal "api.example.com" "$CONFIGURED_ADDITIONAL_DOMAINS" "preflight should store normalized additional domains"
)

(
    CUSTOM_DOMAIN_CONFIGURED=1
    CONFIGURED_PRIMARY_DOMAIN="stale-primary.example.com"
    CONFIGURED_APEX_DOMAIN="stale-apex.example.com"
    CONFIGURED_ADDITIONAL_DOMAINS="stale-additional.example.com"
    DOMAIN=""
    APEX_DOMAIN="IGNORED.EXAMPLE.COM"
    ADDITIONAL_DOMAINS="IGNORED-ADDITIONAL.EXAMPLE.COM"
    preflight_requested_domains
    assert_equal "0" "$CUSTOM_DOMAIN_CONFIGURED" "preflight should reset the configured flag"
    assert_equal "" "$CONFIGURED_PRIMARY_DOMAIN" "no-domain preflight should clear the primary domain"
    assert_equal "" "$CONFIGURED_APEX_DOMAIN" "no-domain preflight should clear the apex domain"
    assert_equal "" "$CONFIGURED_ADDITIONAL_DOMAINS" "no-domain preflight should clear additional domains"
)

normalized_config_dir="${TEMP_DIR}/normalized-configure"
normalized_write_args="${normalized_config_dir}/write-args"
normalized_config_output="${normalized_config_dir}/output.log"
mkdir -p "$normalized_config_dir"
printf 'BIND_HOST=127.0.0.1\n' > "${normalized_config_dir}/.env"
(
    cd "$normalized_config_dir"
    DOMAIN="WWW.Example.COM"
    APEX_DOMAIN="Example.COM"
    ADDITIONAL_DOMAINS="API.Example.COM"
    sync_server_port_env() { return 0; }
    sync_bind_host_env() { return 0; }
    get_public_server_port() { printf '8080'; }
    install_caddy_if_needed() { return 0; }
    write_caddyfile() { printf '%s\n%s\n%s\n%s\n' "$1" "$2" "$3" "$4" > "$normalized_write_args"; }
    configure_caddy_if_requested > "$normalized_config_output"
)
expected_write_args=$'www.example.com\nexample.com\n8080\napi.example.com'
assert_file_content "$expected_write_args" "$normalized_write_args" "Caddy configuration should use normalized domain values"
for normalized_dns_domain in www.example.com example.com api.example.com; do
    if ! grep -Fq "Point DNS for ${normalized_dns_domain}" "$normalized_config_output"; then
        fail "DNS notes should use normalized ${normalized_dns_domain}"
    fi
done
if grep -Fq 'WWW.Example.COM' "$normalized_config_output"; then
    fail "DNS notes should not use raw mixed-case domain input"
fi

summary_dir="${TEMP_DIR}/normalized-summary"
summary_output="${summary_dir}/output.log"
summary_download_log="${summary_dir}/download.log"
mkdir -p "$summary_dir"
(
    cd "$summary_dir"
    export PATH="${preflight_stub_dir}:${PATH}"
    export DOWNLOAD_LOG="$summary_download_log"
    DOMAIN="WWW.Example.COM"
    APEX_DOMAIN="Example.COM"
    ADDITIONAL_DOMAINS="API.Example.COM"
    configure_caddy_if_requested() { CUSTOM_DOMAIN_CONFIGURED=1; }
    main > "$summary_output"
)
for normalized_url in \
    'https://www.example.com' \
    'https://example.com redirects to https://www.example.com' \
    'https://api.example.com'; do
    if ! grep -Fq "$normalized_url" "$summary_output"; then
        fail "main summary should include normalized URL: ${normalized_url}"
    fi
done
if grep -Fq 'WWW.Example.COM' "$summary_output" || grep -Fq 'Example.COM' "$summary_output"; then
    fail "main summary should not use raw mixed-case domain input"
fi

rendered="$(render_managed_caddyfile \
    "www.example.com" \
    "example.com" \
    "8080" \
    $'yundu.example.com\n\nanother.example.com')"
expected_rendered=$'# Managed by Sub2API/TokenStation docker-deploy.sh.\n# To change domains after deployment, edit this file and reload Caddy.\nexample.com {\n\tredir https://www.example.com{uri} permanent\n}\n\nwww.example.com {\n\treverse_proxy 127.0.0.1:8080\n}\n\nyundu.example.com {\n\treverse_proxy 127.0.0.1:8080\n}\n\nanother.example.com {\n\treverse_proxy 127.0.0.1:8080\n}'
assert_equal "$expected_rendered" "$rendered" "renderer should include apex, primary, and additional sites"

for site in www.example.com yundu.example.com another.example.com; do
    count="$(printf '%s\n' "$rendered" | grep -Fxc "${site} {")"
    assert_equal "1" "$count" "renderer should include ${site} exactly once"
done

rendered="$(render_managed_caddyfile "www.example.com" "" "8080" "")"
expected_rendered=$'# Managed by Sub2API/TokenStation docker-deploy.sh.\n# To change domains after deployment, edit this file and reload Caddy.\nwww.example.com {\n\treverse_proxy 127.0.0.1:8080\n}'
assert_equal "$expected_rendered" "$rendered" "empty additional domains should preserve the primary-only config"
count="$(printf '%s\n' "$rendered" | grep -Fc 'reverse_proxy 127.0.0.1:8080')"
assert_equal "1" "$count" "primary-only rendering should have one reverse proxy"

if ! strict_rendered="$(render_managed_caddyfile "strict.example.com" "" "8080")"; then
    fail "three-argument rendering should work with set -u"
fi
expected_strict_rendered=$'# Managed by Sub2API/TokenStation docker-deploy.sh.\n# To change domains after deployment, edit this file and reload Caddy.\nstrict.example.com {\n\treverse_proxy 127.0.0.1:8080\n}'
assert_equal "$expected_strict_rendered" "$strict_rendered" "omitted additional domains should render as empty"

existing_file="${TEMP_DIR}/existing.caddy"
existing_backup="${TEMP_DIR}/existing.caddy.backup"
printf '%s\n' "changed" > "$existing_file"
printf '%s\n' "original" > "$existing_backup"
restore_caddy_backup "$existing_file" "$existing_backup" "1"
assert_file_content "original" "$existing_file" "restore_caddy_backup should restore an existing file"

new_file="${TEMP_DIR}/new.caddy"
printf '%s\n' "new" > "$new_file"
restore_caddy_backup "$new_file" "${TEMP_DIR}/unused.backup" "0"
if [ -e "$new_file" ]; then
    fail "restore_caddy_backup should remove a newly created file"
fi

stub_dir="${TEMP_DIR}/bin"
mkdir -p "$stub_dir"

cat > "${stub_dir}/id" <<'EOF'
#!/bin/bash
if [ "${1:-}" = "-u" ]; then
    printf '0\n'
else
    exec /usr/bin/id "$@"
fi
EOF

cat > "${stub_dir}/caddy" <<'EOF'
#!/bin/bash
if [ -n "${CALL_LOG:-}" ]; then
    printf 'caddy %s\n' "$*" >> "$CALL_LOG"
fi
case "${1:-}" in
    version)
        printf 'test-caddy\n'
        ;;
    fmt)
        [ "${CADDY_FAILURE:-}" != "fmt" ]
        ;;
    validate)
        [ "${CADDY_FAILURE:-}" != "validate" ]
        ;;
    *)
        exit 0
        ;;
esac
EOF

cat > "${stub_dir}/systemctl" <<'EOF'
#!/bin/bash
if [ -n "${CALL_LOG:-}" ]; then
    printf 'systemctl %s\n' "$*" >> "$CALL_LOG"
fi

next_attempt() {
    local operation="$1"
    local count_file="${STUB_STATE_DIR}/${operation}.count"
    local count=0

    if [ -f "$count_file" ]; then
        read -r count < "$count_file"
    fi
    count=$((count + 1))
    printf '%s\n' "$count" > "$count_file"
    printf '%s' "$count"
}

case "${1:-}" in
    is-active)
        [ "${SYSTEMCTL_ACTIVE:-0}" = "1" ]
        ;;
    enable)
        [ "${SYSTEMCTL_ENABLE_FAILURE:-0}" != "1" ]
        ;;
    reload)
        if [ -n "${SYSTEMCTL_RELOAD_FAILURES:-}" ]; then
            attempt="$(next_attempt reload)"
            [ "$attempt" -gt "$SYSTEMCTL_RELOAD_FAILURES" ]
        fi
        ;;
    restart)
        if [ -n "${SYSTEMCTL_RESTART_FAILURES:-}" ]; then
            attempt="$(next_attempt restart)"
            [ "$attempt" -gt "$SYSTEMCTL_RESTART_FAILURES" ]
        fi
        ;;
    *)
        exit 0
        ;;
esac
EOF

chmod +x "${stub_dir}/id" "${stub_dir}/caddy" "${stub_dir}/systemctl"
export PATH="${stub_dir}:${PATH}"
unset CALL_LOG CADDY_FAILURE STUB_STATE_DIR SYSTEMCTL_ACTIVE \
    SYSTEMCTL_ENABLE_FAILURE SYSTEMCTL_RELOAD_FAILURES SYSTEMCTL_RESTART_FAILURES

ordering_dir="${TEMP_DIR}/normal-ordering"
ordering_root="${ordering_dir}/Caddyfile"
ordering_managed="${ordering_dir}/managed/sub2api.caddy"
ordering_log="${ordering_dir}/calls.log"
mkdir -p "$(dirname "$ordering_managed")"
printf 'root original\n' > "$ordering_root"
printf 'managed original\n' > "$ordering_managed"

if ! CALL_LOG="$ordering_log" write_caddyfile \
    "www.example.com" "example.com" "8080" "" \
    "$ordering_root" "$ordering_managed" >/dev/null 2>&1; then
    fail "normal Caddy write should succeed"
fi
if [ ! -f "$ordering_log" ]; then
    fail "Caddy and systemctl stubs should record command order"
fi
expected_ordering="$(printf '%s\n' \
    "caddy fmt --overwrite ${ordering_managed}" \
    "caddy validate --config ${ordering_root}" \
    "systemctl enable --now caddy" \
    "systemctl reload caddy")"
assert_file_content "$expected_ordering" "$ordering_log" "normal Caddy operations should run in order"

fallback_dir="${TEMP_DIR}/reload-fallback"
fallback_root="${fallback_dir}/Caddyfile"
fallback_managed="${fallback_dir}/managed/sub2api.caddy"
fallback_log="${fallback_dir}/calls.log"
fallback_state="${fallback_dir}/state"
mkdir -p "$(dirname "$fallback_managed")" "$fallback_state"
printf 'root original\n' > "$fallback_root"
printf 'managed original\n' > "$fallback_managed"

if ! CALL_LOG="$fallback_log" STUB_STATE_DIR="$fallback_state" SYSTEMCTL_RELOAD_FAILURES=1 \
    write_caddyfile \
        "www.example.com" "example.com" "8080" "" \
        "$fallback_root" "$fallback_managed" >/dev/null 2>&1; then
    fail "restart fallback should succeed after the first reload fails"
fi
expected_fallback="$(printf '%s\n' \
    "caddy fmt --overwrite ${fallback_managed}" \
    "caddy validate --config ${fallback_root}" \
    "systemctl enable --now caddy" \
    "systemctl reload caddy" \
    "systemctl restart caddy")"
assert_file_content "$expected_fallback" "$fallback_log" "reload failure should fall back to restart in order"

assert_write_failure_restores_both_files() {
    local case_name="$1"
    local caddy_failure="$2"
    local enable_failure="$3"
    local active_before="$4"
    local reload_failures="$5"
    local restart_failures="$6"
    local expected_log="$7"
    local case_dir="${TEMP_DIR}/${case_name}"
    local root_caddyfile="${case_dir}/Caddyfile"
    local managed_caddyfile="${case_dir}/managed/sub2api.caddy"
    local output_file="${case_dir}/output.log"
    local call_log="${case_dir}/calls.log"
    local state_dir="${case_dir}/state"
    local root_original="root original ${case_name}"
    local managed_original="managed original ${case_name}"

    mkdir -p "$(dirname "$managed_caddyfile")" "$state_dir"
    printf '%s\n' "$root_original" > "$root_caddyfile"
    printf '%s\n' "$managed_original" > "$managed_caddyfile"

    if CALL_LOG="$call_log" STUB_STATE_DIR="$state_dir" \
        CADDY_FAILURE="$caddy_failure" \
        SYSTEMCTL_ENABLE_FAILURE="$enable_failure" \
        SYSTEMCTL_ACTIVE="$active_before" \
        SYSTEMCTL_RELOAD_FAILURES="$reload_failures" \
        SYSTEMCTL_RESTART_FAILURES="$restart_failures" \
        write_caddyfile \
            "www.example.com" \
            "example.com" \
            "8080" \
            $'yundu.example.com\nanother.example.com' \
            "$root_caddyfile" \
            "$managed_caddyfile" >"$output_file" 2>&1; then
        fail "write_caddyfile should fail for ${case_name}"
    fi

    assert_file_content "$root_original" "$root_caddyfile" "${case_name} should restore the root Caddyfile"
    assert_file_content "$managed_original" "$managed_caddyfile" "${case_name} should restore the managed Caddyfile"
    assert_file_content "$expected_log" "$call_log" "${case_name} should use the expected Caddy/systemctl sequence"
}

fmt_dir="${TEMP_DIR}/fmt-failure"
fmt_expected="$(printf '%s\n' \
    "caddy fmt --overwrite ${fmt_dir}/managed/sub2api.caddy" \
    "systemctl is-active --quiet caddy")"
assert_write_failure_restores_both_files \
    "fmt-failure" "fmt" "" "0" "" "" "$fmt_expected"

validate_dir="${TEMP_DIR}/validate-failure"
validate_expected="$(printf '%s\n' \
    "caddy fmt --overwrite ${validate_dir}/managed/sub2api.caddy" \
    "caddy validate --config ${validate_dir}/Caddyfile" \
    "systemctl is-active --quiet caddy")"
assert_write_failure_restores_both_files \
    "validate-failure" "validate" "" "0" "" "" "$validate_expected"

enable_dir="${TEMP_DIR}/enable-failure"
enable_expected="$(printf '%s\n' \
    "caddy fmt --overwrite ${enable_dir}/managed/sub2api.caddy" \
    "caddy validate --config ${enable_dir}/Caddyfile" \
    "systemctl enable --now caddy" \
    "systemctl is-active --quiet caddy" \
    "systemctl reload caddy")"
assert_write_failure_restores_both_files \
    "enable-failure" "" "1" "1" "0" "0" "$enable_expected"

rollback_dir="${TEMP_DIR}/reload-restart-failure"
rollback_expected="$(printf '%s\n' \
    "caddy fmt --overwrite ${rollback_dir}/managed/sub2api.caddy" \
    "caddy validate --config ${rollback_dir}/Caddyfile" \
    "systemctl enable --now caddy" \
    "systemctl reload caddy" \
    "systemctl restart caddy" \
    "systemctl is-active --quiet caddy" \
    "systemctl reload caddy")"
assert_write_failure_restores_both_files \
    "reload-restart-failure" "" "" "1" "1" "1" "$rollback_expected"

mutation_marker="${TEMP_DIR}/unexpected-caddy-mutation"
if (
    DOMAIN="www.example.com"
    APEX_DOMAIN="example.com"
    ADDITIONAL_DOMAINS="https://bad.example.com"
    sync_server_port_env() { printf 'sync\n' > "$mutation_marker"; }
    install_caddy_if_needed() { printf 'install\n' > "$mutation_marker"; }
    write_caddyfile() { printf 'write\n' > "$mutation_marker"; }
    configure_caddy_if_requested
) >/dev/null 2>&1; then
    fail "invalid additional domains should make Caddy configuration fail"
fi
if [ -e "$mutation_marker" ]; then
    fail "invalid additional domains should fail before deployment mutation"
fi

(
    DOMAIN=""
    ADDITIONAL_DOMAINS="yundu.example.com"
    install_caddy_if_needed() { printf 'install\n' > "$mutation_marker"; }
    write_caddyfile() { printf 'write\n' > "$mutation_marker"; }
    configure_caddy_if_requested
)
if [ -e "$mutation_marker" ]; then
    fail "ADDITIONAL_DOMAINS without DOMAIN should not initiate Caddy setup"
fi

printf 'docker-deploy domain tests passed.\n'
