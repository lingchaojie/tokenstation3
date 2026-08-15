package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type mutableArchiveWriterStatus struct {
	mu        sync.RWMutex
	ready     bool
	initError string
}

func (s *mutableArchiveWriterStatus) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ready
}

func (s *mutableArchiveWriterStatus) InitializationError() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initError
}

func (s *mutableArchiveWriterStatus) set(ready bool, initError string) {
	s.mu.Lock()
	s.ready = ready
	s.initError = initError
	s.mu.Unlock()
}

type fakeWriter struct{ n int32 }

func (f *fakeWriter) Write(_ context.Context, item *archiveWriteItem) error {
	atomic.AddInt32(&f.n, 1)
	item.completeSuccess()
	return nil
}
func (f *fakeWriter) Stop() {}

func TestCapturePoolSubmitAndExtract(t *testing.T) {
	fw := &fakeWriter{}
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 2, QueueSize: 16, OverflowPolicy: "drop",
	}, fw)
	defer p.Stop()

	rec := &CaptureRecord{
		RawRequest:  []byte(`{"metadata":{"user_id":"{\"device_id\":\"d\",\"session_id\":\"sess-1\"}"}}`),
		RawResponse: []byte(`{"stop_reason":"end_turn","usage":{"output_tokens":4}}`),
	}
	p.Submit(rec)

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&fw.n) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if atomic.LoadInt32(&fw.n) != 1 {
		t.Fatal("record not written")
	}
	if rec.SessionID != "sess-1" || rec.StopReason != "end_turn" {
		t.Fatalf("columns not extracted in worker: %+v", rec)
	}
}

func TestCapturePoolExtractsProviderNativeMetadataBeforeSuppressingRawResponse(t *testing.T) {
	fw := &fakeWriter{}
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 4, OverflowPolicy: "drop",
	}, fw)
	defer p.Stop()

	rec := &CaptureRecord{
		Platform:      PlatformGemini,
		RawRequest:    []byte(`{"request":{"sessionId":"nested-session"},"additionalModelRequestFields":{"thinking":{"type":"adaptive"},"output_config":{"effort":"high"}}}`),
		RawResponse:   []byte(`{"candidates":[{"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":5,"cachedContentTokenCount":3}}`),
		ContentPolicy: &CaptureContentPolicy{RawRequest: false, RawResponse: false},
	}
	p.Submit(rec)

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&fw.n) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&fw.n))
	require.Nil(t, rec.RawResponse)
	require.Nil(t, rec.RawRequest)
	require.Equal(t, "nested-session", rec.SessionID)
	require.Equal(t, "high", rec.ThinkingEffort)
	require.Equal(t, "adaptive", rec.ThinkingType)
	require.Equal(t, "STOP", rec.StopReason)
	require.Equal(t, 17, rec.InputTokens)
	require.Equal(t, 5, rec.OutputTokens)
	require.Equal(t, 3, rec.CacheReadTokens)
}

func TestCapturePoolDropsOnFullQueue(t *testing.T) {
	fw := &fakeWriter{}
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 1, OverflowPolicy: "drop",
	}, fw)
	defer p.Stop()
	for i := 0; i < 1000; i++ {
		p.Submit(&CaptureRecord{RawResponse: []byte(`{}`)})
	}
}

func TestCapturePoolNilSafe(t *testing.T) {
	var p *ConversationCapturePool
	p.Submit(&CaptureRecord{}) // must not panic
	p.Stop()                   // must not panic
}

func TestCapturePoolReadinessDelegatesToWriterStatus(t *testing.T) {
	status := &mutableArchiveWriterStatus{initError: "dial failed"}
	pool := newConversationCapturePoolWithStatus(
		conversationCapturePoolOptions{WorkerCount: 1, QueueSize: 1},
		&fakeWriter{},
		status,
		newCaptureHealthTracker("test", time.Now),
		nil,
	)
	t.Cleanup(pool.Stop)

	require.False(t, pool.Ready())
	require.Equal(t, "dial failed", pool.InitializationError())

	status.set(true, "")
	require.True(t, pool.Ready())
	require.Empty(t, pool.InitializationError())
}

// blockingWriter 阻塞在 Write 上直到 release 关闭，用来把 record 卡在 worker 里，
// 从而让字节预算保持占用，验证超预算 drop。
type blockingWriter struct {
	release chan struct{}
	n       int32
}

func (b *blockingWriter) Write(_ context.Context, item *archiveWriteItem) error {
	atomic.AddInt32(&b.n, 1)
	<-b.release
	item.completeSuccess()
	return nil
}
func (b *blockingWriter) Stop() {}

func TestCapturePoolDropsOnByteBudget(t *testing.T) {
	bw := &blockingWriter{release: make(chan struct{})}
	// 单 worker，队列够大（不靠条数限流），字节预算=100。
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 64, OverflowPolicy: "drop", MaxQueueBytes: 100,
	}, bw)

	// 第一条 60 字节：进 worker 并阻塞在 Write（预留 60，未释放）。
	p.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt32(&bw.n) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if atomic.LoadInt32(&bw.n) != 1 {
		t.Fatal("first record should reach writer")
	}
	// 在途已占 60/100。再来一条 60（>剩余 40）必须被 drop（inFlight 保持 60）。
	p.Submit(&CaptureRecord{RawResponse: make([]byte, 60)})
	if got := p.bytes.inFlight.Load(); got != 60 {
		t.Fatalf("over-budget submit must be dropped; inFlight=%d want 60", got)
	}

	// 放行第一条，其 defer release 归还 60 → 回到 0。
	close(bw.release)
	for time.Now().Before(deadline) {
		if p.bytes.inFlight.Load() == 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := p.bytes.inFlight.Load(); got != 0 {
		t.Fatalf("after drain, inFlight must return to 0, got %d", got)
	}
	p.Stop()
}

func TestCapturePoolAcceptsOneRecordWithRequestAndResponseAtBodyLimit(t *testing.T) {
	const bodyLimit = 5 << 20
	fw := &fakeWriter{}
	p := newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount: 1, QueueSize: 1, OverflowPolicy: "drop", MaxQueueBytes: 2 * bodyLimit,
	}, fw)
	t.Cleanup(p.Stop)

	rec := &CaptureRecord{
		RawRequest:  make([]byte, bodyLimit),
		RawResponse: make([]byte, bodyLimit),
	}
	require.Equal(t, int64(2*bodyLimit), recordBytes(rec))
	p.Submit(rec)

	require.Eventually(t, func() bool {
		return atomic.LoadInt32(&fw.n) == 1
	}, 2*time.Second, 5*time.Millisecond, "one max-sized request/response pair must fit the configured body budget")
}
