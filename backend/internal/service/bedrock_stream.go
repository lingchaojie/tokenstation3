package service

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
)

// handleBedrockStreamingResponse 处理 Bedrock InvokeModelWithResponseStream 的 EventStream 响应
// Bedrock 返回 AWS EventStream 二进制格式，每个事件的 payload 中 chunk.bytes 是 base64 编码的
// Claude SSE 事件 JSON。本方法解码后转换为标准 SSE 格式写入客户端。
func (s *GatewayService) handleBedrockStreamingResponse(
	ctx context.Context,
	resp *http.Response,
	c *gin.Context,
	account *Account,
	startTime time.Time,
	model string,
) (*streamingResult, error) {
	w := c.Writer
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, errors.New("streaming not supported")
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if v := resp.Header.Get("x-amzn-requestid"); v != "" {
		c.Header("x-request-id", v)
	}

	usage := &ClaudeUsage{}
	var firstTokenMs *int
	clientDisconnected := false
	semanticOutput := false
	terminalObserved := false
	providerPayloadObserved := false
	providerPhase := anthropicProviderAwaitingStart
	var staged stagedConvertedStream
	defer func() { _ = staged.close() }()

	// Bedrock EventStream 使用 application/vnd.amazon.eventstream 二进制格式。
	// 每个帧结构：total_length(4) + headers_length(4) + prelude_crc(4) + headers + payload + message_crc(4)
	// 但更实用的方式是使用行扫描找 JSON chunks，因为 Bedrock 的响应在二进制帧中。
	// 我们使用 EventStream decoder 来正确解析。
	readActivity := newProviderBodyReadActivity(resp.Body)
	decoder := newBedrockEventStreamDecoder(readActivity)
	streamInterval := time.Duration(0)
	if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
		streamInterval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
	}

	type decodeEvent struct {
		payload []byte
		err     error
	}
	events := make(chan decodeEvent, openAIDefaultStreamQueueSize)
	done := make(chan struct{})
	decoderDone := make(chan struct{})
	var stopOnce sync.Once
	stopDecoder := func() { stopOnce.Do(func() { close(done) }) }
	sendEvent := func(ev decodeEvent) bool {
		select {
		case events <- ev:
			return true
		case <-done:
			return false
		}
	}
	go func() {
		defer close(decoderDone)
		defer close(events)
		for {
			payload, err := decoder.Decode()
			if err != nil {
				if err == io.EOF {
					return
				}
				_ = sendEvent(decodeEvent{err: err})
				return
			}
			if !sendEvent(decodeEvent{payload: payload}) {
				return
			}
		}
	}()
	providerScanFinished := false
	defer func() {
		if !providerScanFinished {
			drainCaptureScannerOnParserFailure(
				ctx, resp, events, decoderDone, &readActivity.lastRead,
				streamInterval, nil, stopDecoder,
			)
			return
		}
		stopDecoder()
		closeCaptureResponseAndJoinScanner(resp, decoderDone)
	}()

	var intervalTicker *time.Ticker
	if streamInterval > 0 {
		intervalTicker = time.NewTicker(streamInterval)
		defer intervalTicker.Stop()
	}
	var intervalCh <-chan time.Time
	if intervalTicker != nil {
		intervalCh = intervalTicker.C
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				providerScanFinished = true
				if !terminalObserved {
					if !staged.committed && !clientDisconnected {
						return nil, newIncompleteProviderStreamFailover(resp, "bedrock stream ended before semantic output")
					}
					if !providerPayloadObserved {
						if staged.committed || semanticOutput {
							return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream usage incomplete: missing valid message_start")
						}
						return nil, newIncompleteProviderStreamFailover(resp, "bedrock stream ended without a valid message_start")
					}
					return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: clientDisconnected, semanticOutput: true}, fmt.Errorf("stream usage incomplete: missing terminal event")
				}
				if !providerPayloadObserved {
					if staged.committed || semanticOutput {
						return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream usage incomplete: missing valid message_start")
					}
					return nil, newIncompleteProviderStreamFailover(resp, "bedrock stream ended without a valid message_start")
				}
				if !clientDisconnected {
					flusher.Flush()
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: clientDisconnected, semanticOutput: semanticOutput}, nil
			}
			if ev.err != nil {
				if !staged.committed && !clientDisconnected {
					return nil, newIncompleteProviderStreamFailover(resp, "bedrock stream read failed before semantic output")
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: clientDisconnected, semanticOutput: semanticOutput}, fmt.Errorf("bedrock stream read error: %w", ev.err)
			}

			// payload 是 JSON，提取 chunk.bytes（base64 编码的 Claude SSE 事件数据）。
			// Decoder 已确认这是 provider chunk；缺失/损坏的 bytes 不是 keepalive，
			// 否则后续合法终态会把这个畸形 attempt “洗白”。
			sseData, chunkErr := extractBedrockChunkData(ev.payload)
			if chunkErr != nil {
				if !staged.committed && !clientDisconnected {
					return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(chunkErr))
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, chunkErr
			}

			if firstTokenMs == nil {
				ms := int(time.Since(startTime).Milliseconds())
				firstTokenMs = &ms
			}

			// 转换 Bedrock 特有的 amazon-bedrock-invocationMetrics 为标准 Anthropic usage 格式
			// 同时移除该字段避免透传给客户端。必须在删除前验证已知字段，
			// 否则界外值会被强制转成0并把畸形 attempt “洗白”。
			var metricsErr error
			sseData, metricsErr = transformBedrockInvocationMetrics(sseData)
			if metricsErr != nil {
				if !staged.committed && !clientDisconnected {
					return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(metricsErr))
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, metricsErr
			}

			// 确定 SSE event type
			if !gjson.ValidBytes(sseData) {
				if !staged.committed && !clientDisconnected {
					return nil, newIncompleteProviderStreamFailover(resp, "bedrock returned an invalid Anthropic event")
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, errors.New("bedrock returned an invalid Anthropic event")
			}
			eventType := gjson.GetBytes(sseData, "type").String()
			if err := validateAnthropicProviderEvent(&providerPhase, eventType, sseData, eventType); err != nil {
				if !staged.committed && !clientDisconnected {
					return nil, newIncompleteProviderStreamFailover(resp, sanitizeStreamError(err))
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, err
			}
			// Provider shape/phase 验证完成后才合并 usage。否则已提交输出后的
			// 畸形尾帧会在被拒绝前先污染 partial result 并进入计费。
			s.parseSSEUsagePassthrough(string(sseData), usage)
			if eventType == "message_start" {
				providerPayloadObserved = true
			}
			if eventType == "message_stop" {
				terminalObserved = true
			}
			if anthropicSSEEventHasSemanticOutput(string(sseData)) {
				semanticOutput = true
			}

			// 写入标准 SSE 格式
			if !clientDisconnected {
				wire := fmt.Sprintf("data: %s\n\n", sseData)
				if eventType != "" {
					wire = fmt.Sprintf("event: %s\ndata: %s\n\n", eventType, sseData)
				}
				if writeErr := staged.write(c, func() { c.Status(http.StatusOK) }, wire, semanticOutput || (terminalObserved && providerPayloadObserved)); writeErr != nil {
					var clientWriteErr *stagedConvertedClientWriteError
					if !errors.As(writeErr, &clientWriteErr) {
						if !staged.committed {
							return nil, newIncompleteProviderStreamFailover(resp, "bedrock pre-output stage exceeded limit")
						}
						return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, writeErr
					}
					clientDisconnected = true
					logger.LegacyPrintf("service.gateway", "[Bedrock] Client disconnected during streaming, continue draining for usage: account=%d", account.ID)
				} else if staged.committed {
					flusher.Flush()
				}
			}

		case <-intervalCh:
			lastRead := readActivity.LastReadTime()
			if time.Since(lastRead) < streamInterval {
				continue
			}
			if terminalObserved {
				if !providerPayloadObserved {
					if staged.committed || semanticOutput {
						return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream usage incomplete: missing valid message_start")
					}
					return nil, newIncompleteProviderStreamFailover(resp, "bedrock stream ended without a valid message_start")
				}
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: clientDisconnected, semanticOutput: semanticOutput}, fmt.Errorf("bedrock stream did not close after terminal event")
			}
			if clientDisconnected {
				return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, clientDisconnect: true, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout after client disconnect")
			}
			if !staged.committed && !clientDisconnected {
				return nil, newIncompleteProviderStreamFailover(resp, "bedrock stream timed out before semantic output")
			}
			logger.LegacyPrintf("service.gateway", "[Bedrock] Stream data interval timeout: account=%d model=%s interval=%s", account.ID, model, streamInterval)
			if s.rateLimitService != nil {
				s.rateLimitService.HandleStreamTimeout(ctx, account, model)
			}
			return &streamingResult{usage: usage, firstTokenMs: firstTokenMs, semanticOutput: semanticOutput}, fmt.Errorf("stream data interval timeout")
		}
	}
}

