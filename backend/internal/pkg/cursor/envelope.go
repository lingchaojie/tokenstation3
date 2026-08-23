package cursor

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	flagCompressed byte = 0x01
	flagEndStream  byte = 0x02

	maxFrameSize             = 64 << 20
	maxDecompressedFrameSize = 64 << 20
)

type Frame struct {
	Flag       byte
	Compressed bool
	EndStream  bool
	Payload    []byte
}

func EncodeFrame(payload []byte, compress bool) []byte {
	flag := byte(0)
	body := payload
	if compress {
		flag = flagCompressed
		body = gzipBytes(payload)
	}
	return encodeRawFrame(flag, body)
}

func encodeRawFrame(flag byte, body []byte) []byte {
	frame := make([]byte, 5+len(body))
	frame[0] = flag
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(body)))
	copy(frame[5:], body)
	return frame
}

type FrameReader struct {
	reader *bufio.Reader
	header [5]byte
}

func NewFrameReader(reader io.Reader) *FrameReader {
	return &FrameReader{reader: bufio.NewReader(reader)}
}

func (reader *FrameReader) Next() (*Frame, error) {
	if _, err := io.ReadFull(reader.reader, reader.header[:]); err != nil {
		return nil, err
	}

	flag := reader.header[0]
	length := binary.BigEndian.Uint32(reader.header[1:])
	if length > maxFrameSize {
		return nil, fmt.Errorf("cursor: frame length %d exceeds max %d", length, maxFrameSize)
	}

	payload := make([]byte, int(length))
	if length > 0 {
		if _, err := io.ReadFull(reader.reader, payload); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}

	compressed := flag&flagCompressed != 0
	if compressed && length > 0 {
		var err error
		payload, err = gunzip(payload)
		if err != nil {
			return nil, fmt.Errorf("cursor: decompress frame: %w", err)
		}
	}

	return &Frame{
		Flag:       flag,
		Compressed: compressed,
		EndStream:  flag&flagEndStream != 0,
		Payload:    payload,
	}, nil
}

func gzipBytes(payload []byte) []byte {
	var output bytes.Buffer
	writer := gzip.NewWriter(&output)
	_, _ = writer.Write(payload)
	_ = writer.Close()
	return output.Bytes()
}

func gunzip(payload []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	output, err := io.ReadAll(io.LimitReader(reader, maxDecompressedFrameSize+1))
	if err != nil {
		return nil, err
	}
	if len(output) > maxDecompressedFrameSize {
		return nil, fmt.Errorf("cursor: decompressed frame exceeds max %d bytes", maxDecompressedFrameSize)
	}
	return output, nil
}
