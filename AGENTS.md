# AGENTS.md

## KIRO Reference Tracking

KIRO gateway work in this repository tracks `https://github.com/nianzs/sub2api`.

Before changing KIRO forwarding, request translation, response parsing, OAuth refresh, cache emulation, cooldown, KIRO usage, or KIRO admin OAuth behavior, compare against the reference fork first.

Start with:

- `backend/internal/pkg/kiro`
- `backend/internal/pkg/kirocooldown`
- `backend/internal/service/kiro_*.go`
- KIRO-related sections of `backend/internal/service/gateway_service.go`
- `backend/internal/handler/admin/kiro_oauth_handler.go`

Reference commit used for the 2026-06-29 replacement: `88a5666b478e234cace9090e0d5f483f1146cb96`.

Keep local DEV features outside KIRO unless a KIRO integration point requires a narrow patch.
