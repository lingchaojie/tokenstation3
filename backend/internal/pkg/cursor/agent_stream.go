package cursor

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Bidirectional transport for agent.v1.AgentService/Run.
//
// Unlike the api2 server-streaming RPCs, Run must keep writing while reading.
// Half-closing the request before the answer arrives makes the upstream fail
// the turn, and the upstream transport itself requires HTTP/2.

const (
	AgentDefaultFirstByteTimeout = 60 * time.Second
	AgentDefaultIdleTimeout      = 30 * time.Second

	agentWatchdogTick = 250 * time.Millisecond

	// AgentDefaultToolCallDrainWindow collects sibling native tool calls that
	// arrive immediately after the first call in a parallel-tool turn.
	AgentDefaultToolCallDrainWindow = 400 * time.Millisecond

	agentEventBuffer    = 32
	agentErrorBodyLimit = 64 << 10
)

// AgentFrameInfo describes one request or response frame for diagnostics.
type AgentFrameInfo struct {
	Index        int
	Label        string
	PayloadBytes int
	FrameBytes   int
	DelayAfter   time.Duration
	Elapsed      time.Duration
}

// AgentStreamOptions configures one Run turn.
type AgentStreamOptions struct {
	BaseURL       string
	Token         string
	ClientVersion string
	GhostMode     bool
	RequestID     string

	HTTPClient *http.Client

	FirstByteTimeout  time.Duration
	IdleTimeout       time.Duration
	HeartbeatInterval time.Duration

	KeepReadingAfterToolCall bool
	ToolCallDrainWindow      time.Duration

	AllowHTTP1 bool

	OnRequestFrame  func(AgentFrameInfo)
	OnResponseFrame func(AgentFrameInfo, *Frame)
}

func (opts AgentStreamOptions) resolved() AgentStreamOptions {
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = DefaultAgentBaseURL
	}
	if strings.TrimSpace(opts.ClientVersion) == "" {
		opts.ClientVersion = DefaultCLIClientVersion
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = NewAgentHTTPClient()
	}
	if opts.FirstByteTimeout <= 0 {
		opts.FirstByteTimeout = AgentDefaultFirstByteTimeout
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = AgentDefaultIdleTimeout
	}
	if opts.HeartbeatInterval <= 0 {
		opts.HeartbeatInterval = AgentHeartbeatInterval
	}
	if opts.ToolCallDrainWindow <= 0 {
		opts.ToolCallDrainWindow = AgentDefaultToolCallDrainWindow
	}
	return opts
}

// NewAgentHTTPClient builds an HTTP/2-capable streaming client. It deliberately
// has no client-level timeout; the request context and stream watchdog own the
// turn lifetime.
func NewAgentHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		ForceAttemptHTTP2:   true,
		TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12},
		DisableCompression:  true,
		TLSHandshakeTimeout: 20 * time.Second,
		IdleConnTimeout:     90 * time.Second,
	}}
}

// AgentStream is one open Run turn. Response headers have arrived while its
// request stream remains open and is fed by a background goroutine.
type AgentStream struct {
	opts   AgentStreamOptions
	resp   *http.Response
	body   io.Reader
	closer io.Closer

	events chan AgentEvent
	start  time.Time

	cancel   context.CancelFunc
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}
	pr       *io.PipeReader
	pw       *io.PipeWriter

	lastActivity atomic.Int64
	gotOutput    atomic.Bool
	timedOut     atomic.Bool
	drained      atomic.Bool
}

// OpenAgentStream opens the bidirectional Run RPC and returns once response
// headers arrive.
func OpenAgentStream(ctx context.Context, params AgentRunParams, opts AgentStreamOptions) (*AgentStream, error) {
	opts = opts.resolved()
	if strings.TrimSpace(opts.Token) == "" {
		return nil, errors.New("cursor: agent stream needs a token")
	}

	reqCtx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	s := &AgentStream{
		opts: opts, events: make(chan AgentEvent, agentEventBuffer), start: time.Now(),
		cancel: cancel, stop: make(chan struct{}), done: make(chan struct{}), pr: pr, pw: pw,
	}
	s.touch()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, AgentRunURL(opts.BaseURL), pr)
	if err != nil {
		s.abort()
		return nil, fmt.Errorf("cursor: build agent request: %w", err)
	}
	req.ContentLength = -1
	for name, values := range BuildAgentHeaders(opts.Token, opts.ClientVersion, opts.GhostMode, opts.RequestID) {
		for _, value := range values {
			req.Header.Set(name, value)
		}
	}

	go s.writeRequestFrames(params)

	resp, err := s.awaitResponse(req)
	if err != nil {
		s.abort()
		return nil, err
	}
	if err := s.acceptResponse(resp); err != nil {
		s.abort()
		_ = resp.Body.Close()
		return nil, err
	}

	go s.readResponseFrames()
	go s.watchdog()
	return s, nil
}

