package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

const (
	defaultCaptureWriterRetryInitialDelay = 2 * time.Second
	defaultCaptureWriterRetryMaxDelay     = 60 * time.Second
)

type archiveWriterFactory func(
	context.Context,
	config.GatewayCaptureConfig,
	*captureHealthTracker,
) (ArchiveWriter, error)

type captureRetryWait func(context.Context, time.Duration) bool

type captureWriterRetryOptions struct {
	InitialDelay time.Duration
	MaxDelay     time.Duration
	Jitter       func(time.Duration) time.Duration
	Wait         captureRetryWait
	Factory      archiveWriterFactory
}

type archiveWriterState struct {
	writer    ArchiveWriter
	ready     bool
	initError string
}

// captureArchiveWriterManager keeps a stable non-blocking writer reference for
// capture workers while ClickHouse initialization is retried in the background.
type captureArchiveWriterManager struct {
	cfg     config.GatewayCaptureConfig
	tracker *captureHealthTracker
	opts    captureWriterRetryOptions

	state atomic.Pointer[archiveWriterState]

	ctx      context.Context
	cancel   context.CancelFunc
	retryWG  sync.WaitGroup
	stopOnce sync.Once
}

func newCaptureArchiveWriterManager(
	cfg config.GatewayCaptureConfig,
	tracker *captureHealthTracker,
	opts captureWriterRetryOptions,
) *captureArchiveWriterManager {
	opts = normalizeCaptureWriterRetryOptions(opts)
	ctx, cancel := context.WithCancel(context.Background())
	manager := &captureArchiveWriterManager{
		cfg:     cfg,
		tracker: tracker,
		opts:    opts,
		ctx:     ctx,
		cancel:  cancel,
	}
	manager.state.Store(&archiveWriterState{writer: unavailableArchiveWriter{}})

	writer, err := opts.Factory(ctx, cfg, tracker)
	if err == nil && writer != nil {
		manager.state.Store(&archiveWriterState{writer: writer, ready: true})
		return manager
	}
	if err == nil {
		err = errors.New("capture: ClickHouse writer factory returned nil writer")
	}
	if writer != nil {
		writer.Stop()
	}
	manager.storeUnavailable(err)
	logger.L().With(
		zap.String("component", "service.capture_archive_writer_manager"),
		zap.Error(err),
	).Error("capture.clickhouse_init_failed_degrade_noop")

	manager.retryWG.Add(1)
	go manager.retryInitialization()
	return manager
}

func normalizeCaptureWriterRetryOptions(opts captureWriterRetryOptions) captureWriterRetryOptions {
	if opts.InitialDelay <= 0 {
		opts.InitialDelay = defaultCaptureWriterRetryInitialDelay
	}
	if opts.MaxDelay <= 0 {
		opts.MaxDelay = defaultCaptureWriterRetryMaxDelay
	}
	if opts.MaxDelay < opts.InitialDelay {
		opts.MaxDelay = opts.InitialDelay
	}
	if opts.Jitter == nil {
		opts.Jitter = captureWriterRetryJitter
	}
	if opts.Wait == nil {
		opts.Wait = waitForCaptureWriterRetry
	}
	if opts.Factory == nil {
		opts.Factory = func(_ context.Context, cfg config.GatewayCaptureConfig, tracker *captureHealthTracker) (ArchiveWriter, error) {
			return newClickHouseArchiveWriter(cfg, tracker)
		}
	}
	return opts
}

func captureWriterRetryJitter(delay time.Duration) time.Duration {
	if delay <= 0 {
		return delay
	}
	spread := delay / 5
	if spread <= 0 {
		return delay
	}
	offset := time.Duration(rand.Int64N(int64(spread)*2+1)) - spread
	return delay + offset
}

func waitForCaptureWriterRetry(ctx context.Context, delay time.Duration) bool {
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (m *captureArchiveWriterManager) retryInitialization() {
	defer m.retryWG.Done()

	delay := m.opts.InitialDelay
	for {
		waitDelay := m.opts.Jitter(delay)
		if waitDelay < 0 {
			waitDelay = 0
		}
		if waitDelay > m.opts.MaxDelay {
			waitDelay = m.opts.MaxDelay
		}
		if !m.opts.Wait(m.ctx, waitDelay) {
			return
		}

		writer, err := m.opts.Factory(m.ctx, m.cfg, m.tracker)
		if m.ctx.Err() != nil {
			if writer != nil {
				writer.Stop()
			}
			return
		}
		if err == nil && writer != nil {
			m.state.Store(&archiveWriterState{writer: writer, ready: true})
			logger.L().With(
				zap.String("component", "service.capture_archive_writer_manager"),
			).Info("capture.clickhouse_init_recovered")
			return
		}
		if err == nil {
			err = errors.New("capture: ClickHouse writer factory returned nil writer")
		}
		if writer != nil {
			writer.Stop()
		}
		m.storeUnavailable(err)

		delay = min(delay*2, m.opts.MaxDelay)
		logger.L().With(
			zap.String("component", "service.capture_archive_writer_manager"),
			zap.Error(err),
			zap.Duration("next_retry_delay", delay),
		).Warn("capture.clickhouse_init_retry_failed")
	}
}

func (m *captureArchiveWriterManager) storeUnavailable(err error) {
	initError := ""
	if err != nil {
		initError = err.Error()
	}
	m.state.Store(&archiveWriterState{
		writer:    unavailableArchiveWriter{},
		initError: initError,
	})
}

func (m *captureArchiveWriterManager) Write(ctx context.Context, item *archiveWriteItem) error {
	if m == nil {
		return errArchiveWriterUnavailable
	}
	state := m.state.Load()
	if state == nil || state.writer == nil {
		return errArchiveWriterUnavailable
	}
	return state.writer.Write(ctx, item)
}

func (m *captureArchiveWriterManager) Ready() bool {
	if m == nil {
		return false
	}
	state := m.state.Load()
	return state != nil && state.ready
}

func (m *captureArchiveWriterManager) InitializationError() string {
	if m == nil {
		return ""
	}
	state := m.state.Load()
	if state == nil {
		return ""
	}
	return state.initError
}

func (m *captureArchiveWriterManager) Stop() {
	if m == nil {
		return
	}
	m.stopOnce.Do(func() {
		m.cancel()
		m.retryWG.Wait()
		state := m.state.Load()
		if state != nil && state.writer != nil {
			state.writer.Stop()
		}
	})
}
