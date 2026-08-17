package service

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
)

type CaptureDropReason string

const (
	CaptureDropIPCUnavailable      CaptureDropReason = "ipc_unavailable"
	CaptureDropIPCBackpressure     CaptureDropReason = "ipc_backpressure"
	CaptureDropSidecarDown         CaptureDropReason = "sidecar_down"
	CaptureDropSpoolCap            CaptureDropReason = "spool_cap"
	CaptureDropSpoolFreeReserve    CaptureDropReason = "spool_free_reserve"
	CaptureDropSpoolCorrupt        CaptureDropReason = "spool_corrupt"
	CaptureDropPreCommitDisconnect CaptureDropReason = "pre_commit_disconnect"

	// Deprecated writer reasons remain only for historical health rows. They
	// are not emitted by the sidecar or exposed by active admin/alert paths.
	CaptureDropByteBudgetExceeded      CaptureDropReason = "byte_budget_exceeded"
	CaptureDropWorkerQueueFull         CaptureDropReason = "worker_queue_full"
	CaptureDropWriterQueueFull         CaptureDropReason = "writer_queue_full"
	CaptureDropWriterUnavailable       CaptureDropReason = "writer_unavailable"
	CaptureDropClickHousePrepareFailed CaptureDropReason = "clickhouse_prepare_failed"
	CaptureDropClickHouseAppendFailed  CaptureDropReason = "clickhouse_append_failed"
	CaptureDropClickHouseSendFailed    CaptureDropReason = "clickhouse_send_failed"
)

// captureOperationalLossObserver holds only main-process losses that cannot be
// durably observed by the sidecar. Atomic counters keep capture failure paths
// free of logging, filesystem, database, or retry work.
type captureOperationalLossObserver struct {
	ipcUnavailable      atomic.Uint64
	ipcBackpressure     atomic.Uint64
	sidecarDown         atomic.Uint64
	preCommitDisconnect atomic.Uint64

	offsetMu          sync.RWMutex
	offsetInitialized bool
	offsetSource      uuid.UUID
	offsets           map[string]uint64
	origins           map[string]uint64
}

func newCaptureOperationalLossObserver() *captureOperationalLossObserver {
	return &captureOperationalLossObserver{}
}

func (o *captureOperationalLossObserver) record(reason CaptureDropReason) {
	if o == nil {
		return
	}
	var counter *atomic.Uint64
	switch reason {
	case CaptureDropIPCUnavailable:
		counter = &o.ipcUnavailable
	case CaptureDropIPCBackpressure:
		counter = &o.ipcBackpressure
	case CaptureDropSidecarDown:
		counter = &o.sidecarDown
	case CaptureDropPreCommitDisconnect:
		counter = &o.preCommitDisconnect
	default:
		return
	}
	for {
		current := counter.Load()
		if current == ^uint64(0) || counter.CompareAndSwap(current, current+1) {
			return
		}
	}
}

func (o *captureOperationalLossObserver) snapshot() map[string]uint64 {
	counts := make(map[string]uint64, 4)
	if o == nil {
		return counts
	}
	for reason, count := range map[CaptureDropReason]uint64{
		CaptureDropIPCUnavailable:      o.ipcUnavailable.Load(),
		CaptureDropIPCBackpressure:     o.ipcBackpressure.Load(),
		CaptureDropSidecarDown:         o.sidecarDown.Load(),
		CaptureDropPreCommitDisconnect: o.preCommitDisconnect.Load(),
	} {
		if count != 0 {
			counts[string(reason)] = count
		}
	}
	return counts
}

func (o *captureOperationalLossObserver) installDurableOffset(sourceID uuid.UUID, persisted map[string]uint64) bool {
	if o == nil || sourceID == uuid.Nil {
		return false
	}
	o.offsetMu.Lock()
	defer o.offsetMu.Unlock()
	if o.offsetInitialized && o.offsetSource == sourceID {
		return true
	}
	current := o.snapshot()
	origins := make(map[string]uint64, len(current))
	if o.offsetInitialized {
		for reason, count := range current {
			origins[reason] = count
		}
	}
	offsets := make(map[string]uint64, 4)
	for _, reason := range []CaptureDropReason{
		CaptureDropIPCUnavailable, CaptureDropIPCBackpressure, CaptureDropSidecarDown, CaptureDropPreCommitDisconnect,
	} {
		offsets[string(reason)] = persisted[string(reason)]
	}
	o.offsetSource = sourceID
	o.offsets = offsets
	o.origins = origins
	o.offsetInitialized = true
	return true
}

func (o *captureOperationalLossObserver) hasDurableOffset(sourceID uuid.UUID) bool {
	if o == nil || sourceID == uuid.Nil {
		return false
	}
	o.offsetMu.RLock()
	defer o.offsetMu.RUnlock()
	return o.offsetInitialized && o.offsetSource == sourceID
}

func (o *captureOperationalLossObserver) cumulativeSnapshot(sourceID uuid.UUID) map[string]uint64 {
	current := o.snapshot()
	if o == nil || sourceID == uuid.Nil {
		return map[string]uint64{}
	}
	o.offsetMu.RLock()
	defer o.offsetMu.RUnlock()
	if !o.offsetInitialized || o.offsetSource != sourceID {
		return current
	}
	result := make(map[string]uint64, len(o.offsets)+len(current))
	for reason, offset := range o.offsets {
		delta := current[reason]
		if origin := o.origins[reason]; delta >= origin {
			delta -= origin
		} else {
			delta = 0
		}
		if cumulative := saturatingUint64Add(offset, delta); cumulative != 0 {
			result[reason] = cumulative
		}
	}
	return result
}

func safeStoredCaptureHealthError(reason CaptureDropReason, value string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return captureHealthErrorCategory(reason)
}

func captureHealthErrorCategory(reason CaptureDropReason) string {
	switch reason {
	case CaptureDropIPCUnavailable:
		return "capture IPC is unavailable"
	case CaptureDropIPCBackpressure:
		return "capture IPC admission is at capacity"
	case CaptureDropSidecarDown:
		return "capture sidecar is down"
	case CaptureDropSpoolCap:
		return "capture spool reached physical cap"
	case CaptureDropSpoolFreeReserve:
		return "capture spool reached filesystem free reserve"
	case CaptureDropSpoolCorrupt:
		return "capture spool record is corrupt"
	case CaptureDropPreCommitDisconnect:
		return "capture IPC disconnected before commit"
	case CaptureDropByteBudgetExceeded:
		return "capture in-flight byte budget exceeded"
	case CaptureDropWorkerQueueFull:
		return "capture worker queue is full"
	case CaptureDropWriterQueueFull:
		return "capture writer queue is full"
	case CaptureDropWriterUnavailable:
		return "capture writer is unavailable"
	case CaptureDropClickHousePrepareFailed:
		return "ClickHouse batch prepare failed"
	case CaptureDropClickHouseAppendFailed:
		return "ClickHouse batch append failed"
	case CaptureDropClickHouseSendFailed:
		return "ClickHouse batch send failed"
	default:
		return "capture archive operation failed"
	}
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
