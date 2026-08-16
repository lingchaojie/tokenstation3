package service

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type geminiCompatHTTPUpstreamStub struct {
	response *http.Response
	err      error
	calls    int
	lastReq  *http.Request
}

type geminiPartialThenErrorBody struct {
	data []byte
	off  int
	err  error
}

func TestValidGeminiProviderPayloadRejectsMalformedCandidateParts(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"scalar candidate":                       `{"candidates":[123]}`,
		"malformed later candidate":              `{"candidates":[{"content":{"parts":[{"text":"ok"}]}},{"finishReason":123}]}`,
		"nonstring text":                         `{"candidates":[{"content":{"parts":[{"text":123}]},"finishReason":"STOP"}]}`,
		"nonstring function name":                `{"candidates":[{"content":{"parts":[{"functionCall":{"name":123,"args":{}}}]},"finishReason":"STOP"}]}`,
		"scalar function arguments":              `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":123}}]},"finishReason":"STOP"}]}`,
		"multiple data variants":                 `{"candidates":[{"content":{"parts":[{"text":"x","functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`,
		"nonstring model version":                `{"modelVersion":123,"candidates":[{"finishReason":"STOP"}]}`,
		"invalid usage sibling":                  `{"usageMetadata":{"promptTokenCount":"bad"},"candidates":[{"finishReason":"STOP"}]}`,
		"invalid candidate details":              `{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":-7}]},"candidates":[{"finishReason":"STOP"}]}`,
		"cache exceeds prompt":                   `{"usageMetadata":{"promptTokenCount":2,"cachedContentTokenCount":3},"candidates":[{"finishReason":"STOP"}]}`,
		"output token overflow":                  `{"usageMetadata":{"candidatesTokenCount":9223372036854775807,"thoughtsTokenCount":1},"candidates":[{"finishReason":"STOP"}]}`,
		"image exceeds output":                   `{"usageMetadata":{"candidatesTokenCount":2,"candidatesTokensDetails":[{"modality":"IMAGE","tokenCount":3}]},"candidates":[{"finishReason":"STOP"}]}`,
		"invalid prompt rating":                  `{"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[123]}}`,
		"invalid candidate ratings":              `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","safetyRatings":"bad"}]}`,
		"invalid finish message":                 `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","finishMessage":123}]}`,
		"scalar grounding metadata":              `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":123}]}`,
		"scalar grounding queries":               `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":"bad"}}]}`,
		"nonstring grounding query":              `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":{"webSearchQueries":[123]}}]}`,
		"scalar grounding chunk":                 `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":{"groundingChunks":[123]}}]}`,
		"nonstring grounding web field":          `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":{"groundingChunks":[{"web":{"uri":123}}]}}]}`,
		"implicit then explicit duplicate index": `{"candidates":[{"content":{"parts":[{"text":"first"}]}},{"index":0,"content":{"parts":[{"text":"second"}]},"finishReason":"STOP"}]}`,
		"explicit then implicit duplicate index": `{"candidates":[{"index":1,"content":{"parts":[{"text":"first"}]}},{"content":{"parts":[{"text":"second"}]},"finishReason":"STOP"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, validGeminiProviderPayload([]byte(body)))
		})
	}

	require.True(t, validGeminiProviderPayload([]byte(`{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`)))
	require.False(t, validGeminiProviderPayload([]byte(`{"candidates":[{"finishReason":"STOP"}]}`)))
	require.True(t, validGeminiProviderPayload([]byte(`{"candidates":[{"finishReason":"SAFETY"}]}`)))
	require.True(t, validGeminiProviderPayload([]byte(`{"candidates":[{"content":{"role":"model","parts":[{"codeExecutionResult":{"outcome":"OUTCOME_OK","output":""}}]},"finishReason":"STOP"}]}`)))
}

func TestGeminiProviderValidationRejectsDuplicateKnownJSONKeys(t *testing.T) {
	for name, body := range map[string]string{
		"root candidates":  `{"candidates":[{"content":{"parts":[{"text":"safe"}]},"finishReason":"STOP"}],"candidates":[{"content":{"parts":[{"text":"danger"}]},"finishReason":"STOP"}]}`,
		"candidate finish": `{"candidates":[{"content":{"parts":[{"text":"safe"}]},"finishReason":"STOP","finishReason":"SAFETY"}]}`,
		"part text":        `{"candidates":[{"content":{"parts":[{"text":"safe","text":"danger"}]},"finishReason":"STOP"}]}`,
		"function name":    `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"safe","name":"danger","args":{}}}]},"finishReason":"STOP"}]}`,
		"usage count":      `{"usageMetadata":{"promptTokenCount":1,"promptTokenCount":2},"candidates":[{"content":{"parts":[{"text":"safe"}]},"finishReason":"STOP"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, validGeminiProviderPayload([]byte(body)))
			_, err := (&geminiProviderStreamState{}).observePayload([]byte(body))
			require.ErrorContains(t, err, "repeated known field")
			_, err = decodeGeminiCompatResponse([]byte(body))
			require.ErrorContains(t, err, "repeated known field")
		})
	}

	forwardCompatible := []byte(`{"candidates":[{"content":{"parts":[{"text":"safe","opaque":{"type":"future-a","type":"future-b"}}]},"finishReason":"STOP"}]}`)
	require.True(t, validGeminiProviderPayload(forwardCompatible))
	_, err := decodeGeminiCompatResponse(forwardCompatible)
	require.NoError(t, err)
}

