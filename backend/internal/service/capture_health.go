package service

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/logredact"
)

type CaptureDropReason string

const (
	CaptureDropByteBudgetExceeded      CaptureDropReason = "byte_budget_exceeded"
	CaptureDropWorkerQueueFull         CaptureDropReason = "worker_queue_full"
	CaptureDropWriterQueueFull         CaptureDropReason = "writer_queue_full"
	CaptureDropWriterUnavailable       CaptureDropReason = "writer_unavailable"
	CaptureDropClickHousePrepareFailed CaptureDropReason = "clickhouse_prepare_failed"
	CaptureDropClickHouseAppendFailed  CaptureDropReason = "clickhouse_append_failed"
	CaptureDropClickHouseSendFailed    CaptureDropReason = "clickhouse_send_failed"
	captureHealthIncidentCapacity                        = 100
	captureHealthErrorMaxBytes                           = 512
)

var captureDropReasons = []CaptureDropReason{
	CaptureDropByteBudgetExceeded,
	CaptureDropWorkerQueueFull,
	CaptureDropWriterQueueFull,
	CaptureDropWriterUnavailable,
	CaptureDropClickHousePrepareFailed,
	CaptureDropClickHouseAppendFailed,
	CaptureDropClickHouseSendFailed,
}

type CaptureReasonStats struct {
	Records uint64 `json:"records"`
	Bytes   uint64 `json:"bytes"`
}

type CaptureGaugeSnapshot struct {
	Current  int64 `json:"current"`
	Peak     int64 `json:"peak"`
	Capacity int64 `json:"capacity"`
}

type CaptureLossIncident struct {
	OccurredAt    time.Time `json:"occurred_at"`
	Reason        string    `json:"reason"`
	Records       int64     `json:"records"`
	Bytes         int64     `json:"bytes"`
	WorkerQueue   int64     `json:"worker_queue"`
	WriterQueue   int64     `json:"writer_queue"`
	InFlightBytes int64     `json:"in_flight_bytes"`
	Error         string    `json:"error"`
}

type CaptureHealthSnapshot struct {
	StartedAt             time.Time                     `json:"started_at"`
	SubmittedRecords      uint64                        `json:"submitted_records"`
	AcceptedRecords       uint64                        `json:"accepted_records"`
	WrittenRecords        uint64                        `json:"written_records"`
	DroppedRecords        uint64                        `json:"dropped_records"`
	DroppedBytes          uint64                        `json:"dropped_bytes"`
	DroppedByReason       map[string]CaptureReasonStats `json:"dropped_by_reason"`
	WorkerQueue           CaptureGaugeSnapshot          `json:"worker_queue"`
	WriterQueue           CaptureGaugeSnapshot          `json:"writer_queue"`
	InFlightBytes         CaptureGaugeSnapshot          `json:"in_flight_bytes"`
	LastSuccessAt         *time.Time                    `json:"last_success_at,omitempty"`
	LastDropAt            *time.Time                    `json:"last_drop_at,omitempty"`
	LastDropReason        string                        `json:"last_drop_reason"`
	LastError             string                        `json:"last_error"`
	RecentIncidents       []CaptureLossIncident         `json:"recent_incidents"`
	HistoryDroppedBuckets uint64                        `json:"history_dropped_buckets"`
}

type captureAtomicGauge struct {
	current  atomic.Int64
	peak     atomic.Int64
	capacity int64
}

func (g *captureAtomicGauge) add(delta int64) int64 {
	current := g.current.Add(delta)
	for {
		peak := g.peak.Load()
		if current <= peak || g.peak.CompareAndSwap(peak, current) {
			break
		}
	}
	return current
}

func (g *captureAtomicGauge) set(value int64) {
	g.current.Store(value)
	for {
		peak := g.peak.Load()
		if value <= peak || g.peak.CompareAndSwap(peak, value) {
			return
		}
	}
}

func (g *captureAtomicGauge) snapshot() CaptureGaugeSnapshot {
	return CaptureGaugeSnapshot{Current: g.current.Load(), Peak: g.peak.Load(), Capacity: g.capacity}
}

type captureDropCounter struct {
	records atomic.Uint64
	bytes   atomic.Uint64
}

type captureHealthBucketKey struct {
	minute time.Time
	reason CaptureDropReason
}

