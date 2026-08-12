//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type kiroSlowFrameErrorReader struct {
	data []byte
	off  int
}

func (r *kiroSlowFrameErrorReader) Read(p []byte) (int, error) {
	if r.off < len(r.data) {
		n := copy(p, r.data[r.off:])
		r.off += n
		return n, nil
	}
	time.Sleep(50 * time.Millisecond)
	return 0, errors.New("kiro read failed")
}
func (r *kiroSlowFrameErrorReader) Close() error { return nil }

type blockingKiroUpstreamBody struct {
	readStarted chan struct{}
	closed      chan struct{}
	startOnce   sync.Once
	closeOnce   sync.Once
}

func newBlockingKiroUpstreamBody() *blockingKiroUpstreamBody {
	return &blockingKiroUpstreamBody{readStarted: make(chan struct{}), closed: make(chan struct{})}
}

func (b *blockingKiroUpstreamBody) Read([]byte) (int, error) {
	b.startOnce.Do(func() { close(b.readStarted) })
	<-b.closed
	return 0, context.Canceled
}

func (b *blockingKiroUpstreamBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestAccountTestService_KiroUsesKiroUpstreamInsteadOfAnthropic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          1,
		Name:        "kiro-test",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/TESTSOCIAL",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{1: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"type":"authentication_error","message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "gpt-4o", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)

	req := upstream.requests[0]
	require.Equal(t, "q.us-east-1.amazonaws.com", req.URL.Host)
	require.Equal(t, "/generateAssistantResponse", req.URL.Path)
	require.Equal(t, "Bearer kiro-access-token", req.Header.Get("Authorization"))
	require.Equal(t, "vibe", req.Header.Get("x-amzn-kiro-agent-mode"))
	require.Empty(t, req.Header.Get("anthropic-version"))
	require.NotContains(t, req.URL.Host, "api.anthropic.com")
}

func TestAccountTestService_Kiro429DoesNotFallbackToCodeWhispererEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          2,
		Name:        "kiro-fallback",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"api_region":   "us-west-2",
			"region":       "us-west-2",
			"profile_arn":  "arn:aws:codewhisperer:us-west-2:123456789012:profile/TESTFALLBACK",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{2: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusTooManyRequests, `{"message":"slow down"}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)

	require.Equal(t, "q.us-west-2.amazonaws.com", upstream.requests[0].URL.Host)
	require.Empty(t, upstream.requests[0].Header.Get("X-Amz-Target"))
	require.Contains(t, err.Error(), "API returned 429")
}

func TestAccountTestService_KiroIDCWithoutProfileArnOmitsProfileArnAndUsesIDCRegion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          5,
		Name:        "kiro-idc-default-region",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"auth_method":  "idc",
			"provider":     "AWS",
			"region":       "ap-northeast-2",
			"start_url":    "https://d-example.awsapps.com/start",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{5: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.ap-northeast-2.amazonaws.com", upstream.requests[0].URL.Host)
	body, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.NotContains(t, string(body), `"profileArn":`)
}

func TestAccountTestService_KiroInvalidModelErrorPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          6,
		Name:        "kiro-invalid-model",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-west-2:123456789012:profile/TESTINVALIDMODEL",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{6: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusBadRequest, `{"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-opus-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Equal(t, `API returned 400: {"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`, err.Error())
}

func TestAccountTestService_KiroInvalidModelDoesNotRefreshProfileArnOrRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          7,
		Name:        "kiro-invalid-model-refresh",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/STALE",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{7: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusBadRequest, `{"message":"Invalid model ID. Please select a different model to continue.","reason":"INVALID_MODEL_ID"}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-opus-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Contains(t, err.Error(), "API returned 400")
	require.Len(t, upstream.requests, 1)

	firstBody, readErr := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, readErr)
	require.Contains(t, string(firstBody), `"profileArn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/STALE"`)
	require.Equal(t, "arn:aws:codewhisperer:us-east-1:123456789012:profile/STALE", account.GetCredential("profile_arn"))
}