func TestValidGeminiRootEnvelopeRejectsOversizedRetainedMetadata(t *testing.T) {
	metadataTooLong := strings.Repeat("m", 1025)
	signatureTooLong := strings.Repeat("s", (64<<10)+1)
	quote := func(value string) string {
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		return string(encoded)
	}

	for name, body := range map[string]string{
		"model version":          `{"modelVersion":` + quote(metadataTooLong) + `}`,
		"response id":            `{"responseId":` + quote(metadataTooLong) + `}`,
		"block reason":           `{"promptFeedback":{"blockReason":` + quote(metadataTooLong) + `}}`,
		"block reason message":   `{"promptFeedback":{"blockReason":"SAFETY","blockReasonMessage":` + quote(metadataTooLong) + `}}`,
		"usage modality":         `{"usageMetadata":{"promptTokenCount":1,"promptTokensDetails":[{"modality":` + quote(metadataTooLong) + `,"tokenCount":1}]}}`,
		"safety category":        `{"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[{"category":` + quote(metadataTooLong) + `}]}}`,
		"safety probability":     `{"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[{"probability":` + quote(metadataTooLong) + `}]}}`,
		"safety severity":        `{"promptFeedback":{"blockReason":"SAFETY","safetyRatings":[{"severity":` + quote(metadataTooLong) + `}]}}`,
		"finish reason":          `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":` + quote(metadataTooLong) + `}]}`,
		"finish message":         `{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP","finishMessage":` + quote(metadataTooLong) + `}]}`,
		"function call name":     `{"candidates":[{"content":{"parts":[{"functionCall":{"name":` + quote(metadataTooLong) + `,"args":{}}}]},"finishReason":"STOP"}]}`,
		"function response name": `{"candidates":[{"content":{"parts":[{"functionResponse":{"name":` + quote(metadataTooLong) + `,"response":{}}}]},"finishReason":"STOP"}]}`,
		"thought signature":      `{"candidates":[{"content":{"parts":[{"text":"ok","thoughtSignature":` + quote(signatureTooLong) + `}]},"finishReason":"STOP"}]}`,
		"inline mime type":       `{"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":` + quote(metadataTooLong) + `,"data":"AA=="}}]},"finishReason":"STOP"}]}`,
		"file uri":               `{"candidates":[{"content":{"parts":[{"fileData":{"fileUri":` + quote(metadataTooLong) + `}}]},"finishReason":"STOP"}]}`,
		"file mime type":         `{"candidates":[{"content":{"parts":[{"fileData":{"mimeType":` + quote(metadataTooLong) + `,"fileUri":"gs://bucket/file"}}]},"finishReason":"STOP"}]}`,
		"code language":          `{"candidates":[{"content":{"parts":[{"executableCode":{"language":` + quote(metadataTooLong) + `,"code":""}}]},"finishReason":"STOP"}]}`,
		"execution outcome":      `{"candidates":[{"content":{"parts":[{"codeExecutionResult":{"outcome":` + quote(metadataTooLong) + `,"output":""}}]},"finishReason":"STOP"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			require.False(t, validGeminiRootEnvelopeShape([]byte(body)))
		})
	}

	validLongOpaqueSignature := strings.Repeat("s", 8<<10)
	require.True(t, validGeminiRootEnvelopeShape([]byte(
		`{"candidates":[{"content":{"parts":[{"text":"ok","thoughtSignature":`+quote(validLongOpaqueSignature)+`}]},"finishReason":"STOP"}]}`,
	)))
}

func TestGeminiProviderStreamStateRejectsMalformedAncillaryBeforeValidPayload(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"modelVersion":123}`,
		`{"responseId":{}}`,
		`{"usageMetadata":{"promptTokenCount":"bad"}}`,
	} {
		state := &geminiProviderStreamState{}
		_, err := state.observePayload([]byte(body))
		require.Error(t, err)
	}
}

func TestGeminiProviderStreamStateTracksCandidateTerminalsIndependently(t *testing.T) {
	t.Parallel()
	state := &geminiProviderStreamState{}
	provider, err := state.observePayload([]byte(`{"candidates":[{"index":0,"content":{"parts":[{"text":"primary"}]},"finishReason":"STOP"}]}`))
	require.NoError(t, err)
	require.True(t, provider)
	require.False(t, state.terminalObserved(), "a candidate finish is not a framing terminal while more alternatives may follow")

	provider, err = state.observePayload([]byte(`{"candidates":[{"index":1,"content":{"parts":[{"text":"alternative"}]},"finishReason":"STOP"}]}`))
	require.NoError(t, err)
	require.True(t, provider)
	require.True(t, state.applicationTerminalObserved())
	require.NoError(t, state.observeDone())
	require.True(t, state.terminalObserved())
}

