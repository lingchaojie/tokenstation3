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
	terminals atomic.Int64
}

func (t *discardingBoundedTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	return &discardingBoundedAttempt{id: begin.CaptureID, terminals: &t.terminals}, nil
}

func (*discardingBoundedTransport) Status(context.Context) (model.Status, error) {
	return model.Status{}, nil
}

func (*discardingBoundedTransport) Close() error { return nil }

func (t *discardingBoundedTransport) TerminalAttempts() int64 {
	return t.terminals.Load()
}

type discardingBoundedAttempt struct {
	id        uuid.UUID
	terminals *atomic.Int64
}

func (a *discardingBoundedAttempt) ID() uuid.UUID                  { return a.id }
func (*discardingBoundedAttempt) WriteRequest([]byte) bool         { return true }
func (*discardingBoundedAttempt) WriteResponse([]byte) bool        { return true }
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

func runCaptureFixture(t *testing.T, pool *ConversationCapturePool, payload []byte) {
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
	require.True(t, attempt.WriteRequest(payload))
	require.True(t, attempt.WriteResponse(payload))
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())
}

func TestFiveHundredLargeCapturesDoNotAccumulateInGatewayHeap(t *testing.T) {
	transport := &discardingBoundedTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	baseline := forceGCAndReadHeap(t)
	payload := make([]byte, 8<<20)
	for i := 0; i < 500; i++ {
		runCaptureFixture(t, pool, payload)
	}
	after := forceGCAndReadHeap(t)
	retained := uint64(0)
	if after > baseline {
		retained = after - baseline
	}
	require.Less(t, retained, uint64(64<<20))
	require.Equal(t, int64(500), transport.TerminalAttempts())
	runtime.KeepAlive(payload)
}
