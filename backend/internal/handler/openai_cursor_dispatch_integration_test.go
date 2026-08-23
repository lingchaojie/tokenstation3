//go:build unit

package handler

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type cursorDispatchRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip cursorDispatchRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

type cursorDispatchHandlerRun struct {
	recorder  *httptest.ResponseRecorder
	opened    []string
	terminals []string
	records   []*service.CaptureRecord
}

func TestCursorDispatchHandlerAttemptLifecycleAndFailover(t *testing.T) {
	tests := []struct {
		name          string
		body          string
		open          func(*testing.T, int, cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error)
		wantOpened    []string
		wantTerminals []string
		wantStatus    int
		wantRecords   int
		wantText      string
	}{
		{
			name: "same account transient precedes next account",
			body: `{"model":"auto","messages":[{"role":"user","content":"handler caller body"}],"stream":false}`,
			open: func(t *testing.T, call int, options cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
				switch call {
				case 0:
					return nil, io.ErrUnexpectedEOF
				case 1:
					return nil, &cursorpkg.AgentError{Code: "resource_exhausted", Message: "local capacity", HTTPStatus: http.StatusTooManyRequests}
				default:
					return cursorDispatchAgentStream(t, options, bytes.Join([][]byte{
						cursorDispatchTextFrame("handler success"),
						cursorDispatchTurnEndedFrame(13, 8, 2, 1),
					}, nil)), nil
				}
			},
			wantOpened:    []string{"token-a", "token-a", "token-b"},
			wantTerminals: []string{"abort", "abort", "commit"},
			wantStatus:    http.StatusOK,
			wantRecords:   1,
			wantText:      "handler success",
		},
		{
			name: "trusted client version rejection stops rotation",
			body: `{"model":"auto","messages":[{"role":"user","content":"version stop"}],"stream":false}`,
			open: func(_ *testing.T, _ int, _ cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
				return nil, &cursorpkg.AgentError{Code: "permission_denied", Message: "update required", HTTPStatus: http.StatusForbidden}
			},
			wantOpened:    []string{"token-a"},
			wantTerminals: []string{"abort"},
			wantStatus:    http.StatusBadGateway,
			wantText:      service.CursorClientVersionRejectedClientMessage,
		},
		{
			name: "post output failure is committed and never replayed",
			body: `{"model":"auto","messages":[{"role":"user","content":"visible prefix"}],"stream":true}`,
			open: func(t *testing.T, _ int, options cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
				return cursorDispatchAgentStream(t, options, bytes.Join([][]byte{
					cursorDispatchTextFrame("visible before failure"),
					cursorDispatchTrailerFrame(`{"error":{"code":"unavailable","message":"local stream failed"}}`),
				}, nil)), nil
			},
			wantOpened:    []string{"token-a"},
			wantTerminals: []string{"commit"},
			wantStatus:    http.StatusOK,
			wantRecords:   1,
			wantText:      "visible before failure",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := runCursorDispatchHandler(t, test.body, test.open)
			require.Equal(t, test.wantOpened, run.opened)
			require.Equal(t, test.wantTerminals, run.terminals)
			require.Equal(t, test.wantStatus, run.recorder.Code)
			require.Len(t, run.records, test.wantRecords)
			require.Contains(t, run.recorder.Body.String(), test.wantText)
			require.NotContains(t, run.recorder.Body.String(), "connect-protocol-version")
			require.NotContains(t, run.recorder.Body.String(), "exec_stream_close")
			require.NotContains(t, run.recorder.Body.String(), "connect_proto")

			if test.wantRecords == 1 {
				record := run.records[0]
				require.Equal(t, service.PlatformCursor, record.Platform)
				require.Equal(t, []byte(test.body), record.RawRequest)
				require.Equal(t, run.recorder.Body.Bytes(), record.RawResponse)
				require.Contains(t, string(record.RawResponse), test.wantText)
				require.NotContains(t, string(record.RawRequest), "connect-protocol-version")
				require.NotContains(t, string(record.RawResponse), "exec_stream_close")
				require.NotContains(t, string(record.RawResponse), "connect_proto")
			}
		})
	}
}

