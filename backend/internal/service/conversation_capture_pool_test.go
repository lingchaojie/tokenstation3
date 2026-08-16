package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type captureTerminalState string

const (
	captureCommitted captureTerminalState = "committed"
	captureAborted   captureTerminalState = "aborted"
)

// These compatibility doubles are also consumed by capture_admin_service_test
// while the admin view is migrated from the retired native writer to sidecar
// status in a later task.
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

func (*fakeWriter) Stop() {}

type conversationCapturePoolOptions struct {
	WorkerCount     int
	QueueSize       int
	OverflowPolicy  string
	SamplePercent   int
	MaxQueueBytes   int64
	WriterQueueSize int
}

type archiveWriterStatus interface {
	Ready() bool
	InitializationError() string
}

type staticArchiveWriterStatus struct {
	ready     bool
	initError string
}

func (s staticArchiveWriterStatus) Ready() bool                 { return s.ready }
func (s staticArchiveWriterStatus) InitializationError() string { return s.initError }

const archiveWriterReconstructingBodyLimit = 32 << 20
const archiveWriterReconstructingHeaderLimit = 1 << 20

type archiveWriterReconstructingTransport struct {
	submit func(*CaptureRecord)
}

func (t *archiveWriterReconstructingTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	if begin.CaptureID == uuid.Nil {
		return nil, errors.New("capture ID is required")
	}
	return &archiveWriterReconstructingAttempt{
		begin:           begin,
		submit:          t.submit,
		request:         boundedCaptureWriter{limit: archiveWriterReconstructingBodyLimit},
		response:        boundedCaptureWriter{limit: archiveWriterReconstructingBodyLimit},
		requestHeaders:  boundedCaptureWriter{limit: archiveWriterReconstructingHeaderLimit},
		responseHeaders: boundedCaptureWriter{limit: archiveWriterReconstructingHeaderLimit},
	}, nil
}

func (*archiveWriterReconstructingTransport) Status(context.Context) (model.Status, error) {
	return model.Status{SpoolReady: true, DeliveryReady: true}, nil
}

func (*archiveWriterReconstructingTransport) Close() error { return nil }

type archiveWriterReconstructingAttempt struct {
	mu sync.Mutex

	begin  model.Begin
	final  model.Final
	submit func(*CaptureRecord)

	request         boundedCaptureWriter
	response        boundedCaptureWriter
	requestHeaders  boundedCaptureWriter
	responseHeaders boundedCaptureWriter
	finalized       bool
	terminal        bool
}

func (a *archiveWriterReconstructingAttempt) ID() uuid.UUID { return a.begin.CaptureID }

func (a *archiveWriterReconstructingAttempt) WriteRequest(p []byte) bool {
	return a.write(&a.request, p)
}

func (a *archiveWriterReconstructingAttempt) WriteResponse(p []byte) bool {
	return a.write(&a.response, p)
}

func (a *archiveWriterReconstructingAttempt) WriteRequestHeaders(p []byte) bool {
	return a.write(&a.requestHeaders, p)
}

func (a *archiveWriterReconstructingAttempt) WriteResponseHeaders(p []byte) bool {
	return a.write(&a.responseHeaders, p)
}

func (a *archiveWriterReconstructingAttempt) write(writer *boundedCaptureWriter, p []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return false
	}
	_, _ = writer.Write(p)
	return true
}

func (a *archiveWriterReconstructingAttempt) Finalize(final model.Final) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return false
	}
	a.final = final
	a.finalized = true
	return true
}

func (a *archiveWriterReconstructingAttempt) Commit() bool {
	a.mu.Lock()
	if a.terminal || !a.finalized {
		a.mu.Unlock()
		return false
	}
	a.terminal = true
	record := a.recordLocked()
	submit := a.submit
	a.mu.Unlock()
	if submit != nil {
		submit(record)
	}
	return true
}

func (a *archiveWriterReconstructingAttempt) Abort() {
	a.mu.Lock()
	a.terminal = true
	a.mu.Unlock()
}

