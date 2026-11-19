//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func buildResponsesFailedSSEStream(errType, errorMessage string) string {
	failed := fmt.Sprintf(`{"type":"response.failed","response":{"id":"resp_err","object":"response","status":"failed","error":{"type":"%s","message":"%s"},"output":[],"usage":{"input_tokens":10,"output_tokens":0,"total_tokens":10}}}`, errType, errorMessage)
	return fmt.Sprintf("data: %s\n\n", failed)
}

func TestForwardAsAnthropic_BufferedResponseFailed_ReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesFailedSSEStream("invalid_request_error", "Content policy violation")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err, "non-cyber response.failed must return an error, not swallow as 200")
	require.Contains(t, err.Error(), "upstream response failed")
	require.Equal(t, http.StatusBadGateway, rec.Code, "should write 502 for non-failover failed response")
}

func TestForwardAsAnthropic_StreamingResponseFailed_ReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesFailedSSEStream("invalid_request_error", "Content policy violation")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err, "streaming response.failed must return an error")
	require.Contains(t, err.Error(), "upstream response failed")
}

func TestForwardAsAnthropic_StreamingBareErrorAfterOutputIsVisible(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_bare_error","object":"response","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_bare_error","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		"",
		`data: {"type":"response.content_part.added","item_id":"msg_bare_error","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`,
		"",
		`data: {"type":"response.output_text.delta","item_id":"msg_bare_error","output_index":0,"content_index":0,"delta":"partial"}`,
		"",
		`event: error`,
		`data: {"type":"error","error":{"type":"server_error","code":"upstream_error","message":"mixed tools failed"}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	require.Contains(t, err.Error(), "upstream response failed: mixed tools failed")
	clientStream := rec.Body.String()
	require.Contains(t, clientStream, `"text":"partial"`)
	require.Contains(t, clientStream, "event: error")
	require.Contains(t, clientStream, "mixed tools failed")
	require.NotContains(t, clientStream, "event: message_stop")
	require.NotContains(t, err.Error(), "missing terminal event")
}

func TestHandleAnthropicStreamingResponseStopsAtTerminalWithoutValidatingTail(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := strings.Join([]string{
		`data: {"type":"response.completed","response":{"id":"terminal-tail","status":"completed","output":[],"usage":{"input_tokens":9,"output_tokens":1}}}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"tail"}`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(strings.NewReader(body)),
	}
	svc := &OpenAIGatewayService{}

	result, err := svc.handleAnthropicStreamingResponse(resp, c, rawChatCompletionsTestAccount(), "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now(), nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "terminal-tail", result.ResponseID)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.True(t, c.Writer.Written())
	require.NotContains(t, rec.Body.String(), `"delta":"tail"`)
}

func TestForwardAsAnthropicRetryableFailedUsageKeepsCommitBoundaryAndTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, afterOutput := range []bool{false, true} {
		t.Run(fmt.Sprint(afterOutput), func(t *testing.T) {
			ctx := openAIUltrafastGroupContext()
			body := []byte(`{"model":"gpt-6.1-sol","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body)).WithContext(ctx)
			sse := `data: {"type":"response.created","response":{"id":"resp_partial","status":"in_progress","output":[]}}` + "\n\n"
			if afterOutput {
				sse += `data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"partial"}` + "\n\n"
			}
			sse += buildResponsesFailedSSEStream("server_error", "An error occurred while processing your request.")
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			result, err := svc.ForwardAsAnthropic(ctx, c, rawChatCompletionsTestAccount(), body, "", "")
			var failover *UpstreamFailoverError
			if !afterOutput {
				require.ErrorAs(t, err, &failover)
				require.Nil(t, result)
				require.Empty(t, rec.Body.String())
				return
			}
			require.Error(t, err)
			require.NotErrorAs(t, err, &failover)
			require.NotNil(t, result)
			require.Equal(t, 10, result.Usage.InputTokens)
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, OpenAIFastTierUltrafast, *result.ServiceTier)
			require.Contains(t, rec.Body.String(), "event: error")
		})
	}
}

