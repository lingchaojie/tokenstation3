package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const (
	maxGrokPendingSSEFields     = 1024
	maxGrokPendingSSEFieldBytes = 8 << 20
)

const grokResponsesClientToolMappingContextKey = "grok_responses_client_tool_mapping"

func adaptGrokResponsesClientTools(body []byte) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var requestBody map[string]any
	if err := decoder.Decode(&requestBody); err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, fmt.Errorf("decode Grok Responses client tools: %w", err)
	}

	mapping, changed, err := apicompat.AdaptResponsesClientTools(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, err
	}
	if !changed {
		return body, mapping, nil
	}
	rebuilt, err := marshalOpenAIUpstreamJSON(requestBody)
	if err != nil {
		return body, apicompat.ResponsesClientToolMapping{}, fmt.Errorf("encode Grok Responses client tools: %w", err)
	}
	return rebuilt, mapping, nil
}

func hasGrokResponsesClientToolMapping(mapping apicompat.ResponsesClientToolMapping) bool {
	return len(mapping.CustomTools) > 0 || mapping.ToolSearch || len(mapping.NamespaceTools) > 0
}

func setGrokResponsesClientToolMapping(c *gin.Context, mapping apicompat.ResponsesClientToolMapping) {
	if c == nil {
		return
	}
	if !hasGrokResponsesClientToolMapping(mapping) {
		clearGrokResponsesClientToolMapping(c)
		return
	}
	c.Set(grokResponsesClientToolMappingContextKey, mapping)
}

func clearGrokResponsesClientToolMapping(c *gin.Context) {
	if c == nil {
		return
	}
	if _, exists := c.Get(grokResponsesClientToolMappingContextKey); !exists {
		return
	}
	c.Set(grokResponsesClientToolMappingContextKey, apicompat.ResponsesClientToolMapping{})
}

func grokResponsesClientToolMapping(c *gin.Context) (apicompat.ResponsesClientToolMapping, bool) {
	if c == nil {
		return apicompat.ResponsesClientToolMapping{}, false
	}
	value, ok := c.Get(grokResponsesClientToolMappingContextKey)
	if !ok {
		return apicompat.ResponsesClientToolMapping{}, false
	}
	mapping, ok := value.(apicompat.ResponsesClientToolMapping)
	return mapping, ok && hasGrokResponsesClientToolMapping(mapping)
}

func restoreGrokResponsesClientToolPayload(c *gin.Context, payload []byte) ([]byte, error) {
	mapping, ok := grokResponsesClientToolMapping(c)
	if !ok || !bytes.Contains(payload, []byte(`"function_call"`)) || !json.Valid(payload) {
		return payload, nil
	}
	restored, _, err := apicompat.RestoreResponsesClientToolPayload(payload, mapping)
	return restored, err
}

