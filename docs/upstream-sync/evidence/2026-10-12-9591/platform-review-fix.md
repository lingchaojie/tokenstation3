# Independent review F2 repair evidence

Task scope: narrowly repair backend review F2 in `cline_usage.go` and corresponding tests. No schema/interface/generated-code changes, database/production access, staging, commit or push. Root owns heartbeat repair, full final validation and fresh backend review.

Coordinates: merge production `896ca30d6b809f72ddb20a455e2410508792183d`; docs HEAD at all focused checks `8a181834da9cf656951ffb59e8f2c47150f97b43`. Independent review source: `review-backend.md` F2; its original reviewed candidate was `75cada11fd365bbeccf5af45609b99e815e2e1d2`. Focused checks validate the two uncommitted repair files identified by hashes below, not the whole pending candidate.

## Root cause and narrow change

Read the actual path `QueryUsage` -> `queryClineUsage` -> `applyClineWallets` -> `setClineWalletLimit` -> `SetModelRateLimit`. The writer replaces the wallet scope JSON record (including reason and reset timestamp). The shortage branches previously rewrote an active opaque cap to a probe-owned shortage; a subsequent healthy probe then cleared that new reason without proving the original cap recovered.

`canReplaceClineWalletLimitFromProbe` now permits shortage writes only for inactive/expired restrictions or the reasons the same account probe can prove recovered: pass window-limit/no-subscription and credits exhausted/low. Active spend/inference/entitlement/unknown reasons keep their entire existing scope and reset deadline. Each wallet is evaluated separately, so preserving one restriction does not prevent the other wallet's normal update. Existing healthy-probe recovery and replacement of expired reasons remain. There is no new restriction representation or policy redesign.

Changed files:

- `backend/internal/service/cline_usage.go`: adds the replacement guard to both shortage branches.
- `backend/internal/service/cline_usage_test.go`: unit-tag tests exercise sequential real `QueryUsage` calls with controlled account API responses; they inspect actual account reason/deadline and scheduling eligibility. External HTTP/DB are replaced at existing test fixture boundaries; real probe parsing, wallet state changes and admission helpers execute.

## RED -> GREEN regressions

1. `TestClineUsageProbeShortageThenHealthyPreservesOpaqueWalletLimits`: six subtests `credits_spend`, `credits_inference`, `credits_unknown`, `pass_inference`, `pass_entitlement`, `pass_unknown`. Before the repair all six fail on replacement of the original restriction reason. Both shortage and healthy calls execute before assertions. After repair each retains the original one-hour deadline, reason and request-level blocked state.
2. `TestClineUsageProbeShortageReplacesExpiredOpaqueWalletLimits`: `cline:pass` and `cline:credits` controls verify expired opaque restrictions become fresh shortage limits and clear after healthy probe.
3. `TestClineUsageProbeShortageThenHealthyRecoversProvableWalletLimits`: `pass_window`, `pass_subscription`, `credits_exhausted`, `credits_low` controls verify active probe-recoverable limits continue normal shortage/healthy transitions.

The second and third controls passed before and after repair; the six first-test failures are expected assertion failures, not compile/setup errors.

## Commands, UTC timing and results

All checks ran sequentially with environment `GOTOOLCHAIN=go1.27.2 GOPROXY=https://goproxy.cn,direct GOMAXPROCS=2`, working directory the task worktree. Unit checks have no DB/cache/port/shared generated-output use. Files listed by each metadata record had identical before/after SHA256 during that run. The root's independent heartbeat test edits are outside this repair's file ownership.

| Check | Start UTC | End UTC | Go exit | Raw log / metadata |
| --- | --- | --- | --- | --- |
| RED | `2026-10-12T12:18:56.757907+00:00` | `2026-10-12T12:20:00.292326+00:00` | 1 | `platform-f2-red.log` / `platform-f2-red.json` |
| GREEN | `2026-10-12T12:21:22.338204+00:00` | `2026-10-12T12:22:28.732920+00:00` | 0 | `platform-f2-green.log` / `platform-f2-green.json` |
| Full focused Cline | `2026-10-12T12:23:03.978042+00:00` | `2026-10-12T12:23:09.436285+00:00` | 0 | `platform-f2-cline-suite.log` / `platform-f2-cline-suite.json` |