func TestForwardAsAnthropicStreamingTerminalUsagePrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []struct {
		name            string
		terminalUsage   string
		wantInput       int
		wantOutput      int
		wantImageInput  int
		wantImageOutput int
		wantKiroCredits float64
	}{
		{
			name:          "zero terminal keeps progressive usage",
			terminalUsage: `"response":{"id":"resp_usage","status":"completed","usage":{"input_tokens":0,"output_tokens":0}}`,
			wantInput:     8,
			wantOutput:    1,
		},
		{
			name:          "positive nested snapshot takes precedence",
			terminalUsage: `"usage":{"input_tokens":2,"output_tokens":1},"response":{"id":"resp_usage","status":"completed","usage":{"input_tokens":11,"output_tokens":3}}`,
			wantInput:     11,
			wantOutput:    3,
		},
		{
			name:          "zero nested snapshot keeps positive event usage",
			terminalUsage: `"usage":{"input_tokens":13,"output_tokens":5},"response":{"id":"resp_usage","status":"completed","usage":{"input_tokens":0,"output_tokens":0}}`,
			wantInput:     13,
			wantOutput:    5,
		},
		{
			name:            "hosted image usage augments progressive tokens when terminal usage is absent",
			terminalUsage:   `"response":{"id":"resp_usage","status":"completed","tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":2},"output_tokens_details":{"image_tokens":3}}}}`,
			wantInput:       8,
			wantOutput:      1,
			wantImageInput:  2,
			wantImageOutput: 3,
		},
		{
			name:            "hosted image usage augments progressive tokens when terminal usage is zero",
			terminalUsage:   `"response":{"id":"resp_usage","status":"completed","usage":{"input_tokens":0,"output_tokens":0},"tool_usage":{"image_gen":{"output_tokens_details":{"image_tokens":3}}}}`,
			wantInput:       8,
			wantOutput:      1,
			wantImageOutput: 3,
		},
		{
			name:            "event credits supplement the authoritative nested token usage",
			terminalUsage:   `"usage":{"_sub2api_kiro_credits":1.5},"response":{"id":"resp_usage","status":"completed","usage":{"input_tokens":11,"output_tokens":3}}`,
			wantInput:       11,
			wantOutput:      3,
			wantKiroCredits: 1.5,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, c, request := newPreContentContext()
			sse := preContentCreatedFrame +
				`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"partial","usage":{"input_tokens":8,"output_tokens":1}}` + "\n\n" +
				`data: {"type":"response.completed",` + tc.terminalUsage + "}\n\n"
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(sse)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

			result, err := svc.ForwardAsAnthropic(c.Request.Context(), c, rawChatCompletionsTestAccount(), request, "", "")
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.wantInput, result.Usage.InputTokens)
			require.Equal(t, tc.wantOutput, result.Usage.OutputTokens)
			require.Equal(t, tc.wantImageInput, result.Usage.ImageInputTokens)
			require.Equal(t, tc.wantImageOutput, result.Usage.ImageOutputTokens)
			require.Equal(t, tc.wantKiroCredits, result.Usage.KiroCredits)
			require.True(t, result.CaptureResponseComplete)
			require.Contains(t, rec.Body.String(), fmt.Sprintf(`"usage":{"input_tokens":%d,"output_tokens":%d`, tc.wantInput, tc.wantOutput))
			require.Contains(t, rec.Body.String(), "event: message_stop")
		})
	}
}

func TestForwardAsAnthropic_StreamingBareErrorBeforeOutputFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := strings.Join([]string{
		`event: error`,
		`data: {"type":"error","error":{"type":"server_error","code":"upstream_error","message":"temporary upstream failure"}}`,
		"",
		`data: [DONE]`,
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "pre-output retryable error must remain failover-safe: %T: %v", err, err)
	require.Empty(t, rec.Body.String(), "failover path must not commit downstream output")
}

func TestForwardAsAnthropic_StreamingGenericBareErrorBeforeOutputIsNotHiddenByFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := "event: error\n" +
		`data: {"type":"error","error":{"type":"server_error","code":"upstream_error","message":"mixed tools failed"}}` + "\n\n" +
		"data: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	_, err := svc.ForwardAsAnthropic(context.Background(), c, rawChatCompletionsTestAccount(), body, "", "")

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Contains(t, rec.Body.String(), "mixed tools failed")
}

func TestForwardAsAnthropic_BufferedResponseFailed_Failover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	ssePayload := buildResponsesFailedSSEStream("rate_limit_error", "Rate limit reached")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(ssePayload)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	account := rawChatCompletionsTestAccount()
	_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "rate_limit_error should trigger UpstreamFailoverError for failover, got: %T: %v", err, err)
}
