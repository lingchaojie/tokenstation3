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

const recordSinkBodyLimit = 32 << 20
const recordSinkHeaderLimit = 1 << 20

type recordSinkTransport struct {
	submit func(*CaptureRecord)
}

func (t *recordSinkTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	if begin.CaptureID == uuid.Nil {
		return nil, errors.New("capture ID is required")
	}
	return &recordSinkAttempt{
		begin:           begin,
		submit:          t.submit,
		request:         boundedCaptureWriter{limit: recordSinkBodyLimit},
		response:        boundedCaptureWriter{limit: recordSinkBodyLimit},
		requestHeaders:  boundedCaptureWriter{limit: recordSinkHeaderLimit},
		responseHeaders: boundedCaptureWriter{limit: recordSinkHeaderLimit},
	}, nil
}

func (*recordSinkTransport) Status(context.Context) (model.Status, error) {
	return model.Status{SpoolReady: true, DeliveryReady: true}, nil
}

func (*recordSinkTransport) Close() error { return nil }

type recordSinkAttempt struct {
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

func (a *recordSinkAttempt) ID() uuid.UUID { return a.begin.CaptureID }

func (a *recordSinkAttempt) WriteRequest(p []byte) bool {
	return a.write(&a.request, p)
}

func (a *recordSinkAttempt) WriteResponse(p []byte) bool {
	return a.write(&a.response, p)
}

func (a *recordSinkAttempt) WriteRequestHeaders(p []byte) bool {
	return a.write(&a.requestHeaders, p)
}

func (a *recordSinkAttempt) WriteResponseHeaders(p []byte) bool {
	return a.write(&a.responseHeaders, p)
}

func (a *recordSinkAttempt) write(writer *boundedCaptureWriter, p []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return false
	}
	_, _ = writer.Write(p)
	return true
}

func (a *recordSinkAttempt) Finalize(final model.Final) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return false
	}
	a.final = final
	a.finalized = true
	return true
}