// extractBedrockChunkData 从 Bedrock EventStream payload 中提取 Claude SSE 事件数据
// Bedrock payload 格式：{"bytes":"<base64-encoded-json>"}
func extractBedrockChunkData(payload []byte) ([]byte, error) {
	if !gjson.ValidBytes(payload) || !gjson.ParseBytes(payload).IsObject() {
		return nil, errors.New("invalid Bedrock chunk envelope")
	}
	bytesField := gjson.GetBytes(payload, "bytes")
	if bytesField.Type != gjson.String || bytesField.String() == "" {
		return nil, errors.New("bedrock chunk envelope has missing or empty bytes")
	}
	decoded, err := base64.StdEncoding.DecodeString(bytesField.String())
	if err != nil {
		return nil, fmt.Errorf("invalid Bedrock chunk base64: %w", err)
	}
	return decoded, nil
}

// transformBedrockInvocationMetrics 将 Bedrock 特有的 amazon-bedrock-invocationMetrics
// 转换为标准 Anthropic usage 格式，并从 SSE 数据中移除该字段。
//
// Bedrock Invoke 返回的 message_delta 事件可能包含：
//
//	{"type":"message_delta","delta":{...},"amazon-bedrock-invocationMetrics":{"inputTokenCount":150,"outputTokenCount":42}}
//
// 转换为：
//
//	{"type":"message_delta","delta":{...},"usage":{"input_tokens":150,"output_tokens":42}}
func transformBedrockInvocationMetrics(data []byte) ([]byte, error) {
	metrics := gjson.GetBytes(data, "amazon-bedrock-invocationMetrics")
	if !metrics.Exists() {
		return data, nil
	}
	if !metrics.IsObject() {
		return nil, errors.New("invalid Bedrock invocation metrics")
	}
	for _, field := range []string{"inputTokenCount", "outputTokenCount"} {
		if value := metrics.Get(field); value.Exists() && !nonNegativeIntegerGJSON(value) {
			return nil, fmt.Errorf("invalid Bedrock invocation metrics %s", field)
		}
	}

	// 移除 Bedrock 特有字段
	var err error
	data, err = sjson.DeleteBytes(data, "amazon-bedrock-invocationMetrics")
	if err != nil {
		return nil, fmt.Errorf("remove Bedrock invocation metrics: %w", err)
	}

	// 如果已有标准 usage 字段，不覆盖
	if gjson.GetBytes(data, "usage").Exists() {
		return data, nil
	}

	// 转换 camelCase → snake_case 写入 usage
	inputTokens := metrics.Get("inputTokenCount")
	outputTokens := metrics.Get("outputTokenCount")
	if inputTokens.Exists() {
		data, err = sjson.SetBytes(data, "usage.input_tokens", inputTokens.Int())
		if err != nil {
			return nil, fmt.Errorf("set Bedrock input token usage: %w", err)
		}
	}
	if outputTokens.Exists() {
		data, err = sjson.SetBytes(data, "usage.output_tokens", outputTokens.Int())
		if err != nil {
			return nil, fmt.Errorf("set Bedrock output token usage: %w", err)
		}
	}

	return data, nil
}

