//go:build unit

package handler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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
	firstDelayedTail     string
	firstTailDelay       time.Duration
	requestIDPerCall     bool
	status               int
}

type openAIChatRetryDelayCaptureUpstream struct {
	service.HTTPUpstream
	calls chan struct{}
}

type openAIChatFinalHTTPErrorCaptureUpstream struct {
	service.HTTPUpstream
	body string
}

type openAIChatLocalErrorCaptureUpstream struct {
	service.HTTPUpstream
}

func (*openAIChatLocalErrorCaptureUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return nil, fmt.Errorf("synthetic transport failure")
}

func (u *openAIChatFinalHTTPErrorCaptureUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusUnprocessableEntity,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(u.body)),
	}, nil
}

func (u *openAIChatRetryDelayCaptureUpstream) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.calls <- struct{}{}
	return &http.Response{
		StatusCode: http.StatusBadGateway,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"retry this account"}}`)),
	}, nil
}

func TestOpenAIChatCompletionsAbortsAttemptBeforeSameAccountRetryDelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9380)
	account := service.Account{
		ID: 9381, Name: "openai-chat-retry-delay", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{
			"api_key":                      "test",
			"base_url":                     "https://api.example.test",
			"pool_mode":                    true,
			"pool_mode_retry_count":        float64(1),
			"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
		},
		Extra: map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	terminals := make(chan string, 4)
	capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(make(chan *service.CaptureRecord, 1), terminals)
	t.Cleanup(capturePool.Stop)
	upstream := &openAIChatRetryDelayCaptureUpstream{calls: make(chan struct{}, 2)}
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil, capturePool,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`)).WithContext(requestCtx)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9382, GroupID: &groupID, User: &service.User{ID: 9383, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9383})

	done := make(chan struct{})
	go func() {
		h.ChatCompletions(c)
		close(done)
	}()
	select {
	case <-upstream.calls:
	case <-time.After(time.Second):
		cancel()
		<-done
		t.Fatal("first upstream call did not start")
	}
	select {
	case terminal := <-terminals:
		require.Equal(t, "abort", terminal)
	case <-time.After(250 * time.Millisecond):
		cancel()
		<-done
		t.Fatal("failed typed attempt remained open during the same-account retry delay")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not stop after request cancellation")
	}
	require.Empty(t, terminals, "request defer must not emit a duplicate terminal")
}

func TestOpenAIResponsesAndMessagesAbortAttemptBeforeSameAccountRetryDelay(t *testing.T) {
	for _, tt := range []struct {
		name string
		path string
		body string
		run  func(*OpenAIGatewayHandler, *gin.Context)
	}{
		{
			name: "responses", path: "/openai/v1/responses",
			body: `{"model":"gpt-5.4","input":"hello","stream":false}`,
			run:  func(h *OpenAIGatewayHandler, c *gin.Context) { h.Responses(c) },
		},
		{
			name: "messages", path: "/openai/v1/messages",
			body: `{"model":"gpt-5.4","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`,
			run:  func(h *OpenAIGatewayHandler, c *gin.Context) { h.Messages(c) },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			groupID := int64(9384)
			account := service.Account{
				ID: 9385, Name: "openai-handler-retry-delay", Platform: service.PlatformOpenAI,
				Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
				Credentials: map[string]any{
					"api_key":                      "test",
					"base_url":                     "https://api.example.test",
					"pool_mode":                    true,
					"pool_mode_retry_count":        float64(1),
					"pool_mode_retry_status_codes": []any{float64(http.StatusBadGateway)},
				},
				Extra: map[string]any{"openai_passthrough": true},
			}
			cfg := &config.Config{RunMode: config.RunModeSimple}
			cfg.Default.RateMultiplier = 1
			cfg.Security.URLAllowlist.Enabled = false
			cfg.Gateway.MaxAccountSwitches = 1
			cfg.Gateway.Capture.Enabled = true
			cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
			settings := newEnabledCaptureSettingService(t, cfg)
			billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
			t.Cleanup(billing.Stop)
			terminals := make(chan string, 4)
			capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(make(chan *service.CaptureRecord, 1), terminals)
			t.Cleanup(capturePool.Stop)
			upstream := &openAIChatRetryDelayCaptureUpstream{calls: make(chan struct{}, 2)}
			gateway := service.NewOpenAIGatewayService(
				&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
				service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil, capturePool,
			)
			h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			requestCtx, cancel := context.WithCancel(context.Background())
			defer cancel()
			c.Request = httptest.NewRequest(http.MethodPost, tt.path, strings.NewReader(tt.body)).WithContext(requestCtx)
			c.Request.Header.Set("Content-Type", "application/json")
			c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
				ID: 9386, GroupID: &groupID, User: &service.User{ID: 9387, Status: service.StatusActive},
				Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1, AllowMessagesDispatch: true},
			})
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9387})

			done := make(chan struct{})
			go func() {
				tt.run(h, c)
				close(done)
			}()
			select {
			case <-upstream.calls:
			case <-time.After(time.Second):
				cancel()
				<-done
				t.Fatal("first upstream call did not start")
			}
			select {
			case terminal := <-terminals:
				require.Equal(t, "abort", terminal)
			case <-time.After(250 * time.Millisecond):
				cancel()
				<-done
				t.Fatal("failed typed attempt remained open during the same-account retry delay")
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("handler did not stop after request cancellation")
			}
			require.Empty(t, terminals, "request defer must not emit a duplicate terminal")
		})
	}
}

