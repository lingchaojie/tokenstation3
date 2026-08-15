package protocol

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type countingDialer struct {
	calls atomic.Int32
	dial  func(context.Context) (net.Conn, error)
}

func (d *countingDialer) DialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	d.calls.Add(1)
	return d.dial(ctx)
}

func (d *countingDialer) Calls() int {
	return int(d.calls.Load())
}

func TestAttemptSplitsPayloadsAndSendsTerminalSequence(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	dialer := &countingDialer{dial: func(context.Context) (net.Conn, error) { return clientConn, nil }}
	client := NewClient(ClientConfig{
		Dial:         dialer.DialContext,
		WriteTimeout: 100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
	})
	id := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	begin := model.Begin{CaptureID: id, RequestID: "req-1", Format: model.PayloadJSON}
	type receivedFrame struct {
		header  Header
		payload []byte
	}
	received := make(chan []receivedFrame, 1)
	go func() {
		defer serverConn.Close()
		frames := make([]receivedFrame, 0, 10)
		h, payload, err := readFrame(serverConn)
		if err != nil {
			received <- nil
			return
		}
		frames = append(frames, receivedFrame{header: h, payload: payload})
		if err := writeFrame(serverConn, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil); err != nil {
			received <- nil
			return
		}
		for {
			h, payload, err = readFrame(serverConn)
			if err != nil {
				break
			}
			frames = append(frames, receivedFrame{header: h, payload: payload})
		}
		received <- frames
	}()

	attempt, err := client.Begin(context.Background(), begin)
	require.NoError(t, err)
	require.Equal(t, id, attempt.ID())
	requestHeaders := make([]byte, MaxPayloadBytes+3)
	for i := range requestHeaders {
		requestHeaders[i] = byte(i)
	}
	responseHeaders := make([]byte, MaxPayloadBytes+5)
	for i := range responseHeaders {
		responseHeaders[i] = byte(i + 1)
	}
	body := make([]byte, MaxPayloadBytes+17)
	for i := range body {
		body[i] = byte(i)
	}
	require.True(t, attempt.WriteRequestHeaders(requestHeaders))
	require.True(t, attempt.WriteResponseHeaders(responseHeaders))
	require.True(t, attempt.WriteResponse(body))
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 201, ResponseComplete: true}))
	require.True(t, attempt.Commit())
	require.False(t, attempt.Commit())
	require.False(t, attempt.WriteRequest([]byte("late")))

	frames := <-received
	require.Len(t, frames, 10)
	require.Equal(t, KindHandshake, frames[0].header.Kind)
	require.Equal(t, KindBegin, frames[1].header.Kind)
	var gotBegin model.Begin
	require.NoError(t, json.Unmarshal(frames[1].payload, &gotBegin))
	require.Equal(t, begin, gotBegin)
	require.Equal(t, KindRequestHeaders, frames[2].header.Kind)
	require.Equal(t, requestHeaders[:MaxPayloadBytes], frames[2].payload)
	require.Equal(t, KindRequestHeaders, frames[3].header.Kind)
	require.Equal(t, requestHeaders[MaxPayloadBytes:], frames[3].payload)
	require.Equal(t, KindResponseHeaders, frames[4].header.Kind)
	require.Equal(t, responseHeaders[:MaxPayloadBytes], frames[4].payload)
	require.Equal(t, KindResponseHeaders, frames[5].header.Kind)
	require.Equal(t, responseHeaders[MaxPayloadBytes:], frames[5].payload)
	require.Equal(t, KindResponseChunk, frames[6].header.Kind)
	require.Len(t, frames[6].payload, MaxPayloadBytes)
	require.Equal(t, KindResponseChunk, frames[7].header.Kind)
	require.Equal(t, body[MaxPayloadBytes:], frames[7].payload)
	require.Equal(t, KindFinal, frames[8].header.Kind)
	require.Equal(t, KindCommit, frames[9].header.Kind)
	require.Equal(t, 1, dialer.Calls())
}

