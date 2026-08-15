// Package protocol implements the bounded Unix-socket protocol shared by the
// capture gateway and sidecar.
package protocol

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
)

const (
	ProtocolVersion uint16 = 2
	HeaderSize             = 28
	MaxPayloadBytes        = 65536
)

var (
	frameMagic       = [4]byte{'C', 'S', 'P', '2'}
	ErrInvalidHeader = errors.New("invalid capture protocol header")
	ErrInvalidMagic  = errors.New("invalid capture protocol magic")
	ErrUnknownKind   = errors.New("unknown capture protocol message kind")
	ErrFrameTooLarge = errors.New("capture protocol frame too large")
)

type Kind uint8

const (
	KindHandshake Kind = iota + 1
	KindBegin
	KindRequestHeaders
	KindResponseHeaders
	KindRequestChunk
	KindResponseChunk
	KindFinal
	KindCommit
	KindAbort
	KindStatusRequest
	KindStatusResponse
	KindProtocolError
)

type Header struct {
	Version   uint16
	Kind      Kind
	CaptureID uuid.UUID
	Length    uint32
}

func (h Header) MarshalBinary() []byte {
	b := make([]byte, HeaderSize)
	copy(b[:4], frameMagic[:])
	binary.BigEndian.PutUint16(b[4:6], h.Version)
	b[6] = byte(h.Kind)
	copy(b[8:24], h.CaptureID[:])
	binary.BigEndian.PutUint32(b[24:28], h.Length)
	return b
}

func ParseHeader(b []byte) (Header, error) {
	if len(b) != HeaderSize {
		return Header{}, ErrInvalidHeader
	}
	if !bytes.Equal(b[:4], frameMagic[:]) {
		return Header{}, ErrInvalidMagic
	}
	if b[7] != 0 {
		return Header{}, ErrInvalidHeader
	}
	kind := Kind(b[6])
	if kind < KindHandshake || kind > KindProtocolError {
		return Header{}, ErrUnknownKind
	}
	length := binary.BigEndian.Uint32(b[24:28])
	if length > MaxPayloadBytes {
		return Header{}, ErrFrameTooLarge
	}
	var captureID uuid.UUID
	copy(captureID[:], b[8:24])
	return Header{
		Version:   binary.BigEndian.Uint16(b[4:6]),
		Kind:      kind,
		CaptureID: captureID,
		Length:    length,
	}, nil
}

func readFrame(r io.Reader) (Header, []byte, error) {
	headerBytes := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, headerBytes); err != nil {
		return Header{}, nil, err
	}
	header, err := ParseHeader(headerBytes)
	if err != nil {
		return Header{}, nil, err
	}
	if header.Length == 0 {
		return header, nil, nil
	}
	payload := make([]byte, int(header.Length))
	if _, err := io.ReadFull(r, payload); err != nil {
		return Header{}, nil, err
	}
	return header, payload, nil
}

func writeFrame(w io.Writer, header Header, payload []byte) error {
	if len(payload) > MaxPayloadBytes {
		return ErrFrameTooLarge
	}
	header.Length = uint32(len(payload))
	if err := writeOnce(w, header.MarshalBinary()); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeOnce(w, payload)
}

func writeOnce(w io.Writer, p []byte) error {
	written, err := w.Write(p)
	if err != nil {
		return err
	}
	if written != len(p) {
		return fmt.Errorf("%w: wrote %d of %d bytes", io.ErrShortWrite, written, len(p))
	}
	return nil
}
