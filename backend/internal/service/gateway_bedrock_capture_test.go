package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayService_BedrockCapturesFinalProviderRequestAndResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	inbound := []byte(`{"model":"anthropic.claude-3-5-sonnet-20240620-v1:0","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(inbound), PlatformAnthropic)
	require.NoError(t, err)

	rawProviderResponse := []byte(`{"id":"msg_bedrock","type":"message","role":"assistant","model":"anthropic.claude-3-5-sonnet-20240620-v1:0","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":7,"output_tokens":3}}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"X-Amzn-Requestid": []string{"bedrock-request-id"},
		},
		Body: io.NopCloser(bytes.NewReader(rawProviderResponse)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: defaultMaxLineSize,
		Capture: config.GatewayCaptureConfig{
			Enabled:      true,
			MaxBodyBytes: 1 << 20,
		},
	}}
	svc := &GatewayService{
		cfg:                  cfg,
		httpUpstream:         upstream,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
	account := &Account{
		ID:          811,
		Name:        "bedrock-capture",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeBedrock,
		Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode":  "apikey",
			"api_key":    "bedrock-provider-secret",
			"aws_region": "us-east-1",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, upstream.lastBody)
	require.Equal(t, upstream.lastBody, result.CaptureRequest)
	require.Equal(t, rawProviderResponse, result.CaptureResponse)
	require.Equal(t, http.StatusOK, result.CaptureHTTPStatus)
	require.Contains(t, result.CaptureUpstreamEndpoint, "bedrock-runtime.us-east-1.amazonaws.com")
	require.NotNil(t, result.CaptureContentPolicy)
	require.NotContains(t, string(result.CaptureRequestHeaders), "bedrock-provider-secret")
}

func TestGatewayService_BedrockTerminalErrorArchivesFinalProviderRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	inbound := []byte(`{"model":"anthropic.claude-3-5-sonnet-20240620-v1:0","max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(inbound), PlatformAnthropic)
	require.NoError(t, err)
	c.Set("parsed_request", parsed)

	errorBody := []byte(`{"message":"bedrock rejected final request"}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusBadRequest,
		Header: http.Header{
			"Content-Type":     []string{"application/json"},
			"X-Amzn-Requestid": []string{"bedrock-error-id"},
		},
		Body: io.NopCloser(bytes.NewReader(errorBody)),
	}}
	cfg := &config.Config{Gateway: config.GatewayConfig{
		Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
	}}
	writer := &webChatArchiveRecordWriter{records: make(chan *CaptureRecord, 2)}
	pool := newConversationCapturePool(conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 4}, writer)
	t.Cleanup(pool.Stop)
	svc := &GatewayService{
		cfg:              cfg,
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{},
		deferredService:  &DeferredService{},
		capturePool:      pool,
	}
	account := &Account{
		ID: 812, Name: "bedrock-terminal-capture", Platform: PlatformAnthropic,
		Type: AccountTypeBedrock, Concurrency: 1,
		Credentials: map[string]any{
			"auth_mode": "apikey", "api_key": "bedrock-provider-secret", "aws_region": "us-east-1",
		},
		Status: StatusActive, Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.Error(t, err)
	require.Nil(t, result)
	pool.Stop()

	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, upstream.lastBody, record.RawRequest, "terminal capture must use the actual Bedrock envelope")
	require.NotEqual(t, inbound, record.RawRequest)
	require.Equal(t, errorBody, record.RawResponse)
	require.Contains(t, record.UpstreamEndpoint, "bedrock-runtime.us-east-1.amazonaws.com")
	require.NotContains(t, string(record.RequestHeaders), "bedrock-provider-secret")
}
