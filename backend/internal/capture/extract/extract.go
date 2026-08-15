// Package extract incrementally derives bounded capture metadata from provider
// request and response streams.
package extract

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
)

const (
	maxMetadataBytes = 1 << 20
	maxJSONDepth     = 128
	maxJSONKeyBytes  = 256
	readerBufferSize = 32 << 10
	maxAWSHeaders    = 64 << 10
	maxAWSFrame      = 64 << 20
	maxSSELineBytes  = maxMetadataBytes + len("data: ")
)

var (
	ErrUnsupportedFormat = errors.New("unsupported capture payload format")
	ErrMetadataLimit     = errors.New("capture metadata exceeds limit")
	ErrMalformedPayload  = errors.New("malformed capture payload")
	ErrReadPayload       = errors.New("read capture payload")
	ErrStreamClosed      = errors.New("capture metadata stream is closed")
)

// Stream consumes the same chunks observed by a capture attempt. Feed methods
// retain at most bounded parser scratch; Finalize closes and joins all decoder
// goroutines before returning the immutable result.
type Stream interface {
	FeedRequest([]byte) error
	FeedResponse([]byte) error
	Finalize(model.Final) (model.Extracted, error)
}

// Input is the reader-based fixture and compatibility surface. Initial seeds
// trusted legacy columns: payload fields overwrite a seed only when they are
// actually observed, including when the observed value is zero.
type Input struct {
	Format   model.PayloadFormat
	Request  io.Reader
	Response io.Reader
	Final    model.Final
	// FinalPresent distinguishes an explicitly observed all-zero Final from an
	// omitted Final in reader fixtures.
	FinalPresent bool
	Initial      model.Extracted
}

type parserMode uint8

const (
	requestMode parserMode = iota
	responseMode
)

type responseState struct {
	value model.Extracted

	stopPresent       bool
	stopRank          int
	inputPresent      bool
	inputRank         int
	outputPresent     bool
	outputRank        int
	cacheReadPresent  bool
	cacheReadRank     int
	cacheWritePresent bool
	cacheWriteRank    int
	signaturePresent  bool
}

type requestState struct {
	value model.Extracted

	sessionRank int
	effortRank  int
	effortRaw   string
	typeRank    int
}

type parseResult struct {
	request  requestState
	response responseState
	err      error
}

type responseParser interface {
	feed([]byte) error
	finish() (responseState, error)
	abort()
}

type metadataStream struct {
	mu       sync.Mutex
	ctx      context.Context
	request  *jsonBody
	response responseParser
	initial  model.Extracted
	closed   bool
}

// New constructs a bounded extractor for the declared response format. All
// provider requests remain JSON; only the response parser varies by format.
func New(ctx context.Context, format model.PayloadFormat) (Stream, error) {
	return newStream(ctx, format, model.Extracted{})
}

func newStream(ctx context.Context, format model.PayloadFormat, initial model.Extracted) (*metadataStream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var response responseParser
	switch format {
	case model.PayloadJSON:
		response = newJSONBody(ctx, responseMode)
	case model.PayloadSSE:
		response = &sseParser{}
	case model.PayloadAWSEventStream:
		response = &awsParser{}
	default:
		return nil, ErrUnsupportedFormat
	}
	return &metadataStream{
		ctx:      ctx,
		request:  newJSONBody(ctx, requestMode),
		response: response,
		initial:  initial,
	}, nil
}

func (s *metadataStream) FeedRequest(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStreamClosed
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return sanitizeError(s.request.feed(payload))
}

func (s *metadataStream) FeedResponse(payload []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrStreamClosed
	}
	if err := s.ctx.Err(); err != nil {
		return err
	}
	return sanitizeError(s.response.feed(payload))
}

func (s *metadataStream) Finalize(final model.Final) (model.Extracted, error) {
	return s.finalize(final, true)
}

// FinalizeWithoutFinal joins parsers without applying a terminal override.
// It supports direct spool tests that commit without a protocol Final frame.
func (s *metadataStream) FinalizeWithoutFinal() (model.Extracted, error) {
	return s.finalize(model.Final{}, false)
}

func (s *metadataStream) finalize(final model.Final, finalPresent bool) (model.Extracted, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return model.Extracted{}, ErrStreamClosed
	}
	s.closed = true

	request, requestErr := s.request.finishRequest()
	response, responseErr := s.response.finish()
	extracted := s.initial
	mergeRequest(&extracted, request)
	mergeResponse(&extracted, response)

	if finalPresent {
		extracted.InputTokens = final.InputTokens
		extracted.OutputTokens = final.OutputTokens
		extracted.CacheReadTokens = final.CacheReadTokens
		extracted.CacheCreationTokens = final.CacheCreationTokens
		extracted.StopReason = final.StopReason
	}
	return extracted, firstSanitizedError(requestErr, responseErr, s.ctx.Err())
}

// abort is intentionally outside Stream: capture attempts use it to cancel
// and join decoder goroutines when the attempt is abandoned before Finalize.
func (s *metadataStream) abort() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.request.abort()
	s.response.abort()
}

// Abort cancels and joins all decoder goroutines without publishing metadata.
// Capture attempts call it when their partial record is abandoned.
func (s *metadataStream) Abort() { s.abort() }

