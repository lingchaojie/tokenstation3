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
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type rawChatBlockingAfterPrefixReadCloser struct {
	reader *bytes.Reader
	closed chan struct{}
	once   sync.Once
}

type rawChatDelayedFiniteTailReadCloser struct {
	first     []byte
	tail      []byte
	read      int
	closed    chan struct{}
	closeOnce sync.Once
}

func (r *rawChatDelayedFiniteTailReadCloser) Read(p []byte) (int, error) {
	switch r.read {
	case 0:
		r.read++
		return copy(p, r.first), nil
	case 1:
		r.read++
		timer := time.NewTimer(75 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return copy(p, r.tail), nil
		case <-r.closed:
			return 0, io.EOF
		}
	default:
		return 0, io.EOF
	}
}

func (r *rawChatDelayedFiniteTailReadCloser) Close() error {
	r.closeOnce.Do(func() { close(r.closed) })
	return nil
}

func newRawChatBlockingAfterPrefixReadCloser(prefix string) *rawChatBlockingAfterPrefixReadCloser {
	return &rawChatBlockingAfterPrefixReadCloser{reader: bytes.NewReader([]byte(prefix)), closed: make(chan struct{})}
}

func (r *rawChatBlockingAfterPrefixReadCloser) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	<-r.closed
	return 0, io.EOF
}

func (r *rawChatBlockingAfterPrefixReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func TestBuildOpenAIChatCompletionsURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		// 已是 /chat/completions：原样返回
		{"already chat/completions", "https://api.openai.com/v1/chat/completions", "https://api.openai.com/v1/chat/completions"},
		// 以 /v1 结尾：追加 /chat/completions
		{"bare /v1", "https://api.openai.com/v1", "https://api.openai.com/v1/chat/completions"},
		// 其他情况：追加 /v1/chat/completions
		{"bare domain", "https://api.openai.com", "https://api.openai.com/v1/chat/completions"},
		{"domain with trailing slash", "https://api.openai.com/", "https://api.openai.com/v1/chat/completions"},
		// 第三方上游常见形式
		{"third-party bare domain", "https://api.deepseek.com", "https://api.deepseek.com/v1/chat/completions"},
		{"third-party with path prefix", "https://api.gptgod.online/api", "https://api.gptgod.online/api/v1/chat/completions"},
		{"third-party versioned path", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		// 带空白字符
		{"whitespace trimmed", "  https://api.openai.com/v1  ", "https://api.openai.com/v1/chat/completions"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildOpenAIChatCompletionsURL(tt.base)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestRawChatMalformedFrameDrainsFiniteProviderTailBeforeCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1/chat/completions", nil)
	setCaptureUpstreamRequest(c, request, 1<<20)
	first := []byte("data: {bad}\n\n")
	tail := []byte("data: {\"choices\":[]}\n\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       &rawChatDelayedFiniteTailReadCloser{first: first, tail: tail, closed: make(chan struct{})},
		Request:    request,
	}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)
	svc := &OpenAIGatewayService{cfg: &config.Config{}}

	result, err := svc.streamRawChatCompletions(
		context.Background(), c, resp,
		&Account{ID: 1, Name: "raw", Platform: PlatformOpenAI},
		"gpt-5.6-sol", "gpt-5.6-sol", "gpt-5.6-sol", nil, nil, time.Now(), 0,
	)
	finishCapture()

	require.Error(t, err)
	require.Nil(t, result)
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Equal(t, append(append([]byte(nil), first...), tail...), capture.Response)
	require.False(t, capture.ResponseTruncated)
}

// TestBuildOpenAIResponsesURL_ProbeURL 锁定 probe/测试端点使用的 URL 构建逻辑，
// 确保 buildOpenAIResponsesURL 对标准 OpenAI base_url 格式均拼出 `/v1/responses`。
func TestBuildOpenAIResponsesURL_ProbeURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		base string
		want string
	}{
		{"bare domain", "https://api.openai.com", "https://api.openai.com/v1/responses"},
		{"domain trailing slash", "https://api.openai.com/", "https://api.openai.com/v1/responses"},
		{"bare /v1", "https://api.openai.com/v1", "https://api.openai.com/v1/responses"},
		{"already /responses", "https://api.openai.com/v1/responses", "https://api.openai.com/v1/responses"},
		{"third-party bare domain", "https://api.deepseek.com", "https://api.deepseek.com/v1/responses"},
		{"third-party versioned path", "https://open.bigmodel.cn/api/paas/v4", "https://open.bigmodel.cn/api/paas/v4/responses"},
		{"only domain, no scheme", "api.gptgod.online", "api.gptgod.online/v1/responses"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildOpenAIResponsesURL(tt.base)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestForwardAsRawChatCompletions_ForcesStreamUsageUpstreamAndPassesUsageDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":9,"completion_tokens":4,"total_tokens":13,"prompt_tokens_details":{"cached_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_raw_usage"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.NotNil(t, upstream.lastReq)
	require.NoError(t, upstream.lastReq.Context().Err())
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(upstream.lastReq.Context()))
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Contains(t, rec.Body.String(), `"usage"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestExtractCCStreamUsagePrefersExplicitCCFields(t *testing.T) {
	t.Parallel()

	payload := `{"choices":[],"usage":{"prompt_tokens":0,"input_tokens":12,"completion_tokens":0,"output_tokens":3,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":6},"completion_tokens_details":{"image_tokens":0},"output_tokens_details":{"image_tokens":5},"_sub2api_kiro_credits":0.17}}`

	usage := OpenAIUsage{}
	require.True(t, mergeCCStreamUsage(&usage, payload))
	require.Zero(t, usage.InputTokens)
	require.Zero(t, usage.OutputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
	require.Zero(t, usage.CacheCreationInputTokens)
	require.Zero(t, usage.ImageOutputTokens)
	require.InDelta(t, 0.17, usage.KiroCredits, 0.000001)

	providerPayload := `{"choices":[],"usage":{"prompt_tokens":0,"input_tokens":12,"completion_tokens":0,"output_tokens":3,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":6},"completion_tokens_details":{"image_tokens":0},"output_tokens_details":{"image_tokens":5},"_sub2api_kiro_credits":0.17}}`
	provider, err := classifyOpenAIChatStreamPayload(providerPayload)
	require.NoError(t, err)
	require.False(t, provider, "usage-only chunks are valid non-semantic provider preambles")
}

func TestOpenAIChatRejectsDuplicateKnownJSONKeys(t *testing.T) {
	for name, payload := range map[string]string{
		"root choices":  `{"choices":[{"index":0,"delta":{"content":"safe"}}],"choices":[]}`,
		"finish reason": `{"choices":[{"index":0,"delta":{},"finish_reason":"stop","finish_reason":null}]}`,
		"root usage":    `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0},"usage":{"prompt_tokens":999,"completion_tokens":0}}`,
		"usage counter": `{"choices":[],"usage":{"prompt_tokens":1,"prompt_tokens":999,"completion_tokens":0}}`,
		"filter offset": `{"choices":[{"index":0,"delta":{"content":"ok"},"content_filter_offsets":{"check_offset":0,"check_offset":-1}}]}`,
		"audio data":    `{"choices":[{"index":0,"delta":{"audio":{"id":"a","data":"safe","data":"","transcript":"t","expires_at":1}}}]}`,
		"reasoning":     `{"choices":[{"index":0,"delta":{"reasoning":"safe","reasoning":""}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := classifyOpenAIChatStreamPayload(payload)
			require.ErrorContains(t, err, "repeated known field")
		})
	}

	provider, err := classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"delta":{"content":"ok"}}],"opaque":{"future":1,"future":2}}`)
	require.NoError(t, err)
	require.True(t, provider, "unknown extension duplicates remain forward-compatible")
}

func TestOpenAIChatRejectsWrongTypedKnownMetadataPromptSiblingsAndErrors(t *testing.T) {
	for name, payload := range map[string]string{
		"id":                 `{"id":123,"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"object":             `{"object":false,"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"created":            `{"created":"bad","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"model":              `{"model":[],"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"system fingerprint": `{"system_fingerprint":{},"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"service tier":       `{"service_tier":[],"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"logprobs":           `{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop","logprobs":"bad"}]}`,
		"prompt annotations": `{"prompt_annotations":{"prompt_index":0},"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"prompt index":       `{"prompt_filter_results":[{"prompt_index":-1}],"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
		"provider error":     `{"error":{"message":"failed"},"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := classifyOpenAIChatStreamPayload(payload)
			require.Error(t, err)
		})
	}
}

func TestOpenAIChatChoiceStateRejectsChangedStreamIdentity(t *testing.T) {
	var state openAIChatChoiceStreamState
	_, err := state.observe(`{"id":"chat-a","object":"chat.completion.chunk","model":"model-a","created":1,"choices":[]}`)
	require.NoError(t, err)
	_, err = state.observe(`{"id":"chat-b","object":"chat.completion.chunk","model":"model-b","created":2,"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
	require.ErrorContains(t, err, "identity changed")
}

func TestExtractCCStreamUsageFallsBackToResponsesFields(t *testing.T) {
	t.Parallel()

	payload := `{"choices":[],"usage":{"input_tokens":12,"output_tokens":3,"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":6},"output_tokens_details":{"image_tokens":5}}}`

	usage := OpenAIUsage{}
	require.True(t, mergeCCStreamUsage(&usage, payload))
	require.Equal(t, 12, usage.InputTokens)
	require.Equal(t, 3, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 6, usage.CacheCreationInputTokens)
	require.Equal(t, 5, usage.ImageOutputTokens)
}

func TestExtractCCStreamUsageSkipsMalformedCanonicalFields(t *testing.T) {
	t.Parallel()

	payloads := []string{
		`{"usage":{"prompt_tokens":null,"input_tokens":12}}`,
		`{"usage":{"prompt_tokens":false,"input_tokens":12}}`,
		`{"usage":{"prompt_tokens":true,"input_tokens":12}}`,
		`{"usage":{"prompt_tokens":"invalid","input_tokens":12}}`,
		`{"usage":{"prompt_tokens":-1,"input_tokens":12}}`,
		`{"usage":{"prompt_tokens":1.5,"input_tokens":12}}`,
	}

	for _, payload := range payloads {
		usage := OpenAIUsage{}
		require.True(t, mergeCCStreamUsage(&usage, payload), payload)
		require.Equal(t, 12, usage.InputTokens, payload)
		providerPayload := `{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":` + gjson.Get(payload, "usage").Raw + `}`
		provider, err := classifyOpenAIChatStreamPayload(providerPayload)
		require.NoError(t, err, payload)
		require.True(t, provider, payload)
	}
	require.False(t, mergeCCStreamUsage(&OpenAIUsage{}, `{"usage":{}}`))
}

func TestMergeCCStreamUsagePreservesValidFieldsAcrossPartialAndMalformedChunks(t *testing.T) {
	t.Parallel()

	usage := OpenAIUsage{}
	require.True(t, mergeCCStreamUsage(&usage, `{"usage":{"prompt_tokens":12,"completion_tokens":3,"cache_read_input_tokens":4,"_sub2api_kiro_credits":0.17}}`))
	require.False(t, mergeCCStreamUsage(&usage, `{"usage":{}}`))
	require.False(t, mergeCCStreamUsage(&usage, `{"usage":{"prompt_tokens":null,"completion_tokens":false}}`))
	require.True(t, mergeCCStreamUsage(&usage, `{"usage":{"completion_tokens":5,"cache_creation_input_tokens":6,"_sub2api_kiro_credits":0}}`))

	require.Equal(t, 12, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 6, usage.CacheCreationInputTokens)
	require.Zero(t, usage.KiroCredits)
}

func TestForwardAsRawChatCompletions_PreservesMappedGPT56MaxEffort(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"sol","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_max","object":"chat.completion","model":"gpt-5.6-sol","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`,
		)),
	}}
	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()
	account.Credentials["model_mapping"] = map[string]any{"sol": "gpt-5.6-sol"}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gpt-5.6-sol", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
}

func TestForwardAsRawChatCompletions_NonStreamingCapturesCacheWriteUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		usageJSON  string
		wantInput  int
		wantOutput int
		wantRead   int
		wantWrite  int
	}{
		{
			name:       "positive cache write",
			usageJSON:  `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":6}}`,
			wantInput:  12,
			wantOutput: 3,
			wantRead:   4,
			wantWrite:  6,
		},
		{
			name:       "nested zero overrides legacy alias",
			usageJSON:  `{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15,"cache_creation_input_tokens":19,"prompt_tokens_details":{"cached_tokens":4,"cache_write_tokens":0}}`,
			wantInput:  12,
			wantOutput: 3,
			wantRead:   4,
			wantWrite:  0,
		},
		{
			name:       "explicit CC zeros override Responses dialect",
			usageJSON:  `{"prompt_tokens":0,"input_tokens":12,"completion_tokens":0,"output_tokens":3,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":0},"input_tokens_details":{"cached_tokens":4,"cache_write_tokens":6}}`,
			wantInput:  0,
			wantOutput: 0,
			wantRead:   0,
			wantWrite:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.6","messages":[{"role":"user","content":"hello"}],"stream":false}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(
					`{"id":"chatcmpl_cache","object":"chat.completion","model":"gpt-5.6","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":` + tt.usageJSON + `}`,
				)),
			}}
			svc := &OpenAIGatewayService{
				cfg:          rawChatCompletionsTestConfig(),
				httpUpstream: upstream,
			}

			result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")

			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tt.wantInput, result.Usage.InputTokens)
			require.Equal(t, tt.wantOutput, result.Usage.OutputTokens)
			require.Equal(t, tt.wantRead, result.Usage.CacheReadInputTokens)
			require.Equal(t, tt.wantWrite, result.Usage.CacheCreationInputTokens)
		})
	}
}

