package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestServerDispatchesValidAttemptAndStatus(t *testing.T) {
	factory := &recordingFactory{}
	wantStatus := model.Status{SpoolReady: true, SpoolUsedBytes: 1234}
	delivered := make(chan model.Status, 1)
	server, socketPath := startTestServer(t, ServerConfig{
		MaxSessions:     4,
		Status:          func() model.Status { return wantStatus },
		StatusDelivered: func(status model.Status) { delivered <- status },
	}, factory)

	client := NewClient(ClientConfig{
		SocketPath:   socketPath,
		DialTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
	})
	id := uuid.New()
	attempt, err := client.Begin(context.Background(), model.Begin{CaptureID: id, RequestID: "req-1"})
	require.NoError(t, err)
	require.True(t, attempt.WriteRequestHeaders([]byte("x-request: safe")))
	require.True(t, attempt.WriteRequest([]byte("request")))
	require.True(t, attempt.WriteResponseHeaders([]byte("x-response: safe")))
	require.True(t, attempt.WriteResponse([]byte("response")))
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())

	require.Eventually(t, func() bool {
		sink := factory.firstSink()
		return sink != nil && sink.committed()
	}, time.Second, time.Millisecond)
	sink := factory.firstSink()
	require.Equal(t, []string{
		"request_headers:x-request: safe",
		"request:request",
		"response_headers:x-response: safe",
		"response:response",
		"final:200",
		"commit",
	}, sink.eventsSnapshot())
	require.Empty(t, sink.abortErrors())

	gotStatus, err := client.Status(context.Background())
	require.NoError(t, err)
	require.Equal(t, wantStatus, gotStatus)
	require.Equal(t, wantStatus, <-delivered)
	select {
	case duplicate := <-delivered:
		t.Fatalf("status delivery callback called twice: %+v", duplicate)
	default:
	}
	require.NoError(t, server.Close())
}

func TestServerDoesNotAcknowledgeFailedStatusResponse(t *testing.T) {
	var input bytes.Buffer
	require.NoError(t, writeFrame(&input, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil))
	require.NoError(t, writeFrame(&input, Header{Version: ProtocolVersion, Kind: KindStatusRequest}, nil))
	conn := &failSecondWriteConn{Reader: &input}
	acknowledged := false
	server := NewServer(ServerConfig{
		Status:          func() model.Status { return model.Status{SpoolReady: true} },
		StatusDelivered: func(model.Status) { acknowledged = true },
	}, &recordingFactory{})

	server.handleConnection(conn)

	require.False(t, acknowledged)
	require.Equal(t, 2, conn.writes)
}

func TestServerAbortsOpenedSessionWhenBeginAcknowledgementFails(t *testing.T) {
	id := uuid.New()
	beginPayload, err := json.Marshal(model.Begin{CaptureID: id})
	require.NoError(t, err)
	var input bytes.Buffer
	require.NoError(t, writeFrame(&input, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil))
	require.NoError(t, writeFrame(&input, Header{Version: ProtocolVersion, Kind: KindBegin, CaptureID: id}, beginPayload))
	conn := &failSecondWriteConn{Reader: &input}
	factory := &recordingFactory{}
	server := NewServer(ServerConfig{}, factory)

	server.handleConnection(conn)

	require.Equal(t, 2, conn.writes)
	require.Len(t, factory.beginsSnapshot(), 1)
	require.Len(t, factory.firstSink().abortErrors(), 1)
	require.False(t, factory.firstSink().committed())
}

