package cursor

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func newReaderStream(body io.Reader, opts AgentStreamOptions) *AgentStream {
	s := &AgentStream{
		opts:   opts.resolved(),
		body:   body,
		events: make(chan AgentEvent, agentEventBuffer),
		start:  time.Now(),
		cancel: func() {},
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
	if closer, ok := body.(io.Closer); ok {
		s.closer = closer
	}
	s.touch()
	return s
}

func drainEvents(t *testing.T, s *AgentStream) []AgentEvent {
	t.Helper()
	go s.readResponseFrames()

	var events []AgentEvent
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-s.Events():
			if !ok {
				return events
			}
			events = append(events, event)
		case <-timer.C:
			t.Fatal("timed out waiting for the event stream to close")
		}
	}
}

func dataFrame(payload []byte) []byte { return EncodeFrame(payload, false) }

func trailerFrame(body string) []byte { return encodeRawFrame(flagEndStream, []byte(body)) }

func textFrame(text string) []byte {
	return dataFrame(agentNest(agentStringField(fieldAgentDeltaText, text),
		fieldAgentServerInteractionUpdate, fieldAgentUpdateTextDelta))
}

func thinkingFrame(text string) []byte {
	return dataFrame(agentNest(agentStringField(fieldAgentDeltaText, text),
		fieldAgentServerInteractionUpdate, fieldAgentUpdateThinkingDelta))
}

func heartbeatFrame() []byte {
	return dataFrame(agentNest(nil, fieldAgentServerInteractionUpdate, fieldAgentUpdateHeartbeat))
}

func turnEndedFrame(inputTokens, outputTokens int64) []byte {
	var turn Writer
	turn.WriteInt64(fieldAgentTurnInputTokens, inputTokens)
	turn.WriteInt64(fieldAgentTurnOutputTokens, outputTokens)
	return dataFrame(agentNest(turn.Bytes(), fieldAgentServerInteractionUpdate, fieldAgentUpdateTurnEnded))
}

func toolFrame(name, id string) []byte {
	return dataFrame(agentMCPArgsPayload("", name, id, nil))
}

func TestAgentStreamEmitsTextThenTurnEnd(t *testing.T) {
	body := bytes.NewReader(bytes.Join([][]byte{
		textFrame("Hel"),
		textFrame("lo"),
		trailerFrame("{}"),
	}, nil))

	events := drainEvents(t, newReaderStream(body, AgentStreamOptions{}))
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3: %+v", len(events), events)
	}
	if events[0].Type != AgentEventText || events[1].Type != AgentEventText {
		t.Fatalf("events = %+v, want two text events", events)
	}
	if got := events[0].Text + events[1].Text; got != "Hello" {
		t.Errorf("assembled text = %q, want %q", got, "Hello")
	}
	if events[2].Type != AgentEventTurnEnded {
		t.Errorf("final event = %s, want turn_ended", events[2].Type)
	}
	if events[2].ProviderTerminal {
		t.Error("empty Connect end must not claim an explicit provider TurnEnded update")
	}
}

func TestAgentStreamSurfacesTrailerError(t *testing.T) {
	body := io.NopCloser(bytes.NewReader(bytes.Join([][]byte{
		textFrame("partial"),
		trailerFrame(`{"error":{"code":"permission_denied","message":"client version too old"}}`),
	}, nil)))
	s := newReaderStream(nil, AgentStreamOptions{})
	if err := s.acceptResponse(&http.Response{
		Status: "200 OK", StatusCode: http.StatusOK, Proto: "HTTP/2.0", ProtoMajor: 2,
		Header: http.Header{}, Body: body,
	}); err != nil {
		t.Fatalf("accept response: %v", err)
	}

	events := drainEvents(t, s)
	last := events[len(events)-1]
	if last.Type != AgentEventError {
		t.Fatalf("final event = %s, want error", last.Type)
	}
	var agentErr *AgentError
	if !errors.As(last.Err, &agentErr) {
		t.Fatalf("error = %v, want an *AgentError", last.Err)
	}
	if agentErr.Code != "permission_denied" || agentErr.HTTPStatus != http.StatusForbidden {
		t.Errorf("error = %+v, want permission_denied/403", agentErr)
	}
	if !agentErr.HasHTTPResponse || agentErr.ActualHTTPStatus != http.StatusOK {
		t.Errorf("HTTP provenance = has:%v actual:%d, want true/200", agentErr.HasHTTPResponse, agentErr.ActualHTTPStatus)
	}
}