type grokResponsesClientToolStreamBody struct {
	*io.PipeReader
	source    io.Closer
	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func (b *grokResponsesClientToolStreamBody) providerReadActivity() *providerBodyReadActivity {
	if b == nil {
		return nil
	}
	if carrier, ok := b.source.(providerBodyReadActivityCarrier); ok {
		return carrier.providerReadActivity()
	}
	return nil
}

func (b *grokResponsesClientToolStreamBody) Close() error {
	readerErr := b.closeCaptureUnderlying()
	b.joinCaptureReaders()
	b.finishCapture()
	if readerErr != nil {
		return readerErr
	}
	return b.closeErr
}

func (b *grokResponsesClientToolStreamBody) closeSource() error {
	b.closeOnce.Do(func() { b.closeErr = b.source.Close() })
	return b.closeErr
}

func (b *grokResponsesClientToolStreamBody) closeCaptureUnderlying() error {
	readerErr := b.PipeReader.Close()
	var sourceErr error
	if source, ok := b.source.(captureResponseLifecycle); ok {
		sourceErr = source.closeCaptureUnderlying()
	} else {
		sourceErr = b.closeSource()
	}
	if readerErr != nil {
		return readerErr
	}
	return sourceErr
}

func (b *grokResponsesClientToolStreamBody) joinCaptureReaders() {
	if source, ok := b.source.(captureResponseLifecycle); ok {
		source.joinCaptureReaders()
	}
	if b.done != nil {
		<-b.done
	}
}

func (b *grokResponsesClientToolStreamBody) finishCapture() {
	if source, ok := b.source.(captureResponseLifecycle); ok {
		source.finishCapture()
	}
}

func (b *grokResponsesClientToolStreamBody) captureResponseNeedsDrain() bool {
	if source, ok := b.source.(captureResponseDrainLifecycle); ok {
		return source.captureResponseNeedsDrain()
	}
	return false
}

func (b *grokResponsesClientToolStreamBody) captureResponseDrainRemaining() int64 {
	if source, ok := b.source.(captureResponseDrainLifecycle); ok {
		return source.captureResponseDrainRemaining()
	}
	return 0
}

func (b *grokResponsesClientToolStreamBody) markCaptureResponseTruncated() {
	if source, ok := b.source.(captureResponseDrainLifecycle); ok {
		source.markCaptureResponseTruncated()
	}
}

func newGrokResponsesClientToolStreamBody(
	source io.ReadCloser,
	mapping apicompat.ResponsesClientToolMapping,
	maxLineSize int,
) io.ReadCloser {
	reader, writer := io.Pipe()
	body := &grokResponsesClientToolStreamBody{PipeReader: reader, source: source, done: make(chan struct{})}
	go func() {
		defer close(body.done)
		transformGrokResponsesClientToolStream(source, writer, mapping, maxLineSize)
	}()
	return body
}

func transformGrokResponsesClientToolStream(
	source io.ReadCloser,
	destination *io.PipeWriter,
	mapping apicompat.ResponsesClientToolMapping,
	maxLineSize int,
) {
	defer func() { _ = source.Close() }()
	if maxLineSize <= 0 {
		maxLineSize = defaultMaxLineSize
	}

	scanner := bufio.NewScanner(source)
	scanBuf := getSSEScannerBuf64K()
	defer putSSEScannerBuf64K(scanBuf)
	scanner.Buffer(scanBuf[:0], maxLineSize)
	documents := newOpenAISSEJSONDocumentScanner(scanner)
	restorer := apicompat.NewResponsesClientToolStreamRestorer(mapping)
	rawProviderState := openAIResponsesSSEAttemptState{}
	rawProviderTerminal := false
	buffered := bufio.NewWriterSize(destination, 4*1024)
	pendingFields := make([]string, 0, 2)
	pendingFieldBytes := 0
	frameHadEventField := false
	frameEmitted := false
	fail := func(err error) {
		_ = buffered.Flush()
		drainCaptureResponseRemainderBounded(context.Background(), source, captureOverflowDrainTimeout)
		_ = destination.CloseWithError(err)
	}

	writeLine := func(line string) error {
		if _, err := buffered.WriteString(line); err != nil {
			return err
		}
		return buffered.WriteByte('\n')
	}
	writePendingFields := func(payload []byte, includeNonEvent bool) error {
		eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
		for _, field := range pendingFields {
			if _, isEvent := extractOpenAISSEEventLine(field); isEvent {
				if eventType != "" {
					if err := writeLine("event: " + eventType); err != nil {
						return err
					}
				} else if err := writeLine(field); err != nil {
					return err
				}
				continue
			}
			if includeNonEvent {
				if err := writeLine(field); err != nil {
					return err
				}
			}
		}
		return nil
	}
	writePayloads := func(payloads [][]byte) error {
		for index, payload := range payloads {
			if index == 0 {
				if err := writePendingFields(payload, true); err != nil {
					return err
				}
			} else if frameHadEventField {
				eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
				if eventType != "" {
					if err := writeLine("event: " + eventType); err != nil {
						return err
					}
				}
			}
			if err := writeLine("data: " + string(payload)); err != nil {
				return err
			}
			if err := writeLine(""); err != nil {
				return err
			}
		}
		return buffered.Flush()
	}
	declaredEventType := func() string {
		for index := len(pendingFields) - 1; index >= 0; index-- {
			if eventType, ok := extractOpenAISSEEventLine(pendingFields[index]); ok {
				return eventType
			}
		}
		return ""
	}

	for documents.Scan() {
		line := documents.Text()
		data, isData := extractOpenAISSEDataLine(line)
		if isData {
			payload := []byte(data)
			payloads := [][]byte{payload}
			if json.Valid(payload) {
				if rawProviderTerminal {
					fail(errors.New("Grok Responses data arrived after a terminal event")) //nolint:staticcheck // Protocol name is intentionally capitalized.
					return
				}
				validatedType, validateErr := validateOpenAIResponsesSSEPayload(payload, declaredEventType())
				if validateErr == nil {
					validateErr = rawProviderState.observe(payload, validatedType)
				}
				if validateErr != nil {
					fail(fmt.Errorf("validate raw Grok Responses event: %w", validateErr))
					return
				}
				rawProviderTerminal = isOpenAICompatResponsesTerminalEvent(validatedType)
				var err error
				payloads, _, err = restorer.RestoreEvent(payload)
				if err != nil {
					fail(fmt.Errorf("restore Grok Responses client tool event: %w", err))
					return
				}
			}
			if err := writePayloads(payloads); err != nil {
				_ = destination.CloseWithError(err)
				return
			}
			pendingFields = pendingFields[:0]
			pendingFieldBytes = 0
			frameHadEventField = false
			frameEmitted = true
			continue
		}

		if line == "" {
			if !frameEmitted {
				for _, field := range pendingFields {
					if err := writeLine(field); err != nil {
						_ = destination.CloseWithError(err)
						return
					}
				}
				if len(pendingFields) > 0 {
					if err := writeLine(""); err != nil {
						_ = destination.CloseWithError(err)
						return
					}
					if err := buffered.Flush(); err != nil {
						_ = destination.CloseWithError(err)
						return
					}
				}
			}
			pendingFields = pendingFields[:0]
			pendingFieldBytes = 0
			frameHadEventField = false
			frameEmitted = false
			continue
		}

		if _, isEvent := extractOpenAISSEEventLine(line); isEvent {
			frameHadEventField = true
		}
		lineBytes := len(line) + 1
		if len(pendingFields) >= maxGrokPendingSSEFields || lineBytes > maxGrokPendingSSEFieldBytes-pendingFieldBytes {
			fail(fmt.Errorf("Grok Responses pending SSE fields exceeded limit: fields=%d bytes=%d", len(pendingFields)+1, pendingFieldBytes+lineBytes)) //nolint:staticcheck // Protocol name is intentionally capitalized.
			return
		}
		pendingFields = append(pendingFields, line)
		pendingFieldBytes += lineBytes
	}

	for _, field := range pendingFields {
		if err := writeLine(field); err != nil {
			_ = destination.CloseWithError(err)
			return
		}
	}
	if err := buffered.Flush(); err != nil {
		_ = destination.CloseWithError(err)
		return
	}
	if err := documents.Err(); err != nil {
		_ = destination.CloseWithError(err)
		return
	}
	_ = destination.Close()
}
