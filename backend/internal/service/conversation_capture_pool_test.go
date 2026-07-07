package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type fakeWriter struct{ n int32 }

func (f *fakeWriter) Write(_ context.Context, rec *CaptureRecord) error {
	atomic.AddInt32(&f.n, 1)
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
