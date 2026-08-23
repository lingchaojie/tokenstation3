# Cursor Fork Parity Matrix

## Reference and interpretation

- Behavior source: `SJwen0/cursor--@3709f0f6c83ed84b62c2a0f7f8e1ff63d6cfb7d4`.
- Imported fork range: `8b628eb20..3709f0f6c` (inclusive).
- Local integration base: refreshed `origin/dev@f768645be81754a170eaa48b8dd889692ef40473`.
- The integration is a semantic port, not a file-level cherry-pick. The tests below are the executable authority for the behavior retained on current DEV.
- Connect/Protobuf exists only between sub2api and Cursor. Public responses and capture records stay in the caller's protocol.

All names below are exact test names in this branch. A test can cover more than one fork commit when a later fork commit tightened an earlier contract.

## Commit-to-test map

### `8b628eb20` — initial Cursor platform, credentials, models, and Agent Run forwarding

Core platform and wire behavior:

- `TestCursorPlatformRegistration` — `backend/internal/service/cursor_platform_test.go`
- `TestCursorPlatformMigration` — `backend/migrations/cursor_platform_migration_test.go`
- `TestEncodeFrameRoundTrip` and `TestFrameReaderRecognizesAllConnectFlags` — `backend/internal/pkg/cursor/envelope_test.go`
- `TestEncodeAgentRunRequestCarriesCoreFields` — `backend/internal/pkg/cursor/agent_request_test.go`
- `TestParseAgentServerMessageEventEnums` — `backend/internal/pkg/cursor/agent_response_test.go`
- `TestOpenAgentStreamUsesPipeBodyAndStreamingHeaders` and `TestOpenAgentStreamHTTP2ReadsResponseWhileRequestStaysOpen` — `backend/internal/pkg/cursor/agent_stream_test.go`
- `TestBuildAgentHeadersSendsExactlyTenCLIHeaders` — `backend/internal/pkg/cursor/agent_const_test.go`

Credential, model, and gateway behavior:

- `TestCursorOAuthServiceImportAPIKey` and `TestCursorOAuthServiceCookieImportAndUpgradeRetainWebSession` — `backend/internal/service/cursor_oauth_service_test.go`
- `TestCursorObservedModelsSyncUsesRawUnaryAndPreservesAccountDocuments` — `backend/internal/service/cursor_observed_models_test.go`
- `TestCursorRunParamsIdenticalAcrossInboundProtocols` — `backend/internal/service/openai_gateway_cursor_bridges_test.go`
- `TestCursorDispatchPublicEntrypointsPreserveCallerProtocolAndCapture` — `backend/internal/service/openai_compatible_cursor_test.go`
- `polls the primary deep link through pending to success and stores only normalized credentials` — `frontend/src/components/account/__tests__/CreateAccountModal.cursor.spec.ts`

### `a085dcf8b` — platform validation and group binding completeness

- `TestCursorPlatformRegistration` — proves Cursor is in the concrete/quota platform catalogs but not the unsupported scheduling-threshold catalog.
- `TestCursorGroupOAuthOnlyRejectsAPIKeyBindingsBeforeBindGroups` — `backend/internal/service/cursor_account_test.go`
- `TestCursorBillingQuotaProfitAndThresholdContracts` — `backend/internal/service/openai_compatible_cursor_test.go`
- `registers Cursor as an account and quota platform only` — `frontend/src/constants/__tests__/platforms.spec.ts`

The fork's deployment/E2E prose is replaced by the local operator runbook, `docs/CURSOR_FORWARDING_RUNBOOK_CN.md`, whose verification commands use local fixtures only.

### `24d48450e` — gateway model guidance and idle-timeout warning

- `TestCursorAgentWireModelPreservesCursorIDsAndObservedThinkingVariants` — `backend/internal/service/openai_gateway_cursor_translate_test.go`; proves caller `auto` becomes Cursor wire model `default` without applying Codex model rewriting.
- `TestAgentStreamDefaultsIncludeThirtySecondIdleAndDrainWindow` — `backend/internal/pkg/cursor/agent_stream_test.go`; supersedes the fork-era 4/15-second warning with the final 30-second default.

### `ec176befd` — 30-second activity-based idle fallback and authoritative end

