package service

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestCursorDispatchPublicEntrypointsPreserveCallerProtocolAndCapture(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		body         []byte
		wantResponse func(*testing.T, []byte)
		forward      func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name: "chat", path: "/v1/chat/completions",
			body: []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`),
			wantResponse: func(t *testing.T, body []byte) {
				require.Equal(t, "chat.completion", gjson.GetBytes(body, "object").String())
				require.Equal(t, "hello from Cursor", gjson.GetBytes(body, "choices.0.message.content").String())
			},
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			},
		},
		{
			name: "responses", path: "/v1/responses",
			body: []byte(`{"model":"auto","input":"hello"}`),
			wantResponse: func(t *testing.T, body []byte) {
				require.Equal(t, "response", gjson.GetBytes(body, "object").String())
				require.False(t, gjson.GetBytes(body, "choices").Exists())
				require.Contains(t, string(body), "hello from Cursor")
			},
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(ctx, c, account, body)
			},
		},
		{
			name: "messages", path: "/v1/messages",
			body: []byte(`{"model":"auto","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
			wantResponse: func(t *testing.T, body []byte) {
				require.Equal(t, "message", gjson.GetBytes(body, "type").String())
				require.False(t, gjson.GetBytes(body, "choices").Exists())
				require.Contains(t, string(body), "hello from Cursor")
			},
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
			},
		},
	}

	var opens int
	var params []cursorpkg.AgentRunParams
	baseOpener := cursorCaptureSuccessOpener(t)
	opener := func(ctx context.Context, got cursorpkg.AgentRunParams, options cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
		opens++
		params = append(params, got)
		return baseOpener(ctx, got, options)
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			svc := cursorCaptureService(t, transport, opener)
			c, recorder := cursorCaptureContext(t, test.path, test.body, true)

			result, err := test.forward(svc, c.Request.Context(), c, cursorChatForwardAccount(t), test.body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.HasBillableUsage())
			require.Equal(t, OpenAIUsage{InputTokens: 13, OutputTokens: 8, CacheReadInputTokens: 2, CacheCreationInputTokens: 1}, result.Usage)
			require.Equal(t, cursorpkg.AgentDefaultModel, result.UpstreamModel)
			require.Equal(t, cursorAgentEndpoint, result.UpstreamEndpoint)
			test.wantResponse(t, recorder.Body.Bytes())
			require.NotContains(t, recorder.Body.String(), "connect_proto")
			require.NotContains(t, recorder.Body.String(), "exec_stream_close")

			require.Len(t, transport.Attempts(), 1)
			attempt := transport.Attempts()[0]
			require.Equal(t, model.PayloadJSON, attempt.begin.Format)
			require.Equal(t, test.body, attempt.RequestBytes())
			require.Equal(t, recorder.Body.Bytes(), attempt.ResponseBytes())
			require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
			require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformCursor, result))
			require.Len(t, attempt.Finals(), 1)
		})
	}

	require.Equal(t, 3, opens)
	require.Len(t, params, 3)
	for _, got := range params {
		require.Equal(t, cursorpkg.AgentDefaultModel, got.Model)
	}
}

func TestCursorDispatchValidationPrecedesCodexRestrictionAndNeverOpensUpstream(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		body     []byte
		wantType string
		forward  func(*OpenAIGatewayService, context.Context, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name: "chat", path: "/v1/chat/completions", body: []byte(`{"messages":[]}`), wantType: "invalid_request_error",
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			},
		},
		{
			name: "responses", path: "/v1/responses", body: []byte(`{"input":"hello"}`), wantType: "invalid_request_error",
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.Forward(ctx, c, account, body)
			},
		},
		{
			name: "messages", path: "/v1/messages", body: []byte(`{"max_tokens":32,"messages":[]}`), wantType: "invalid_request_error",
			forward: func(svc *OpenAIGatewayService, ctx context.Context, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opens := 0
			transport := &recordingCaptureTransport{}
			svc := cursorCaptureService(t, transport, func(context.Context, cursorpkg.AgentRunParams, cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
				opens++
				return nil, nil
			})
			svc.codexDetector = &stubCodexRestrictionDetector{result: CodexClientRestrictionDetectionResult{Enabled: true, Matched: false}}
			c, recorder := cursorCaptureContext(t, test.path, test.body, true)

			result, err := test.forward(svc, c.Request.Context(), c, cursorChatForwardAccount(t), test.body)
			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, test.wantType, gjson.GetBytes(recorder.Body.Bytes(), "error.type").String())
			if test.name == "messages" {
				require.Equal(t, "error", gjson.GetBytes(recorder.Body.Bytes(), "type").String())
			}
			require.NotContains(t, strings.ToLower(recorder.Body.String()), "codex")
			require.Zero(t, opens)
			require.Empty(t, transport.Attempts())
		})
	}
}