func TestAccountTestService_KiroPreferredEndpointIsIgnored(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := newTestContext()

	account := &Account{
		ID:          6,
		Name:        "kiro-preferred-endpoint",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token":       "kiro-access-token",
			"api_region":         "us-west-2",
			"profile_arn":        "arn:aws:codewhisperer:us-west-2:123456789012:profile/PREFERRED",
			"preferred_endpoint": "codewhisperer",
		},
	}
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{6: account}}
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{
			newJSONResponse(http.StatusUnauthorized, `{"type":"error","error":{"message":"Invalid bearer token"}}`),
		},
	}
	svc := &AccountTestService{
		accountRepo:         repo,
		kiroTokenProvider:   NewKiroTokenProvider(nil, nil, nil),
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}

	err := svc.TestAccountConnection(ctx, account.ID, "claude-sonnet-4-6", "", AccountTestModeDefault)
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.us-west-2.amazonaws.com", upstream.requests[0].URL.Host)
	require.Empty(t, upstream.requests[0].Header.Get("X-Amz-Target"))
}

func TestBuildKiroPayloadForAccount_KiroBuilderIDWithoutProfileArnOmitsProfileArn(t *testing.T) {
	account := &Account{
		ID:       3,
		Name:     "kiro-builder-id",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method": "idc",
			"provider":    "BuilderId",
			"region":      "us-east-1",
			"client_id":   "builder-client-id",
		},
	}

	testPayload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(testPayload)
	require.NoError(t, err)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, nil, payloadBytes, "claude-sonnet-4-6", "kiro-access-token", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	kiroPayload := buildResult.Payload
	require.NotContains(t, string(kiroPayload), `"profileArn":`)
}

func TestBuildKiroPayloadForAccount_KiroBuilderIDWithCachedProfileArnAddsForQMode(t *testing.T) {
	account := &Account{
		ID:       33,
		Name:     "kiro-builder-id-cached",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method": "builder-id",
			"provider":    "BuilderId",
			"region":      "us-east-1",
			"client_id":   "builder-client-id",
			"profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/CACHED",
		},
	}

	testPayload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(testPayload)
	require.NoError(t, err)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, nil, payloadBytes, "claude-sonnet-4-6", "kiro-access-token", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	kiroPayload := buildResult.Payload
	require.Contains(t, string(kiroPayload), `"profileArn":"arn:aws:codewhisperer:us-east-1:123456789012:profile/CACHED"`)
}

func TestForwardKiroMessagesStreamCapturesMeteringCredits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	account := &Account{
		ID:          21,
		Name:        "kiro-stream-credits",
		Platform:    PlatformKiro,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/STREAMCREDITS",
		},
	}
	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello"},
	}))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{
			"tokenUsage": map[string]any{
				"uncachedInputTokens": 7,
				"outputTokens":        3,
			},
		},
	}))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "meteringEvent", map[string]any{
		"meteringEvent": map[string]any{"usage": 0.17},
	}))
	rawUpstreamBody := snapshotBytes(upstreamBody.Bytes())
	upstream := &queuedHTTPUpstream{
		responses: []*http.Response{{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
			Body:       io.NopCloser(upstreamBody),
		}},
	}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				StreamDataIntervalTimeout: 0,
				MaxLineSize:               defaultMaxLineSize,
				Capture:                   config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
			},
		},
		rateLimitService: &RateLimitService{},
	}
	requestBody := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), domain.PlatformAnthropic)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.InDelta(t, 0.17, result.Usage.KiroCredits, 0.000001)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, rawUpstreamBody, result.CaptureResponse, "capture must preserve the provider-native AWS event stream")
	require.Len(t, upstream.requests, 1)
	require.Equal(t, snapshotHTTPRequestBody(upstream.requests[0]), result.UpstreamRequest,
		"native KIRO capture must preserve the final provider request")
	require.NotContains(t, string(result.CaptureResponse), "_sub2api_kiro_credits")
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_credits")
}