// FromReaders feeds fixed-size chunks through the exact Stream implementation.
// It never materializes either body as a whole.
func FromReaders(ctx context.Context, in Input) (model.Extracted, error) {
	stream, err := newStream(ctx, in.Format, in.Initial)
	if err != nil {
		return model.Extracted{}, err
	}
	var feedErr error
	if err := feedReader(ctx, in.Request, stream.FeedRequest); err != nil {
		feedErr = err
	}
	if err := feedReader(ctx, in.Response, stream.FeedResponse); err != nil && feedErr == nil {
		feedErr = err
	}
	var extracted model.Extracted
	var finalizeErr error
	if in.FinalPresent || in.Final != (model.Final{}) {
		extracted, finalizeErr = stream.Finalize(in.Final)
	} else {
		extracted, finalizeErr = stream.FinalizeWithoutFinal()
	}
	return extracted, firstSanitizedError(feedErr, finalizeErr)
}

func feedReader(ctx context.Context, reader io.Reader, feed func([]byte) error) error {
	if reader == nil {
		return nil
	}
	buf := make([]byte, readerBufferSize)
	var firstErr error
	for {
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		n, readErr := reader.Read(buf)
		if n > 0 {
			if err := feed(buf[:n]); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) && firstErr == nil {
				firstErr = ErrReadPayload
			}
			return firstErr
		}
		if n == 0 {
			if firstErr != nil {
				return firstErr
			}
			return ErrReadPayload
		}
	}
}

func mergeRequest(dst *model.Extracted, src requestState) {
	if dst.SessionID == "" {
		dst.SessionID = src.value.SessionID
	}
	if dst.ThinkingEffort == "" {
		dst.ThinkingEffort = src.value.ThinkingEffort
	}
	if dst.ThinkingType == "" {
		dst.ThinkingType = src.value.ThinkingType
	}
}

func mergeResponse(dst *model.Extracted, src responseState) {
	if src.stopPresent {
		dst.StopReason = src.value.StopReason
	}
	if src.inputPresent {
		dst.InputTokens = src.value.InputTokens
	}
	if src.outputPresent {
		dst.OutputTokens = src.value.OutputTokens
	}
	if src.cacheReadPresent {
		dst.CacheReadTokens = src.value.CacheReadTokens
	}
	if src.cacheWritePresent {
		dst.CacheCreationTokens = src.value.CacheCreationTokens
	}
	if src.signaturePresent {
		dst.SignaturePresent = true
	}
}

func mergeResponseState(dst *responseState, src responseState) {
	if src.stopPresent {
		dst.value.StopReason, dst.stopPresent = src.value.StopReason, true
	}
	if src.inputPresent {
		dst.value.InputTokens, dst.inputPresent = src.value.InputTokens, true
	}
	if src.outputPresent {
		dst.value.OutputTokens, dst.outputPresent = src.value.OutputTokens, true
	}
	if src.cacheReadPresent {
		dst.value.CacheReadTokens, dst.cacheReadPresent = src.value.CacheReadTokens, true
	}
	if src.cacheWritePresent {
		dst.value.CacheCreationTokens, dst.cacheWritePresent = src.value.CacheCreationTokens, true
	}
	if src.signaturePresent {
		dst.value.SignaturePresent, dst.signaturePresent = true, true
	}
}

func firstSanitizedError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return sanitizeError(err)
		}
	}
	return nil
}

func sanitizeError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, ErrUnsupportedFormat):
		return ErrUnsupportedFormat
	case errors.Is(err, ErrMetadataLimit):
		return ErrMetadataLimit
	case errors.Is(err, ErrReadPayload):
		return ErrReadPayload
	case errors.Is(err, ErrStreamClosed):
		return ErrStreamClosed
	default:
		return ErrMalformedPayload
	}
}

// jsonBody owns one fixed-capacity pipe and one decoder goroutine. Closing or
// aborting it always joins done before returning.
type jsonBody struct {
	mu        sync.Mutex
	reader    *io.PipeReader
	writer    *io.PipeWriter
	done      chan parseResult
	stopWatch func() bool
	closed    bool
}

func newJSONBody(ctx context.Context, mode parserMode) *jsonBody {
	reader, writer := io.Pipe()
	body := &jsonBody{
		reader: reader,
		writer: writer,
		done:   make(chan parseResult, 1),
	}
	body.stopWatch = context.AfterFunc(ctx, func() {
		body.mu.Lock()
		if !body.closed {
			body.closed = true
			_ = body.writer.CloseWithError(context.Canceled)
			_ = body.reader.CloseWithError(context.Canceled)
		}
		body.mu.Unlock()
	})
	go func() {
		result := parseJSONDocument(body.reader, mode)
		if result.err != nil {
			_ = body.reader.CloseWithError(sanitizeError(result.err))
		} else {
			_ = body.reader.Close()
		}
		body.done <- result
		close(body.done)
	}()
	return body
}

func (b *jsonBody) feed(payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return ErrStreamClosed
	}
	_, err := b.writer.Write(payload)
	return sanitizeError(err)
}

func (b *jsonBody) finishRequest() (requestState, error) {
	result := b.finishResult()
	return result.request, result.err
}

func (b *jsonBody) finish() (responseState, error) {
	result := b.finishResult()
	return result.response, result.err
}

func (b *jsonBody) finishResult() parseResult {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		_ = b.writer.Close()
	}
	b.mu.Unlock()
	result := <-b.done
	if b.stopWatch != nil {
		b.stopWatch()
	}
	_ = b.reader.Close()
	return result
}

