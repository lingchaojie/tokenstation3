# Gateway integration evidence — 2026-09-25 a3eb

Implementation worker: `/root/gateway_integration`. This is domain implementation/audit evidence, **not** the independent final review or permission to push.

## Coordinates and scope

- Worktree: `.worktrees/audit-sub2api-20260925-a3eb`.
- Merge base: `881f3202694c6bc932446931a30c27d9675178b9`.
- Local parent: `a892514e488bb880987aba7bec3b102d9964fb23`.
- Upstream parent: `a3eb7ef302961cba716dc78b39b93b60c467db0e`.
- Read repository instructions, KIRO reference guide, sync skill, approved integration plan, and risk assessment.
- Initial forwarding ownership excludes `gateway_usage_billing.go` and `openai_gateway_usage.go`; billing worker owns those. Coordinator delegated handler tests, display-only models tests, expanded OpenAI service audit, and the final Lite/capture repair.
- No provider calls, production writes, commits, pushes, or additional workers were performed by this worker.

## Semantic integration decisions and traced flows

### Anthropic / KIRO / protocol conversion

Model mapping precedes model-dependent conversion. Local KIRO direct/native forwarding, KIRO compaction, relay request construction, capture ownership and terminal capture remain distinct. Anthropic relay reasoning effort is extracted from the final body returned by upstream request construction, not the ingress body; the same actual body feeds capture. Compaction does not claim an unforwarded effort. Opus55 signed-thinking preservation and tool-choice handling coexist with local compaction. A test exposed an earlier OAuth normalization branch stripping tool_choice before the new Opus55 exception; the earlier branch now honors that exception.

Responses conversion retains local refusal and cache/compaction structures. Stream accumulation retains bounded builders and the shared output-retention budget. Upstream signed thinking/redacted data/signature deltas are charged to that same budget, preventing signature content from bypassing the local memory bound. Thinking and tool argument deltas finalize from their accumulated builders; ordinary opaque encrypted reasoning is not mistaken for the marked Anthropic envelope.

### Gemini / Antigravity

Native and compatibility requests derive explicit reasoning levels from the actual outbound Gemini body. Local Chat -> Responses -> Anthropic conversion retains output_config.effort and converts high to Gemini thinkingLevel high; the upstream test expecting that field to be absent was semantically incompatible, so it now verifies actual forwarded high and the configured 2x price. Missing/unsupported native effort cases continue proving no multiplier.

Transport failure finishes capture, aborts the failed typed attempt, and returns the shared failover classification. Client cancellation is not turned into retry/eviction. countTokens preserves its local estimate fallback only for retryable transport failures. Vertex retryInfo cooldown is bounded; Go/Python-genai SSE comments are handled without changing payload/usage semantics. Models-list tests use the local display-only ModelsListConfig plus discovered Antigravity account mappings, never resurrect the removed group request allowlist.

### OpenAI request, stream, capture and cancellation

Final request construction retains DeepSeek reasoning placeholders, image/media conversion, strict-provider developer-role normalization, GPT6 compatibility/cache fields, nested tool-schema repair, final header identity/UA ordering and GPT5.5 Lite normalization. Model aliases are restored only in client-visible model fields; raw upstream model observation and capture occur before response rewriting. Plugin/Composite transport/catalog paths, Grok audio and independent x_search remain excluded; the ordinary HTTP forwarding helper lives outside optional plugin files.

Raw Chat retains the local asynchronous scanner, staging/commit boundary, typed capture, WebChat behavior, sparse usage merge, and joined cleanup rather than adopting a duplicate synchronous scanner loop. Native/passthrough Responses processors stop at a successful framed terminal without waiting for provider EOF. Their early-success branch must not enter the local parser-failure drain; a hanging-body regression verifies completion, usage and close. Failed terminal handling still performs the applicable local error/capture logic. Keepalive does not count as visible first token; WS keepalive does not commit staged lifecycle events, and no retry is attempted after client disconnect. Shared error frames use upstream protocol fields after commitment while pre-output errors preserve local failover behavior.

The upstream shared CC sender detached cancellation, conflicting with the local canceled-request invariant. Full unit evidence exposed this; request cancellation propagation was restored at the CC send boundary. Available usage can still be drained after a write failure while the request context remains live. Transport response-body close still owns cancellation of its child resources.