func TestForwardKiroMessagesCommittedPartialCarriesUsageAndCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	account := &Account{ID: 22, Name: "kiro-partial", Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "kiro-access-token", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/PARTIAL"}}
	var payload bytes.Buffer
	_, _ = payload.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"assistantResponseEvent": map[string]any{"content": "hello"}}))
	raw := append([]byte(nil), payload.Bytes()...)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: &kiroSlowFrameErrorReader{data: raw}}}}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{}, tlsFPProfileService: &TLSFingerprintProfileService{}, rateLimitService: &RateLimitService{}, cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}}
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.Error(t, err)
	require.NotNil(t, result)
	require.Greater(t, result.Usage.InputTokens, 0)
	require.Contains(t, string(result.CaptureResponse), "hello")
	require.Contains(t, rec.Body.String(), "hello")
}

func TestCapturePolicyMissAvoidsKiroResponseTee(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{
		ID: 24, Name: "kiro-policy-off", Platform: PlatformKiro, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/POLICYOFF",
			"region":       "us-east-1",
		},
	}
	requestBody := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), domain.PlatformAnthropic)
	require.NoError(t, err)

	t.Run("normal runtime stream", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		var raw bytes.Buffer
		_, _ = raw.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": "hello"},
		}))
		_, _ = raw.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
			"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 1, "outputTokens": 1}},
		}))
		upstream := &queuedHTTPUpstream{responses: []*http.Response{{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}},
			Body:       io.NopCloser(bytes.NewReader(raw.Bytes())),
		}}}
		cfg := &config.Config{Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
			Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
		}}
		svc := &GatewayService{
			cfg: cfg, httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
			tlsFPProfileService: &TLSFingerprintProfileService{}, rateLimitService: &RateLimitService{},
		}

		resp, _, err := svc.openKiroAnthropicStreamResponse(
			context.Background(), c, account, parsed, requestBody,
			"claude-sonnet-4-6", "claude-sonnet-4-6", c.Request.Header, nil,
		)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		_, captured := takeCaptureResult(c)
		require.False(t, captured, "runtime-policy miss must not tee the AWS response")
	})

	t.Run("websearch stream", func(t *testing.T) {
		endpoint := "https://q.us-east-1.amazonaws.com/mcp"
		kiroWebSearchDescCache.Store(endpoint, "Search the web")
		t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
		webSearchBody := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"news"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
		webParsed, parseErr := ParseGatewayRequest(NewRequestBodyRef(webSearchBody), domain.PlatformAnthropic)
		require.NoError(t, parseErr)
		mcpBody := []byte(`{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`)
		var raw bytes.Buffer
		_, _ = raw.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
			"assistantResponseEvent": map[string]any{"content": "answer"},
		}))
		_, _ = raw.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
			"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 1, "outputTokens": 1}},
		}))
		upstream := &queuedHTTPUpstream{responses: []*http.Response{
			{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(mcpBody))},
			{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(raw.Bytes()))},
		}}
		cfg := &config.Config{Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
			Capture:     config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
		}}
		svc := &GatewayService{
			cfg: cfg, httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
			tlsFPProfileService: &TLSFingerprintProfileService{}, rateLimitService: &RateLimitService{},
		}

		resp, _, err := svc.openKiroAnthropicStreamResponse(
			context.Background(), c, account, webParsed, webSearchBody,
			"claude-sonnet-4-6", "claude-sonnet-4-6", c.Request.Header, nil,
		)
		require.NoError(t, err)
		_, err = io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		_, captured := takeCaptureResult(c)
		require.False(t, captured, "runtime-policy miss must not tee the WebSearch AWS response")
	})
}

func TestForwardKiroMessagesPreOutputReadFailureIsFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := &Account{ID: 23, Name: "kiro-pre-output", Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "kiro-access-token", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/PRE"}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: &errTailReader{err: errors.New("kiro read failed")}}}}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{}, tlsFPProfileService: &TLSFingerprintProfileService{}, rateLimitService: &RateLimitService{}, cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize, Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20}}}}
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.ErrorAs(t, err, &failover)
	require.Equal(t, -1, c.Writer.Size())
	require.Empty(t, rec.Body.String())
	_, captured := takeCaptureResult(c)
	require.False(t, captured)
}