- `TestAgentStreamExplicitEndBeatsIdleTimeout`
- `TestAgentStreamStopsAtTurnEnded`
- `TestAgentStreamIdleTimeoutResetsOnEveryResponseFrame`
- `TestAgentStreamDefaultsIncludeThirtySecondIdleAndDrainWindow`

All four tests are in `backend/internal/pkg/cursor/agent_stream_test.go`. Together they prove that every response frame refreshes activity, an upstream end frame wins immediately, and idle expiry is only a stuck-stream fallback.

### `d87149806` — standalone upstream diagnostic CLI and QUICKSTART

The standalone `backend/cmd/cursor_e2e` program and root `QUICKSTART.md` are intentionally excluded. The diagnostic intent is covered without a live provider call by:

- `TestCursorAccountTestUsesRawAvailableModelsAndPersistsAfterReadiness` — `backend/internal/service/account_test_service_cursor_test.go`
- `TestCursorDispatchHandlerAttemptLifecycleAndFailover` — `backend/internal/handler/openai_cursor_dispatch_integration_test.go`
- `TestCursorDispatchPublicEntrypointsPreserveCallerProtocolAndCapture` — `backend/internal/service/openai_compatible_cursor_test.go`

The final scope guard rejects `cursor_e2e` and `QUICKSTART` additions in the branch diff.

### `5ffd09fdf` — same-account transient retry and three-protocol bridge parity

- `TestCursorAgentFailureTransientRetriesSameAccountBeforeFailover`
- `TestCursorAgentFailureCancellationNeverRetries`
- `TestCursorAgentFailureSeparatesMappedStatusFromActualHTTPProvenance`
- `TestCursorRunParamsIdenticalAcrossInboundProtocols`

These tests are in `backend/internal/service/openai_gateway_cursor_bridges_test.go`. `TestCursorDispatchHandlerAttemptLifecycleAndFailover` in `backend/internal/handler/openai_cursor_dispatch_integration_test.go` additionally exercises the handler/scheduler attempt sequence and confirms post-output failures are not replayed.

### `563fe0d52` — credential isolation, proxy safety, and token lifecycle

- `TestCursorAccountTestUsesRawAvailableModelsAndPersistsAfterReadiness` and `TestCursorAccountActiveUsageIsRejectedWithoutBearerForwarding` — prevent Cursor credentials from falling through to unrelated provider probes or usage APIs.
- `TestValidateCursorAgentHostUsesCurrentDEVAllowlistForCustomHosts` and `TestCursorAgentHTTPClientFailsClosedOnInvalidProxyAssociation` — `backend/internal/service/openai_gateway_cursor_transport_test.go`.
- `TestCursorObservedModelsSyncConfiguredProxyFailsClosed` — `backend/internal/service/cursor_observed_models_test.go`.
- `TestCursorTokenProviderRejectedFingerprintForcesRotation`, `TestCursorTokenProviderWaiterPollsCacheWithBackoff`, and `TestCursorTokenProviderNeverReturnsSameRejectedTokenAfterRefresh` — `backend/internal/service/cursor_token_provider_test.go`.
- `TestCursorOAuthServicePollPendingThenConfirmed` and `TestNormalizeCursorReauthorizedCredentialsReplacesMutuallyExclusiveSources` — `backend/internal/service/cursor_oauth_service_test.go`.
- `TestAccountRepository_ListOAuthRefreshCandidatePage_CursorRefreshCandidatesUseAlternateCredentialSources` — `backend/internal/repository/account_repo_temp_unsched_test.go`.

### `53294c5b3` — exact model IDs, OAuth-only accounts, authoritative observations, bounded gzip