func TestForwardAsRawChatCompletions_PreservesDeepSeekReasoningContentNonStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamJSON := `{"id":"chatcmpl_reasoning","object":"chat.completion","model":"deepseek-reasoner","choices":[{"index":0,"message":{"role":"assistant","reasoning_content":"think first","content":"final answer"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_deepseek_reasoning_json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamJSON)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Equal(t, "think first", gjson.Get(rec.Body.String(), "choices.0.message.reasoning_content").String())
	require.Equal(t, "final answer", gjson.Get(rec.Body.String(), "choices.0.message.content").String())
}

func TestForwardAsRawChatCompletions_PreservesDeepSeekReasoningContentStreaming(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-reasoner","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"reasoning_content":"think first"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{"content":"final answer"},"finish_reason":null}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_reasoning","object":"chat.completion.chunk","model":"deepseek-reasoner","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_deepseek_reasoning_stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.OutputTokens)
	require.Contains(t, rec.Body.String(), `"reasoning_content":"think first"`)
	require.Contains(t, rec.Body.String(), `"content":"final answer"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_PreservesDeepSeekReasoningContentInRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"weather"},{"role":"assistant","reasoning_content":"need tool","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]},{"role":"tool","tool_call_id":"call_1","content":"cloudy"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_deepseek_reasoning_request"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_request","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"done"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":2,"total_tokens":6}}`)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "need tool", gjson.GetBytes(upstream.lastBody, "messages.1.reasoning_content").String())
	require.Equal(t, "get_weather", gjson.GetBytes(upstream.lastBody, "messages.1.tool_calls.0.function.name").String())
}

