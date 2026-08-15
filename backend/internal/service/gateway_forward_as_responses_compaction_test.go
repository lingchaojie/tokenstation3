//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func anthropicCompactionSSE(text string) string {
	encoded, _ := json.Marshal(text)
	return strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_cmp","type":"message","role":"assistant","content":[],"model":"claude-haiku-4-5","usage":{"input_tokens":120}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":` + string(encoded) + `}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":20}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")
}

func compactionHTTPResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"text/event-stream"},
			"x-request-id": []string{"rid_compaction"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func TestHandleResponsesCompactionResponse_StreamAndNonStream(t *testing.T) {
	for _, clientStream := range []bool{false, true} {
		t.Run(map[bool]string{false: "non-stream", true: "stream"}[clientStream], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}

			result, err := svc.handleResponsesCompactionResponse(
				compactionHTTPResponse(anthropicCompactionSSE("<summary>work state</summary>")),
				c, "gpt-5.6-sol", "claude-haiku-4-5", nil, time.Now(), clientStream, true,
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, clientStream, result.Stream)

			body := rec.Body.String()
			if clientStream {
				require.Contains(t, body, "event: response.output_item.done")
				require.Contains(t, body, "event: response.completed")
			}
			item := gjson.Get(body, "output.0")
			if clientStream {
				for _, line := range strings.Split(body, "\n") {
					if strings.HasPrefix(line, "data: ") && gjson.Get(strings.TrimPrefix(line, "data: "), "type").String() == "response.output_item.done" {
						item = gjson.Get(strings.TrimPrefix(line, "data: "), "item")
					}
				}
			}
			require.Equal(t, apicompat.CompactionItemType, item.Get("type").String())
			require.NotEmpty(t, item.Get("encrypted_content").String())
		})
	}
}

func TestHandleResponsesCompactionResponse_EmptyOrIncompleteFailsWithoutItem(t *testing.T) {
	for name, body := range map[string]string{
		"empty summary":    anthropicCompactionSSE("   "),
		"missing terminal": strings.ReplaceAll(anthropicCompactionSSE("summary"), "event: message_stop\ndata: {\"type\":\"message_stop\"}\n", ""),
	} {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			svc := &GatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}}
			_, err := svc.handleResponsesCompactionResponse(
				compactionHTTPResponse(body), c, "gpt-5.6-sol", "claude-haiku-4-5", nil, time.Now(), true, true,
			)
			require.Error(t, err)
			require.NotContains(t, rec.Body.String(), `"type":"compaction"`)
		})
	}
}

func TestAnthropicResponseTextIgnoresThinking(t *testing.T) {
	resp := &apicompat.AnthropicResponse{Content: []apicompat.AnthropicContentBlock{
		{Type: "thinking", Thinking: "private"},
		{Type: "text", Text: "first"},
		{Type: "text", Text: "second"},
	}}
	require.Equal(t, "first\nsecond", anthropicResponseText(resp))
}

func TestForwardAsResponses_CompactionRewritesKiroRelayRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{
		"model":"gpt-5.6-sol",
		"stream":false,
		"max_output_tokens":8192,
		"reasoning":{"effort":"high"},
		"tools":[{"type":"function","name":"exec","parameters":{"type":"object"}}],
		"tool_choice":"auto",
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"continue work"}]},
			{"type":"compaction_trigger"}
		]
	}`)
	upstream := &queuedHTTPUpstream{responses: []*http.Response{compactionHTTPResponse(anthropicCompactionSSE("<summary>state</summary>"))}}
	svc := &GatewayService{
		cfg:                 &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}},
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID: 701, Platform: PlatformKiro, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":               "sk-relay",
			"base_url":              "https://relay.example.com",
			"model_mapping":         map[string]any{"gpt-5.6-sol": "claude-sonnet-4-6"},
			"compact_model_mapping": map[string]any{"gpt-5.6-sol": "claude-haiku-4-5"},
		},
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	result, err := svc.ForwardAsResponses(c.Request.Context(), c, account, body, &ParsedRequest{})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, upstream.requests, 1)
	upstreamBody, err := io.ReadAll(upstream.requests[0].Body)
	require.NoError(t, err)

	require.Equal(t, "claude-haiku-4-5", gjson.GetBytes(upstreamBody, "model").String())
	require.Contains(t, string(upstreamBody), "produce a faithful, concise summary")
	require.Equal(t, "none", gjson.GetBytes(upstreamBody, "tool_choice.type").String())
	require.GreaterOrEqual(t, int(gjson.GetBytes(upstreamBody, "max_tokens").Int()), compactionMinMaxTokens)
	require.False(t, gjson.GetBytes(upstreamBody, "thinking").Exists())
	require.False(t, gjson.GetBytes(upstreamBody, "output_config").Exists())
	require.Len(t, gjson.GetBytes(upstreamBody, "tools").Array(), 1)
	require.Equal(t, "compaction", gjson.Get(rec.Body.String(), "output.0.type").String())
}