func TestGeminiProviderValidationDenseArraysStayWithinBoundedAllocation(t *testing.T) {
	const targetBytes = 8 << 20

	t.Run("candidates", func(t *testing.T) {
		const candidate = `{"content":{"role":"model","parts":[{"text":""}]}},`
		count := (targetBytes - len(`{"candidates":[]}`)) / len(candidate)
		body := []byte(`{"candidates":[` + strings.Repeat(candidate, count) + `{"content":{"role":"model","parts":[{"text":""}]}}]}`)
		require.GreaterOrEqual(t, len(body), targetBytes-(2*len(candidate)))

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := (&geminiProviderStreamState{}).observePayload(body)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		require.ErrorContains(t, err, "too many candidates")
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
	})

	t.Run("parts", func(t *testing.T) {
		const part = `{"text":""},`
		count := (targetBytes - len(`{"candidates":[{"content":{"role":"model","parts":[]}}]}`)) / len(part)
		body := []byte(`{"candidates":[{"content":{"role":"model","parts":[` + strings.Repeat(part, count) + `{"text":""}]}}]}`)
		require.GreaterOrEqual(t, len(body), targetBytes-(2*len(part)))

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := (&geminiProviderStreamState{}).observePayload(body)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		require.ErrorContains(t, err, "too many parts")
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
	})

	t.Run("grounding chunks", func(t *testing.T) {
		const chunk = `{},`
		count := (targetBytes - len(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":{"groundingChunks":[]}}]}`)) / len(chunk)
		body := []byte(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP","groundingMetadata":{"groundingChunks":[` + strings.Repeat(chunk, count) + `{}` + `]}}]}`)
		require.GreaterOrEqual(t, len(body), targetBytes-(2*len(chunk)))

		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		_, err := (&geminiProviderStreamState{}).observePayload(body)
		var after runtime.MemStats
		runtime.ReadMemStats(&after)

		require.Error(t, err)
		require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
	})
}

func TestDecodeGeminiCompatResponseIgnoresDenseUnknownFieldsWithBoundedAllocation(t *testing.T) {
	const targetBytes = 8 << 20
	const item = `{},`
	count := (targetBytes - len(`{"junk":[],"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)) / len(item)
	body := []byte(`{"junk":[` + strings.Repeat(item, count) + `{}` + `],"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`)
	require.GreaterOrEqual(t, len(body), targetBytes-(2*len(item)))

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	decoded, err := decodeGeminiCompatResponse(body)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	require.NoError(t, err)
	require.Equal(t, "STOP", extractGeminiFinishReason(decoded))
	require.Equal(t, "ok", extractGeminiParts(decoded)[0]["text"])
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(12<<20))
}

func TestGeminiProviderStreamStateBoundsTrackedCandidates(t *testing.T) {
	state := &geminiProviderStreamState{}
	for index := 0; index <= 1024; index++ {
		_, err := state.observePayload([]byte(fmt.Sprintf(`{"candidates":[{"index":%d,"finishReason":"SAFETY"}]}`, index)))
		if index < 1024 {
			require.NoError(t, err)
			continue
		}
		require.ErrorContains(t, err, "too many candidates")
	}
}

func TestGeminiProviderStreamStateDONERequiresApplicationTerminal(t *testing.T) {
	t.Parallel()
	state := &geminiProviderStreamState{}
	provider, err := state.observePayload([]byte(`{"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]}}]}`))
	require.NoError(t, err)
	require.True(t, provider)
	require.Error(t, state.observeDone())
}

func TestValidGeminiTerminalResponseRequiresEveryCandidateToFinish(t *testing.T) {
	t.Parallel()
	require.False(t, validGeminiTerminalResponse([]byte(`{"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`)))
	require.False(t, validGeminiTerminalResponse([]byte(`{"candidates":[{"index":0,"finishReason":"STOP"},{"index":1,"content":{"parts":[{"text":"partial"}]}}]}`)))
	require.False(t, validGeminiTerminalResponse([]byte(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]}}]}`)))
	require.False(t, validGeminiTerminalResponse([]byte(`{"candidates":[{"index":0,"finishReason":"STOP"}]}`)))
	require.True(t, validGeminiTerminalResponse([]byte(`{"candidates":[{"index":0,"content":{"parts":[{"text":"primary"}]},"finishReason":"STOP"},{"index":1,"content":{"parts":[{"text":"done"}]},"finishReason":"STOP"}]}`)))
}

func TestGeminiProviderStreamStateRejectsBlockedPromptWithCandidates(t *testing.T) {
	t.Parallel()
	state := &geminiProviderStreamState{}
	_, err := state.observePayload([]byte(`{"promptFeedback":{"blockReason":"SAFETY"},"candidates":[{"index":0,"content":{"parts":[{"text":"partial"}]}}]}`))
	require.Error(t, err)
}

func TestExtractGeminiUsageSumsAllImageDetailsCaseInsensitively(t *testing.T) {
	usage := extractGeminiUsage([]byte(`{"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":7,"thoughtsTokenCount":1,"candidatesTokensDetails":[{"modality":"image","tokenCount":2},{"modality":"IMAGE","tokenCount":3},{"modality":"TEXT","tokenCount":2}]}}`))
	require.NotNil(t, usage)
	require.Equal(t, 8, usage.OutputTokens)
	require.Equal(t, 5, usage.ImageOutputTokens)
}

func (b *geminiPartialThenErrorBody) Read(p []byte) (int, error) {
	if b.off < len(b.data) {
		n := copy(p, b.data[b.off:])
		b.off += n
		return n, nil
	}
	return 0, b.err
}

func (b *geminiPartialThenErrorBody) Close() error { return nil }

func (s *geminiCompatHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	s.calls++
	s.lastReq = req
	if s.err != nil {
		return nil, s.err
	}
	if s.response == nil {
		return nil, fmt.Errorf("missing stub response")
	}
	resp := *s.response
	return &resp, nil
}

func (s *geminiCompatHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestGeminiForwardAsChatCompletions_OAuthRoutesToGeminiAndReturnsChatFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamBody := `data: {"response":{"candidates":[{"content":{"parts":[{"text":"hello from gemini"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3}}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}
	svc := &GeminiMessagesCompatService{
		tokenProvider: &GeminiTokenProvider{},
		httpUpstream:  httpStub,
		cfg:           &config.Config{},
	}
	account := &Account{
		ID:       101,
		Platform: PlatformGemini,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "ya29.test-token",
			"project_id":   "project-1",
		},
		Concurrency: 1,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "gemini-2.5-flash", result.Model)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)

	require.NotNil(t, httpStub.lastReq)
	require.Contains(t, httpStub.lastReq.URL.String(), "/v1internal:streamGenerateContent?alt=sse")
	require.Equal(t, "Bearer ya29.test-token", httpStub.lastReq.Header.Get("Authorization"))
	require.Empty(t, httpStub.lastReq.Header.Get("x-api-key"))
	require.Empty(t, httpStub.lastReq.Header.Get("anthropic-version"))

	var sent map[string]any
	sentBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(sentBody, &sent))
	require.Equal(t, "gemini-2.5-flash", sent["model"])
	require.Equal(t, "project-1", sent["project"])
	require.Contains(t, fmt.Sprint(sent["request"]), "hi")

	var got map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "chat.completion", got["object"])
	require.Equal(t, "gemini-2.5-flash", got["model"])
	choices, ok := got["choices"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, choices)
	choice, ok := choices[0].(map[string]any)
	require.True(t, ok)
	message, ok := choice["message"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "assistant", message["role"])
	require.Equal(t, "hello from gemini", message["content"])
	usage, ok := got["usage"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(7), usage["prompt_tokens"])
	require.Equal(t, float64(3), usage["completion_tokens"])
	require.Equal(t, float64(10), usage["total_tokens"])
}

func TestGeminiForwardAsChatCompletions_StreamsOpenAIChunksFromGeminiSSE(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamBody := `data: {"candidates":[{"content":{"parts":[{"text":"hel"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":2}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		},
	}
	svc := &GeminiMessagesCompatService{
		httpUpstream: httpStub,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:       102,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "gemini-api-key",
		},
		Concurrency: 1,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-2.5-flash","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.True(t, result.Stream)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)

	require.NotNil(t, httpStub.lastReq)
	require.Contains(t, httpStub.lastReq.URL.String(), "/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse")
	require.Equal(t, "gemini-api-key", httpStub.lastReq.Header.Get("x-goog-api-key"))

	out := rec.Body.String()
	require.Contains(t, out, `"object":"chat.completion.chunk"`)
	require.Contains(t, out, `"role":"assistant"`)
	require.Contains(t, out, `"content":"hel"`)
	require.Contains(t, out, `"content":"lo"`)
	require.Contains(t, out, `"usage":{"prompt_tokens":2,"completion_tokens":2,"total_tokens":4}`)
	require.Contains(t, out, "data: [DONE]")
}

func TestGeminiForwardAsChatCompletions_SelectsFirstCandidateWithoutMergingAlternatives(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstreamBody := `data: {"candidates":[{"content":{"parts":[{"text":"primary"}]},"finishReason":"STOP"},{"content":{"parts":[{"text":"alternative-must-not-merge"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}` + "\n\n" +
		"data: [DONE]\n\n"
	httpStub := &geminiCompatHTTPUpstreamStub{response: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &GeminiMessagesCompatService{httpUpstream: httpStub, cfg: &config.Config{}}
	account := &Account{
		ID: 103, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1,
		Credentials: map[string]any{"api_key": "gemini-api-key"},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"gemini-2.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, recorder.Body.String(), `"content":"primary"`)
	require.NotContains(t, recorder.Body.String(), "alternative-must-not-merge")
}

func TestGeminiStreamingReadErrorAfterSemanticOutputPreservesPartialUsageAndTerminalCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	providerBody := []byte(`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}` + "\n\n")
	forcedErr := errors.New("forced gemini stream read failure")

	tests := []struct {
		name    string
		path    string
		body    []byte
		forward func(*GeminiMessagesCompatService, *gin.Context, *Account, []byte) (*ForwardResult, error)
	}{
		{
			name: "anthropic_compat",
			path: "/v1/messages",
			body: []byte(`{"model":"gemini-2.5-flash","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			forward: func(s *GeminiMessagesCompatService, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
				return s.Forward(context.Background(), c, account, body)
			},
		},
		{
			name: "chat_completions_compat",
			path: "/v1/chat/completions",
			body: []byte(`{"model":"gemini-2.5-flash","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"user","content":"hi"}]}`),
			forward: func(s *GeminiMessagesCompatService, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
				return s.ForwardAsChatCompletions(context.Background(), c, account, body)
			},
		},
		{
			name: "gemini_native",
			path: "/v1beta/models/gemini-2.5-flash:streamGenerateContent",
			body: []byte(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`),
			forward: func(s *GeminiMessagesCompatService, c *gin.Context, account *Account, body []byte) (*ForwardResult, error) {
				return s.ForwardNative(context.Background(), c, account, "gemini-2.5-flash", "streamGenerateContent", true, body)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &geminiCompatHTTPUpstreamStub{response: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Goog-Request-Id": {"gemini-partial"}},
				Body:       &geminiPartialThenErrorBody{data: providerBody, err: forcedErr},
			}}
			svc := &GeminiMessagesCompatService{httpUpstream: upstream, cfg: &config.Config{Gateway: config.GatewayConfig{
				Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20},
			}}}
			account := &Account{ID: 120, Platform: PlatformGemini, Type: AccountTypeAPIKey, Concurrency: 1, Credentials: map[string]any{"api_key": "gemini-api-key"}}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, bytes.NewReader(tt.body))
			enableCaptureForTest(t, c)

			result, err := tt.forward(svc, c, account, tt.body)

			require.ErrorIs(t, err, forcedErr)
			require.NotNil(t, result, "semantic output must retain a billable partial result")
			require.False(t, result.UpstreamFailed, "committed partial output is not a retryable pre-output failure")
			require.True(t, result.CaptureTerminalError)
			require.Equal(t, 2, result.Usage.InputTokens)
			require.Equal(t, 1, result.Usage.OutputTokens)
			require.Equal(t, providerBody, result.CaptureResponse)
			require.Contains(t, recorder.Body.String(), "partial")
		})
	}
}

func TestGeminiForwardAsChatCompletions_MapsImageGenerationConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`)),
		},
	}
	svc := &GeminiMessagesCompatService{
		httpUpstream: httpStub,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:       103,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "gemini-api-key",
		},
		Concurrency: 1,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{"model":"gemini-3.1-flash-image","messages":[{"role":"user","content":"draw"}],"tools":[{"type":"image_generation","size":"2K","aspect_ratio":"16:9"}]}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "gemini-3.1-flash-image", result.Model)

	require.NotNil(t, httpStub.lastReq)
	sentBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)
	require.Equal(t, "IMAGE", gjson.GetBytes(sentBody, "generationConfig.responseModalities.1").String())
	require.Equal(t, "16:9", gjson.GetBytes(sentBody, "generationConfig.imageConfig.aspectRatio").String())
	require.Equal(t, "2K", gjson.GetBytes(sentBody, "generationConfig.imageConfig.imageSize").String())
}

func TestGeminiForwardAsChatCompletions_FunctionNamedWebSearchStaysClientSide(t *testing.T) {
	gin.SetMode(gin.TestMode)

	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(
				`{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1}}`,
			)),
		},
	}
	svc := &GeminiMessagesCompatService{
		httpUpstream: httpStub,
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:       103,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "gemini-api-key",
		},
		Concurrency: 1,
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	body := []byte(`{
		"model":"gemini-3.6-flash-high",
		"messages":[{"role":"user","content":"search and read"}],
		"tools":[
			{"type":"function","function":{"name":"web_search","description":"Search through the Hermes client","parameters":{"type":"object","properties":{"query":{"type":"string"}},"required":["query"]}}},
			{"type":"function","function":{"name":"read_file","description":"Read a local file","parameters":{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}}}
		]
	}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, httpStub.lastReq)

	postedBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)

	var posted map[string]any
	require.NoError(t, json.Unmarshal(postedBody, &posted))
	tools, ok := posted["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 1, "Chat Completions function tools must not be promoted to Gemini built-ins by name")

	functionTool, ok := tools[0].(map[string]any)
	require.True(t, ok)
	functionDecls, ok := functionTool["functionDeclarations"].([]any)
	require.True(t, ok)
	require.Len(t, functionDecls, 2)
	webSearchDecl, ok := functionDecls[0].(map[string]any)
	require.True(t, ok)
	readFileDecl, ok := functionDecls[1].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "web_search", webSearchDecl["name"])
	require.Equal(t, "read_file", readFileDecl["name"])
	require.NotContains(t, functionTool, "googleSearch")
	require.NotContains(t, functionTool, "google_search")
}

// TestConvertClaudeToolsToGeminiTools_CustomType 测试custom类型工具转换
func TestConvertClaudeToolsToGeminiTools_CustomType(t *testing.T) {
	tests := []struct {
		name        string
		tools       any
		expectedLen int
		description string
	}{
		{
			name: "Standard tools",
			tools: []any{
				map[string]any{
					"name":         "get_weather",
					"description":  "Get weather info",
					"input_schema": map[string]any{"type": "object"},
				},
			},
			expectedLen: 1,
			description: "标准工具格式应该正常转换",
		},
		{
			name: "Custom type tool (MCP format)",
			tools: []any{
				map[string]any{
					"type": "custom",
					"name": "mcp_tool",
					"custom": map[string]any{
						"description":  "MCP tool description",
						"input_schema": map[string]any{"type": "object"},
					},
				},
			},
			expectedLen: 1,
			description: "Custom类型工具应该从custom字段读取",
		},
		{
			name: "Mixed standard and custom tools",
			tools: []any{
				map[string]any{
					"name":         "standard_tool",
					"description":  "Standard",
					"input_schema": map[string]any{"type": "object"},
				},
				map[string]any{
					"type": "custom",
					"name": "custom_tool",
					"custom": map[string]any{
						"description":  "Custom",
						"input_schema": map[string]any{"type": "object"},
					},
				},
			},
			expectedLen: 1,
			description: "混合工具应该都能正确转换",
		},
		{
			name: "Custom tool without custom field",
			tools: []any{
				map[string]any{
					"type": "custom",
					"name": "invalid_custom",
					// 缺少 custom 字段
				},
			},
			expectedLen: 0, // 应该被跳过
			description: "缺少custom字段的custom工具应该被跳过",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertClaudeToolsToGeminiTools(tt.tools)

			if tt.expectedLen == 0 {
				if result != nil {
					t.Errorf("%s: expected nil result, got %v", tt.description, result)
				}
				return
			}

			if result == nil {
				t.Fatalf("%s: expected non-nil result", tt.description)
			}

			if len(result) != 1 {
				t.Errorf("%s: expected 1 tool declaration, got %d", tt.description, len(result))
				return
			}

			toolDecl, ok := result[0].(map[string]any)
			if !ok {
				t.Fatalf("%s: result[0] is not map[string]any", tt.description)
			}

			funcDecls, ok := toolDecl["functionDeclarations"].([]any)
			if !ok {
				t.Fatalf("%s: functionDeclarations is not []any", tt.description)
			}

			toolsArr, _ := tt.tools.([]any)
			expectedFuncCount := 0
			for _, tool := range toolsArr {
				toolMap, _ := tool.(map[string]any)
				if toolMap["name"] != "" {
					// 检查是否为有效的custom工具
					if toolMap["type"] == "custom" {
						if toolMap["custom"] != nil {
							expectedFuncCount++
						}
					} else {
						expectedFuncCount++
					}
				}
			}

			if len(funcDecls) != expectedFuncCount {
				t.Errorf("%s: expected %d function declarations, got %d",
					tt.description, expectedFuncCount, len(funcDecls))
			}
		})
	}
}

func TestCleanToolSchema_NormalizesGeminiUnsupportedSchemaFields(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"$defs": map[string]any{
			"unused": map[string]any{"type": "string"},
		},
		"definitions": map[string]any{
			"legacy": map[string]any{"type": "number"},
		},
		"properties": map[string]any{
			"path": map[string]any{
				"type": []any{"string", "null"},
			},
			"count": map[string]any{
				"type": []any{"null", "integer"},
			},
			"empty": map[string]any{
				"type": []any{"null"},
			},
		},
	}

	cleaned, ok := cleanToolSchema(schema).(map[string]any)
	require.True(t, ok)
	require.Equal(t, "OBJECT", cleaned["type"])
	require.NotContains(t, cleaned, "$defs")
	require.NotContains(t, cleaned, "definitions")

	properties, ok := cleaned["properties"].(map[string]any)
	require.True(t, ok)

	pathSchema, ok := properties["path"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "STRING", pathSchema["type"])

	countSchema, ok := properties["count"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "INTEGER", countSchema["type"])

	emptySchema, ok := properties["empty"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, emptySchema, "type")
}

func TestConvertClaudeToolsToGeminiTools_PreservesWebSearchAlongsideFunctions(t *testing.T) {
	tools := []any{
		map[string]any{
			"name":         "get_weather",
			"description":  "Get weather info",
			"input_schema": map[string]any{"type": "object"},
		},
		map[string]any{
			"type": "web_search_20250305",
			"name": "web_search",
		},
	}

	result := convertClaudeToolsToGeminiTools(tools)
	require.Len(t, result, 2)

	functionDecl, ok := result[0].(map[string]any)
	require.True(t, ok)
	funcDecls, ok := functionDecl["functionDeclarations"].([]any)
	require.True(t, ok)
	require.Len(t, funcDecls, 1)

	searchDecl, ok := result[1].(map[string]any)
	require.True(t, ok)
	googleSearch, ok := searchDecl["googleSearch"].(map[string]any)
	require.True(t, ok)
	require.Empty(t, googleSearch)
}

func TestGeminiHandleNativeNonStreamingResponse_DebugDisabledDoesNotEmitHeaderLogs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logSink, restore := captureStructuredLog(t)
	defer restore()

	svc := &GeminiMessagesCompatService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				GeminiDebugResponseHeaders: false,
			},
		},
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":      []string{"application/json"},
			"X-RateLimit-Limit": []string{"60"},
		},
		Body: io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":2}}`)),
	}

	usage, err := svc.handleNativeNonStreamingResponse(c, resp, false, "generateContent")
	require.NoError(t, err)
	require.NotNil(t, usage)
	require.False(t, logSink.ContainsMessage("[GeminiAPI]"), "debug 关闭时不应输出 Gemini 响应头日志")
}