func TestServerAppendsConsecutiveMultipartHeaders(t *testing.T) {
	factory := &recordingFactory{}
	_, socketPath := startTestServer(t, ServerConfig{MaxSessions: 1}, factory)
	client := NewClient(ClientConfig{
		SocketPath:   socketPath,
		DialTimeout:  100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond,
		ReadTimeout:  100 * time.Millisecond,
	})
	attempt, err := client.Begin(context.Background(), model.Begin{CaptureID: uuid.New()})
	require.NoError(t, err)
	requestHeaders := bytes.Repeat([]byte("r"), MaxPayloadBytes+19)
	responseHeaders := bytes.Repeat([]byte("s"), 2*MaxPayloadBytes+7)

	require.True(t, attempt.WriteRequestHeaders(requestHeaders))
	require.True(t, attempt.WriteResponseHeaders(responseHeaders))
	require.True(t, attempt.Finalize(model.Final{HTTPStatus: 200, ResponseComplete: true}))
	require.True(t, attempt.Commit())

	require.Eventually(t, func() bool {
		sink := factory.firstSink()
		return sink != nil && sink.committed()
	}, time.Second, time.Millisecond)
	sink := factory.firstSink()
	require.Equal(t, requestHeaders, sink.requestHeadersSnapshot())
	require.Equal(t, responseHeaders, sink.responseHeadersSnapshot())
}

func TestServerRejectsVersionMismatchBeforeBegin(t *testing.T) {
	factory := &recordingFactory{}
	_, socketPath := startTestServer(t, ServerConfig{MaxSessions: 2}, factory)
	conn := dialTestSocket(t, socketPath)
	defer func() { _ = conn.Close() }()

	require.NoError(t, writeFrame(conn, Header{Version: 99, Kind: KindHandshake}, nil))
	h, _, err := readFrame(conn)
	require.NoError(t, err)
	require.Equal(t, KindProtocolError, h.Kind)
	require.Empty(t, factory.beginsSnapshot())
}

func TestServerBoundsAcceptedSessionsBeforeSpawningHandlers(t *testing.T) {
	factory := &recordingFactory{}
	server, socketPath := startTestServer(t, ServerConfig{MaxSessions: 2}, factory)

	connections := make([]net.Conn, 0, 2)
	for range 2 {
		conn := dialTestSocket(t, socketPath)
		require.NoError(t, writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil))
		h, _, err := readFrame(conn)
		require.NoError(t, err)
		require.Equal(t, KindHandshake, h.Kind)
		connections = append(connections, conn)
	}
	t.Cleanup(func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	})
	require.Eventually(t, func() bool { return server.ActiveHandlers() == 2 }, time.Second, time.Millisecond)

	extra := dialTestSocket(t, socketPath)
	defer func() { _ = extra.Close() }()
	require.NoError(t, writeFrame(extra, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil))
	require.NoError(t, extra.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	started := time.Now()
	header, _, err := readFrame(extra)
	require.NoError(t, err)
	require.Equal(t, KindHandshake, header.Kind)
	_, _, err = readFrame(extra)
	require.Error(t, err)
	require.Less(t, time.Since(started), 100*time.Millisecond, "an incomplete overload probe is bounded by one operation deadline")
	require.Equal(t, 2, server.ActiveHandlers())
}

func TestServerOverloadReturnsTypedRejectionOnlyForValidBegin(t *testing.T) {
	factory := &recordingFactory{}
	server, socketPath := startTestServer(t, ServerConfig{MaxSessions: 1}, factory)
	client := NewClient(ClientConfig{
		SocketPath: socketPath, DialTimeout: 100 * time.Millisecond,
		WriteTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond,
	})
	t.Cleanup(func() { _ = client.Close() })

	first, err := client.Begin(context.Background(), model.Begin{CaptureID: uuid.New()})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return server.ActiveHandlers() == 1 }, time.Second, time.Millisecond)

	_, err = client.Begin(context.Background(), model.Begin{CaptureID: uuid.New()})
	require.ErrorIs(t, err, ErrIPCBackpressure)

	_, err = client.Status(context.Background())
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrIPCBackpressure, "status probes are not dropped captures")

	invalidBegin := dialTestSocket(t, socketPath)
	handshake(t, invalidBegin, ProtocolVersion)
	invalidID := uuid.New()
	require.NoError(t, writeFrame(invalidBegin, Header{
		Version: ProtocolVersion, Kind: KindBegin, CaptureID: invalidID,
	}, []byte(`{"capture_id":"not-a-uuid"}`)))
	require.NoError(t, invalidBegin.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	_, _, err = readFrame(invalidBegin)
	require.Error(t, err, "malformed Begin must close without a typed capture rejection")
	_ = invalidBegin.Close()

	malformed := dialTestSocket(t, socketPath)
	require.NoError(t, malformed.SetDeadline(time.Now().Add(100*time.Millisecond)))
	_, err = malformed.Write([]byte("not a capture frame"))
	require.NoError(t, err)
	_ = malformed.Close()

	require.Len(t, factory.beginsSnapshot(), 1, "overload rejection must not call factory.Open")
	first.Abort()
}