func (b *jsonBody) abort() {
	b.mu.Lock()
	if !b.closed {
		b.closed = true
		_ = b.writer.CloseWithError(context.Canceled)
		_ = b.reader.CloseWithError(context.Canceled)
	}
	b.mu.Unlock()
	<-b.done
	if b.stopWatch != nil {
		b.stopWatch()
	}
}

// The JSON walker skips uninteresting scalar content without retaining it.
// Interesting strings are capped at maxMetadataBytes before decoding.
type jsonWalker struct {
	reader   *bufio.Reader
	mode     parserMode
	request  requestState
	response responseState
	firstErr error
	gemini   [2]geminiUsage
}

type geminiUsage struct {
	candidatesSeen bool
	candidatesOK   bool
	candidates     uint32
	thoughtsSeen   bool
	thoughtsOK     bool
	thoughts       uint32
}

func parseJSONDocument(reader io.Reader, mode parserMode) parseResult {
	walker := &jsonWalker{reader: bufio.NewReaderSize(reader, readerBufferSize), mode: mode}
	first, err := walker.readNonSpace()
	if errors.Is(err, io.EOF) {
		return parseResult{}
	}
	if err != nil {
		return parseResult{err: err}
	}
	if err := walker.reader.UnreadByte(); err != nil {
		return parseResult{err: ErrMalformedPayload}
	}
	if err := walker.scanValue(nil, 0); err != nil {
		return parseResult{request: walker.request, response: walker.response, err: err}
	}
	_ = first
	for {
		b, err := walker.reader.ReadByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return parseResult{request: walker.request, response: walker.response, err: err}
		}
		if !isJSONSpace(b) {
			return parseResult{request: walker.request, response: walker.response, err: ErrMalformedPayload}
		}
	}
	walker.finishGemini()
	walker.finishRequest()
	return parseResult{request: walker.request, response: walker.response, err: walker.firstErr}
}

func parseJSONBytes(payload []byte, mode parserMode) parseResult {
	return parseJSONDocument(bytes.NewReader(payload), mode)
}

func (w *jsonWalker) scanValue(path []string, depth int) error {
	if depth > maxJSONDepth {
		return ErrMetadataLimit
	}
	b, err := w.readNonSpace()
	if err != nil {
		return ErrMalformedPayload
	}
	interesting := w.pathInteresting(path)
	switch b {
	case '{':
		w.handleWrongType(path)
		return w.scanObject(path, depth+1)
	case '[':
		w.handleWrongType(path)
		return w.scanArray(path, depth+1)
	case '"':
		limit := 0
		if interesting {
			limit = maxMetadataBytes
		}
		value, retained, err := w.readJSONString(limit, interesting)
		if err != nil {
			return err
		}
		if retained {
			w.handleString(path, value)
		} else {
			w.handleWrongType(path)
		}
		return nil
	default:
		literal, retained, err := w.readLiteral(b, interesting)
		if err != nil {
			return err
		}
		if retained {
			w.handleLiteral(path, literal)
		} else if interesting {
			w.handleWrongType(path)
		}
		return nil
	}
}

func (w *jsonWalker) scanObject(path []string, depth int) error {
	b, err := w.readNonSpace()
	if err != nil {
		return ErrMalformedPayload
	}
	if b == '}' {
		return nil
	}
	if err := w.reader.UnreadByte(); err != nil {
		return ErrMalformedPayload
	}
	for {
		b, err = w.readNonSpace()
		if err != nil || b != '"' {
			return ErrMalformedPayload
		}
		key, retained, err := w.readJSONString(maxJSONKeyBytes, false)
		if err != nil {
			return err
		}
		if !retained {
			key = ""
		}
		b, err = w.readNonSpace()
		if err != nil || b != ':' {
			return ErrMalformedPayload
		}
		child := appendPath(path, key)
		if err := w.scanValue(child, depth); err != nil {
			return err
		}
		b, err = w.readNonSpace()
		if err != nil {
			return ErrMalformedPayload
		}
		switch b {
		case '}':
			return nil
		case ',':
			continue
		default:
			return ErrMalformedPayload
		}
	}
}

func (w *jsonWalker) scanArray(path []string, depth int) error {
	b, err := w.readNonSpace()
	if err != nil {
		return ErrMalformedPayload
	}
	if b == ']' {
		return nil
	}
	if err := w.reader.UnreadByte(); err != nil {
		return ErrMalformedPayload
	}
	itemPath := appendPath(path, "*")
	for {
		if err := w.scanValue(itemPath, depth); err != nil {
			return err
		}
		b, err = w.readNonSpace()
		if err != nil {
			return ErrMalformedPayload
		}
		switch b {
		case ']':
			return nil
		case ',':
			continue
		default:
			return ErrMalformedPayload
		}
	}
}

func appendPath(path []string, item string) []string {
	next := make([]string, len(path)+1)
	copy(next, path)
	next[len(path)] = item
	return next
}

func (w *jsonWalker) readNonSpace() (byte, error) {
	for {
		b, err := w.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		if !isJSONSpace(b) {
			return b, nil
		}
	}
}

