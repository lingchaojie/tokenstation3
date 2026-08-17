package sidecar

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/Wei-Shaw/sub2api/internal/capture/upload"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestProtocolSpoolExtractionRetainsPayloadStopReasonUnlessFinalOverrides(t *testing.T) {
	root := t.TempDir()
	store, err := spool.Open(spool.Config{RootDir: filepath.Join(root, "spool")})
	require.NoError(t, err)
	_, err = store.Recover(context.Background())
	require.NoError(t, err)

	socketPath := filepath.Join(root, "capture.sock")
	server := protocol.NewServer(protocol.ServerConfig{SocketPath: socketPath, MaxSessions: 8}, store)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	waitForRuntimeSocket(t, socketPath)
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		require.NoError(t, <-done)
	})
	client := protocol.NewClient(protocol.ClientConfig{
		SocketPath: socketPath, DialTimeout: time.Second, WriteTimeout: time.Second, ReadTimeout: time.Second,
	})
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	awsPayload := append(
		sidecarAWSEventStreamFixture(t, "messageMetadataEvent", sidecarMustJSON(t, map[string]any{
			"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{
				"uncachedInputTokens": 11, "outputTokens": 5, "cacheReadInputTokens": 3, "cacheWriteInputTokens": 2,
			}},
		})),
		sidecarAWSEventStreamFixture(t, "messageStopEvent", sidecarMustJSON(t, map[string]any{
			"messageStopEvent": map[string]any{"stopReason": "aws-stop", "signature": "aws-signature"},
		}))...,
	)
	tests := []struct {
		name       string
		format     model.PayloadFormat
		response   []byte
		final      model.Final
		wantStop   string
		wantSigned bool
	}{
		{
			name: "json empty final", format: model.PayloadJSON,
			response: []byte(`{"stop_reason":"json-stop","content":[{"type":"thinking","signature":"json-signature"}],"usage":{"input_tokens":7}}`),
			wantStop: "json-stop", wantSigned: true,
		},
		{
			name: "json explicit final override", format: model.PayloadJSON,
			response: []byte(`{"stop_reason":"json-stop"}`),
			final:    model.Final{StopReason: "pre_commit_disconnect"}, wantStop: "pre_commit_disconnect",
		},
		{
			name: "sse empty final", format: model.PayloadSSE,
			response: []byte("event: message_delta\ndata: {\"delta\":{\"stop_reason\":\"sse-stop\"},\"signature\":\"sse-signature\",\"usage\":{\"output_tokens\":9}}\n\n"),
			wantStop: "sse-stop", wantSigned: true,
		},
		{
			name: "sse explicit final override", format: model.PayloadSSE,
			response: []byte("data: {\"delta\":{\"stop_reason\":\"sse-stop\"}}\n\n"),
			final:    model.Final{StopReason: "pre_commit_disconnect"}, wantStop: "pre_commit_disconnect",
		},
		{
			name: "aws empty final", format: model.PayloadAWSEventStream,
			response: awsPayload, wantStop: "aws-stop", wantSigned: true,
		},
		{
			name: "aws explicit final override", format: model.PayloadAWSEventStream,
			response: awsPayload, final: model.Final{StopReason: "pre_commit_disconnect"},
			wantStop: "pre_commit_disconnect", wantSigned: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id := uuid.New()
			attempt, beginErr := client.Begin(context.Background(), model.Begin{
				CaptureID: id, CapturedAt: time.Now().UTC(), Format: test.format,
				Policy: model.ContentPolicy{StoreResponseBody: true},
			})
			require.NoError(t, beginErr)
			midpoint := len(test.response) / 2
			require.True(t, attempt.WriteResponse(test.response[:midpoint]))
			require.True(t, attempt.WriteResponse(test.response[midpoint:]))
			require.True(t, attempt.Finalize(test.final))
			require.True(t, attempt.Commit())

			var manifest model.Manifest
			require.Eventually(t, func() bool {
				for _, ref := range store.Ready() {
					if ref.CaptureID == id {
						manifest = ref.Manifest
						return true
					}
				}
				return false
			}, time.Second, time.Millisecond)
			require.Equal(t, test.format, manifest.Begin.Format)
			require.Equal(t, test.wantStop, manifest.Extracted.StopReason)
			require.Equal(t, test.wantSigned, manifest.Extracted.SignaturePresent)
			require.Zero(t, manifest.Extracted.InputTokens, "explicit Final zero remains authoritative")
			require.Zero(t, manifest.Extracted.OutputTokens, "explicit Final zero remains authoritative")
			require.EqualValues(t, len(test.response), manifest.Response.ObservedBytes)
			require.EqualValues(t, len(test.response), manifest.Response.StoredBytes)
			require.Len(t, manifest.Response.SHA256, 64)
		})
	}

	batch, err := store.NextBatch(len(tests), 64<<20)
	require.NoError(t, err)
	require.Len(t, batch.Records, len(tests))
	var encoded bytes.Buffer
	require.NoError(t, (upload.RowBinaryEncoder{}).EncodeBatch(context.Background(), &encoded, batch))
	require.NotEmpty(t, encoded.Bytes())
}

func sidecarMustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func sidecarAWSEventStreamFixture(t *testing.T, eventType string, payload []byte) []byte {
	t.Helper()
	headerName := []byte(":event-type")
	headerValue := []byte(eventType)
	header := make([]byte, 0, 1+len(headerName)+1+2+len(headerValue))
	header = append(header, byte(len(headerName)))
	header = append(header, headerName...)
	header = append(header, 7)
	header = binary.BigEndian.AppendUint16(header, uint16(len(headerValue)))
	header = append(header, headerValue...)
	total := 12 + len(header) + len(payload) + 4
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[0:4], uint32(total))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(header)))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	copy(frame[12:], header)
	copy(frame[12+len(header):], payload)
	binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
	return frame
}