func (a *archiveWriterReconstructingAttempt) recordLocked() *CaptureRecord {
	content := CaptureContentPolicy{
		RawRequest:      a.begin.Policy.StoreRequestBody,
		RawResponse:     a.begin.Policy.StoreResponseBody,
		RequestHeaders:  a.begin.Policy.StoreRequestHeaders,
		ResponseHeaders: a.begin.Policy.StoreResponseHeaders,
	}
	record := &CaptureRecord{
		CapturedAt:          a.begin.CapturedAt,
		Platform:            a.begin.Platform,
		RequestID:           a.begin.RequestID,
		RequestedModel:      a.begin.RequestedModel,
		UpstreamModel:       a.begin.UpstreamModel,
		UpstreamEndpoint:    a.begin.UpstreamEndpoint,
		Stream:              a.begin.Stream,
		HTTPStatus:          int(a.final.HTTPStatus),
		RawRequest:          snapshotBytes(a.request.buf),
		RawResponse:         snapshotBytes(a.response.buf),
		RequestHeaders:      snapshotBytes(a.requestHeaders.buf),
		ResponseHeaders:     snapshotBytes(a.responseHeaders.buf),
		Truncated:           a.request.truncated || a.response.truncated || a.requestHeaders.truncated || a.responseHeaders.truncated || !a.final.ResponseComplete,
		ContentPolicy:       &content,
		StopReason:          a.final.StopReason,
		InputTokens:         int(a.final.InputTokens),
		OutputTokens:        int(a.final.OutputTokens),
		CacheReadTokens:     int(a.final.CacheReadTokens),
		CacheCreationTokens: int(a.final.CacheCreationTokens),
	}
	if record.CapturedAt.IsZero() {
		record.CapturedAt = time.Now().UTC()
	}
	if record.RequestID == "" {
		record.RequestID = CaptureRequestID("")
	}
	return record
}

// newConversationCapturePool is a synchronous test-only compatibility shim for
// archive-writer fixtures that predate the sidecar transport.
func newConversationCapturePool(opts conversationCapturePoolOptions, writer ArchiveWriter) *ConversationCapturePool {
	tracker := newCaptureHealthTracker("test", time.Now)
	tracker.setCapacities(int64(opts.QueueSize), int64(opts.WriterQueueSize), opts.MaxQueueBytes)
	submit := func(record *CaptureRecord) {
		if record == nil || writer == nil {
			return
		}
		tracker.recordSubmitted()
		tracker.recordAccepted()
		size := recordBytes(record)
		tracker.inFlightBytes.add(size)
		item := newArchiveWriteItem(record, size, func(result archiveWriteResult) {
			tracker.inFlightBytes.add(-size)
			if result.success {
				tracker.recordWritten(1)
			} else {
				tracker.recordDrop(result.reason, 1, size, result.err)
			}
		})
		extractCaptureColumns(record)
		if record.ContentPolicy != nil {
			ApplyCaptureContentPolicy(record, *record.ContentPolicy)
		}
		if err := writer.Write(context.Background(), item); err != nil {
			reason := CaptureDropWriterUnavailable
			if errors.Is(err, errArchiveQueueFull) {
				reason = CaptureDropWriterQueueFull
			}
			item.completeDrop(reason, err)
		}
	}
	pool := newConversationCapturePoolForTransport(&archiveWriterReconstructingTransport{submit: submit}, func() bool { return true })
	pool.testHealth = tracker.snapshot
	pool.testReady = func() bool { return true }
	pool.testStop = func() {
		if writer != nil {
			writer.Stop()
		}
	}
	pool.testSubmit = submit
	return pool
}

func newConversationCapturePoolWithStatus(
	opts conversationCapturePoolOptions,
	writer ArchiveWriter,
	status archiveWriterStatus,
	tracker *captureHealthTracker,
	reporter *captureHealthReporter,
) *ConversationCapturePool {
	pool := newConversationCapturePool(opts, writer)
	if tracker != nil {
		pool.testHealth = tracker.snapshot
	}
	if status != nil {
		pool.testReady = status.Ready
		pool.testInitializationError = status.InitializationError
	}
	if reporter != nil {
		previousStop := pool.testStop
		pool.testStop = func() {
			previousStop()
			reporter.Stop()
		}
	}
	return pool
}

func newConversationCapturePoolWithState(
	opts conversationCapturePoolOptions,
	writer ArchiveWriter,
	tracker *captureHealthTracker,
	reporter *captureHealthReporter,
	ready bool,
	initError string,
) *ConversationCapturePool {
	return newConversationCapturePoolWithStatus(opts, writer, staticArchiveWriterStatus{
		ready: ready, initError: sanitizeCaptureHealthError(errors.New(initError)),
	}, tracker, reporter)
}