func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func (w *jsonWalker) readJSONString(limit int, reportLimit bool) (string, bool, error) {
	retained := limit > 0
	raw := make([]byte, 0, min(limit+2, 256))
	if retained {
		raw = append(raw, '"')
	}
	escaped := false
	unicodeDigits := 0
	for {
		b, err := w.reader.ReadByte()
		if err != nil {
			return "", false, ErrMalformedPayload
		}
		if retained {
			if len(raw) >= limit+1 {
				retained = false
				raw = nil
				if reportLimit && w.firstErr == nil {
					w.firstErr = ErrMetadataLimit
				}
			} else {
				raw = append(raw, b)
			}
		}
		if unicodeDigits > 0 {
			if !isHex(b) {
				return "", false, ErrMalformedPayload
			}
			unicodeDigits--
			continue
		}
		if escaped {
			escaped = false
			switch b {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			case 'u':
				unicodeDigits = 4
			default:
				return "", false, ErrMalformedPayload
			}
			continue
		}
		if b == '\\' {
			escaped = true
			continue
		}
		if b == '"' {
			break
		}
		if b < 0x20 {
			return "", false, ErrMalformedPayload
		}
	}
	if !retained {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false, ErrMalformedPayload
	}
	return value, true, nil
}

func (w *jsonWalker) readLiteral(first byte, interesting bool) (string, bool, error) {
	const maxLiteralBytes = 128
	retained := true
	buf := []byte{first}
	for {
		b, err := w.reader.ReadByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", false, err
		}
		if b == ',' || b == '}' || b == ']' || isJSONSpace(b) {
			if b == ',' || b == '}' || b == ']' {
				if err := w.reader.UnreadByte(); err != nil {
					return "", false, ErrMalformedPayload
				}
			}
			break
		}
		if retained {
			if len(buf) >= maxLiteralBytes {
				retained = false
				buf = nil
				if interesting && w.firstErr == nil {
					w.firstErr = ErrMetadataLimit
				}
			} else {
				buf = append(buf, b)
			}
		}
	}
	if !retained {
		return "", false, nil
	}
	if !json.Valid(buf) {
		return "", false, ErrMalformedPayload
	}
	return string(buf), true, nil
}

func isHex(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F'
}

func pathEqual(path []string, want ...string) bool {
	if len(path) != len(want) {
		return false
	}
	for i := range want {
		if path[i] != want[i] {
			return false
		}
	}
	return true
}

func pathSuffix(path []string, want ...string) bool {
	if len(path) < len(want) {
		return false
	}
	return pathEqual(path[len(path)-len(want):], want...)
}

func (w *jsonWalker) pathInteresting(path []string) bool {
	if w.mode == requestMode {
		return requestStringRank(path) != 0
	}
	if len(path) > 0 && (path[len(path)-1] == "signature" || path[len(path)-1] == "thoughtSignature") {
		return true
	}
	return responseStringRank(path) != 0 || responseNumberRank(path) != 0 || geminiPath(path) >= 0
}

func (w *jsonWalker) handleString(path []string, value string) {
	if w.mode == requestMode {
		w.handleRequestString(path, value)
		return
	}
	if len(path) > 0 && (path[len(path)-1] == "signature" || path[len(path)-1] == "thoughtSignature") {
		if strings.TrimSpace(value) != "" {
			w.response.value.SignaturePresent = true
			w.response.signaturePresent = true
		}
	}
	if rank := responseStringRank(path); rank != 0 && strings.TrimSpace(value) != "" {
		w.response.setStop(value, rank)
	}
	if responseNumberRank(path) != 0 || geminiPath(path) >= 0 {
		w.handleWrongType(path)
	}
}

func (w *jsonWalker) handleLiteral(path []string, literal string) {
	if w.mode != responseMode {
		return
	}
	value, err := strconv.ParseUint(literal, 10, 32)
	if err != nil {
		w.handleWrongType(path)
		return
	}
	if scope := geminiPath(path); scope >= 0 {
		usage := &w.gemini[scope/2]
		if scope%2 == 0 {
			usage.candidatesSeen, usage.candidatesOK, usage.candidates = true, true, uint32(value)
		} else {
			usage.thoughtsSeen, usage.thoughtsOK, usage.thoughts = true, true, uint32(value)
		}
		return
	}
	rank := responseNumberRank(path)
	if rank == 0 {
		return
	}
	switch responseNumberKind(path) {
	case 1:
		w.response.setInput(uint32(value), rank)
	case 2:
		w.response.setOutput(uint32(value), rank)
	case 3:
		w.response.setCacheRead(uint32(value), rank)
	case 4:
		w.response.setCacheWrite(uint32(value), rank)
	}
}

func (w *jsonWalker) handleWrongType(path []string) {
	if w.mode != responseMode {
		return
	}
	if scope := geminiPath(path); scope >= 0 {
		usage := &w.gemini[scope/2]
		if scope%2 == 0 {
			usage.candidatesSeen, usage.candidatesOK = true, false
		} else {
			usage.thoughtsSeen, usage.thoughtsOK = true, false
		}
	}
}