The automatic GPT5.5 Lite request rewrite changed req.Body/GetBody after the caller selected its body byte slice. A new test reproduced typed capture recording 132 stale bytes instead of the actual 66-byte request. The repair splits typed capture initialization from payload writing and replays the final GetBody into capture in fixed 32KiB chunks. It never consumes live req.Body or buffers another full request. Model/stream metadata remain unchanged by Lite normalization. Replay-open/read errors abort capture without changing forwarding; regressions verify cleanup and wire-body preservation.

### Expanded automatic OpenAI service audit

- Scheduler: nullable OAuth override distinguishes absent/cleared/zero; account billing rate is the defined fallback, with invalid/non-finite guards. Legacy previous_response ownership lookup flows through existing capability/quota/profit veto and attached profit-gate handling, then group/privacy/runtime/proxy/transport checks; rejection releases an acquired slot. Sticky decision fields are populated.
- Images: compatible Gemini image models use the API-key capability fence after mapping; no Composite context rewrite. Explicit structured insufficient_balance signals alone trigger the image-model cooldown and retry outcome; prompt text cannot impersonate an error code.
- Metadata/identity: ASCII JSON encoding preserves Unicode meaning while making turn metadata header-safe. OpenCode/CommandCode UA selection uses trusted platform or exact destination host; explicit allowed account overrides retain their ordering.
- Quota: nullable decimal-string Codex credits are independent of reset cards; unavailable details do not fabricate zero balances. Snapshot writes target the queried row (including shadows); successful missing credit data invalidates stale credit snapshots.
- Referral: business interface receives credentials/proxy through existing preparation. Send validates a single email, selected program, eligibility, known capacity and confirmation; shadow send is rejected; unknown send outcomes are not retried.
- Item IDs, prompt cache and tool schemas: narrow GPT family/ID updates and schema-only null-required traversal retain payloads and local alias behavior. Test changes for Unicode, role precision, image controls, final aliases, rate fallback and credit unknown/null cases were checked against their production consumers.

## Verification

All commands run from `backend/` unless noted.

- `go test ./internal/pkg/apicompat`: PASS (0.040s); includes new signed-thinking retention-budget regression.
- `go test -tags=unit ./internal/handler -run 'Test(Gemini|AppendUpstreamGemini|GatewayModels_GPT6|CodexModels.*Gemini|OpenAIResponsesWebSocket.*)' -count=1`: PASS (6.573s).
- `go test -tags=unit ./internal/service -run 'Test(Gateway|Gemini|Antigravity|OpenAI|Openai|Codex|BuildCodex|.*Reasoning.*)' -count=1`: PASS (109.624s), raw output `/tmp/gateway-suite-20260925.log`.
- `go test -tags=unit ./internal/service -run 'Test(Mapped|NormalizeStrict|ForwardAsChatCompletions_Strict|ApplyOpenCode|LegacySchedulerDecision|QueryUsage|Cache.*Credits|CachePostReset|ResetCredit|PrepareUpstreamCall|ParseOpenAIRateLimit|SanitizeResponses|DropNull|.*ToolSchema|.*SignedThinking)' -count=1`: PASS (0.448s).
- `go test -tags=unit ./internal/service -run 'Test(OpenAICapture|OpenAIHTTPCapture|FinalizeOpenAIForwardResult|.*CaptureAttempt|.*TypedCapture|GatewayAnthropicCompatReasoning|GeminiChatCompatReasoning)' -count=1`: PASS (4.064s), after capture production repair.
- `go test -tags=unit ./internal/service -run 'Test(.*Capture.*|.*Kiro.*|.*KIRO.*|.*WebChat.*|SanitizeOpenAIResponses.*|NormalizeOpenAI.*|TrimOpenAI.*)' -count=1`: PASS (21.705s), raw output `/tmp/gateway-local-invariants-20260925.log`.
- New Lite-capture regression: observed FAIL before repair (132 vs 66 bytes), PASS afterward. `go test -tags=unit ./internal/service -run '^TestOpenAICapture' -count=1`: PASS (0.020s), including replay failure cleanup.
- `go test -tags=unit ./internal/service -run 'Test(ForwardAsRawChatCompletions|ForwardAsChatCompletions|.*DeepSeek|Gemini|OpenAICapture)' -count=1`: PASS (8.525s), after cancellation restoration and Gemini lint cleanup; raw output `/tmp/gateway-final-fixes-20260925.log`.
- `git diff --check`: PASS; scoped conflict-marker scan returned no matches.
- Coordinator owns complete generated-code/build/lint/full unit/integration and independent review gates. Earlier shared-package compile blockers were resolved with owners rather than bypassed. No baseline failure is asserted here.

