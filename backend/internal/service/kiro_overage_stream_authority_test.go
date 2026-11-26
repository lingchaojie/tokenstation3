//go:build unit

package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroOverageAuthorityPreservesTypedStreamingFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	until := time.Now().Add(time.Hour)
	repo := &kiroOverageAuthorityRepo{until: &until}
	account := newKiroCacheTransactionAccount(813, "WEBSEARCH-OVERAGE-PARK")
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search for current events"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &queuedHTTPUpstream{}
	transport := &recordingCaptureTransport{}
	svc := &GatewayService{
		accountRepo: repo, httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{
			Enabled: true, MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10,
		}}},
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	t.Cleanup(svc.capturePool.Stop)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	SetCaptureRequestedModel(c, parsed.Model)

	result, err := svc.Forward(c.Request.Context(), c, account, parsed)

	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, http.StatusPaymentRequired, failure.StatusCode)
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.RetryableOnSameAccount)
	require.False(t, failure.HasUpstreamHTTPResponse)
	require.Empty(t, rec.Body.String())
	require.Empty(t, upstream.requests)
	require.Empty(t, transport.Attempts())
}

func TestKiroWebSearchAdmissionAcrossProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const mcpEndpoint = "https://q.us-east-1.amazonaws.com/mcp"
	kiroWebSearchDescCache.Store(mcpEndpoint, "Search the web")
	t.Cleanup(func() { kiroWebSearchDescCache.Delete(mcpEndpoint) })

	const messages = `"messages":[{"role":"user","content":"search for current events"}]`
	for _, protocol := range []struct {
		name, path, input string
		buffered          bool
	}{
		{"messages", "/v1/messages", messages, false},
		{"chat_stream", "/v1/chat/completions", messages, false},
		{"chat_buffered", "/v1/chat/completions", messages, true},
		{"responses_stream", "/v1/responses", `"input":"search for current events"`, false},
		{"responses_buffered", "/v1/responses", `"input":"search for current events"`, true},
		{"compaction_stream", "/v1/responses", `"input":[{"type":"message","role":"user","content":"search for current events"},{"type":"compaction_trigger"}]`, true},
	} {
		for _, gate := range []struct {
			name string
			at   int
		}{
			{"first_mcp", 1},
			{"first_runtime", 2},
			{"second_mcp", 3},
			{"second_runtime", 4},
		} {
			t.Run(protocol.name+"/"+gate.name, func(t *testing.T) {
				stream := !protocol.buffered || protocol.name == "compaction_stream"
				tool := "web_search"
				if protocol.path == "/v1/messages" {
					tool = "web_search_20250305"
				}
				body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-6","stream":%t,"tools":[{"type":%q,"name":"web_search"}],%s}`, stream, tool, protocol.input))
				parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-6", Stream: stream}
				until := nextKiroMonthlyResetUTC(time.Now())
				repo := &kiroOverageAuthorityRepo{until: &until, parkOnCheck: gate.at}
				account := newKiroCacheTransactionAccount(821, "WEBSEARCH-GATE")
				var accepted bytes.Buffer
				accepted.Write(buildKiroEventStreamFrame(t, "assistantResponseEvent", map[string]any{"content": "Check another source."}))
				accepted.Write(buildKiroEventStreamFrame(t, "toolUseEvent", map[string]any{
					"toolUseId": "toolu_continue", "name": "remote_web_search", "input": map[string]any{"query": "recent news"}, "stop": true,
				}))
				accepted.Write(buildKiroEventStreamFrame(t, "messageMetadataEvent", map[string]any{
					"tokenUsage":  map[string]any{"uncachedInputTokens": 31, "outputTokens": 13, "cacheReadInputTokens": 7},
					"creditsUsed": 0.4,
				}))
				accepted.Write(buildKiroEventStreamFrame(t, "messageStopEvent", map[string]any{"stopReason": "tool_use"}))
				raw := accepted.Bytes()
				const mcpBody = `{"jsonrpc":"2.0","id":"test","result":{"content":[{"type":"text","text":"{\"results\":[]}"}]}}`
				upstream := &queuedHTTPUpstream{responses: []*http.Response{
					newJSONResponse(http.StatusOK, mcpBody),
					{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/vnd.amazon.eventstream"}}, Body: io.NopCloser(bytes.NewReader(raw))},
					newJSONResponse(http.StatusOK, mcpBody),
				}}
				transport := &recordingCaptureTransport{}
				svc := &GatewayService{
					accountRepo: repo, httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
					tlsFPProfileService: &TLSFingerprintProfileService{},
					cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{
						Enabled: true, MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10,
					}}},
					capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
				}
				t.Cleanup(svc.capturePool.Stop)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, protocol.path, bytes.NewReader(body))
				enableCaptureForTest(t, c)
				SetCaptureRequestedModel(c, parsed.Model)

				var result *ForwardResult
				var err error
				switch protocol.path {
				case "/v1/messages":
					result, err = svc.Forward(c.Request.Context(), c, account, parsed)
				case "/v1/chat/completions":
					result, err = svc.ForwardAsChatCompletions(c.Request.Context(), c, account, body, parsed)
				default:
					result, err = svc.ForwardAsResponses(c.Request.Context(), c, account, body, parsed)
				}

				require.Error(t, err)
				require.Equal(t, gate.at, repo.checks)
				require.Len(t, upstream.requests, gate.at-1)
				attempts := transport.Attempts()
				require.Len(t, attempts, gate.at-1, "the blocked request never owns a provider capture")
				if gate.at >= 3 {
					require.Equal(t, raw, attempts[1].ResponseBytes())
				}
				var failure *UpstreamFailoverError
				if gate.at < 3 || protocol.buffered {
					require.Nil(t, result)
					require.ErrorAs(t, err, &failure)
					require.Equal(t, http.StatusPaymentRequired, failure.StatusCode)
					require.Equal(t, GatewayFailureReason(kiroOverageLimitCode), failure.Reason)
					require.True(t, failure.ShouldRetryNextAccount())
					require.False(t, failure.RetryableOnSameAccount)
					require.False(t, failure.HasUpstreamHTTPResponse)
					require.Zero(t, failure.UpstreamHTTPStatus)
					require.Empty(t, rec.Body.String())
					return
				}
				require.NotErrorAs(t, err, &failure)
				require.NotNil(t, result)
				require.True(t, result.CaptureTerminalError)
				require.False(t, result.CaptureResponseComplete)
				require.Contains(t, rec.Body.String(), "Check another source.")
				require.Contains(t, rec.Body.String(), "Kiro monthly overage quota is exhausted")
				require.Equal(t, 31, result.Usage.InputTokens)
				require.Equal(t, 13, result.Usage.OutputTokens)
				require.Equal(t, 7, result.Usage.CacheReadInputTokens)
				require.InDelta(t, 0.4, result.Usage.KiroCredits, 1e-9)
			})
		}
	}
}
