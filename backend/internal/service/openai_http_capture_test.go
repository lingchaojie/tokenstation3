package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIHTTPCaptureDefaultPolicyAllocatesNothing(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024)}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)

	require.False(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, []byte(`{"model":"gpt-5"}`)))
	_, exists := c.Get(captureResultContextKey)
	require.False(t, exists)
}

func TestOpenAIHTTPCaptureKeepsActualOutboundAndRawResponse(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024)}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	req.Header.Set("Authorization", "Bearer upstream-secret")
	req.Header.Set("OpenAI-Beta", "responses=v1")
	outbound := []byte(`{"model":"mapped-gpt"}`)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, outbound))

	rawResponse := []byte("data: {\"type\":\"response.completed\"}\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"X-Request-Id": []string{"req-upstream"}},
		Body:       io.NopCloser(bytes.NewReader(rawResponse)),
		Request:    req,
	}
	svc.wrapOpenAIHTTPCaptureResponse(c, &Account{Platform: PlatformOpenAI}, resp)
	consumed, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, rawResponse, consumed)
	finishOpenAIHTTPCapture(resp)

	result := &OpenAIForwardResult{}
	svc.applyOpenAIHTTPSuccessCapture(c, &Account{Platform: PlatformOpenAI}, result)
	require.Equal(t, outbound, result.CaptureRequest)
	require.Equal(t, rawResponse, result.CaptureResponse)
	require.Equal(t, "https://api.openai.test/v1/responses", result.CaptureUpstreamEndpoint)
	require.Equal(t, http.StatusOK, result.CaptureHTTPStatus)
	require.NotContains(t, string(result.CaptureRequestHeaders), "upstream-secret")
	require.Contains(t, string(result.CaptureRequestHeaders), "Openai-Beta")
	require.NotNil(t, result.CaptureContentPolicy)
}

func TestOpenAIHTTPCaptureResponseIsBounded(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(4)}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, []byte(`{}`)))
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte("123456"))), Request: req}
	svc.wrapOpenAIHTTPCaptureResponse(c, &Account{Platform: PlatformOpenAI}, resp)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(resp)

	bridge, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, []byte("1234"), bridge.Response)
	require.True(t, bridge.Truncated)
}

