//go:build unit

package handler

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func task8BedrockHandlerExceptionFrame(t *testing.T) []byte {
	t.Helper()
	headers := [][2]string{
		{":event-type", "modelStreamErrorException"},
		{":message-type", "exception"},
		{":exception-type", "modelStreamErrorException"},
	}
	var headerBytes bytes.Buffer
	for _, header := range headers {
		require.NoError(t, headerBytes.WriteByte(byte(len(header[0]))))
		_, err := headerBytes.WriteString(header[0])
		require.NoError(t, err)
		require.NoError(t, headerBytes.WriteByte(7))
		require.NoError(t, binary.Write(&headerBytes, binary.BigEndian, uint16(len(header[1]))))
		_, err = headerBytes.WriteString(header[1])
		require.NoError(t, err)
	}
	payload := []byte(`{"message":"account 801 provider stream failed"}`)
	totalLength := uint32(12 + headerBytes.Len() + len(payload) + 4)
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLength)
	binary.BigEndian.PutUint32(prelude[4:8], uint32(headerBytes.Len()))

	var frame bytes.Buffer
	_, _ = frame.Write(prelude)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(prelude)))
	_, _ = frame.Write(headerBytes.Bytes())
	_, _ = frame.Write(payload)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func TestTask8BedrockPreOutputFailureTraversesHandlerAndFinalizesOnlySecondAccount(t *testing.T) {
	const groupID, userID = int64(9801), int64(9802)
	first := newBedrockHandlerAccount(801, 1)
	second := newBedrockHandlerAccount(802, 2)
	secondBody := bedrockHandlerStream(t,
		map[string]any{"type": "message_start", "message": map[string]any{"id": "msg-802", "type": "message", "role": "assistant", "content": []any{}, "usage": map[string]any{"input_tokens": 3, "output_tokens": 0}}},
		map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}},
		map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": "from-802"}},
		map[string]any{"type": "content_block_stop", "index": 0},
		map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn"}, "usage": map[string]any{"output_tokens": 1}},
		map[string]any{"type": "message_stop"},
	)
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		801: {status: http.StatusOK, contentType: "application/vnd.amazon.eventstream", body: task8BedrockHandlerExceptionFrame(t)},
		802: {status: http.StatusOK, contentType: "application/vnd.amazon.eventstream", body: secondBody},
	}}
	inbound := `{"model":"claude-sonnet-4-6","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`

	got := runBedrockMessagesHandler(t, groupID, userID, []*service.Account{first, second}, upstream, inbound, false)
	calls, _ := upstream.snapshot()

	require.Equal(t, []int64{801, 802}, calls, "only a typed pre-output failure can cross the production handler failover branch")
	require.Equal(t, http.StatusOK, got.recorder.Code)
	require.Contains(t, got.recorder.Body.String(), "from-802")
	require.Len(t, got.captures, 1, "account 801 must not finalize a capture")
	require.Equal(t, "bedrock-account-802", got.captures[0].RequestID)
	require.Len(t, got.usages, 1, "account 801 must not submit usage")
	require.Equal(t, int64(802), got.usages[0].AccountID)
}