func TestServerClampsConfiguredMaxSessionsToHardLimit(t *testing.T) {
	factory := &recordingFactory{}
	server, socketPath := startTestServer(t, ServerConfig{MaxSessions: 64}, factory)

	connections := make([]net.Conn, 0, 32)
	for range 32 {
		conn := dialTestSocket(t, socketPath)
		require.NoError(t, writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil))
		header, _, err := readFrame(conn)
		require.NoError(t, err)
		require.Equal(t, KindHandshake, header.Kind)
		connections = append(connections, conn)
	}
	t.Cleanup(func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	})
	require.Eventually(t, func() bool { return server.ActiveHandlers() == 32 }, time.Second, time.Millisecond)

	extra := dialTestSocket(t, socketPath)
	defer func() { _ = extra.Close() }()
	require.NoError(t, writeFrame(extra, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil))
	require.NoError(t, extra.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	header, _, err := readFrame(extra)
	require.NoError(t, err)
	require.Equal(t, KindHandshake, header.Kind)
	_, _, err = readFrame(extra)
	require.Error(t, err)
	require.Equal(t, 32, server.ActiveHandlers())
}

func TestServerRejectsIllegalOrderAndAbortsOpenedSession(t *testing.T) {
	factory := &recordingFactory{}
	_, socketPath := startTestServer(t, ServerConfig{MaxSessions: 2}, factory)
	conn := dialTestSocket(t, socketPath)
	defer func() { _ = conn.Close() }()
	handshake(t, conn, ProtocolVersion)
	id := uuid.New()
	beginPayload, err := json.Marshal(model.Begin{CaptureID: id})
	require.NoError(t, err)
	require.NoError(t, writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindBegin, CaptureID: id}, beginPayload))
	readBeginAck(t, conn, id)
	require.NoError(t, writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindCommit, CaptureID: id}, nil))

	h, _, err := readFrame(conn)
	require.NoError(t, err)
	require.Equal(t, KindProtocolError, h.Kind)
	require.Eventually(t, func() bool {
		sink := factory.firstSink()
		return sink != nil && len(sink.abortErrors()) == 1
	}, time.Second, time.Millisecond)
	require.False(t, factory.firstSink().committed())
}

func TestServerAbortsSessionOnDisconnectAndPanic(t *testing.T) {
	t.Run("disconnect", func(t *testing.T) {
		factory := &recordingFactory{}
		_, socketPath := startTestServer(t, ServerConfig{MaxSessions: 1}, factory)
		conn := dialTestSocket(t, socketPath)
		handshake(t, conn, ProtocolVersion)
		writeBegin(t, conn, uuid.New())
		require.NoError(t, conn.Close())

		require.Eventually(t, func() bool {
			sink := factory.firstSink()
			return sink != nil && len(sink.abortErrors()) == 1
		}, time.Second, time.Millisecond)
	})

	t.Run("sink panic", func(t *testing.T) {
		factory := &recordingFactory{panicOnRequest: true}
		server, socketPath := startTestServer(t, ServerConfig{MaxSessions: 1}, factory)
		conn := dialTestSocket(t, socketPath)
		handshake(t, conn, ProtocolVersion)
		id := uuid.New()
		writeBegin(t, conn, id)
		require.NoError(t, writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindRequestChunk, CaptureID: id}, []byte("boom")))
		require.NoError(t, conn.Close())

		require.Eventually(t, func() bool {
			sink := factory.firstSink()
			return sink != nil && len(sink.abortErrors()) == 1 && server.ActiveHandlers() == 0
		}, time.Second, time.Millisecond)
	})
}

