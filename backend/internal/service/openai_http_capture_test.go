package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
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

type captureHTTPUpstreamStub struct {
	rawResponse []byte
	request     *http.Request
}

func (s *captureHTTPUpstreamStub) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	s.request = req
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(s.rawResponse)),
		Request:    req,
	}, nil
}

func (s *captureHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, concurrency)
}

func TestOpenAIHTTPCaptureEndpointMatrixUsesActualSendPipelineIndependentOfPassthrough(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
		for _, passthrough := range []bool{false, true} {
			t.Run(path+"/passthrough="+fmt.Sprint(passthrough), func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, path, nil)
				c.Set("openai_passthrough", passthrough)
				setOpenAIHTTPCaptureScopeForTest(t, c, true)
				upstream := &captureHTTPUpstreamStub{rawResponse: []byte(`{"id":"upstream-raw"}`)}
				svc := &OpenAIGatewayService{cfg: captureEnabledConfigForTest(1024), httpUpstream: upstream}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "secret"}}
				outbound := []byte(`{"model":"mapped-provider-model"}`)

				resp, err := svc.sendCCUpstreamRequest(context.Background(), c, account, "https://api.openai.test/v1/chat/completions", outbound, false, "secret", "", "")
				require.NoError(t, err)
				_, err = io.Copy(io.Discard, resp.Body)
				require.NoError(t, err)
				finishOpenAIHTTPCapture(resp)

				result := &OpenAIForwardResult{}
				svc.applyOpenAIHTTPSuccessCapture(c, account, result)
				require.Equal(t, outbound, result.CaptureRequest)
				require.Equal(t, upstream.rawResponse, result.CaptureResponse)
				require.Equal(t, "https://api.openai.test/v1/chat/completions", result.CaptureUpstreamEndpoint)
				require.NotContains(t, string(result.CaptureRequestHeaders), "secret")
			})
		}
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