func TestAgentStreamTreatsCleanEOFAsEnd(t *testing.T) {
	events := drainEvents(t, newReaderStream(bytes.NewReader(textFrame("hi")), AgentStreamOptions{}))
	if len(events) != 2 || events[1].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want text then turn_ended", events)
	}
	if events[1].ProviderTerminal {
		t.Error("clean EOF must remain a synthetic, non-authoritative end")
	}
}

func TestAgentStreamExplicitEndBeatsIdleTimeout(t *testing.T) {
	const idleTimeout = 150 * time.Millisecond
	bodies := map[string][]byte{
		"turn-ended update": bytes.Join([][]byte{thinkingFrame("hm"), turnEndedFrame(10, 20)}, nil),
		"connect trailer":   bytes.Join([][]byte{thinkingFrame("hm"), trailerFrame("{}")}, nil),
	}

	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			s := newReaderStream(bytes.NewReader(body), AgentStreamOptions{IdleTimeout: idleTimeout})
			s.closer = io.NopCloser(strings.NewReader(""))
			go s.watchdog()

			events := drainEvents(t, s)
			if last := events[len(events)-1]; last.Type != AgentEventTurnEnded {
				t.Fatalf("final event = %s, want turn_ended: %+v", last.Type, events)
			}
			time.Sleep(idleTimeout + 2*agentWatchdogTick)
			if s.timedOut.Load() {
				t.Fatal("idle watchdog fired after the explicit end")
			}
		})
	}
}

func TestAgentStreamStopsAtTurnEnded(t *testing.T) {
	body := bytes.NewReader(bytes.Join([][]byte{
		turnEndedFrame(10, 20),
		textFrame("after the end"),
	}, nil))

	events := drainEvents(t, newReaderStream(body, AgentStreamOptions{}))
	if len(events) != 1 || events[0].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want one turn_ended", events)
	}
	if events[0].Usage == nil || events[0].Usage.InputTokens != 10 || events[0].Usage.OutputTokens != 20 {
		t.Errorf("usage = %+v, want input=10 output=20", events[0].Usage)
	}
	if !events[0].ProviderTerminal {
		t.Error("protobuf TurnEnded update must retain provider-terminal provenance")
	}
}

func TestAgentStreamParallelToolCallsDrainTogether(t *testing.T) {
	body := bytes.NewReader(bytes.Join([][]byte{
		toolFrame("read_file", "call-1"),
		toolFrame("list_dir", "call-2"),
		toolFrame("grep", "call-3"),
	}, nil))

	events := drainEvents(t, newReaderStream(body, AgentStreamOptions{
		ToolCallDrainWindow: 2 * time.Second,
	}))
	var ids []string
	for _, event := range events {
		if event.Type == AgentEventToolCall {
			ids = append(ids, event.ToolCall.ID)
		}
	}
	if got := strings.Join(ids, ","); got != "call-1,call-2,call-3" {
		t.Fatalf("tool call IDs = %q, want all three: %+v", got, events)
	}
	if last := events[len(events)-1]; last.Type != AgentEventTurnEnded {
		t.Errorf("final event = %s, want turn_ended", last.Type)
	}
}

func TestAgentStreamToolCallDrainWindowBoundsWait(t *testing.T) {
	body := &blockingBody{
		prefix:  bytes.NewReader(toolFrame("get_weather", "call-1")),
		unblock: make(chan struct{}),
	}
	start := time.Now()
	events := drainEvents(t, newReaderStream(body, AgentStreamOptions{
		ToolCallDrainWindow: 80 * time.Millisecond,
	}))

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("tool drain took %s, want a bounded wait", elapsed)
	}
	if len(events) != 2 || events[0].Type != AgentEventToolCall || events[1].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want tool_call then turn_ended", events)
	}
	if events[1].ProviderTerminal {
		t.Error("tool-drain completion must remain a synthetic, non-authoritative end")
	}
}

