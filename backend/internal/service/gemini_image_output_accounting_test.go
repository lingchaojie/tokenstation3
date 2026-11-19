//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const geminiTestPNG = "iVBORw0KGgoAAAANSUhEUg=="

func newGeminiImageTestContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost,
		"/v1beta/models/nana-banana-2:generateContent", strings.NewReader("{}"))
	return c
}

func geminiImageResponse(parts string) string {
	return `{"candidates":[{"content":{"role":"model","parts":[` + parts + `]},"finishReason":"STOP"}]}`
}

func TestCountGeminiInlineImageOutputs(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    int
	}{
		{
			name:    "camelCase inlineData",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`),
			want:    1,
		},
		{
			// 官方 SDK 与部分中转把字段回成 snake_case。
			name:    "snake_case inline_data",
			payload: geminiImageResponse(`{"inline_data":{"mime_type":"image/png","data":"` + geminiTestPNG + `"}}`),
			want:    1,
		},
		{
			name: "text and image mixed",
			payload: geminiImageResponse(`{"text":"here you go"},` +
				`{"inlineData":{"mimeType":"image/jpeg","data":"` + geminiTestPNG + `"}}`),
			want: 1,
		},
		{
			name: "multiple images",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}},` +
				`{"inlineData":{"mimeType":"image/webp","data":"` + geminiTestPNG + `"}}`),
			want: 2,
		},
		{
			name:    "uppercase mime type",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"IMAGE/PNG","data":"` + geminiTestPNG + `"}}`),
			want:    1,
		},
		{
			name:    "text only",
			payload: geminiImageResponse(`{"text":"no image here"}`),
			want:    0,
		},
		{
			// 非图片的内联附件（例如音频）不能按图片计费。
			name:    "non image mime type",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"audio/mpeg","data":"` + geminiTestPNG + `"}}`),
			want:    0,
		},
		{
			name:    "empty data is not billable",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":""}}`),
			want:    0,
		},
		{
			name:    "camelCase fileData",
			payload: geminiImageResponse(`{"fileData":{"mimeType":"image/jpeg","fileUri":"https://example.test/image.jpg"}}`),
			want:    0,
		},
		{
			name:    "snake_case file_data",
			payload: geminiImageResponse(`{"file_data":{"mime_type":"image/png","file_uri":"gs://bucket/image.png"}}`),
			want:    0,
		},
		{
			name:    "mixed inline and referenced images",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}},{"fileData":{"mimeType":"image/webp","fileUri":"https://example.test/image.webp"}}`),
			want:    1,
		},
		{
			name:    "a part is counted once",
			payload: geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"},"fileData":{"mimeType":"image/png","fileUri":"https://example.test/image.png"}}`),
			want:    1,
		},
		{name: "empty payload", payload: "", want: 0},
		{name: "invalid json", payload: "not-json", want: 0},
		{name: "error response", payload: `{"error":{"code":429,"message":"quota"}}`, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, countGeminiInlineImageOutputs([]byte(tc.payload)))
		})
	}
}

// 累积式 SSE 会把同一张图在后续 chunk 里整段重发，逐 chunk 累加会重复计费。
// 计数器取单个 payload 内的最大值，正是为了挡住这一点。
func TestObserveGeminiImageOutputs_CumulativeChunksDoNotDoubleCount(t *testing.T) {
	c := newGeminiImageTestContext(t)
	beginGeminiImageOutputObservation(c)

	oneImage := geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`)
	for range 4 {
		observeGeminiImageOutputs(c, []byte(oneImage))
	}

	require.Equal(t, 1, observedGeminiImageOutputs(c))
}

func TestObserveGeminiImageOutputs_KeepsLargestChunk(t *testing.T) {
	c := newGeminiImageTestContext(t)
	beginGeminiImageOutputObservation(c)

	observeGeminiImageOutputs(c, []byte(geminiImageResponse(`{"text":"working"}`)))
	observeGeminiImageOutputs(c, []byte(geminiImageResponse(
		`{"inlineData":{"mimeType":"image/png","data":"`+geminiTestPNG+`"}},`+
			`{"inlineData":{"mimeType":"image/png","data":"`+geminiTestPNG+`"}}`)))
	// 收尾 chunk 只带 usageMetadata，不能把已数到的张数抹掉。
	observeGeminiImageOutputs(c, []byte(`{"usageMetadata":{"promptTokenCount":9}}`))

	require.Equal(t, 2, observedGeminiImageOutputs(c))
}

// failover 会拿同一个 gin.Context 重跑 Forward，计数器必须按次重置，
// 否则失败账号已经回吐的图会被叠加到成功账号的账单上。
func TestBeginGeminiImageOutputObservation_ResetsPerForward(t *testing.T) {
	c := newGeminiImageTestContext(t)
	oneImage := []byte(geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`))

	beginGeminiImageOutputObservation(c)
	observeGeminiImageOutputs(c, oneImage)
	require.Equal(t, 1, observedGeminiImageOutputs(c))

	beginGeminiImageOutputObservation(c)
	require.Equal(t, 0, observedGeminiImageOutputs(c))
	observeGeminiImageOutputs(c, oneImage)
	require.Equal(t, 1, observedGeminiImageOutputs(c))
}

