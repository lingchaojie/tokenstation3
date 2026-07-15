# Multi-Domain OAuth Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Support two fixed product domains using the same backend while letting OAuth callbacks stay on the domain that started the flow.

**Architecture:** Add a small OAuth domain resolver in the backend handler layer. The resolver derives provider callback URLs from the current request host only when that host is explicitly allowed, otherwise existing fixed `redirect_url` behavior remains unchanged.

**Tech Stack:** Go, Gin, existing config/settings services, existing OAuth handlers.

## Global Constraints

- Do not modify production configuration or restart production services in this branch.
- Preserve existing fixed `redirect_url` behavior when no multi-domain allowlist is configured.
- Do not trust arbitrary `Host` values; dynamic callback generation must require an allowlist.
- Keep frontend unchanged for this pass.

---

### Task 1: OAuth Domain Resolver

**Files:**
- Create: `backend/internal/handler/oauth_domain.go`
- Test: `backend/internal/handler/oauth_domain_test.go`
- Modify: `backend/internal/config/config.go`
- Test: `backend/internal/config/config_test.go`

**Steps:**
- [ ] Write failing tests for allowed current-host callback URL resolution, fallback to fixed URLs, and absolute frontend callback rebasing.
- [ ] Add `security.oauth_redirect.allowed_hosts` config.
- [ ] Implement resolver helpers.
- [ ] Run targeted config and handler tests.

### Task 2: Provider Integration

**Files:**
- Modify: `backend/internal/handler/auth_linuxdo_oauth.go`
- Modify: `backend/internal/handler/auth_oidc_oauth.go`
- Modify: `backend/internal/handler/auth_wechat_oauth.go`
- Modify: `backend/internal/handler/auth_dingtalk_oauth.go`
- Modify: `backend/internal/handler/auth_email_oauth.go`

**Steps:**
- [ ] Write failing provider start tests that assert `redirect_uri` uses an allowlisted request host.
- [ ] Resolve `redirect_uri` on start and callback for LinuxDo, OIDC, WeChat, DingTalk, GitHub, and Google email OAuth.
- [ ] Resolve frontend callback paths so absolute old-domain callbacks do not force users off the new domain when the new host is allowlisted.
- [ ] Run targeted OAuth handler tests.

### Task 3: Deployment Documentation

**Files:**
- Create: `docs/deployment/multi-domain-oauth.md`

**Steps:**
- [ ] Document the background, server topology, Caddy shape, OAuth provider callback URLs, and backend config.
- [ ] Make clear that production changes require separate confirmation.
- [ ] Include rollback notes.