## Coverage manifest

The following initial/delegated files were inspected for both conflict and automatic-merge changes, with relevant current call chains and local preservation boundaries traced as described above. New tests and capture helper repair are included. Paths are relative to worktree root.

```text
backend/internal/handler/gateway_models_test.go
backend/internal/handler/gemini_mixed_models_test.go
backend/internal/handler/gemini_v1beta_handler.go
backend/internal/handler/gemini_v1beta_handler_test.go
backend/internal/handler/openai_gateway_handler_test.go
backend/internal/pkg/apicompat/anthropic_responses_test.go
backend/internal/pkg/apicompat/anthropic_signed_thinking_retention_test.go
backend/internal/pkg/apicompat/anthropic_to_responses.go
backend/internal/pkg/apicompat/anthropic_to_responses_response.go
backend/internal/pkg/apicompat/anthropic_to_responses_stream_test.go
backend/internal/pkg/apicompat/chatcompletions_to_responses.go
backend/internal/pkg/apicompat/responses_to_anthropic_instructions_test.go
backend/internal/pkg/apicompat/responses_to_anthropic_request.go
backend/internal/pkg/apicompat/responses_to_anthropic_tool_pairing_test.go
backend/internal/pkg/apicompat/responses_to_anthropic_tool_schema.go
backend/internal/pkg/apicompat/responses_to_anthropic_tools_test.go
backend/internal/pkg/apicompat/responses_tool_output_media.go
backend/internal/pkg/apicompat/responses_tool_output_media_test.go
backend/internal/pkg/apicompat/types.go
backend/internal/service/antigravity_gateway_claude.go
backend/internal/service/antigravity_gateway_compat.go
backend/internal/service/antigravity_gateway_gemini.go
backend/internal/service/antigravity_gateway_service.go
backend/internal/service/antigravity_gateway_streaming.go
backend/internal/service/capture_context.go
backend/internal/service/gateway_anthropic_apikey_passthrough_test.go
backend/internal/service/gateway_billing_header.go
backend/internal/service/gateway_billing_header_test.go
backend/internal/service/gateway_claude_oauth_body.go
backend/internal/service/gateway_cli_version_runtime_test.go
backend/internal/service/gateway_compat_reasoning_pricing_test.go
backend/internal/service/gateway_count_tokens.go
backend/internal/service/gateway_forward.go
backend/internal/service/gateway_forward_as_chat_completions.go
backend/internal/service/gateway_forward_as_responses.go
backend/internal/service/gateway_forward_as_responses_test.go
backend/internal/service/gateway_image_reasoning_pricing_test.go
backend/internal/service/gateway_multiplatform_test.go
backend/internal/service/gateway_reasoning_pricing_test.go
backend/internal/service/gateway_request.go
backend/internal/service/gateway_scheduling.go
backend/internal/service/gateway_service.go
backend/internal/service/gateway_simple_mode_record_usage_test.go
backend/internal/service/gateway_thinking_budget_test.go
backend/internal/service/gateway_upstream_request.go
backend/internal/service/gateway_upstream_response.go
backend/internal/service/gateway_upstream_transport_error.go
backend/internal/service/gateway_usage_billing_simple_mode_test.go
backend/internal/service/gemini_chat_completions_compat_service.go
backend/internal/service/gemini_error_policy_test.go
backend/internal/service/gemini_messages_compat_service.go
backend/internal/service/gemini_messages_compat_service_test.go
backend/internal/service/gemini_native_reasoning_pricing_test.go
backend/internal/service/gemini_reasoning_effort.go
backend/internal/service/gemini_sse_comment_compat.go
backend/internal/service/gemini_sse_comment_compat_test.go
backend/internal/service/gemini_upstream_transport_error.go
backend/internal/service/gemini_upstream_transport_error_test.go
backend/internal/service/openai_codex_model_metadata_test.go
backend/internal/service/openai_codex_models_service.go
backend/internal/service/openai_codex_models_service_test.go
backend/internal/service/openai_gateway_cc_pipeline.go
backend/internal/service/openai_gateway_chat_completions.go
backend/internal/service/openai_gateway_chat_completions_anthropic_native.go
backend/internal/service/openai_gateway_chat_completions_raw.go
backend/internal/service/openai_gateway_chat_completions_raw_test.go
backend/internal/service/openai_gateway_chat_completions_test.go
backend/internal/service/openai_gateway_converted_reasoning_pricing_test.go
backend/internal/service/openai_gateway_deepseek_chat_reasoning_test.go
backend/internal/service/openai_gateway_deepseek_input_image_test.go
backend/internal/service/openai_gateway_forward.go
backend/internal/service/openai_gateway_grok.go
backend/internal/service/openai_gateway_grok_chat_bridge.go
backend/internal/service/openai_gateway_messages.go
backend/internal/service/openai_gateway_messages_anthropic_native.go
backend/internal/service/openai_gateway_messages_chat_fallback.go
backend/internal/service/openai_gateway_passthrough.go
backend/internal/service/openai_gateway_request_body.go
backend/internal/service/openai_gateway_response_flush_test.go
backend/internal/service/openai_gateway_response_handling.go
backend/internal/service/openai_gateway_responses_anthropic_native.go
backend/internal/service/openai_gateway_responses_chat_fallback.go
backend/internal/service/openai_gateway_scheduling.go
backend/internal/service/openai_gateway_service_hotpath_test.go
backend/internal/service/openai_gateway_service_test.go
backend/internal/service/openai_gateway_upstream_errors.go
backend/internal/service/openai_http_capture.go
backend/internal/service/openai_http_capture_test.go
backend/internal/service/openai_images_compatible_test.go
backend/internal/service/openai_upstream_transport_error.go
backend/internal/service/openai_ws_http_bridge.go
backend/internal/service/openai_ws_http_bridge_test.go
backend/internal/service/upstream_user_agent_recorder_test.go
```