func TestForwardKiroMessagesCancellationClosesTranslatedAndRawBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := &Account{ID: 24, Name: "kiro-cancel", Platform: PlatformKiro, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, Credentials: map[string]any{"access_token": "kiro-access-token", "profile_arn": "arn:aws:codewhisperer:us-east-1:123456789012:profile/CANCEL"}}
	rawBody := newBlockingKiroUpstreamBody()
	defer func() { _ = rawBody.Close() }()
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: rawBody}}}
	svc := &GatewayService{httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{}, tlsFPProfileService: &TLSFingerprintProfileService{}, rateLimitService: &RateLimitService{}, cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan *ForwardResult, 1)
	errCh := make(chan error, 1)
	go func() {
		result, forwardErr := svc.forwardKiroMessages(ctx, c, account, parsed, time.Now())
		resultCh <- result
		errCh <- forwardErr
	}()

	select {
	case <-rawBody.readStarted:
	case <-time.After(time.Second):
		t.Fatal("KIRO translator did not start reading the raw upstream body")
	}
	cancel()

	var result *ForwardResult
	var forwardErr error
	returnedBeforeCleanup := true
	select {
	case result = <-resultCh:
		forwardErr = <-errCh
	case <-time.After(500 * time.Millisecond):
		returnedBeforeCleanup = false
		_ = rawBody.Close()
		result = <-resultCh
		forwardErr = <-errCh
	}
	require.True(t, returnedBeforeCleanup, "cancel must stop both shared scanner and KIRO translator")
	require.Nil(t, result)
	require.Error(t, forwardErr)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, forwardErr, &failoverErr)
	select {
	case <-rawBody.closed:
	default:
		t.Fatal("closing the translated response must also close KIRO's raw upstream body")
	}
	require.Equal(t, -1, c.Writer.Size())
}