func TestOpenAIChatCompletionsCommitsPreCommitDisconnectExactlyOnce(t *testing.T) {
	providerSSE := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_disconnect","model":"gpt-5.4","status":"in_progress","output":[]}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"observed"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"resp_disconnect","object":"response","model":"gpt-5.4","status":"completed","output":[{"type":"message","id":"msg_disconnect","role":"assistant","status":"completed","content":[{"type":"output_text","text":"observed"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")
	upstream := &rawCCHandlerFailoverUpstream{firstBody: providerSSE}
	got := runRawCCHandlerScenario(t, "native_chat", upstream, func(w gin.ResponseWriter) gin.ResponseWriter {
		return &rawCCFailFirstWriteResponseWriter{ResponseWriter: w}
	})

	require.Equal(t, []int64{9201}, upstream.calls(), "a client disconnect must not replay the provider request")
	require.Len(t, got.captures, 1)
	require.Equal(t, providerSSE, string(got.captures[0].RawResponse))
	require.Equal(t, "pre_commit_disconnect", got.captures[0].StopReason)
	require.True(t, got.captures[0].Truncated, "a pre-commit disconnect is necessarily incomplete")
}

func TestOpenAIChatCompletionsCommitsFinalHTTPErrorExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9390)
	account := service.Account{
		ID: 9391, Name: "openai-chat-final-error", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	records := make(chan *service.CaptureRecord, 1)
	terminals := make(chan string, 2)
	capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(records, terminals)
	t.Cleanup(capturePool.Stop)
	providerBody := `{"error":{"message":"final request error"}}`
	upstream := &openAIChatFinalHTTPErrorCaptureUpstream{body: providerBody}
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil, capturePool,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9392, GroupID: &groupID, User: &service.User{ID: 9393, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9393})

	h.ChatCompletions(c)

	terminal := <-terminals
	require.Equal(t, "commit", terminal)
	select {
	case terminal := <-terminals:
		t.Fatalf("final HTTP error published duplicate terminal event %q", terminal)
	default:
	}
	select {
	case record := <-records:
		require.Equal(t, http.StatusUnprocessableEntity, record.HTTPStatus)
		require.Equal(t, providerBody, string(record.RawResponse))
		require.False(t, record.Truncated, "a fully read final provider HTTP error is complete")
	default:
		t.Fatal("final HTTP error did not publish its typed capture")
	}
}