Expanded OpenAI automatic-region audit (no edits by this worker except separately listed delegated tests); excludes coordinator-owned usage implementation:

```text
backend/internal/service/openai_access_state_failover_test.go
backend/internal/service/openai_account_runtime_block_fastpath.go
backend/internal/service/openai_account_scheduler.go
backend/internal/service/openai_account_scheduler_canonical_quota_test.go
backend/internal/service/openai_account_scheduler_upstream_cost_test.go
backend/internal/service/openai_capacity_shed_test.go
backend/internal/service/openai_chat_roles.go
backend/internal/service/openai_chat_roles_test.go
backend/internal/service/openai_codex_account_identity.go
backend/internal/service/openai_codex_fingerprint.go
backend/internal/service/openai_codex_transform.go
backend/internal/service/openai_codex_turn_metadata.go
backend/internal/service/openai_codex_turn_metadata_test.go
backend/internal/service/openai_codex_version_sync_service_test.go
backend/internal/service/openai_compat_prompt_cache_key.go
backend/internal/service/openai_compat_prompt_cache_key_test.go
backend/internal/service/openai_images.go
backend/internal/service/openai_images_balance.go
backend/internal/service/openai_images_balance_test.go
backend/internal/service/openai_legacy_scheduler_decision_test.go
backend/internal/service/openai_lite_mapped_gpt55.go
backend/internal/service/openai_lite_mapped_gpt55_test.go
backend/internal/service/openai_model_alias.go
backend/internal/service/openai_opencode_session.go
backend/internal/service/openai_opencode_session_test.go
backend/internal/service/openai_passthrough_normalization_test.go
backend/internal/service/openai_quota_credits_test.go
backend/internal/service/openai_quota_reset_credits_test.go
backend/internal/service/openai_quota_service.go
backend/internal/service/openai_quota_spark_window_test.go
backend/internal/service/openai_referral_client.go
backend/internal/service/openai_referral_service.go
backend/internal/service/openai_referral_service_test.go
backend/internal/service/openai_response_model_rewrite_test.go
backend/internal/service/openai_responses_item_id.go
backend/internal/service/openai_responses_item_id_test.go
backend/internal/service/openai_responses_tool_schema.go
backend/internal/service/openai_responses_tool_schema_test.go
backend/internal/service/openai_scheduling_rate_fallback_test.go
backend/internal/service/openai_visible_ttft_test.go
backend/internal/service/openai_ws_forwarder_ingress_execution_scope_test.go
```

## Remaining gates

Domain changes still require the coordinator's final whole-repository checks and an independent fresh reviewer. This record makes no merge/push/CI completion claim.