- `TestCursorAgentWireModelPreservesCursorIDsAndObservedThinkingVariants` — preserves model IDs and thinking/max variants.
- `TestCursorAccountTypeValidatorRequiresExactlyOAuth`, `TestCursorAccountCreateRejectsEveryNonOAuthTypeBeforeWrite`, and `TestAdminCursorAccountUpdateRejectsEveryNonOAuthTypeBeforeMutation` — `backend/internal/service/cursor_account_test.go`; cover every supported non-OAuth account type at validation, admin create, account-service create, and explicit admin update boundaries.
- `TestCursorAccountServiceUpdateRejectsLegacyNonOAuthRowsBeforeMutation`, `TestAdminCursorAccountUpdateWithoutTypeRejectsLegacyNonOAuthRowsBeforeMutation`, `TestAdminCursorRecoveryRejectsEveryLegacyNonOAuthTypeBeforeMutation`, and `TestAdminCursorSchedulableToggleRejectsInvalidEnableButAllowsQuarantine` — the same file; close omitted-type update and admin recovery/enable paths while retaining quarantine.
- `TestSchedulableEntQueriesExcludeLegacyCursorNonOAuthAccounts`, `TestSchedulableCapacityQueryExcludesLegacyCursorNonOAuthAccounts`, `TestGroupAvailabilityQueriesExcludeLegacyCursorNonOAuthAccounts`, and `TestDashboardNormalAccountCountExcludesLegacyCursorNonOAuthAccounts` — `backend/internal/repository/cursor_oauth_schedulable_query_test.go`; prove legacy non-OAuth rows cannot re-enter scheduling, capacity, group availability, or normal-account counts.
- `TestCursorObservedModelsAreAuthoritativeNormalizedAndAliasDefault` and `TestGatewayCursorObservedSnapshotIsAuthoritativeAndFiltersAliases` — `backend/internal/service/cursor_observed_models_test.go`.
- `TestApplicationOwnsCursorObservedModelsLifecycle` — `backend/cmd/server/wire_gen_test.go`; `TestProvideCursorObservedModelsServiceWaitsForExplicitStartAndStartsOnce` and `TestCursorObservedModelsServiceStopBeforeStartPreventsWorkerLaunch` — `backend/internal/service/cursor_observed_models_test.go`. Together they prove Application-owned explicit start, single initial refresh, cancellation/join on cleanup, and no post-stop worker launch.
- `TestFrameReaderRejectsGzipExpansionOver64MiB` and `TestFrameReaderAcceptsGzipPayloadAt64MiB` — `backend/internal/pkg/cursor/envelope_test.go`.
- `renders Cursor as OAuth-only and fills its whitelist from observed account snapshots` — `frontend/src/components/account/__tests__/CreateAccountModal.cursor.spec.ts`.

### `d006da61d` — output limits, mid-stream failures, and complete usage estimates

- `TestCursorRequestOutputLimitBoundariesAndPrecedence` and `TestCursorInputEstimateIsDeterministicOverflowSafeAndDoesNotMutateRequest` — `backend/internal/service/openai_gateway_cursor_translate_test.go`.
- `TestConsumeCursorAgentEventsLocalLimitIsUnicodeSafeAndNotProviderTerminal` and `TestResolveCursorUsageAuthoritativeFallbackAndSaturation` — `backend/internal/service/openai_gateway_cursor_stream_test.go`.
- `TestCursorChatStreamingProviderErrorAfterOutputIsSafeTerminalFailure` — `backend/internal/service/openai_gateway_cursor_chat_test.go`.
- `TestCursorResponsesMidStreamFailureUsesNativeErrorWithoutCompleted`, `TestCursorAnthropicMidStreamFailureUsesNativeErrorWithoutMessageStop`, and `TestCursorResponsesAndAnthropicValidationIsNativeAndSecretFree` — `backend/internal/service/openai_gateway_cursor_bridges_test.go`.
- `TestCursorTerminalErrorCaptureStoresExactCallerProtocolDelivery` — `backend/internal/handler/openai_cursor_dispatch_integration_test.go`; covers all six caller protocol/mode combinations against both real non-2xx Agent responses and HTTP-200 Connect error trailers, and requires capture `RawResponse` to equal the exact delivered terminal bytes.

### `3709f0f6c` — route/catalog gaps, audit redaction, and parallel tool draining