Regression command (RED and GREEN):

```bash
go -C backend test -count=1 -p=2 -tags=unit ./internal/service -run '^TestClineUsageProbe(ShortageThenHealthy|ShortageReplacesExpired)' -v
```

Full focused Cline command:

```bash
go -C backend test -count=1 -p=2 -tags=unit ./internal/service -run '^Test(Cline|ClassifyCline|HandleUpstreamError_Cline|AccountTestService_Cline|CNProviderBalanceCheckRunOnce(ProbesClineUsage|DispatchesAccountProbesByPlatform))' -v
```

Full focused suite: 27 top-level tests passed, zero failure/skip markers. Existing gateway/test-model/error classification/wallet isolation/credits conversion/probe/auth/periodic dispatch recovery behavior included.

Exact passing top-level test names:

- `TestClineProfileSingleModeChatCompletionsOnly`
- `TestClineGatewayForwardsToChatCompletions`
- `TestAccountTestService_ClineDefaultTestModel`
- `TestClassifyClineError`
- `TestClinePassLimitCooldown`
- `TestClineWalletRateLimitKeys`
- `TestHandleUpstreamError_ClineCoolsDownWallet`
- `TestHandleUpstreamError_Cline402KeepsAccountWhenCooldownWriteFails`
- `TestClineBalanceProbeConvertsMicrocredits`
- `TestClineBalanceProbeFailures`
- `TestClineAccountAPIRequiresOfficialHost`
- `TestClineUsageProbeWritesWindowsAndBalance`
- `TestClineUsageProbeCoolsDownExhaustedWallets`
- `TestClineUsageProbeWithoutSubscription`
- `TestClineUsageProbeClearsRecoveredWallets`
- `TestClineUsageProbeClearsOnlyProvablePassCooldowns`
- `TestClineUsageProbeAuthFailure`
- `TestCNProviderBalanceCheckRunOnceProbesClineUsage`
- `TestCNProviderBalanceCheckRunOnceDispatchesAccountProbesByPlatform`
- `TestClineOfficialHostSkipsUpstreamBillingProbe`
- `TestClineUsageProbeShortageThenHealthyPreservesOpaqueWalletLimits`
- `TestClineUsageProbeShortageReplacesExpiredOpaqueWalletLimits`
- `TestClineUsageProbeShortageThenHealthyRecoversProvableWalletLimits`
- `TestClassifyClineErrorRequiresStructuredFields`
- `TestHandleUpstreamError_Cline400DoesNotCoolWallet`
- `TestHandleUpstreamError_Cline429EchoedKeywordsDoNotCoolWallet`
- `TestHandleUpstreamError_ClineStructuredErrorsPreserveCooldown`

## SHA256 and handoff

| File | RED | GREEN / focused Cline |
| --- | --- | --- |
| `backend/internal/service/cline_usage.go` | `739d51220805c9391d501d5a953f7a5543ff968e478e79c7930cf1e195130ef1` | `c8477bf2a3448b1f5277869f5e599885c1627bae2a074a58a4a19ba32726a5d4` |
| `backend/internal/service/cline_usage_test.go` | `de9896b71e67f7eb9ffa575296804f68a1d9519075a44fdfb549af8e2bf452e4` | `de9896b71e67f7eb9ffa575296804f68a1d9519075a44fdfb549af8e2bf452e4` |
| `backend/internal/service/cline_test.go` | `unchanged existing test; see full suite metadata` | `d371bdc9d7e46fdeb36e31722c8745ec60bc2644c7c35cf547295f097ecbcb08` |
| `backend/internal/service/ratelimit_cline_test.go` | `unchanged existing test; see full suite metadata` | `c60bcdf51d77411c177ea8e2337a75b7749299b1ebee93594941fb321ecf0b5d` |

`gofmt` and `git diff --check` passed for both repair files. No focused test processes remain running. Full final checks and fresh independent whole-backend review remain root-coordinated release gates; this scoped author repair evidence does not claim SAFE TO PUSH.
