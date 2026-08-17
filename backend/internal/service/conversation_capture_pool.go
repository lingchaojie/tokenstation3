package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/Wei-Shaw/sub2api/internal/capture/sidecar"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/google/uuid"
)

// ConversationCapturePool is retained as the gateway compatibility name, but
// it owns only the synchronous IPC transport. There is no worker, queue, or
// body storage in the gateway process.
type ConversationCapturePool struct {
	transport      protocol.Transport
	runtimeEnabled func() bool
	stopOnce       sync.Once
	healthReporter *captureStatusReporter
	supervisor     *CaptureSidecarSupervisor
	losses         *captureOperationalLossObserver
}

// CaptureAttempt is the gateway-owned façade for one synchronous sidecar
// session. It serializes writes with the terminal operation and owns no body
// storage; after the first IPC failure all later capture calls are no-ops.
type CaptureAttempt struct {
	mu          sync.Mutex
	attempt     protocol.Attempt
	policy      model.ContentPolicy
	headerLimit int
	failed      bool
	terminal    bool
	lossOnce    sync.Once
	losses      *captureOperationalLossObserver
}

func newConversationCapturePoolForTransport(transport protocol.Transport, runtimeEnabled func() bool) *ConversationCapturePool {
	if transport == nil {
		return nil
	}
	if runtimeEnabled == nil {
		runtimeEnabled = func() bool { return true }
	}
	return &ConversationCapturePool{
		transport: transport, runtimeEnabled: runtimeEnabled, losses: newCaptureOperationalLossObserver(),
	}
}

// NewConversationCapturePool is the Wire-facing constructor. Static capture
// disabled returns nil and therefore creates neither a socket client nor an
// attempt. Runtime master-off is re-sampled from the atomically published
// setting immediately before Begin and leaves this transport open so sidecar
// spool upload can continue draining.
func NewConversationCapturePool(
	cfg *config.Config,
	healthRepo CaptureHealthRepository,
	settings *SettingService,
	supervisor *CaptureSidecarSupervisor,
) *ConversationCapturePool {
	if cfg == nil || !cfg.Gateway.Capture.Enabled {
		return nil
	}
	transport := protocol.NewClient(protocol.ClientConfig{
		SocketPath:   cfg.Gateway.Capture.Sidecar.Socket,
		DialTimeout:  10 * time.Millisecond,
		WriteTimeout: 10 * time.Millisecond,
		ReadTimeout:  10 * time.Millisecond,
	})
	pool := newConversationCapturePoolForTransport(transport, settings.CaptureRuntimeMasterEnabledHot)
	pool.supervisor = supervisor
	pool.healthReporter = newCaptureStatusReporter(
		pool,
		supervisor,
		healthRepo,
		sidecar.StatusCheckpointPath(cfg.Gateway.Capture.Spool.Dir),
		sidecar.ReadStatusCheckpoint,
	)
	pool.healthReporter.Start()
	return pool
}

// Begin performs the one allowed transport admission synchronously. Capture
// unavailability is reported only to the caller as a missing attempt; callers
// continue proxying unchanged.
func (p *ConversationCapturePool) Begin(ctx context.Context, begin model.Begin) (*CaptureAttempt, bool) {
	if p == nil || (p.runtimeEnabled != nil && !p.runtimeEnabled()) {
		return nil, false
	}
	sidecarRunning := p.supervisor == nil || p.supervisor.Status().Running
	if p.transport == nil {
		p.recordAdmissionLoss(sidecarRunning, errors.New("capture transport unavailable"))
		return nil, false
	}
	attempt, err := p.transport.Begin(ctx, begin)
	if err != nil || attempt == nil {
		p.recordAdmissionLoss(sidecarRunning, err)
		return nil, false
	}
	return &CaptureAttempt{attempt: attempt, policy: begin.Policy, losses: p.losses}, true
}

func (p *ConversationCapturePool) recordAdmissionLoss(sidecarRunning bool, beginErr error) {
	if p == nil || p.losses == nil {
		return
	}
	reason := CaptureDropIPCUnavailable
	if errors.Is(beginErr, protocol.ErrIPCBackpressure) {
		reason = CaptureDropIPCBackpressure
	} else if !sidecarRunning {
		reason = CaptureDropSidecarDown
	}
	p.losses.record(reason)
}

func (a *CaptureAttempt) ID() uuid.UUID {
	if a == nil || a.attempt == nil {
		return uuid.Nil
	}
	return a.attempt.ID()
}

func (a *CaptureAttempt) WriteRequest(payload []byte) bool {
	return a.write(a != nil && a.policy.StoreRequestBody, func(attempt protocol.Attempt) bool {
		return attempt.WriteRequest(payload)
	})
}

func (a *CaptureAttempt) WriteResponse(payload []byte) bool {
	return a.write(a != nil && a.policy.StoreResponseBody, func(attempt protocol.Attempt) bool {
		return attempt.WriteResponse(payload)
	})
}

func (a *CaptureAttempt) WriteRequestHeaders(payload []byte) bool {
	return a.write(a != nil && a.policy.StoreRequestHeaders, func(attempt protocol.Attempt) bool {
		return attempt.WriteRequestHeaders(payload)
	})
}

func (a *CaptureAttempt) WriteResponseHeaders(payload []byte) bool {
	return a.write(a != nil && a.policy.StoreResponseHeaders, func(attempt protocol.Attempt) bool {
		return attempt.WriteResponseHeaders(payload)
	})
}

func (a *CaptureAttempt) write(enabled bool, write func(protocol.Attempt) bool) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed || a.terminal || a.attempt == nil {
		return false
	}
	if !enabled {
		return true
	}
	if !write(a.attempt) {
		a.failed = true
		a.recordPreCommitDisconnect()
		return false
	}
	return true
}