- `TestCursorDispatchResponsesRoutesSelectCompatibleGateway` — `backend/internal/server/api_contract_test.go`.
- `TestCursorQuotaRequestPlatformAndMessagesDispatch` — `backend/internal/handler/openai_quota_platform_contract_test.go`.
- `TestCursorBillingQuotaProfitAndThresholdContracts` — `backend/internal/service/openai_compatible_cursor_test.go`.
- `TestCursorOAuthDeepLinkRoutesOmitAuditBody` — `backend/internal/server/middleware/audit_log_test.go`.
- `TestRedactAuditBody_CursorDeepLinkPasswordAndSSOFields` — `backend/internal/service/audit_log_test.go`.
- `TestCursorDeepLinkCanaryIsRedacted` and `TestCursorCredentialTextSpellingsAreRedacted` — `backend/internal/util/logredact/redact_test.go`.
- `TestAgentStreamParallelToolCallsDrainTogether` — `backend/internal/pkg/cursor/agent_stream_test.go`.
- `TestParseAgentServerMessagePreservesParallelMCPCalls` — `backend/internal/pkg/cursor/agent_response_test.go`.
- `TestCursorToolIdentityIsStableAcrossAllCallerProtocolsAndModes` — `backend/internal/service/openai_gateway_cursor_bridges_test.go`; proves preserved/synthesized IDs and duplicate suppression remain identical across Chat, Responses, and Messages in buffered and streaming modes.
- `TestConsumeCursorAgentEventsNormalizesToolIdentityIndependentOfEventOrder`, `TestConsumeCursorAgentEventsFlushesAcceptedToolsAtEveryTerminalBoundary`, and `TestConsumeCursorAgentEventsDoesNotEmitBufferedToolsAfterDownstreamError` — `backend/internal/service/openai_gateway_cursor_stream_test.go`; cover interleaved genuine/synthesized IDs, turn-end/channel-close/upstream-error/local-limit boundaries, and downstream-write failure.

## Local capture and route authority

Task 14 deliberately differs from a raw fork transport dump. The following behavior tests are authoritative:

- `TestCursorCaptureStoresSixCallerProtocolModes` — `backend/internal/service/openai_gateway_cursor_capture_test.go`; covers JSON and SSE for Chat Completions, Responses, and Anthropic Messages.
- `TestCursorCaptureTypedSinkExtractsStopReasonOnlyFromDeliveredBody` — `backend/internal/service/openai_gateway_typed_capture_test.go`.
- `TestCursorCapturePartialWriterStoresOnlyDeliveredPrefix` and `TestCursorCaptureProviderFailureAfterPartialWriteOutranksDisconnect` — `backend/internal/service/openai_gateway_cursor_capture_test.go`.
- `TestCursorDispatchPublicEntrypointsPreserveCallerProtocolAndCapture` — verifies public output and capture records contain caller JSON rather than Connect frames.

Task 15 route and catalog support/exclusion is exercised by real server and catalog behavior:

- `TestCursorDispatchResponsesRoutesSelectCompatibleGateway` — supported Chat Completions/Responses aliases and excluded OpenAI-only Responses subpaths.
- `TestCursorQuotaRequestPlatformAndMessagesDispatch` — Messages dispatch and request-time quota attribution.
- `TestCursorSchedulerPlatformSnapshotAndBulkTargetsAreFirstClass` — scheduler buckets and OpenAI-compatible platform selection.
- `registers Cursor as an account and quota platform only` — frontend account/quota catalogs without unsupported scheduling-threshold membership.

## Intentional exclusions

| Excluded surface | Rationale | Executable evidence |
| --- | --- | --- |
| Standalone `cursor_e2e` CLI, fork QUICKSTART, and fork OpenSpec package | A live provider diagnostic is not part of the service runtime and would bypass the application's normal account/proxy path. | `git diff origin/dev...HEAD --name-only \| rg 'cursor_e2e\|QUICKSTART\|openspec/changes/add-cursor-platform\|channelMonitor'` must produce no output. |
| Cursor channel monitor | The current monitor contract is API-key plus OpenAI-compatible HTTP; Cursor Agent Run is OAuth Connect/Protobuf and has no dedicated checker. | The same branch-diff guard rejects `channelMonitor` changes; `registers Cursor as an account and quota platform only` verifies the supported frontend catalogs. |
| Stateful Agent sessions | This port executes stateless turns. Tool continuations flatten the caller's complete history into the next request; it does not expose a session coordinator or `/v1/agents` route. | `TestBuildCursorAgentRunFlattensHistoryAndToolResultsInOrder` and `TestBuildCursorAgentRunStructuredTranscriptIsInjectionSafeAndOrdered` in `backend/internal/service/openai_gateway_cursor_translate_test.go`. |
| Raw Connect capture / a `connect_proto` capture format | Connect frames are an upstream implementation detail; capture is the caller-visible JSON/SSE delivery. | `TestCursorCaptureStoresSixCallerProtocolModes`, plus `git grep -nE 'connect_proto\|application/connect\+proto' -- backend/internal/capture frontend/src/constants/channelMonitor.ts` must produce no production registration hit. |

The shell checks are final scope guards only. They are intentionally not committed as source/path grep tests; the behavioral tests above remain the regression authority.