func TestGeminiMessagesCompatServiceForward_PreservesRequestedModelAndMappedUpstreamModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	enableCaptureForTest(t, c)

	rawProviderResponse := []byte(`{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`)
	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"x-request-id": []string{"gemini-req-1"}},
			Body:       io.NopCloser(bytes.NewReader(rawProviderResponse)),
		},
	}
	transport := &recordingCaptureTransport{}
	svc := &GeminiMessagesCompatService{httpUpstream: httpStub, cfg: &config.Config{Gateway: config.GatewayConfig{
		Capture: config.GatewayCaptureConfig{Enabled: true, MaxBodyBytes: 1 << 20, MaxHeaderBytes: 1 << 20},
	}}, capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true })}
	account := &Account{
		ID:       1,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-key",
			"model_mapping": map[string]any{
				"claude-sonnet-4": "claude-sonnet-4-20250514",
			},
		},
	}
	body := []byte(`{"model":"claude-sonnet-4","max_tokens":16,"messages":[{"role":"user","content":"hello"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "claude-sonnet-4", result.Model)
	require.Equal(t, "claude-sonnet-4-20250514", result.UpstreamModel)
	require.Equal(t, 1, httpStub.calls)
	require.NotNil(t, httpStub.lastReq)
	require.Contains(t, httpStub.lastReq.URL.String(), "/models/claude-sonnet-4-20250514:")
	require.Nil(t, result.CaptureRequest)
	require.Nil(t, result.CaptureResponse)
	require.Zero(t, result.CaptureHTTPStatus)
	require.Nil(t, result.CaptureContentPolicy)
	require.True(t, CommitForwardCaptureAttempt(c, PlatformGemini, result))
	require.Len(t, transport.Attempts(), 1)
	attempt := transport.Attempts()[0]
	require.Equal(t, snapshotHTTPRequestBody(httpStub.lastReq), attempt.RequestBytes())
	require.Equal(t, rawProviderResponse, attempt.ResponseBytes())
	require.Equal(t, []captureTerminalState{captureCommitted}, attempt.TerminalStates())
	require.NotContains(t, string(attempt.RequestHeaderBytes()), "test-key")
}

func TestGeminiMessagesCompatServiceForward_NormalizesWebSearchToolForAIStudio(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	httpStub := &geminiCompatHTTPUpstreamStub{
		response: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"x-request-id": []string{"gemini-req-2"}},
			Body:       io.NopCloser(strings.NewReader(`{"candidates":[{"content":{"parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5}}`)),
		},
	}
	svc := &GeminiMessagesCompatService{httpUpstream: httpStub, cfg: &config.Config{}}
	account := &Account{
		ID:   1,
		Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": "test-key",
		},
	}
	body := []byte(`{"model":"claude-sonnet-4","max_tokens":16,"messages":[{"role":"user","content":"hello"}],"tools":[{"name":"get_weather","description":"Get weather info","input_schema":{"type":"object"}},{"type":"web_search_20250305","name":"web_search"}]}`)

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, httpStub.lastReq)

	postedBody, err := io.ReadAll(httpStub.lastReq.Body)
	require.NoError(t, err)

	var posted map[string]any
	require.NoError(t, json.Unmarshal(postedBody, &posted))
	tools, ok := posted["tools"].([]any)
	require.True(t, ok)
	require.Len(t, tools, 2)

	searchTool, ok := tools[1].(map[string]any)
	require.True(t, ok)
	_, hasSnake := searchTool["google_search"]
	_, hasCamel := searchTool["googleSearch"]
	require.True(t, hasSnake)
	require.False(t, hasCamel)
	_, hasFuncDecl := searchTool["functionDeclarations"]
	require.False(t, hasFuncDecl)
}