func TestAgentStreamKeepReadingAfterToolCall(t *testing.T) {
	body := bytes.NewReader(bytes.Join([][]byte{
		toolFrame("get_weather", "call-1"),
		textFrame("and more"),
		trailerFrame("{}"),
	}, nil))

	events := drainEvents(t, newReaderStream(body, AgentStreamOptions{KeepReadingAfterToolCall: true}))
	if len(events) != 3 || events[1].Type != AgentEventText || events[1].Text != "and more" {
		t.Fatalf("events = %+v, want tool call, text, turn end", events)
	}
}

func TestAgentStreamDefaultsIncludeThirtySecondIdleAndDrainWindow(t *testing.T) {
	opts := (AgentStreamOptions{}).resolved()
	if opts.FirstByteTimeout != AgentDefaultFirstByteTimeout {
		t.Errorf("first byte timeout = %s, want %s", opts.FirstByteTimeout, AgentDefaultFirstByteTimeout)
	}
	if opts.IdleTimeout != 30*time.Second {
		t.Errorf("idle timeout = %s, want 30s", opts.IdleTimeout)
	}
	if opts.IdleTimeout != AgentDefaultIdleTimeout {
		t.Errorf("idle timeout = %s, want default %s", opts.IdleTimeout, AgentDefaultIdleTimeout)
	}
	if opts.HeartbeatInterval != AgentHeartbeatInterval {
		t.Errorf("heartbeat interval = %s, want %s", opts.HeartbeatInterval, AgentHeartbeatInterval)
	}
	if opts.ToolCallDrainWindow != 400*time.Millisecond {
		t.Errorf("tool call drain = %s, want 400ms", opts.ToolCallDrainWindow)
	}
	if opts.ToolCallDrainWindow != AgentDefaultToolCallDrainWindow {
		t.Errorf("tool call drain = %s, want default %s", opts.ToolCallDrainWindow, AgentDefaultToolCallDrainWindow)
	}
}

func TestAgentStreamFirstFrameTimeoutReportsNoOutput(t *testing.T) {
	body := &blockingBody{prefix: bytes.NewReader(nil), unblock: make(chan struct{})}
	s := newReaderStream(body, AgentStreamOptions{FirstByteTimeout: 40 * time.Millisecond})
	go s.watchdog()
	events := drainEvents(t, s)

	if len(events) != 1 || events[0].Type != AgentEventError {
		t.Fatalf("events = %+v, want one timeout error", events)
	}
	if !strings.Contains(events[0].Err.Error(), "no output") {
		t.Errorf("error = %v, want no-output timeout", events[0].Err)
	}
}

func TestAgentStreamIdleTimeoutAfterOutputIsCleanEnd(t *testing.T) {
	body := &blockingBody{prefix: bytes.NewReader(thinkingFrame("working")), unblock: make(chan struct{})}
	s := newReaderStream(body, AgentStreamOptions{
		FirstByteTimeout: 5 * time.Second,
		IdleTimeout:      40 * time.Millisecond,
	})
	go s.watchdog()
	events := drainEvents(t, s)

	if len(events) != 2 || events[0].Type != AgentEventThinking || events[1].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want thinking then clean end", events)
	}
	if events[1].ProviderTerminal {
		t.Error("idle completion after output must remain a synthetic, non-authoritative end")
	}
}