func TestForwardKiroMessagesNonStreamPreservesFullCacheHitZeros(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroCacheTracker()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := kiroCacheAccount(31, "refresh-nonstream-hit", "kiro-access-token")
	account.Name = "kiro-nonstream-full-hit"
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 1
	account.Credentials["profile_arn"] = "arn:aws:codewhisperer:us-east-1:123456789012:profile/NONSTREAMHIT"

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "content with explicit zero output"},
	}))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "metadataEvent", map[string]any{
		"metadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens": 0, "cacheReadInputTokens": 120,
			"cacheWriteInputTokens": 0, "outputTokens": 0, "totalTokens": 120,
		}},
	}))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(upstreamBody),
	}}}
	svc := &GatewayService{
		httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	requestBody := kiroCacheRequestBody("nonstream full hit", false)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.Group = kiroCacheGroup(1)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Zero(t, result.Usage.InputTokens)
	require.Equal(t, 120, result.Usage.CacheReadInputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Zero(t, result.Usage.OutputTokens)
}

func TestForwardKiroMessagesNonStreamDirectAPIKeyReachesAWSUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	account := &Account{
		ID:          34,
		Name:        "kiro-nonstream-apikey",
		Platform:    PlatformKiro,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":    "kiro-api-key",
			"api_region": "us-west-2",
			"model_mapping": map[string]any{
				"claude-sonnet-4-6": "claude-sonnet-4-6",
			},
		},
	}
	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "hello from API key"},
	}))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "metadataEvent", map[string]any{
		"metadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens": 3, "outputTokens": 4, "totalTokens": 7,
		}},
	}))
	rawUpstreamBody := snapshotBytes(upstreamBody.Bytes())
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(upstreamBody),
	}}}
	svc := &GatewayService{
		httpUpstream:        upstream,
		kiroCooldownStore:   &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cfg: &config.Config{Gateway: config.GatewayConfig{
			Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
		}},
	}
	requestBody := []byte(`{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"hi"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), domain.PlatformAnthropic)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, "q.us-west-2.amazonaws.com", upstream.requests[0].URL.Host)
	require.Equal(t, "Bearer kiro-api-key", upstream.requests[0].Header.Get("Authorization"))
	require.Equal(t, []string{"API_KEY"}, upstream.requests[0].Header["tokentype"])
	require.Equal(t, snapshotHTTPRequestBody(upstream.requests[0]), result.UpstreamRequest,
		"native KIRO capture must preserve the final provider request")
	require.Equal(t, rawUpstreamBody, result.CaptureResponse,
		"non-stream capture must preserve raw AWS event-stream bytes, not translated JSON")
}

func TestForwardKiroMessagesNonStreamOnlyWebSearchCapturesFinalProviderPair(t *testing.T) {
	gin.SetMode(gin.TestMode)
	endpoint := "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(endpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(endpoint) })

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)
	account := &Account{
		ID: 35, Name: "kiro-nonstream-websearch", Platform: PlatformKiro, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"access_token": "kiro-access-token",
			"profile_arn":  "arn:aws:codewhisperer:us-east-1:123456789012:profile/WEBSEARCH",
			"region":       "us-east-1",
		},
	}
	mcpBody := []byte(`{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`)
	var providerBody bytes.Buffer
	_, _ = providerBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "answer"},
	}))
	_, _ = providerBody.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{"uncachedInputTokens": 4, "outputTokens": 2}},
	}))
	rawProviderBody := snapshotBytes(providerBody.Bytes())
	upstream := &webChatGeminiSequenceRecorder{responses: []*http.Response{
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewReader(mcpBody))},
		{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(rawProviderBody))},
	}}
	svc := &GatewayService{
		httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{}, rateLimitService: &RateLimitService{},
		cfg: &config.Config{Gateway: config.GatewayConfig{
			Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
		}},
	}
	body := []byte(`{"model":"claude-sonnet-4-6","stream":false,"messages":[{"role":"user","content":"news"}],"tools":[{"type":"web_search_20250305","name":"web_search"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), domain.PlatformAnthropic)
	require.NoError(t, err)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.bodies, 2, "MCP request then final AWS runtime request")
	require.Equal(t, upstream.bodies[1], result.UpstreamRequest)
	require.Equal(t, rawProviderBody, result.CaptureResponse)
}

func TestForwardKiroMessagesStreamContentBeforeMetadataFinalZerosReplaceProvisionalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resetKiroCacheTracker()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	account := kiroCacheAccount(32, "refresh-stream-hit", "kiro-access-token")
	account.Name = "kiro-stream-final-usage"
	account.Status = StatusActive
	account.Schedulable = true
	account.Concurrency = 1
	account.Credentials["profile_arn"] = "arn:aws:codewhisperer:us-east-1:123456789012:profile/STREAMHIT"

	upstreamBody := bytes.NewBuffer(nil)
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{
		"assistantResponseEvent": map[string]any{"content": "content arrives before metadata"},
	}))
	_, _ = upstreamBody.Write(buildKiroEventStreamFrame(t, "metadataEvent", map[string]any{
		"metadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens": 0, "cacheReadInputTokens": 120,
			"cacheWriteInputTokens": 0, "outputTokens": 0, "totalTokens": 120,
		}},
	}))
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/vnd.amazon.eventstream"}},
		Body:       io.NopCloser(upstreamBody),
	}}}
	svc := &GatewayService{
		httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cfg: &config.Config{Gateway: config.GatewayConfig{
			StreamDataIntervalTimeout: 0, MaxLineSize: defaultMaxLineSize,
		}},
		rateLimitService: &RateLimitService{},
	}
	requestBody := []byte(strings.Replace(string(kiroCacheRequestBody("stream provisional creation", false)), "{", `{"stream":true,`, 1))
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(requestBody), domain.PlatformAnthropic)
	require.NoError(t, err)
	parsed.Group = kiroCacheGroup(1)

	result, err := svc.forwardKiroMessages(context.Background(), c, account, parsed, time.Now())
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Zero(t, result.Usage.InputTokens)
	require.Equal(t, 120, result.Usage.CacheReadInputTokens)
	require.Zero(t, result.Usage.CacheCreationInputTokens)
	require.Zero(t, result.Usage.OutputTokens)

	start := findAnthropicSSEEventData(t, rec.Body.String(), "message_start")
	delta := findAnthropicSSEEventData(t, rec.Body.String(), "message_delta")
	require.Greater(t, gjson.GetBytes(start, "message.usage.cache_creation_input_tokens").Int(), int64(0))
	require.Zero(t, gjson.GetBytes(delta, "usage.input_tokens").Int())
	require.Equal(t, int64(120), gjson.GetBytes(delta, "usage.cache_read_input_tokens").Int())
	require.Zero(t, gjson.GetBytes(delta, "usage.cache_creation_input_tokens").Int())
	require.NotContains(t, rec.Body.String(), "_sub2api_kiro_final_usage")
}

