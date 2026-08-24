package cursor

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

const (
	wireVarint  = 0
	wireFixed64 = 1
	wireLen     = 2
	wireFixed32 = 5
)

const maxProtobufValueDepth = 64

var errVarintOverflow = errors.New("cursor: varint overflows 64 bits")

type Writer struct{ buf []byte }

func (w *Writer) Bytes() []byte { return w.buf }

func (w *Writer) Reset() { w.buf = w.buf[:0] }

func (w *Writer) WriteVarint(value uint64) {
	for value >= 0x80 {
		w.buf = append(w.buf, byte(value)|0x80)
		value >>= 7
	}
	w.buf = append(w.buf, byte(value))
}

func (w *Writer) WriteTag(field, wireType int) {
	w.WriteVarint(uint64(field)<<3 | uint64(wireType))
}

func (w *Writer) WriteBytes(field int, value []byte) {
	w.WriteTag(field, wireLen)
	w.WriteVarint(uint64(len(value)))
	w.buf = append(w.buf, value...)
}

func (w *Writer) WriteString(field int, value string) {
	w.WriteTag(field, wireLen)
	w.WriteVarint(uint64(len(value)))
	w.buf = append(w.buf, value...)
}

func (w *Writer) WriteMessage(field int, value []byte) { w.WriteBytes(field, value) }

func (w *Writer) WriteBool(field int, value bool) {
	w.WriteTag(field, wireVarint)
	if value {
		w.WriteVarint(1)
		return
	}
	w.WriteVarint(0)
}

func (w *Writer) WriteInt32(field int, value int32) {
	w.WriteTag(field, wireVarint)
	w.WriteVarint(uint64(int64(value)))
}

func (w *Writer) WriteInt64(field int, value int64) {
	w.WriteTag(field, wireVarint)
	w.WriteVarint(uint64(value))
}

func (w *Writer) WriteDouble(field int, value float64) {
	w.WriteTag(field, wireFixed64)
	var bits [8]byte
	binary.LittleEndian.PutUint64(bits[:], math.Float64bits(value))
	w.buf = append(w.buf, bits[:]...)
}

func ReadVarint(data []byte) (uint64, int, error) {
	var value uint64
	var shift uint
	for index, b := range data {
		if index == 9 && b > 1 {
			return 0, 0, errVarintOverflow
		}
		if b < 0x80 {
			return value | uint64(b)<<shift, index + 1, nil
		}
		value |= uint64(b&0x7f) << shift
		shift += 7
	}
	return 0, 0, io.ErrUnexpectedEOF
}

func ReadTag(data []byte) (field, wireType, n int, err error) {
	value, n, err := ReadVarint(data)
	if err != nil {
		return 0, 0, 0, err
	}
	return int(value >> 3), int(value & 7), n, nil
}

type Value struct {
	WireType int
	Varint   uint64
	Bytes    []byte
}

type Fields map[int][]Value

func Decode(data []byte) (Fields, error) {
	fields := make(Fields)
	for position := 0; position < len(data); {
		field, wireType, consumed, err := ReadTag(data[position:])
		if err != nil {
			return nil, err
		}
		position += consumed

		switch wireType {
		case wireVarint:
			value, consumed, err := ReadVarint(data[position:])
			if err != nil {
				return nil, err
			}
			position += consumed
			fields[field] = append(fields[field], Value{WireType: wireType, Varint: value})
		case wireLen:
			length, consumed, err := ReadVarint(data[position:])
			if err != nil {
				return nil, err
			}
			position += consumed
			if length > uint64(len(data)-position) {
				return nil, io.ErrUnexpectedEOF
			}
			end := position + int(length)
			fields[field] = append(fields[field], Value{WireType: wireType, Bytes: data[position:end]})
			position = end
		case wireFixed64:
			if len(data)-position < 8 {
				return nil, io.ErrUnexpectedEOF
			}
			fields[field] = append(fields[field], Value{WireType: wireType, Varint: binary.LittleEndian.Uint64(data[position:])})
			position += 8
		case wireFixed32:
			if len(data)-position < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			fields[field] = append(fields[field], Value{WireType: wireType, Varint: uint64(binary.LittleEndian.Uint32(data[position:]))})
			position += 4
		default:
			return nil, fmt.Errorf("cursor: unsupported wire type %d for field %d", wireType, field)
		}
	}
	return fields, nil
}

func (fields Fields) Has(field int) bool { return len(fields[field]) > 0 }

func (fields Fields) Varint(field int) uint64 {
	values := fields[field]
	if len(values) == 0 {
		return 0
	}
	return values[len(values)-1].Varint
}

func (fields Fields) Bool(field int) bool { return fields.Varint(field) != 0 }

func (fields Fields) Int32(field int) int32 { return int32(fields.Varint(field)) }

func (fields Fields) Int64(field int) int64 { return int64(fields.Varint(field)) }