func (w *jsonWalker) handleRequestString(path []string, value string) {
	rank := requestStringRank(path)
	if rank == 0 || strings.TrimSpace(value) == "" {
		return
	}
	switch {
	case rank >= 1 && rank <= 20:
		session := strings.TrimSpace(value)
		if pathEqual(path, "metadata", "user_id") || pathEqual(path, "request", "metadata", "user_id") {
			session = parseMetadataSession(session)
			if session == "" {
				return
			}
		} else if pathEqual(path, "request", "sessionId") || pathEqual(path, "request", "session_id") || pathEqual(path, "request", "conversation_id") {
			if parsed := parseMetadataSession(session); parsed != "" {
				session = parsed
			}
		}
		if w.request.sessionRank == 0 || rank < w.request.sessionRank {
			w.request.sessionRank = rank
			w.request.value.SessionID = session
		}
	case rank >= 30 && rank <= 39:
		if w.request.effortRank == 0 || rank < w.request.effortRank {
			w.request.effortRank = rank
			w.request.effortRaw = value
		}
	case rank >= 40:
		if w.request.typeRank == 0 || rank < w.request.typeRank {
			w.request.typeRank = rank
			w.request.value.ThinkingType = strings.TrimSpace(value)
		}
	}
}

func (w *jsonWalker) finishRequest() {
	switch value := strings.ToLower(strings.TrimSpace(w.request.effortRaw)); value {
	case "low", "medium", "high", "xhigh", "max":
		w.request.value.ThinkingEffort = value
	}
}

func (w *jsonWalker) finishGemini() {
	for scope, usage := range w.gemini {
		if !usage.candidatesSeen && !usage.thoughtsSeen {
			continue
		}
		if usage.candidatesSeen && !usage.candidatesOK || usage.thoughtsSeen && !usage.thoughtsOK {
			continue
		}
		total := uint64(usage.candidates) + uint64(usage.thoughts)
		if total > uint64(^uint32(0)) {
			continue
		}
		w.response.setOutput(uint32(total), 50+scope*10)
	}
}

func requestStringRank(path []string) int {
	switch {
	case pathEqual(path, "metadata", "user_id"):
		return 1
	case pathEqual(path, "request", "metadata", "user_id"):
		return 2
	case pathEqual(path, "conversationState", "conversationId"):
		return 3
	case pathEqual(path, "request", "sessionId"):
		return 4
	case pathEqual(path, "request", "session_id"):
		return 5
	case pathEqual(path, "request", "conversation_id"):
		return 6
	case pathEqual(path, "prompt_cache_key"):
		return 7
	case pathEqual(path, "conversation_id"):
		return 8
	case pathEqual(path, "session_id"):
		return 9
	case pathEqual(path, "sessionId"):
		return 10
	case pathEqual(path, "metadata", "session_id"):
		return 11
	case pathEqual(path, "thread_id"):
		return 12
	case pathEqual(path, "output_config", "effort"):
		return 30
	case pathEqual(path, "additionalModelRequestFields", "output_config", "effort"):
		return 31
	case pathEqual(path, "request", "output_config", "effort"):
		return 32
	case pathEqual(path, "thinking", "type"):
		return 40
	case pathEqual(path, "additionalModelRequestFields", "thinking", "type"):
		return 41
	case pathEqual(path, "request", "thinking", "type"):
		return 42
	default:
		return 0
	}
}

func responseStringRank(path []string) int {
	switch {
	case pathEqual(path, "stop_reason"):
		return 10
	case pathEqual(path, "delta", "stop_reason"):
		return 20
	case pathEqual(path, "choices", "*", "finish_reason"):
		return 30
	case pathEqual(path, "response", "status"):
		return 40
	case pathEqual(path, "candidates", "*", "finishReason"):
		return 50
	case pathEqual(path, "response", "candidates", "*", "finishReason"):
		return 60
	case pathEqual(path, "messageStopEvent", "stopReason"):
		return 70
	case pathEqual(path, "stopReason"):
		return 70
	default:
		return 0
	}
}

