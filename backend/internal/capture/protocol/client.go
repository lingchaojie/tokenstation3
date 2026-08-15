package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
)

type Attempt interface {
	ID() uuid.UUID
	WriteRequest([]byte) bool
	WriteResponse([]byte) bool
	WriteRequestHeaders([]byte) bool
	WriteResponseHeaders([]byte) bool
	Finalize(model.Final) bool
	Commit() bool
	Abort()
}

type Transport interface {
	Begin(context.Context, model.Begin) (Attempt, error)
	Status(context.Context) (model.Status, error)
	Close() error
}

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type ClientConfig struct {
	SocketPath   string
	Dial         DialContextFunc
	DialTimeout  time.Duration
	WriteTimeout time.Duration
	ReadTimeout  time.Duration
}

type Client struct {
	config ClientConfig

	mu       sync.Mutex
	closed   bool
	attempts map[*clientAttempt]struct{}
}

func NewClient(config ClientConfig) *Client {
	if config.Dial == nil {
		dialer := &net.Dialer{}
		config.Dial = dialer.DialContext
	}
	if config.DialTimeout <= 0 {
		config.DialTimeout = time.Millisecond
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = time.Millisecond
	}
	if config.ReadTimeout <= 0 {
		config.ReadTimeout = config.WriteTimeout
	}
	return &Client{config: config, attempts: make(map[*clientAttempt]struct{})}
}

func (c *Client) Begin(ctx context.Context, begin model.Begin) (Attempt, error) {
	if begin.CaptureID == uuid.Nil {
		return nil, errors.New("capture ID is required")
	}
	conn, err := c.open(ctx)
	if err != nil {
		return nil, err
	}
	attempt := &clientAttempt{
		id:           begin.CaptureID,
		conn:         conn,
		writeTimeout: c.config.WriteTimeout,
		owner:        c,
	}
	payload, err := json.Marshal(begin)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if !attempt.writeFrameLocked(KindBegin, payload) {
		return nil, errors.New("send capture begin")
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		attempt.failLocked()
		return nil, net.ErrClosed
	}
	c.attempts[attempt] = struct{}{}
	c.mu.Unlock()
	return attempt, nil
}

func (c *Client) Status(ctx context.Context) (model.Status, error) {
	conn, err := c.open(ctx)
	if err != nil {
		return model.Status{}, err
	}
	defer conn.Close()
	if err := writeFrameWithDeadline(conn, c.config.WriteTimeout, Header{
		Version: ProtocolVersion,
		Kind:    KindStatusRequest,
	}, nil); err != nil {
		return model.Status{}, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout)); err != nil {
		return model.Status{}, err
	}
	header, payload, err := readFrame(conn)
	if err != nil {
		return model.Status{}, err
	}
	if header.Version != ProtocolVersion {
		return model.Status{}, fmt.Errorf("status protocol version %d", header.Version)
	}
	if header.Kind == KindProtocolError {
		return model.Status{}, fmt.Errorf("capture protocol error: %s", payload)
	}
	if header.Kind != KindStatusResponse || header.CaptureID != uuid.Nil {
		return model.Status{}, ErrInvalidHeader
	}
	var status model.Status
	if err := json.Unmarshal(payload, &status); err != nil {
		return model.Status{}, err
	}
	return status, nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	attempts := make([]*clientAttempt, 0, len(c.attempts))
	for attempt := range c.attempts {
		attempts = append(attempts, attempt)
	}
	c.mu.Unlock()
	for _, attempt := range attempts {
		attempt.closeWithoutFrame()
	}
	return nil
}

