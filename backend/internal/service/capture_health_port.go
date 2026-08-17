package service

import (
	"context"
	"time"
)

// CaptureHealthEvent is one durable minute-level archive loss aggregate.
// It intentionally contains no request or response body data.
type CaptureHealthEvent struct {
	MinuteBucket              time.Time `json:"minute_bucket"`
	InstanceID                string    `json:"instance_id"`
	Reason                    string    `json:"reason"`
	DroppedRecords            int64     `json:"dropped_records"`
	DroppedBytes              int64     `json:"dropped_bytes"`
	SpoolUsedBytesPeak        int64     `json:"spool_used_bytes_peak"`
	ReadyRecordsPeak          int64     `json:"ready_records_peak"`
	OldestReadyAgeSecondsPeak int64     `json:"oldest_ready_age_seconds_peak"`
	UploadRetries             int64     `json:"upload_retries"`
	SidecarRestarts           int64     `json:"sidecar_restarts"`
	LastError                 string    `json:"last_error"`

	// Deprecated database compatibility fields are retained for Task 12's
	// production cleanup, but are never serialized through the active API.
	WorkerQueuePeak  int64 `json:"-"`
	WriterQueuePeak  int64 `json:"-"`
	InFlightBytePeak int64 `json:"-"`
}

// CaptureHealthRepository persists only aggregated archive-loss health data.
type CaptureHealthRepository interface {
	UpsertEvents(ctx context.Context, events []CaptureHealthEvent) error
	ListEvents(ctx context.Context, start, end time.Time) ([]CaptureHealthEvent, error)
	ListLatestEventsBefore(ctx context.Context, before time.Time, instanceIDs, reasons []string) ([]CaptureHealthEvent, error)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