func TestForwardAsRawChatCompletions_NormalizesGLMReasoningEffortForUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"xhigh","stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_glm_effort"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"chatcmpl_glm","object":"chat.completion","model":"glm-5.2","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
}

func TestForwardAsRawChatCompletions_SilentRefusalTriggersFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_silent","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"chatcmpl_silent","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_silent"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr), "unexpected error: %T %v", err, err)
	require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
	require.True(t, IsOpenAISilentRefusalErrorBody(failoverErr.ResponseBody), "unexpected failover body: %s", failoverErr.ResponseBody)
	require.False(t, c.Writer.Written(), "silent refusal must not commit a 200 response before failover")
	require.Empty(t, rec.Body.String())
}

func TestForwardAsRawChatCompletions_EmptySemanticFieldsRemainReplaySafe(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := `data: {"id":"chatcmpl_empty","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"","tool_calls":[{"function":{"name":"","arguments":""}}],"function_call":{"name":"","arguments":""},"audio":{}}}]}` + "\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_empty_semantic"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestForwardAsRawChatCompletions_SilentRefusalToolCallsExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":""}}]}}]}`,
		"",
		`data: {"id":"chatcmpl_tool","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_tool"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"tool_calls"`)
	require.Contains(t, rec.Body.String(), `"finish_reason":"tool_calls"`)
}