func TestAgentStreamIdleTimeoutResetsOnEveryResponseFrame(t *testing.T) {
	const (
		idleTimeout = 450 * time.Millisecond
		frameGap    = 140 * time.Millisecond
	)
	pr, pw := io.Pipe()
	s := newReaderStream(pr, AgentStreamOptions{IdleTimeout: idleTimeout})
	s.closer = pr

	go func() {
		defer func() { _ = pw.Close() }()
		frames := [][]byte{
			thinkingFrame("weighing"),
			heartbeatFrame(),
			heartbeatFrame(),
			heartbeatFrame(),
			textFrame("the answer"),
			trailerFrame("{}"),
		}
		for _, frame := range frames {
			time.Sleep(frameGap)
			if _, err := pw.Write(frame); err != nil {
				return
			}
		}
	}()

	go s.watchdog()
	events := drainEvents(t, s)
	if s.timedOut.Load() {
		t.Fatal("watchdog fired while response frames were still arriving")
	}
	var text strings.Builder
	for _, event := range events {
		if event.Type == AgentEventText {
			text.WriteString(event.Text)
		}
	}
	if text.String() != "the answer" {
		t.Errorf("text = %q, want the final answer after heartbeats", text.String())
	}
	if last := events[len(events)-1]; last.Type != AgentEventTurnEnded {
		t.Errorf("final event = %s, want turn_ended", last.Type)
	}
}

func TestAgentStreamReportsMalformedAndTruncatedFrames(t *testing.T) {
	t.Run("malformed protobuf", func(t *testing.T) {
		events := drainEvents(t, newReaderStream(bytes.NewReader(dataFrame([]byte{0x0a, 0x7f, 0x01})), AgentStreamOptions{}))
		if len(events) != 1 || events[0].Type != AgentEventError {
			t.Fatalf("events = %+v, want one parse error", events)
		}
	})

	t.Run("truncated envelope", func(t *testing.T) {
		full := textFrame("hello")
		events := drainEvents(t, newReaderStream(bytes.NewReader(full[:len(full)-2]), AgentStreamOptions{}))
		if len(events) != 1 || events[0].Type != AgentEventError {
			t.Fatalf("events = %+v, want one read error", events)
		}
		if !strings.Contains(events[0].Err.Error(), "unexpected EOF") {
			t.Errorf("error = %v, want unexpected EOF", events[0].Err)
		}
	})
}

func TestAgentStreamResponseFrameObserverSeesDecodedFrames(t *testing.T) {
	var infos []AgentFrameInfo
	var frames []*Frame
	s := newReaderStream(bytes.NewReader(bytes.Join([][]byte{textFrame("hi"), trailerFrame("{}")}, nil)), AgentStreamOptions{
		OnResponseFrame: func(info AgentFrameInfo, frame *Frame) {
			infos = append(infos, info)
			frames = append(frames, frame)
		},
	})
	drainEvents(t, s)

	if len(infos) != 2 || infos[0].Index != 1 || infos[1].Index != 2 {
		t.Fatalf("response frame infos = %+v, want indexes 1 and 2", infos)
	}
	if infos[0].FrameBytes != infos[0].PayloadBytes+5 || frames[1].EndStream != true {
		t.Errorf("observed frames = %+v infos = %+v", frames, infos)
	}
}

func TestAgentStreamWholeBodyGzipIsDecoded(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write(bytes.Join([][]byte{textFrame("zipped"), trailerFrame("{}")}, nil))
	_ = zw.Close()

	s := newReaderStream(nil, AgentStreamOptions{AllowHTTP1: true})
	resp := &http.Response{
		Status: "200 OK", StatusCode: http.StatusOK,
		Proto: "HTTP/2.0", ProtoMajor: 2,
		Header: http.Header{"Content-Encoding": []string{"gzip"}},
		Body:   io.NopCloser(bytes.NewReader(compressed.Bytes())),
	}
	if err := s.acceptResponse(resp); err != nil {
		t.Fatalf("accept gzip response: %v", err)
	}
	events := drainEvents(t, s)
	if len(events) != 2 || events[0].Text != "zipped" || events[1].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want decoded text then turn end", events)
	}
}