type captureHealthTracker struct {
	instanceID string
	now        func() time.Time
	startedAt  time.Time

	submitted             atomic.Uint64
	accepted              atomic.Uint64
	written               atomic.Uint64
	dropped               atomic.Uint64
	dropBytes             atomic.Uint64
	historyDroppedBuckets atomic.Uint64

	dropByReason  map[CaptureDropReason]*captureDropCounter
	workerQueue   captureAtomicGauge
	writerQueue   captureAtomicGauge
	inFlightBytes captureAtomicGauge

	lastSuccessUnixNano atomic.Int64
	lastDropUnixNano    atomic.Int64
	lastDropReason      atomic.Value // string
	lastError           atomic.Value // string

	mu        sync.Mutex
	incidents []CaptureLossIncident
	buckets   map[captureHealthBucketKey]CaptureHealthEvent
}

func newCaptureHealthTracker(instanceID string, now func() time.Time) *captureHealthTracker {
	if now == nil {
		now = time.Now
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instanceID = "unknown"
	}
	if len(instanceID) > 255 {
		instanceID = instanceID[:255]
	}
	t := &captureHealthTracker{
		instanceID:   instanceID,
		now:          now,
		startedAt:    now().UTC(),
		dropByReason: make(map[CaptureDropReason]*captureDropCounter, len(captureDropReasons)),
		incidents:    make([]CaptureLossIncident, 0, captureHealthIncidentCapacity),
		buckets:      make(map[captureHealthBucketKey]CaptureHealthEvent),
	}
	for _, reason := range captureDropReasons {
		t.dropByReason[reason] = &captureDropCounter{}
	}
	t.lastDropReason.Store("")
	t.lastError.Store("")
	return t
}

func (t *captureHealthTracker) setCapacities(workerQueue, writerQueue, maxBytes int64) {
	if t == nil {
		return
	}
	t.workerQueue.capacity = workerQueue
	t.writerQueue.capacity = writerQueue
	t.inFlightBytes.capacity = maxBytes
}

func (t *captureHealthTracker) recordSubmitted() {
	if t != nil {
		t.submitted.Add(1)
	}
}
func (t *captureHealthTracker) recordAccepted() {
	if t != nil {
		t.accepted.Add(1)
	}
}

func (t *captureHealthTracker) recordWritten(records int64) {
	if t == nil || records <= 0 {
		return
	}
	t.written.Add(uint64(records))
	t.lastSuccessUnixNano.Store(t.now().UTC().UnixNano())
}

func (t *captureHealthTracker) recordDrop(reason CaptureDropReason, records, bytes int64, cause error) {
	if t == nil || records <= 0 {
		return
	}
	if bytes < 0 {
		bytes = 0
	}
	counter, ok := t.dropByReason[reason]
	if !ok {
		reason = CaptureDropWriterUnavailable
		counter = t.dropByReason[reason]
	}
	t.dropped.Add(uint64(records))
	t.dropBytes.Add(uint64(bytes))
	counter.records.Add(uint64(records))
	counter.bytes.Add(uint64(bytes))

	at := t.now().UTC()
	errorSummary := safeCaptureHealthErrorSummary(reason, cause)
	t.lastDropUnixNano.Store(at.UnixNano())
	t.lastDropReason.Store(string(reason))
	t.lastError.Store(errorSummary)
	workerCurrent := t.workerQueue.current.Load()
	writerCurrent := t.writerQueue.current.Load()
	inFlightCurrent := t.inFlightBytes.current.Load()
	incident := CaptureLossIncident{
		OccurredAt: at, Reason: string(reason), Records: records, Bytes: bytes,
		WorkerQueue: workerCurrent, WriterQueue: writerCurrent, InFlightBytes: inFlightCurrent, Error: errorSummary,
	}

	key := captureHealthBucketKey{minute: at.Truncate(time.Minute), reason: reason}
	t.mu.Lock()
	if len(t.incidents) == captureHealthIncidentCapacity {
		copy(t.incidents, t.incidents[1:])
		t.incidents[len(t.incidents)-1] = incident
	} else {
		t.incidents = append(t.incidents, incident)
	}
	bucket := t.buckets[key]
	bucket.MinuteBucket = key.minute
	bucket.InstanceID = t.instanceID
	bucket.Reason = string(reason)
	bucket.DroppedRecords += records
	bucket.DroppedBytes += bytes
	bucket.WorkerQueuePeak = maxInt64(bucket.WorkerQueuePeak, workerCurrent)
	bucket.WriterQueuePeak = maxInt64(bucket.WriterQueuePeak, writerCurrent)
	bucket.InFlightBytePeak = maxInt64(bucket.InFlightBytePeak, inFlightCurrent)
	if errorSummary != "" {
		bucket.LastError = errorSummary
	}
	t.buckets[key] = bucket
	t.mu.Unlock()
}

