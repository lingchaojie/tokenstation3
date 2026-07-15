# Multi-Domain OAuth Deployment

## Background

The production server currently terminates HTTP/HTTPS in Caddy and proxies the existing product domain to the backend container on `127.0.0.1:8080`.

The target architecture for the English product domain is:

- Existing domain: keep serving the current embedded UI from the backend.
- New English domain: serve a separate frontend build.
- Both domains proxy `/api/*` to the same backend and database.
- OAuth login should start and finish on the domain the user entered from.

This document describes the backend and production configuration needed for the "two fixed domains" OAuth approach.

## Backend Behavior

The backend supports dynamic OAuth callback URL generation only for allowlisted hosts.

When a user starts OAuth from an allowed host, the backend sends the provider a callback URL on that same host:

```text
https://english.example.com/api/v1/auth/oauth/oidc/callback
```

When the request host is not allowlisted, the backend keeps the provider's configured fixed `redirect_url`. This preserves existing single-domain deployments and prevents untrusted `Host` headers from becoming OAuth redirect targets.

## Backend Config

Add both public product hosts to `config.yaml`:

```yaml
security:
  oauth_redirect:
    allowed_hosts:
      - "www.linx2.ai"
      - "english.example.com"
```

Replace `english.example.com` with the real new domain.

The existing per-provider `redirect_url` values should remain configured. They act as the legacy fallback when the request host is not in `security.oauth_redirect.allowed_hosts`.

## OAuth Provider Console

For every enabled third-party login provider, register callback URLs for both domains.

Examples:

```text
https://www.linx2.ai/api/v1/auth/oauth/github/callback
https://english.example.com/api/v1/auth/oauth/github/callback

https://www.linx2.ai/api/v1/auth/oauth/google/callback
https://english.example.com/api/v1/auth/oauth/google/callback

https://www.linx2.ai/api/v1/auth/oauth/linuxdo/callback
https://english.example.com/api/v1/auth/oauth/linuxdo/callback

https://www.linx2.ai/api/v1/auth/oauth/oidc/callback
https://english.example.com/api/v1/auth/oauth/oidc/callback

https://www.linx2.ai/api/v1/auth/oauth/wechat/callback
https://english.example.com/api/v1/auth/oauth/wechat/callback

https://www.linx2.ai/api/v1/auth/oauth/dingtalk/callback
https://english.example.com/api/v1/auth/oauth/dingtalk/callback
```

Only configure providers that are actually enabled in production.

## Caddy Shape

The current domain can stay as-is:

```caddy
linx2.ai {
    redir https://www.linx2.ai{uri} permanent
}

www.linx2.ai {
    reverse_proxy 127.0.0.1:8080
}
```

The new full product domain should serve the English frontend and proxy API routes to the same backend:

```caddy
english.example.com {
    handle /api/* {
        reverse_proxy 127.0.0.1:8080
    }

    root * /srv/english-ui/dist
    try_files {path} /index.html
    file_server
}
```

The English frontend should use a relative API base URL:

```text
/api/v1
```

Do not hard-code `https://www.linx2.ai/api/v1` in the English frontend. Keeping API calls on the current domain avoids browser CORS issues.

## Production Rollout Order

1. Deploy the backend version that contains multi-domain OAuth support.
2. Point the new domain DNS A record to the production server IP.
3. Register the new callback URLs in each enabled OAuth provider console.
4. Add the new host to `security.oauth_redirect.allowed_hosts`.
5. Add the new Caddy site block.
6. Reload Caddy and restart the backend only after confirming the config.
7. Test OAuth from the old domain and the new domain.

Production config changes and service restarts require explicit confirmation before execution.

## Rollback

To roll back the new domain without affecting the existing domain:

1. Remove or comment out the new Caddy site block.
2. Remove the new domain from `security.oauth_redirect.allowed_hosts`.
3. Reload Caddy and restart the backend after confirmation.

The existing domain continues to use its fixed provider `redirect_url`.