// responseNumberKind returns 1=input, 2=output, 3=cache-read, 4=cache-write.
func responseNumberKind(path []string) int {
	if kind := kiroCounterKind(path); kind != 0 {
		return kind
	}
	switch {
	case pathEqual(path, "usage", "input_tokens"), pathEqual(path, "usage", "prompt_tokens"),
		pathEqual(path, "message", "usage", "input_tokens"),
		pathEqual(path, "response", "usage", "input_tokens"), pathEqual(path, "response", "usage", "prompt_tokens"),
		pathEqual(path, "usageMetadata", "promptTokenCount"), pathEqual(path, "response", "usageMetadata", "promptTokenCount"),
		pathEqual(path, "messageMetadataEvent", "tokenUsage", "uncachedInputTokens"):
		return 1
	case pathEqual(path, "usage", "output_tokens"), pathEqual(path, "usage", "completion_tokens"),
		pathEqual(path, "message", "usage", "output_tokens"),
		pathEqual(path, "response", "usage", "output_tokens"), pathEqual(path, "response", "usage", "completion_tokens"),
		pathEqual(path, "messageMetadataEvent", "tokenUsage", "outputTokens"):
		return 2
	case pathEqual(path, "usage", "cache_read_input_tokens"), pathEqual(path, "usage", "cache_read_tokens"), pathEqual(path, "usage", "cached_tokens"),
		pathEqual(path, "usage", "prompt_tokens_details", "cached_tokens"), pathEqual(path, "usage", "input_tokens_details", "cached_tokens"),
		pathEqual(path, "message", "usage", "cache_read_input_tokens"),
		pathEqual(path, "response", "usage", "cache_read_input_tokens"), pathEqual(path, "response", "usage", "cache_read_tokens"), pathEqual(path, "response", "usage", "cached_tokens"),
		pathEqual(path, "response", "usage", "prompt_tokens_details", "cached_tokens"), pathEqual(path, "response", "usage", "input_tokens_details", "cached_tokens"),
		pathEqual(path, "usageMetadata", "cachedContentTokenCount"), pathEqual(path, "response", "usageMetadata", "cachedContentTokenCount"),
		pathEqual(path, "messageMetadataEvent", "tokenUsage", "cacheReadInputTokens"):
		return 3
	case pathEqual(path, "usage", "cache_creation_input_tokens"), pathEqual(path, "usage", "cache_write_input_tokens"), pathEqual(path, "usage", "cache_write_tokens"), pathEqual(path, "usage", "cache_creation_tokens"),
		pathEqual(path, "usage", "prompt_tokens_details", "cache_creation_tokens"), pathEqual(path, "usage", "prompt_tokens_details", "cache_write_tokens"),
		pathEqual(path, "usage", "input_tokens_details", "cache_creation_tokens"), pathEqual(path, "usage", "input_tokens_details", "cache_write_tokens"),
		pathEqual(path, "message", "usage", "cache_creation_input_tokens"),
		pathEqual(path, "response", "usage", "cache_creation_input_tokens"), pathEqual(path, "response", "usage", "cache_write_tokens"), pathEqual(path, "response", "usage", "cache_creation_tokens"),
		pathEqual(path, "response", "usage", "prompt_tokens_details", "cache_creation_tokens"), pathEqual(path, "response", "usage", "prompt_tokens_details", "cache_write_tokens"),
		pathEqual(path, "response", "usage", "input_tokens_details", "cache_creation_tokens"), pathEqual(path, "response", "usage", "input_tokens_details", "cache_write_tokens"),
		pathEqual(path, "messageMetadataEvent", "tokenUsage", "cacheWriteInputTokens"):
		return 4
	default:
		return 0
	}
}

func responseNumberRank(path []string) int {
	kind := responseNumberKind(path)
	if kind == 0 {
		return 0
	}
	switch {
	case isKiroCounterPath(path):
		return 70
	case len(path) > 0 && path[0] == "response" && len(path) > 1 && path[1] == "usageMetadata":
		return 60
	case len(path) > 0 && path[0] == "usageMetadata":
		return 50
	case len(path) > 0 && path[0] == "response":
		return 40
	case len(path) > 0 && path[0] == "message":
		return 30
	}
	base := 20
	if len(path) > 0 && path[0] == "response" {
		base = 40
	}
	switch kind {
	case 1, 2:
		if path[len(path)-1] == "input_tokens" || path[len(path)-1] == "output_tokens" {
			return base + 2
		}
		return base + 1
	case 3:
		switch {
		case pathSuffix(path, "input_tokens_details", "cached_tokens"):
			return base + 5
		case pathSuffix(path, "prompt_tokens_details", "cached_tokens"):
			return base + 4
		case path[len(path)-1] == "cache_read_input_tokens":
			return base + 3
		case path[len(path)-1] == "cache_read_tokens":
			return base + 2
		default:
			return base + 1
		}
	case 4:
		switch {
		case pathSuffix(path, "input_tokens_details", "cache_write_tokens"):
			return base + 8
		case pathSuffix(path, "prompt_tokens_details", "cache_write_tokens"):
			return base + 7
		case pathSuffix(path, "input_tokens_details", "cache_creation_tokens"):
			return base + 6
		case pathSuffix(path, "prompt_tokens_details", "cache_creation_tokens"):
			return base + 5
		case path[len(path)-1] == "cache_write_tokens":
			return base + 4
		case path[len(path)-1] == "cache_creation_input_tokens":
			return base + 3
		case path[len(path)-1] == "cache_write_input_tokens":
			return base + 2
		default:
			return base + 1
		}
	default:
		return base
	}
}

func isKiroCounterPath(path []string) bool {
	return kiroCounterKind(path) != 0
}

func kiroCounterKind(path []string) int {
	validShape := len(path) == 1 ||
		len(path) == 2 && (path[0] == "tokenUsage" || strings.HasSuffix(path[0], "Event")) ||
		len(path) == 3 && strings.HasSuffix(path[0], "Event") && path[1] == "tokenUsage"
	if !validShape {
		return 0
	}
	switch path[len(path)-1] {
	case "inputTokens", "uncachedInputTokens":
		return 1
	case "outputTokens":
		return 2
	case "cacheReadInputTokens":
		return 3
	case "cacheWriteInputTokens":
		return 4
	default:
		return 0
	}
}

// geminiPath returns 0/1 for root candidates/thoughts and 2/3 for response.
func geminiPath(path []string) int {
	switch {
	case pathEqual(path, "usageMetadata", "candidatesTokenCount"):
		return 0
	case pathEqual(path, "usageMetadata", "thoughtsTokenCount"):
		return 1
	case pathEqual(path, "response", "usageMetadata", "candidatesTokenCount"):
		return 2
	case pathEqual(path, "response", "usageMetadata", "thoughtsTokenCount"):
		return 3
	default:
		return -1
	}
}

func (s *responseState) setStop(value string, rank int) {
	if !s.stopPresent || rank >= s.stopRank {
		s.value.StopReason, s.stopPresent, s.stopRank = strings.TrimSpace(value), true, rank
	}
}