func TestAttemptDoesNotRetryOrBlockWhenSocketBackpressures(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	dialer := &countingDialer{dial: func(context.Context) (net.Conn, error) { return clientConn, nil }}
	client := NewClient(ClientConfig{
		Dial:         dialer.DialContext,
		WriteTimeout: time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
	})
	beginRead := make(chan struct{})
	go func() {
		defer serverConn.Close()
		_, _, _ = readFrame(serverConn)
		_ = writeFrame(serverConn, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil)
		_, _, _ = readFrame(serverConn)
		close(beginRead)
		<-time.After(100 * time.Millisecond)
	}()

	attempt, err := client.Begin(context.Background(), model.Begin{CaptureID: uuid.New()})
	require.NoError(t, err)
	<-beginRead
	start := time.Now()
	require.False(t, attempt.WriteResponse(make([]byte, MaxPayloadBytes)))
	require.Less(t, time.Since(start), 50*time.Millisecond)
	require.False(t, attempt.WriteResponse([]byte("never retried")))
	require.Equal(t, 1, dialer.Calls())
}

func TestAttemptShortWritePermanentlyFailsAndCloses(t *testing.T) {
	conn := &scriptedConn{writesBeforeShort: 3}
	dialer := &countingDialer{dial: func(context.Context) (net.Conn, error) { return conn, nil }}
	client := NewClient(ClientConfig{Dial: dialer.DialContext, WriteTimeout: time.Millisecond})
	attempt, err := client.Begin(context.Background(), model.Begin{CaptureID: uuid.New()})
	require.NoError(t, err)

	require.False(t, attempt.WriteRequest([]byte("payload")))
	require.True(t, conn.closed.Load())
	require.False(t, attempt.Finalize(model.Final{}))
	require.Equal(t, 1, dialer.Calls())
}

func TestClientStatusRoundTrip(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	dialer := &countingDialer{dial: func(context.Context) (net.Conn, error) { return clientConn, nil }}
	client := NewClient(ClientConfig{
		Dial:         dialer.DialContext,
		WriteTimeout: 100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
	})
	want := model.Status{
		HealthSourceID: uuid.MustParse("11112222-3333-4444-5555-666677778888"),
		SpoolReady:     true,
		ReadyRecords:   7,
		DroppedByReason: map[string]uint64{
			"ipc_backpressure": 3,
		},
	}
	go func() {
		defer serverConn.Close()
		_, _, _ = readFrame(serverConn)
		_ = writeFrame(serverConn, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil)
		h, _, _ := readFrame(serverConn)
		if h.Kind != KindStatusRequest {
			return
		}
		payload, _ := json.Marshal(want)
		_ = writeFrame(serverConn, Header{Version: ProtocolVersion, Kind: KindStatusResponse}, payload)
	}()

	got, err := client.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, 1, dialer.Calls())
}

type scriptedConn struct {
	writesBeforeShort int
	writes            int
	closed            atomic.Bool
	readBuffer        []byte
}

func (c *scriptedConn) Read(p []byte) (int, error) {
	if len(c.readBuffer) == 0 {
		h := Header{Version: ProtocolVersion, Kind: KindHandshake}.MarshalBinary()
		c.readBuffer = append(c.readBuffer, h...)
	}
	n := copy(p, c.readBuffer)
	c.readBuffer = c.readBuffer[n:]
	return n, nil
}

func (c *scriptedConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes > c.writesBeforeShort {
		return len(p) - 1, nil
	}
	return len(p), nil
}

func (c *scriptedConn) Close() error                     { c.closed.Store(true); return nil }
func (c *scriptedConn) LocalAddr() net.Addr              { return stubAddr("local") }
func (c *scriptedConn) RemoteAddr() net.Addr             { return stubAddr("remote") }
func (c *scriptedConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedConn) SetWriteDeadline(time.Time) error { return nil }

type stubAddr string

func (a stubAddr) Network() string { return "test" }
func (a stubAddr) String() string  { return string(a) }
