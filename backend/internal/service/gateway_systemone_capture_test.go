package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestB8deSystemOneCaptureOptInAndFinalAttempt(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[enabled], func(t *testing.T) {
			policy := DefaultCaptureRuntimePolicy()
			policy.Enabled, policy.Platforms.TypeSafe = true, enabled
			c, svc, transport, account := newFinalAttemptFixture(t, policy)
			defer AbortCaptureAttempt(c)
			account.ID, account.Platform, account.Type = 7, PlatformTypeSafe, AccountTypeAPIKey
			account.Credentials = map[string]any{"base_url": "https://api.typesafe.ai", "api_key": "private-key"}
			SetCaptureRequestedModel(c, "jev-latest")
			calls := 0
			svc.httpUpstream = &systemOneHTTPUpstream{do: func(req *http.Request) (*http.Response, error) {
				calls++
				require.Equal(t, "/v1/systemone", req.URL.Path)
				status, body := 503, `{"error":"retry"}`
				if calls == 2 {
					status, body = 200, `{"model":"jev-1","answers":{},"usage":{"input_tokens":11}}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"X-Request-Id": {"native-attempt"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			}}
			_, err := svc.ForwardSystemOne(context.Background(), c, account, []byte(`{"model":"jev-latest","state":"first"}`))
			var failure *UpstreamFailoverError
			require.ErrorAs(t, err, &failure)
			require.True(t, failure.HasUpstreamHTTPResponse)
			require.Equal(t, PlatformTypeSafe, failure.Platform)
			require.False(t, failure.CaptureResponseIncomplete)
			body := []byte(`{"model":"jev-latest","state":"final"}`)
			result, err := svc.ForwardSystemOne(context.Background(), c, account, body)
			require.NoError(t, err)
			require.Equal(t, 11, result.Usage.InputTokens)
			require.True(t, result.CaptureResponseComplete)
			if !enabled {
				require.Empty(t, transport.Attempts())
				return
			}
			require.Len(t, transport.Attempts(), 2)
			first, last := transport.Attempts()[0], transport.Attempts()[1]
			require.Equal(t, []captureTerminalState{captureAborted}, first.TerminalStates())
			require.Equal(t, body, last.RequestBytes())
			require.Equal(t, result.Body, last.ResponseBytes())
			require.NotContains(t, string(last.requestHeaders), "private-key")
			require.True(t, CommitForwardCaptureAttempt(c, PlatformTypeSafe, &result.ForwardResult))
			CommitForwardCaptureAttempt(c, PlatformTypeSafe, &result.ForwardResult)
			require.Equal(t, []captureTerminalState{captureCommitted}, last.TerminalStates())
			require.Len(t, last.Finals(), 1)
		})
	}
}

func TestB8deSystemOneTerminalErrorsRemainCaptureOnly(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"request error", 400, `{"error":"bad input"}`},
		{"invalid successful response", 200, `not json`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			policy := DefaultCaptureRuntimePolicy()
			policy.Enabled, policy.Platforms.TypeSafe = true, true
			c, svc, transport, account := newFinalAttemptFixture(t, policy)
			defer AbortCaptureAttempt(c)
			account.Platform, account.Type = PlatformTypeSafe, AccountTypeAPIKey
			account.Credentials = map[string]any{"base_url": "https://api.typesafe.ai", "api_key": "private-key"}
			svc.httpUpstream = newSystemOneStatusUpstream(tc.status, tc.body)
			result, err := svc.ForwardSystemOne(context.Background(), c, account, []byte(`{"model":"jev-latest"}`))
			require.Error(t, err)
			require.NotNil(t, result)
			require.True(t, result.UpstreamFailed)
			require.True(t, result.CaptureTerminalError)
			require.Zero(t, result.Usage.InputTokens)
			require.True(t, CommitForwardCaptureAttempt(c, PlatformTypeSafe, &result.ForwardResult))
			require.Len(t, transport.Attempts(), 1)
			require.Equal(t, []byte(tc.body), transport.Attempts()[0].ResponseBytes())
			require.Equal(t, uint16(tc.status), transport.Attempts()[0].Finals()[0].HTTPStatus)
		})
	}
}
