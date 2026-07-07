package service

import (
	"context"
	"math/rand/v2"
	"sync"

	"github.com/alitto/pond/v2"
)

type conversationCapturePoolOptions struct {
	WorkerCount    int
	QueueSize      int
	OverflowPolicy string // drop | sample
	SamplePercent  int
}

// ConversationCapturePool 是与转发/计费隔离的第三条异步通道。
// 队列满时按 overflow 策略 drop/sample，绝不 sync 回写、绝不阻塞热路径。
type ConversationCapturePool struct {
	pool     pond.Pool
	writer   ArchiveWriter
	overflow string
	sample   int
	stopOnce sync.Once
}

func newConversationCapturePool(opts conversationCapturePoolOptions, writer ArchiveWriter) *ConversationCapturePool {
	workers := opts.WorkerCount
	if workers <= 0 {
		workers = 1
	}
	queue := opts.QueueSize
	if queue <= 0 {
		queue = 1
	}
	return &ConversationCapturePool{
		pool:     pond.NewPool(workers, pond.WithQueueSize(queue)),
		writer:   writer,
		overflow: opts.OverflowPolicy,
		sample:   opts.SamplePercent,
	}
}

// Submit 非阻塞提交。队列满时按策略丢弃/采样，绝不阻塞调用方。
func (p *ConversationCapturePool) Submit(rec *CaptureRecord) {
	if p == nil || rec == nil || p.pool == nil || p.pool.Stopped() {
		return
	}
	task := func() {
		defer func() { _ = recover() }() // worker panic 不外溢
		extractCaptureColumns(rec)
		_ = p.writer.Write(context.Background(), rec)
	}
	if _, ok := p.pool.TrySubmit(task); ok {
		return
	}
	// 队列满：drop（默认）。sample 策略下按概率再试一次入队，失败即丢。
	if p.overflow == "sample" && p.sample > 0 && rand.IntN(100) < p.sample {
		_, _ = p.pool.TrySubmit(task)
	}
}

func (p *ConversationCapturePool) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		if p.pool != nil {
			p.pool.StopAndWait()
		}
		if p.writer != nil {
			p.writer.Stop()
		}
	})
}