func TestAgentStreamAcceptResponseRequiresHTTP2(t *testing.T) {
	response := func() *http.Response {
		return &http.Response{
			Status: "200 OK", StatusCode: http.StatusOK,
			Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: http.Header{}, Body: io.NopCloser(strings.NewReader("")),
		}
	}

	if err := newReaderStream(nil, AgentStreamOptions{}).acceptResponse(response()); err == nil || !strings.Contains(err.Error(), "HTTP/2") {
		t.Fatalf("error = %v, want HTTP/2 requirement", err)
	}
	if err := newReaderStream(nil, AgentStreamOptions{AllowHTTP1: true}).acceptResponse(response()); err != nil {
		t.Fatalf("AllowHTTP1 must waive the requirement: %v", err)
	}
}

func TestAgentStreamWritePacesEveryFramePlanThenHeartbeats(t *testing.T) {
	params := AgentRunParams{Prompt: "hi", MessageID: "m", ConversationID: "c"}
	plans := BuildRunFrameSequence(params)
	pr, pw := io.Pipe()
	observed := make(chan AgentFrameInfo, len(plans)+2)
	s := &AgentStream{
		opts: AgentStreamOptions{
			HeartbeatInterval: 25 * time.Millisecond,
			OnRequestFrame: func(info AgentFrameInfo) {
				observed <- info
			},
		}.resolved(),
		start:  time.Now(),
		cancel: func() {},
		stop:   make(chan struct{}),
		pr:     pr,
		pw:     pw,
	}
	frames := make(chan *Frame, len(plans)+2)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		reader := NewFrameReader(pr)
		for {
			frame, err := reader.Next()
			if err != nil {
				return
			}
			frames <- frame
		}
	}()
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		s.writeRequestFrames(params)
	}()

	cumulativeDelay := time.Duration(0)
	for index, plan := range plans {
		info := receiveFrameInfo(t, observed)
		frame := receiveFrame(t, frames)
		if info.Index != index+1 || info.Label != plan.Label || info.DelayAfter != plan.DelayAfter {
			t.Errorf("plan %d info = %+v, want label=%q delay=%s", index, info, plan.Label, plan.DelayAfter)
		}
		if !bytes.Equal(frame.Payload, plan.Payload) {
			t.Errorf("plan %d payload differs", index)
		}
		if info.PayloadBytes != len(plan.Payload) || info.FrameBytes != len(plan.Payload)+5 {
			t.Errorf("plan %d accounting = %+v", index, info)
		}
		if info.Elapsed < cumulativeDelay {
			t.Errorf("plan %d arrived at %s before cumulative pacing %s", index, info.Elapsed, cumulativeDelay)
		}
		cumulativeDelay += plan.DelayAfter
	}

	heartbeatInfo := receiveFrameInfo(t, observed)
	heartbeat := receiveFrame(t, frames)
	if heartbeatInfo.Label != "client_heartbeat" || !bytes.Equal(heartbeat.Payload, EncodeClientHeartbeat()) {
		t.Errorf("heartbeat info=%+v payload=%x", heartbeatInfo, heartbeat.Payload)
	}
	if heartbeatInfo.Elapsed < cumulativeDelay+s.opts.HeartbeatInterval {
		t.Errorf("heartbeat arrived at %s before plans (%s) plus interval (%s)", heartbeatInfo.Elapsed, cumulativeDelay, s.opts.HeartbeatInterval)
	}

	s.signalStop()
	_ = pr.Close()
	select {
	case <-writeDone:
	case <-time.After(time.Second):
		t.Fatal("request writer did not stop")
	}
	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("request frame reader did not stop")
	}
}

func receiveFrameInfo(t *testing.T, ch <-chan AgentFrameInfo) AgentFrameInfo {
	t.Helper()
	select {
	case info := <-ch:
		return info
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for request frame info")
		return AgentFrameInfo{}
	}
}

func receiveFrame(t *testing.T, ch <-chan *Frame) *Frame {
	t.Helper()
	select {
	case frame := <-ch:
		return frame
	case <-time.After(8 * time.Second):
		t.Fatal("timed out waiting for request frame")
		return nil
	}
}

func TestOpenAgentStreamRequiresToken(t *testing.T) {
	_, err := OpenAgentStream(context.Background(), AgentRunParams{Prompt: "hi"}, AgentStreamOptions{})
	if err == nil {
		t.Fatal("expected an error when no token is configured")
	}
}

