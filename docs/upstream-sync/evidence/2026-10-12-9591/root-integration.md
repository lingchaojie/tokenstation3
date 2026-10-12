# Root semantic integration coverage

Coordinates: ours c517f84cd, base b8dece9000c68815a5b867ca5a1e6f236e173905, upstream 9591136d45cef26453348ed9532a7ebbce7cf99c.

## Decisions and call chains

- BillingService.CalculateCostUnified and token calculation: preserve sparse-price validation, explicit-zero semantics, richer 5m/1h and image buckets, explicit effort override/default 1x, and interval/time pricing. Subscription exemption disables only model long-context ladders and uses the same policy for validation. Non-resolver path continues through validated calculateCostInternalWithPolicy; it does not bypass guards by directly computing totals.
- OpenAIGateway.RecordUsage: actual billing credential+sent model decides OAuth-like GPT61 exemption; customer aliases/response labels cannot redefine eligibility. Preserve fail-closed missing-price and observation-only response model policy; propagate exemption to customer/free-Fast recomputation and account stats. Provider account costs retain local long-context default for other credentials, independent of customer opt-in. Explicit account rules keep precedence.
- WebSocket BeforeTurn takes authenticated current group snapshot and freezes pricing time; AfterTurn validates then submits the SAME per-turn APIKey snapshot. Local capture terminal-error/success cleanup, cyber audit flags, idem billing and quota scope remain. New test fixture group/auth fields included; no duplicate asynchronous billing block.
- Responses/Chat/Messages routing resolves provider profile after mapping using generalized resolveUpstreamProtocolFor. Removed redundant OpenCode/native-CN branch that would bypass the new resolver; keep local request normalization, compact guards, task recovery, redaction/capture and header lifecycle.
- Encrypted-content retry: combine exact thinking_signature_invalid+message guard with existing invalid_encrypted_content, once per request; preserve local complete/incomplete error replay and capture final attempt. Do not replace local response replay with plain NopCloser.
- Concrete model catalogs: upstream changes in gateway_handler, gateway_models_test and gateway_service are exclusively Composite additions/loops, audited individually and excluded under prior policy; retain local unified/default-follow concrete model discovery helpers, constructors and caches.
- Shared concrete-platform normalization uses domain catalog, without duplicate declaration. KIRO Responses compatibility explicitly remains a separate dispatch path; token counting retains KIRO Anthropic handler and Grok specialized counter.
- Responses refusal conversion: existing local pointer null/empty semantics, strings.Builder, done/item recovery and bounded buffer state already implement upstream refusal behavior. Remove automatically duplicated Refusal fields/cases/helper, retain upstream independent wire tests and error checking.
- Buffered Anthropic deltas use existing bounded/index-validated accumulator, including signature state; upstream negative-index tests validate all four conversion paths. Ping comments share the existing first-output staging boundary, then flush immediately after semantic output; ping-only upstreams still fail over without committing200. Existing upstream immediate-pre-output fixture was adapted to this local policy; red test demonstrated direct heartbeat writing violated it. Ping client-write failure stops later writes/flushes and preserves only observed usage.
- OAuth tool/cache compatibility: only explicitly requested tool-change betas survive whitelist/drop policy; cache block count then ordered TTL normalization preserves client1h choice. Hosted search history tool injection is cached-only, pins no-tools choice to none, uses additional_tools in Lite and skips compact; only actual OAuth-like transformations apply.
- Codex catalog metadata: remote catalogs honor account mappings, generated catalogs unchanged; preserve original bytes/ETag when projection/merge does not change entries; service_tiers null normalizes to array; generated GPT61 context500k does not overwrite explicit remote metadata.
- WS usage: parse image-input counters from usage or tool_usage, keep turn attribution/nonzero merge/aggregate and adapter transfer. Existing local image cache and quota cost buckets remain.
- EasyPay: strict notification field set plus dropping unsigned caller return-query parameters prevents sign reuse; keep signed resume params generated later and existing idempotent fulfillment/reward ledger. Mock-only verification.
- Setup: random absent credentials only when bootstrapping actual first admin; existing deployments skip changes, configured credentials validated against login email/bcrypt72-byte constraints. No production config access.
- Moderation config save uses transactional setter when supported; deleted-group rejection and runtime replacement occur only after save. Existing whitelist/audit-only flow stays.
- Web fallback bypasses expanded bare API aliases while keeping local frontend paths and embedded asset fixture SVG contract. New bypass list does not register excluded audio/search endpoints.
- Ops outputTPS type is frontend JSON contract; repository owner excludes image/video requests and aggregates per-request output durations/TTFT.
- Migration248 is quota-only CHECK removal, existing242..247 immutable; static migration guard rewritten coordinate without Composite table access. Local historical checksum guard extended only approved248 addition.
- Generated Ent/Wire: rebuilt exclusively from final local schemas; generated runtime and Wire match prior bytes because only validator implementation behind schema changed.