// bedrockEventStreamDecoder 解码 AWS EventStream 二进制帧
// EventStream 帧格式：
//
//	[total_byte_length: 4 bytes]
//	[headers_byte_length: 4 bytes]
//	[prelude_crc: 4 bytes]
//	[headers: variable]
//	[payload: variable]
//	[message_crc: 4 bytes]
type bedrockEventStreamDecoder struct {
	reader *bufio.Reader
}

// bedrockMaxEventStreamFrameBytes is a functional protocol limit, not a
// capture-retention limit. A single valid AWS EventStream message may be
// larger than the 8 MiB capture prefix, so keep forwarding bounded by the
// gateway's established 128 MiB upstream-response ceiling while capture
// independently truncates its retained copy.
const bedrockMaxEventStreamFrameBytes = config.DefaultUpstreamResponseReadMaxBytes

func newBedrockEventStreamDecoder(r io.Reader) *bedrockEventStreamDecoder {
	return &bedrockEventStreamDecoder{
		reader: bufio.NewReaderSize(r, 64*1024),
	}
}

// Decode 读取下一个 EventStream 帧并返回 chunk 类型事件的 payload
func (d *bedrockEventStreamDecoder) Decode() ([]byte, error) {
	for {
		// 读取 prelude: total_length(4) + headers_length(4) + prelude_crc(4) = 12 bytes
		prelude := make([]byte, 12)
		if _, err := io.ReadFull(d.reader, prelude); err != nil {
			return nil, err
		}

		// 验证 prelude CRC（AWS EventStream 使用标准 CRC32 / IEEE）
		preludeCRC := bedrockReadUint32(prelude[8:12])
		if crc32.Checksum(prelude[0:8], crc32IEEETable) != preludeCRC {
			return nil, fmt.Errorf("eventstream prelude CRC mismatch")
		}

		totalLength := bedrockReadUint32(prelude[0:4])
		headersLength := bedrockReadUint32(prelude[4:8])

		if totalLength < 16 || totalLength > uint32(bedrockMaxEventStreamFrameBytes) { // minimum: 12 prelude + 4 message_crc
			return nil, fmt.Errorf("invalid eventstream frame: total_length=%d", totalLength)
		}
		if headersLength > totalLength-16 {
			return nil, fmt.Errorf("invalid eventstream frame: headers_length=%d total_length=%d", headersLength, totalLength)
		}

		// 读取 headers + payload + message_crc
		remaining := int(totalLength) - 12
		if remaining <= 0 {
			continue
		}
		data := make([]byte, remaining)
		if _, err := io.ReadFull(d.reader, data); err != nil {
			return nil, err
		}

		// 验证 message CRC（覆盖 prelude + headers + payload）
		messageCRC := bedrockReadUint32(data[len(data)-4:])
		h := crc32.New(crc32IEEETable)
		_, _ = h.Write(prelude)
		_, _ = h.Write(data[:len(data)-4])
		if h.Sum32() != messageCRC {
			return nil, fmt.Errorf("eventstream message CRC mismatch")
		}

		// 解析 headers
		headers := data[:headersLength]
		payload := data[headersLength : len(data)-4] // 去掉 message_crc

		parsedHeaders, err := parseEventStreamHeaders(headers)
		if err != nil {
			return nil, err
		}
		if err := validateBedrockEventStreamProtocolHeaders(parsedHeaders); err != nil {
			return nil, err
		}
		// 从 headers 中提取 :event-type
		eventType := parsedHeaders[":event-type"]

		// 只处理 chunk 事件
		if eventType == "chunk" {
			// payload 是完整的 JSON，包含 bytes 字段
			return payload, nil
		}

		// 检查异常事件
		exceptionType := parsedHeaders[":exception-type"]
		if exceptionType != "" {
			return nil, fmt.Errorf("bedrock exception: %s: %s", exceptionType, string(payload))
		}

		messageType := parsedHeaders[":message-type"]
		if messageType == "exception" || messageType == "error" {
			return nil, fmt.Errorf("bedrock error: %s", string(payload))
		}

		// 跳过其他事件类型（如 initial-response）
	}
}