func TestOpenAIChatCompletionsAbortsLocalErrorExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	groupID := int64(9395)
	account := service.Account{
		ID: 9396, Name: "openai-chat-local-error", Platform: service.PlatformOpenAI,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions)},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	records := make(chan *service.CaptureRecord, 1)
	terminals := make(chan string, 2)
	capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(records, terminals)
	t.Cleanup(capturePool.Stop)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}, nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, &openAIChatLocalErrorCaptureUpstream{}, &service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil, capturePool,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billing, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9397, GroupID: &groupID, User: &service.User{ID: 9398, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9398})

	h.ChatCompletions(c)

	require.Equal(t, "abort", <-terminals)
	select {
	case terminal := <-terminals:
		t.Fatalf("local error published duplicate terminal event %q", terminal)
	default:
	}
	select {
	case record := <-records:
		t.Fatalf("local error unexpectedly committed capture: %+v", record)
	default:
	}
	require.Equal(t, http.StatusBadGateway, recorder.Code)
}

func (u *rawCCHandlerFailoverUpstream) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	requestBody, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.accountIDs = append(u.accountIDs, accountID)
	callNumber := len(u.accountIDs)
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
	contentType := "text/event-stream"
	if !strings.Contains(body, "data:") && !strings.Contains(body, "event:") {
		contentType = "application/json"
	}
	requestID := "request-account-" + strconv.FormatInt(accountID, 10)
	if u.requestIDPerCall {
		requestID = "request-call-" + strconv.Itoa(callNumber)
	}
	responseBody := io.ReadCloser(io.NopCloser(strings.NewReader(body)))
	contentLength := int64(len(body))
	if accountID == 9201 && u.firstDelayedTail != "" {
		reader, writer := io.Pipe()
		responseBody = reader
		contentLength = -1
		tail := u.firstDelayedTail
		delay := u.firstTailDelay
		go func() {
			if _, err := writer.Write([]byte(body)); err != nil {
				_ = writer.CloseWithError(err)
				return
			}
			if delay > 0 {
				time.Sleep(delay)
			}
			if _, err := writer.Write([]byte(tail)); err != nil {
				_ = writer.CloseWithError(err)
				return
			}
			_ = writer.Close()
		}()
	}
	status := u.status
	if status == 0 {
		status = http.StatusOK
	}
	return &http.Response{StatusCode: status, ContentLength: contentLength, Header: http.Header{
		"Content-Type": {contentType},
		"X-Request-Id": {requestID},
	}, Body: responseBody, Request: req}, nil
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
	clientStreamOverride ...bool,
) rawCCHandlerRunResult {
	t.Helper()
	groupID := int64(9290)
	nativeResponses := strings.HasPrefix(endpoint, "native_")
	passthroughResponses := strings.HasPrefix(endpoint, "passthrough_")
	newAccount := func(id int64, priority int) service.Account {
		extra := map[string]any{}
		if passthroughResponses {
			extra["openai_passthrough"] = true
		} else if !nativeResponses {
			extra[openai_compat.ExtraKeyResponsesMode] = string(openai_compat.ResponsesSupportModeForceChatCompletions)
		}
		return service.Account{
			ID: id, Name: "raw-cc-scenario", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
			Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
			Extra:       extra,
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
	clientStream := true
	if len(clientStreamOverride) > 0 {
		clientStream = clientStreamOverride[0]
	}
	streamJSON := strconv.FormatBool(clientStream)
	requestBody := `{"model":"gpt-5.4","input":"hello","stream":` + streamJSON + `}`
	path := "/openai/v1/responses"
	if endpoint == "messages" || endpoint == "native_messages" {
		requestBody = `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":` + streamJSON + `,"max_tokens":64}`
		path = "/openai/v1/messages"
	} else if endpoint == "chat" || endpoint == "native_chat" {
		requestBody = `{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":` + streamJSON + `}`
		path = "/openai/v1/chat/completions"
	}
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9293, GroupID: &groupID, User: &service.User{ID: 9294, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, AllowMessagesDispatch: true, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9294, Concurrency: 0})

	if endpoint == "messages" || endpoint == "native_messages" {
		h.Messages(c)
	} else if endpoint == "chat" || endpoint == "native_chat" {
		h.ChatCompletions(c)
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

func TestOpenAIResponsesOfficialNullableUsagePreambleDoesNotReplay(t *testing.T) {
	body := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"nullable-usage","status":"in_progress","output":[],"usage":null}}`,
		``,
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"nullable-message","type":"message","role":"assistant","status":"in_progress","content":[]}}`,
		``,
		`data: {"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"nullable-message","part":{"type":"output_text","text":""}}`,
		``,
		`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"nullable-message","delta":"accepted-first"}`,
		``,
		`data: {"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"nullable-message","text":"accepted-first"}`,
		``,
		`data: {"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"nullable-message","part":{"type":"output_text","text":"accepted-first"}}`,
		``,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"nullable-message","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"accepted-first"}]}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"nullable-usage","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"accepted-first"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		``,
	}, "\n")
	for _, endpoint := range []string{"native_responses", "passthrough_responses", "native_messages", "native_chat"} {
		t.Run(endpoint, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: body}
			got := runRawCCHandlerScenario(t, endpoint, upstream, nil)
			require.Equal(t, []int64{9201}, upstream.calls())
			require.Contains(t, got.recorder.Body.String(), "accepted-first")
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(body), got.captures[0].RawResponse)
			require.Len(t, got.usages, 1)
			require.Equal(t, int64(9201), got.usages[0].AccountID)
		})
	}
}

