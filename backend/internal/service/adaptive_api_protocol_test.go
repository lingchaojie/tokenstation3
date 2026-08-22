//go:build unit

package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func adaptiveProtocolTestAccount(platform string, baseURLs map[string]any) *Account {
	return &Account{
		ID:          701,
		Name:        "adaptive-cn",
		Platform:    platform,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":       "sk-test",
			"api_protocol":  APIProtocolAdaptive,
			"account_mode":  AccountModePayG,
			"api_base_urls": baseURLs,
		},
	}
}

func adaptiveProtocolTestContext(path string, body []byte) *gin.Context {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

type cnProtocolIngressCase struct {
	name    string
	path    string
	body    []byte
	forward func(*OpenAIGatewayService, *gin.Context, *Account, []byte) error
}

func cnProtocolIngressCases() []cnProtocolIngressCase {
	return []cnProtocolIngressCase{
		{
			name: "chat completions",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"deepseek-chat","messages":[{"role":"user","content":"hello"}],"stream":false}`),
			forward: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) error {
				_, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
				return err
			},
		},
		{
			name: "messages",
			path: "/v1/messages",
			body: []byte(`{"model":"deepseek-chat","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`),
			forward: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) error {
				_, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")
				return err
			},
		},
		{
			name: "responses",
			path: "/v1/responses",
			body: []byte(`{"model":"deepseek-chat","input":"hello","stream":false}`),
			forward: func(svc *OpenAIGatewayService, c *gin.Context, account *Account, body []byte) error {
				_, err := svc.Forward(context.Background(), c, account, body)
				return err
			},
		},
	}
}

func TestAdaptiveProtocolRoutesChatCompletionsToNativeChat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"glm-4.7","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformZhipu, map[string]any{
		APIProtocolChatCompletions: "http://chat.example",
		APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.ForwardAsChatCompletions(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.NotNil(t, upstream.lastReq, err)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

func TestAdaptiveProtocolRoutesResponsesShapedChatToNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4","input":"hello","max_output_tokens":32,"stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformDeepseek, map[string]any{
		APIProtocolChatCompletions: "http://chat.example",
		APIProtocolAnthropic:       "http://anthropic.example",
		APIProtocolResponses:       "http://responses.example",
	})

	_, err := svc.ForwardAsChatCompletions(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
	require.False(t, gjson.GetBytes(upstream.lastBody, "messages").Exists())
}

func TestAdaptiveProtocolConvertsResponsesShapedChatForChatOnlyProvider(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kimi-k2.5","input":"hello","max_output_tokens":32,"stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformKimi, map[string]any{
		APIProtocolChatCompletions: "http://chat.example",
		APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.ForwardAsChatCompletions(context.Background(), adaptiveProtocolTestContext("/v1/chat/completions", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

func TestAdaptiveProtocolRoutesMessagesToNativeAnthropic(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"glm-4.7","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformZhipu, map[string]any{
		APIProtocolChatCompletions: "http://chat.example",
		APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.ForwardAsAnthropic(context.Background(), adaptiveProtocolTestContext("/v1/messages", body), account, body, "", "")
	require.Error(t, err)
	require.Equal(t, "http://anthropic.example/v1/messages", upstream.lastReq.URL.String())
	require.Equal(t, "glm-4.7", gjson.GetBytes(upstream.lastBody, "model").String())
}

func TestNativeAnthropicMessagesVerifiedFullBodyIsComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"glm-4.7","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
	providerBody := `{"id":"msg_complete","type":"message","role":"assistant","model":"glm-4.7","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":5,"output_tokens":2}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"application/json"}, "x-request-id": {"native-full-body"}},
		Body:       io.NopCloser(strings.NewReader(providerBody)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformZhipu, nil)
	account.Credentials["api_protocol"] = APIProtocolAnthropic
	account.Credentials["base_url"] = "http://anthropic.example"
	c := adaptiveProtocolTestContext("/v1/messages", body)

	result, err := svc.ForwardAsAnthropic(context.Background(), c, account, body, "", "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.True(t, result.CaptureResponseComplete, "successful full-body JSON read proves non-stream completion")
}

func TestAdaptiveProtocolConvertsKimiResponsesToChatCompletions(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"kimi-k2.5","input":"hello","stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformKimi, map[string]any{
		APIProtocolChatCompletions: "http://chat.example",
		APIProtocolAnthropic:       "http://anthropic.example",
	})

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
	require.Error(t, err)
	require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
	require.True(t, gjson.GetBytes(upstream.lastBody, "messages").IsArray())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input").Exists())
}

func TestAdaptiveProtocolRoutesDeepSeekResponsesToNativeResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4","input":"hello","max_output_tokens":32,"store":true,"previous_response_id":"resp_old","stream":false}`)
	upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := adaptiveProtocolTestAccount(PlatformDeepseek, map[string]any{
		APIProtocolChatCompletions: "http://chat.example",
		APIProtocolAnthropic:       "http://anthropic.example",
		APIProtocolResponses:       "http://responses.example",
	})

	_, err := svc.Forward(context.Background(), adaptiveProtocolTestContext("/v1/responses", body), account, body)
	require.Error(t, err)
	require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "store").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.Equal(t, int64(32), gjson.GetBytes(upstream.lastBody, "max_output_tokens").Int())
	require.False(t, gjson.GetBytes(upstream.lastBody, "instructions").Exists())
}

func TestFixedCNChatProtocolOverridesStaleResponsesMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range cnProtocolIngressCases() {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			account := adaptiveProtocolTestAccount(PlatformDeepseek, nil)
			account.Credentials["api_protocol"] = APIProtocolChatCompletions
			account.Credentials["base_url"] = "http://chat.example"
			account.Extra = map[string]any{
				openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceResponses),
			}

			err := tc.forward(svc, adaptiveProtocolTestContext(tc.path, tc.body), account, tc.body)

			require.Error(t, err)
			require.Equal(t, "http://chat.example/v1/chat/completions", upstream.lastReq.URL.String())
		})
	}
}

func TestFixedCNResponsesProtocolOverridesStaleChatMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range cnProtocolIngressCases() {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{err: errors.New("stop after capture")}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			account := adaptiveProtocolTestAccount(PlatformDeepseek, nil)
			account.Credentials["api_protocol"] = APIProtocolResponses
			account.Credentials["base_url"] = "http://responses.example"
			account.Extra = map[string]any{
				openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
			}

			err := tc.forward(svc, adaptiveProtocolTestContext(tc.path, tc.body), account, tc.body)

			require.Error(t, err)
			require.Equal(t, "http://responses.example/responses", upstream.lastReq.URL.String())
		})
	}
}