func TestOpenAgentStreamUsesPipeBodyAndStreamingHeaders(t *testing.T) {
	type observation struct {
		bodyIsPipe    bool
		contentLength int64
		method        string
		path          string
		authorization string
		frame         *Frame
		err           error
	}
	observed := make(chan observation, 1)
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		go func() {
			<-req.Context().Done()
			_ = req.Body.Close()
		}()
		frame, err := NewFrameReader(req.Body).Next()
		_, bodyIsPipe := req.Body.(*io.PipeReader)
		observed <- observation{
			bodyIsPipe: bodyIsPipe, contentLength: req.ContentLength,
			method: req.Method, path: req.URL.Path,
			authorization: req.Header.Get("authorization"), frame: frame, err: err,
		}
		return h2Response(io.NopCloser(bytes.NewReader(trailerFrame("{}")))), nil
	})}

	stream, err := OpenAgentStream(context.Background(), AgentRunParams{
		Prompt: "hi", MessageID: "m", ConversationID: "c",
	}, AgentStreamOptions{
		BaseURL: "https://agent.example.test", Token: "user::jwt", HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	events := collectOpenStreamEvents(t, stream)
	if len(events) != 1 || events[0].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want clean end", events)
	}

	got := <-observed
	if got.err != nil {
		t.Fatalf("read first request frame: %v", got.err)
	}
	if !got.bodyIsPipe || got.contentLength != -1 {
		t.Errorf("request body pipe=%v contentLength=%d, want pipe/-1", got.bodyIsPipe, got.contentLength)
	}
	if got.method != http.MethodPost || got.path != EndpointAgentRun || got.authorization != "Bearer jwt" {
		t.Errorf("request method=%q path=%q authorization=%q", got.method, got.path, got.authorization)
	}
	plans := BuildRunFrameSequence(AgentRunParams{Prompt: "hi", MessageID: "m", ConversationID: "c"})
	if got.frame == nil || !bytes.Equal(got.frame.Payload, plans[0].Payload) {
		t.Errorf("first request frame = %+v, want run request", got.frame)
	}
	_ = stream.Close()
}

func TestOpenAgentStreamNon2xxBodyIsBoundedAndClosed(t *testing.T) {
	body := &countingBody{remaining: agentErrorBodyLimit * 2}
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Status: "503 Service Unavailable", StatusCode: http.StatusServiceUnavailable,
			Proto: "HTTP/2.0", ProtoMajor: 2, Header: http.Header{}, Body: body,
		}, nil
	})}

	_, err := OpenAgentStream(context.Background(), AgentRunParams{Prompt: "hi"}, AgentStreamOptions{
		BaseURL: "https://agent.example.test", Token: "test-token", HTTPClient: client,
	})
	var agentErr *AgentError
	if !errors.As(err, &agentErr) || agentErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Fatalf("error = %v, want *AgentError with 503", err)
	}
	if !agentErr.HasHTTPResponse || agentErr.ActualHTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTP provenance = has:%v actual:%d, want true/503", agentErr.HasHTTPResponse, agentErr.ActualHTTPStatus)
	}
	if got := body.read.Load(); got != agentErrorBodyLimit {
		t.Errorf("body bytes read = %d, want bounded %d", got, agentErrorBodyLimit)
	}
	if !body.closed.Load() {
		t.Fatal("non-2xx response body was not closed")
	}
}

func TestOpenAgentStreamNon2xxConnectBodyKeepsActualHTTPClassification(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			Status: "503 Service Unavailable", StatusCode: http.StatusServiceUnavailable,
			Proto: "HTTP/2.0", ProtoMajor: 2, Header: http.Header{},
			Body: io.NopCloser(strings.NewReader(`{"error":{"code":"permission_denied","message":"rejected"}}`)),
		}, nil
	})}

	_, err := OpenAgentStream(context.Background(), AgentRunParams{Prompt: "hi"}, AgentStreamOptions{
		BaseURL: "https://agent.example.test", Token: "test-token", HTTPClient: client,
	})
	var agentErr *AgentError
	if !errors.As(err, &agentErr) {
		t.Fatalf("error = %v, want *AgentError", err)
	}
	if agentErr.HTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("classification status = %d, want actual non-2xx 503", agentErr.HTTPStatus)
	}
	if !agentErr.HasHTTPResponse || agentErr.ActualHTTPStatus != http.StatusServiceUnavailable {
		t.Errorf("HTTP provenance = has:%v actual:%d, want true/503", agentErr.HasHTTPResponse, agentErr.ActualHTTPStatus)
	}
}