func TestOpenAIHandlersRejectNestedFailedResponsesTerminalBeforeCommit(t *testing.T) {
	second := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"second-response","status":"in_progress"}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"recovered"}`,
		"",
		`data: {"type":"response.completed","response":{"id":"second-response","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"recovered"}]}],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
	}, "\n")
	for _, endpoint := range []string{"native_responses", "passthrough_responses", "native_messages", "native_chat"} {
		for _, status := range []string{"failed", "incomplete", "cancelled", "queued", "in_progress"} {
			t.Run(endpoint+"/"+status, func(t *testing.T) {
				first := `data: {"type":"response.completed","response":{"id":"first-response","status":"` + status + `","output":[],"error":{"message":"first must fail"}}}` + "\n\n"
				upstream := &rawCCHandlerFailoverUpstream{firstBody: first, secondBody: second}
				got := runRawCCHandlerScenario(t, endpoint, upstream, nil)
				calls := upstream.calls()
				require.NotEmpty(t, calls)
				require.Equal(t, int64(9202), calls[len(calls)-1])
				require.NotContains(t, got.recorder.Body.String(), "first-response")
				require.Contains(t, got.recorder.Body.String(), "recovered")
				require.Len(t, got.captures, 1)
				require.Equal(t, []byte(second), got.captures[0].RawResponse)
				require.Len(t, got.usages, 1)
				require.Equal(t, int64(9202), got.usages[0].AccountID)
			})
		}
	}
}

func TestOpenAIHandlersAcceptOfficialTopLevelErrorAndFailOver(t *testing.T) {
	second := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"second-response","status":"in_progress"}}`,
		``,
		`data: {"type":"response.output_text.delta","delta":"recovered"}`,
		``,
		`data: {"type":"response.completed","response":{"id":"second-response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		``,
	}, "\n")
	first := "event: error\n" + `data: {"type":"error","code":"server_is_overloaded","message":"provider overloaded","param":null}` + "\n\n"
	for _, endpoint := range []string{"native_responses", "passthrough_responses", "native_messages"} {
		t.Run(endpoint, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: first, secondBody: second}
			got := runRawCCHandlerScenario(t, endpoint, upstream, nil)
			calls := upstream.calls()
			require.NotEmpty(t, calls)
			require.Equal(t, int64(9202), calls[len(calls)-1])
			require.NotContains(t, got.recorder.Body.String(), "provider overloaded")
			require.Contains(t, got.recorder.Body.String(), "recovered")
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(second), got.captures[0].RawResponse)
			require.Len(t, got.usages, 1)
			require.Equal(t, int64(9202), got.usages[0].AccountID)
		})
	}
}

