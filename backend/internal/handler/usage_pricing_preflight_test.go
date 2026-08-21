//go:build unit

package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFinalizeGatewayUsagePricingValidationMarksDurableBillingFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := &service.ForwardResult{Stream: false}

	require.False(t, finalizeGatewayUsagePricingValidation(c, result, errors.New("model price not found")))
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete)
	require.False(t, result.UpstreamFailed, "local pricing configuration must not penalize the upstream account")

	marked, ok := service.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "api_error", marked.ErrType)
	require.Equal(t, "usage_pricing_unavailable", marked.Code)
	require.Equal(t, "Unable to price upstream usage", marked.Message)
	require.Equal(t, http.StatusBadGateway, marked.IntendedStatus)
	require.True(t, marked.CountTowardsSLA)
	require.False(t, marked.Stream)
}

func TestFinalizeOpenAIUsagePricingValidationPreservesStreamMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	result := &service.OpenAIForwardResult{Stream: true}

	require.False(t, finalizeOpenAIUsagePricingValidation(c, result, errors.New("model price not found")))
	require.True(t, result.CaptureTerminalError)
	require.True(t, result.CaptureResponseComplete)
	require.False(t, result.UpstreamFailed)

	marked, ok := service.GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, "api_error", marked.ErrType)
	require.Equal(t, "usage_pricing_unavailable", marked.Code)
	require.True(t, marked.Stream)
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
		initialPolicy *service.CaptureContentPolicy
		wantCapture   bool
	}{
		{name: "success enabled terminal disabled", success: true, terminal: false, initialPolicy: &service.CaptureContentPolicy{RawRequest: true, RawResponse: true}, wantCapture: false},
		{name: "success disabled terminal enabled", success: false, terminal: true, initialPolicy: nil, wantCapture: true},
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
			policy.ModelAllowlists.Anthropic = nil
			require.NoError(t, service.InstallCaptureRuntimePolicyForUnitTest(c, policy, 9, nil))
			records := make(chan *service.CaptureRecord, 1)
			h := &GatewayHandler{capturePool: service.NewConversationCapturePoolForUnitTest(records), cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1024}}}}
			result := &service.ForwardResult{
				RequestID: "gateway-pricing-failure", Model: "claude-test", UpstreamModel: "claude-test",
				UpstreamRequest: []byte(`{"model":"claude-test"}`), CaptureResponse: []byte(`{"type":"message"}`),
				CaptureContentPolicy: tt.initialPolicy,
			}

			require.False(t, finalizeGatewayUsagePricingValidation(c, result, errors.New("unpriced")))
			h.submitGatewayResultCaptureForRequest(c, result, &service.Account{ID: 1, Platform: service.PlatformAnthropic}, nil, "/v1/messages")

			require.Equal(t, tt.wantCapture, len(records) == 1)
		})
	}
}

func TestOpenAIBufferedCaptureReevaluatesOutcomeAfterPricingFailure(t *testing.T) {
	tests := []struct {
		name          string
		success       bool
		terminal      bool
		initialPolicy *service.CaptureContentPolicy
		wantCapture   bool
	}{
		{name: "success enabled terminal disabled", success: true, terminal: false, initialPolicy: &service.CaptureContentPolicy{RawRequest: true, RawResponse: true}, wantCapture: false},
		{name: "success disabled terminal enabled", success: false, terminal: true, initialPolicy: nil, wantCapture: true},
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
				CaptureContentPolicy: tt.initialPolicy,
			}

			require.False(t, finalizeOpenAIUsagePricingValidation(c, result, errors.New("unpriced")))
			h.submitCapture(c, result, &service.Account{ID: 1, Platform: service.PlatformOpenAI}, nil, "/v1/responses")

			require.Equal(t, tt.wantCapture, len(records) == 1)
		})
	}
}