type recordingCaptureTransport struct {
	mu       sync.Mutex
	beginErr error
	begins   int
	closed   bool
	attempts []*recordingCaptureAttempt
}

func (t *recordingCaptureTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.begins++
	if t.beginErr != nil {
		return nil, t.beginErr
	}
	attempt := &recordingCaptureAttempt{id: begin.CaptureID, begin: begin}
	t.attempts = append(t.attempts, attempt)
	return attempt, nil
}

func (*recordingCaptureTransport) Status(context.Context) (model.Status, error) {
	return model.Status{}, nil
}

func (t *recordingCaptureTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

func (t *recordingCaptureTransport) Begins() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.begins
}

func (t *recordingCaptureTransport) Closed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

func (t *recordingCaptureTransport) Attempts() []*recordingCaptureAttempt {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]*recordingCaptureAttempt(nil), t.attempts...)
}

type recordingCaptureAttempt struct {
	mu sync.Mutex

	id    uuid.UUID
	begin model.Begin

	request         []byte
	requestInputs   [][]byte
	response        []byte
	requestHeaders  []byte
	responseHeaders []byte
	finals          []model.Final
	terminals       []captureTerminalState

	failNextWrite bool
	writeCalls    int
}

func (a *recordingCaptureAttempt) ID() uuid.UUID { return a.id }

func (a *recordingCaptureAttempt) WriteRequest(p []byte) bool {
	a.mu.Lock()
	a.requestInputs = append(a.requestInputs, p)
	a.mu.Unlock()
	return a.write(&a.request, p)
}

func (a *recordingCaptureAttempt) WriteResponse(p []byte) bool {
	return a.write(&a.response, p)
}

func (a *recordingCaptureAttempt) WriteRequestHeaders(p []byte) bool {
	return a.write(&a.requestHeaders, p)
}

func (a *recordingCaptureAttempt) WriteResponseHeaders(p []byte) bool {
	return a.write(&a.responseHeaders, p)
}

func (a *recordingCaptureAttempt) write(dst *[]byte, p []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writeCalls++
	if a.failNextWrite {
		a.failNextWrite = false
		return false
	}
	*dst = append(*dst, p...)
	return true
}

func (a *recordingCaptureAttempt) Finalize(final model.Final) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.finals = append(a.finals, final)
	return true
}

func (a *recordingCaptureAttempt) Commit() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terminals = append(a.terminals, captureCommitted)
	return true
}

func (a *recordingCaptureAttempt) Abort() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.terminals = append(a.terminals, captureAborted)
}

func (a *recordingCaptureAttempt) setFailNextWrite() {
	a.mu.Lock()
	a.failNextWrite = true
	a.mu.Unlock()
}

func (a *recordingCaptureAttempt) WriteCalls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeCalls
}

func (a *recordingCaptureAttempt) ResponseBytes() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.response...)
}

func (a *recordingCaptureAttempt) RequestBytes() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.request...)
}

func (a *recordingCaptureAttempt) RequestHeaderBytes() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.requestHeaders...)
}

func (a *recordingCaptureAttempt) ResponseHeaderBytes() []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]byte(nil), a.responseHeaders...)
}

func (a *recordingCaptureAttempt) RequestInputs() [][]byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([][]byte(nil), a.requestInputs...)
}

func (a *recordingCaptureAttempt) TerminalStates() []captureTerminalState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]captureTerminalState(nil), a.terminals...)
}

func (a *recordingCaptureAttempt) Finals() []model.Final {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]model.Final(nil), a.finals...)
}

func testCaptureBegin() model.Begin {
	return model.Begin{
		CaptureID: uuid.New(),
		Platform:  PlatformAnthropic,
		Policy: model.ContentPolicy{
			StoreRequestBody:     true,
			StoreResponseBody:    true,
			StoreRequestHeaders:  true,
			StoreResponseHeaders: true,
		},
	}
}

func TestConversationCapturePoolBeginsTransportAttemptSynchronously(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	require.Equal(t, 1, transport.Begins())
	require.True(t, attempt.WriteResponse([]byte("chunk")))
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())
	require.Equal(t, []captureTerminalState{captureCommitted}, transport.Attempts()[0].TerminalStates())
}