type agentDoResult struct {
	resp *http.Response
	err  error
}

func (s *AgentStream) awaitResponse(req *http.Request) (*http.Response, error) {
	result := make(chan agentDoResult, 1)
	go func() {
		resp, err := s.opts.HTTPClient.Do(req)
		result <- agentDoResult{resp: resp, err: err}
	}()

	timer := time.NewTimer(s.opts.FirstByteTimeout)
	defer timer.Stop()
	select {
	case response := <-result:
		if response.err != nil {
			return nil, fmt.Errorf("cursor: agent request failed: %w", response.err)
		}
		return response.resp, nil
	case <-req.Context().Done():
		s.discardLateResponse(result)
		return nil, fmt.Errorf("cursor: agent request cancelled: %w", req.Context().Err())
	case <-timer.C:
		s.cancel()
		s.discardLateResponse(result)
		return nil, fmt.Errorf("cursor: no response headers within %s", s.opts.FirstByteTimeout)
	}
}

func (s *AgentStream) discardLateResponse(result <-chan agentDoResult) {
	go func() {
		if response := <-result; response.resp != nil {
			_ = response.resp.Body.Close()
		}
	}()
}

func (s *AgentStream) acceptResponse(resp *http.Response) error {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, agentErrorBodyLimit))
		if agentErr := ParseAgentTrailer(body); agentErr != nil && agentErr.Code != "" {
			agentErr.HTTPStatus = resp.StatusCode
			agentErr.HasHTTPResponse = true
			agentErr.ActualHTTPStatus = resp.StatusCode
			return agentErr
		}
		trimmed := strings.TrimSpace(string(body))
		return &AgentError{
			Message: fmt.Sprintf("%s: %s", resp.Status, trimmed),
			Raw:     trimmed, HTTPStatus: resp.StatusCode,
			HasHTTPResponse: true, ActualHTTPStatus: resp.StatusCode,
		}
	}
	if resp.ProtoMajor < 2 && !s.opts.AllowHTTP1 {
		return fmt.Errorf("cursor: agent stream needs HTTP/2 but negotiated %s; "+
			"the upstream load balancer drops HTTP/1.1 requests with an empty 464", resp.Proto)
	}

	s.resp = resp
	s.closer = resp.Body
	s.body = resp.Body
	if strings.EqualFold(strings.TrimSpace(resp.Header.Get("content-encoding")), "gzip") {
		reader, err := gzip.NewReader(resp.Body)
		if err != nil {
			return fmt.Errorf("cursor: gzip agent response: %w", err)
		}
		s.body = reader
	}
	return nil
}

// Events returns the channel of decoded server events. The channel is closed
// when the turn terminates.
func (s *AgentStream) Events() <-chan AgentEvent { return s.events }

// Response exposes the HTTP response for header inspection. Callers must not
// read Response.Body directly.
func (s *AgentStream) Response() *http.Response { return s.resp }

// Close ends the turn and waits until its frame reader exits. It is idempotent.
func (s *AgentStream) Close() error {
	s.signalStop()
	if s.closer != nil {
		_ = s.closer.Close()
	}
	<-s.done
	return nil
}

func (s *AgentStream) abort() {
	s.signalStop()
	_ = s.pr.Close()
	close(s.done)
}

func (s *AgentStream) signalStop() {
	s.stopOnce.Do(func() {
		close(s.stop)
		s.cancel()
	})
}

func (s *AgentStream) touch() { s.lastActivity.Store(time.Now().UnixNano()) }

func (s *AgentStream) idleFor() time.Duration {
	return time.Since(time.Unix(0, s.lastActivity.Load()))
}

func (s *AgentStream) writeRequestFrames(params AgentRunParams) {
	defer func() { _ = s.pw.Close() }()

	index := 0
	write := func(label string, payload []byte, delay time.Duration) bool {
		index++
		frame := EncodeFrame(payload, false)
		if _, err := s.pw.Write(frame); err != nil {
			return false
		}
		if s.opts.OnRequestFrame != nil {
			s.opts.OnRequestFrame(AgentFrameInfo{
				Index: index, Label: label, PayloadBytes: len(payload), FrameBytes: len(frame),
				DelayAfter: delay, Elapsed: time.Since(s.start),
			})
		}
		return s.pause(delay)
	}

	for _, plan := range BuildRunFrameSequence(params) {
		if !write(plan.Label, plan.Payload, plan.DelayAfter) {
			return
		}
	}

	heartbeat := EncodeClientHeartbeat()
	ticker := time.NewTicker(s.opts.HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if !write("client_heartbeat", heartbeat, 0) {
				return
			}
		}
	}
}

