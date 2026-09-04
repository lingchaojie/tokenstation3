//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type delayedOpenAITerminalTailBody struct {
	terminal []byte
	tail     []byte
	delay    time.Duration
	closed   chan struct{}
	close    sync.Once
	reads    int
}

func (r *delayedOpenAITerminalTailBody) Read(p []byte) (int, error) {
	switch r.reads {
	case 0:
		r.reads++
		return copy(p, r.terminal), nil
	case 1:
		select {
		case <-time.After(r.delay):
			r.reads++
			return copy(p, r.tail), nil
		case <-r.closed:
			return 0, io.EOF
		}
	default:
		return 0, io.EOF
	}
}

func (r *delayedOpenAITerminalTailBody) Close() error {
	r.close.Do(func() { close(r.closed) })
	return nil
}

func TestReadOpenAICompatBufferedTerminalKeepsParserErrorsButAllowsProviderTail(t *testing.T) {
	terminal := `data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed","output":[]}}` + "\n\n"
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{
			name:    "malformed JSON before terminal",
			body:    "data: {not-json}\n\n" + terminal,
			wantErr: true,
		},
		{
			name: "provider data after terminal",
			body: terminal + `data: {"type":"response.output_text.delta","delta":"tail"}` + "\n\n",
		},
		{
			name:    "incomplete event tail after terminal",
			body:    terminal + "event: response.output_text.delta\n",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}

			finalResponse, _, _, _, err := (&OpenAIGatewayService{}).readOpenAICompatBufferedTerminal(resp, nil, "test", "rid")

			if tt.wantErr {
				require.Error(t, err)
				require.Nil(t, finalResponse)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, finalResponse)
			require.Equal(t, "resp_ok", finalResponse.ID)
		})
	}
}

func TestReadOpenAICompatBufferedTerminalAllowsDelayedChunkedTail(t *testing.T) {
	terminal := []byte(`data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed","output":[]}}` + "\n\n")
	tail := []byte(`data: {"type":"response.output_text.delta","delta":"tail"}` + "\n\n")
	body := &delayedOpenAITerminalTailBody{
		terminal: terminal,
		tail:     tail,
		delay:    5 * time.Millisecond,
		closed:   make(chan struct{}),
	}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: -1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:          body,
	}

	finalResponse, _, _, _, err := (&OpenAIGatewayService{}).readOpenAICompatBufferedTerminal(resp, nil, "test", "rid")

	require.NoError(t, err)
	require.NotNil(t, finalResponse)
	require.Equal(t, "resp_ok", finalResponse.ID)
}

func TestReadOpenAICompatBufferedTerminalRejectsBufferedPartialTailBeforeClosingOpenBody(t *testing.T) {
	terminal := []byte(`data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed","output":[]}}` + "\n\n")
	partialTail := []byte("event: response.output_text.delta")
	body := &providerPrefixThenBlockReader{
		prefix: append(append([]byte(nil), terminal...), partialTail...),
		closed: make(chan struct{}),
	}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: -1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:          body,
	}

	finalResponse, _, _, _, err := (&OpenAIGatewayService{}).readOpenAICompatBufferedTerminal(resp, nil, "test", "rid")

	require.ErrorContains(t, err, "incomplete provider event")
	require.Nil(t, finalResponse)
	select {
	case <-body.closed:
	default:
		t.Fatal("terminal tail validation must close and join the open provider body")
	}
}

func TestReadOpenAICompatBufferedTerminalReturnsAfterBoundedTailGrace(t *testing.T) {
	terminal := []byte(`data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed","output":[]}}` + "\n\n")
	body := &providerPrefixThenBlockReader{prefix: terminal, closed: make(chan struct{})}
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: -1,
		Header:        http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:          body,
	}
	started := time.Now()

	finalResponse, _, _, _, err := (&OpenAIGatewayService{}).readOpenAICompatBufferedTerminal(resp, nil, "test", "rid")

	require.NoError(t, err)
	require.NotNil(t, finalResponse)
	require.Equal(t, "resp_ok", finalResponse.ID)
	require.Less(t, time.Since(started), 250*time.Millisecond)
}

func TestBufferedResponsesSSEToJSONUsesCompletedAggregateWithoutStrictIntermediateValidation(t *testing.T) {
	terminal := `data: {"type":"response.completed","response":{"id":"resp_ok","status":"completed","output":[]}}` + "\n\n"
	t.Run("native malformed intermediate", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
		result, err := (&OpenAIGatewayService{}).handleSSEToJSONWithContext(
			context.Background(), resp, c, nil, []byte("data: {bad}\n\n"+terminal), "gpt-5", "gpt-5",
		)
		require.NoError(t, err)
		require.NotNil(t, result)
	})

	t.Run("passthrough aggregate mismatch", func(t *testing.T) {
		body := strings.Join([]string{
			`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg-a","type":"message","role":"assistant","content":[]}}`,
			`data: {"type":"response.content_part.added","output_index":0,"content_index":0,"item_id":"msg-a","part":{"type":"output_text","text":""}}`,
			`data: {"type":"response.output_text.delta","output_index":0,"content_index":0,"item_id":"msg-a","delta":"safe"}`,
			`data: {"type":"response.output_text.done","output_index":0,"content_index":0,"item_id":"msg-a","text":"safe"}`,
			`data: {"type":"response.content_part.done","output_index":0,"content_index":0,"item_id":"msg-a","part":{"type":"output_text","text":"safe"}}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"msg-a","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"danger"}]}}`,
			strings.TrimSpace(terminal),
			"",
		}, "\n\n")
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
		resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}}
		result, err := (&OpenAIGatewayService{}).handlePassthroughSSEToJSONWithAccountContext(
			context.Background(), resp, c, nil, []byte(body), "gpt-5", "gpt-5",
		)
		require.NoError(t, err)
		require.NotNil(t, result)
	})
}