## Per-file coverage

- `Makefile`: Delegated: security or platform owner, see corresponding coverage report.
- `README_JA.md`: Delegated: security or platform owner, see corresponding coverage report.
- `backend/cmd/server/VERSION`: Reviewed non-runtime coordinate/data compatibility.
- `backend/ent/runtime/runtime.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/handler/gateway_handler.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/handler/gateway_models_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/handler/openai_codex_models_handler_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/handler/openai_gateway_handler.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/handler/openai_gateway_handler_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/handler/openai_gateway_ws_group_pricing_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/integration/e2e_user_flow_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/payment/provider/easypay.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/payment/provider/easypay_notify_security_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/antigravity/claude_types.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/antigravity/claude_types_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/antigravity/stream_transformer.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/apicompat/anthropic_responses_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/apicompat/chatcompletions_to_responses.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/apicompat/chatcompletions_to_responses_developer_role_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/apicompat/chatcompletions_to_responses_legacy_function_call_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/apicompat/chatcompletions_to_responses_tool_choice_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/apicompat/responses_to_chatcompletions.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/apicompat/responses_to_chatcompletions_refusal_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/apicompat/thinking_signature_wire_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/pkg/apicompat/types.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/claude/constants.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/xai/sso_device.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/pkg/xai/sso_device_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/server/api_contract_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/server/routes/gateway.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/anthropic_apikey_auth.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/anthropic_buffered_negative_index_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/auth_service.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/billing_service.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/content_moderation.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/domain_constants.go`: Delegated: security or platform owner, see corresponding coverage report.
- `backend/internal/service/gateway_cache_ttl_order_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/gateway_claude_oauth_body.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/gateway_forward_as_chat_completions.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/gateway_forward_as_chat_completions_ping_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/gateway_forward_as_responses.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/gateway_image_reasoning_pricing_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/gateway_service.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/gateway_tool_changes_beta_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/gateway_upstream_request.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/model_rate_limit.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_alpha_search_billing_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_apikey_responses_probe.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_codex_model_metadata.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_codex_model_metadata_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_codex_models_pinned.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_codex_models_pinned_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_codex_models_service.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_codex_models_service_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_codex_transform.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_chat_completions.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_chat_completions_anthropic_native.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_cn_fixes_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_gateway_count_tokens.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_forward.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_messages.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_messages_anthropic_native.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_passthrough.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_provider_claude_billing_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_gateway_request_body.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_response_handling.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_responses_anthropic_native.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_responses_empty_completed_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_gateway_scheduling.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_search_surcharge_test.go`: Excluded/deleted under existing product policy; caller references inspected.
- `backend/internal/service/openai_gateway_service.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_gateway_service_hotpath_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_gateway_usage.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_image_generation_controls_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_models_list.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_models_list_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_oauth_web_search_history.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_oauth_web_search_history_forward_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_oauth_web_search_history_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_responses_namespace.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_responses_namespace_benchmark_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_responses_namespace_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_subscription_long_context_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_ws_v2/passthrough_relay.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_ws_v2/passthrough_relay_image_usage_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/openai_ws_v2_passthrough_adapter.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/openai_ws_v2_passthrough_image_usage_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/ops_dashboard_models.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/payment_resume_service.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/service/payment_resume_service_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/service/testdata/record_upstream_protocol_routing_main.py`: Reviewed non-runtime coordinate/data compatibility.
- `backend/internal/setup/cli.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/setup/handler.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/setup/setup.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/setup/setup_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/internal/web/embed_on.go`: Reviewed base/ours/upstream/final changed region and relevant call chain as mapped above.
- `backend/internal/web/embed_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.
- `backend/migrations/242_drop_platform_check_constraints.sql`: Delegated: security or platform owner, see corresponding coverage report.
- `backend/migrations/platform_check_constraints_drop_migration_test.go`: Read upstream test intent and fixture interfaces; integrated with local regression semantics; execution results recorded separately.

## Final compatibility checks

Explicit local ModelsListConfig display entries retain pinned catalog native IDs; only
those explicit IDs bypass mapping projection. Unrelated remote IDs remain mapping-filtered
and both original pinned-list regression and new wildcard discovery regression passed.
Missing account custom effort entries now remain1x for Fable in integration/unit tests.
Native Anthropic predicate tests target the generalized resolver after obsolete helper
removal. Embedded static asset tests retain shipped local PNG branding.
