//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rawCCHandlerFailoverUpstream struct {
	service.HTTPUpstream
	mu                   sync.Mutex
	accountIDs           []int64
	requestBodyByAccount map[int64][][]byte
	firstBody            string
	secondBody           string
}

func (u *rawCCHandlerFailoverUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	requestBody, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	if u.requestBodyByAccount == nil {
		u.requestBodyByAccount = make(map[int64][][]byte)
	}
	u.requestBodyByAccount[accountID] = append(u.requestBodyByAccount[accountID], append([]byte(nil), requestBody...))
	u.mu.Unlock()
	body := ""
	if accountID == 9201 {
		body = u.firstBody
		if body == "" {
			body = `data: {"id":"first-attempt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}` + "\n\n"
		}
	} else {
		body = u.secondBody
		if body == "" {
			body = strings.Join([]string{
				`data: {"id":"second-attempt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
				"",
				`data: {"id":"second-attempt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"recovered"},"finish_reason":null}]}`,
				"",
				`data: {"id":"second-attempt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
				"",
				"data: [DONE]",
				"",
			}, "\n")
		}
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"Content-Type": {"text/event-stream"},
		"X-Request-Id": {"request-account-" + strconv.FormatInt(accountID, 10)},
	}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func (u *rawCCHandlerFailoverUpstream) requestBodies(accountID int64) [][]byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	bodies := u.requestBodyByAccount[accountID]
	out := make([][]byte, len(bodies))
	for i := range bodies {
		out[i] = append([]byte(nil), bodies[i]...)
	}
	return out
}

func (u *rawCCHandlerFailoverUpstream) calls() []int64 {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]int64(nil), u.accountIDs...)
}

func TestOpenAIResponsesRawCCPreambleFailoverUsesSecondAccountWithoutLeakingFirstAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9200)
	newAccount := func(id int64, priority int) service.Account {
		return service.Account{
			ID: id, Name: "raw-cc", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
			Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
		}
	}
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{newAccount(9201, 1), newAccount(9202, 2)}}
	upstream := &rawCCHandlerFailoverUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", strings.NewReader(`{"model":"gpt-5.4","input":"hello","stream":true}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9203, GroupID: &groupID, User: &service.User{ID: 9204, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9204, Concurrency: 0})

	h.Responses(c)

	require.Equal(t, []int64{9201, 9202}, upstream.calls(), "handler must actually select a second account")
	wire := recorder.Body.String()
	require.NotContains(t, wire, "first-attempt", "staged first-attempt preamble must be discarded")
	require.Contains(t, wire, "second-attempt")
	require.Contains(t, wire, "recovered")
}

func TestOpenAIMessagesRawCCPreambleFailoverUsesSecondAccountWithoutLeakingFirstAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9210)
	newAccount := func(id int64, priority int) service.Account {
		return service.Account{
			ID: id, Name: "raw-cc-messages", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
			Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
		}
	}
	// The upstream fake keys the replayable first attempt on account 9201.
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{newAccount(9201, 1), newAccount(9212, 2)}}
	upstream := &rawCCHandlerFailoverUpstream{}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	gateway := service.NewOpenAIGatewayService(
		accountRepo, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, nil,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/messages", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true,"max_tokens":64}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9213, GroupID: &groupID, User: &service.User{ID: 9214, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9214, Concurrency: 0})

	h.Messages(c)

	require.Equal(t, []int64{9201, 9212}, upstream.calls(), "handler must actually select a second account")
	wire := recorder.Body.String()
	require.NotContains(t, wire, "first-attempt", "staged message_start from first attempt must be discarded")
	require.Contains(t, wire, "second-attempt")
	require.Contains(t, wire, "recovered")
}

type rawCCHandlerRunResult struct {
	recorder *httptest.ResponseRecorder
	captures []*service.CaptureRecord
	usages   []*service.UsageLog
}

type rawCCFailFirstWriteResponseWriter struct {
	gin.ResponseWriter
	failed bool
}

func (w *rawCCFailFirstWriteResponseWriter) Write(p []byte) (int, error) {
	if !w.failed {
		w.failed = true
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}

type rawCCHandlerUsageRepo struct {
	service.UsageLogRepository
	records chan<- *service.UsageLog
}

func (r *rawCCHandlerUsageRepo) Create(_ context.Context, log *service.UsageLog) (bool, error) {
	if r != nil && r.records != nil && log != nil {
		cloned := *log
		r.records <- &cloned
	}
	return true, nil
}

func runRawCCHandlerScenario(
	t *testing.T,
	endpoint string,
	upstream *rawCCHandlerFailoverUpstream,
	wrapWriter func(gin.ResponseWriter) gin.ResponseWriter,
) rawCCHandlerRunResult {
	t.Helper()
	groupID := int64(9290)
	newAccount := func(id int64, priority int) service.Account {
		return service.Account{
			ID: id, Name: "raw-cc-scenario", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
			Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
		}
	}
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{newAccount(9201, 1), newAccount(9202, 2)}}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 2
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settingService := newEnabledCaptureSettingService(t, cfg)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 2)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	t.Cleanup(capturePool.Stop)
	usageRecords := make(chan *service.UsageLog, 2)
	usageRepo := &rawCCHandlerUsageRepo{records: usageRecords}
	gateway := service.NewOpenAIGatewayService(
		accountRepo, usageRepo, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, upstream,
		&service.DeferredService{}, nil, nil, nil, nil, nil, settingService, nil, capturePool,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if wrapWriter != nil {
		c.Writer = wrapWriter(c.Writer)
	}
	requestBody := `{"model":"gpt-5.4","input":"hello","stream":true}`
	path := "/openai/v1/responses"
	if endpoint == "messages" {
		requestBody = `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":true,"max_tokens":64}`
		path = "/openai/v1/messages"
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9293, GroupID: &groupID, User: &service.User{ID: 9294, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9294, Concurrency: 0})

	if endpoint == "messages" {
		h.Messages(c)
	} else {
		h.Responses(c)
	}
	capturePool.Stop()

	var captures []*service.CaptureRecord
	for {
		select {
		case capture := <-captureRecords:
			captures = append(captures, capture)
		default:
			goto capturesDrained
		}
	}

capturesDrained:
	var usages []*service.UsageLog
	for {
		select {
		case usage := <-usageRecords:
			usages = append(usages, usage)
		default:
			return rawCCHandlerRunResult{recorder: recorder, captures: captures, usages: usages}
		}
	}
}

func TestOpenAIRawCCHandlerRetriesOnlyPreSemanticEmptyOrMalformedAttempts(t *testing.T) {
	const establishedFirstOutputStageLimit = 8 * 1024 * 1024
	firstSemantic := strings.Repeat("x", establishedFirstOutputStageLimit-256)
	oversizedConvertedSemantic := `data: {"id":"first-overflow","choices":[{"delta":{"content":"` + firstSemantic + `"}}]}` + "\n\n"
	require.Less(t, len(strings.TrimSuffix(oversizedConvertedSemantic, "\n\n")), establishedFirstOutputStageLimit,
		"fixture must overflow converted staging rather than the scanner-token guard")

	for _, endpoint := range []string{"responses", "messages"} {
		for _, scenario := range []struct {
			name string
			sse  string
		}{
			{
				name: "empty delta EOF",
				sse:  "data: {\"id\":\"first-empty\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"reasoning_content\":\"\",\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"\",\"arguments\":\"\"}}]}}]}\n\n",
			},
			{name: "malformed then done", sse: "data: {not-json}\n\ndata: [DONE]\n\n"},
			{name: "oversized first semantic converted event", sse: oversizedConvertedSemantic},
		} {
			t.Run(endpoint+"/"+scenario.name, func(t *testing.T) {
				upstream := &rawCCHandlerFailoverUpstream{firstBody: scenario.sse}
				got := runRawCCHandlerScenario(t, endpoint, upstream, nil)
				require.Equal(t, []int64{9201, 9202}, upstream.calls())
				require.NotContains(t, got.recorder.Body.String(), "first-empty")
				require.Contains(t, got.recorder.Body.String(), "second-attempt")
				require.Len(t, got.captures, 1, "only the committed second attempt may be captured")
				capture := got.captures[0]
				require.NotContains(t, string(capture.RawResponse), "first-empty")
				require.Contains(t, string(capture.RawResponse), "second-attempt")
				finalBodies := upstream.requestBodies(9202)
				require.Len(t, finalBodies, 1)
				require.Equal(t, finalBodies[0], capture.RawRequest)
				require.Equal(t, service.HashUsageRequestPayload(finalBodies[0]), hashFinalOpenAIUpstreamRequest(&service.OpenAIForwardResult{UpstreamRequest: capture.RawRequest}, nil))
				require.Len(t, got.usages, 1, "only the committed second attempt may persist usage")
				require.Equal(t, int64(9202), got.usages[0].AccountID)
				require.Equal(t, 2, got.usages[0].InputTokens)
				require.Equal(t, 1, got.usages[0].OutputTokens)
				require.Contains(t, got.recorder.Body.String(), `"input_tokens":2`)
				require.Contains(t, got.recorder.Body.String(), `"output_tokens":1`)
			})
		}
	}
}

func TestOpenAIRawCCHandlerZeroByteCommitFailureDoesNotLeakAttemptHeaders(t *testing.T) {
	largeID := strings.Repeat("s", 65*1024)
	first := strings.Join([]string{
		`data: {"id":"` + largeID + `","choices":[{"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"first-write","choices":[{"delta":{"content":"hello"}}]}`,
		"",
		"data: {not-json}",
		"",
	}, "\n")
	for _, endpoint := range []string{"responses", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: first}
			got := runRawCCHandlerScenario(t, endpoint, upstream, func(w gin.ResponseWriter) gin.ResponseWriter {
				return &rawCCFailFirstWriteResponseWriter{ResponseWriter: w}
			})

			require.Equal(t, []int64{9201, 9202}, upstream.calls())
			require.Contains(t, got.recorder.Body.String(), "second-attempt")
			require.Equal(t, []string{"request-account-9202"}, got.recorder.Header().Values("X-Request-Id"),
				"the successful account must exclusively own downstream attempt headers")
		})
	}
}

func TestOpenAIRawCCHandlerCommittedMalformedAttemptDoesNotReplay(t *testing.T) {
	for _, endpoint := range []string{"responses", "messages"} {
		t.Run(endpoint, func(t *testing.T) {
			first := "data: {\"id\":\"first-committed\",\"choices\":[{\"delta\":{\"content\":\"committed\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":0}}\n\ndata: {not-json}\n\ndata: [DONE]\n\n"
			upstream := &rawCCHandlerFailoverUpstream{firstBody: first}
			got := runRawCCHandlerScenario(t, endpoint, upstream, nil)
			require.Equal(t, []int64{9201}, upstream.calls())
			require.Contains(t, got.recorder.Body.String(), "committed")
			require.NotContains(t, got.recorder.Body.String(), "recovered")
			require.Len(t, got.captures, 1)
			require.Equal(t, first, string(got.captures[0].RawResponse))
			firstBodies := upstream.requestBodies(9201)
			require.Len(t, firstBodies, 1)
			require.Equal(t, firstBodies[0], got.captures[0].RawRequest)
			require.Len(t, got.usages, 1)
			require.Equal(t, int64(9201), got.usages[0].AccountID)
		})
	}
}