// issue #5358：自定义模型名（客户端名与上游映射名都不在白名单里）走 Gemini 原生
// generateContent 生图，改动前 ImageCount 恒为 0，calculateRecordUsageCost 的按次
// 计费分支整条不触发，四次生图全部记 $0。
func TestResolveGeminiImageCount(t *testing.T) {
	oneImage := []byte(geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`))
	textOnly := []byte(geminiImageResponse(`{"text":"hello"}`))

	t.Run("custom model name bills by observed images", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		beginGeminiImageOutputObservation(c)
		observeGeminiImageOutputs(c, oneImage)

		require.False(t, isImageGenerationModel("nana-banana-2"), "前置条件：白名单判不出自定义名")
		require.Equal(t, 1, resolveGeminiImageCount(c, "nana-banana-2", "nana-banana-2"))
	})

	t.Run("falls back to requested model name", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		beginGeminiImageOutputObservation(c)
		observeGeminiImageOutputs(c, textOnly)

		require.Equal(t, 1, resolveGeminiImageCount(c, "gemini-3-pro-image-preview", "gemini-3-pro-image-preview"))
	})

	t.Run("falls back to mapped upstream model name", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		beginGeminiImageOutputObservation(c)
		observeGeminiImageOutputs(c, textOnly)

		require.Equal(t, 1, resolveGeminiImageCount(c, "my-image-alias", "gemini-2.5-flash-image"))
	})

	t.Run("text model stays unbilled", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		beginGeminiImageOutputObservation(c)
		observeGeminiImageOutputs(c, textOnly)

		require.Equal(t, 0, resolveGeminiImageCount(c, "gemini-2.5-pro", "gemini-2.5-pro"))
	})

	t.Run("no counter on context degrades to name heuristic", func(t *testing.T) {
		c := newGeminiImageTestContext(t)
		require.Equal(t, 0, resolveGeminiImageCount(c, "nana-banana-2", "nana-banana-2"))
		require.Equal(t, 1, resolveGeminiImageCount(c, "gemini-3-pro-image", "gemini-3-pro-image"))
	})
}

// 端到端守住接线：/v1beta/models/{model}:generateContent 的非流式响应体
// 必须真的喂进计数器，否则上面的单测全绿而线上依然记 $0。
func TestHandleNativeNonStreamingResponse_FeedsImageCounter(t *testing.T) {
	c := newGeminiImageTestContext(t)
	beginGeminiImageOutputObservation(c)

	body := geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}

	svc := &GeminiMessagesCompatService{}
	usage, err := svc.handleNativeNonStreamingResponse(c, resp, false, "generateContent")
	require.NoError(t, err)
	require.NotNil(t, usage)

	require.Equal(t, 1, observedGeminiImageOutputs(c))
	require.Equal(t, 1, resolveGeminiImageCount(c, "nana-banana-2", "nana-banana-2"))
}

func TestGeminiForwardNative_FileReferencesKeepExistingBilling(t *testing.T) {
	const model = "gemini-nano-banana-2.1"
	require.False(t, isImageGenerationModel(model))
	const input = `{"contents":[{"role":"user","parts":[{"text":"generate a photo"}]}]}`
	const camelURI = "https://example.test/two.png"
	const snakeURI = "https://example.test/one.jpg"
	parts := `{"file_data":{"mime_type":"image/jpeg","file_uri":"` + snakeURI + `"}},{"fileData":{"mimeType":"image/png","fileUri":"` + camelURI + `"}}`
	body := strings.TrimSuffix(geminiImageResponse(parts), "}") + `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`
	const groupID = 128
	billing := &GatewayService{
		billingService: NewBillingService(&config.Config{}, nil),
		resolver:       newOpenAIImageChannelPricingResolverForTest(t, groupID, model, 0.25),
	}
	for _, tt := range []struct {
		name, mappedModel string
		wantCount         int
		wantCost          float64
	}{
		{name: "unrecognized alias", mappedModel: model, wantCount: 0, wantCost: 0.25},
		{name: "alias mapped to a known image model", mappedModel: "gemini-2.5-flash-image", wantCount: 1, wantCost: 0.25},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/"+model+":generateContent", nil)
			upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"application/json"}},
				Body:       io.NopCloser(strings.NewReader(body)),
			}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{}}
			account := &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey, Credentials: map[string]any{
				"api_key":       "test-only-placeholder",
				"model_mapping": map[string]any{model: tt.mappedModel},
			}}
			result, err := svc.ForwardNative(context.Background(), c, account, model, "generateContent", false, []byte(input))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, w.Code)
			require.Contains(t, w.Body.String(), camelURI)
			require.Contains(t, w.Body.String(), snakeURI)
			require.Equal(t, tt.mappedModel, result.UpstreamModel)
			require.Equal(t, tt.wantCount, result.ImageCount)
			require.Equal(t, tt.wantCount > 0, result.ImageEndpoint)
			cost := billing.calculateRecordUsageCost(context.Background(), result,
				&APIKey{GroupID: i64p(groupID), Group: &Group{ID: groupID}},
				model, 1.0, 1.0, time.Time{}, nil)
			require.NotNil(t, cost)
			require.Equal(t, string(BillingModeImage), cost.BillingMode)
			require.InDelta(t, tt.wantCost, cost.TotalCost, 1e-12)
		})
	}
}

func TestGeminiForwardImageReferenceAccountingByProtocol(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const fileURI = "https://example.test/image-one.png"
	const anotherFileURI = "https://example.test/image-two.png"
	const references = `{"fileData":{"mimeType":"image/png","fileUri":"` + fileURI + `"}},` +
		`{"fileData":{"mimeType":"image/png","fileUri":"` + anotherFileURI + `"}}`
	const inline = `{"inlineData":{"mimeType":"image/png","data":"` + geminiTestPNG + `"}}`
	for _, tt := range []struct {
		name, model, parts string
		compat, native     int
	}{
		{name: "file references", model: "custom-gemini-photo", parts: references, compat: 0, native: 0},
		{name: "mixed inline and references", model: "custom-gemini-photo", parts: inline + "," + references, compat: 1, native: 1},
		{name: "known model fallback", model: "gemini-2.5-flash-image", parts: references, compat: 1, native: 1},
		{name: "known model with mixed inline and references", model: "gemini-2.5-flash-image", parts: inline + "," + references, compat: 1, native: 1},
	} {
		for _, wire := range []struct {
			name          string
			clientStream  bool
			codeAssistSSE bool
		}{
			{name: "buffered"},
			{name: "streaming", clientStream: true},
			{name: "aggregated Code Assist stream", codeAssistSSE: true},
		} {
			for _, native := range []bool{false, true} {
				protocol := "messages"
				if native {
					protocol = "native"
				}
				t.Run(tt.name+"/"+wire.name+"/"+protocol, func(t *testing.T) {
					usage := `,"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`
					body := strings.TrimSuffix(geminiImageResponse(`{"text":"done"},`+tt.parts), "}") + usage
					account := &Account{ID: 1, Platform: PlatformGemini, Type: AccountTypeAPIKey,
						Credentials: map[string]any{"api_key": "test-only-placeholder"},
					}
					contentType := "application/json"
					if wire.codeAssistSSE {
						account.Type = AccountTypeOAuth
						account.Credentials = map[string]any{"access_token": "test-only-placeholder", "project_id": "test-project"}
						body = `{"response":` + body + `}`
					}
					if wire.clientStream || wire.codeAssistSSE {
						contentType = "text/event-stream"
						body = "data: " + body + "\n\ndata: [DONE]\n\n"
					}
					upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
						StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {contentType}},
						Body: io.NopCloser(strings.NewReader(body)),
					}}
					svc := &GeminiMessagesCompatService{httpUpstream: upstream, tokenProvider: &GeminiTokenProvider{}, cfg: &config.Config{}}
					w := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(w)
					if native {
						action := "generateContent"
						if wire.clientStream {
							action = "streamGenerateContent"
						}
						c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/"+tt.model+":"+action, nil)
						result, err := svc.ForwardNative(context.Background(), c, account, tt.model, action, wire.clientStream,
							[]byte(`{"contents":[{"role":"user","parts":[{"text":"generate a photo"}]}]}`))
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, w.Code)
						require.Contains(t, w.Body.String(), fileURI)
						require.Contains(t, w.Body.String(), anotherFileURI)
						require.Equal(t, tt.native, result.ImageCount)
					} else {
						c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
						result, err := svc.Forward(context.Background(), c, account, []byte(`{"model":"`+tt.model+
							`","max_tokens":16,"messages":[{"role":"user","content":"generate a photo"}],"stream":`+strconv.FormatBool(wire.clientStream)+`}`))
						require.NoError(t, err)
						require.Equal(t, http.StatusOK, w.Code)
						require.Contains(t, w.Body.String(), "done")
						require.NotContains(t, w.Body.String(), fileURI)
						require.NotContains(t, w.Body.String(), anotherFileURI)
						require.Equal(t, tt.compat, result.ImageCount)
					}
					require.Equal(t, 1, upstream.calls)
				})
			}
		}
	}
}

func TestGeminiInlineBillingIgnoresMalformedData(t *testing.T) {
	for _, data := range []string{`123`, `true`, `{"text":"not base64"}`, `["not base64"]`} {
		t.Run(data, func(t *testing.T) {
			payload := geminiImageResponse(`{"inlineData":{"mimeType":"image/png","data":` + data + `}}`)
			c := newGeminiImageTestContext(t)
			beginGeminiImageOutputObservation(c)
			observeGeminiImageOutputs(c, []byte(payload))
			require.Zero(t, resolveGeminiImageCount(c, "custom-alias", "custom-alias"))
		})
	}
}