func TestOpenAgentStreamFirstByteTimeoutClosesLateResponse(t *testing.T) {
	release := make(chan struct{})
	body := &trackedReadCloser{Reader: strings.NewReader(""), closed: make(chan struct{})}
	started := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		close(started)
		<-release
		return h2Response(body), nil
	})}

	start := time.Now()
	_, err := OpenAgentStream(context.Background(), AgentRunParams{Prompt: "hi"}, AgentStreamOptions{
		BaseURL: "https://agent.example.test", Token: "test-token", HTTPClient: client,
		FirstByteTimeout: 40 * time.Millisecond,
	})
	if err == nil || !strings.Contains(err.Error(), "no response headers") {
		t.Fatalf("error = %v, want response-header timeout", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("header timeout took %s", elapsed)
	}
	<-started
	close(release)
	select {
	case <-body.closed:
	case <-time.After(time.Second):
		t.Fatal("late response body was not closed")
	}
}

func TestOpenAgentStreamCancellationAfterHeadersStopsLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	responseReady := make(chan struct{})
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := &contextBody{ctx: req.Context(), done: make(chan struct{})}
		go func() {
			<-req.Context().Done()
			_ = req.Body.Close()
		}()
		close(responseReady)
		return h2Response(body), nil
	})}

	stream, err := OpenAgentStream(ctx, AgentRunParams{Prompt: "hi"}, AgentStreamOptions{
		BaseURL: "https://agent.example.test", Token: "test-token", HTTPClient: client,
	})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	<-responseReady
	cancel()
	events := collectOpenStreamEvents(t, stream)
	if len(events) != 1 || events[0].Type != AgentEventError || !errors.Is(events[0].Err, context.Canceled) {
		t.Fatalf("events = %+v, want context cancellation error", events)
	}
	_ = stream.Close()
}

