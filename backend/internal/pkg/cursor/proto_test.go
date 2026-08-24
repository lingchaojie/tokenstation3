package cursor

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWriteFixed64AndDecode(t *testing.T) {
	var w Writer
	w.WriteDouble(2, 1.5)
	require.Equal(t, []byte{0x11, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xf8, 0x3f}, w.Bytes())

	fields, err := Decode(w.Bytes())
	require.NoError(t, err)
	require.Equal(t, math.Float64bits(1.5), fields.Varint(2))
}

func TestDecodePreservesRepeatedBytesInWireOrder(t *testing.T) {
	fields, err := Decode([]byte{0x0a, 0x01, 'a', 0x0a, 0x01, 'b', 0x0a, 0x01, 'c'})
	require.NoError(t, err)
	require.Equal(t, [][]byte{[]byte("a"), []byte("b"), []byte("c")}, fields.AllBytes(1))
	require.Equal(t, "c", fields.String(1))
}

func TestDecodeRejectsTruncatedLengthDelimitedField(t *testing.T) {
	_, err := Decode([]byte{0x0a, 0x05, 'a'})
	require.Error(t, err)
}

func TestDecodeRejectsInvalidWireType(t *testing.T) {
	_, err := Decode([]byte{0x0b}) // field 1, deprecated start-group wire type
	require.Error(t, err)
}

func TestReadVarintRejectsOverflow(t *testing.T) {
	_, _, err := ReadVarint([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x02})
	require.Error(t, err)
}

func TestEncodeProtobufValueSortsMapKeys(t *testing.T) {
	value := map[string]any{"z": true, "a": "first"}
	first := encodeProtobufValue(value)
	require.Equal(t, []byte{
		0x2a, 0x17, 0x0a, 0x0c, 0x0a, 0x01, 'a', 0x12, 0x07, 0x1a, 0x05, 'f', 'i', 'r', 's', 't', 0x0a, 0x07, 0x0a, 0x01, 'z', 0x12, 0x02, 0x20, 0x01,
	}, first)
	for range 10 {
		require.Equal(t, first, encodeProtobufValue(value))
	}
}

func TestDecodeProtobufValueBoundsRecursion(t *testing.T) {
	var value any = "leaf"
	for range maxProtobufValueDepth + 8 {
		value = []any{value}
	}

	decoded := decodeProtobufValue(encodeProtobufValue(value))
	require.LessOrEqual(t, protobufValueDepth(decoded), maxProtobufValueDepth)
}

func protobufValueDepth(value any) int {
	items, ok := value.([]any)
	if !ok || len(items) == 0 {
		return 0
	}
	return 1 + protobufValueDepth(items[0])
}
