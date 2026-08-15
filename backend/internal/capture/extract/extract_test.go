package extract

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/stretchr/testify/require"
)

type chunkReader struct {
	Chunks [][]byte
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.Chunks) == 0 {
		return 0, io.EOF
	}
	chunk := r.Chunks[0]
	r.Chunks = r.Chunks[1:]
	n := copy(p, chunk)
	if n < len(chunk) {
		r.Chunks = append([][]byte{chunk[n:]}, r.Chunks...)
	}
	return n, nil
}

func TestExtractJSONMetadataAndAuthoritativeFinal(t *testing.T) {
	request := strings.NewReader(`{
		"metadata":{"user_id":"{\"device_id\":\"device-1\",\"session_id\":\"session-1\"}"},
		"output_config":{"effort":"HIGH"},
		"thinking":{"type":"adaptive"}
	}`)
	response := strings.NewReader(`{
		"stop_reason":"payload-stop",
		"usage":{"input_tokens":7,"output_tokens":8,"cache_read_input_tokens":2,"cache_creation_input_tokens":1},
		"content":[{"type":"thinking","signature":"signed"}]
	}`)

	got, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadJSON,
		Request:  request,
		Response: response,
		Final: model.Final{
			InputTokens:         70,
			OutputTokens:        80,
			CacheReadTokens:     20,
			CacheCreationTokens: 10,
			StopReason:          "final-stop",
		},
	})

	require.NoError(t, err)
	require.Equal(t, model.Extracted{
		SessionID:           "session-1",
		ThinkingEffort:      "high",
		ThinkingType:        "adaptive",
		SignaturePresent:    true,
		InputTokens:         70,
		OutputTokens:        80,
		CacheReadTokens:     20,
		CacheCreationTokens: 10,
		StopReason:          "final-stop",
	}, got)
}

func TestExtractJSONInvalidMetadataUserIDFallsBackToConversationID(t *testing.T) {
	got, err := FromReaders(context.Background(), Input{
		Format:  model.PayloadJSON,
		Request: strings.NewReader(`{"metadata":{"user_id":"not-a-metadata-id"},"conversation_id":"conversation-fallback"}`),
	})

	require.NoError(t, err)
	require.Equal(t, "conversation-fallback", got.SessionID)
}

func TestExtractJSONInvalidLegacyMetadataUserIDFallsBack(t *testing.T) {
	got, err := FromReaders(context.Background(), Input{
		Format: model.PayloadJSON,
		Request: strings.NewReader(`{"metadata":{"user_id":"user_short_account_bad!_session_11111111-1111-1111-1111-111111111111"},` +
			`"session_id":"session-fallback"}`),
	})

	require.NoError(t, err)
	require.Equal(t, "session-fallback", got.SessionID)
}

func TestStreamFinalizeTreatsExplicitZeroFinalUsageAsAuthoritative(t *testing.T) {
	stream, err := New(context.Background(), model.PayloadJSON)
	require.NoError(t, err)
	require.NoError(t, stream.FeedResponse([]byte(`{"stop_reason":"payload","usage":{"input_tokens":7,"output_tokens":8}}`)))

	got, err := stream.Finalize(model.Final{})

	require.NoError(t, err)
	require.Zero(t, got.InputTokens)
	require.Zero(t, got.OutputTokens)
	require.Empty(t, got.StopReason)
}

func TestExtractJSONOpenAIUsageAliasPriorityDoesNotDependOnObjectOrder(t *testing.T) {
	got, err := FromReaders(context.Background(), Input{
		Format: model.PayloadJSON,
		Response: strings.NewReader(`{"usage":{` +
			`"input_tokens_details":{"cached_tokens":9,"cache_write_tokens":8},` +
			`"cached_tokens":1,"cache_creation_tokens":2}}`),
	})

	require.NoError(t, err)
	require.EqualValues(t, 9, got.CacheReadTokens)
	require.EqualValues(t, 8, got.CacheCreationTokens)
}