func TestOpenAIHTTPCaptureRetryKeepsOnlyFinalAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024)}
	account := &Account{Platform: PlatformOpenAI}

	firstReq := httptest.NewRequest(http.MethodPost, "https://first.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, firstReq, []byte(`{"attempt":1}`)))
	firstResp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(bytes.NewReader([]byte(`{"error":"retry"}`))), Request: firstReq}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, firstResp)
	_, err := io.Copy(io.Discard, firstResp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(firstResp)

	finalReq := httptest.NewRequest(http.MethodPost, "https://final.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, finalReq, []byte(`{"attempt":2}`)))
	finalResp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{"ok":true}`))), Request: finalReq}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, finalResp)
	_, err = io.Copy(io.Discard, finalResp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(finalResp)

	result := &OpenAIForwardResult{}
	svc.applyOpenAIHTTPSuccessCapture(c, account, result)
	require.Equal(t, []byte(`{"attempt":2}`), result.CaptureRequest)
	require.Equal(t, []byte(`{"ok":true}`), result.CaptureResponse)
	require.Equal(t, "https://final.openai.test/v1/responses", result.CaptureUpstreamEndpoint)
}

func TestOpenAIHTTPCaptureRejectsNonTextEndpoints(t *testing.T) {
	for _, path := range []string{
		"/v1/responses/compact",
		"/v1/images/generations",
		"/v1/videos",
		"/v1/embeddings",
		"/v1/responses/ws",
	} {
		t.Run(path, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, path, nil)
			setOpenAIHTTPCaptureScopeForTest(t, c, true)
			svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024)}
			req := httptest.NewRequest(http.MethodPost, "https://api.openai.test"+path, nil)
			require.False(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, []byte(`{}`)))
			_, exists := c.Get(captureResultContextKey)
			require.False(t, exists)
		})
	}
}

func TestOpenAIHTTPCaptureRejectsResponsesImageIntent(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
	SetOpenAIImageIntentHint(c, true)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024)}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	require.False(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, []byte(`{"model":"gpt-image"}`)))
}

func TestOpenAIHTTPCaptureEndpointMatrixUsesRealForwardersIndependentOfPassthrough(t *testing.T) {
	completedSSE := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"resp_native","object":"response","model":"gpt-5","status":"completed","output":[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	cases := []struct {
		name         string
		path         string
		body         []byte
		responseBody string
		contentType  string
		forward      func(*OpenAIGatewayService, *gin.Context, *Account, []byte) (*OpenAIForwardResult, error)
	}{
		{
			name: "responses", path: "/v1/responses",
			body:         []byte(`{"model":"gpt-5","stream":false,"input":"hello"}`),
			responseBody: `{"id":"resp_native","model":"gpt-5","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`,
			contentType:  "application/json",
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.Forward(context.Background(), c, account, body)
			},
		},
		{
			name: "chat-completions", path: "/v1/chat/completions",
			body:         []byte(`{"model":"gpt-5","stream":false,"messages":[{"role":"user","content":"hello"}]}`),
			responseBody: completedSSE, contentType: "text/event-stream",
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
			},
		},
		{
			name: "messages", path: "/v1/messages",
			body:         []byte(`{"model":"gpt-5","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`),
			responseBody: completedSSE, contentType: "text/event-stream",
			forward: func(s *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) (*OpenAIForwardResult, error) {
				return s.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
			},
		},
	}
	for _, tc := range cases {
		for _, passthrough := range []bool{false, true} {
			t.Run(tc.name+"/passthrough="+strconv.FormatBool(passthrough), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
				SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
				setOpenAIHTTPCaptureScopeForTest(t, c, true)
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{tc.contentType}},
					Body:       io.NopCloser(strings.NewReader(tc.responseBody)),
				}}
				cfg := captureEnabledConfigForTest(1 << 20)
				cfg.Security.URLAllowlist.Enabled = false
				svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
				account := &Account{
					ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "secret", "base_url": "https://api.openai.test"},
					Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough},
				}

				result, err := tc.forward(svc, c, account, tc.body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, []byte(tc.responseBody), result.CaptureResponse)
				require.NotEmpty(t, result.CaptureRequest)
				require.Contains(t, result.CaptureUpstreamEndpoint, "api.openai.test")
				require.NotContains(t, string(result.CaptureRequestHeaders), "secret")
				if tc.name == "responses" {
					actualPassthrough, _ := c.Get("openai_passthrough")
					require.Equal(t, passthrough, actualPassthrough == true)
				}
			})
		}
	}
}

func TestOpenAIHTTPCaptureRealForwardStoresEmptyTerminalHTTPBody(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(strconv.FormatBool(passthrough), func(t *testing.T) {
			writer := newDeferredLifecycleWriter()
			pool := newConversationCapturePool(conversationCapturePoolOptions{
				WorkerCount: 1, QueueSize: 8, WriterQueueSize: 8, OverflowPolicy: "drop", MaxQueueBytes: 1 << 20,
			}, writer)
			t.Cleanup(pool.Stop)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader("")),
			}}
			cfg := captureEnabledConfigForTest(1 << 20)
			cfg.Security.URLAllowlist.Enabled = false
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream, capturePool: pool}
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "secret", "base_url": "https://api.openai.test"},
				Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough},
			}
			body := []byte(`{"model":"gpt-5","stream":false,"input":"hello"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			setOpenAIHTTPCaptureScopeForTest(t, c, true)

			result, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Nil(t, result)
			item := writer.take(t)
			require.Equal(t, http.StatusBadRequest, item.record.HTTPStatus)
			require.Empty(t, item.record.RawResponse)
			require.NotEmpty(t, item.record.RawRequest)
			item.completeSuccess()
		})
	}
}

func captureEnabledConfigForTest(limit int) *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = limit
	return cfg
}

func setOpenAIHTTPCaptureScopeForTest(t *testing.T, c *gin.Context, openAI bool) {
	t.Helper()
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	policy.Platforms.OpenAI = openAI
	compiled, err := CompileCaptureRuntimePolicy(policy)
	require.NoError(t, err)
	setCompiledCaptureScopeForTest(c, compiled, 9, nil)
}
