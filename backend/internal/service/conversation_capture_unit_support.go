//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	cursorpkg "github.com/Wei-Shaw/sub2api/internal/pkg/cursor"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const recordReconstructingTestBodyLimit = 32 << 20
const recordReconstructingTestHeaderLimit = 1 << 20

type recordReconstructingTestTransport struct {
	mu        sync.Mutex
	records   chan<- *CaptureRecord
	terminals chan<- string
	closed    bool
}

func newRecordReconstructingTestTransport(records chan<- *CaptureRecord) protocol.Transport {
	return &recordReconstructingTestTransport{records: records}
}

func (t *recordReconstructingTestTransport) Begin(_ context.Context, begin model.Begin) (protocol.Attempt, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("test capture transport closed")
	}
	if begin.CaptureID == uuid.Nil {
		return nil, errors.New("capture ID is required")
	}
	return &recordReconstructingTestAttempt{
		begin:           begin,
		records:         t.records,
		terminals:       t.terminals,
		request:         boundedCaptureWriter{limit: recordReconstructingTestBodyLimit},
		response:        boundedCaptureWriter{limit: recordReconstructingTestBodyLimit},
		requestHeaders:  boundedCaptureWriter{limit: recordReconstructingTestHeaderLimit},
		responseHeaders: boundedCaptureWriter{limit: recordReconstructingTestHeaderLimit},
	}, nil
}

func (*recordReconstructingTestTransport) Status(context.Context) (model.Status, error) {
	return model.Status{SpoolReady: true, DeliveryReady: true}, nil
}

func (t *recordReconstructingTestTransport) Close() error {
	t.mu.Lock()
	t.closed = true
	t.mu.Unlock()
	return nil
}

type recordReconstructingTestAttempt struct {
	mu sync.Mutex

	begin     model.Begin
	final     model.Final
	records   chan<- *CaptureRecord
	terminals chan<- string

	request         boundedCaptureWriter
	response        boundedCaptureWriter
	requestHeaders  boundedCaptureWriter
	responseHeaders boundedCaptureWriter
	finalized       bool
	terminal        bool
}

func (a *recordReconstructingTestAttempt) ID() uuid.UUID { return a.begin.CaptureID }

func (a *recordReconstructingTestAttempt) WriteRequest(p []byte) bool {
	return a.write(&a.request, p)
}

func (a *recordReconstructingTestAttempt) WriteResponse(p []byte) bool {
	return a.write(&a.response, p)
}

func (a *recordReconstructingTestAttempt) WriteRequestHeaders(p []byte) bool {
	return a.write(&a.requestHeaders, p)
}

func (a *recordReconstructingTestAttempt) WriteResponseHeaders(p []byte) bool {
	return a.write(&a.responseHeaders, p)
}

func (a *recordReconstructingTestAttempt) write(writer *boundedCaptureWriter, p []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return false
	}
	_, _ = writer.Write(p)
	return true
}

func (a *recordReconstructingTestAttempt) Finalize(final model.Final) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return false
	}
	a.final = final
	a.finalized = true
	return true
}

func (a *recordReconstructingTestAttempt) Commit() bool {
	a.mu.Lock()
	if a.terminal || !a.finalized {
		a.mu.Unlock()
		return false
	}
	a.terminal = true
	record := a.recordLocked()
	records := a.records
	a.mu.Unlock()
	if records != nil {
		records <- record
	}
	a.publishTerminal("commit")
	return true
}

func (a *recordReconstructingTestAttempt) Abort() {
	a.mu.Lock()
	if a.terminal {
		a.mu.Unlock()
		return
	}
	a.terminal = true
	a.mu.Unlock()
	a.publishTerminal("abort")
}

func (a *recordReconstructingTestAttempt) publishTerminal(state string) {
	if a.terminals != nil {
		a.terminals <- state
	}
}

func (a *recordReconstructingTestAttempt) recordLocked() *CaptureRecord {
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
		Truncated:           !a.final.ResponseComplete || a.request.truncated || a.response.truncated || a.requestHeaders.truncated || a.responseHeaders.truncated,
		ContentPolicy:       &content,
		InputTokens:         int(a.final.InputTokens),
		OutputTokens:        int(a.final.OutputTokens),
		CacheReadTokens:     int(a.final.CacheReadTokens),
		CacheCreationTokens: int(a.final.CacheCreationTokens),
	}
	if record.CapturedAt.IsZero() {
		record.CapturedAt = time.Now().UTC()
	}
	if providerRequestID := captureProviderRequestIDBytes(record.ResponseHeaders); providerRequestID != "" {
		record.RequestID = providerRequestID
	}
	if record.RequestID == "" {
		record.RequestID = CaptureRequestID("")
	}
	extractCaptureColumns(record)
	ApplyCaptureContentPolicy(record, content)
	return record
}

// NewConversationCapturePoolForUnitTest builds an in-memory capture sink for
// cross-package handler tests compiled with the unit tag.
func NewConversationCapturePoolForUnitTest(records chan<- *CaptureRecord) *ConversationCapturePool {
	return newConversationCapturePoolForTransport(newRecordReconstructingTestTransport(records), func() bool { return true })
}

// NewConversationCapturePoolWithTerminalEventsForUnitTest additionally exposes
// exact attempt terminal ownership to cross-package handler tests.
func NewConversationCapturePoolWithTerminalEventsForUnitTest(records chan<- *CaptureRecord, terminals chan<- string) *ConversationCapturePool {
	return newConversationCapturePoolForTransport(&recordReconstructingTestTransport{records: records, terminals: terminals}, func() bool { return true })
}

// InstallCaptureRuntimePolicyForUnitTest exposes the real compiled policy
// decision to cross-package handler tests without adding test hooks to the
// production request path.
func InstallCaptureRuntimePolicyForUnitTest(c *gin.Context, policy CaptureRuntimePolicy, userID int64, groupID *int64) error {
	compiled, err := CompileCaptureRuntimePolicy(policy)
	if err != nil {
		return err
	}
	setCompiledCaptureScopeForTest(c, compiled, userID, groupID)
	return nil
}

// InstallOpenAIAccountSchedulerForUnitTest installs a scheduler spy and a
// short-lived enabled runtime setting so cross-package handler tests exercise
// the real selection/report call sites without a settings repository.
func (s *OpenAIGatewayService) InstallOpenAIAccountSchedulerForUnitTest(scheduler OpenAIAccountScheduler) func() {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   true,
		expiresAt: time.Now().Add(time.Hour).UnixNano(),
	})
	if s != nil {
		s.openaiScheduler = scheduler
		s.openaiSchedulerOnce.Do(func() {})
	}
	return resetOpenAIAdvancedSchedulerSettingCacheForTest
}

// InstallCursorAgentStreamOpenerForUnitTest injects an in-memory Cursor Run
// stream into a constructed gateway service for cross-package handler tests.
func (s *OpenAIGatewayService) InstallCursorAgentStreamOpenerForUnitTest(opener func(
	context.Context,
	cursorpkg.AgentRunParams,
	cursorpkg.AgentStreamOptions,
) (*cursorpkg.AgentStream, error)) func() {
	if s == nil {
		return func() {}
	}
	previous := s.cursorAgentStreamOpener
	s.cursorAgentStreamOpener = cursorAgentStreamOpener(opener)
	return func() {
		s.cursorAgentStreamOpener = previous
	}
}
