# KIRO Reference Replacement Design

## Objective

Replace the local DEV KIRO forwarding gateway implementation with the implementation from `https://github.com/nianzs/sub2api`, using reference commit `88a5666b478e234cace9090e0d5f483f1146cb96`.

## Scope

The replacement targets KIRO runtime forwarding, KIRO request and response translation, KIRO OAuth token handling used by forwarding, KIRO cache emulation, KIRO cooldown state, KIRO usage fetching, and the backend/frontend wiring required to expose those capabilities.

Existing local DEV behavior outside KIRO must be preserved. Files shared with OpenAI, Anthropic, Antigravity, Grok, billing, payment, admin, and user flows should be patched only where KIRO integration requires it.

## Reference Implementation

The reference fork keeps its KIRO implementation primarily in:

- `backend/internal/pkg/kiro`
- `backend/internal/pkg/kirocooldown`
- `backend/internal/service/kiro_*.go`
- KIRO-related sections of `backend/internal/service/gateway_service.go`
- KIRO-related sections of account usage, token refresh, group cache configuration, and dependency wiring
- KIRO-related frontend account creation/editing and admin API files

Local DEV currently has a different older KIRO path in `backend/internal/service/openai_gateway_kiro.go`, `backend/internal/service/kiro_eventstream.go`, `backend/internal/service/kiro_bridge.go`, and related helpers. These files should not remain as competing implementations after the replacement.

## Architecture

KIRO direct accounts should route through `GatewayService.forwardKiroMessages`, using the reference fork's translator package to build KIRO runtime payloads and convert KIRO event-stream responses back to Claude-compatible responses.

The runtime path should use stable account keys, machine IDs, profile ARN resolution, conditional KIRO headers, retry and cooldown handling, and optional KRS endpoint mode exactly as in the reference fork. KIRO cache emulation should be driven by group settings and should inject simulated cache usage into the translated Claude usage object.

OAuth/token logic should use the reference fork's `KiroOAuthService`, `KiroTokenProvider`, and token refresher pattern, integrated with the local account repository and token cache interfaces.

## Data Flow

1. A request is parsed by the existing gateway flow.
2. If the selected account is a direct KIRO account, the gateway calls the reference-style KIRO runtime path.
3. The KIRO runtime path maps the requested model, builds an Anthropic-compatible body where needed, constructs the KIRO payload, attaches KIRO headers, and sends the request to the configured KIRO endpoint.
4. The KIRO response stream is parsed by `internal/pkg/kiro` and written back in the requested client format.
5. Usage, KIRO credits, cache emulation usage, cooldown state, and account usage snapshots are recorded through existing local DEV persistence paths.

## Error Handling

KIRO HTTP and runtime errors should use the reference fork's classifier. Transient 429 and monthly request-count limits should update KIRO cooldown state so schedulers can skip blocked accounts. Auth/profile/quota errors should produce actionable upstream error events without disabling unrelated accounts.

## Testing

The replacement should import the reference fork's KIRO-focused unit tests where compatible, then adapt only test harness details required by local DEV interfaces. Verification should include:

- KIRO package tests.
- KIRO service tests.
- Admin group KIRO validation tests.
- Backend package tests that cover touched dependency wiring.
- Frontend typecheck or targeted tests when frontend KIRO files change.

## Documentation

Add a root `AGENTS.md` that documents the KIRO tracking rule: future KIRO gateway changes must check `https://github.com/nianzs/sub2api` first, especially `backend/internal/pkg/kiro`, `backend/internal/pkg/kirocooldown`, and `backend/internal/service/kiro_*.go`.