func TestConversationCapturePoolBeginFailureIsAttemptedOnceWithoutRetry(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: errors.New("sidecar unavailable")}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.False(t, ok)
	require.Nil(t, attempt)
	require.Equal(t, 1, transport.Begins())
}

func TestRuntimeMasterOffDoesNotBeginButLeavesTransportOpen(t *testing.T) {
	transport := &recordingCaptureTransport{}
	var enabled atomic.Bool
	pool := newConversationCapturePoolForTransport(transport, enabled.Load)

	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.False(t, ok)
	require.Nil(t, attempt)
	require.Zero(t, transport.Begins())
	require.False(t, transport.Closed())
}

func TestProductionConversationCapturePoolStaticDisabledIsInert(t *testing.T) {
	repo := &capturePolicyRepoStub{}
	settings := NewSettingService(repo, &config.Config{})

	pool := NewConversationCapturePool(&config.Config{}, nil, settings)
	require.Nil(t, pool)
	gets, sets := repo.calls()
	require.Zero(t, gets)
	require.Zero(t, sets)
}

func TestProductionConversationCapturePoolResamplesActualRuntimeMasterBeforeBegin(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.Sidecar.Socket = "/tmp/capture-runtime-master-test.sock"
	settings := NewSettingService(&capturePolicyRepoStub{}, cfg)
	policy := DefaultCaptureRuntimePolicy()
	policy.Enabled = true
	_, err := settings.UpdateCaptureRuntimePolicy(context.Background(), policy)
	require.NoError(t, err)

	requestScope, _ := gin.CreateTestContext(httptest.NewRecorder())
	setCompiledCaptureScopeForTest(requestScope, settings.GetCompiledCaptureRuntimePolicyHot(), 99, nil)
	require.True(t, CaptureMayApplyFor(requestScope, PlatformAnthropic), "request scope must retain its admitted on snapshot")

	pool := NewConversationCapturePool(cfg, nil, settings)
	require.NotNil(t, pool)
	productionTransport := pool.transport
	transport := &recordingCaptureTransport{}
	pool.transport = transport
	require.NoError(t, productionTransport.Close())
	t.Cleanup(pool.Stop)

	policy.Enabled = false
	_, err = settings.UpdateCaptureRuntimePolicy(context.Background(), policy)
	require.NoError(t, err)

	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.False(t, ok)
	require.Nil(t, attempt)
	require.Zero(t, transport.Begins(), "master-off must veto admission immediately before transport Begin")
	require.False(t, transport.Closed(), "runtime master-off must leave the sidecar transport open")
}

func TestCaptureAttemptDoesNotRetryAfterIPCWriteFailure(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	recording := transport.Attempts()[0]
	recording.setFailNextWrite()

	require.False(t, attempt.WriteResponse([]byte("first")))
	require.False(t, attempt.WriteResponse([]byte("second")))
	require.False(t, attempt.Finalize(model.Final{HTTPStatus: 200}))
	require.False(t, attempt.Commit())
	require.Equal(t, 1, recording.WriteCalls(), "a failed IPC handle must become a no-op")
	require.Empty(t, recording.TerminalStates())
}

func TestCaptureAttemptContentPolicySkipsDisabledPayloads(t *testing.T) {
	transport := &recordingCaptureTransport{}
	begin := testCaptureBegin()
	begin.Policy = model.ContentPolicy{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), begin)
	require.True(t, ok)

	require.True(t, attempt.WriteRequest([]byte("request")))
	require.True(t, attempt.WriteResponse([]byte("response")))
	require.True(t, attempt.WriteRequestHeaders([]byte("request headers")))
	require.True(t, attempt.WriteResponseHeaders([]byte("response headers")))
	require.True(t, attempt.Finalize(model.Final{ResponseComplete: true}))
	require.True(t, attempt.Commit())
	require.Zero(t, transport.Attempts()[0].WriteCalls(), "disabled content columns must never enter IPC")
}

func TestCaptureAttemptOwnsExactlyOneTerminalOperation(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	committed, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	require.True(t, committed.Commit())
	require.False(t, committed.Commit())
	committed.Abort()
	require.Equal(t, []captureTerminalState{captureCommitted}, transport.Attempts()[0].TerminalStates())

	aborted, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	aborted.Abort()
	aborted.Abort()
	require.False(t, aborted.Commit())
	require.Equal(t, []captureTerminalState{captureAborted}, transport.Attempts()[1].TerminalStates())
}
