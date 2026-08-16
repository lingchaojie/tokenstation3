package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

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
			Enabled:        true,
			MaxBodyBytes:   1 << 20,
			MaxHeaderBytes: 1 << 20,
		},
	}}
	transport := &recordingCaptureTransport{}
	svc := &GatewayService{
		cfg:                  cfg,
		httpUpstream:         upstream,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
		capturePool:          newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	account := &Account{
		ID:          811,
		Name:        "bedrock-capture",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeBedrock,
		Concurrency: 1,
		Credentials: map[string]any{
			"aws_access_key_id":     "AKIA_CAPTURE_TEST",
			"aws_secret_access_key": "bedrock-secret-access-key",
			"aws_session_token":     "bedrock-temporary-session-token",
			"aws_region":            "us-east-1",
		},
		Status:      StatusActive,
		Schedulable: true,
	}

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, upstream.lastBody)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureRequest)
	require.Nil(t, result.CaptureResponse)
	require.Zero(t, result.CaptureHTTPStatus)
	require.Empty(t, result.CaptureUpstreamEndpoint)
	require.Nil(t, result.CaptureContentPolicy)

	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	attempt := attempts[0]
	require.Equal(t, upstream.lastBody, attempt.RequestBytes())
	require.Equal(t, rawProviderResponse, attempt.ResponseBytes())
	require.Equal(t, captureHeaderBytes(upstream.lastReq.Header, cfg.Gateway.Capture.MaxHeaderBytes), attempt.RequestHeaderBytes())
	require.Equal(t, redactHTTPHeader(upstream.resp.Header), attempt.ResponseHeaderBytes())
	require.Contains(t, attempt.begin.UpstreamEndpoint, "bedrock-runtime.us-east-1.amazonaws.com")
	require.NotContains(t, string(attempt.RequestHeaderBytes()), "AKIA_CAPTURE_TEST")
	require.NotContains(t, string(attempt.RequestHeaderBytes()), "bedrock-secret-access-key")
	require.NotContains(t, string(attempt.RequestHeaderBytes()), "bedrock-temporary-session-token")
	require.NotContains(t, strings.ToLower(string(attempt.RequestHeaderBytes())), "x-amz-security-token")
	require.Contains(t, string(attempt.ResponseHeaderBytes()), "X-Amzn-Requestid")
	require.NotContains(t, string(attempt.ResponseHeaderBytes()), "X-Request-Id", "capture must not synthesize provider response headers")
	require.Empty(t, attempt.TerminalStates(), "the handler-side usage sink owns commit")
	AbortCaptureAttempt(c)
}

type bedrockCloseReleasedReader struct {
	tail   []byte
	closed chan struct{}
	once   sync.Once
	done   bool
}

func (r *bedrockCloseReleasedReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	<-r.closed
	r.done = true
	n := copy(p, r.tail)
	return n, io.EOF
}

func (r *bedrockCloseReleasedReader) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestBedrockStreamTimeoutJoinsDecoderBeforePublishingCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	setCapturePlatform(c, PlatformAnthropic)
	SetCaptureOutboundRequest(c, c.Request, []byte(`{"model":"bedrock"}`), 1024)

	tail := []byte("tail-returned-as-close-unblocks-decoder")
	body := &bedrockCloseReleasedReader{tail: tail, closed: make(chan struct{})}
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: body, Request: c.Request}
	beginCaptureResponse(c, resp, true, 1024)
	svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}
	account := &Account{ID: 1, Platform: PlatformAnthropic}

	_, err := svc.handleBedrockStreamingResponse(context.Background(), resp, c, account, time.Now(), "bedrock")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.HasUpstreamHTTPResponse)
	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, tail, bridge.Response)
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
		Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20},
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
	require.True(t, CommitTerminalErrorCaptureAttempt(c, PlatformAnthropic, upstream.resp.StatusCode))
	pool.Stop()

	require.Len(t, writer.records, 1)
	record := <-writer.records
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, upstream.lastBody, record.RawRequest, "terminal capture must use the actual Bedrock envelope")
	require.NotEqual(t, inbound, record.RawRequest)
	require.Equal(t, errorBody, record.RawResponse)
	require.Contains(t, record.UpstreamEndpoint, "bedrock-runtime.us-east-1.amazonaws.com")
	require.NotContains(t, string(record.RequestHeaders), "bedrock-provider-secret")
	require.Contains(t, string(record.ResponseHeaders), "X-Amzn-Requestid")
	require.NotContains(t, string(record.ResponseHeaders), "X-Request-Id")
}