func TestOpenAIResponsesEventOnlyFrameDoesNotContaminateNextEvent(t *testing.T) {
	providerBody := strings.Join([]string{
		`event: response.failed`,
		``,
		`data: {"type":"response.created","response":{"id":"first-response","status":"in_progress"}}`,
		``,
		`data: {"type":"response.completed","response":{"id":"first-response","status":"completed","output":[],"usage":{"input_tokens":2,"output_tokens":0,"total_tokens":2}}}`,
		``,
	}, "\n")
	for _, endpoint := range []string{"native_responses", "passthrough_responses"} {
		t.Run(endpoint, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: providerBody}
			got := runRawCCHandlerScenario(t, endpoint, upstream, nil)
			calls := upstream.calls()
			require.NotEmpty(t, calls)
			for _, accountID := range calls {
				require.Equal(t, int64(9201), accountID)
			}
			require.NotContains(t, got.recorder.Body.String(), "event: response.failed")
			require.Contains(t, got.recorder.Body.String(), "first-response")
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(providerBody), got.captures[0].RawResponse)
			require.Len(t, got.usages, 1)
			require.Equal(t, int64(9201), got.usages[0].AccountID)
		})
	}
}

func TestOpenAIRawCCAllowsContentFilterTerminalAndTrailingAzureAnnotation(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"prompt_filter_results":[{"prompt_index":0,"content_filter_results":{"hate":{"filtered":false}}}],"choices":[],"usage":null}`,
		``,
		`data: {"id":"filtered","choices":[{"index":0,"content_filter_results":{"hate":{"filtered":false}},"content_filter_offsets":{"check_offset":0,"start_offset":0,"end_offset":0},"finish_reason":null}]}`,
		``,
		`data: {"id":"filtered","choices":[{"index":0,"delta":{"role":"assistant","content":null},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":0}}`,
		``,
		`data: {"id":"filtered","choices":[{"index":0,"delta":{},"content_filter_results":{"hate":{"filtered":true}},"content_filter_offsets":{"check_offset":0,"start_offset":0,"end_offset":0},"finish_reason":null}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	bufferedBody := `{"id":"filtered","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":0}}`
	for _, endpoint := range []string{"responses", "messages", "chat"} {
		for _, scenario := range []struct {
			name   string
			body   string
			stream bool
		}{
			{name: "stream", body: streamBody, stream: true},
			{name: "buffered", body: bufferedBody, stream: false},
		} {
			t.Run(endpoint+"/"+scenario.name, func(t *testing.T) {
				upstream := &rawCCHandlerFailoverUpstream{firstBody: scenario.body}
				got := runRawCCHandlerScenario(t, endpoint, upstream, nil, scenario.stream)
				require.Equal(t, []int64{9201}, upstream.calls())
				require.Len(t, got.captures, 1)
				require.Equal(t, []byte(scenario.body), got.captures[0].RawResponse)
				require.Len(t, got.usages, 1)
				require.Equal(t, int64(9201), got.usages[0].AccountID)
			})
		}
	}
}

func TestOpenAIChatCompatResponsesPreservesProviderRefusal(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"id":"refused","choices":[{"index":0,"delta":{"role":"assistant","refusal":"policy "},"finish_reason":null}]}`,
		``,
		`data: {"id":"refused","choices":[{"index":0,"delta":{"refusal":"blocked"},"finish_reason":null}]}`,
		``,
		`data: {"id":"refused","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":0}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	bufferedBody := `{"id":"refused","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"refusal":"policy blocked"},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":0}}`
	for _, scenario := range []struct {
		name   string
		body   string
		stream bool
	}{
		{name: "stream", body: streamBody, stream: true},
		{name: "buffered", body: bufferedBody, stream: false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: scenario.body}
			got := runRawCCHandlerScenario(t, "responses", upstream, nil, scenario.stream)
			require.Equal(t, []int64{9201}, upstream.calls())
			require.Contains(t, got.recorder.Body.String(), `"type":"refusal"`)
			require.Contains(t, got.recorder.Body.String(), `"refusal":"policy blocked"`)
			if scenario.stream {
				require.Contains(t, got.recorder.Body.String(), `"type":"response.refusal.delta"`)
				require.Contains(t, got.recorder.Body.String(), `"type":"response.refusal.done"`)
				require.NotContains(t, got.recorder.Body.String(), `"type":"response.output_text.delta"`)
			}
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(scenario.body), got.captures[0].RawResponse)
			require.Len(t, got.usages, 1)
			require.Equal(t, int64(9201), got.usages[0].AccountID)
		})
	}
}

func TestOpenAIChatCompatResponsesPreservesMixedTextAndRefusal(t *testing.T) {
	streamBodies := map[string]string{
		"text_then_refusal": strings.Join([]string{
			`data: {"id":"mixed","choices":[{"index":0,"delta":{"role":"assistant","content":"visible"},"finish_reason":null}]}`,
			``,
			`data: {"id":"mixed","choices":[{"index":0,"delta":{"refusal":"blocked"},"finish_reason":null}]}`,
			``,
			`data: {"id":"mixed","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
		"refusal_then_text": strings.Join([]string{
			`data: {"id":"mixed","choices":[{"index":0,"delta":{"role":"assistant","refusal":"blocked"},"finish_reason":null}]}`,
			``,
			`data: {"id":"mixed","choices":[{"index":0,"delta":{"content":"visible"},"finish_reason":null}]}`,
			``,
			`data: {"id":"mixed","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
			``,
			`data: [DONE]`,
			``,
		}, "\n"),
	}
	for name, body := range streamBodies {
		t.Run("stream/"+name, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: body}
			got := runRawCCHandlerScenario(t, "responses", upstream, nil, true)
			require.Equal(t, []int64{9201}, upstream.calls())
			require.Contains(t, got.recorder.Body.String(), `"type":"response.output_text.delta"`)
			require.Contains(t, got.recorder.Body.String(), `"type":"response.refusal.delta"`)
			require.Contains(t, got.recorder.Body.String(), `"text":"visible"`)
			require.Contains(t, got.recorder.Body.String(), `"refusal":"blocked"`)
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(body), got.captures[0].RawResponse)
		})
	}

	t.Run("buffered", func(t *testing.T) {
		body := `{"id":"mixed","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"visible","refusal":"blocked"},"finish_reason":"content_filter"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`
		upstream := &rawCCHandlerFailoverUpstream{firstBody: body}
		got := runRawCCHandlerScenario(t, "responses", upstream, nil, false)
		require.Equal(t, []int64{9201}, upstream.calls())
		require.Contains(t, got.recorder.Body.String(), `"type":"output_text"`)
		require.Contains(t, got.recorder.Body.String(), `"text":"visible"`)
		require.Contains(t, got.recorder.Body.String(), `"type":"refusal"`)
		require.Contains(t, got.recorder.Body.String(), `"refusal":"blocked"`)
		require.Len(t, got.captures, 1)
		require.Equal(t, []byte(body), got.captures[0].RawResponse)
	})
}

func TestOpenAIRawCCAllowsOfficialRefusalWithoutAccountReplay(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"id":"first-refusal","choices":[{"index":0,"delta":{"role":"assistant","refusal":"blocked by policy"},"finish_reason":null}]}`,
		``,
		`data: {"id":"first-refusal","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":0}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	bufferedBody := `{"id":"first-refusal","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"refusal":"blocked by policy"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":0}}`
	for _, endpoint := range []string{"responses", "messages", "chat"} {
		for _, scenario := range []struct {
			name   string
			body   string
			stream bool
		}{
			{name: "stream", body: streamBody, stream: true},
			{name: "buffered", body: bufferedBody, stream: false},
		} {
			t.Run(endpoint+"/"+scenario.name, func(t *testing.T) {
				upstream := &rawCCHandlerFailoverUpstream{firstBody: scenario.body}
				got := runRawCCHandlerScenario(t, endpoint, upstream, nil, scenario.stream)
				require.Equal(t, []int64{9201}, upstream.calls())
				require.Contains(t, got.recorder.Body.String(), "blocked by policy")
				require.Len(t, got.captures, 1)
				require.Equal(t, []byte(scenario.body), got.captures[0].RawResponse)
				require.Len(t, got.usages, 1)
				require.Equal(t, int64(9201), got.usages[0].AccountID)
			})
		}
	}
}

func TestOpenAIRawCCAudioIsNativeOnlyAndCompatibilityBridgesFailClosed(t *testing.T) {
	streamBody := strings.Join([]string{
		`data: {"id":"first-audio","choices":[{"index":0,"delta":{"role":"assistant","audio":{"id":"audio-1","data":"YWJj","transcript":"hello","expires_at":4102444800}},"finish_reason":null}]}`,
		``,
		`data: {"id":"first-audio","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	bufferedBody := `{"id":"first-audio","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":null,"audio":{"id":"audio-1","data":"YWJj","transcript":"hello","expires_at":4102444800}},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`
	secondBuffered := `{"id":"second-attempt","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"recovered"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`
	for _, scenario := range []struct {
		name   string
		body   string
		second string
		stream bool
	}{
		{name: "stream", body: streamBody, stream: true},
		{name: "buffered", body: bufferedBody, second: secondBuffered, stream: false},
	} {
		t.Run("native_chat/"+scenario.name, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: scenario.body}
			got := runRawCCHandlerScenario(t, "chat", upstream, nil, scenario.stream)
			require.Equal(t, []int64{9201}, upstream.calls())
			require.Contains(t, got.recorder.Body.String(), "audio-1")
			require.Len(t, got.captures, 1)
			require.Equal(t, []byte(scenario.body), got.captures[0].RawResponse)
			require.Len(t, got.usages, 1)
			require.Equal(t, int64(9201), got.usages[0].AccountID)
		})
		for _, endpoint := range []string{"responses", "messages"} {
			t.Run(endpoint+"/"+scenario.name, func(t *testing.T) {
				upstream := &rawCCHandlerFailoverUpstream{firstBody: scenario.body, secondBody: scenario.second}
				got := runRawCCHandlerScenario(t, endpoint, upstream, nil, scenario.stream)
				calls := upstream.calls()
				require.NotEmpty(t, calls)
				require.Equal(t, int64(9202), calls[len(calls)-1])
				require.NotContains(t, got.recorder.Body.String(), "audio-1")
				require.Contains(t, got.recorder.Body.String(), "recovered")
				require.Len(t, got.captures, 1)
				require.Contains(t, string(got.captures[0].RawResponse), "recovered")
				require.Len(t, got.usages, 1)
				require.Equal(t, int64(9202), got.usages[0].AccountID)
			})
		}
	}
}

func TestOpenAIRawCCFirstSemanticChunkDoesNotWaitForTerminalOrOverflowStage(t *testing.T) {
	const establishedFirstOutputStageLimit = 8 * 1024 * 1024
	// Keep the first semantic SSE frame just under the staging ceiling while
	// making the complete response cross it once the terminal frames arrive.
	firstSemantic := strings.Repeat("x", establishedFirstOutputStageLimit-192)
	first := strings.Join([]string{
		`data: {"id":"large","choices":[{"index":0,"delta":{"content":"` + firstSemantic + `"},"finish_reason":null}]}`,
		``,
		`data: {"id":"large","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n")
	require.Greater(t, len(first), establishedFirstOutputStageLimit)
	upstream := &rawCCHandlerFailoverUpstream{firstBody: first}
	got := runRawCCHandlerScenario(t, "chat", upstream, nil)
	require.Equal(t, []int64{9201}, upstream.calls())
	require.Equal(t, http.StatusOK, got.recorder.Code)
	require.Contains(t, got.recorder.Body.String(), `"completion_tokens":1`)
	require.Len(t, got.captures, 1)
	require.Len(t, got.usages, 1)
}

func TestOpenAITypedNativeAndPassthroughBoundedHTTPErrorCommitsIncomplete(t *testing.T) {
	const functionalErrorLimit = 512 << 10
	providerBody := `{"error":{"type":"invalid_request_error","message":"` + strings.Repeat("x", functionalErrorLimit) + `"}}`
	for _, endpoint := range []string{"native_responses", "passthrough_responses"} {
		t.Run(endpoint, func(t *testing.T) {
			upstream := &rawCCHandlerFailoverUpstream{firstBody: providerBody, status: http.StatusUnprocessableEntity}
			got := runRawCCHandlerScenario(t, endpoint, upstream, nil, false)
			require.Equal(t, []int64{9201}, upstream.calls())
			require.Len(t, got.captures, 1, "a real non-failover provider HTTP error must commit its typed attempt")
			require.Equal(t, []byte(providerBody[:functionalErrorLimit]), got.captures[0].RawResponse)
			require.True(t, got.captures[0].Truncated, "a bounded provider-error prefix cannot be finalized complete")
			require.Equal(t, http.StatusUnprocessableEntity, got.captures[0].HTTPStatus)
		})
	}
}

func TestOpenAIRawCCTerminalCaptureKeepsOnlyNaturallyReadBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const providerBody = "toolong"
	groupID := int64(9350)
	account := service.Account{
		ID: 9351, Name: "raw-cc-functional-limit", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: 1,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://api.example.test"},
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesMode: string(openai_compat.ResponsesSupportModeForceChatCompletions),
		},
	}
	accountRepo := &openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{account}}
	// This account ID uses the fake's second-body branch. Keep every retry on the
	// same invalid provider response so the final exhausted attempt is the one
	// whose capture-drain behavior is asserted below.
	upstream := &rawCCHandlerFailoverUpstream{secondBody: providerBody}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = 1
	cfg.Gateway.UpstreamResponseReadMaxBytes = 3
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newTerminalOnlyCaptureSettingService(t, cfg)
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
		&service.DeferredService{}, nil, nil, nil, nil, nil, settings, nil, capturePool,
	)
	h := NewOpenAIGatewayHandler(gateway, service.NewConcurrencyService(nil), billingCache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, capturePool)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(`{"model":"gpt-5.4","messages":[{"role":"user","content":"hello"}],"stream":false}`))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9352, GroupID: &groupID, User: &service.User{ID: 9353, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9353})

	h.ChatCompletions(c)
	capturePool.Stop()
	t.Logf("calls=%v status=%d response=%q captures=%d usages=%d", upstream.calls(), recorder.Code, recorder.Body.String(), len(captureRecords), len(usageRecords))

	select {
	case capture := <-captureRecords:
		require.Equal(t, []byte(providerBody[:4]), capture.RawResponse, "capture must not drain bytes beyond the functional limit+1 read")
		require.True(t, capture.Truncated, "the provider response was not consumed to completion")
		require.Equal(t, http.StatusOK, capture.HTTPStatus)
		require.Equal(t, "request-account-9351", capture.RequestID)
	default:
		require.Fail(t, "final HTTP 200 invalid provider attempt must be captured")
	}
	select {
	case extra := <-captureRecords:
		require.Failf(t, "capture must be exact-once", "unexpected extra capture: %+v", extra)
	default:
	}
	select {
	case usage := <-usageRecords:
		require.Failf(t, "failed provider attempt must not be billed", "unexpected usage: %+v", usage)
	default:
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
