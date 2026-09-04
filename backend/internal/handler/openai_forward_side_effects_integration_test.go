package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type task6OpenAIUpstream struct{ responseBody string }

func (u task6OpenAIUpstream) response(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-task6"}},
		Body:       io.NopCloser(strings.NewReader(u.responseBody)),
		Request:    req,
	}
}

func (u task6OpenAIUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.response(req), nil
}

func (u task6OpenAIUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.response(req), nil
}

func task6RealOpenAIForward(t *testing.T, responseBody string) (*service.OpenAIForwardResult, error) {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 64 * 1024
	settingService := newEnabledCaptureSettingService(t, cfg)
	svc := service.NewOpenAIGatewayService(
		nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, nil, nil, nil,
		task6OpenAIUpstream{responseBody: responseBody}, nil, nil, nil, nil, nil, nil, settingService, nil, nil,
	)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gpt-5.4","stream":true,"input":"hello"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	service.PrepareCapturePolicyScope(context.Background(), c, settingService, 9, nil)
	account := &service.Account{
		ID: 9, Name: "task6", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Concurrency: 1,
		Credentials: map[string]any{"access_token": "token", "chatgpt_account_id": "acct"},
		Extra:       map[string]any{"openai_passthrough": true}, Status: service.StatusActive, Schedulable: true,
	}
	return svc.Forward(context.Background(), c, account, body)
}

func TestOpenAIRealForwardFeedsAtMostOnceSideEffectSinkWithoutResultCaptureBuffer(t *testing.T) {
	tests := []struct {
		name, response string
		wantSink       int
	}{
		{name: "pre-output failed request", response: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\"}}\n\n", wantSink: 1},
		{name: "committed partial", response: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"x\"}\n\n", wantSink: 1},
		{name: "success", response: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\ndata: [DONE]\n\n", wantSink: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := task6RealOpenAIForward(t, tt.response)
			sinkCalls := 0
			sink := newOpenAIForwardSideEffectSubmitter(func(got *service.OpenAIForwardResult) {
				sinkCalls++
				require.Nil(t, got.CaptureResponse, "typed capture must not retain a whole response in the forward result")
			})
			sink.Submit(result)
			sink.Submit(result)
			require.Equal(t, tt.wantSink, sinkCalls)
		})
	}
}