func (s *responseState) setInput(value uint32, rank int) {
	if !s.inputPresent || rank >= s.inputRank {
		s.value.InputTokens, s.inputPresent, s.inputRank = value, true, rank
	}
}

func (s *responseState) setOutput(value uint32, rank int) {
	if !s.outputPresent || rank >= s.outputRank {
		s.value.OutputTokens, s.outputPresent, s.outputRank = value, true, rank
	}
}

func (s *responseState) setCacheRead(value uint32, rank int) {
	if !s.cacheReadPresent || rank >= s.cacheReadRank {
		s.value.CacheReadTokens, s.cacheReadPresent, s.cacheReadRank = value, true, rank
	}
}

func (s *responseState) setCacheWrite(value uint32, rank int) {
	if !s.cacheWritePresent || rank >= s.cacheWriteRank {
		s.value.CacheCreationTokens, s.cacheWritePresent, s.cacheWriteRank = value, true, rank
	}
}

func parseMetadataSession(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "{") {
		var value struct {
			DeviceID  string `json:"device_id"`
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(raw), &value) == nil && value.DeviceID != "" && value.SessionID != "" {
			return value.SessionID
		}
		return ""
	}
	const sessionMarker = "_session_"
	const accountMarker = "_account_"
	if !strings.HasPrefix(raw, "user_") {
		return ""
	}
	accountIndex := strings.Index(raw, accountMarker)
	if accountIndex < 0 {
		return ""
	}
	device := raw[len("user_"):accountIndex]
	remainder := raw[accountIndex+len(accountMarker):]
	sessionIndex := strings.Index(remainder, sessionMarker)
	if len(device) != 64 || sessionIndex < 0 || !isHexString(device) {
		return ""
	}
	account := remainder[:sessionIndex]
	session := remainder[sessionIndex+len(sessionMarker):]
	if len(session) == 36 && isHexOrHyphen(account, true) && isHexOrHyphen(session, false) {
		return session
	}
	return ""
}

func isHexOrHyphen(value string, emptyOK bool) bool {
	if value == "" {
		return emptyOK
	}
	for i := range len(value) {
		b := value[i]
		if !isHex(b) && b != '-' {
			return false
		}
	}
	return true
}

func isHexString(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		if !isHex(value[i]) {
			return false
		}
	}
	return true
}

type sseParser struct {
	line         []byte
	event        []byte
	discardLine  bool
	discardEvent bool
	pendingCR    bool
	state        responseState
	err          error
}

func (p *sseParser) feed(payload []byte) error {
	for _, b := range payload {
		if p.pendingCR {
			p.pendingCR = false
			if b == '\n' {
				continue
			}
		}
		if b == '\r' {
			p.finishLine()
			p.pendingCR = true
			continue
		}
		if b == '\n' {
			p.finishLine()
			continue
		}
		if p.discardLine {
			continue
		}
		if len(p.line) >= maxSSELineBytes {
			p.discardLine = true
			p.recordError(ErrMetadataLimit)
			continue
		}
		p.line = append(p.line, b)
	}
	return p.err
}

func (p *sseParser) finishLine() {
	line := bytes.TrimSuffix(p.line, []byte{'\r'})
	if p.discardLine {
		p.discardEvent = true
	} else if len(line) == 0 {
		p.finishEvent()
	} else if bytes.HasPrefix(line, []byte("data:")) {
		data := line[len("data:"):]
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		needed := len(data)
		if len(p.event) > 0 {
			needed++
		}
		if len(p.event)+needed > maxMetadataBytes {
			p.discardEvent = true
			p.event = nil
			p.recordError(ErrMetadataLimit)
		} else if !p.discardEvent {
			if len(p.event) > 0 {
				p.event = append(p.event, '\n')
			}
			p.event = append(p.event, data...)
		}
	}
	p.line = p.line[:0]
	p.discardLine = false
}

func (p *sseParser) finishEvent() {
	if !p.discardEvent {
		payload := bytes.TrimSpace(p.event)
		if len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
			result := parseJSONBytes(payload, responseMode)
			mergeResponseState(&p.state, result.response)
			p.recordError(result.err)
		}
	}
	p.event = p.event[:0]
	p.discardEvent = false
}

func (p *sseParser) finish() (responseState, error) {
	if len(p.line) > 0 || p.discardLine {
		p.finishLine()
	}
	if len(p.event) > 0 || p.discardEvent {
		p.finishEvent()
	}
	return p.state, p.err
}

func (p *sseParser) abort() {
	p.line = nil
	p.event = nil
	p.pendingCR = false
}

func (p *sseParser) recordError(err error) {
	if err != nil && p.err == nil {
		p.err = sanitizeError(err)
	}
}

type awsParser struct {
	prelude        [12]byte
	preludeN       int
	headers        []byte
	headerN        int
	payload        []byte
	payloadN       uint32
	payloadLen     uint32
	crcBytes       [4]byte
	crcN           int
	checksum       hash.Hash32
	eventType      string
	collectPayload bool
	state          responseState
	err            error
	failed         bool
	sawBytes       bool
}

