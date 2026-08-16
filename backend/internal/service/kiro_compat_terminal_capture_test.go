//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroCompatibilityWrappersPreserveProviderHTTPFailureForTerminalCapture(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		requestBody []byte
		forward     func(*GatewayService, context.Context, *gin.Context, *Account, []byte, *ParsedRequest) (*ForwardResult, error)
	}{
		{
			name:        "chat_completions",
			path:        "/v1/chat/completions",
			requestBody: []byte(`{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hello"}]}`),
			forward:     (*GatewayService).ForwardAsChatCompletions,
		},
		{
			name:        "responses",
			path:        "/v1/responses",
			requestBody: []byte(`{"model":"claude-sonnet-4-6","stream":false,"input":"hello"}`),
			forward:     (*GatewayService).ForwardAsResponses,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.requestBody))
			enableCaptureForTest(t, c)

			errorBody := []byte(`{"message":"Kiro payment required"}`)
			upstream := &queuedHTTPUpstream{responses: []*http.Response{{
				StatusCode: http.StatusPaymentRequired,
				Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Amzn-Requestid": []string{"kiro-402-id"}},
				Body:       io.NopCloser(bytes.NewReader(errorBody)),
			}}}
			cfg := &config.Config{Gateway: config.GatewayConfig{
				MaxLineSize: defaultMaxLineSize,
				Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 16 << 10},
			}}
			transport := &recordingCaptureTransport{}
			svc := &GatewayService{
				cfg: cfg, httpUpstream: upstream, tlsFPProfileService: &TLSFingerprintProfileService{},
				kiroCooldownStore: &stubKiroCooldownStore{}, rateLimitService: &RateLimitService{},
				capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
			}
			account := &Account{
				ID: 38, Name: "kiro-compat-terminal", Platform: PlatformKiro, Type: AccountTypeAPIKey,
				Status: StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"api_key": "kiro-provider-secret", "api_region": "us-west-2",
					"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
				},
			}
			parsed := &ParsedRequest{Body: NewRequestBodyRef(tt.requestBody), Model: "claude-sonnet-4-6", Stream: false}
			SetCaptureRequestedModel(c, parsed.Model)

			result, err := tt.forward(svc, context.Background(), c, account, tt.requestBody, parsed)
			require.Nil(t, result)
			var failure *UpstreamFailoverError
			require.True(t, errors.As(err, &failure), "the compatibility wrapper must preserve a real provider HTTP failure")
			require.True(t, failure.HasUpstreamHTTPResponse)
			require.Equal(t, http.StatusPaymentRequired, failure.StatusCode)
			require.Equal(t, errorBody, failure.ResponseBody)
			require.Equal(t, PlatformKiro, failure.Platform)

			require.True(t, CommitTerminalErrorCaptureAttemptWithCompleteness(c, PlatformKiro, failure.HTTPStatusForCapture(), !failure.CaptureResponseIncomplete))
			require.Len(t, transport.Attempts(), 1)
			attempt := transport.Attempts()[0]
			require.Len(t, upstream.requests, 1)
			require.Equal(t, snapshotHTTPRequestBody(upstream.requests[0]), attempt.RequestBytes())
			require.Equal(t, errorBody, attempt.ResponseBytes())
			require.NotContains(t, string(attempt.RequestHeaderBytes()), "kiro-provider-secret")
			require.Equal(t, []captureTerminalState{captureCommitted}, attempt.TerminalStates())
		})
	}
}
