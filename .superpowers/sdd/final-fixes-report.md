# API Documentation Final Fixes Report

Date: 2026-07-16
Branch: `feat/api-docs`

## Scope

This pass addressed the final review findings for the API documentation surface. It changes frontend documentation, navigation, search, and tests only. No backend runtime behavior was changed.

## Findings and fixes

### 1. Streaming error examples did not match the live gateway contracts

- Confirmed the contract against `backend/internal/handler/stream_error_event.go`, `gateway_handler.go`, and `openai_gateway_handler.go`.
- Updated the Responses example to use the `response.failed` event and its nested `response` object, including `id`, `object`, `model`, `status`, `output`, and nested `error`.
- Documented the distinct Anthropic-backed and OpenAI-backed non-Responses streaming error envelopes.
- Reused the shared OpenAI example model constant instead of introducing another model literal.
- Added source-backed regression assertions that verify the Go JSON tags/writer formats and parse the documented JSON into the exact expected structures.
- TDD evidence: the focused guide test failed against the old examples, then passed after the documentation change.

### 2. The public homepage API Docs entry was hidden on mobile

- Added a compact, localized `/docs` icon link below the `md` breakpoint while preserving the existing desktop link and external documentation link behavior.
- Hid the long brand wordmark below `sm` so the 390 px header does not overflow; the labeled logo remains visible.
- Added tests for mobile/desktop visibility classes, accessible naming, link targets, external-link coexistence, and custom homepage modes.
- TDD evidence: the new homepage test first failed because the mobile link was absent, then passed after implementation.
- Browser evidence: headless Google Chrome at 390x844 showed one visible API Docs link, the desktop link hidden, `scrollWidth === viewportWidth === 390`, and clicking the link navigated from `/home` to `/docs`.

### 3. Documentation hashes were not honored by router scrolling

- Added decoded hash lookup with a bounded animation-frame wait so asynchronously rendered headings can be found.
- Preserved saved-position precedence and top-of-page fallback.
- Applied a 128 px offset for API Docs routes so targets land below the sticky header.
- Added direct and in-app router tests for delayed targets, saved positions, and fallback behavior.
- TDD evidence: the new hash test failed before the router scheduled target lookup, then passed after implementation.
- Browser evidence: direct `/docs#available-models` and in-app navigation to `/docs#first-request` both scrolled the target into the viewport below the sticky header.

### 4. Unknown documentation slugs highlighted Quickstart

- Removed the `quickstart` fallback from `ApiDocsView` and made the sidebar/shell current-page identifier optional.
- Added a regression test that an unknown docs route has no `aria-current="page"` sidebar entry.
- TDD evidence: the new assertion failed with the old fallback and passed after implementation.

### 5. Search result categories exposed internal page-kind identifiers

- Added localized Guide, API reference, and Platform category labels in English and Chinese.
- Search entries now display and index the localized category.
- Added source-builder and Chinese integration assertions.
- TDD evidence: the tests first observed raw `guide`/`endpoint` labels, then passed with localized labels.

### 6. Final coverage gaps

- Added a direct router assertion that anonymous access to `/docs` redirects to the named Login route when the application is backend-only.
- Split the code-copy feedback timer coverage so the two-second reset and unmount cancellation are independently verified; unmount clears exactly one pending timer.
- Existing implementation already satisfied both behaviors once the missing regression coverage was added.

### 7. The homepage header still overflowed at 320 px

- Real-browser measurement isolated the remaining width pressure to the full header CTA: at 320 px the action group was 264.6 px wide, the CTA alone was 108.6 px, and its right edge landed at 340.6 px.
- Below 360 px, the CTA is now a fixed 40 px icon control. Visitor state shows an arrow icon; authenticated state preserves the visible user initial.
- The complete localized label remains in the DOM as screen-reader-only text and the link keeps its localized `aria-label`. At 360 px and wider, the original full label and padding return.
- The API Docs, locale, and theme controls retain their existing size and behavior; desktop navigation and custom-home branches are unchanged.
- TDD evidence: the visitor and authenticated responsive/semantic assertions failed against the old CTA classes, then all 22 HomeView tests passed after the minimal implementation.
- Browser evidence: Google Chrome at 320, 360, and 390 px reported `scrollWidth === clientWidth`; every header control was inside the viewport; exactly one `/docs` link was visible; and both the CTA and Docs link were successfully clicked. The direct and in-app docs hash smoke tests also continued to pass at 320 px.

## Verification

- API docs/navigation/locale/shared focused suite: 23 files, 241 tests passed.
- Follow-up homepage/API Docs focused suite: 10 files, 85 tests passed.
- Full frontend suite: 226 files, 1680 tests passed.
- Frontend lint: passed.
- Frontend typecheck: passed.
- Frontend production build: passed.
- Focused backend streaming-error handler tests: passed.
- Full backend `go test ./...`: passed.
- `git diff --check`: passed.
- Added-line credential scan: no matches. A whole-tree scan only matched a pre-existing synthetic private-key fixture in `EditAccountModal.spec.ts`.
- Backend diff check: no backend files changed.

## Knowledge graph

The source fixes and this report are committed first. The repository graph is then refreshed with the pinned `graphifyy==0.9.11` tool, validated, rerun for idempotency, and committed separately as generated output. The generated commit hash and command evidence are included in the final handoff.