func TestCursorSchedulerPlatformSnapshotAndBulkTargetsAreFirstClass(t *testing.T) {
	require.True(t, IsOpenAICompatiblePlatform(PlatformCursor))
	require.Equal(t, PlatformCursor, NormalizeOpenAICompatiblePlatform(PlatformCursor))
	require.Equal(t, PlatformCursor, OpenAICompatiblePlatformFromContext(WithOpenAICompatiblePlatform(context.Background(), PlatformCursor)))
	require.Equal(t, PlatformCursor, OpenAICompatiblePlatformFromContext(context.WithValue(context.Background(), ctxkey.Platform, PlatformCursor)))

	account := &Account{
		ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
	}
	require.True(t, isOpenAICompatibleAccountEligibleForRequest(context.Background(), account, PlatformCursor, "", false, OpenAIEndpointCapabilityChatCompletions))
	require.True(t, isOpenAICompatibleAccountEligibleForRequest(context.Background(), account, PlatformCursor, "", false, OpenAIEndpointCapabilityResponses))

	platforms := schedulerSnapshotPlatforms()
	require.Equal(t, []string{
		PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity, PlatformKiro,
		PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformCursor,
	}, platforms[:])
	seen := make(map[string]int, len(platforms))
	for _, platform := range platforms {
		seen[platform]++
	}
	require.Len(t, seen, 10)
	for platform, count := range seen {
		require.Equal(t, 1, count, "platform=%s", platform)
	}

	var cursorBuckets []SchedulerBucket
	for _, bucket := range schedulerCanonicalBuckets(7) {
		if bucket.Platform == PlatformCursor {
			cursorBuckets = append(cursorBuckets, bucket)
		}
	}
	require.ElementsMatch(t, []SchedulerBucket{
		{GroupID: 7, Platform: PlatformCursor, Mode: SchedulerModeSingle},
		{GroupID: 7, Platform: PlatformCursor, Mode: SchedulerModeForced},
	}, cursorBuckets)
	require.Nil(t, rebuildPlatformsForMixedAccount(account))
	require.Equal(t, []string{PlatformCursor}, schedulerBulkRebuildPlatforms(account))

	wsConfig := &config.Config{}
	wsConfig.Gateway.OpenAIWS.Enabled = true
	wsConfig.Gateway.OpenAIWS.OAuthEnabled = true
	wsConfig.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	account.Extra = map[string]any{"openai_oauth_responses_websockets_v2_enabled": true}
	require.Equal(t, OpenAIUpstreamTransportHTTPSSE, NewOpenAIWSProtocolResolver(wsConfig).Resolve(account).Transport)
}