func (a *recordSinkAttempt) Commit() bool {
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

func (a *recordSinkAttempt) Abort() {
	a.mu.Lock()
	a.terminal = true
	a.mu.Unlock()
}

func (a *recordSinkAttempt) recordLocked() *CaptureRecord {
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
		SessionID:           a.begin.SessionID,
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

func newConversationCapturePoolForRecords(records chan<- *CaptureRecord) *ConversationCapturePool {
	return newConversationCapturePoolForTransport(&recordSinkTransport{submit: func(record *CaptureRecord) {
		if record != nil && records != nil {
			records <- record
		}
	}}, func() bool { return true })
}

type recordingCaptureTransport struct {
	mu          sync.Mutex
	beginErr    error
	failWriteAt int
	status      model.Status
	begins      int
	closed      bool
	attempts    []*recordingCaptureAttempt
}

func (t *recordingCaptureTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.begins++
	if t.beginErr != nil {
		return nil, t.beginErr
	}
	attempt := &recordingCaptureAttempt{id: begin.CaptureID, begin: begin, failWriteAt: t.failWriteAt}
	t.attempts = append(t.attempts, attempt)
	return attempt, nil
}

func (t *recordingCaptureTransport) Status(context.Context) (model.Status, error) {
	return t.status, nil
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
	failWriteAt   int
	failCommit    bool
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
	if a.failWriteAt > 0 && a.writeCalls == a.failWriteAt {
		return false
	}
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
	return !a.failCommit
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

func (a *recordingCaptureAttempt) setFailCommit() {
	a.mu.Lock()
	a.failCommit = true
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

func TestConversationCapturePoolSubmitPreservesSessionID(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	pool.Submit(&CaptureRecord{SessionID: "capture-session", CapturedAt: time.Now().UTC()})

	require.Equal(t, 1, transport.Begins())
	require.Equal(t, "capture-session", transport.Attempts()[0].begin.SessionID)
}

func TestConversationCapturePoolSubmitDoesNotForwardRecordStopReason(t *testing.T) {
	transport := &recordingCaptureTransport{}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	pool.Submit(&CaptureRecord{
		CapturedAt: time.Now().UTC(),
		StopReason: "gateway_custom_value",
	})

	require.Len(t, transport.Attempts(), 1)
	require.Equal(t, []model.Final{{ResponseComplete: true}}, transport.Attempts()[0].Finals())
}

func TestRecordSinkCompatibilityPreservesSessionID(t *testing.T) {
	records := make(chan *CaptureRecord, 1)
	pool := newConversationCapturePoolForRecords(records)
	begin := testCaptureBegin()
	begin.SessionID = "capture-session"
	attempt, ok := pool.Begin(context.Background(), begin)
	require.True(t, ok)
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())

	require.Equal(t, "capture-session", (<-records).SessionID)
}

func TestConversationCapturePoolBeginFailureIsAttemptedOnceWithoutRetry(t *testing.T) {
	transport := &recordingCaptureTransport{beginErr: errors.New("sidecar unavailable")}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.False(t, ok)
	require.Nil(t, attempt)
	require.Equal(t, 1, transport.Begins())
}

func TestConversationCapturePoolClassifiesFailedAdmissionFromLiveSupervisor(t *testing.T) {
	for _, test := range []struct {
		name              string
		supervisorRunning bool
		beginErr          error
		reason            string
	}{
		{name: "sidecar down", supervisorRunning: false, beginErr: errors.New("transport unavailable"), reason: "sidecar_down"},
		{name: "IPC unavailable", supervisorRunning: true, beginErr: errors.New("transport unavailable"), reason: "ipc_unavailable"},
		{name: "typed IPC backpressure", supervisorRunning: true, beginErr: protocol.ErrIPCBackpressure, reason: "ipc_backpressure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			minute := time.Date(2026, 8, 17, 2, 3, 0, 0, time.UTC)
			transport := &recordingCaptureTransport{
				beginErr: test.beginErr,
				status: model.Status{
					HealthSourceID: uuid.New(),
					HealthBuckets:  []model.HealthBucket{{Minute: minute}},
				},
			}
			pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
			pool.supervisor = &CaptureSidecarSupervisor{status: CaptureSidecarSupervisorStatus{Running: test.supervisorRunning}}

			attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
			require.False(t, ok)
			require.Nil(t, attempt)
			status, err := pool.Status(context.Background())
			require.NoError(t, err)
			require.EqualValues(t, 1, status.DroppedByReason[test.reason])
			require.EqualValues(t, 1, status.DroppedRecords)
			require.EqualValues(t, 1, status.HealthBuckets[0].DroppedRecords[test.reason])
			if test.reason == "ipc_backpressure" {
				require.Zero(t, status.DroppedByReason["ipc_unavailable"], "typed rejection has one owner")
			}
		})
	}
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

	pool := NewConversationCapturePool(&config.Config{}, nil, settings, nil)
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

	pool := NewConversationCapturePool(cfg, nil, settings, nil)
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

func TestCaptureAttemptCountsPreCommitDisconnectExactlyOnce(t *testing.T) {
	minute := time.Date(2026, 8, 17, 2, 3, 0, 0, time.UTC)
	transport := &recordingCaptureTransport{status: model.Status{
		HealthSourceID: uuid.New(),
		HealthBuckets:  []model.HealthBucket{{Minute: minute}},
	}}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })
	attempt, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	transport.Attempts()[0].setFailNextWrite()

	require.False(t, attempt.WriteResponse([]byte("first")))
	require.False(t, attempt.WriteResponse([]byte("second")))
	require.False(t, attempt.Finalize(model.Final{HTTPStatus: 200}))
	require.False(t, attempt.Commit())
	attempt.Abort()
	status, err := pool.Status(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, status.DroppedByReason["pre_commit_disconnect"])
	require.EqualValues(t, 1, status.DroppedRecords)
}

func TestCaptureAttemptCountsFailedCommitButNotExplicitAbort(t *testing.T) {
	minute := time.Date(2026, 8, 17, 2, 3, 0, 0, time.UTC)
	transport := &recordingCaptureTransport{status: model.Status{
		HealthSourceID: uuid.New(),
		HealthBuckets:  []model.HealthBucket{{Minute: minute}},
	}}
	pool := newConversationCapturePoolForTransport(transport, func() bool { return true })

	failed, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	transport.Attempts()[0].setFailCommit()
	require.False(t, failed.Commit())
	require.False(t, failed.Commit())
	failed.Abort()

	aborted, ok := pool.Begin(context.Background(), testCaptureBegin())
	require.True(t, ok)
	aborted.Abort()
	aborted.Abort()

	status, err := pool.Status(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, status.DroppedByReason["pre_commit_disconnect"])
	require.EqualValues(t, 1, status.DroppedRecords)
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
