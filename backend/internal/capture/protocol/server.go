package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
)

const hardMaxSessions = 32

type SessionFactory interface {
	Open(model.Begin) (SessionSink, error)
}

type SessionSink interface {
	WriteRequestHeaders([]byte) error
	WriteResponseHeaders([]byte) error
	WriteRequest([]byte) error
	WriteResponse([]byte) error
	Finalize(model.Final) error
	Commit() error
	Abort(error)
}

type ServerConfig struct {
	SocketPath      string
	MaxSessions     int
	WriteTimeout    time.Duration
	Status          func() model.Status
	StatusDelivered func(model.Status)
}

type Server struct {
	config  ServerConfig
	factory SessionFactory
	limit   chan struct{}

	listenerMu  sync.Mutex
	listener    net.Listener
	closed      bool
	connections map[net.Conn]struct{}
	closeOnce   sync.Once
	wait        sync.WaitGroup
	active      atomic.Int32
}

func NewServer(config ServerConfig, factory SessionFactory) *Server {
	if config.MaxSessions <= 0 || config.MaxSessions > hardMaxSessions {
		config.MaxSessions = hardMaxSessions
	}
	if config.WriteTimeout <= 0 {
		config.WriteTimeout = time.Second
	}
	return &Server{
		config:      config,
		factory:     factory,
		limit:       make(chan struct{}, config.MaxSessions),
		connections: make(map[net.Conn]struct{}),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if s.factory == nil {
		return errors.New("capture session factory is required")
	}
	if s.config.SocketPath == "" {
		return errors.New("capture socket path is required")
	}
	if err := prepareSocketPath(s.config.SocketPath); err != nil {
		return err
	}
	listener, err := net.Listen("unix", s.config.SocketPath)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.config.SocketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = removeSocketNode(s.config.SocketPath)
		return err
	}
	if unixListener, ok := listener.(*net.UnixListener); ok {
		unixListener.SetUnlinkOnClose(false)
	}
	s.listenerMu.Lock()
	if s.closed {
		s.listenerMu.Unlock()
		_ = listener.Close()
		_ = removeSocketNode(s.config.SocketPath)
		return net.ErrClosed
	}
	s.listener = listener
	s.listenerMu.Unlock()

	stopContextWatch := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-stopContextWatch:
		}
	}()

	for {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			close(stopContextWatch)
			s.wait.Wait()
			_ = removeSocketNode(s.config.SocketPath)
			if ctx.Err() != nil || errors.Is(acceptErr, net.ErrClosed) {
				return nil
			}
			return acceptErr
		}
		select {
		case s.limit <- struct{}{}:
			if !s.trackConnection(conn) {
				<-s.limit
				_ = conn.Close()
				continue
			}
			s.active.Add(1)
			s.wait.Add(1)
			go s.serveConnection(conn)
		default:
			_ = conn.Close()
		}
	}
}

func (s *Server) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		s.listenerMu.Lock()
		s.closed = true
		if s.listener != nil {
			closeErr = s.listener.Close()
		}
		for conn := range s.connections {
			_ = conn.Close()
		}
		s.listenerMu.Unlock()
	})
	if errors.Is(closeErr, net.ErrClosed) {
		return nil
	}
	return closeErr
}

func (s *Server) ActiveHandlers() int {
	return int(s.active.Load())
}

func (s *Server) serveConnection(conn net.Conn) {
	defer func() {
		s.untrackConnection(conn)
		_ = conn.Close()
		s.active.Add(-1)
		<-s.limit
		s.wait.Done()
	}()
	s.handleConnection(conn)
}

