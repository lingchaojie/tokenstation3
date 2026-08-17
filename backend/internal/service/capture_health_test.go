package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/stretchr/testify/require"
)

func TestCapturePoolSynchronousAdmissionFailureHasNoQueueState(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: errors.New("sidecar unavailable")}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	attempt, ok := pool.Begin(context.Background(), model.Begin{})

	require.False(t, ok)
	require.Nil(t, attempt)
	require.Equal(t, 1, transport.Begins(), "admission must synchronously issue exactly one IPC Begin")
}

func TestCapturePoolSynchronousAttemptHasExactlyOneTerminal(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), model.Begin{Policy: model.ContentPolicy{StoreResponseBody: true}})
	require.True(t, ok)
	require.True(t, attempt.WriteResponse([]byte("response")))
	final := model.Final{HTTPStatus: 200, StopReason: "stop", ResponseComplete: true}
	require.True(t, attempt.Finalize(final))
	require.True(t, attempt.Commit())
	require.False(t, attempt.Commit(), "a synchronous attempt may commit only once")
	attempt.Abort()

	recording := transport.Attempts()[0]
	require.Equal(t, []captureTerminalState{captureCommitted}, recording.TerminalStates())
	require.Equal(t, []model.Final{final}, recording.Finals())
}