// extractEventStreamHeaderValue 从 EventStream headers 二进制数据中提取指定 header 的字符串值
// EventStream header 格式：
//
//	[name_length: 1 byte][name: variable][value_type: 1 byte][value: variable]
//
// value_type = 7 表示 string 类型，前 2 bytes 为长度
func extractEventStreamHeaderValue(headers []byte, targetName string) string {
	parsed, err := parseEventStreamHeaders(headers)
	if err != nil {
		return ""
	}
	return parsed[targetName]
}

func parseEventStreamHeaders(headers []byte) (map[string]string, error) {
	values := make(map[string]string)
	pos := 0
	for pos < len(headers) {
		nameLen := int(headers[pos])
		pos++
		if nameLen == 0 {
			return nil, errors.New("invalid eventstream empty header name")
		}
		if pos+nameLen > len(headers) {
			return nil, errors.New("invalid eventstream header name")
		}
		nameBytes := headers[pos : pos+nameLen]
		if !utf8.Valid(nameBytes) {
			return nil, errors.New("invalid eventstream header name UTF-8")
		}
		name := string(nameBytes)
		pos += nameLen

		if pos >= len(headers) {
			return nil, errors.New("invalid eventstream header type")
		}
		valueType := headers[pos]
		pos++
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("duplicate eventstream header %q", name)
		}
		if strings.HasPrefix(name, ":") && valueType != 7 {
			return nil, fmt.Errorf("invalid eventstream pseudo-header %q type %d: expected string", name, valueType)
		}

		switch valueType {
		case 7: // string
			if pos+2 > len(headers) {
				return nil, errors.New("invalid eventstream string header length")
			}
			valueLen := int(bedrockReadUint16(headers[pos : pos+2]))
			pos += 2
			if pos+valueLen > len(headers) {
				return nil, errors.New("invalid eventstream string header value")
			}
			valueBytes := headers[pos : pos+valueLen]
			if !utf8.Valid(valueBytes) {
				return nil, fmt.Errorf("invalid eventstream string header %q UTF-8", name)
			}
			values[name] = string(valueBytes)
			pos += valueLen
		case 0: // bool true
			values[name] = "true"
		case 1: // bool false
			values[name] = "false"
		case 2: // byte
			if pos+1 > len(headers) {
				return nil, errors.New("invalid eventstream byte header")
			}
			pos++
		case 3: // short
			if pos+2 > len(headers) {
				return nil, errors.New("invalid eventstream short header")
			}
			pos += 2
		case 4: // int
			if pos+4 > len(headers) {
				return nil, errors.New("invalid eventstream int header")
			}
			pos += 4
		case 5: // long
			if pos+8 > len(headers) {
				return nil, errors.New("invalid eventstream long header")
			}
			pos += 8
		case 6: // bytes
			if pos+2 > len(headers) {
				return nil, errors.New("invalid eventstream bytes header length")
			}
			valueLen := int(bedrockReadUint16(headers[pos : pos+2]))
			pos += 2
			if pos+valueLen > len(headers) {
				return nil, errors.New("invalid eventstream bytes header value")
			}
			pos += valueLen
		case 8: // timestamp
			if pos+8 > len(headers) {
				return nil, errors.New("invalid eventstream timestamp header")
			}
			pos += 8
		case 9: // uuid
			if pos+16 > len(headers) {
				return nil, errors.New("invalid eventstream uuid header")
			}
			pos += 16
		default:
			return nil, fmt.Errorf("invalid eventstream header %q type %d", name, valueType)
		}
	}
	return values, nil
}