func (c *Client) open(ctx context.Context) (net.Conn, error) {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	dialCtx, cancel := context.WithTimeout(ctx, c.config.DialTimeout)
	defer cancel()
	conn, err := c.config.Dial(dialCtx, "unix", c.config.SocketPath)
	if err != nil {
		return nil, err
	}
	if err := writeFrameWithDeadline(conn, c.config.WriteTimeout, Header{
		Version: ProtocolVersion,
		Kind:    KindHandshake,
	}, nil); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := conn.SetReadDeadline(time.Now().Add(c.config.ReadTimeout)); err != nil {
		_ = conn.Close()
		return nil, err
	}
	header, payload, err := readFrame(conn)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if header.Kind == KindProtocolError {
		_ = conn.Close()
		return nil, fmt.Errorf("capture protocol error: %s", payload)
	}
	if header.Version != ProtocolVersion || header.Kind != KindHandshake || header.CaptureID != uuid.Nil || len(payload) != 0 {
		_ = conn.Close()
		return nil, ErrInvalidHeader
	}
	return conn, nil
}

func (c *Client) remove(attempt *clientAttempt) {
	c.mu.Lock()
	delete(c.attempts, attempt)
	c.mu.Unlock()
}

type clientAttempt struct {
	mu           sync.Mutex
	id           uuid.UUID
	conn         net.Conn
	writeTimeout time.Duration
	owner        *Client
	failed       bool
}

func (a *clientAttempt) ID() uuid.UUID { return a.id }

func (a *clientAttempt) WriteRequest(payload []byte) bool {
	return a.writePayload(KindRequestChunk, payload)
}

func (a *clientAttempt) WriteResponse(payload []byte) bool {
	return a.writePayload(KindResponseChunk, payload)
}

func (a *clientAttempt) WriteRequestHeaders(payload []byte) bool {
	return a.writePayload(KindRequestHeaders, payload)
}

func (a *clientAttempt) WriteResponseHeaders(payload []byte) bool {
	return a.writePayload(KindResponseHeaders, payload)
}

func (a *clientAttempt) Finalize(final model.Final) bool {
	payload, err := json.Marshal(final)
	if err != nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.writeFrameLocked(KindFinal, payload)
}

func (a *clientAttempt) Commit() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		return false
	}
	ok := a.writeFrameLocked(KindCommit, nil)
	a.finishLocked()
	return ok
}

func (a *clientAttempt) Abort() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.failed {
		_ = a.writeFrameLocked(KindAbort, nil)
	}
	a.finishLocked()
}

func (a *clientAttempt) writePayload(kind Kind, payload []byte) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.failed {
		return false
	}
	if len(payload) == 0 {
		return a.writeFrameLocked(kind, nil)
	}
	for len(payload) > 0 {
		chunkSize := len(payload)
		if chunkSize > MaxPayloadBytes {
			chunkSize = MaxPayloadBytes
		}
		if !a.writeFrameLocked(kind, payload[:chunkSize]) {
			return false
		}
		payload = payload[chunkSize:]
	}
	return true
}

func (a *clientAttempt) writeFrameLocked(kind Kind, payload []byte) bool {
	if a.failed {
		return false
	}
	err := writeFrameWithDeadline(a.conn, a.writeTimeout, Header{
		Version:   ProtocolVersion,
		Kind:      kind,
		CaptureID: a.id,
	}, payload)
	if err != nil {
		a.failLocked()
		return false
	}
	return true
}

func (a *clientAttempt) failLocked() {
	if a.failed {
		return
	}
	a.failed = true
	_ = a.conn.Close()
	if a.owner != nil {
		a.owner.remove(a)
	}
}

func (a *clientAttempt) finishLocked() {
	a.failLocked()
}

func (a *clientAttempt) closeWithoutFrame() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failLocked()
}

func writeFrameWithDeadline(conn net.Conn, timeout time.Duration, header Header, payload []byte) error {
	if len(payload) > MaxPayloadBytes {
		return ErrFrameTooLarge
	}
	header.Length = uint32(len(payload))
	if err := writeConnOnce(conn, timeout, header.MarshalBinary()); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	return writeConnOnce(conn, timeout, payload)
}

func writeConnOnce(conn net.Conn, timeout time.Duration, payload []byte) error {
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		return err
	}
	written, err := conn.Write(payload)
	if err != nil {
		return err
	}
	if written != len(payload) {
		return fmt.Errorf("%w: wrote %d of %d bytes", io.ErrShortWrite, written, len(payload))
	}
	return nil
}