func (s *Server) handleConnection(conn net.Conn) {
	var (
		sink       SessionSink
		terminal   bool
		captureID  uuid.UUID
		abortCause = errors.New("capture session disconnected")
	)
	defer func() {
		if recovered := recover(); recovered != nil {
			abortCause = fmt.Errorf("capture session panic: %v", recovered)
		}
		if sink != nil && !terminal {
			safeAbort(sink, abortCause)
		}
	}()

	header, payload, err := readFrame(conn)
	if err != nil {
		return
	}
	if header.Kind != KindHandshake || header.CaptureID != uuid.Nil || len(payload) != 0 {
		s.protocolError(conn, "handshake required")
		return
	}
	if header.Version != ProtocolVersion {
		s.protocolError(conn, "protocol version mismatch")
		return
	}
	if err := s.writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindHandshake}, nil); err != nil {
		return
	}

	header, payload, err = readFrame(conn)
	if err != nil {
		return
	}
	if header.Version != ProtocolVersion {
		s.protocolError(conn, "protocol version mismatch")
		return
	}
	if header.Kind == KindStatusRequest {
		if header.CaptureID != uuid.Nil || len(payload) != 0 {
			s.protocolError(conn, "invalid status request")
			return
		}
		status := model.Status{}
		if s.config.Status != nil {
			status = s.config.Status()
		}
		encoded, encodeErr := json.Marshal(status)
		if encodeErr != nil {
			s.protocolError(conn, "encode status")
			return
		}
		if err := s.writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindStatusResponse}, encoded); err == nil && s.config.StatusDelivered != nil {
			s.config.StatusDelivered(status)
		}
		return
	}
	if header.Kind != KindBegin || header.CaptureID == uuid.Nil {
		s.protocolError(conn, "begin required")
		return
	}
	var begin model.Begin
	if err := json.Unmarshal(payload, &begin); err != nil || begin.CaptureID != header.CaptureID {
		s.protocolError(conn, "invalid begin")
		return
	}
	captureID = begin.CaptureID
	sink, err = s.factory.Open(begin)
	if err != nil {
		s.protocolError(conn, "open session")
		return
	}
	abortCause = errors.New("capture session ended before commit")
	requestHeadersSeen := false
	responseHeadersSeen := false
	finalSeen := false
	var previousKind Kind

	for {
		header, payload, err = readFrame(conn)
		if err != nil {
			abortCause = fmt.Errorf("capture session read: %w", err)
			return
		}
		if header.Version != ProtocolVersion || header.CaptureID != captureID {
			abortCause = errors.New("capture session identity mismatch")
			s.protocolError(conn, abortCause.Error())
			return
		}
		if finalSeen && header.Kind != KindCommit && header.Kind != KindAbort {
			abortCause = errors.New("message after final")
			s.protocolError(conn, abortCause.Error())
			return
		}
		switch header.Kind {
		case KindRequestHeaders:
			if requestHeadersSeen && previousKind != KindRequestHeaders {
				err = errors.New("non-consecutive request headers")
			} else {
				requestHeadersSeen = true
				err = sink.WriteRequestHeaders(payload)
			}
		case KindResponseHeaders:
			if responseHeadersSeen && previousKind != KindResponseHeaders {
				err = errors.New("non-consecutive response headers")
			} else {
				responseHeadersSeen = true
				err = sink.WriteResponseHeaders(payload)
			}
		case KindRequestChunk:
			err = sink.WriteRequest(payload)
		case KindResponseChunk:
			err = sink.WriteResponse(payload)
		case KindFinal:
			var final model.Final
			if err = json.Unmarshal(payload, &final); err == nil {
				err = sink.Finalize(final)
				if err == nil {
					finalSeen = true
				}
			}
		case KindCommit:
			if len(payload) != 0 || !finalSeen {
				err = errors.New("commit before final")
			} else {
				err = sink.Commit()
				if err == nil {
					terminal = true
					return
				}
			}
		case KindAbort:
			if len(payload) != 0 {
				err = errors.New("invalid abort")
			} else {
				safeAbort(sink, errors.New("capture attempt aborted"))
				terminal = true
				return
			}
		default:
			err = errors.New("illegal capture session message")
		}
		if err != nil {
			abortCause = err
			s.protocolError(conn, "capture session rejected")
			return
		}
		previousKind = header.Kind
	}
}

func (s *Server) writeFrame(conn net.Conn, header Header, payload []byte) error {
	return writeFrameWithDeadline(conn, s.config.WriteTimeout, header, payload)
}

func (s *Server) protocolError(conn net.Conn, message string) {
	payload := []byte(message)
	if len(payload) > MaxPayloadBytes {
		payload = payload[:MaxPayloadBytes]
	}
	_ = s.writeFrame(conn, Header{Version: ProtocolVersion, Kind: KindProtocolError}, payload)
}

func (s *Server) trackConnection(conn net.Conn) bool {
	s.listenerMu.Lock()
	defer s.listenerMu.Unlock()
	if s.closed {
		return false
	}
	s.connections[conn] = struct{}{}
	return true
}

func (s *Server) untrackConnection(conn net.Conn) {
	s.listenerMu.Lock()
	delete(s.connections, conn)
	s.listenerMu.Unlock()
}

func prepareSocketPath(socketPath string) error {
	parent := filepath.Dir(socketPath)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(parent, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket node at %s", socketPath)
	}
	return os.Remove(socketPath)
}

func removeSocketNode(socketPath string) error {
	info, err := os.Lstat(socketPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return nil
	}
	return os.Remove(socketPath)
}

func safeAbort(sink SessionSink, err error) {
	defer func() { _ = recover() }()
	sink.Abort(err)
}
