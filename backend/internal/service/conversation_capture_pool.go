package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"time"

	"github.com/alitto/pond/v2"
	"go.uber.org/zap"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

type conversationCapturePoolOptions struct {
	WorkerCount     int
	QueueSize       int
	OverflowPolicy  string // drop | sample
	SamplePercent   int
	MaxQueueBytes   int64 // 0 = 不限；按字节限流在途 record，防大 body 突发打爆内存
	WriterQueueSize int
}

// ConversationCapturePool 是与转发/计费隔离的第三条异步通道。
// 队列满时按 overflow 策略 drop/sample，绝不 sync 回写、绝不阻塞热路径。
type ConversationCapturePool struct {
	pool      pond.Pool
	writer    ArchiveWriter
	overflow  string
	sample    int
	bytes     *captureByteGauge
	health    *captureHealthTracker
	reporter  *captureHealthReporter
	ready     bool
	initError string
	queueMu   sync.Mutex
	stopOnce  sync.Once
}

func newConversationCapturePool(opts conversationCapturePoolOptions, writer ArchiveWriter) *ConversationCapturePool {
	tracker := newCaptureHealthTracker(captureInstanceID(), time.Now)
	return newConversationCapturePoolWithState(opts, writer, tracker, nil, true, "")
}

func newConversationCapturePoolWithState(
	opts conversationCapturePoolOptions,
	writer ArchiveWriter,
	tracker *captureHealthTracker,
	reporter *captureHealthReporter,
	ready bool,
	initError string,
) *ConversationCapturePool {
	workers := opts.WorkerCount
	if workers <= 0 {
		workers = 1
	}
	queue := opts.QueueSize
	if queue <= 0 {
		queue = 1
	}
	if tracker == nil {
		tracker = newCaptureHealthTracker(captureInstanceID(), time.Now)
	}
	tracker.setCapacities(int64(queue), int64(opts.WriterQueueSize), opts.MaxQueueBytes)
	return &ConversationCapturePool{
		pool:      pond.NewPool(workers, pond.WithQueueSize(queue)),
		writer:    writer,
		overflow:  opts.OverflowPolicy,
		sample:    opts.SamplePercent,
		bytes:     &captureByteGauge{max: opts.MaxQueueBytes},
		health:    tracker,
		reporter:  reporter,
		ready:     ready,
		initError: sanitizeCaptureHealthError(errors.New(initError)),
	}
}

// Submit 非阻塞提交。队列满时按策略丢弃/采样，绝不阻塞调用方。
func (p *ConversationCapturePool) Submit(rec *CaptureRecord) {
	if p == nil || rec == nil || p.pool == nil || p.pool.Stopped() {
		return
	}
	p.health.recordSubmitted()
	// reserveAndSubmit 预留字节 + 入队，两者都成功才返回 true；任一失败都撤销预留。
	// task 顶部 defer release 覆盖所有退出路径（Write 成功/失败/drop、panic），单点释放不泄漏。
	n := recordBytes(rec)
	reserveAndSubmit := func() (bool, CaptureDropReason) {
		if !p.bytes.tryReserve(n) {
			return false, CaptureDropByteBudgetExceeded
		}
		p.health.inFlightBytes.add(n)
		item := newArchiveWriteItem(rec, n, func(result archiveWriteResult) {
			if result.success {
				p.health.recordWritten(1)
			} else {
				p.health.recordDrop(result.reason, 1, n, result.err)
			}
			p.bytes.release(n)
			p.health.inFlightBytes.add(-n)
		})
		task := func() {
			p.queueMu.Lock()
			p.health.workerQueue.add(-1)
			p.queueMu.Unlock()
			defer func() {
				if recovered := recover(); recovered != nil {
					item.completeDrop(CaptureDropWriterUnavailable, fmt.Errorf("capture worker panic: %v", recovered))
				}
			}()
			extractCaptureColumns(rec)
			if rec.ContentPolicy != nil {
				ApplyCaptureContentPolicy(rec, *rec.ContentPolicy)
			}
			err := p.writer.Write(context.Background(), item)
			if err == nil {
				return
			}
			reason := CaptureDropWriterUnavailable
			if errors.Is(err, errArchiveQueueFull) {
				reason = CaptureDropWriterQueueFull
			}
			item.completeDrop(reason, err)
		}
		p.queueMu.Lock()
		if _, ok := p.pool.TrySubmit(task); ok {
			p.health.workerQueue.add(1)
			p.health.recordAccepted()
			p.queueMu.Unlock()
			return true, ""
		}
		p.queueMu.Unlock()
		p.bytes.release(n) // 入队失败，撤销预留
		p.health.inFlightBytes.add(-n)
		return false, CaptureDropWorkerQueueFull
	}
	ok, finalReason := reserveAndSubmit()
	if ok {
		return
	}
	// 队列满或超字节预算：drop（默认）。sample 策略下按概率再试一次入队，失败即丢。
	if p.overflow == "sample" && p.sample > 0 && rand.IntN(100) < p.sample {
		if ok, reason := reserveAndSubmit(); ok {
			return
		} else {
			p.health.recordDrop(reason, 1, n, nil)
			return
		}
	}
	p.health.recordDrop(finalReason, 1, n, nil)
}

// NewConversationCapturePool 是 wire provider。capture 关闭时返回 nil（handler 侧已 nil 保护）；
// ClickHouse 建连失败时降级为 noopArchiveWriter（仍可 Submit，但不落库），绝不阻塞启动、绝不影响转发。
func NewConversationCapturePool(cfg *config.Config, repos ...CaptureHealthRepository) *ConversationCapturePool {
	if cfg == nil || !cfg.Gateway.Capture.Enabled {
		return nil
	}
	cc := cfg.Gateway.Capture
	tracker := newCaptureHealthTracker(captureInstanceID(), time.Now)
	writer, err := newClickHouseArchiveWriter(cc, tracker)
	ready := err == nil
	initError := ""
	if err != nil {
		logger.L().With(
			zap.String("component", "service.conversation_capture_pool"),
			zap.Error(err),
		).Error("capture.clickhouse_init_failed_degrade_noop")
		initError = err.Error()
		writer = unavailableArchiveWriter{}
	}
	var reporter *captureHealthReporter
	if len(repos) > 0 && repos[0] != nil {
		reporter = newCaptureHealthReporter(tracker, repos[0], captureHealthReporterOptions{})
		reporter.Start()
	}
	return newConversationCapturePoolWithState(conversationCapturePoolOptions{
		WorkerCount:     cc.WorkerCount,
		QueueSize:       cc.QueueSize,
		WriterQueueSize: cc.WriterQueueSize,
		OverflowPolicy:  cc.OverflowPolicy,
		SamplePercent:   cc.OverflowSamplePercent,
		MaxQueueBytes:   cc.MaxQueueBytes,
	}, writer, tracker, reporter, ready, initError)
}

func captureInstanceID() string {
	host, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return host
}

func (p *ConversationCapturePool) Health() CaptureHealthSnapshot {
	if p == nil {
		return CaptureHealthSnapshot{DroppedByReason: map[string]CaptureReasonStats{}, RecentIncidents: []CaptureLossIncident{}}
	}
	return p.health.snapshot()
}

func (p *ConversationCapturePool) Ready() bool { return p != nil && p.ready }

func (p *ConversationCapturePool) InitializationError() string {
	if p == nil {
		return ""
	}
	return p.initError
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
		if p.reporter != nil {
			p.reporter.Stop()
		}
	})
}