func TestConvertClaudeMessagesToGeminiGenerateContent_AddsThoughtSignatureForToolUse(t *testing.T) {
	claudeReq := map[string]any{
		"model":      "claude-haiku-4-5-20251001",
		"max_tokens": 10,
		"messages": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{"type": "text", "text": "hi"},
				},
			},
			map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "text", "text": "ok"},
					map[string]any{
						"type":  "tool_use",
						"id":    "toolu_123",
						"name":  "default_api:write_file",
						"input": map[string]any{"path": "a.txt", "content": "x"},
						// no signature on purpose
					},
				},
			},
		},
		"tools": []any{
			map[string]any{
				"name":        "default_api:write_file",
				"description": "write file",
				"input_schema": map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
				},
			},
		},
	}
	b, _ := json.Marshal(claudeReq)

	out, err := convertClaudeMessagesToGeminiGenerateContent(b)
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	s := string(out)
	if !strings.Contains(s, "\"functionCall\"") {
		t.Fatalf("expected functionCall in output, got: %s", s)
	}
	if !strings.Contains(s, "\"thoughtSignature\":\""+geminiDummyThoughtSignature+"\"") {
		t.Fatalf("expected injected thoughtSignature %q, got: %s", geminiDummyThoughtSignature, s)
	}
}

