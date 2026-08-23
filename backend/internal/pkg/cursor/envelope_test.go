package cursor

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
)

func TestEncodeFrameRoundTrip(t *testing.T) {
	want := []byte("payload")
	frame, err := NewFrameReader(bytes.NewReader(EncodeFrame(want, false))).Next()
	require.NoError(t, err)
	require.Equal(t, want, frame.Payload)
	require.False(t, frame.EndStream)
}

func TestFrameReaderRecognizesAllConnectFlags(t *testing.T) {
	tests := []struct {
		name       string
		flag       byte
		body       []byte
		want       []byte
		compressed bool
		endStream  bool
	}{
		{name: "data", flag: 0x00, body: []byte("proto"), want: []byte("proto"), compressed: false, endStream: false},
		{name: "compressed data", flag: 0x01, body: gzipFixture(t, []byte("proto")), want: []byte("proto"), compressed: true, endStream: false},
		{name: "trailer", flag: 0x02, body: []byte("{}"), want: []byte("{}"), compressed: false, endStream: true},
		{name: "compressed trailer", flag: 0x03, body: gzipFixture(t, []byte("{}")), want: []byte("{}"), compressed: true, endStream: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := NewFrameReader(bytes.NewReader(rawFrame(tc.flag, tc.body))).Next()
			require.NoError(t, err)
			require.Equal(t, tc.compressed, frame.Compressed)
			require.Equal(t, tc.endStream, frame.EndStream)
			require.Equal(t, tc.want, frame.Payload)
		})
	}
}

func TestFrameReaderRejectsEncodedFrameOver64MiB(t *testing.T) {
	header := []byte{0x00, 0x04, 0x00, 0x00, 0x01}
	_, err := NewFrameReader(bytes.NewReader(header)).Next()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds max")
}

func TestFrameReaderRejectsGzipExpansionOver64MiB(t *testing.T) {
	bomb := gzipFixture(t, make([]byte, maxDecompressedFrameSize+1))
	require.Less(t, len(bomb), maxFrameSize)
	_, err := NewFrameReader(bytes.NewReader(rawFrame(0x01, bomb))).Next()
	require.Error(t, err)
	require.Contains(t, err.Error(), "exceeds max")
}

func TestFrameReaderRejectsTruncatedPayload(t *testing.T) {
	_, err := NewFrameReader(bytes.NewReader([]byte{0x00, 0x00, 0x00, 0x00, 0x02, 'x'})).Next()
	require.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestFrameReaderHandlesPartialReads(t *testing.T) {
	stream := append(EncodeFrame([]byte("one"), false), EncodeFrame([]byte("two"), false)...)
	reader := NewFrameReader(iotest.OneByteReader(bytes.NewReader(stream)))
	first, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("one"), first.Payload)
	second, err := reader.Next()
	require.NoError(t, err)
	require.Equal(t, []byte("two"), second.Payload)
}

func rawFrame(flag byte, body []byte) []byte {
	frame := make([]byte, 5+len(body))
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

func gzipFixture(t *testing.T, payload []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	writer := gzip.NewWriter(&out)
	_, err := writer.Write(payload)
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return out.Bytes()
}