func TestOpenAgentStreamHTTP2ReadsResponseWhileRequestStaysOpen(t *testing.T) {
	requestDrained := make(chan struct{})
	server := newAgentTestServer(t, func(w http.ResponseWriter, req *http.Request) {
		if req.ProtoMajor != 2 {
			t.Errorf("request protocol = %s, want HTTP/2", req.Proto)
		}
		if req.ContentLength != -1 {
			t.Errorf("request content length = %d, want -1", req.ContentLength)
		}
		first, err := NewFrameReader(req.Body).Next()
		if err != nil {
			t.Errorf("read first request frame: %v", err)
			return
		}
		if len(first.Payload) == 0 {
			t.Error("first request frame is empty")
		}
		go func() {
			_, _ = io.Copy(io.Discard, req.Body)
			close(requestDrained)
		}()

		w.Header().Set("content-type", agentConnectContentType)
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		_, _ = w.Write(textFrame("hi there"))
		flusher.Flush()
		_, _ = w.Write(trailerFrame("{}"))
		flusher.Flush()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := OpenAgentStream(ctx, AgentRunParams{
		Prompt: "hi", MessageID: "m", ConversationID: "c",
	}, AgentStreamOptions{
		BaseURL: server.URL, Token: "test-token", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if stream.Response().ProtoMajor != 2 {
		t.Fatalf("response protocol = %s, want HTTP/2", stream.Response().Proto)
	}
	events := collectOpenStreamEvents(t, stream)
	if len(events) != 2 || events[0].Type != AgentEventText || events[0].Text != "hi there" || events[1].Type != AgentEventTurnEnded {
		t.Fatalf("events = %+v, want text then turn end", events)
	}
	_ = stream.Close()
	select {
	case <-requestDrained:
	case <-time.After(time.Second):
		t.Fatal("request body goroutine did not exit")
	}
}

func TestAgentStreamCloseIsIdempotentAndUnblocksRead(t *testing.T) {
	t.Run("active stream", func(t *testing.T) {
		body := &blockingBody{prefix: bytes.NewReader(nil), unblock: make(chan struct{})}
		s := newReaderStream(body, AgentStreamOptions{})
		go s.readResponseFrames()

		done := make(chan struct{})
		go func() {
			_ = s.Close()
			_ = s.Close()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("Close blocked on active stream")
		}
		if _, ok := <-s.Events(); ok {
			t.Fatal("events channel remained open")
		}
	})

	t.Run("finished stream", func(t *testing.T) {
		s := newReaderStream(bytes.NewReader(trailerFrame("{}")), AgentStreamOptions{})
		s.closer = io.NopCloser(strings.NewReader(""))
		drainEvents(t, s)
		if err := s.Close(); err != nil {
			t.Fatalf("close finished stream: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatalf("close finished stream again: %v", err)
		}
	})
}

func TestAgentStreamRepeatedCloseDoesNotLeakLifecycleGoroutines(t *testing.T) {
	for index := 0; index < 25; index++ {
		body := &blockingBody{prefix: bytes.NewReader(nil), unblock: make(chan struct{})}
		s := newReaderStream(body, AgentStreamOptions{})
		go s.readResponseFrames()
		if err := s.Close(); err != nil {
			t.Fatalf("stream %d close: %v", index, err)
		}
		select {
		case <-s.done:
		default:
			t.Fatalf("stream %d lifecycle goroutine still running", index)
		}
	}
}

func collectOpenStreamEvents(t *testing.T, stream *AgentStream) []AgentEvent {
	t.Helper()
	var events []AgentEvent
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-stream.Events():
			if !ok {
				return events
			}
			events = append(events, event)
		case <-timer.C:
			t.Fatal("timed out waiting for open stream events")
		}
	}
}

func newAgentTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func h2Response(body io.ReadCloser) *http.Response {
	return &http.Response{
		Status: "200 OK", StatusCode: http.StatusOK,
		Proto: "HTTP/2.0", ProtoMajor: 2,
		Header: http.Header{}, Body: body,
	}
}

type blockingBody struct {
	prefix  *bytes.Reader
	unblock chan struct{}
	once    sync.Once
}

func (body *blockingBody) Read(p []byte) (int, error) {
	if body.prefix.Len() > 0 {
		return body.prefix.Read(p)
	}
	<-body.unblock
	return 0, io.ErrClosedPipe
}

func (body *blockingBody) Close() error {
	body.once.Do(func() { close(body.unblock) })
	return nil
}

type countingBody struct {
	remaining int64
	read      atomic.Int64
	closed    atomic.Bool
}

func (body *countingBody) Read(p []byte) (int, error) {
	if body.remaining == 0 {
		return 0, io.EOF
	}
	count := int64(len(p))
	if count > body.remaining {
		count = body.remaining
	}
	for index := int64(0); index < count; index++ {
		p[index] = 'x'
	}
	body.remaining -= count
	body.read.Add(count)
	return int(count), nil
}

func (body *countingBody) Close() error {
	body.closed.Store(true)
	return nil
}

type trackedReadCloser struct {
	io.Reader
	closed chan struct{}
	once   sync.Once
}

func (body *trackedReadCloser) Close() error {
	body.once.Do(func() {
		close(body.closed)
	})
	return nil
}

type contextBody struct {
	ctx  context.Context
	once sync.Once
	done chan struct{}
}

func (body *contextBody) Read([]byte) (int, error) {
	select {
	case <-body.ctx.Done():
		return 0, body.ctx.Err()
	case <-body.done:
		return 0, io.ErrClosedPipe
	}
}

func (body *contextBody) Close() error {
	body.once.Do(func() {
		close(body.done)
	})
	return nil
}
