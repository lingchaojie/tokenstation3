# OpenAI Compact Non-Stream Keepalive

## Background

This change tracks upstream PR [Wei-Shaw/sub2api#2976](https://github.com/Wei-Shaw/sub2api/pull/2976), "feat(gateway): add downstream keepalive for non-stream compact responses".

The production symptom this class of fix targets is a long-running non-stream OpenAI compact request sitting behind a reverse proxy or CDN, especially Cloudflare. The upstream may accept the request and spend a long time processing without returning response bytes. If the downstream connection stays silent longer than the proxy read/idle timeout, Cloudflare can return 524 before Sub2API has a complete JSON response to write.

This is an application-layer idle timeout problem. TCP keepalive does not help, and changing only the upstream request mode does not necessarily produce downstream bytes. JSON clients also cannot be switched to SSE without breaking non-stream callers.

## Original PR

PR #2976 adds `gateway.openai_compact_nonstream_keepalive_interval` for `/responses/compact` non-stream responses. When enabled, Sub2API writes a blank line (`\n`) to the downstream client at a fixed interval while waiting for the upstream compact response, then writes the final JSON once it is ready.

Important upstream PR details:

- Original default: `0`, disabled by default.
- Valid values: `0` or `5-60` seconds.
- Mechanism: downstream blank-line keepalive, not SSE.
- JSON compatibility: JSON allows leading whitespace, so `\n{...}` remains parseable as JSON.
- Trade-off: after the first blank line is flushed, the HTTP status has been committed, so later upstream errors cannot be represented with a different downstream status code or normal failover behavior.

## Local Decision

For this repository, the setting is intentionally enabled by default at `60` seconds.

Reasoning:

- Cloudflare's 524 window is around 120 seconds for the observed deployment path.
- `60` is the maximum allowed non-zero value from PR #2976 and stays safely below the proxy timeout.
- A longer interval reduces extra writes compared with shorter values while still preventing a fully silent connection.

This is a local policy difference from upstream PR #2976. Upstream made the feature opt-in; this repo defaults it on for compact non-stream requests.

## Scope

Covered:

- OpenAI `/v1/responses/compact` and equivalent compact route suffixes.
- Non-stream compact requests in the normal OpenAI HTTP forwarding path.
- Non-stream compact requests in the OpenAI OAuth passthrough path.
- All models sent through the compact endpoint, because the trigger is the request path, not the model name.

Not covered:

- Normal `/v1/responses` non-compact non-stream requests.
- `/v1/chat/completions` non-stream requests.
- Anthropic or Kiro `/v1/messages` non-stream requests.
- Other providers' long non-stream calls.

Streaming requests use their own stream/SSE handling and are not the target of this change.

## Configuration

Config key:

```yaml
gateway:
  # 0 disables this feature. Non-zero values must be 5-60.
  openai_compact_nonstream_keepalive_interval: 60
```

Environment variable:

```env
GATEWAY_OPENAI_COMPACT_NONSTREAM_KEEPALIVE_INTERVAL=60
```

Validation:

- `0`: disabled.
- `5-60`: enabled, interval in seconds.
- Negative values and `1-4` are rejected.

## Deployment Notes

`deploy/.env.example` is only a template and is not automatically loaded by Docker Compose.

Effective sources are:

- Code default: `gateway.openai_compact_nonstream_keepalive_interval = 60`.
- Compose templates: `GATEWAY_OPENAI_COMPACT_NONSTREAM_KEEPALIVE_INTERVAL=${GATEWAY_OPENAI_COMPACT_NONSTREAM_KEEPALIVE_INTERVAL:-60}`.
- Actual production `.env` or mounted `config.yaml`, if present, can override the default.

If production only pulls a new image, the code default applies unless production config explicitly sets another value. If production also updates the compose file from this repo and the real `.env` does not define the variable, Compose injects `60`.

This repository change does not modify production host configuration by itself.

## Implementation Summary

The service starts a compact non-stream keepalive helper immediately before the upstream HTTP request is sent. The helper:

- Applies only when the request path is `/responses/compact`.
- Applies only for non-stream handling.
- Sets downstream headers appropriate for an unbuffered JSON response.
- Writes `\n` and flushes every configured interval.
- Stops before the final JSON body, converted SSE body, or protocol error body is written.

If an upstream transport error or HTTP error happens after the keepalive has already committed the downstream response, the handler logs that the compact keepalive committed the response and writes the best available OpenAI-format error body. At that point the downstream status code cannot be changed.

## Files

- `backend/internal/config/config.go`: config field, default, validation.
- `backend/internal/service/openai_gateway_service.go`: keepalive helper and forwarding integration.
- `backend/internal/config/config_test.go`: default/env/range tests.
- `backend/internal/service/openai_oauth_passthrough_test.go`: compact non-stream keepalive behavior tests.
- `deploy/.env.example`: environment variable template.
- `deploy/config.example.yaml`: YAML example.
- `deploy/docker-compose*.yml`: Compose default environment wiring.

## Verification

Focused verification used for this change:

```bash
cd backend
go test ./internal/config -run 'OpenAICompactNonstreamKeepalive' -count=1
go test -tags=unit ./internal/service -run 'TestOpenAIGatewayService_OAuthPassthrough_CompactNonstreamKeepalive' -count=1
go test ./internal/config -count=1
go test -tags=unit ./internal/service -count=1
```

Deployment template checks:

```bash
git diff --check
POSTGRES_PASSWORD=dummy docker compose -f deploy/docker-compose.yml config
POSTGRES_PASSWORD=dummy docker compose -f deploy/docker-compose.local.yml config
POSTGRES_PASSWORD=dummy docker compose -f deploy/docker-compose.dev.yml config
DATABASE_HOST=db.example DATABASE_PASSWORD=dummy REDIS_HOST=redis.example docker compose -f deploy/docker-compose.standalone.yml config
```