func runCursorDispatchHandler(
	t *testing.T,
	body string,
	open func(*testing.T, int, cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error),
) cursorDispatchHandlerRun {
	t.Helper()
	gin.SetMode(gin.TestMode)

	groupID := int64(9510)
	accounts := []*service.Account{
		{
			ID: 9511, Name: "cursor-a", Platform: service.PlatformCursor, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"access_token": "token-a"},
		},
		{
			ID: 9512, Name: "cursor-b", Platform: service.PlatformCursor, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, Concurrency: 1,
			Credentials: map[string]any{"access_token": "token-b"},
		},
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Gateway.MaxAccountSwitches = 3
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	billing := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billing.Stop)
	records := make(chan *service.CaptureRecord, 8)
	terminalEvents := make(chan string, 8)
	capturePool := service.NewConversationCapturePoolWithTerminalEventsForUnitTest(records, terminalEvents)
	t.Cleanup(capturePool.Stop)
	gateway := service.NewOpenAIGatewayService(
		&openAIWSFailoverHandlerAccountRepoStub{accounts: []service.Account{*accounts[0], *accounts[1]}},
		nil, nil, nil, nil, nil, nil, cfg, nil, nil,
		service.NewBillingService(cfg, nil), nil, billing, nil, &service.DeferredService{},
		nil, nil, nil, nil, nil, settings, nil, capturePool,
	)

	var openedMu sync.Mutex
	var opened []string
	call := 0
	resetOpener := gateway.InstallCursorAgentStreamOpenerForUnitTest(func(_ context.Context, _ cursorpkg.AgentRunParams, options cursorpkg.AgentStreamOptions) (*cursorpkg.AgentStream, error) {
		openedMu.Lock()
		opened = append(opened, options.Token)
		currentCall := call
		call++
		openedMu.Unlock()
		return open(t, currentCall, options)
	})
	t.Cleanup(resetOpener)

	h := NewOpenAIGatewayHandler(
		gateway, service.NewConcurrencyService(nil), billing,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg),
		nil, nil, nil, nil, cfg, capturePool,
	)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	requestContext := service.WithOpenAICompatiblePlatform(context.Background(), service.PlatformCursor)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", strings.NewReader(body)).WithContext(requestContext)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: 9513, GroupID: &groupID,
		User:  &service.User{ID: 9514, Status: service.StatusActive},
		Group: &service.Group{ID: groupID, Platform: service.PlatformCursor, Status: service.StatusActive, RateMultiplier: 1},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 9514})

	h.ChatCompletions(c)

	openedMu.Lock()
	openedCopy := append([]string(nil), opened...)
	openedMu.Unlock()
	return cursorDispatchHandlerRun{
		recorder:  recorder,
		opened:    openedCopy,
		terminals: drainCursorDispatchTerminals(terminalEvents),
		records:   drainCursorDispatchRecords(records),
	}
}

func cursorDispatchAgentStream(t *testing.T, options cursorpkg.AgentStreamOptions, providerBody []byte) *cursorpkg.AgentStream {
	t.Helper()
	drained := make(chan struct{})
	client := &http.Client{Transport: cursorDispatchRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		go func() {
			_, _ = io.Copy(io.Discard, request.Body)
			close(drained)
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Proto:      "HTTP/2.0",
			ProtoMajor: 2,
			Header:     http.Header{"X-Request-Id": {"cursor-handler-local"}},
			Body:       io.NopCloser(bytes.NewReader(providerBody)),
			Request:    request,
		}, nil
	})}
	stream, err := cursorpkg.OpenAgentStream(context.Background(), cursorpkg.AgentRunParams{Prompt: "local handler test"}, cursorpkg.AgentStreamOptions{
		BaseURL:           "https://local.invalid",
		Token:             options.Token,
		ClientVersion:     options.ClientVersion,
		HTTPClient:        client,
		FirstByteTimeout:  time.Second,
		IdleTimeout:       time.Second,
		HeartbeatInterval: time.Hour,
		AllowHTTP1:        true,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		select {
		case <-drained:
		case <-time.After(time.Second):
			t.Error("local Cursor handler request writer did not stop")
		}
	})
	return stream
}

func cursorDispatchTextFrame(text string) []byte {
	var delta, update, top cursorpkg.Writer
	delta.WriteString(1, text)
	update.WriteBytes(1, delta.Bytes())
	top.WriteBytes(1, update.Bytes())
	return cursorpkg.EncodeFrame(top.Bytes(), false)
}

func cursorDispatchTurnEndedFrame(input, output, cacheRead, cacheWrite int64) []byte {
	var usage, update, top cursorpkg.Writer
	usage.WriteInt64(1, input)
	usage.WriteInt64(2, output)
	usage.WriteInt64(3, cacheRead)
	usage.WriteInt64(4, cacheWrite)
	update.WriteBytes(14, usage.Bytes())
	top.WriteBytes(1, update.Bytes())
	return cursorpkg.EncodeFrame(top.Bytes(), false)
}

func cursorDispatchTrailerFrame(payload string) []byte {
	body := []byte(payload)
	frame := make([]byte, 5+len(body))
	frame[0] = 0x02
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

func drainCursorDispatchTerminals(events <-chan string) []string {
	var terminals []string
	for {
		select {
		case terminal := <-events:
			terminals = append(terminals, terminal)
		default:
			return terminals
		}
	}
}

func drainCursorDispatchRecords(records <-chan *service.CaptureRecord) []*service.CaptureRecord {
	var captured []*service.CaptureRecord
	for {
		select {
		case record := <-records:
			captured = append(captured, record)
		default:
			return captured
		}
	}
}
