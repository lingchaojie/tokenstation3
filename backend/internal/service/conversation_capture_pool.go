package service

import (
	"context"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
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

	// Tests in this package can install synchronous compatibility hooks for
	// legacy archive-writer fixtures. Production/Wire construction never does.
	testSubmit              func(*CaptureRecord)
	testStop                func()
	testHealth              func() CaptureHealthSnapshot
	testReady               func() bool
	testInitializationError func() string
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
}

func newConversationCapturePoolForTransport(transport protocol.Transport, runtimeEnabled func() bool) *ConversationCapturePool {
	if transport == nil {
		return nil
	}
	if runtimeEnabled == nil {
		runtimeEnabled = func() bool { return true }
	}
	return &ConversationCapturePool{transport: transport, runtimeEnabled: runtimeEnabled}
}

// NewConversationCapturePool is the Wire-facing constructor. Static capture
// disabled returns nil and therefore creates neither a socket client nor an
// attempt. Runtime master-off is enforced before Begin by the immutable request
// policy snapshot and leaves this transport open so sidecar spool upload can
// continue draining.
func NewConversationCapturePool(cfg *config.Config, _ CaptureHealthRepository) *ConversationCapturePool {
	if cfg == nil || !cfg.Gateway.Capture.Enabled {
		return nil
	}
	transport := protocol.NewClient(protocol.ClientConfig{
		SocketPath:   cfg.Gateway.Capture.Sidecar.Socket,
		DialTimeout:  time.Millisecond,
		WriteTimeout: time.Millisecond,
		ReadTimeout:  time.Millisecond,
	})
	return newConversationCapturePoolForTransport(transport, func() bool { return true })
}

// Begin performs the one allowed transport admission synchronously. Capture
// unavailability is reported only to the caller as a missing attempt; callers
// continue proxying unchanged.
func (p *ConversationCapturePool) Begin(ctx context.Context, begin model.Begin) (*CaptureAttempt, bool) {
	if p == nil || p.transport == nil || (p.runtimeEnabled != nil && !p.runtimeEnabled()) {
		return nil, false
	}
	attempt, err := p.transport.Begin(ctx, begin)
	if err != nil || attempt == nil {
		return nil, false
	}
	return &CaptureAttempt{attempt: attempt, policy: begin.Policy}, true
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
		return false
	}
	return true
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

// Submit synchronously translates remaining compatibility CaptureRecord
// producers into the sidecar protocol. It has no queue or body copy; Task 9
// gateway paths use Begin directly, while provider-native migrations follow in
// their own task.
func (p *ConversationCapturePool) Submit(record *CaptureRecord) {
	if p == nil || record == nil {
		return
	}
	if p.testSubmit != nil {
		p.testSubmit(record)
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

func (p *ConversationCapturePool) Health() CaptureHealthSnapshot {
	if p != nil && p.testHealth != nil {
		return p.testHealth()
	}
	return CaptureHealthSnapshot{DroppedByReason: map[string]CaptureReasonStats{}, RecentIncidents: []CaptureLossIncident{}}
}

func (p *ConversationCapturePool) Ready() bool {
	if p == nil {
		return false
	}
	if p.testReady != nil {
		return p.testReady()
	}
	return p.transport != nil
}

func (p *ConversationCapturePool) InitializationError() string {
	if p == nil || p.testInitializationError == nil {
		return ""
	}
	return p.testInitializationError()
}

func (p *ConversationCapturePool) Stop() {
	if p == nil {
		return
	}
	p.stopOnce.Do(func() {
		if p.transport != nil {
			_ = p.transport.Close()
		}
		if p.testStop != nil {
			p.testStop()
		}
	})
}