func (fields Fields) Bytes(field int) []byte {
	values := fields[field]
	for index := len(values) - 1; index >= 0; index-- {
		if values[index].WireType == wireLen {
			return values[index].Bytes
		}
	}
	return nil
}

func (fields Fields) String(field int) string { return string(fields.Bytes(field)) }

func (fields Fields) AllBytes(field int) [][]byte {
	values := fields[field]
	if len(values) == 0 {
		return nil
	}
	result := make([][]byte, 0, len(values))
	for _, value := range values {
		if value.WireType == wireLen {
			result = append(result, value.Bytes)
		}
	}
	return result
}

const (
	fieldProtobufValueNull    = 1
	fieldProtobufValueNumber  = 2
	fieldProtobufValueString  = 3
	fieldProtobufValueBool    = 4
	fieldProtobufValueStruct  = 5
	fieldProtobufValueList    = 6
	fieldProtobufStructFields = 1
	fieldProtobufMapKey       = 1
	fieldProtobufMapValue     = 2
	fieldProtobufListValues   = 1
)

func encodeProtobufValue(value any) []byte {
	var writer Writer
	switch typed := value.(type) {
	case nil:
		writer.WriteInt32(fieldProtobufValueNull, 0)
	case bool:
		writer.WriteBool(fieldProtobufValueBool, typed)
	case string:
		writer.WriteString(fieldProtobufValueString, typed)
	case json.Number:
		floatValue, err := typed.Float64()
		if err != nil {
			writer.WriteString(fieldProtobufValueString, typed.String())
			break
		}
		writer.WriteDouble(fieldProtobufValueNumber, floatValue)
	case float64:
		writer.WriteDouble(fieldProtobufValueNumber, typed)
	case float32:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case int:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case int8:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case int16:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case int32:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case int64:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case uint:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case uint8:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case uint16:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case uint32:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case uint64:
		writer.WriteDouble(fieldProtobufValueNumber, float64(typed))
	case map[string]any:
		writer.WriteBytes(fieldProtobufValueStruct, encodeProtobufStruct(typed))
	case []any:
		writer.WriteBytes(fieldProtobufValueList, encodeProtobufList(typed))
	default:
		writer.WriteInt32(fieldProtobufValueNull, 0)
	}
	return writer.Bytes()
}

func encodeProtobufStruct(fields map[string]any) []byte {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var writer Writer
	for _, key := range keys {
		var entry Writer
		entry.WriteString(fieldProtobufMapKey, key)
		entry.WriteBytes(fieldProtobufMapValue, encodeProtobufValue(fields[key]))
		writer.WriteMessage(fieldProtobufStructFields, entry.Bytes())
	}
	return writer.Bytes()
}

func encodeProtobufList(items []any) []byte {
	var writer Writer
	for _, item := range items {
		writer.WriteBytes(fieldProtobufListValues, encodeProtobufValue(item))
	}
	return writer.Bytes()
}

func decodeProtobufValue(data []byte) any { return decodeProtobufValueAt(data, 0) }

func decodeProtobufValueAt(data []byte, depth int) any {
	if depth >= maxProtobufValueDepth {
		return nil
	}
	fields, err := Decode(data)
	if err != nil {
		return nil
	}
	switch {
	case fields.Has(fieldProtobufValueNull):
		return nil
	case fields.Has(fieldProtobufValueNumber):
		return math.Float64frombits(fields.Varint(fieldProtobufValueNumber))
	case fields.Has(fieldProtobufValueString):
		return fields.String(fieldProtobufValueString)
	case fields.Has(fieldProtobufValueBool):
		return fields.Bool(fieldProtobufValueBool)
	case fields.Has(fieldProtobufValueStruct):
		return decodeProtobufStructAt(fields.Bytes(fieldProtobufValueStruct), depth+1)
	case fields.Has(fieldProtobufValueList):
		return decodeProtobufListAt(fields.Bytes(fieldProtobufValueList), depth+1)
	default:
		return nil
	}
}

func decodeProtobufStructAt(data []byte, depth int) any {
	fields, err := Decode(data)
	if err != nil {
		return nil
	}
	result := make(map[string]any)
	for _, raw := range fields.AllBytes(fieldProtobufStructFields) {
		entry, err := Decode(raw)
		if err != nil {
			continue
		}
		result[entry.String(fieldProtobufMapKey)] = decodeProtobufValueAt(entry.Bytes(fieldProtobufMapValue), depth)
	}
	return result
}

func decodeProtobufListAt(data []byte, depth int) any {
	fields, err := Decode(data)
	if err != nil {
		return nil
	}
	values := fields.AllBytes(fieldProtobufListValues)
	result := make([]any, 0, len(values))
	for _, raw := range values {
		result = append(result, decodeProtobufValueAt(raw, depth))
	}
	return result
}