func TestServerSocketPermissionsAndUnsafeExistingNodes(t *testing.T) {
	t.Run("creates private directory and socket", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "capture")
		factory := &recordingFactory{}
		_, socketPath := startTestServerAt(t, ServerConfig{SocketPath: filepath.Join(parent, "capture.sock")}, factory)

		parentInfo, err := os.Stat(parent)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o700), parentInfo.Mode().Perm())
		socketInfo, err := os.Lstat(socketPath)
		require.NoError(t, err)
		require.NotZero(t, socketInfo.Mode()&os.ModeSocket)
		require.Equal(t, os.FileMode(0o600), socketInfo.Mode().Perm())
	})

	for _, test := range []struct {
		name   string
		create func(t *testing.T, path string)
	}{
		{
			name: "regular file",
			create: func(t *testing.T, path string) {
				require.NoError(t, os.WriteFile(path, []byte("keep"), 0o600))
			},
		},
		{
			name: "symlink",
			create: func(t *testing.T, path string) {
				target := filepath.Join(filepath.Dir(path), "target")
				require.NoError(t, os.WriteFile(target, []byte("keep"), 0o600))
				require.NoError(t, os.Symlink(target, path))
			},
		},
	} {
		t.Run("refuses "+test.name, func(t *testing.T) {
			parent := t.TempDir()
			path := filepath.Join(parent, "capture.sock")
			test.create(t, path)
			server := NewServer(ServerConfig{SocketPath: path}, &recordingFactory{})
			err := server.Serve(context.Background())
			require.Error(t, err)
			_, statErr := os.Lstat(path)
			require.NoError(t, statErr)
		})
	}
}

func TestServerRemovesOnlyStaleSocketAndStopsWithContext(t *testing.T) {
	parent := t.TempDir()
	path := filepath.Join(parent, "capture.sock")
	stale, err := net.Listen("unix", path)
	require.NoError(t, err)
	require.NoError(t, stale.Close())

	server := NewServer(ServerConfig{SocketPath: path}, &recordingFactory{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	waitForServerReady(t, server, path)
	cancel()
	require.NoError(t, <-done)
	_, err = os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist)
}

type recordingFactory struct {
	mu             sync.Mutex
	begins         []model.Begin
	sinks          []*recordingSink
	openErr        error
	panicOnRequest bool
}

type failSecondWriteConn struct {
	io.Reader
	writes int
}

func (c *failSecondWriteConn) Write(p []byte) (int, error) {
	c.writes++
	if c.writes == 2 {
		return 0, errors.New("injected status write failure")
	}
	return len(p), nil
}

func (*failSecondWriteConn) Close() error                     { return nil }
func (*failSecondWriteConn) LocalAddr() net.Addr              { return testAddr("local") }
func (*failSecondWriteConn) RemoteAddr() net.Addr             { return testAddr("remote") }
func (*failSecondWriteConn) SetDeadline(time.Time) error      { return nil }
func (*failSecondWriteConn) SetReadDeadline(time.Time) error  { return nil }
func (*failSecondWriteConn) SetWriteDeadline(time.Time) error { return nil }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }

func (f *recordingFactory) Open(begin model.Begin) (SessionSink, error) {
	if f.openErr != nil {
		return nil, f.openErr
	}
	sink := &recordingSink{panicOnRequest: f.panicOnRequest}
	f.mu.Lock()
	f.begins = append(f.begins, begin)
	f.sinks = append(f.sinks, sink)
	f.mu.Unlock()
	return sink, nil
}

func (f *recordingFactory) beginsSnapshot() []model.Begin {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Begin(nil), f.begins...)
}

func (f *recordingFactory) firstSink() *recordingSink {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sinks) == 0 {
		return nil
	}
	return f.sinks[0]
}

type recordingSink struct {
	mu              sync.Mutex
	events          []string
	requestHeaders  []byte
	responseHeaders []byte
	aborted         []error
	didCommit       bool
	panicOnRequest  bool
}

