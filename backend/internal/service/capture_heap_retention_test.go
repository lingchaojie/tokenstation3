package service

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type discardingBoundedTransport struct {
	terminals     atomic.Int64
	requestBytes  atomic.Int64
	responseBytes atomic.Int64
}

func (t *discardingBoundedTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	return &discardingBoundedAttempt{
		id:            begin.CaptureID,
		terminals:     &t.terminals,
		requestBytes:  &t.requestBytes,
		responseBytes: &t.responseBytes,
	}, nil
}

func (*discardingBoundedTransport) Status(context.Context) (model.Status, error) {
	return model.Status{}, nil
}

func (*discardingBoundedTransport) Close() error { return nil }

func (t *discardingBoundedTransport) TerminalAttempts() int64 {
	return t.terminals.Load()
}

func (t *discardingBoundedTransport) AcceptedRequestBytes() int64 {
	return t.requestBytes.Load()
}

func (t *discardingBoundedTransport) AcceptedResponseBytes() int64 {
	return t.responseBytes.Load()
}

type discardingBoundedAttempt struct {
	id            uuid.UUID
	terminals     *atomic.Int64
	requestBytes  *atomic.Int64
	responseBytes *atomic.Int64
}

func (a *discardingBoundedAttempt) ID() uuid.UUID { return a.id }
func (a *discardingBoundedAttempt) WriteRequest(payload []byte) bool {
	a.requestBytes.Add(int64(len(payload)))
	return true
}
func (a *discardingBoundedAttempt) WriteResponse(payload []byte) bool {
	a.responseBytes.Add(int64(len(payload)))
	return true
}
func (*discardingBoundedAttempt) WriteRequestHeaders([]byte) bool  { return true }
func (*discardingBoundedAttempt) WriteResponseHeaders([]byte) bool { return true }
func (*discardingBoundedAttempt) Finalize(model.Final) bool        { return true }
func (a *discardingBoundedAttempt) Commit() bool                   { a.terminals.Add(1); return true }
func (a *discardingBoundedAttempt) Abort()                         { a.terminals.Add(1) }

func forceGCAndReadHeap(t *testing.T) uint64 {
	t.Helper()
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func runCaptureFixture(t *testing.T, pool *ConversationCapturePool, requestPayload, responsePayload []byte) {
	t.Helper()
	attempt, ok := pool.Begin(context.Background(), model.Begin{
		CaptureID: uuid.New(),
		Platform:  PlatformOpenAI,
		Format:    model.PayloadJSON,
		Policy: model.ContentPolicy{
			StoreRequestBody:  true,
			StoreResponseBody: true,
		},
	})
	require.True(t, ok)
	require.True(t, attempt.WriteRequest(requestPayload))
	require.True(t, attempt.WriteResponse(responsePayload))
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())
}

func TestFiveHundredLargeCapturesDoNotAccumulateInGatewayHeap(t *testing.T) {
	transport := &discardingBoundedTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	baseline := forceGCAndReadHeap(t)
	for i := 0; i < 500; i++ {
		requestPayload := make([]byte, 8<<20)
		responsePayload := make([]byte, 8<<20)
		runCaptureFixture(t, pool, requestPayload, responsePayload)
		runtime.KeepAlive(requestPayload)
		runtime.KeepAlive(responsePayload)
		if i%16 == 15 {
			runtime.GC()
		}
	}
	after := forceGCAndReadHeap(t)
	retained := uint64(0)
	if after > baseline {
		retained = after - baseline
	}
	require.Less(t, retained, uint64(64<<20))
	require.Equal(t, int64(500), transport.TerminalAttempts())
	require.Equal(t, int64(500*(8<<20)), transport.AcceptedRequestBytes())
	require.Equal(t, int64(500*(8<<20)), transport.AcceptedResponseBytes())
	t.Logf("retained=%d request_bytes=%d response_bytes=%d terminals=%d", retained, transport.AcceptedRequestBytes(), transport.AcceptedResponseBytes(), transport.TerminalAttempts())
}