func TestCursorBillingQuotaProfitAndThresholdContracts(t *testing.T) {
	result := &OpenAIForwardResult{Usage: OpenAIUsage{InputTokens: 3}, UpstreamEndpoint: cursorAgentEndpoint}
	require.True(t, result.HasBillableUsage())

	apiKey := &APIKey{Group: &Group{Platform: PlatformOpenAI}}
	quotaCtx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformCursor)
	require.Equal(t, PlatformCursor, QuotaPlatform(quotaCtx, apiKey))

	decision := EvaluateAccountSchedulingThreshold(&Account{
		Platform:    PlatformCursor,
		Credentials: map[string]any{accountSchedulingThresholdCredentialKey: 1},
		Extra: map[string]any{
			"codex_5h_used_percent": 99.0,
			"codex_5h_reset_at":     time.Now().Add(time.Hour).Unix(),
		},
	}, map[string]int{PlatformCursor: 1}, time.Now())
	require.False(t, decision.ShouldPause)

	require.True(t, profitControlPlatformSupported(PlatformCursor))
	require.NoError(t, ValidateProfitControlConfig(PlatformCursor, true, 0.2, 0.1))
	enabled, margin, buffer := NormalizeProfitControlConfig(PlatformCursor, true, 0.2, 0.1)
	require.True(t, enabled)
	require.Equal(t, 0.2, margin)
	require.Equal(t, 0.1, buffer)
	require.True(t, groupSupportsOAuthOnlyFilter(PlatformCursor))

	group := profitControlTestGroup(9, 0.2, 0.1)
	group.Platform = PlatformCursor
	gate := (&OpenAIGatewayService{}).resolveOpenAIProfitControlGate(profitControlTestCtx(group), &group.ID)
	require.NotNil(t, gate)
	require.Equal(t, PlatformCursor, gate.platform)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
	expensive := profitControlTestAccountWithRate(&Account{Platform: PlatformCursor}, 0.8)
	vetoed, reason := OpenAIProfitControlVeto(ctx, expensive)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
}

func TestCursorGroupModelCandidatesUseObservedFallbackAndMapping(t *testing.T) {
	observed := &Account{
		ID: 1, Platform: PlatformCursor, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{
			"observed-alias": "gpt-5", "hidden-alias": "not-observed",
		}},
		Extra: cursorObservedExtra("gpt-5"),
	}
	require.Equal(t, []string{"gpt-5", "observed-alias"}, cursorGroupModelCandidateIDs([]Account{*observed}))

	fallback := &Account{
		ID: 2, Platform: PlatformCursor, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"model_mapping": map[string]any{"custom-alias": "custom-target"}},
	}
	want := append(cursorpkg.DefaultModelIDs(), "custom-alias")
	require.Equal(t, sortedCursorModelIDs(want), cursorGroupModelCandidateIDs([]Account{*fallback}))
	require.Equal(t, sortedCursorModelIDs(cursorpkg.DefaultModelIDs()), sortedCursorModelIDs(defaultModelsListCandidateIDs(PlatformCursor)))
}

func TestCursorBillingChannelPricingAndMappingRemainPlatformIsolated(t *testing.T) {
	price := func(platform string, input float64) ChannelModelPricing {
		return ChannelModelPricing{Platform: platform, Models: []string{"shared-model"}, BillingMode: BillingModeToken, InputPrice: &input}
	}
	cache := populateChannelCache([]Channel{{
		ID: 4, Status: StatusActive, GroupIDs: []int64{11},
		ModelPricing: []ChannelModelPricing{price(PlatformOpenAI, 1), price(PlatformGrok, 2), price(PlatformCursor, 3)},
		ModelMapping: map[string]map[string]string{
			PlatformOpenAI: {"shared-model": "openai-target"},
			PlatformGrok:   {"shared-model": "grok-target"},
			PlatformCursor: {"shared-model": "cursor-target"},
		},
	}}, map[int64]string{11: PlatformCursor})

	pricing := lookupPricingAcrossPlatforms(cache, 11, PlatformCursor, "shared-model")
	require.NotNil(t, pricing)
	require.Equal(t, PlatformCursor, pricing.Platform)
	require.Equal(t, 3.0, *pricing.InputPrice)
	require.Equal(t, "cursor-target", lookupMappingAcrossPlatforms(cache, 11, PlatformCursor, "shared-model"))
	require.NotEqual(t, "openai-target", lookupMappingAcrossPlatforms(cache, 11, PlatformCursor, "shared-model"))
}