func TestHandleChatStreamingResponse_SilentRefusalReasoningSummaryExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	upstreamBody := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_reasoning","model":"gpt-5.5"}}`,
		"",
		`data: {"type":"response.reasoning_summary_text.delta","delta":"thinking only"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_reasoning","model":"gpt-5.5","status":"completed","output":null}}`,
		"",
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_reasoning"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}

	result, err := svc.handleChatStreamingResponse(
		context.Background(),
		resp,
		c,
		rawChatCompletionsTestAccount(),
		"gpt-5.5",
		"gpt-5.5",
		"gpt-5.5",
		time.Now(),
		openAISilentRefusalMinRequestBodyBytes,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"reasoning_content":"thinking only"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_SilentRefusalNormalContentExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := largeRawChatCompletionsBody()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_ok","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"chatcmpl_ok","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_ok","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":""},"finish_reason":"stop"}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_ok"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"content":"ok"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_ClientDisconnectDrainsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Writer = &openAIChatFailingWriter{ResponseWriter: c.Writer, failAfter: 0}
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		"",
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[],"usage":{"prompt_tokens":17,"completion_tokens":8,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":6}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_raw_disconnect"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(context.Background(), c, account, body, "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)
	require.Equal(t, 6, result.Usage.CacheReadInputTokens)
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
}

func TestForwardAsRawChatCompletions_HonorsProviderIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)

	requestBody := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	providerBody := newRawChatBlockingAfterPrefixReadCloser(
		`data: {"id":"chatcmpl_idle","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n",
	)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       providerBody,
	}}
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.StreamDataIntervalTimeout = 1
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

	type outcome struct {
		result *OpenAIForwardResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.forwardAsRawChatCompletions(context.Background(), c, rawChatCompletionsTestAccount(), requestBody, "")
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		require.NotNil(t, got.result)
		require.ErrorContains(t, got.err, "stream data interval timeout")
	case <-time.After(2 * time.Second):
		_ = providerBody.Close()
		<-done
		t.Fatal("raw Chat Completions provider stream ignored StreamDataIntervalTimeout")
	}
	require.NoError(t, providerBody.Close())
}

func TestForwardAsRawChatCompletionsTreatsPartialLineBytesAsProviderActivity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	requestBody := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	providerBody := &providerSlowChunksReader{
		chunks: [][]byte{
			[]byte(`data: {"id":"chatcmpl_slow","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,`),
			[]byte(`"delta":{"content":"slow-ok"},"finish_reason":"stop"}]}`),
			[]byte("\n\ndata: [DONE]\n\n"),
		},
		interval: 400 * time.Millisecond,
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       providerBody,
	}}
	cfg := rawChatCompletionsTestConfig()
	cfg.Gateway.StreamDataIntervalTimeout = 1
	svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}

	result, err := svc.forwardAsRawChatCompletions(
		context.Background(), c, rawChatCompletionsTestAccount(), requestBody, "",
	)

	require.NoError(t, err, "provider bytes arriving inside the idle interval must keep a partial SSE line alive")
	require.NotNil(t, result)
	require.Contains(t, rec.Body.String(), `"content":"slow-ok"`)
	require.Contains(t, rec.Body.String(), "data: [DONE]")
}