func TestExtractJSONMalformedGeminiCounterDoesNotCreatePartialSum(t *testing.T) {
	got, err := FromReaders(context.Background(), Input{
		Format: model.PayloadJSON,
		Response: strings.NewReader(`{"usageMetadata":{` +
			`"candidatesTokenCount":{"inputTokens":1},"thoughtsTokenCount":2}}`),
		Initial: model.Extracted{OutputTokens: 7},
	})

	require.NoError(t, err)
	require.Zero(t, got.InputTokens)
	require.EqualValues(t, 7, got.OutputTokens)
}

func TestExtractSSEAcrossArbitraryChunkBoundariesAndDone(t *testing.T) {
	r := &chunkReader{Chunks: [][]byte{
		[]byte("event: message_start\r\nda"),
		[]byte("ta: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7,\"cache_read_input_tokens\":2}}}\r\n\r"),
		[]byte("\nevent: message_delta\ndata: {\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":9}}\n\n"),
		[]byte("data: [DO"), []byte("NE]\n\n"),
	}}

	got, err := FromReaders(context.Background(), Input{Format: model.PayloadSSE, Response: r})

	require.NoError(t, err)
	require.EqualValues(t, 7, got.InputTokens)
	require.EqualValues(t, 9, got.OutputTokens)
	require.EqualValues(t, 2, got.CacheReadTokens)
	require.Equal(t, "end_turn", got.StopReason)
}

func TestExtractSSEAcceptsBareCRLineEndings(t *testing.T) {
	r := &chunkReader{Chunks: [][]byte{
		[]byte("data: {\"usage\":{\"input_tokens\":7}}\r"),
		[]byte("\rdata: {\"usage\":{\"output_tokens\":9}}\r\r"),
	}}

	got, err := FromReaders(context.Background(), Input{Format: model.PayloadSSE, Response: r})

	require.NoError(t, err)
	require.EqualValues(t, 7, got.InputTokens)
	require.EqualValues(t, 9, got.OutputTokens)
}

func TestExtractSSERejectsOversizedMetadataEventWithoutRetainingIt(t *testing.T) {
	stream, err := New(context.Background(), model.PayloadSSE)
	require.NoError(t, err)

	chunk := bytes.Repeat([]byte("x"), 64<<10)
	require.NoError(t, stream.FeedResponse([]byte("data: {\"padding\":\"")))
	var feedErr error
	for i := 0; i < 18; i++ {
		if err := stream.FeedResponse(chunk); err != nil && feedErr == nil {
			feedErr = err
		}
	}
	if err := stream.FeedResponse([]byte("\"}\n\n")); err != nil && feedErr == nil {
		feedErr = err
	}
	_, finalizeErr := stream.Finalize(model.Final{})

	require.ErrorIs(t, firstError(feedErr, finalizeErr), ErrMetadataLimit)
}

func TestExtractAWSFramesAcrossBoundariesAndValidatesCRC(t *testing.T) {
	metadata := awsEventStreamFixture(t, "messageMetadataEvent", mustJSON(t, map[string]any{
		"messageMetadataEvent": map[string]any{"tokenUsage": map[string]any{
			"uncachedInputTokens":   11,
			"outputTokens":          5,
			"cacheReadInputTokens":  3,
			"cacheWriteInputTokens": 2,
		}},
	}))
	stop := awsEventStreamFixture(t, "messageStopEvent", mustJSON(t, map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))
	payload := append(metadata, stop...)
	chunks := make([][]byte, 0, len(payload))
	for _, b := range payload {
		chunks = append(chunks, []byte{b})
	}

	got, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadAWSEventStream,
		Response: &chunkReader{Chunks: chunks},
	})

	require.NoError(t, err)
	require.EqualValues(t, 11, got.InputTokens)
	require.EqualValues(t, 5, got.OutputTokens)
	require.EqualValues(t, 3, got.CacheReadTokens)
	require.EqualValues(t, 2, got.CacheCreationTokens)
	require.Equal(t, "end_turn", got.StopReason)

	corrupt := append([]byte(nil), metadata...)
	corrupt[len(corrupt)-1] ^= 0xff
	_, err = FromReaders(context.Background(), Input{
		Format:   model.PayloadAWSEventStream,
		Response: bytes.NewReader(corrupt),
	})
	require.ErrorIs(t, err, ErrMalformedPayload)
}

