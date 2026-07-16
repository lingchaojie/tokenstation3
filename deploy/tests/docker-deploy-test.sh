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
case "${1:-}" in
    is-active)
        exit 1
        ;;
    reload|restart)
        [ "${SYSTEMCTL_FAILURE:-}" != "reload-restart" ]
        ;;
    *)
        exit 0
        ;;
esac
EOF

chmod +x "${stub_dir}/id" "${stub_dir}/caddy" "${stub_dir}/systemctl"
export PATH="${stub_dir}:${PATH}"

assert_write_failure_restores_both_files() {
    local case_name="$1"
    local caddy_failure="$2"
    local systemctl_failure="$3"
    local case_dir="${TEMP_DIR}/${case_name}"
    local root_caddyfile="${case_dir}/Caddyfile"
    local managed_caddyfile="${case_dir}/managed/sub2api.caddy"
    local output_file="${case_dir}/output.log"
    local root_original="root original ${case_name}"
    local managed_original="managed original ${case_name}"

    mkdir -p "$(dirname "$managed_caddyfile")"
    printf '%s\n' "$root_original" > "$root_caddyfile"
    printf '%s\n' "$managed_original" > "$managed_caddyfile"

    if CADDY_FAILURE="$caddy_failure" SYSTEMCTL_FAILURE="$systemctl_failure" \
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
}

assert_write_failure_restores_both_files "fmt-failure" "fmt" ""
assert_write_failure_restores_both_files "validate-failure" "validate" ""
assert_write_failure_restores_both_files "reload-restart-failure" "" "reload-restart"

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
