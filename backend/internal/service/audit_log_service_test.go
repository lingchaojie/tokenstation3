package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type auditLogServiceTestRepo struct {
	mu sync.Mutex

	logs []*AuditLog

	batchStarted   chan struct{}
	batchRelease   chan struct{}
	batchStartOnce sync.Once
	rejectAction   string
	failClear      bool
}

func (r *auditLogServiceTestRepo) BatchInsert(ctx context.Context, logs []*AuditLog) (int64, error) {
	if r.batchStarted != nil {
		r.batchStartOnce.Do(func() { close(r.batchStarted) })
		select {
		case <-r.batchRelease:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	for _, item := range logs {
		if item != nil && item.Action == r.rejectAction {
			return 0, errors.New("rejected audit record")
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, item := range logs {
		if item != nil {
			r.logs = append(r.logs, item)
		}
	}
	return int64(len(logs)), nil
}

func (r *auditLogServiceTestRepo) Insert(_ context.Context, log *AuditLog) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failClear {
		return errors.New("clear trace failed")
	}
	r.logs = append(r.logs, log)
	return nil
}

func (r *auditLogServiceTestRepo) List(context.Context, *AuditLogFilter) (*AuditLogList, error) {
	return &AuditLogList{}, nil
}

func (r *auditLogServiceTestRepo) GetByID(context.Context, int64) (*AuditLog, error) {
	return nil, ErrAuditLogNotFound
}

func (r *auditLogServiceTestRepo) Count(context.Context) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(len(r.logs)), nil
}

func (r *auditLogServiceTestRepo) TruncateAll(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = nil
	return nil
}

func (r *auditLogServiceTestRepo) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

// ClearAllWithTrace models the atomic repository contract used by the fixed service.
// It is deliberately present before the production interface grows so the RED test
// can distinguish the existing truncate-then-insert behavior from an atomic clear.
func (r *auditLogServiceTestRepo) ClearAllWithTrace(_ context.Context, trace *AuditLog) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failClear {
		return 0, errors.New("clear trace failed")
	}
	deleted := int64(len(r.logs))
	r.logs = nil
	if trace != nil {
		if trace.Extra == nil {
			trace.Extra = map[string]any{}
		}
		trace.Extra["deleted_rows"] = deleted
		r.logs = append(r.logs, trace)
	}
	return deleted, nil
}

func (r *auditLogServiceTestRepo) snapshot() []*AuditLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*AuditLog(nil), r.logs...)
}

func TestAuditLogServiceClearAllWaitsForQueuedWritesAndLeavesOnlyTrace(t *testing.T) {
	repo := &auditLogServiceTestRepo{
		batchStarted: make(chan struct{}),
		batchRelease: make(chan struct{}),
	}
	svc := NewAuditLogService(repo, nil)
	svc.Start()
	defer svc.Stop()

	for i := 0; i < auditLogBatchSize; i++ {
		svc.Record(&AuditLog{Action: "before-clear"})
	}
	select {
	case <-repo.batchStarted:
	case <-time.After(time.Second):
		t.Fatal("writer did not start flushing queued records")
	}

	type clearResult struct {
		deleted int64
		err     error
	}
	resultCh := make(chan clearResult, 1)
	go func() {
		deleted, err := svc.ClearAll(context.Background(), &AuditLog{})
		resultCh <- clearResult{deleted: deleted, err: err}
	}()

	returnedBeforeWriter := false
	var result clearResult
	select {
	case result = <-resultCh:
		returnedBeforeWriter = true
	case <-time.After(50 * time.Millisecond):
	}
	close(repo.batchRelease)

	if !returnedBeforeWriter {
		select {
		case result = <-resultCh:
		case <-time.After(time.Second):
			t.Fatal("ClearAll did not finish after the writer was released")
		}
	}
	if returnedBeforeWriter {
		t.Fatal("ClearAll returned before queued pre-clear audit records were flushed")
	}
	if result.err != nil {
		t.Fatalf("ClearAll: %v", result.err)
	}
	if result.deleted != auditLogBatchSize {
		t.Fatalf("deleted=%d, want %d", result.deleted, auditLogBatchSize)
	}
	logs := repo.snapshot()
	if len(logs) != 1 || logs[0].Action != AuditActionAuditLogClear {
		t.Fatalf("logs after clear = %#v, want only clear trace", logs)
	}
}

func TestAuditLogServiceClearAllTraceFailureRollsBackDeletion(t *testing.T) {
	repo := &auditLogServiceTestRepo{
		logs:      []*AuditLog{{Action: "existing"}},
		failClear: true,
	}
	svc := NewAuditLogService(repo, nil)
	svc.Start()
	defer svc.Stop()

	_, err := svc.ClearAll(context.Background(), &AuditLog{})
	if err == nil {
		t.Fatal("ClearAll unexpectedly succeeded")
	}
	logs := repo.snapshot()
	if len(logs) != 1 || logs[0].Action != "existing" {
		t.Fatalf("failed clear changed stored audit logs: %#v", logs)
	}
}

func TestAuditLogWriterIsolatesMalformedRecordInsteadOfDroppingWholeBatch(t *testing.T) {
	repo := &auditLogServiceTestRepo{rejectAction: "bad"}
	svc := NewAuditLogService(repo, nil)
	svc.Start()
	for i := 0; i < auditLogBatchSize; i++ {
		action := "good"
		if i == auditLogBatchSize/2 {
			action = "bad"
		}
		svc.Record(&AuditLog{Action: action})
	}
	svc.Stop()

	logs := repo.snapshot()
	if len(logs) != auditLogBatchSize-1 {
		t.Fatalf("persisted %d valid records, want %d", len(logs), auditLogBatchSize-1)
	}
	if got := atomic.LoadUint64(&svc.writeFailed); got != 1 {
		t.Fatalf("writeFailed=%d, want only the malformed record", got)
	}
}