func validateBedrockEventStreamProtocolHeaders(headers map[string]string) error {
	messageType := headers[":message-type"]
	eventType := headers[":event-type"]
	exceptionType := headers[":exception-type"]

	switch messageType {
	case "event":
		if eventType == "" {
			return errors.New("invalid eventstream protocol headers: event message omitted :event-type")
		}
		if exceptionType != "" {
			return errors.New("invalid eventstream protocol headers: event message included :exception-type")
		}
	case "exception":
		if exceptionType == "" {
			return errors.New("invalid eventstream protocol headers: exception message omitted :exception-type")
		}
		if eventType != "" {
			return errors.New("invalid eventstream protocol headers: exception message included :event-type")
		}
	case "error":
		if eventType != "" || exceptionType != "" {
			return errors.New("invalid eventstream protocol headers: error message included event or exception type")
		}
	case "":
		return errors.New("invalid eventstream protocol headers: frame omitted :message-type")
	default:
		return fmt.Errorf("invalid eventstream protocol headers: unsupported :message-type %q", messageType)
	}
	return nil
}

// crc32IEEETable is the CRC32 / IEEE table used by AWS EventStream.
var crc32IEEETable = crc32.MakeTable(crc32.IEEE)

func bedrockReadUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func bedrockReadUint16(b []byte) uint16 {
	return uint16(b[0])<<8 | uint16(b[1])
}
