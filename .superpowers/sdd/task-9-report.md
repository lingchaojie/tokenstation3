# Task 9 Report: Safe Post-Authentication Return Paths

## Scope

- Added one shared post-auth redirect resolver for Login, two-factor Login, direct Register, email-verification Register, and EmailVerify completion.
- Preserved `/dashboard` as the default destination and did not change router guards or `BACKEND_MODE_ALLOWED_PATHS`.
- Preserved anonymous beginner-guide progress by returning successful authentication to `/getting-started`, where the existing guide store performs its authenticated merge.
- Added only `pending_redirect` to the existing `register_data` handoff. No guide progress, API key, selected-key ID, generated configuration, or command is added to query, route history, or the handoff.
- Left `graphify-out/` untouched; graph refresh is deferred to Task 13 as instructed.

## TDD Evidence

### Redirect resolver RED

Command:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/authRedirect.spec.ts
```

Observed failure: the suite could not resolve the missing `@/router/authRedirect` module. After the minimal helper was added, its 17 initial cases passed.

### Login and Register RED

Command:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/authRedirect.spec.ts
```

Observed result: 4 failed, 19 passed.

- Duplicate Login query values were pushed as an array instead of falling back to `/dashboard`.
- Two-factor Login pushed `//evil.example` instead of falling back.
- Direct Register ignored `/getting-started` and pushed `/dashboard`.
- Email-verification Register did not store `pending_redirect`.

After wiring Login and Register to the shared resolver: 23 passed.

### EmailVerify RED

Command:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/authRedirect.spec.ts src/views/auth/__tests__/EmailVerifyView.spec.ts
```

Observed result: 1 failed, 31 passed. EmailVerify pushed `/%252Fevil.example` unchanged instead of `/dashboard`.

After resolving the stored value immediately before navigation: 32 passed.

## Security Contract

The shared resolver:

- accepts trimmed internal absolute paths such as `/getting-started` and `/profile?tab=security`;
- rejects missing values, non-strings, duplicate-query arrays, relative paths, external URLs, protocol-relative URLs, and leading backslashes;
- rejects encoded and repeatedly encoded leading slash/backslash variants;
- returns the caller's existing internal fallback, `/dashboard` by default.

## Verification

Focused auth/router/guide/store regression command:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec vitest run src/router/__tests__/authRedirect.spec.ts src/router/__tests__/getting-started-route.spec.ts src/views/auth/__tests__/EmailVerifyView.spec.ts src/views/auth/__tests__/AuthLinearShell.spec.ts src/views/public/__tests__/GettingStartedView.spec.ts src/components/getting-started/__tests__/GuideApiKeyStep.spec.ts src/stores/__tests__/beginnerGuide.spec.ts
```

Result: 7 test files passed, 106 tests passed.

Additional checks:

```bash
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend run typecheck
COREPACK_ENABLE_PROJECT_SPEC=0 pnpm --dir frontend exec eslint src/router/authRedirect.ts src/router/__tests__/authRedirect.spec.ts src/views/auth/LoginView.vue src/views/auth/RegisterView.vue src/views/auth/EmailVerifyView.vue src/views/auth/__tests__/EmailVerifyView.spec.ts
git diff --check -- <Task 9 paths>
```

Result: all commands exited successfully with no type, lint, or whitespace errors. The only recurring test output was the repository's existing stale Browserslist-data notice.