func (p *awsParser) feed(payload []byte) error {
	if p.failed {
		return p.err
	}
	if len(payload) > 0 {
		p.sawBytes = true
	}
	for len(payload) > 0 && !p.failed {
		switch {
		case p.preludeN < len(p.prelude):
			n := copy(p.prelude[p.preludeN:], payload)
			p.preludeN += n
			payload = payload[n:]
			if p.preludeN == len(p.prelude) {
				p.startFrame()
			}
		case p.headerN < len(p.headers):
			n := min(len(payload), len(p.headers)-p.headerN)
			copy(p.headers[p.headerN:], payload[:n])
			_, _ = p.checksum.Write(payload[:n])
			p.headerN += n
			payload = payload[n:]
			if p.headerN == len(p.headers) {
				var valid bool
				p.eventType, valid = parseAWSHeaders(p.headers)
				if !valid {
					p.fail(ErrMalformedPayload)
					continue
				}
				p.collectPayload = p.payloadLen <= maxMetadataBytes
				if p.collectPayload && p.payloadLen > 0 {
					p.payload = make([]byte, 0, p.payloadLen)
				} else if p.payloadLen > maxMetadataBytes && awsMetadataCritical(p.eventType) {
					p.recordError(ErrMetadataLimit)
				}
			}
		case p.payloadN < p.payloadLen:
			n := min(len(payload), int(p.payloadLen-p.payloadN))
			_, _ = p.checksum.Write(payload[:n])
			if p.collectPayload {
				p.payload = append(p.payload, payload[:n]...)
			}
			p.payloadN += uint32(n)
			payload = payload[n:]
		case p.crcN < len(p.crcBytes):
			n := copy(p.crcBytes[p.crcN:], payload)
			p.crcN += n
			payload = payload[n:]
			if p.crcN == len(p.crcBytes) {
				p.finishFrame()
			}
		}
	}
	return p.err
}

func (p *awsParser) startFrame() {
	total := binary.BigEndian.Uint32(p.prelude[0:4])
	headers := binary.BigEndian.Uint32(p.prelude[4:8])
	preludeCRC := binary.BigEndian.Uint32(p.prelude[8:12])
	if total < 16 || total > maxAWSFrame || headers > maxAWSHeaders || headers > total-16 || crc32.ChecksumIEEE(p.prelude[:8]) != preludeCRC {
		p.fail(ErrMalformedPayload)
		return
	}
	p.headers = make([]byte, headers)
	p.payloadLen = total - 16 - headers
	p.checksum = crc32.NewIEEE()
	_, _ = p.checksum.Write(p.prelude[:])
	if headers == 0 {
		p.eventType = ""
		p.collectPayload = p.payloadLen <= maxMetadataBytes
		if p.collectPayload && p.payloadLen > 0 {
			p.payload = make([]byte, 0, p.payloadLen)
		}
	}
}

func (p *awsParser) finishFrame() {
	if p.checksum == nil || binary.BigEndian.Uint32(p.crcBytes[:]) != p.checksum.Sum32() {
		p.fail(ErrMalformedPayload)
		return
	}
	if p.collectPayload && len(bytes.TrimSpace(p.payload)) > 0 {
		trimmed := bytes.TrimSpace(p.payload)
		if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
			result := parseJSONBytes(trimmed, responseMode)
			mergeResponseState(&p.state, result.response)
			p.recordError(result.err)
		} else if awsMetadataCritical(p.eventType) {
			p.recordError(ErrMalformedPayload)
		}
	}
	p.resetFrame()
}

func (p *awsParser) resetFrame() {
	p.preludeN = 0
	p.headers = nil
	p.headerN = 0
	p.payload = nil
	p.payloadN = 0
	p.payloadLen = 0
	p.crcN = 0
	p.checksum = nil
	p.eventType = ""
	p.collectPayload = false
}

func (p *awsParser) finish() (responseState, error) {
	if !p.failed && p.sawBytes && p.preludeN != 0 {
		p.recordError(ErrMalformedPayload)
	}
	return p.state, p.err
}

func (p *awsParser) abort() {
	p.headers = nil
	p.payload = nil
}

func (p *awsParser) fail(err error) {
	p.recordError(err)
	p.failed = true
}

func (p *awsParser) recordError(err error) {
	if err != nil && p.err == nil {
		p.err = sanitizeError(err)
	}
}

func awsMetadataCritical(eventType string) bool {
	lower := strings.ToLower(eventType)
	return strings.Contains(lower, "metadata") || strings.Contains(lower, "usage") || strings.Contains(lower, "stop")
}

func parseAWSHeaders(headers []byte) (string, bool) {
	eventType := ""
	for offset := 0; offset < len(headers); {
		nameLength := int(headers[offset])
		offset++
		if nameLength == 0 || offset+nameLength+1 > len(headers) {
			return "", false
		}
		name := string(headers[offset : offset+nameLength])
		offset += nameLength
		typeCode := headers[offset]
		offset++
		valueLength := 0
		valueOffset := offset
		switch typeCode {
		case 0, 1:
		case 2:
			valueLength = 1
		case 3:
			valueLength = 2
		case 4:
			valueLength = 4
		case 5, 8:
			valueLength = 8
		case 6, 7:
			if offset+2 > len(headers) {
				return "", false
			}
			valueLength = int(binary.BigEndian.Uint16(headers[offset : offset+2]))
			offset += 2
			valueOffset = offset
		case 9:
			valueLength = 16
		default:
			return "", false
		}
		if offset+valueLength > len(headers) {
			return "", false
		}
		if name == ":event-type" && typeCode == 7 {
			eventType = string(headers[valueOffset : valueOffset+valueLength])
		}
		offset += valueLength
	}
	return eventType, true
}