func TestConvertClaudeMessagesToGeminiGenerateContent_MapsOutputEffortToThinkingLevel(t *testing.T) {
	claudeReq := map[string]any{
		"model":         "gemini-3.5-flash",
		"max_tokens":    1024,
		"thinking":      map[string]any{"type": "adaptive"},
		"output_config": map[string]any{"effort": "high"},
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "hello",
			},
		},
	}
	body, _ := json.Marshal(claudeReq)

	out, err := convertClaudeMessagesToGeminiGenerateContent(body)

	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(out, "generationConfig.thinkingConfig.thinkingLevel").String())
}

func TestEnsureGeminiFunctionCallThoughtSignatures_InsertsWhenMissing(t *testing.T) {
	geminiReq := map[string]any{
		"contents": []any{
			map[string]any{
				"role": "user",
				"parts": []any{
					map[string]any{
						"functionCall": map[string]any{
							"name": "default_api:write_file",
							"args": map[string]any{"path": "a.txt"},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(geminiReq)
	out := ensureGeminiFunctionCallThoughtSignatures(b)
	s := string(out)
	if !strings.Contains(s, "\"thoughtSignature\":\""+geminiDummyThoughtSignature+"\"") {
		t.Fatalf("expected injected thoughtSignature %q, got: %s", geminiDummyThoughtSignature, s)
	}
}

// TestUnwrapGeminiResponse 测试 unwrapGeminiResponse 的各种输入场景
// 关键区别：只有 response 为 JSON 对象/数组时才解包
func TestUnwrapGeminiResponse(t *testing.T) {
	// 构造 >50KB 的大型 JSON 对象
	largePadding := strings.Repeat("x", 50*1024)
	largeInput := []byte(fmt.Sprintf(`{"response":{"id":"big","pad":"%s"}}`, largePadding))
	largeExpected := fmt.Sprintf(`{"id":"big","pad":"%s"}`, largePadding)

	tests := []struct {
		name     string
		input    []byte
		expected string
		wantErr  bool
	}{
		{
			name:     "正常 response 包装（JSON 对象）",
			input:    []byte(`{"response":{"key":"val"}}`),
			expected: `{"key":"val"}`,
		},
		{
			name:     "无包装直接返回",
			input:    []byte(`{"key":"val"}`),
			expected: `{"key":"val"}`,
		},
		{
			name:     "空 JSON",
			input:    []byte(`{}`),
			expected: `{}`,
		},
		{
			name:     "null response 返回原始 body",
			input:    []byte(`{"response":null}`),
			expected: `{"response":null}`,
		},
		{
			name:     "非法 JSON 返回原始 body",
			input:    []byte(`not json`),
			expected: `not json`,
		},
		{
			name:     "response 为基础类型 string 返回原始 body",
			input:    []byte(`{"response":"hello"}`),
			expected: `{"response":"hello"}`,
		},
		{
			name:     "嵌套 response 只解一层",
			input:    []byte(`{"response":{"response":{"inner":true}}}`),
			expected: `{"response":{"inner":true}}`,
		},
		{
			name:     "大型 JSON >50KB",
			input:    largeInput,
			expected: largeExpected,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := unwrapGeminiResponse(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expected, strings.TrimSpace(string(got)))
		})
	}
}

// ---------------------------------------------------------------------------
// Task 8.1 — extractGeminiUsage 测试
// ---------------------------------------------------------------------------

func TestExtractGeminiUsage(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantNil   bool
		wantUsage *ClaudeUsage
	}{
		{
			name:    "完整 usageMetadata",
			input:   `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"cachedContentTokenCount":20}}`,
			wantNil: false,
			wantUsage: &ClaudeUsage{
				InputTokens:          80,
				OutputTokens:         50,
				CacheReadInputTokens: 20,
			},
		},
		{
			name:    "包含 thoughtsTokenCount",
			input:   `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"thoughtsTokenCount":50}}`,
			wantNil: false,
			wantUsage: &ClaudeUsage{
				InputTokens:          100,
				OutputTokens:         70,
				CacheReadInputTokens: 0,
			},
		},
		{
			name:    "包含 thoughtsTokenCount 与缓存",
			input:   `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":20,"cachedContentTokenCount":30,"thoughtsTokenCount":50}}`,
			wantNil: false,
			wantUsage: &ClaudeUsage{
				InputTokens:          70,
				OutputTokens:         70,
				CacheReadInputTokens: 30,
			},
		},
		{
			name:    "缺失 cachedContentTokenCount",
			input:   `{"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50}}`,
			wantNil: false,
			wantUsage: &ClaudeUsage{
				InputTokens:          100,
				OutputTokens:         50,
				CacheReadInputTokens: 0,
			},
		},
		{
			name:    "无 usageMetadata",
			input:   `{"candidates":[]}`,
			wantNil: true,
		},
		{
			// gjson 对 null 返回 Exists()=true，因此函数不会返回 nil，
			// 而是返回全零的 ClaudeUsage。
			name:    "null usageMetadata — gjson Exists 为 true",
			input:   `{"usageMetadata":null}`,
			wantNil: false,
			wantUsage: &ClaudeUsage{
				InputTokens:          0,
				OutputTokens:         0,
				CacheReadInputTokens: 0,
			},
		},
		{
			name:    "零值字段",
			input:   `{"usageMetadata":{"promptTokenCount":0,"candidatesTokenCount":0,"cachedContentTokenCount":0}}`,
			wantNil: false,
			wantUsage: &ClaudeUsage{
				InputTokens:          0,
				OutputTokens:         0,
				CacheReadInputTokens: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractGeminiUsage([]byte(tt.input))
			if tt.wantNil {
				if got != nil {
					t.Fatalf("期望返回 nil，实际返回 %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("期望返回非 nil，实际返回 nil")
				return
			}
			if got.InputTokens != tt.wantUsage.InputTokens {
				t.Errorf("InputTokens: 期望 %d，实际 %d", tt.wantUsage.InputTokens, got.InputTokens)
			}
			if got.OutputTokens != tt.wantUsage.OutputTokens {
				t.Errorf("OutputTokens: 期望 %d，实际 %d", tt.wantUsage.OutputTokens, got.OutputTokens)
			}
			if got.CacheReadInputTokens != tt.wantUsage.CacheReadInputTokens {
				t.Errorf("CacheReadInputTokens: 期望 %d，实际 %d", tt.wantUsage.CacheReadInputTokens, got.CacheReadInputTokens)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task 8.2 — estimateGeminiCountTokens 测试
// ---------------------------------------------------------------------------

func TestEstimateGeminiCountTokens(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantGt0   bool // 期望结果 > 0
		wantExact *int // 如果非 nil，期望精确匹配
	}{
		{
			name: "含 systemInstruction 和 contents",
			input: `{
				"systemInstruction":{"parts":[{"text":"You are a helpful assistant."}]},
				"contents":[{"parts":[{"text":"Hello, how are you?"}]}]
			}`,
			wantGt0: true,
		},
		{
			name: "仅 contents，无 systemInstruction",
			input: `{
				"contents":[{"parts":[{"text":"Hello, how are you?"}]}]
			}`,
			wantGt0: true,
		},
		{
			name:      "空 parts",
			input:     `{"contents":[{"parts":[]}]}`,
			wantGt0:   false,
			wantExact: intPtr(0),
		},
		{
			name:      "非文本 parts（inlineData）",
			input:     `{"contents":[{"parts":[{"inlineData":{"mimeType":"image/png"}}]}]}`,
			wantGt0:   false,
			wantExact: intPtr(0),
		},
		{
			name:      "空白文本",
			input:     `{"contents":[{"parts":[{"text":"   "}]}]}`,
			wantGt0:   false,
			wantExact: intPtr(0),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := estimateGeminiCountTokens([]byte(tt.input))
			if tt.wantExact != nil {
				if got != *tt.wantExact {
					t.Errorf("期望精确值 %d，实际 %d", *tt.wantExact, got)
				}
				return
			}
			if tt.wantGt0 && got <= 0 {
				t.Errorf("期望返回 > 0，实际 %d", got)
			}
			if !tt.wantGt0 && got != 0 {
				t.Errorf("期望返回 0，实际 %d", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Task 8.3 — ParseGeminiRateLimitResetTime 测试
// ---------------------------------------------------------------------------

func TestParseGeminiRateLimitResetTime(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantNil     bool
		approxDelta int64 // 预期的 (返回值 - now) 大约是多少秒
	}{
		{
			name:        "正常 quotaResetDelay",
			input:       `{"error":{"details":[{"metadata":{"quotaResetDelay":"12.345s"}}]}}`,
			wantNil:     false,
			approxDelta: 13, // 向上取整 12.345 -> 13
		},
		{
			name:        "daily quota",
			input:       `{"error":{"message":"quota per day exceeded"}}`,
			wantNil:     false,
			approxDelta: -1, // 不检查精确 delta，仅检查非 nil
		},
		{
			name:    "无 details 且无 regex 匹配",
			input:   `{"error":{"message":"rate limit"}}`,
			wantNil: true,
		},
		{
			name:        "regex 回退匹配",
			input:       `Please retry in 30s`,
			wantNil:     false,
			approxDelta: 30,
		},
		{
			name:    "完全无匹配",
			input:   `{"error":{"code":429}}`,
			wantNil: true,
		},
		{
			name:        "非法 JSON 但 regex 回退仍工作",
			input:       `not json but Please retry in 10s`,
			wantNil:     false,
			approxDelta: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now().Unix()
			got := ParseGeminiRateLimitResetTime([]byte(tt.input))

			if tt.wantNil {
				if got != nil {
					t.Fatalf("期望返回 nil，实际返回 %d", *got)
				}
				return
			}

			if got == nil {
				t.Fatalf("期望返回非 nil，实际返回 nil")
			}

			// approxDelta == -1 表示只检查非 nil，不检查具体值（如 daily quota 场景）
			if tt.approxDelta == -1 {
				// 仅验证返回的时间戳在合理范围内（未来的某个时间）
				if *got < now {
					t.Errorf("期望返回的时间戳 >= now(%d)，实际 %d", now, *got)
				}
				return
			}

			// 使用 +/-2 秒容差进行范围检查
			delta := *got - now
			if delta < tt.approxDelta-2 || delta > tt.approxDelta+2 {
				t.Errorf("期望 delta 约为 %d 秒（+/-2），实际 delta = %d 秒（返回值=%d, now=%d）",
					tt.approxDelta, delta, *got, now)
			}
		})
	}
}

// TestGeminiMessagesHandleStreamingResponse_ClosesToolBlockBeforeText guards the
// tool→text ordering in the Gemini→Anthropic (messages) streaming bridge. When
// Gemini emits a functionCall part followed by a text part, the tool_use content
// block must be closed before the text block opens; otherwise the Anthropic SSE
// stream contains overlapping content blocks. The chat-completions sibling
// already enforces this via closeOpenTool().
func TestGeminiMessagesHandleStreamingResponse_ClosesToolBlockBeforeText(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamBody := `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"get_weather","args":{"city":"SF"}}}]}}]}` + "\n\n" +
		`data: {"candidates":[{"content":{"parts":[{"text":"All done."}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":3}}` + "\n\n" +
		"data: [DONE]\n\n"

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)

	svc := &GeminiMessagesCompatService{}
	result, err := svc.handleStreamingResponse(c, resp, time.Now(), "claude-3-5-sonnet")
	require.NoError(t, err)
	require.NotNil(t, result)

	events := parseAnthropicContentBlockEvents(t, rec.Body.String())

	// Anthropic allows at most one content block open at a time: every
	// content_block_start must be matched by a content_block_stop before the
	// next start. Replay the lifecycle and assert there is no overlap.
	open := -1
	blockTypes := map[int]string{}
	textStarted := false
	toolClosed := false
	toolClosedBeforeText := false
	for _, ev := range events {
		switch ev.event {
		case "content_block_start":
			require.Equalf(t, -1, open,
				"content block %d opened while block %d was still open (overlapping blocks)", ev.index, open)
			open = ev.index
			blockTypes[ev.index] = ev.blockType
			if ev.blockType == "text" {
				textStarted = true
				if toolClosed {
					toolClosedBeforeText = true
				}
			}
		case "content_block_stop":
			require.Equalf(t, open, ev.index,
				"content_block_stop index %d does not match the open block %d", ev.index, open)
			if blockTypes[ev.index] == "tool_use" {
				toolClosed = true
			}
			open = -1
		}
	}

	require.True(t, textStarted, "expected a text content block to be emitted after the tool call")
	require.True(t, toolClosedBeforeText, "tool_use block must be closed before the text block starts")
	require.Equal(t, -1, open, "stream ended with a content block still open")
}

type geminiPartialClientWriteErrorWriter struct {
	gin.ResponseWriter
	wrote bool
}

type geminiBlockingAfterPrefixReadCloser struct {
	reader *bytes.Reader
	closed chan struct{}
	once   sync.Once
}

func newGeminiBlockingAfterPrefixReadCloser(prefix string) *geminiBlockingAfterPrefixReadCloser {
	return &geminiBlockingAfterPrefixReadCloser{
		reader: bytes.NewReader([]byte(prefix)),
		closed: make(chan struct{}),
	}
}

func (r *geminiBlockingAfterPrefixReadCloser) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	<-r.closed
	return 0, io.EOF
}

func (r *geminiBlockingAfterPrefixReadCloser) Close() error {
	r.once.Do(func() { close(r.closed) })
	return nil
}

func (w *geminiPartialClientWriteErrorWriter) Write(p []byte) (int, error) {
	if w.wrote || len(p) == 0 {
		return 0, errors.New("write failed: client disconnected")
	}
	w.wrote = true
	return 1, errors.New("write failed: client disconnected")
}

func TestGeminiStreamingClientDisconnectDoesNotHideMissingProviderTerminal(t *testing.T) {
	gin.SetMode(gin.TestMode)

	providerBody := "data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n"
	for _, tc := range []struct {
		name string
		run  func(*GeminiMessagesCompatService, *gin.Context, *http.Response) (any, error)
	}{
		{
			name: "messages compatibility",
			run: func(svc *GeminiMessagesCompatService, c *gin.Context, resp *http.Response) (any, error) {
				return svc.handleStreamingResponse(c, resp, time.Now(), "claude-3-5-sonnet")
			},
		},
		{
			name: "native oauth",
			run: func(svc *GeminiMessagesCompatService, c *gin.Context, resp *http.Response) (any, error) {
				return svc.handleNativeStreamingResponse(c, resp, time.Now(), true)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Writer = &geminiPartialClientWriteErrorWriter{ResponseWriter: c.Writer}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(providerBody)),
			}

			result, err := tc.run(&GeminiMessagesCompatService{}, c, resp)
			require.NotNil(t, result)
			require.ErrorContains(t, err, "missing terminal event")
		})
	}
}

func TestGeminiMessagesStreamingResponseHonorsProviderIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := newGeminiBlockingAfterPrefixReadCloser(
		"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n",
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	svc := &GeminiMessagesCompatService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}

	type outcome struct {
		result *geminiStreamResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.handleStreamingResponse(c, resp, time.Now(), "claude-3-5-sonnet")
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		require.NotNil(t, got.result)
		require.ErrorContains(t, got.err, "stream data interval timeout")
	case <-time.After(2 * time.Second):
		_ = body.Close()
		<-done
		t.Fatal("Gemini Messages provider stream ignored StreamDataIntervalTimeout")
	}
	require.NoError(t, body.Close())
}

func TestGeminiNativeStreamingResponseHonorsProviderIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := newGeminiBlockingAfterPrefixReadCloser(
		"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n",
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:streamGenerateContent", nil)
	svc := &GeminiMessagesCompatService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}

	type outcome struct {
		result *geminiNativeStreamResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.handleNativeStreamingResponse(c, resp, time.Now(), true)
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		require.NotNil(t, got.result)
		require.ErrorContains(t, got.err, "stream data interval timeout")
	case <-time.After(2 * time.Second):
		_ = body.Close()
		<-done
		t.Fatal("Gemini native provider stream ignored StreamDataIntervalTimeout")
	}
	require.NoError(t, body.Close())
}

func TestGeminiNativeStreamingAllowsSingleInlineDataFrameBeyondFirstOutputGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	imageData := strings.Repeat("A", openAIFirstOutputStageMaxBytes+1024)
	payload := `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"inlineData":{"mimeType":"image/png","data":"` + imageData + `"}}]},"finishReason":"STOP"}]}`
	body := "data: " + payload + "\n\ndata: [DONE]\n\n"
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:streamGenerateContent", nil)
	svc := &GeminiMessagesCompatService{cfg: &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize: 16 * 1024 * 1024,
	}}}

	result, err := svc.handleNativeStreamingResponse(c, resp, time.Now(), false)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.terminalObserved)
	require.Greater(t, rec.Body.Len(), openAIFirstOutputStageMaxBytes)
	require.Contains(t, rec.Body.String(), `"mimeType":"image/png"`)
}

func TestGeminiChatCompatibilityStreamingResponseHonorsProviderIdleTimeout(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := newGeminiBlockingAfterPrefixReadCloser(
		"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n",
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	svc := &GeminiMessagesCompatService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}}}

	type outcome struct {
		result *geminiStreamResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := svc.handleChatCompletionsStreamingResponseFromGemini(
			context.Background(), c, resp, time.Now(), "gemini-2.5-pro", true, false,
		)
		done <- outcome{result: result, err: err}
	}()

	select {
	case got := <-done:
		require.NotNil(t, got.result)
		require.ErrorContains(t, got.err, "stream data interval timeout")
	case <-time.After(2 * time.Second):
		_ = body.Close()
		<-done
		t.Fatal("Gemini Chat compatibility provider stream ignored StreamDataIntervalTimeout")
	}
	require.NoError(t, body.Close())
}

func TestCollectGeminiSSEHonorsProviderIdleTimeout(t *testing.T) {
	body := newGeminiBlockingAfterPrefixReadCloser(
		"data: {\"candidates\":[{\"index\":0,\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"partial\"}]}}]}\n\n",
	)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       body,
	}
	started := time.Now()

	result, usage, err := collectGeminiSSE(
		resp, true, &config.Config{Gateway: config.GatewayConfig{StreamDataIntervalTimeout: 1}},
	)
	require.Nil(t, result)
	require.Nil(t, usage)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Contains(t, string(failoverErr.ResponseBody), "data interval timeout")
	require.Less(t, time.Since(started), 2*time.Second)
	require.NoError(t, body.Close())
}

