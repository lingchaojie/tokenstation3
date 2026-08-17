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
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:         captureEnabledConfigForTest(1024),
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
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
	require.Nil(t, result.CaptureRequest)
	require.Nil(t, result.CaptureResponse)
	require.Empty(t, result.CaptureUpstreamEndpoint)
	require.Zero(t, result.CaptureHTTPStatus)
	require.Nil(t, result.CaptureContentPolicy)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, outbound, attempts[0].RequestBytes())
	require.Equal(t, rawResponse, attempts[0].ResponseBytes())
	require.Equal(t, captureHeaderBytes(req.Header, svc.cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].RequestHeaderBytes())
	require.Equal(t, captureHeaderBytes(resp.Header, svc.cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].ResponseHeaderBytes())
	require.Contains(t, attempts[0].begin.UpstreamEndpoint, "api.openai.test")
	require.NotContains(t, string(attempts[0].RequestHeaderBytes()), "upstream-secret")
	require.Contains(t, string(attempts[0].RequestHeaderBytes()), "Openai-Beta")
	require.Empty(t, attempts[0].TerminalStates())
	AbortCaptureAttempt(c)
}

func TestFinalizeOpenAIForwardResultKeepsFinalTypedAttemptRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:         captureEnabledConfigForTest(1024),
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	account := &Account{Platform: PlatformOpenAI}
	initialBody := []byte(`{"model":"initial","stream":true}`)
	initial := httptest.NewRequest(http.MethodPost, "https://proxy.example/v1/responses", bytes.NewReader(initialBody))
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, initial, initialBody))

	finalBody := []byte(`{"model":"final","stream":false}`)
	final := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", bytes.NewReader(finalBody))
	final.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(finalBody)), nil }
	final.Header.Set("X-Final-Request", "yes")
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, final, finalBody))
	rawResponse := []byte(`{"id":"final-response"}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Xai-Request-Id": {"final-request-id"}},
		Body:       io.NopCloser(bytes.NewReader(rawResponse)),
		Request:    final,
	}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(resp)

	result := &OpenAIForwardResult{UpstreamModel: "initial", Stream: true}
	svc.applyOpenAIHTTPSuccessCapture(c, account, result)
	result = finalizeOpenAIForwardResult(c, result, finalBody)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureRequest)
	require.Nil(t, result.CaptureResponse)
	require.Equal(t, HashUsageRequestPayload(finalBody), result.UpstreamRequestHash)
	attempts := transport.Attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
	require.Equal(t, finalBody, attempts[1].RequestBytes())
	require.Equal(t, rawResponse, attempts[1].ResponseBytes())
	require.Equal(t, "final", attempts[1].begin.UpstreamModel)
	require.False(t, attempts[1].begin.Stream)
	require.Contains(t, string(attempts[1].RequestHeaderBytes()), "X-Final-Request")
	require.Empty(t, attempts[1].TerminalStates())
	AbortCaptureAttempt(c)
}

func TestFinalizeOpenAIForwardResultCaptureMissDoesNotSnapshotRequest(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := bytes.Repeat([]byte("x"), 1<<20)
	result := finalizeOpenAIForwardResult(c, &OpenAIForwardResult{}, body)
	require.Nil(t, result.UpstreamRequest)
	require.Nil(t, result.CaptureRequest)
	require.Equal(t, HashUsageRequestPayload(body), result.UpstreamRequestHash)
}

func TestOpenAIHTTPCaptureKeepsObservedEmptySuccessResponse(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:         captureEnabledConfigForTest(1024),
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	account := &Account{Platform: PlatformOpenAI}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, req, []byte(`{"model":"gpt-5"}`)))
	resp := &http.Response{
		StatusCode: http.StatusNoContent,
		Header:     http.Header{"X-Request-Id": []string{"empty-success"}},
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    req,
	}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(resp)

	result := &OpenAIForwardResult{}
	svc.applyOpenAIHTTPSuccessCapture(c, account, result)
	require.Nil(t, result.CaptureContentPolicy)
	require.Zero(t, result.CaptureHTTPStatus)
	require.Nil(t, result.CaptureRequest)
	require.Nil(t, result.CaptureResponse)
	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, []byte(`{"model":"gpt-5"}`), attempts[0].RequestBytes())
	require.Empty(t, attempts[0].ResponseBytes())
	require.Contains(t, string(attempts[0].ResponseHeaderBytes()), "empty-success")
	require.Empty(t, attempts[0].TerminalStates())
	AbortCaptureAttempt(c)
}

func TestOpenAIHTTPCaptureStreamsPastRetiredGatewayBodyLimit(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:         captureEnabledConfigForTest(4),
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, []byte(`{}`)))
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte("123456"))), Request: req}
	svc.wrapOpenAIHTTPCaptureResponse(c, &Account{Platform: PlatformOpenAI}, resp)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(resp)

	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, []byte("123456"), attempts[0].ResponseBytes())
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge)
	require.Empty(t, attempts[0].TerminalStates())
	AbortCaptureAttempt(c)
}

func TestOpenAIHTTPCaptureUsesTypedReaderWithoutGatewayHardMaximum(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:         captureEnabledConfigForTest(4),
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/chat/completions", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, &Account{Platform: PlatformOpenAI}, req, []byte(`{}`)))
	body := bytes.Repeat([]byte("x"), 64<<10)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Request: req}
	svc.wrapOpenAIHTTPCaptureResponse(c, &Account{Platform: PlatformOpenAI}, resp)
	_, ok := resp.Body.(*captureResponseReader)
	require.True(t, ok)
	_, err := io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(resp)

	attempts := transport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, body, attempts[0].ResponseBytes())
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge)
	AbortCaptureAttempt(c)
}

func TestOpenAIHTTPCaptureRetryKeepsOnlyFinalAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024), capturePool: pool}
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
	require.True(t, CommitOpenAIForwardCaptureAttempt(c, string(account.Platform), result))
	require.Len(t, transport.Attempts(), 2)
	require.Equal(t, []captureTerminalState{captureAborted}, transport.Attempts()[0].TerminalStates())
	require.Equal(t, []captureTerminalState{captureCommitted}, transport.Attempts()[1].TerminalStates())
	require.Equal(t, []byte(`{"attempt":2}`), transport.Attempts()[1].RequestBytes())
	require.Equal(t, []byte(`{"ok":true}`), transport.Attempts()[1].ResponseBytes())
}

func TestOpenAIHTTPCaptureLateFinalizerCannotOverwriteNewAttempt(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	transport := &recordingCaptureTransport{}
	svc := &OpenAIGatewayService{
		cfg:         captureEnabledConfigForTest(1024),
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	account := &Account{Platform: PlatformOpenAI}

	firstReq := httptest.NewRequest(http.MethodPost, "https://first.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, firstReq, []byte(`{"attempt":1}`)))
	firstResp := &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(bytes.NewReader([]byte(`{"old":true}`))), Request: firstReq}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, firstResp)
	_, err := io.Copy(io.Discard, firstResp.Body)
	require.NoError(t, err)

	secondReq := httptest.NewRequest(http.MethodPost, "https://second.openai.test/v1/responses", nil)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, secondReq, []byte(`{"attempt":2}`)))
	secondResp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader([]byte(`{"new":true}`))), Request: secondReq}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, secondResp)
	_, err = io.Copy(io.Discard, secondResp.Body)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(secondResp)
	finishOpenAIHTTPCapture(firstResp)

	attempts := transport.Attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, []byte(`{"attempt":1}`), attempts[0].RequestBytes())
	require.Equal(t, []byte(`{"old":true}`), attempts[0].ResponseBytes())
	require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
	require.Equal(t, []byte(`{"attempt":2}`), attempts[1].RequestBytes())
	require.Equal(t, []byte(`{"new":true}`), attempts[1].ResponseBytes())
	require.Contains(t, attempts[1].begin.UpstreamEndpoint, "second.openai.test")
	require.Empty(t, attempts[1].TerminalStates())
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge)
	AbortCaptureAttempt(c)
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
			enableCaptureForTest(t, c)
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
				transport := &recordingCaptureTransport{}
				svc := &OpenAIGatewayService{
					cfg:          cfg,
					httpUpstream: upstream,
					capturePool:  newConversationCapturePoolForTransport(transport, func() bool { return true }),
				}
				account := &Account{
					ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
					Credentials: map[string]any{"api_key": "secret", "base_url": "https://api.openai.test"},
					Extra:       map[string]any{"use_responses_api": true, "openai_passthrough": passthrough},
				}

				result, err := tc.forward(svc, c, account, tc.body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Nil(t, result.UpstreamRequest)
				require.Nil(t, result.CaptureRequest)
				require.Nil(t, result.CaptureResponse)
				require.Empty(t, result.CaptureUpstreamEndpoint)
				attempts := transport.Attempts()
				require.Len(t, attempts, 1)
				require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
				require.Equal(t, []byte(tc.responseBody), attempts[0].ResponseBytes())
				require.Equal(t, captureHeaderBytes(upstream.lastReq.Header, cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].RequestHeaderBytes())
				require.Equal(t, captureHeaderBytes(upstream.resp.Header, cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].ResponseHeaderBytes())
				require.Contains(t, attempts[0].begin.UpstreamEndpoint, "api.openai.test")
				require.NotContains(t, string(attempts[0].RequestHeaderBytes()), "secret")
				require.Empty(t, attempts[0].TerminalStates(), "the handler-side usage sink owns commit")
				if tc.name == "responses" {
					actualPassthrough, _ := c.Get("openai_passthrough")
					require.Equal(t, passthrough, actualPassthrough == true)
				}
				AbortCaptureAttempt(c)
			})
		}
	}
}

func TestOpenAIHTTPCaptureRealForwardStoresEmptyTerminalHTTPBody(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(strconv.FormatBool(passthrough), func(t *testing.T) {
			transport := &recordingCaptureTransport{}
			pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
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
			require.NotNil(t, result)
			require.True(t, result.UpstreamFailed)
			require.True(t, result.CaptureTerminalError)
			require.True(t, result.CaptureResponseComplete)
			require.Equal(t, http.StatusBadRequest, result.UpstreamHTTPStatus)
			require.Nil(t, result.CaptureRequest)
			require.Nil(t, result.CaptureResponse)
			attempts := transport.Attempts()
			require.Len(t, attempts, 1)
			require.Equal(t, upstream.lastBody, attempts[0].RequestBytes())
			require.Empty(t, attempts[0].ResponseBytes())
			require.Equal(t, captureHeaderBytes(upstream.lastReq.Header, cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].RequestHeaderBytes())
			require.Equal(t, captureHeaderBytes(upstream.resp.Header, cfg.Gateway.Capture.MaxHeaderBytes), attempts[0].ResponseHeaderBytes())
			require.Empty(t, attempts[0].TerminalStates(), "the handler-side terminal-error sink owns commit")
			require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result))
			require.Equal(t, []captureTerminalState{captureCommitted}, attempts[0].TerminalStates())
		})
	}
}

func captureEnabledConfigForTest(limit int) *config.Config {
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = limit
	cfg.Gateway.Capture.MaxHeaderBytes = 1 << 20
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

type trackingCaptureReadCloser struct {
	reader    io.Reader
	bytesRead int
	closes    int
}

func (r *trackingCaptureReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytesRead += n
	return n, err
}

func (r *trackingCaptureReadCloser) Close() error {
	r.closes++
	return nil
}

func TestResponseTeeForwardsOnlyBytesReadByProviderConsumer(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	source := &trackingCaptureReadCloser{reader: strings.NewReader("abcdef")}
	reader := newCaptureResponseReader(source, attempt)

	buf := make([]byte, 3)
	_, err := io.ReadFull(reader, buf)
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), transport.Attempts()[0].ResponseBytes())
	require.Equal(t, 3, source.bytesRead, "capture must not read beyond the provider consumer")
}

func TestResponseTeeCloseOwnsOnlyUnderlyingReader(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	source := &trackingCaptureReadCloser{reader: strings.NewReader("body")}
	reader := newCaptureResponseReader(source, attempt)

	require.NoError(t, reader.Close())
	require.NoError(t, reader.Close())
	require.Equal(t, 1, source.closes)
	require.Empty(t, transport.Attempts()[0].TerminalStates(), "response close must not steal commit/abort ownership")
	require.Empty(t, transport.Attempts()[0].ResponseBytes(), "response close must not perform an extra read")
}

func TestOpenAIHTTPFinalAttemptStreamsWithoutResultBodyBuffer(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	setOpenAIHTTPCaptureScopeForTest(t, c, true)
	cfg := captureEnabledConfigForTest(32 << 20)
	cfg.Gateway.Capture.MaxHeaderBytes = 1 << 20
	svc := &OpenAIGatewayService{cfg: cfg, capturePool: pool}
	account := &Account{Platform: PlatformOpenAI}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.test/v1/responses", nil)
	body := []byte(`{"model":"mapped-gpt","stream":true}`)
	require.True(t, svc.prepareOpenAIHTTPCaptureAttempt(c, account, req, body))
	source := &trackingCaptureReadCloser{reader: strings.NewReader("abcdef")}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": []string{"upstream-id"}}, Body: source, Request: req}
	svc.wrapOpenAIHTTPCaptureResponse(c, account, resp)

	buf := make([]byte, 3)
	_, err := io.ReadFull(resp.Body, buf)
	require.NoError(t, err)
	finishOpenAIHTTPCapture(resp)
	result := &OpenAIForwardResult{}
	svc.applyOpenAIHTTPSuccessCapture(c, account, result)

	require.Len(t, transport.Attempts(), 1, "OpenAI HTTP capture must begin the sidecar attempt")
	recording := transport.Attempts()[0]
	require.Equal(t, body, recording.RequestBytes())
	require.Equal(t, []byte("abc"), recording.ResponseBytes())
	require.Equal(t, 3, source.bytesRead)
	require.Nil(t, result.CaptureResponse, "production results must not retain a whole response body")
	_, legacyBridge := takeCaptureResult(c)
	require.False(t, legacyBridge, "OpenAI streaming attempt must not allocate the legacy whole-body bridge")
	require.Empty(t, recording.TerminalStates(), "the handler side-effect sink still owns commit")
}