func TestForwardAsRawChatCompletions_UpstreamRequestPropagatesClientCancel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	reqCtx, cancel := context.WithCancel(context.Background())
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body)).WithContext(reqCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	cancel()

	upstreamBody := strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.4","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"rid_raw_ctx"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()

	result, err := svc.forwardAsRawChatCompletions(reqCtx, c, account, body, "")
	require.NoError(t, err, "the recorder returns a synthetic response even for a canceled request")
	require.NotNil(t, result)
	require.NotNil(t, upstream.lastReq)
	require.ErrorIs(t, upstream.lastReq.Context().Err(), context.Canceled)
}

func TestForwardAsChatCompletions_UnknownResponsesSupportFallbackUsesVersionedChatURL(t *testing.T) {
	gin.SetMode(gin.TestMode)

	body := []byte(`{"model":"glm-4.5-air","messages":[{"role":"user","content":"hello"}],"stream":false}`)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusNotFound,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"not found"}}`)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "x-request-id": []string{"rid_raw_fallback"}},
			Body: io.NopCloser(strings.NewReader(
				`{"id":"chatcmpl_1","object":"chat.completion","model":"glm-4.5-air","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`,
			)),
		},
	}}

	svc := &OpenAIGatewayService{
		cfg:          rawChatCompletionsTestConfig(),
		httpUpstream: upstream,
	}
	account := rawChatCompletionsTestAccount()
	account.Credentials["base_url"] = "https://open.bigmodel.cn/api/paas/v4"

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Len(t, upstream.requests, 2)
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4/responses", upstream.requests[0].URL.String())
	require.Equal(t, "https://open.bigmodel.cn/api/paas/v4/chat/completions", upstream.requests[1].URL.String())
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"content":"ok"`)
}

func TestIsOpenAIChatUsageOnlyStreamChunk(t *testing.T) {
	t.Parallel()

	require.True(t, isOpenAIChatUsageOnlyStreamChunk(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(`{"choices":[{"index":0}],"usage":{"prompt_tokens":1,"completion_tokens":2}}`))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(`{"choices":[]}`))
	require.False(t, isOpenAIChatUsageOnlyStreamChunk(``))
}

func TestOpenAIChatPayloadIsValidProviderPayloadRejectsEmptyChoicesWithoutUsage(t *testing.T) {
	t.Parallel()
	require.False(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[]}`))
	require.False(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0}}`))
	require.False(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[{}]}`))
	require.False(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`))
	require.True(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`))
	require.False(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[{"index":0,"text":123}]}`))
	require.False(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[{"index":0,"message":{"content":"ok","tool_calls":123}}]}`))
	require.True(t, openAIChatPayloadIsValidProviderPayload(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`))
}

func TestClassifyOpenAIChatStreamPayloadKeepsPreambleAndRejectsBadChoices(t *testing.T) {
	t.Parallel()

	provider, err := classifyOpenAIChatStreamPayload(`{"choices":[{}]}`)
	require.Error(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"delta":{"role":"assistant"}}]}`)
	require.NoError(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
	require.NoError(t, err)
	require.True(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false}},"content_filter_offsets":{"check_offset":0,"start_offset":0,"end_offset":0},"finish_reason":null}]}`)
	require.NoError(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"delta":{},"content_filter_results":{"hate":{"filtered":false}},"content_filter_offsets":{"check_offset":0,"start_offset":0,"end_offset":0},"finish_reason":null}]}`)
	require.NoError(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"content_filter_results":[],"finish_reason":null}]}`)
	require.Error(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"prompt_filter_results":[{"prompt_index":0,"content_filter_results":{"hate":{"filtered":false}}}],"choices":[],"usage":null}`)
	require.NoError(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"prompt_annotations":[{"prompt_index":-1,"content_filter_results":{}}],"choices":[],"usage":null}`)
	require.Error(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"delta":{"content":"ok"}}]}`)
	require.NoError(t, err)
	require.True(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"delta":{"role":"assistant"},"text":123,"finish_reason":"stop"}]}`)
	require.Error(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":-1,"delta":{"content":"ok"}}]}`)
	require.Error(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"message":{"role":"user","content":"echo"},"finish_reason":"stop"}]}`)
	require.Error(t, err)
	require.False(t, provider)

	provider, err = classifyOpenAIChatStreamPayload(`{"choices":[{"index":0,"message":{"role":"assistant","tool_calls":[{"function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	require.Error(t, err)
	require.False(t, provider)
}

func TestOpenAIChatChoiceStreamStateRequiresCompleteToolCallAtFinish(t *testing.T) {
	var state openAIChatChoiceStreamState
	complete, err := state.observe(`{"choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"function":{"name":"lookup","arguments":""}}]},"finish_reason":null}]}`)
	require.NoError(t, err)
	require.False(t, complete)

	complete, err = state.observe(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`)
	require.Error(t, err)
	require.False(t, complete)
}

func TestOpenAIChatChoiceStreamStateRequiresCompleteLegacyFunctionCallAtFinish(t *testing.T) {
	var state openAIChatChoiceStreamState
	complete, err := state.observe(`{"choices":[{"index":0,"delta":{"role":"assistant","function_call":{"arguments":""}},"finish_reason":null}]}`)
	require.NoError(t, err)
	require.False(t, complete)

	complete, err = state.observe(`{"choices":[{"index":0,"delta":{},"finish_reason":"function_call"}]}`)
	require.Error(t, err)
	require.False(t, complete)
}

func TestOpenAIChatChoiceStreamStateRequiresFinishReasonToMatchCalls(t *testing.T) {
	t.Run("tool_calls without call", func(t *testing.T) {
		var state openAIChatChoiceStreamState
		_, err := state.observe(`{"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":"tool_calls"}]}`)
		require.Error(t, err)
	})

	t.Run("tool call with stop", func(t *testing.T) {
		var state openAIChatChoiceStreamState
		_, err := state.observe(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"stop"}]}`)
		require.Error(t, err)
	})
}

func TestOpenAIChatPayloadDenseChoicesStayWithinBoundedAllocation(t *testing.T) {
	const targetBytes = 8 << 20
	const choice = `{"delta":{"role":"assistant"}},`
	choiceCount := (targetBytes - len(`{"choices":[]}`)) / len(choice)
	payload := `{"choices":[` + strings.Repeat(choice, choiceCount) + `{"delta":{"role":"assistant"}}]}`
	require.GreaterOrEqual(t, len(payload), targetBytes-(2*len(choice)))

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	provider, err := classifyOpenAIChatStreamPayload(payload)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.ErrorContains(t, err, "too many choices")
	require.False(t, provider)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
}

func TestOpenAIChatSilentRefusalDetectorDenseChoicesStayWithinBoundedAllocation(t *testing.T) {
	const targetBytes = 8 << 20
	const choice = `{"delta":{"role":"assistant"}},`
	choiceCount := (targetBytes - len(`{"choices":[]}`)) / len(choice)
	payload := []byte(`{"choices":[` + strings.Repeat(choice, choiceCount) + `{"delta":{"role":"assistant"}}]}`)
	require.GreaterOrEqual(t, len(payload), targetBytes-(2*len(choice)))
	detector := newOpenAIChatSilentRefusalDetector(openAISilentRefusalMinRequestBodyBytes)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	detector.ObservePayload(payload)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
}

func TestOpenAIChatChoiceStreamStateBoundsTrackedChoicesAndTools(t *testing.T) {
	t.Run("choices", func(t *testing.T) {
		var state openAIChatChoiceStreamState
		for index := 0; index <= 1024; index++ {
			_, err := state.observe(fmt.Sprintf(`{"choices":[{"index":%d,"delta":{"role":"assistant"},"finish_reason":null}]}`, index))
			if index < 1024 {
				require.NoError(t, err)
				continue
			}
			require.ErrorContains(t, err, "too many choices")
		}
	})

	t.Run("tool calls", func(t *testing.T) {
		var state openAIChatChoiceStreamState
		for index := 0; index <= 1024; index++ {
			_, err := state.observe(fmt.Sprintf(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":%d,"id":"call-%d","type":"function","function":{"name":"lookup","arguments":""}}]},"finish_reason":null}]}`, index, index))
			if index < 1024 {
				require.NoError(t, err)
				continue
			}
			require.ErrorContains(t, err, "too many tool calls")
		}
	})
}

func TestOpenAIChatRetainedToolMetadataIsBounded(t *testing.T) {
	oversized := strings.Repeat("x", maxOpenAIChatRetainedIdentifierBytes+1)
	for name, payload := range map[string]string{
		"tool id":              `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"` + oversized + `","type":"function","function":{}}]}}]}`,
		"tool function name":   `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"` + oversized + `"}}]}}]}`,
		"legacy function name": `{"choices":[{"index":0,"delta":{"function_call":{"name":"` + oversized + `"}}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			provider, err := classifyOpenAIChatStreamPayload(payload)
			require.ErrorContains(t, err, "too long")
			require.False(t, provider)
		})
	}

	var state openAIChatChoiceStreamState
	_, err := state.observe(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"` + oversized + `","type":"function","function":{}}]}}]}`)
	require.ErrorContains(t, err, "too long")

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	state = openAIChatChoiceStreamState{}
	identifierPadding := strings.Repeat("x", maxOpenAIChatRetainedIdentifierBytes-16)
	for index := 0; index < maxOpenAIChatToolCallsPerChoice; index++ {
		indexText := fmt.Sprintf("%08d", index)
		_, err = state.observe(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":` + fmt.Sprint(index) + `,"id":"` + indexText + identifierPadding + `","type":"function","function":{}}]}}]}`)
		require.NoError(t, err)
	}
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(16<<20))
}

func TestOpenAIChatBufferedChoicesRequireFinishReasonToMatchCalls(t *testing.T) {
	require.False(t, openAIChatBufferedChoicesComplete([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"tool_calls"}]}`)))
	require.False(t, openAIChatBufferedChoicesComplete([]byte(`{"choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"stop"}]}`)))
}

func TestBufferRawChatCompletionsRejectsEmptyChoicesWithoutUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[]}`))}

	result, err := (&OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}).bufferRawChatCompletions(
		c, resp, "gpt-5.4", "gpt-5.4", "gpt-5.4", nil, nil, time.Now(),
	)
	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.True(t, failure.HasUpstreamHTTPResponse)
	require.Empty(t, rec.Body.Bytes(), "invalid provider JSON must stay uncommitted for account failover")
}

func TestEnsureOpenAIChatStreamUsage(t *testing.T) {
	t.Parallel()

	body, err := ensureOpenAIChatStreamUsage([]byte(`{"model":"gpt-5.4"}`))
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())

	body, err = ensureOpenAIChatStreamUsage([]byte(`{"model":"gpt-5.4","stream_options":{"include_usage":false}}`))
	require.NoError(t, err)
	require.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
}

func TestBufferRawChatCompletions_RejectsOversizedResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader("toolong")),
	}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
	svc.cfg.Gateway.UpstreamResponseReadMaxBytes = 3

	result, err := svc.bufferRawChatCompletions(c, resp, "gpt-5.4", "gpt-5.4", "gpt-5.4", nil, nil, time.Now())
	require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
	require.Nil(t, result)
	require.Equal(t, http.StatusOK, rec.Code, "precommit parser failures are returned for handler-level failover")
	require.False(t, rec.Result().Header.Get("Content-Type") != "")
}

func rawChatCompletionsTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
}

func rawChatCompletionsTestAccount() *Account {
	return &Account{
		ID:          101,
		Name:        "raw-openai-apikey",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": "http://upstream.example",
		},
	}
}

func largeRawChatCompletionsBody() []byte {
	return []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"` +
		strings.Repeat("x", openAISilentRefusalMinRequestBodyBytes) +
		`"}],"stream":true}`)
}