func (s *AgentStream) pause(delay time.Duration) bool {
	if delay <= 0 {
		select {
		case <-s.stop:
			return false
		default:
			return true
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-s.stop:
		return false
	case <-timer.C:
		return true
	}
}

func (s *AgentStream) readResponseFrames() {
	defer close(s.done)
	defer close(s.events)
	defer s.signalStop()

	var drainTimer *time.Timer
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()

	reader := NewFrameReader(s.body)
	for index := 1; ; index++ {
		frame, err := reader.Next()
		if err != nil {
			s.emitTerminal(err)
			return
		}
		s.touch()
		if s.opts.OnResponseFrame != nil {
			s.opts.OnResponseFrame(AgentFrameInfo{
				Index: index, PayloadBytes: len(frame.Payload), FrameBytes: len(frame.Payload) + 5,
				Elapsed: time.Since(s.start),
			}, frame)
		}

		if frame.EndStream {
			if agentErr := ParseAgentTrailer(frame.Payload); agentErr != nil {
				if s.resp != nil {
					agentErr.HasHTTPResponse = true
					agentErr.ActualHTTPStatus = s.resp.StatusCode
				}
				s.emit(AgentEvent{Type: AgentEventError, Err: agentErr})
				return
			}
			s.emit(AgentEvent{Type: AgentEventTurnEnded})
			return
		}

		event, err := ParseAgentServerMessage(frame.Payload)
		if err != nil {
			s.emit(AgentEvent{Type: AgentEventError, Err: fmt.Errorf("cursor: parse agent frame %d: %w", index, err)})
			return
		}
		if event == nil {
			continue
		}
		if drainTimer != nil && !isAgentToolCallEvent(event.Type) && event.Type != AgentEventTurnEnded {
			return
		}
		if isAgentOutput(event.Type) {
			s.gotOutput.Store(true)
		}
		if !s.emit(*event) {
			return
		}
		if event.Type == AgentEventTurnEnded {
			return
		}
		if event.Type == AgentEventToolCall && !s.opts.KeepReadingAfterToolCall && drainTimer == nil {
			drainTimer = time.AfterFunc(s.opts.ToolCallDrainWindow, func() {
				s.drained.Store(true)
				if s.closer != nil {
					_ = s.closer.Close()
				}
			})
		}
	}
}

func isAgentToolCallEvent(eventType AgentEventType) bool {
	switch eventType {
	case AgentEventToolCall, AgentEventToolCallStarted, AgentEventToolCallArgs:
		return true
	default:
		return false
	}
}

func isAgentOutput(eventType AgentEventType) bool {
	switch eventType {
	case AgentEventText, AgentEventThinking, AgentEventThinkingEnd,
		AgentEventToolCall, AgentEventToolCallStarted, AgentEventToolCallArgs:
		return true
	default:
		return false
	}
}

func (s *AgentStream) emitTerminal(err error) {
	switch {
	case errors.Is(err, io.EOF):
		s.emit(AgentEvent{Type: AgentEventTurnEnded})
	case s.drained.Load():
		s.emit(AgentEvent{Type: AgentEventTurnEnded})
	case s.timedOut.Load() && s.gotOutput.Load():
		s.emit(AgentEvent{Type: AgentEventTurnEnded})
	case s.timedOut.Load():
		s.emit(AgentEvent{Type: AgentEventError, Err: fmt.Errorf(
			"cursor: upstream sent no output within %s", s.opts.FirstByteTimeout)})
	case s.stopped():
	default:
		s.emit(AgentEvent{Type: AgentEventError, Err: fmt.Errorf("cursor: read agent stream: %w", err)})
	}
}

func (s *AgentStream) stopped() bool {
	select {
	case <-s.stop:
		return true
	default:
		return false
	}
}

func (s *AgentStream) emit(event AgentEvent) bool {
	select {
	case s.events <- event:
		return true
	case <-s.stop:
		return false
	}
}

func (s *AgentStream) watchdog() {
	ticker := time.NewTicker(agentWatchdogTick)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-s.stop:
			return
		case <-ticker.C:
			budget := s.opts.FirstByteTimeout
			if s.gotOutput.Load() {
				budget = s.opts.IdleTimeout
			}
			if s.idleFor() < budget {
				continue
			}
			s.timedOut.Store(true)
			if s.closer != nil {
				_ = s.closer.Close()
			}
			return
		}
	}
}