func TestCollectGeminiSSEDrainsCapturePastSmallerFunctionalLimit(t *testing.T) {
	line := ": " + strings.Repeat("x", 1022) + "\n"
	providerBody := []byte(strings.Repeat(line, 200))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	beginCaptureAttempt(c)
	request := httptest.NewRequest(http.MethodPost, "https://provider.test/v1beta/models/gemini:streamGenerateContent", nil)
	setCaptureUpstreamRequest(c, request, 1<<20)
	resp := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(providerBody)), Request: request}
	finishCapture := beginCaptureResponse(c, resp, true, 1<<20)
	cfg := &config.Config{Gateway: config.GatewayConfig{
		MaxLineSize:                  2 * 1024 * 1024,
		UpstreamResponseReadMaxBytes: 1024,
	}}

	result, usage, err := collectGeminiSSE(resp, false, cfg)

	require.Nil(t, result)
	require.Nil(t, usage)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	finishCapture()
	capture, ok := takeCaptureResult(c)
	require.True(t, ok)
	require.Len(t, capture.Response, len(providerBody))
	require.True(t, bytes.Equal(providerBody, capture.Response), "capture must preserve the finite provider body exactly")
	require.False(t, capture.ResponseTruncated)
}

type anthropicContentBlockEvent struct {
	event     string
	index     int
	blockType string
}

// parseAnthropicContentBlockEvents extracts content_block_start/stop events (with
// their index and, for starts, the content block type) from an Anthropic SSE body.
func parseAnthropicContentBlockEvents(t *testing.T, raw string) []anthropicContentBlockEvent {
	t.Helper()
	var events []anthropicContentBlockEvent
	for _, chunk := range strings.Split(raw, "\n\n") {
		var eventName, dataLine string
		for _, line := range strings.Split(chunk, "\n") {
			switch {
			case strings.HasPrefix(line, "event:"):
				eventName = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLine = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			}
		}
		if eventName != "content_block_start" && eventName != "content_block_stop" {
			continue
		}
		var payload struct {
			Index        int `json:"index"`
			ContentBlock struct {
				Type string `json:"type"`
			} `json:"content_block"`
		}
		require.NoError(t, json.Unmarshal([]byte(dataLine), &payload))
		events = append(events, anthropicContentBlockEvent{
			event:     eventName,
			index:     payload.Index,
			blockType: payload.ContentBlock.Type,
		})
	}
	return events
}