func (a *CaptureAttempt) Finalize(final model.Final) bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed || a.terminal || a.attempt == nil {
		return false
	}
	if !a.attempt.Finalize(final) {
		a.failed = true
		a.recordPreCommitDisconnect()
		return false
	}
	return true
}

func (a *CaptureAttempt) Commit() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed || a.terminal || a.attempt == nil {
		return false
	}
	a.terminal = true
	if !a.attempt.Commit() {
		a.failed = true
		a.recordPreCommitDisconnect()
		return false
	}
	return true
}

func (a *CaptureAttempt) recordPreCommitDisconnect() {
	if a == nil {
		return
	}
	a.lossOnce.Do(func() {
		if a.losses != nil {
			a.losses.record(CaptureDropPreCommitDisconnect)
		}
	})
}

func (a *CaptureAttempt) Abort() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed || a.terminal || a.attempt == nil {
		return
	}
	a.terminal = true
	a.attempt.Abort()
}

// Submit synchronously translates the few remaining compatibility
// CaptureRecord producers into the sidecar protocol. It has no queue or body
// copy; provider-native wire paths use Begin directly.
func (p *ConversationCapturePool) Submit(record *CaptureRecord) {
	if p == nil || record == nil {
		return
	}
	policy := model.ContentPolicy{
		StoreRequestBody:     true,
		StoreResponseBody:    true,
		StoreRequestHeaders:  true,
		StoreResponseHeaders: true,
	}
	if record.ContentPolicy != nil {
		policy = captureModelContentPolicy(*record.ContentPolicy)
	}
	format := model.PayloadJSON
	if record.Stream {
		format = model.PayloadSSE
	}
	begin := model.Begin{
		CaptureID:        uuid.New(),
		CapturedAt:       record.CapturedAt,
		RequestID:        record.RequestID,
		Platform:         record.Platform,
		RequestedModel:   record.RequestedModel,
		UpstreamModel:    record.UpstreamModel,
		UpstreamEndpoint: record.UpstreamEndpoint,
		Stream:           record.Stream,
		Format:           format,
		Policy:           policy,
	}
	attempt, ok := p.Begin(context.Background(), begin)
	if !ok {
		return
	}
	attempt.WriteRequestHeaders(record.RequestHeaders)
	attempt.WriteRequest(record.RawRequest)
	attempt.WriteResponseHeaders(record.ResponseHeaders)
	attempt.WriteResponse(record.RawResponse)
	if !attempt.Finalize(model.Final{
		HTTPStatus:          boundedCaptureUint16(record.HTTPStatus),
		InputTokens:         boundedCaptureUint32(record.InputTokens),
		OutputTokens:        boundedCaptureUint32(record.OutputTokens),
		CacheReadTokens:     boundedCaptureUint32(record.CacheReadTokens),
		CacheCreationTokens: boundedCaptureUint32(record.CacheCreationTokens),
		StopReason:          record.StopReason,
		ResponseComplete:    !record.Truncated,
	}) {
		attempt.Abort()
		return
	}
	attempt.Commit()
}

// Status performs the bounded sidecar protocol status exchange. Callers may
// fall back to the validated disk checkpoint when this live request fails.
func (p *ConversationCapturePool) Status(ctx context.Context) (model.Status, error) {
	status, err := p.rawStatus(ctx)
	if err == nil && p.healthReporter != nil {
		p.healthReporter.observe(status)
	}
	if err == nil {
		status = p.withObservedLosses(status)
	}
	return status, err
}

func (p *ConversationCapturePool) rawStatus(ctx context.Context) (model.Status, error) {
	if p == nil || p.transport == nil {
		return model.Status{}, errors.New("capture status source is unavailable")
	}
	return p.transport.Status(ctx)
}

func (p *ConversationCapturePool) withObservedLosses(status model.Status) model.Status {
	if p == nil || p.losses == nil {
		return status
	}
	if status.HealthSourceID == uuid.Nil {
		return status
	}
	counts := p.losses.cumulativeSnapshot(status.HealthSourceID)
	if len(counts) == 0 {
		return status
	}
	status.DroppedByReason = cloneUint64Counts(status.DroppedByReason)
	if status.HealthBuckets != nil {
		buckets := make([]model.HealthBucket, len(status.HealthBuckets))
		copy(buckets, status.HealthBuckets)
		for i := range buckets {
			buckets[i].DroppedRecords = cloneUint64Counts(buckets[i].DroppedRecords)
			buckets[i].DroppedBytes = cloneUint64Counts(buckets[i].DroppedBytes)
		}
		status.HealthBuckets = buckets
	}
	for reason, count := range counts {
		status.DroppedByReason[reason] = saturatingUint64Add(status.DroppedByReason[reason], count)
		if len(status.HealthBuckets) != 0 {
			current := &status.HealthBuckets[len(status.HealthBuckets)-1]
			current.DroppedRecords[reason] = saturatingUint64Add(current.DroppedRecords[reason], count)
		}
	}
	status.DroppedRecords = 0
	for _, count := range status.DroppedByReason {
		status.DroppedRecords = saturatingUint64Add(status.DroppedRecords, count)
	}
	return status
}

func cloneUint64Counts(source map[string]uint64) map[string]uint64 {
	cloned := make(map[string]uint64, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func saturatingUint64Add(left, right uint64) uint64 {
	if right > ^uint64(0)-left {
		return ^uint64(0)
	}
	return left + right
}

func (p *ConversationCapturePool) Ready() bool {
	if p == nil {
		return false
	}
	return p.transport != nil
}

func (p *ConversationCapturePool) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		if p.healthReporter != nil {
			p.healthReporter.Stop()
		}
		if p.transport != nil {
			_ = p.transport.Close()
		}
	})
}
