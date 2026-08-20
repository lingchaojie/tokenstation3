// Package model defines the stable capture-sidecar data contracts.
package model

import (
	"time"

	"github.com/google/uuid"
)

type ContentPolicy struct {
	StoreRequestBody     bool `json:"store_request_body"`
	StoreResponseBody    bool `json:"store_response_body"`
	StoreRequestHeaders  bool `json:"store_request_headers"`
	StoreResponseHeaders bool `json:"store_response_headers"`
}

type PayloadFormat string

const (
	PayloadJSON           PayloadFormat = "json"
	PayloadSSE            PayloadFormat = "sse"
	PayloadAWSEventStream PayloadFormat = "aws_event_stream"
)

type BodyStat struct {
	ObservedBytes uint64 `json:"observed_bytes"`
	StoredBytes   uint64 `json:"stored_bytes"`
	SHA256        string `json:"sha256"`
	Truncated     bool   `json:"truncated"`
	Complete      bool   `json:"complete"`
}

type HeaderStat BodyStat

type Begin struct {
	CaptureID        uuid.UUID     `json:"capture_id"`
	CapturedAt       time.Time     `json:"captured_at"`
	RequestID        string        `json:"request_id"`
	SessionID        string        `json:"session_id,omitempty"`
	Platform         string        `json:"platform"`
	RequestedModel   string        `json:"requested_model"`
	UpstreamModel    string        `json:"upstream_model"`
	UpstreamEndpoint string        `json:"upstream_endpoint"`
	Stream           bool          `json:"stream"`
	Format           PayloadFormat `json:"format"`
	Policy           ContentPolicy `json:"policy"`
}

type Final struct {
	HTTPStatus          uint16 `json:"http_status"`
	InputTokens         uint32 `json:"input_tokens"`
	OutputTokens        uint32 `json:"output_tokens"`
	CacheReadTokens     uint32 `json:"cache_read_tokens"`
	CacheCreationTokens uint32 `json:"cache_creation_tokens"`
	StopReason          string `json:"stop_reason"`
	ResponseComplete    bool   `json:"response_complete"`
}

type Extracted struct {
	SessionID           string `json:"session_id"`
	ThinkingEffort      string `json:"thinking_effort"`
	ThinkingType        string `json:"thinking_type"`
	SignaturePresent    bool   `json:"signature_present"`
	InputTokens         uint32 `json:"input_tokens"`
	OutputTokens        uint32 `json:"output_tokens"`
	CacheReadTokens     uint32 `json:"cache_read_tokens"`
	CacheCreationTokens uint32 `json:"cache_creation_tokens"`
	StopReason          string `json:"stop_reason"`
}

type FileStat struct {
	Name               string `json:"name"`
	CompressedBytes    uint64 `json:"compressed_bytes"`
	UncompressedBytes  uint64 `json:"uncompressed_bytes"`
	CompressedSHA256   string `json:"compressed_sha256"`
	UncompressedSHA256 string `json:"uncompressed_sha256"`
}

type Manifest struct {
	SpoolVersion    uint16     `json:"spool_version"`
	CaptureVersion  uint16     `json:"capture_version"`
	CaptureID       uuid.UUID  `json:"capture_id"`
	Begin           Begin      `json:"begin"`
	Final           Final      `json:"final"`
	Extracted       Extracted  `json:"extracted"`
	Request         BodyStat   `json:"request"`
	Response        BodyStat   `json:"response"`
	RequestHeaders  HeaderStat `json:"request_headers"`
	ResponseHeaders HeaderStat `json:"response_headers"`
	Files           []FileStat `json:"files"`
}

type Status struct {
	HealthSourceID        uuid.UUID         `json:"health_source_id"`
	SpoolReady            bool              `json:"spool_ready"`
	DeliveryReady         bool              `json:"delivery_ready"`
	SpoolUsedBytes        int64             `json:"spool_used_bytes"`
	SpoolMaxBytes         int64             `json:"spool_max_bytes"`
	FilesystemFreeBytes   int64             `json:"filesystem_free_bytes"`
	ReadyRecords          int64             `json:"ready_records"`
	OldestReadyAgeSeconds int64             `json:"oldest_ready_age_seconds"`
	CurrentBatchID        string            `json:"current_batch_id"`
	UploadRetries         uint64            `json:"upload_retries"`
	DroppedRecords        uint64            `json:"dropped_records"`
	DroppedByReason       map[string]uint64 `json:"dropped_by_reason"`
	HealthBuckets         []HealthBucket    `json:"health_buckets"`
	LastUploadAt          *time.Time        `json:"last_upload_at"`
}

type HealthBucket struct {
	Minute         time.Time         `json:"minute"`
	DroppedRecords map[string]uint64 `json:"dropped_records"`
	DroppedBytes   map[string]uint64 `json:"dropped_bytes"`
	UploadRetries  uint64            `json:"upload_retries"`
}