func findAnthropicSSEEventData(t *testing.T, stream, eventName string) []byte {
	t.Helper()
	marker := "event: " + eventName + "\ndata: "
	start := strings.Index(stream, marker)
	require.NotEqual(t, -1, start, "missing %s event", eventName)
	data := stream[start+len(marker):]
	end := strings.Index(data, "\n\n")
	require.NotEqual(t, -1, end, "unterminated %s event", eventName)
	return []byte(data[:end])
}

func buildKiroEventStreamFrame(t *testing.T, eventType string, payload map[string]any) []byte {
	t.Helper()
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	headerName := []byte(":event-type")
	headerValue := []byte(eventType)
	headersLen := 1 + len(headerName) + 1 + 2 + len(headerValue)
	totalLen := 12 + headersLen + len(payloadBytes) + 4
	frame := make([]byte, totalLen)
	binary.BigEndian.PutUint32(frame[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(frame[4:8], uint32(headersLen))
	offset := 12
	frame[offset] = byte(len(headerName))
	offset++
	copy(frame[offset:], headerName)
	offset += len(headerName)
	frame[offset] = 7 // string header
	offset++
	binary.BigEndian.PutUint16(frame[offset:offset+2], uint16(len(headerValue)))
	offset += 2
	copy(frame[offset:], headerValue)
	offset += len(headerValue)
	copy(frame[offset:], payloadBytes)
	return frame
}

func TestBuildKiroPayloadForAccount_KiroEnterpriseIDCOmitsMissingProfileArn(t *testing.T) {
	account := &Account{
		ID:       4,
		Name:     "kiro-enterprise-idc",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_method": "idc",
			"provider":    "AWS",
			"region":      "us-east-1",
			"client_id":   "enterprise-client-id",
			"start_url":   "https://d-example.awsapps.com/start",
		},
	}

	testPayload, err := createTestPayload("claude-sonnet-4-6")
	require.NoError(t, err)
	payloadBytes, err := json.Marshal(testPayload)
	require.NoError(t, err)

	buildResult, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, nil, payloadBytes, "claude-sonnet-4-6", "kiro-access-token", "claude-sonnet-4-6", nil)
	require.NoError(t, err)
	kiroPayload := buildResult.Payload
	require.NotContains(t, string(kiroPayload), `"profileArn":`)
}

func TestBuildKiroPayloadForAccount_StableConversationIDByDefault(t *testing.T) {
	t.Setenv("SUB2API_KIRO_CONVERSATION_ID_MODE", "")
	account := &Account{
		ID:       44,
		Name:     "kiro-stable-conversation",
		Platform: PlatformKiro,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "stable-refresh",
			"profile_arn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/STABLE",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4-5","system":"stable sys","messages":[{"role":"user","content":"hello"}]}`)
	ref := NewRequestBodyRef(body)
	parsed, err := ParseGatewayRequest(ref, "anthropic")
	require.NoError(t, err)
	parsed.Group = &Group{Platform: PlatformKiro}
	parsed.SessionContext = &SessionContext{APIKeyID: 9}

	first, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, parsed, body, "claude-sonnet-4.5", "kiro-access-token", "claude-sonnet-4.5", nil)
	require.NoError(t, err)
	second, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, parsed, body, "claude-sonnet-4.5", "rotated-token", "claude-sonnet-4.5", nil)
	require.NoError(t, err)

	firstID := gjson.GetBytes(first.Payload, "conversationState.conversationId").String()
	secondID := gjson.GetBytes(second.Payload, "conversationState.conversationId").String()
	require.NotEmpty(t, firstID)
	require.Equal(t, firstID, secondID)

	t.Setenv("SUB2API_KIRO_CONVERSATION_ID_MODE", "random")
	randomized, err := (&GatewayService{}).buildKiroPayloadForAccount(context.Background(), account, parsed, body, "claude-sonnet-4.5", "kiro-access-token", "claude-sonnet-4.5", nil)
	require.NoError(t, err)
	require.NotEqual(t, firstID, gjson.GetBytes(randomized.Payload, "conversationState.conversationId").String())
}
