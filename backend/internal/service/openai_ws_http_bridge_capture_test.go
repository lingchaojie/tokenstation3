//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newTask7WSBridgeCaptureFixture(
	t *testing.T,
	upstream HTTPUpstream,
) (*OpenAIGatewayService, *gin.Context, *Account, *recordingCaptureTransport, *openaiTransportAccountRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	enableCaptureForTest(t, c)
	SetCaptureRequestedModel(c, "gpt-5")

	captureTransport := &recordingCaptureTransport{}
	accountRepo := &openaiTransportAccountRepoStub{}
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	cfg.Gateway.Capture.MaxHeaderBytes = 1 << 20
	service := &OpenAIGatewayService{
		accountRepo:  accountRepo,
		cfg:          cfg,
		httpUpstream: upstream,
		capturePool: newConversationCapturePoolForTransport(
			captureTransport,
			func() bool { return true },
		),
	}
	account := &Account{
		ID:          7107,
		Name:        "task7-ws-capture",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Concurrency: 1,
	}
	return service, c, account, captureTransport, accountRepo
}

func TestTask7WSBridgeRejectedFieldCaptureCommitsOnlyFinalWirePairAfterClientWrite(t *testing.T) {
	rejectedBody := `{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"truncation","message":"Unsupported parameter: truncation"}}`
	completedSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_capture_retry\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
		{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"X-Request-Id": []string{"rid-rejected"}},
			Body:       io.NopCloser(strings.NewReader(rejectedBody)),
		},
		{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"rid-final"}},
			Body:       io.NopCloser(strings.NewReader(completedSSE)),
		},
	}}
	service, c, account, captureTransport, accountRepo := newTask7WSBridgeCaptureFixture(t, upstream)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi","truncation":"auto"}`)
	var clientWrites [][]byte

	result, err := service.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "sk-test", payload, len(payload),
		"gpt-5", "", "", "", "", 1,
		func(message []byte) error {
			clientWrites = append(clientWrites, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, upstream.callCount)
	require.Len(t, clientWrites, 1)
	require.Equal(t, "response.completed", gjson.GetBytes(clientWrites[0], "type").String())

	attempts := captureTransport.Attempts()
	require.Len(t, attempts, 2)
	require.True(t, gjson.GetBytes(attempts[0].RequestBytes(), "truncation").Exists())
	require.Equal(t, []byte(rejectedBody), attempts[0].ResponseBytes())
	require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
	require.False(t, gjson.GetBytes(attempts[1].RequestBytes(), "truncation").Exists())
	require.Equal(t, []byte(completedSSE), attempts[1].ResponseBytes())
	require.Empty(t, attempts[1].TerminalStates(), "the final capture must remain pending until the successful client write returns")

	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result))
	require.Equal(t, []captureTerminalState{captureCommitted}, attempts[1].TerminalStates())
	require.False(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result), "the final wire pair must commit at most once")
	require.Empty(t, accountRepo.tempUnschedCalls)
	require.False(t, service.isOpenAIAccountRuntimeBlocked(account))
}

func TestTask7WSBridgeRejectedFieldCaptureFailurePathsNeverCommitOrPunishAccount(t *testing.T) {
	rejectedBody := `{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"truncation","message":"Unsupported parameter: truncation"}}`
	completedSSE := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_capture_write\",\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi","truncation":"auto"}`)

	t.Run("client write failure", func(t *testing.T) {
		upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{
			{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(rejectedBody))},
			{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(completedSSE))},
		}}
		service, c, account, captureTransport, accountRepo := newTask7WSBridgeCaptureFixture(t, upstream)

		result, err := service.proxyOpenAIWSHTTPBridgeTurn(
			context.Background(), c, account, "sk-test", payload, len(payload),
			"gpt-5", "", "", "", "", 1,
			func([]byte) error { return errors.New("client write failed") },
		)

		require.Error(t, err)
		require.Nil(t, result)
		require.Equal(t, 2, upstream.callCount)
		attempts := captureTransport.Attempts()
		require.Len(t, attempts, 2)
		require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
		require.Empty(t, attempts[1].TerminalStates(), "a failed client write cannot commit the final attempt")
		AbortCaptureAttempt(c) // production request-handler defer owns abandoned pending attempts
		require.Equal(t, []captureTerminalState{captureAborted}, attempts[1].TerminalStates())
		require.Empty(t, accountRepo.tempUnschedCalls)
		require.False(t, service.isOpenAIAccountRuntimeBlocked(account))
	})

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "transport failure", err: io.EOF},
		{name: "request cancellation", err: context.Canceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			upstream := &httpUpstreamSequenceRecorder{errs: []error{test.err}}
			service, c, account, captureTransport, accountRepo := newTask7WSBridgeCaptureFixture(t, upstream)

			result, err := service.proxyOpenAIWSHTTPBridgeTurn(
				context.Background(), c, account, "sk-test", payload, len(payload),
				"gpt-5", "", "", "", "", 1,
				func([]byte) error { return nil },
			)

			require.Error(t, err)
			require.Nil(t, result)
			require.Equal(t, 1, upstream.callCount)
			attempts := captureTransport.Attempts()
			require.Len(t, attempts, 1)
			require.Equal(t, []captureTerminalState{captureAborted}, attempts[0].TerminalStates())
			require.Empty(t, accountRepo.tempUnschedCalls)
			require.False(t, service.isOpenAIAccountRuntimeBlocked(account))
		})
	}
}

func TestTask7WSBridgeVisibleFramePreventsReplayAndKeepsSingleCapture(t *testing.T) {
	providerSSE := strings.Join([]string{
		`data: {"type":"response.created","response":{"id":"resp_visible"}}`,
		"",
		`data: {"type":"response.output_text.delta","delta":"partial"}`,
		"",
		`data: {"type":"response.failed","response":{"id":"resp_visible","status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}`,
		"",
	}, "\n")
	upstream := &httpUpstreamSequenceRecorder{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(providerSSE)),
	}}}
	service, c, account, captureTransport, _ := newTask7WSBridgeCaptureFixture(t, upstream)
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"hi"}`)
	var clientWrites [][]byte

	result, err := service.proxyOpenAIWSHTTPBridgeTurn(
		context.Background(), c, account, "sk-test", payload, len(payload),
		"gpt-5", "", "", "", "", 1,
		func(message []byte) error {
			clientWrites = append(clientWrites, append([]byte(nil), message...))
			return nil
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 1, upstream.callCount, "a client-visible frame permanently closes the replay window")
	require.Len(t, clientWrites, 3)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	attempts := captureTransport.Attempts()
	require.Len(t, attempts, 1)
	require.Equal(t, []byte(providerSSE), attempts[0].ResponseBytes())
	require.Empty(t, attempts[0].TerminalStates())
	require.True(t, CommitOpenAIForwardCaptureAttempt(c, PlatformOpenAI, result))
	require.Equal(t, []captureTerminalState{captureCommitted}, attempts[0].TerminalStates())
}
