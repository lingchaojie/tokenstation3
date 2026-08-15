package protocol

import (
	"encoding/binary"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestFrameHeaderRoundTrip(t *testing.T) {
	id := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	h := Header{Version: 2, Kind: KindRequestChunk, CaptureID: id, Length: 65536}
	b := h.MarshalBinary()

	require.Len(t, b, 28)
	require.Equal(t, []byte("CSP2"), b[:4])
	require.Equal(t, byte(0), b[7])
	got, err := ParseHeader(b)
	require.NoError(t, err)
	require.Equal(t, h, got)
}

func TestParseHeaderRejectsOversizedPayload(t *testing.T) {
	b := validHeaderBytes()
	binary.BigEndian.PutUint32(b[24:28], 65537)

	_, err := ParseHeader(b)
	require.ErrorIs(t, err, ErrFrameTooLarge)
}

func TestParseHeaderRejectsMalformedHeader(t *testing.T) {
	tests := []struct {
		name string
		edit func([]byte) []byte
		err  error
	}{
		{
			name: "short",
			edit: func(b []byte) []byte { return b[:len(b)-1] },
			err:  ErrInvalidHeader,
		},
		{
			name: "magic",
			edit: func(b []byte) []byte { copy(b[:4], "NOPE"); return b },
			err:  ErrInvalidMagic,
		},
		{
			name: "reserved byte",
			edit: func(b []byte) []byte { b[7] = 1; return b },
			err:  ErrInvalidHeader,
		},
		{
			name: "unknown kind",
			edit: func(b []byte) []byte { b[6] = 255; return b },
			err:  ErrUnknownKind,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseHeader(test.edit(validHeaderBytes()))
			require.ErrorIs(t, err, test.err)
		})
	}
}

func TestAllProtocolKindsHaveStableDistinctWireValues(t *testing.T) {
	kinds := []Kind{
		KindHandshake,
		KindBegin,
		KindRequestHeaders,
		KindResponseHeaders,
		KindRequestChunk,
		KindResponseChunk,
		KindFinal,
		KindCommit,
		KindAbort,
		KindStatusRequest,
		KindStatusResponse,
		KindProtocolError,
	}
	seen := make(map[Kind]struct{}, len(kinds))
	for index, kind := range kinds {
		require.Equal(t, Kind(index+1), kind)
		_, duplicate := seen[kind]
		require.False(t, duplicate)
		seen[kind] = struct{}{}
	}
}

func validHeaderBytes() []byte {
	return Header{
		Version:   ProtocolVersion,
		Kind:      KindBegin,
		CaptureID: uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff"),
		Length:    32,
	}.MarshalBinary()
}
