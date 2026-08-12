package service

import (
	"context"
	"time"
)

// CaptureHealthEvent is one durable minute-level archive loss aggregate.
// It intentionally contains no request or response body data.
type CaptureHealthEvent struct {
	MinuteBucket     time.Time `json:"minute_bucket"`
	InstanceID       string    `json:"instance_id"`
	Reason           string    `json:"reason"`
	DroppedRecords   int64     `json:"dropped_records"`
	DroppedBytes     int64     `json:"dropped_bytes"`
	WorkerQueuePeak  int64     `json:"worker_queue_peak"`
	WriterQueuePeak  int64     `json:"writer_queue_peak"`
	InFlightBytePeak int64     `json:"in_flight_bytes_peak"`
	LastError        string    `json:"last_error"`
}

// CaptureHealthRepository persists only aggregated archive-loss health data.
type CaptureHealthRepository interface {
	UpsertEvents(ctx context.Context, events []CaptureHealthEvent) error
	ListEvents(ctx context.Context, start, end time.Time) ([]CaptureHealthEvent, error)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
