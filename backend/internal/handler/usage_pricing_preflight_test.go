//go:build unit

package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Removing SystemOne's synchronous pricing preflight must lose the terminal
// failure classification even though the provider's response is already sent.
func TestB8deSystemOneMissingUsageClassifiedBeforeCapture(t *testing.T) {
	c, recorder := newSystemOneHandlerContext(validSystemOneHandlerBody)
	h := &GatewayHandler{gatewayService: &service.GatewayService{}}
	groupID := int64(7)
	key := &service.APIKey{
		ID: 4, UserID: 9, User: &service.User{ID: 9}, GroupID: &groupID,
		Group: &service.Group{ID: groupID, Platform: service.PlatformTypeSafe},
	}
	account := &service.Account{ID: 11, Platform: service.PlatformTypeSafe}
	result := &service.SystemOneForwardResult{
		ForwardResult: service.ForwardResult{
			RequestID: "systemone-missing-usage", Model: "jev-latest",
			UpstreamModel: "jev-latest", CaptureResponseComplete: true,
		},
		StatusCode: http.StatusOK, ContentType: "application/json",
		Body: []byte(`{"answers":{}}`),
	}
	c.Data(result.StatusCode, result.ContentType, result.Body)

	h.recordSystemOneUsage(c, key, account, nil, service.ChannelMappingResult{},
		"jev-latest", []byte(validSystemOneHandlerBody), result, 9, time.Now())

	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete)
	require.False(t, result.UpstreamFailed, "missing usage must not penalize the provider account")
	marked, ok := service.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "usage_pricing_unavailable", marked.Code)
	require.Equal(t, http.StatusBadGateway, marked.IntendedStatus)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, `{"answers":{}}`, recorder.Body.String(), "do not append a second response")
}

func TestFinalizeGatewayUsagePricingValidationPreservesProviderCompletionProof(t *testing.T) {
	tests := []struct {
		name     string
		stream   bool
		complete bool
	}{
		{name: "verified nonstream", complete: true},
		{name: "streaming clean EOF", stream: true, complete: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			result := &service.ForwardResult{Stream: tt.stream, CaptureResponseComplete: tt.complete}

			require.False(t, finalizeGatewayUsagePricingValidation(c, result, errors.New("model price not found")))
			require.True(t, result.CaptureTerminalError)
			require.Equal(t, tt.complete, result.CaptureResponseComplete)
			require.False(t, result.UpstreamFailed, "local pricing configuration must not penalize the upstream account")

			marked, ok := service.GetOpsStreamError(c)
			require.True(t, ok)
			require.Equal(t, "api_error", marked.ErrType)
			require.Equal(t, "usage_pricing_unavailable", marked.Code)
			require.Equal(t, "Unable to price upstream usage", marked.Message)
			require.Equal(t, http.StatusBadGateway, marked.IntendedStatus)
			require.True(t, marked.CountTowardsSLA)
			require.Equal(t, tt.stream, marked.Stream)
		})
	}
}

func TestFinalizeOpenAIUsagePricingValidationPreservesProviderCompletionProof(t *testing.T) {
	tests := []struct {
		name     string
		stream   bool
		complete bool
	}{
		{name: "verified nonstream", complete: true},
		{name: "streaming clean EOF", stream: true, complete: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			result := &service.OpenAIForwardResult{Stream: tt.stream, CaptureResponseComplete: tt.complete}

			require.False(t, finalizeOpenAIUsagePricingValidation(c, result, errors.New("model price not found")))
			require.True(t, result.CaptureTerminalError)
			require.Equal(t, tt.complete, result.CaptureResponseComplete)
			require.False(t, result.UpstreamFailed)

			marked, ok := service.GetOpsStreamError(c)
			require.True(t, ok)
			require.Equal(t, "api_error", marked.ErrType)
			require.Equal(t, "usage_pricing_unavailable", marked.Code)
			require.Equal(t, tt.stream, marked.Stream)
		})
	}
}

func TestFinalizeUsagePricingValidationSuccessIsNoop(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	gatewayResult := &service.ForwardResult{}
	openAIResult := &service.OpenAIForwardResult{}

	require.True(t, finalizeGatewayUsagePricingValidation(c, gatewayResult, nil))
	require.True(t, finalizeOpenAIUsagePricingValidation(c, openAIResult, nil))
	require.False(t, gatewayResult.CaptureTerminalError)
	require.False(t, openAIResult.CaptureTerminalError)
	_, marked := service.GetOpsStreamError(c)
	require.False(t, marked)
}

