package service

import (
	"context"
	"math/rand/v2"
	"sync"

	"github.com/alitto/pond/v2"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type conversationCapturePoolOptions struct {
	WorkerCount    int
	QueueSize      int
	OverflowPolicy string // drop | sample
	SamplePercent  int
	MaxQueueBytes  int64 // 0 = 不限；按字节限流在途 record，防大 body 突发打爆内存
}

// ConversationCapturePool 是与转发/计费隔离的第三条异步通道。
// 队列满时按 overflow 策略 drop/sample，绝不 sync 回写、绝不阻塞热路径。
type ConversationCapturePool struct {
	pool     pond.Pool
	writer   ArchiveWriter
	overflow string
	sample   int
	bytes    *captureByteGauge
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
		bytes:    &captureByteGauge{max: opts.MaxQueueBytes},
	}
}

// Submit 非阻塞提交。队列满时按策略丢弃/采样，绝不阻塞调用方。
func (p *ConversationCapturePool) Submit(rec *CaptureRecord) {
	if p == nil || rec == nil || p.pool == nil || p.pool.Stopped() {
		return
	}
	// reserveAndSubmit 预留字节 + 入队，两者都成功才返回 true；任一失败都撤销预留。
	// task 顶部 defer release 覆盖所有退出路径（Write 成功/失败/drop、panic），单点释放不泄漏。
	n := recordBytes(rec)
	reserveAndSubmit := func() bool {
		if !p.bytes.tryReserve(n) {
			return false
		}
		task := func() {
			defer p.bytes.release(n)
			defer func() { _ = recover() }() // worker panic 不外溢
			extractCaptureColumns(rec)
			_ = p.writer.Write(context.Background(), rec)
		}
		if _, ok := p.pool.TrySubmit(task); ok {
			return true
		}
		p.bytes.release(n) // 入队失败，撤销预留
		return false
	}
	if reserveAndSubmit() {
		return
	}
	// 队列满或超字节预算：drop（默认）。sample 策略下按概率再试一次入队，失败即丢。
	if p.overflow == "sample" && p.sample > 0 && rand.IntN(100) < p.sample {
		reserveAndSubmit()
	}
}

// NewConversationCapturePool 是 wire provider。capture 关闭时返回 nil（handler 侧已 nil 保护）；
// ClickHouse 建连失败时降级为 noopArchiveWriter（仍可 Submit，但不落库），绝不阻塞启动、绝不影响转发。
func NewConversationCapturePool(cfg *config.Config) *ConversationCapturePool {
	if cfg == nil || !cfg.Gateway.Capture.Enabled {
		return nil
	}
	cc := cfg.Gateway.Capture
	writer, err := newClickHouseArchiveWriter(cc)
	if err != nil {
		logger.L().With(
			zap.String("component", "service.conversation_capture_pool"),
			zap.Error(err),
		).Error("capture.clickhouse_init_failed_degrade_noop")
		writer = noopArchiveWriter{}
	}
	return newConversationCapturePool(conversationCapturePoolOptions{
		WorkerCount:    cc.WorkerCount,
		QueueSize:      cc.QueueSize,
		OverflowPolicy: cc.OverflowPolicy,
		SamplePercent:  cc.OverflowSamplePercent,
		MaxQueueBytes:  cc.MaxQueueBytes,
	}, writer)
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