func (s *recordingSink) WriteRequestHeaders(p []byte) error {
	s.mu.Lock()
	s.requestHeaders = append(s.requestHeaders, p...)
	s.mu.Unlock()
	s.addEvent("request_headers:" + string(p))
	return nil
}
func (s *recordingSink) WriteResponseHeaders(p []byte) error {
	s.mu.Lock()
	s.responseHeaders = append(s.responseHeaders, p...)
	s.mu.Unlock()
	s.addEvent("response_headers:" + string(p))
	return nil
}
func (s *recordingSink) WriteRequest(p []byte) error {
	if s.panicOnRequest {
		panic("sink panic")
	}
	s.addEvent("request:" + string(p))
	return nil
}
func (s *recordingSink) WriteResponse(p []byte) error {
	s.addEvent("response:" + string(p))
	return nil
}
func (s *recordingSink) Finalize(final model.Final) error {
	s.addEvent("final:" + fmtUint16(final.HTTPStatus))
	return nil
}
func (s *recordingSink) Commit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "commit")
	s.didCommit = true
	return nil
}
func (s *recordingSink) Abort(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.aborted = append(s.aborted, err)
}
func (s *recordingSink) addEvent(event string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}
func (s *recordingSink) eventsSnapshot() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}
func (s *recordingSink) abortErrors() []error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]error(nil), s.aborted...)
}
func (s *recordingSink) committed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.didCommit
}

func (s *recordingSink) requestHeadersSnapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.requestHeaders...)
}

func (s *recordingSink) responseHeadersSnapshot() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.responseHeaders...)
}

func startTestServer(t *testing.T, cfg ServerConfig, factory SessionFactory) (*Server, string) {
	t.Helper()
	if cfg.SocketPath == "" {
		cfg.SocketPath = filepath.Join(t.TempDir(), "capture", "capture.sock")
	}
	return startTestServerAt(t, cfg, factory)
}

func startTestServerAt(t *testing.T, cfg ServerConfig, factory SessionFactory) (*Server, string) {
	t.Helper()
	server := NewServer(cfg, factory)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	waitForServerReady(t, server, cfg.SocketPath)
	t.Cleanup(func() {
		cancel()
		_ = server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve() did not stop")
		}
	})
	return server, cfg.SocketPath
}

func waitForServerReady(t *testing.T, server *Server, path string) {
	t.Helper()
	require.Eventually(t, func() bool {
		server.listenerMu.Lock()
		listenerReady := server.listener != nil && !server.closed
		server.listenerMu.Unlock()
		if !listenerReady {
			return false
		}
		info, err := os.Lstat(path)
		return err == nil && info.Mode()&os.ModeSocket != 0
	}, time.Second, time.Millisecond)
}

func dialTestSocket(t *testing.T, path string) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	require.NoError(t, err)
	return conn
}

func handshake(t *testing.T, conn net.Conn, version uint16) {
	t.Helper()
	require.NoError(t, writeFrame(conn, Header{Version: version, Kind: KindHandshake}, nil))
	h, _, err := readFrame(conn)
	require.NoError(t, err)
	require.Equal(t, KindHandshake, h.Kind)
}

func writeBegin(t *testing.T, conn net.Conn, id uuid.UUID) {
	t.Helper()
	payload, err := json.Marshal(model.Begin{CaptureID: id})
	require.NoError(t, err)
	require.NoError(t, writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindBegin, CaptureID: id}, payload))
	readBeginAck(t, conn, id)
}

func readBeginAck(t *testing.T, conn net.Conn, id uuid.UUID) {
	t.Helper()
	header, payload, err := readFrame(conn)
	require.NoError(t, err)
	require.Equal(t, KindBegin, header.Kind)
	require.Equal(t, id, header.CaptureID)
	require.Empty(t, payload)
}

func fmtUint16(value uint16) string {
	if value == 0 {
		return "0"
	}
	var digits [5]byte
	index := len(digits)
	for value > 0 {
		index--
		digits[index] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[index:])
}