func TestExtractAWSRejectsMalformedHeaderEncodingWithValidMessageCRC(t *testing.T) {
	frame := awsEventStreamFixture(t, "messageStopEvent", mustJSON(t, map[string]any{
		"messageStopEvent": map[string]any{"stopReason": "end_turn"},
	}))
	typeOffset := 12 + 1 + len(":event-type")
	frame[typeOffset] = 0
	binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))

	_, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadAWSEventStream,
		Response: bytes.NewReader(frame),
	})

	require.ErrorIs(t, err, ErrMalformedPayload)
}

func TestExtractAWSAcceptsProviderNativeFlatUsageAndStopShapes(t *testing.T) {
	usage := awsEventStreamFixture(t, "messageMetadataEvent", mustJSON(t, map[string]any{
		"tokenUsage": map[string]any{
			"uncachedInputTokens":   17,
			"outputTokens":          6,
			"cacheReadInputTokens":  4,
			"cacheWriteInputTokens": 3,
		},
	}))
	stop := awsEventStreamFixture(t, "messageStopEvent", mustJSON(t, map[string]any{
		"stopReason": "max_tokens",
	}))

	got, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadAWSEventStream,
		Response: bytes.NewReader(append(usage, stop...)),
	})

	require.NoError(t, err)
	require.Equal(t, model.Extracted{
		InputTokens:         17,
		OutputTokens:        6,
		CacheReadTokens:     4,
		CacheCreationTokens: 3,
		StopReason:          "max_tokens",
	}, got)
}

func TestExtractJSONIgnoresNestedContentStopReason(t *testing.T) {
	got, err := FromReaders(context.Background(), Input{
		Format: model.PayloadJSON,
		Response: strings.NewReader(`{
			"stop_reason":"stop",
			"content":{"stopReason":"user-supplied-content"}
		}`),
	})

	require.NoError(t, err)
	require.Equal(t, "stop", got.StopReason)
}

func TestExtractAWSLargeNonMetadataFrameUsesBoundedScratch(t *testing.T) {
	payload := awsEventStreamFixture(t, "contentBlockDelta", bytes.Repeat([]byte("x"), 2<<20))
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	got, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadAWSEventStream,
		Response: bytes.NewReader(payload),
	})

	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	require.NoError(t, err)
	require.Equal(t, model.Extracted{}, got)
	require.Less(t, after.TotalAlloc-before.TotalAlloc, uint64(4<<20))
}

func TestExtractionErrorsAreSanitized(t *testing.T) {
	secret := "sk-secret-body-value"
	_, err := FromReaders(context.Background(), Input{
		Format:   model.PayloadJSON,
		Response: strings.NewReader(`{"usage":{"input_tokens":1},"secret":"` + secret),
	})

	require.ErrorIs(t, err, ErrMalformedPayload)
	require.NotContains(t, err.Error(), secret)
	require.NotContains(t, err.Error(), "input_tokens")
}

func TestNewRejectsUnsupportedFormat(t *testing.T) {
	_, err := New(context.Background(), model.PayloadFormat("opaque"))
	require.ErrorIs(t, err, ErrUnsupportedFormat)
}

func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func awsEventStreamFixture(t *testing.T, eventType string, payload []byte) []byte {
	t.Helper()
	headerName := []byte(":event-type")
	headerValue := []byte(eventType)
	headersLen := 1 + len(headerName) + 1 + 2 + len(headerValue)
	totalLen := 12 + headersLen + len(payload) + 4
	frame := make([]byte, totalLen)
	binary.BigEndian.PutUint32(frame[0:4], uint32(totalLen))
	binary.BigEndian.PutUint32(frame[4:8], uint32(headersLen))
	binary.BigEndian.PutUint32(frame[8:12], crc32.ChecksumIEEE(frame[:8]))
	offset := 12
	frame[offset] = byte(len(headerName))
	offset++
	copy(frame[offset:], headerName)
	offset += len(headerName)
	frame[offset] = 7
	offset++
	binary.BigEndian.PutUint16(frame[offset:offset+2], uint16(len(headerValue)))
	offset += 2
	copy(frame[offset:], headerValue)
	offset += len(headerValue)
	copy(frame[offset:], payload)
	binary.BigEndian.PutUint32(frame[len(frame)-4:], crc32.ChecksumIEEE(frame[:len(frame)-4]))
	return frame
}