func safeCaptureHealthErrorSummary(reason CaptureDropReason, err error) string {
	if err == nil {
		return ""
	}
	switch reason {
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
		return sanitizeCaptureHealthError(err)
	}
}

func (t *captureHealthTracker) recordHistoryBucketsDropped(count uint64) {
	if t != nil && count > 0 {
		t.historyDroppedBuckets.Add(count)
	}
}

func sanitizeCaptureHealthError(err error) string {
	if err == nil {
		return ""
	}
	redacted := logredact.RedactText(err.Error(), "username", "user", "token", "secret", "api_key", "apikey", "dsn")
	if len(redacted) > captureHealthErrorMaxBytes {
		redacted = redacted[:captureHealthErrorMaxBytes]
	}
	return redacted
}

func (t *captureHealthTracker) snapshot() CaptureHealthSnapshot {
	if t == nil {
		return CaptureHealthSnapshot{DroppedByReason: map[string]CaptureReasonStats{}, RecentIncidents: []CaptureLossIncident{}}
	}
	droppedByReason := make(map[string]CaptureReasonStats, len(t.dropByReason))
	for _, reason := range captureDropReasons {
		counter := t.dropByReason[reason]
		droppedByReason[string(reason)] = CaptureReasonStats{Records: counter.records.Load(), Bytes: counter.bytes.Load()}
	}
	t.mu.Lock()
	incidents := append([]CaptureLossIncident(nil), t.incidents...)
	t.mu.Unlock()

	snapshot := CaptureHealthSnapshot{
		StartedAt: t.startedAt, SubmittedRecords: t.submitted.Load(), AcceptedRecords: t.accepted.Load(),
		WrittenRecords: t.written.Load(), DroppedRecords: t.dropped.Load(), DroppedBytes: t.dropBytes.Load(),
		DroppedByReason: droppedByReason, WorkerQueue: t.workerQueue.snapshot(), WriterQueue: t.writerQueue.snapshot(),
		InFlightBytes: t.inFlightBytes.snapshot(), LastDropReason: atomicString(&t.lastDropReason),
		LastError: atomicString(&t.lastError), RecentIncidents: incidents,
		HistoryDroppedBuckets: t.historyDroppedBuckets.Load(),
	}
	if value := t.lastSuccessUnixNano.Load(); value > 0 {
		at := time.Unix(0, value).UTC()
		snapshot.LastSuccessAt = &at
	}
	if value := t.lastDropUnixNano.Load(); value > 0 {
		at := time.Unix(0, value).UTC()
		snapshot.LastDropAt = &at
	}
	return snapshot
}

func atomicString(value *atomic.Value) string {
	if value == nil {
		return ""
	}
	result, _ := value.Load().(string)
	return result
}

func (t *captureHealthTracker) takeBucketsBefore(cutoff time.Time, includeCurrent bool) []CaptureHealthEvent {
	if t == nil {
		return nil
	}
	cutoff = cutoff.UTC().Truncate(time.Minute)
	t.mu.Lock()
	events := make([]CaptureHealthEvent, 0, len(t.buckets))
	for key, event := range t.buckets {
		if includeCurrent || key.minute.Before(cutoff) {
			events = append(events, event)
			delete(t.buckets, key)
		}
	}
	t.mu.Unlock()
	sort.Slice(events, func(i, j int) bool {
		if events[i].MinuteBucket.Equal(events[j].MinuteBucket) {
			if events[i].InstanceID == events[j].InstanceID {
				return events[i].Reason < events[j].Reason
			}
			return events[i].InstanceID < events[j].InstanceID
		}
		return events[i].MinuteBucket.Before(events[j].MinuteBucket)
	})
	return events
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
