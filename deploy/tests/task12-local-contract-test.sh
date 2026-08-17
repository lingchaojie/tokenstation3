#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
compose_file="$repo_root/deploy/docker-compose.yml"
dev_compose_file="$repo_root/deploy/docker-compose.dev.yml"
env_example="$repo_root/deploy/.env.example"
caddyfile="$repo_root/deploy/Caddyfile"
config_example="$repo_root/deploy/config.example.yaml"

assert_contains() {
	file=$1
	pattern=$2
	message=$3
	if ! grep -Eq "$pattern" "$file"; then
		echo "$message" >&2
		exit 1
	fi
}

assert_absent() {
	file=$1
	pattern=$2
	message=$3
	if grep -Eq "$pattern" "$file"; then
		echo "$message" >&2
		exit 1
	fi
}

assert_contains "$compose_file" 'SUB2API_IMAGE:-ghcr\.io/lingchaojie/sub2api:latest' \
	'docker-compose.yml must retain the local GHCR image default'
assert_contains "$compose_file" 'UPDATE_GITHUB_REPO=.*lingchaojie/tokenstation3' \
	'docker-compose.yml must retain the local update repository default'
assert_absent "$compose_file" 'ALIPAY_MOBILE_PRECREATE_DEEP_LINK' \
	'docker-compose.yml must not expose the deferred Alipay deep-link switch'
assert_contains "$dev_compose_file" '127\.0\.0\.1.*SERVER_PORT:-8080.*:8080' \
	'development compose must bind the service to 127.0.0.1:8080 by default'

assert_contains "$env_example" '^SUB2API_IMAGE=ghcr\.io/lingchaojie/sub2api:latest$' \
	'.env.example must retain the local GHCR image default'
assert_contains "$env_example" '^UPDATE_GITHUB_REPO=lingchaojie/tokenstation3$' \
	'.env.example must retain the local update repository default'
assert_contains "$env_example" '^UPDATE_GITHUB_TOKEN=$' \
	'.env.example must document the approved updater token'
assert_absent "$env_example" 'ALIPAY_MOBILE_PRECREATE_DEEP_LINK' \
	'.env.example must not expose the deferred Alipay deep-link switch'

assert_contains "$caddyfile" '^example\.com \{' \
	'Caddyfile must retain the local redirect domain'
assert_contains "$caddyfile" '^www\.example\.com \{' \
	'Caddyfile must retain the local application domain'
assert_contains "$caddyfile" 'reverse_proxy[[:space:]]+127\.0\.0\.1:8080' \
	'Caddyfile must proxy to the local 127.0.0.1:8080 service'
assert_contains "$caddyfile" 'header Content-Type application/json\*' \
	'Caddyfile must retain the canonical non-SSE compression matcher'

assert_contains "$config_example" 'https://sdk\.51\.la' \
	'config.example.yaml must retain the local 51.LA CSP source'
assert_absent "$config_example" 'max_session_duration_seconds' \
	'config.example.yaml must not expose deferred Live controls'

echo 'Task 12 local deployment contracts are preserved'