func TestFinalizeUsagePricingValidationPreservesExistingIncompleteTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	gatewayResult := &service.ForwardResult{CaptureTerminalError: true, CaptureResponseComplete: false}
	openAIResult := &service.OpenAIForwardResult{CaptureTerminalError: true, CaptureResponseComplete: false}

	require.False(t, finalizeGatewayUsagePricingValidation(c, gatewayResult, errors.New("model price not found")))
	require.False(t, gatewayResult.CaptureResponseComplete)

	c, _ = gin.CreateTestContext(httptest.NewRecorder())
	require.False(t, finalizeOpenAIUsagePricingValidation(c, openAIResult, errors.New("model price not found")))
	require.False(t, openAIResult.CaptureResponseComplete)
}

func TestGatewayBufferedCaptureReevaluatesOutcomeAfterPricingFailure(t *testing.T) {
	tests := []struct {
		name          string
		success       bool
		terminal      bool
		complete      bool
		initialPolicy *service.CaptureContentPolicy
		wantCapture   bool
	}{
		{name: "success enabled terminal disabled", success: true, terminal: false, initialPolicy: &service.CaptureContentPolicy{RawRequest: true, RawResponse: true}, wantCapture: false},
		{name: "terminal enabled verified nonstream", success: false, terminal: true, complete: true, initialPolicy: nil, wantCapture: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			policy := service.DefaultCaptureRuntimePolicy()
			policy.Enabled = true
			policy.Platforms.Anthropic = true
			policy.Outcomes.Success = tt.success
			policy.Outcomes.TerminalError = tt.terminal
			policy.ModelsListConfigs.Anthropic = nil
			require.NoError(t, service.InstallCaptureRuntimePolicyForUnitTest(c, policy, 9, nil))
			records := make(chan *service.CaptureRecord, 1)
			h := &GatewayHandler{capturePool: service.NewConversationCapturePoolForUnitTest(records), cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1024}}}}
			result := &service.ForwardResult{
				RequestID: "gateway-pricing-failure", Model: "claude-test", UpstreamModel: "claude-test",
				UpstreamRequest: []byte(`{"model":"claude-test"}`), CaptureResponse: []byte(`{"type":"message"}`),
				CaptureContentPolicy: tt.initialPolicy, CaptureResponseComplete: tt.complete,
			}

			require.False(t, finalizeGatewayUsagePricingValidation(c, result, errors.New("unpriced")))
			h.submitGatewayResultCaptureForRequest(c, result, &service.Account{ID: 1, Platform: service.PlatformAnthropic}, nil, "/v1/messages")

			require.Equal(t, tt.wantCapture, len(records) == 1)
			if tt.wantCapture {
				require.False(t, (<-records).Truncated, "verified non-stream pricing failure must remain a complete capture")
			}
		})
	}
}

func TestOpenAIBufferedCaptureReevaluatesOutcomeAfterPricingFailure(t *testing.T) {
	tests := []struct {
		name          string
		success       bool
		terminal      bool
		complete      bool
		initialPolicy *service.CaptureContentPolicy
		wantCapture   bool
	}{
		{name: "success enabled terminal disabled", success: true, terminal: false, initialPolicy: &service.CaptureContentPolicy{RawRequest: true, RawResponse: true}, wantCapture: false},
		{name: "terminal enabled verified nonstream", success: false, terminal: true, complete: true, initialPolicy: nil, wantCapture: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			policy := service.DefaultCaptureRuntimePolicy()
			policy.Enabled = true
			policy.Platforms.OpenAI = true
			policy.Outcomes.Success = tt.success
			policy.Outcomes.TerminalError = tt.terminal
			require.NoError(t, service.InstallCaptureRuntimePolicyForUnitTest(c, policy, 9, nil))
			records := make(chan *service.CaptureRecord, 1)
			h := &OpenAIGatewayHandler{capturePool: service.NewConversationCapturePoolForUnitTest(records), cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1024}}}}
			result := &service.OpenAIForwardResult{
				RequestID: "openai-pricing-failure", Model: "gpt-test", UpstreamModel: "gpt-test",
				UpstreamRequest: []byte(`{"model":"gpt-test"}`), CaptureResponse: []byte(`{"id":"resp"}`),
				CaptureContentPolicy: tt.initialPolicy, CaptureResponseComplete: tt.complete,
			}

			require.False(t, finalizeOpenAIUsagePricingValidation(c, result, errors.New("unpriced")))
			h.submitCapture(c, result, &service.Account{ID: 1, Platform: service.PlatformOpenAI}, nil, "/v1/responses")

			require.Equal(t, tt.wantCapture, len(records) == 1)
			if tt.wantCapture {
				require.False(t, (<-records).Truncated, "verified non-stream pricing failure must remain a complete capture")
			}
		})
	}
}
