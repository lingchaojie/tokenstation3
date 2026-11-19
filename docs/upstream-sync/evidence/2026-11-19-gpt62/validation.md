# GPT-6.2 validation — 2026-11-19

Local base: `b3bf9419b372af1d3e0a3948e93fcec5ba103d25`. Tools: Go 1.27.3, golangci-lint 2.14.0 built with Go 1.27.3, pnpm 9.15.9.

| Check | Command | Result |
|---|---|---|
| Backend unit | `GOMAXPROCS=4 go test -p 2 -timeout=20m -tags=unit ./...` in backend | exit 0 |
| Backend integration | `GOMAXPROCS=3 go test -p 1 -timeout=20m -tags=integration ./...` in backend | exit 0; local Docker PostgreSQL/Redis harness |
| Backend lint | `GOMAXPROCS=2 golangci-lint run --concurrency=1 --timeout=10m ./...` in backend | exit 0; 0 issues |
| Embedded backend build | `GOMAXPROCS=2 CGO_ENABLED=0 go build -p 1 -tags=embed -trimpath -o <ignored-verifier> ./cmd/server` in backend | exit 0 |
| Frontend tests | `pnpm exec vitest run --maxWorkers=2 --minWorkers=1` in frontend | 444 files, 3,563 tests passed |
| Frontend lint | `pnpm run lint:check` | exit 0 |
| Frontend typecheck | `pnpm run typecheck` | exit 0 |
| Frontend build | `pnpm run build` | exit 0 |
| Diff whitespace | `git diff --check` | exit 0 |

The local embedded verification binary has SHA256 `ccc4d9c3299aa6e35be4211d10e43acf861866ea413b5a70229f144a4c1c31a8`. It is a build check, not a published deployment artifact. The release workflow must build the final reviewed commit and pin its own immutable image digest.

The two Codex descriptors are byte-identical to the pinned upstream tree. A parsed comparison of the complete bundled pricing JSON confirmed that all existing entries are unchanged and only the two new model IDs were added. No dependencies or migrations changed; the original checkout retains its pre-existing `.gitignore` and `AGENTS.md` edits.

Test-first evidence: initial GPT62 cases failed for absent identities/prices/capabilities; the Headers case failed with actual TUI vs required CLI; public catalog and frontend presets/export cases failed for absent models. The mapped raw-Chat-Completions sampling case failed on temperature passthrough before normalization was added. The first whole unit run identified three additional stale TUI assertions (compact HTTP, SetupToken Messages, WebSocket); only expected identities changed, then the whole suite passed.

Coverage includes three pricing sources, aliases/lookalikes, Standard/Fast/Ultra costs across dates, cache buckets, long-context boundaries and credential exemption, actual record-usage calculation, subscription plan entitlements, API vs OAuth Codex transport, public cards and client exports. All provider-facing tests use fixtures. No real provider calls or production mutations were performed.

Independent review: pending.
