package service

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	captureextract "github.com/Wei-Shaw/sub2api/internal/capture/extract"
	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/config"
	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const captureHardMaxBodyBytes = config.GatewayCaptureMaxBodyBytes

const captureRedactedURLValue = "[REDACTED]"

func captureQueryKeyIsSensitive(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	compact := strings.NewReplacer("-", "", "_", "", ".", "").Replace(lower)
	switch compact {
	case "code", "authorizationcode", "oauthcode", "sig", "sas":
		return true
	}
	for _, segment := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '-' || r == '_' || r == '.'
	}) {
		switch segment {
		case "auth", "authorization", "token", "key", "secret", "password", "passwd", "credential", "credentials", "signature", "proxy", "bearer":
			return true
		}
	}
	normalized := compact
	for _, marker := range []string{"authentication", "authorization", "credential", "signature", "password", "passwd", "secret", "bearertoken", "accesstoken", "refreshtoken", "sessiontoken", "securitytoken", "apikey", "apitoken", "authkey", "authtoken"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	switch normalized {
	case "key", "apikey", "accesskey", "accesstoken", "token", "authorization", "auth", "bearer",
		"proxy", "password", "passwd", "secret", "clientsecret", "credential", "credentials", "signature",
		"apitoken", "refreshtoken", "idtoken", "xauthtoken", "xapikey", "clientsignature",
		"credentialsignature", "xcredentialsignature", "xamzcredential", "xamzsignature", "securitytoken", "sessiontoken":
		return true
	default:
		return false
	}
}

// redactCaptureEndpoint keeps provider routing diagnostics (scheme/host/path and
// non-sensitive query keys) without persisting URL userinfo, relay credentials,
// API keys, tokens, or signed-request material into the archive.
func redactCaptureEndpoint(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		redacted := raw
		if idx := strings.IndexAny(redacted, "?#"); idx >= 0 {
			redacted = redacted[:idx]
		}
		if schemeEnd := strings.Index(redacted, "://"); schemeEnd >= 0 {
			authorityStart := schemeEnd + 3
			authorityEnd := len(redacted)
			if slash := strings.IndexByte(redacted[authorityStart:], '/'); slash >= 0 {
				authorityEnd = authorityStart + slash
			}
			if at := strings.LastIndexByte(redacted[authorityStart:authorityEnd], '@'); at >= 0 {
				redacted = redacted[:authorityStart] + redacted[authorityStart+at+1:]
			}
		}
		return redacted
	}
	u.User = nil
	u.Fragment = ""
	u.RawFragment = ""
	query := u.Query()
	for key := range query {
		if captureQueryKeyIsSensitive(key) {
			query.Set(key, captureRedactedURLValue)
		}
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func redactCaptureURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return redactCaptureEndpoint(u.String())
}

// CaptureRecord 是提交给归档通道的一条原始上游调用快照。
// 所有 []byte 字段在提交前已 deep-copy，worker 内安全读取。
type CaptureRecord struct {
	CapturedAt       time.Time
	Platform         string
	RequestID        string
	RequestedModel   string
	UpstreamModel    string
	UpstreamEndpoint string
	Stream           bool
	HTTPStatus       int
	RawRequest       []byte // 最终上游请求体逐字
	RawResponse      []byte // 流式=原始 SSE；非流式=完整 JSON
	RequestHeaders   []byte // 脱敏后 JSON
	ResponseHeaders  []byte // 脱敏后 JSON
	Truncated        bool
	ContentPolicy    *CaptureContentPolicy

	// 以下抽取列由 worker 调用 extractCaptureColumns 填充，提交时可留空。
	SessionID           string
	ThinkingEffort      string
	ThinkingType        string
	StopReason          string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	SignaturePresent    bool
}

// snapshotBytes 返回 src 的独立副本，避免 worker 读到被后续改写的底层数组。
func snapshotBytes(src []byte) []byte {
	if src == nil {
		return nil
	}
	dst := make([]byte, len(src))
	copy(dst, src)
	return dst
}

func snapshotObservedBytes(src []byte) []byte {
	if src == nil {
		return []byte{}
	}
	return snapshotBytes(src)
}

// captureWithLimit 返回最多 limit 字节的独立副本及是否被截断。limit<=0 或 src 为 nil 视为不采集。
func captureWithLimit(src []byte, limit int) ([]byte, bool) {
	limit = normalizeCaptureLimit(limit)
	if limit <= 0 || src == nil {
		return nil, false
	}
	if len(src) <= limit {
		return snapshotBytes(src), false
	}
	dst := make([]byte, limit)
	copy(dst, src[:limit])
	return dst, true
}

func normalizeCaptureLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit > captureHardMaxBodyBytes {
		return captureHardMaxBodyBytes
	}
	return limit
}

// captureResponseIfEnabled 便于测试的薄封装。
func captureResponseIfEnabled(enabled bool, src []byte, limit int) []byte {
	if !enabled {
		return nil
	}
	b, _ := captureWithLimit(src, limit)
	return b
}

// sseTee 在上游 SSE 读取 goroutine 里按行累积原始字节（含 SSE 帧换行），
// 达到 limit 后停止累积并标记 truncated。mutex 保护：读 goroutine 写入、
// 主 goroutine 在返回时读取，二者可能并发，必须加锁。
type sseTee struct {
	mu        sync.Mutex
	buf       []byte
	limit     int
	truncated bool
}

// captureResponseReader mirrors only the bytes returned by the provider read
// already requested by the functional consumer. It never drains, buffers, or
// owns the attempt's terminal operation.
type captureResponseReader struct {
	upstream  io.ReadCloser
	attempt   *CaptureAttempt
	closeOnce sync.Once
	closeErr  error
}

func newCaptureResponseReader(upstream io.ReadCloser, attempt *CaptureAttempt) *captureResponseReader {
	return &captureResponseReader{upstream: upstream, attempt: attempt}
}

func (r *captureResponseReader) Read(p []byte) (int, error) {
	if r == nil || r.upstream == nil {
		return 0, io.EOF
	}
	n, err := r.upstream.Read(p)
	if n > 0 && r.attempt != nil {
		r.attempt.WriteResponse(p[:n])
	}
	return n, err
}

func (r *captureResponseReader) Close() error {
	if r == nil || r.upstream == nil {
		return nil
	}
	r.closeOnce.Do(func() { r.closeErr = r.upstream.Close() })
	return r.closeErr
}

func (r *captureResponseReader) closeCaptureUnderlying() error { return r.Close() }
func (*captureResponseReader) joinCaptureReaders()             {}
func (*captureResponseReader) finishCapture()                  {}

// captureBodyReadCloser captures the exact bytes consumed from an upstream
// response without changing read/close behavior. It lets callers finalize the
// same capture on success and committed-error paths.
type captureBodyReadCloser struct {
	io.ReadCloser
	attempt    captureAttemptToken
	resp       *http.Response
	mu         sync.Mutex
	buf        []byte
	limit      int
	observed   int64
	truncated  bool
	finishOnce sync.Once
	closeOnce  sync.Once
	closeErr   error
}

// captureResponseLifecycle lets layered streaming translators stop the real
// provider reader, join every goroutine that can still call Read, and only then
// publish one immutable capture snapshot.
type captureResponseLifecycle interface {
	closeCaptureUnderlying() error
	joinCaptureReaders()
	finishCapture()
}

// captureResponseDrainLifecycle exposes capture state through layered stream
// translators. It lets an early parser failure keep the existing sole reader
// alive long enough to capture a finite tail, or explicitly mark an idle/context
// aborted snapshot as incomplete instead of publishing a false exact body.
type captureResponseDrainLifecycle interface {
	captureResponseNeedsDrain() bool
	captureResponseDrainRemaining() int64
	markCaptureResponseTruncated()
}

func (r *captureBodyReadCloser) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n <= 0 {
		return n, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observed += int64(n)
	remain := r.limit - len(r.buf)
	if remain > n {
		remain = n
	}
	if remain > 0 {
		r.buf = append(r.buf, p[:remain]...)
	}
	if remain < n {
		r.truncated = true
	}
	return n, err
}

func (r *captureBodyReadCloser) bytes() ([]byte, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return snapshotBytes(r.buf), r.truncated
}

func (r *captureBodyReadCloser) Close() error {
	r.Finish(r.resp)
	return r.closeUnderlying()
}

func (r *captureBodyReadCloser) closeUnderlying() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() { r.closeErr = r.ReadCloser.Close() })
	return r.closeErr
}

func (r *captureBodyReadCloser) closeCaptureUnderlying() error { return r.closeUnderlying() }
func (r *captureBodyReadCloser) joinCaptureReaders()           {}
func (r *captureBodyReadCloser) finishCapture()                { r.Finish(r.resp) }
func (r *captureBodyReadCloser) captureResponseNeedsDrain() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observed <= int64(r.limit)
}
func (r *captureBodyReadCloser) captureResponseDrainRemaining() int64 {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return int64(r.limit) + 1 - r.observed
}
func (r *captureBodyReadCloser) markCaptureResponseTruncated() {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.truncated = true
	r.mu.Unlock()
}

func (r *captureBodyReadCloser) Finish(resp *http.Response) {
	if r == nil {
		return
	}
	r.finishOnce.Do(func() {
		body, truncated := r.bytes()
		setCaptureResultForAttempt(r.attempt, resp, body, truncated)
	})
}

func beginCaptureResponse(c *gin.Context, resp *http.Response, enabled bool, limit int) func() {
	// Main Gateway paths admitted through the typed attempt own capture at the
	// natural response-read boundary. A failed typed admission is still an
	// explicit no-fallback decision; provider-native/KIRO paths remain on the
	// legacy bridge until their dedicated migration.
	if captureStreamingAttemptPath(c) {
		attempt := captureAttemptForRequest(c)
		if attempt == nil || resp == nil || resp.Body == nil {
			return func() {}
		}
		if _, alreadyWrapped := resp.Body.(*captureResponseReader); alreadyWrapped {
			return func() {}
		}
		setCaptureAttemptResponseHTTPStatus(c, attempt, resp.StatusCode)
		attempt.WriteResponseHeaders(captureHeaderBytes(resp.Header, attempt.headerLimit))
		resp.Body = newCaptureResponseReader(resp.Body, attempt)
		return func() {}
	}
	limit = normalizeCaptureLimit(limit)
	if !enabled || limit <= 0 || resp == nil || resp.Body == nil {
		return func() {}
	}
	reader := &captureBodyReadCloser{ReadCloser: resp.Body, attempt: currentCaptureAttempt(c, true), resp: resp, limit: limit}
	resp.Body = reader
	return func() {
		reader.Finish(resp)
	}
}

func finishCaptureResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if body, ok := resp.Body.(captureResponseLifecycle); ok {
		body.finishCapture()
	}
}

// closeCaptureResponseUnderlying interrupts a blocked reader without
// publishing the capture. The scanner owner must join its read goroutine and
// only then call Finish so bytes returned while Close unblocks Read are not
// lost from the immutable snapshot.
func closeCaptureResponseUnderlying(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	if body, ok := resp.Body.(captureResponseLifecycle); ok {
		_ = body.closeCaptureUnderlying()
	} else {
		_ = resp.Body.Close()
	}
}

// closeCaptureResponseAndJoinScanner interrupts a blocked scanner without
// freezing its capture first. The scanner may still return bytes while the
// underlying Close is taking effect, so final publication must happen only
// after the goroutine has exited. captureBodyReadCloser.Close cannot provide
// that ordering because its public Close intentionally publishes immediately.
func closeCaptureResponseAndJoinScanner(resp *http.Response, scanDone <-chan struct{}) {
	if resp == nil || resp.Body == nil {
		if scanDone != nil {
			<-scanDone
		}
		return
	}
	if body, ok := resp.Body.(captureResponseLifecycle); ok {
		closeCaptureResponseUnderlying(resp)
		if scanDone != nil {
			<-scanDone
		}
		body.joinCaptureReaders()
		body.finishCapture()
		return
	}
	closeCaptureResponseUnderlying(resp)
	if scanDone != nil {
		<-scanDone
	}
}

// readCaptureAwareUpstreamErrorBody always returns at most the caller's normal
// functional error-body bound. When a policy-approved capture wrapper is
// present, it may consume farther (up to the capture ceiling plus one byte) so
// the archive can retain more forensic bytes without changing classification,
// passthrough, logging, or client behavior merely because capture is enabled.
func readCaptureAwareUpstreamErrorBody(resp *http.Response, fallbackLimit int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	if fallbackLimit <= 0 {
		fallbackLimit = gatewayUpstreamErrorBodyReadLimit
	}
	readLimit := fallbackLimit
	var captureLimit int64
	switch captureReader := resp.Body.(type) {
	case *captureBodyReadCloser:
		captureLimit = int64(captureReader.limit)
	case *openAIHTTPCaptureReadCloser:
		captureLimit = int64(captureReader.limit)
	}
	ctx := context.Background()
	if resp.Request != nil {
		ctx = resp.Request.Context()
	}
	body, err := readAllWithProviderIdle(ctx, resp.Body, captureOverflowDrainTimeout, func(reader io.Reader) ([]byte, error) {
		return io.ReadAll(io.LimitReader(reader, readLimit))
	})
	if captureLimit >= fallbackLimit && int64(len(body)) == fallbackLimit {
		drainCaptureResponseRemainderBounded(ctx, resp.Body, captureOverflowDrainTimeout)
	}
	return body, err
}

func newSSETee(limit int) *sseTee { return &sseTee{limit: normalizeCaptureLimit(limit)} }

func (t *sseTee) appendLine(line string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.truncated {
		return
	}
	chunk := line + "\n" // 还原 scanner 去掉的换行；事件间空行 -> "\n\n"
	if len(t.buf)+len(chunk) > t.limit {
		if remain := t.limit - len(t.buf); remain > 0 {
			t.buf = append(t.buf, chunk[:remain]...)
		}
		t.truncated = true
		return
	}
	t.buf = append(t.buf, chunk...)
}

func (t *sseTee) bytes() ([]byte, bool) {
	if t == nil {
		return nil, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.buf == nil {
		return nil, t.truncated
	}
	out := make([]byte, len(t.buf))
	copy(out, t.buf)
	return out, t.truncated
}

// captureResultContextKey 是 gin.Context 上暂存响应体采集结果的 key。
// handleNonStreamingResponse / handleStreamingResponse 都不直接返回 *ForwardResult，
// 真正的 *ForwardResult 由上层 Forward 组装；这里借用请求级 gin.Context 把采集字节
// 从响应处理阶段传递到 ForwardResult 组装阶段，避免改动既有函数签名影响调用方/测试。
// 流式与非流式互斥，同一请求只有一条路径会写入。
const captureResultContextKey = "gateway_capture_result"

// captureResultBridge 是暂存在 gin.Context 上的采集结果。
// 只保存“上游相关”数据：上游请求头/响应头（脱敏后）与响应体，不含任何客户端侧字段。
type captureResultBridge struct {
	UpstreamRequest       []byte
	UpstreamRequestHash   string
	Response              []byte
	ResponseObserved      bool
	Truncated             bool
	RequestTruncated      bool
	ResponseTruncated     bool
	RequestHeaders        []byte // 上游请求头(脱敏)JSON —— 真正发给厂商的头
	ResponseHeaders       []byte // 上游响应头(脱敏)JSON —— 厂商返回的头
	UpstreamEndpoint      string
	HTTPStatus            int
	Platform              string
	OutboundRequest       *http.Request
	RequestCaptureLimit   int
	RequestCaptureInvalid bool
	UpstreamModel         string
	UpstreamStream        bool
	UpstreamStreamKnown   bool
}

// captureResultSlot is shared by all forwarding attempts that reuse one Gin
// context. Forwarding attempts are sequential, while response finalization may
// race with result assembly; the slot makes publish/take/reset atomic so a take
// can never clear a bridge published after it.
type captureResultSlot struct {
	mu         sync.Mutex
	generation uint64
	bridge     *captureResultBridge
}

type captureAttemptToken struct {
	slot       *captureResultSlot
	generation uint64
}

func existingCaptureSlot(c *gin.Context) *captureResultSlot {
	if c == nil {
		return nil
	}
	if v, ok := c.Get(captureResultContextKey); ok {
		if slot, ok := v.(*captureResultSlot); ok && slot != nil {
			return slot
		}
	}
	return nil
}

func captureSlot(c *gin.Context) *captureResultSlot {
	if slot := existingCaptureSlot(c); slot != nil {
		return slot
	}
	if c == nil {
		return nil
	}
	slot := &captureResultSlot{}
	c.Set(captureResultContextKey, slot)
	return slot
}

func currentCaptureAttempt(c *gin.Context, create bool) captureAttemptToken {
	slot := existingCaptureSlot(c)
	if slot == nil && create {
		slot = captureSlot(c)
	}
	if slot == nil {
		return captureAttemptToken{}
	}
	slot.mu.Lock()
	if slot.generation == 0 {
		slot.generation = 1
	}
	if create && slot.bridge == nil {
		slot.bridge = &captureResultBridge{}
	}
	token := captureAttemptToken{slot: slot, generation: slot.generation}
	slot.mu.Unlock()
	return token
}

func startCaptureAttempt(c *gin.Context) captureAttemptToken {
	slot := captureSlot(c)
	if slot == nil {
		return captureAttemptToken{}
	}
	slot.mu.Lock()
	platform := ""
	if slot.bridge != nil {
		platform = slot.bridge.Platform
	}
	slot.generation++
	if slot.generation == 0 {
		slot.generation = 1
	}
	slot.bridge = &captureResultBridge{Platform: platform}
	token := captureAttemptToken{slot: slot, generation: slot.generation}
	slot.mu.Unlock()
	return token
}

func withCaptureAttempt(token captureAttemptToken, fn func(*captureResultBridge)) bool {
	if token.slot == nil || fn == nil {
		return false
	}
	token.slot.mu.Lock()
	defer token.slot.mu.Unlock()
	if token.slot.generation != token.generation {
		return false
	}
	if token.slot.bridge == nil {
		token.slot.bridge = &captureResultBridge{}
	}
	fn(token.slot.bridge)
	return true
}

// beginCaptureAttempt isolates a new account attempt from any result left by a
// prior nil-result/failover path. Handlers invoke service attempts serially for
// a request; response publishing and result taking remain synchronized below.
func beginCaptureAttempt(c *gin.Context) {
	slot := existingCaptureSlot(c)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	slot.generation++
	slot.bridge = nil
	slot.mu.Unlock()
}

// ResetCaptureExchange clears snapshots from an intermediate failover attempt
// without replacing the synchronized slot shared by response finalization.
func ResetCaptureExchange(c *gin.Context) {
	AbortCaptureAttempt(c)
	if existingCaptureSlot(c) == nil {
		return
	}
	beginCaptureAttempt(c)
	if c != nil {
		c.Set(kiroCaptureHeadersContextKey, nil)
	}
}

// SetCaptureOutboundRequest snapshots the actual post-mapping request sent to
// the provider. Callers must guard this with CaptureMayApplyFor.
func SetCaptureOutboundRequest(c *gin.Context, req *http.Request, body []byte, limit int) {
	markCaptureLegacyOwner(c)
	token := startCaptureAttempt(c)
	if token.slot == nil {
		return
	}
	request, requestTruncated := captureWithLimit(body, limit)
	requestHash := HashUsageRequestPayload(body)
	withCaptureAttempt(token, func(bridge *captureResultBridge) {
		bridge.UpstreamRequest = request
		bridge.UpstreamRequestHash = requestHash
		bridge.RequestTruncated = requestTruncated
		bridge.Truncated = requestTruncated
		bridge.OutboundRequest = req
		bridge.RequestCaptureLimit = normalizeCaptureLimit(limit)
		if req != nil {
			bridge.RequestHeaders = redactHTTPHeader(req.Header)
			if req.URL != nil {
				bridge.UpstreamEndpoint = redactCaptureURL(req.URL)
			}
		}
		bridge.UpstreamModel, bridge.UpstreamStream, bridge.UpstreamStreamKnown = extractCaptureProviderRequestMeta(bridge.Platform, body, bridge.UpstreamEndpoint)
	})
}

func setCapturePlatform(c *gin.Context, platform string) {
	markCaptureLegacyOwner(c)
	slot := captureSlot(c)
	if slot == nil {
		return
	}
	slot.mu.Lock()
	if slot.bridge == nil {
		slot.bridge = &captureResultBridge{}
	}
	slot.bridge.Platform = strings.ToLower(strings.TrimSpace(platform))
	slot.mu.Unlock()
}

// setCaptureResult 在响应处理阶段写入采集结果（流式与非流式共用）。
// resp 是上游 http.Response —— 从中取“真正发给厂商的请求头”(resp.Request.Header)
// 与“厂商返回的响应头”(resp.Header)，脱敏后随桥暂存；均为上游相关，不含客户端头。
func setCaptureResult(c *gin.Context, resp *http.Response, body []byte, truncated bool) {
	setCaptureResultForAttempt(currentCaptureAttempt(c, true), resp, body, truncated)
}

func setCaptureResultForAttempt(token captureAttemptToken, resp *http.Response, body []byte, truncated bool) {
	body, hardTruncated := captureWithLimit(body, captureHardMaxBodyBytes)
	truncated = truncated || hardTruncated
	var finalRequest []byte
	var finalRequestHash string
	var finalRequestTruncated bool
	var finalRequestObserved bool
	var finalRequestModel string
	var finalRequestStream bool
	var finalRequestStreamKnown bool
	if token.slot != nil && resp != nil && resp.Request != nil {
		token.slot.mu.Lock()
		sameRequest := token.slot.generation == token.generation && token.slot.bridge != nil && token.slot.bridge.OutboundRequest == resp.Request
		requestLimit := 0
		if token.slot.generation == token.generation && token.slot.bridge != nil {
			requestLimit = token.slot.bridge.RequestCaptureLimit
		}
		token.slot.mu.Unlock()
		if !sameRequest {
			finalRequest, finalRequestTruncated, finalRequestHash, finalRequestObserved,
				finalRequestModel, finalRequestStream, finalRequestStreamKnown = snapshotCompletedHTTPRequestBodyForCapture(resp.Request, requestLimit)
		}
	}
	withCaptureAttempt(token, func(bridge *captureResultBridge) {
		if finalRequestObserved {
			bridge.UpstreamRequest = finalRequest
			bridge.UpstreamRequestHash = finalRequestHash
			bridge.RequestTruncated = finalRequestTruncated
			bridge.RequestCaptureInvalid = false
		} else if resp != nil && resp.Request != nil && bridge.OutboundRequest != nil && bridge.OutboundRequest != resp.Request {
			bridge.RequestCaptureInvalid = true
		}
		// Several adapters perform a bounded functional re-read after the raw
		// capture reader has already observed a longer provider body. Keep the
		// longest snapshot within the same attempt so that the later classification
		// pass cannot downgrade an exact forensic capture to its smaller business
		// parsing prefix. Attempt generations still isolate retries.
		if !bridge.ResponseObserved || len(body) >= len(bridge.Response) {
			bridge.Response = snapshotBytes(body)
		}
		bridge.ResponseObserved = true
		bridge.ResponseTruncated = bridge.ResponseTruncated || truncated
		bridge.Truncated = bridge.RequestTruncated || bridge.ResponseTruncated
		if resp != nil {
			bridge.HTTPStatus = resp.StatusCode
			if resp.Request != nil {
				// Redirects and provider-specific fallback transports may replace the
				// request after the service prepared its initial snapshot. The response
				// always points at the request that produced this response, so it owns
				// final endpoint/header metadata for the captured exchange.
				bridge.RequestHeaders = redactHTTPHeader(resp.Request.Header)
				if resp.Request.URL != nil {
					bridge.UpstreamEndpoint = redactCaptureURL(resp.Request.URL)
				}
			}
			bridge.ResponseHeaders = redactHTTPHeader(resp.Header)
		}
		if finalRequestObserved {
			// A redirect or provider fallback owns a different final request.
			// Recompute attempt metadata from the same body/URL now stored in the
			// bridge so it cannot retain model/stream values from the initial request.
			bridge.UpstreamModel, bridge.UpstreamStream, bridge.UpstreamStreamKnown = extractCaptureProviderRequestMeta(bridge.Platform, finalRequest, bridge.UpstreamEndpoint)
			if finalRequestModel != "" {
				bridge.UpstreamModel = finalRequestModel
			}
			if finalRequestStreamKnown {
				bridge.UpstreamStream, bridge.UpstreamStreamKnown = finalRequestStream, true
			}
		}
	})
}

// snapshotCompletedHTTPRequestBodyForCapture reconstructs a request after the
// transport has followed a redirect or replaced it with a provider fallback.
// GetBody is the only safe source for a non-empty body after transmission; a
// nil body is an observed empty request (for example POST 302 -> GET).
func snapshotCompletedHTTPRequestBodyForCapture(req *http.Request, limit int) ([]byte, bool, string, bool, string, bool, bool) {
	limit = normalizeCaptureLimit(limit)
	if req == nil || limit <= 0 {
		return nil, false, "", false, "", false, false
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, false, "", false, "", false, false
		}
		defer func() { _ = body.Close() }()
		hasher := sha256.New()
		writer := &boundedCaptureWriter{limit: limit}
		tee := io.TeeReader(body, io.MultiWriter(hasher, writer))
		model, stream, streamKnown, _ := extractCaptureProviderRequestMetaFromReader(tee)
		if _, err := io.Copy(io.Discard, tee); err != nil {
			return nil, false, "", false, "", false, false
		}
		return snapshotObservedBytes(writer.buf), writer.truncated, hex.EncodeToString(hasher.Sum(nil)), true, model, stream, streamKnown
	}
	if req.Body == nil || req.Body == http.NoBody {
		return []byte{}, false, HashUsageRequestPayload(nil), true, "", false, false
	}
	return nil, false, "", false, "", false, false
}

func markCaptureResultTruncated(c *gin.Context) {
	withCaptureAttempt(currentCaptureAttempt(c, false), func(bridge *captureResultBridge) {
		bridge.ResponseTruncated = true
		bridge.Truncated = true
	})
}

type boundedCaptureWriter struct {
	buf       []byte
	limit     int
	total     int64
	truncated bool
}

func (w *boundedCaptureWriter) Write(p []byte) (int, error) {
	w.total += int64(len(p))
	if remain := w.limit - len(w.buf); remain > 0 {
		if remain > len(p) {
			remain = len(p)
		}
		w.buf = append(w.buf, p[:remain]...)
	}
	if w.total > int64(w.limit) {
		w.truncated = true
	}
	return len(p), nil
}

type replayPrefixReadCloser struct {
	io.Reader
	closer io.Closer
}

func (r *replayPrefixReadCloser) Close() error { return r.closer.Close() }

func snapshotHTTPRequestBodyForCapture(req *http.Request, limit int) ([]byte, bool, string, string, bool, bool) {
	limit = normalizeCaptureLimit(limit)
	if req == nil || limit <= 0 {
		return nil, false, "", "", false, false
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			defer func() { _ = body.Close() }()
			hasher := sha256.New()
			writer := &boundedCaptureWriter{limit: limit}
			tee := io.TeeReader(body, io.MultiWriter(hasher, writer))
			model, stream, known, _ := extractCaptureProviderRequestMetaFromReader(tee)
			if _, copyErr := io.Copy(io.Discard, tee); copyErr == nil {
				return snapshotBytes(writer.buf), writer.truncated, hex.EncodeToString(hasher.Sum(nil)), model, stream, known
			}
		}
	}
	if req.Body == nil {
		return nil, false, "", "", false, false
	}
	original := req.Body
	prefix, err := io.ReadAll(io.LimitReader(original, int64(limit)+1))
	// A legal Reader may return data and an error in the same call. Restore any
	// observed prefix before checking the error so capture can never consume the
	// beginning of the real provider request.
	req.Body = &replayPrefixReadCloser{Reader: io.MultiReader(bytes.NewReader(prefix), original), closer: original}
	if err != nil {
		return nil, false, "", "", false, false
	}
	captured, truncated := captureWithLimit(prefix, limit)
	requestHash := ""
	if !truncated {
		requestHash = HashUsageRequestPayload(prefix)
	}
	model, stream, known := extractCaptureProviderRequestMeta("", prefix, "")
	return captured, truncated, requestHash, model, stream, known
}

const (
	captureJSONMetaMaxDepth = 128
	captureJSONMetaMaxKey   = 256
	captureJSONMetaMaxModel = 4096
	captureJSONMetaMaxStop  = 4096
)

// captureJSONMetaScanner is a deliberately small, allocation-bounded JSON
// walker. encoding/json.Decoder.Token materializes every scalar string, so an
// unrelated provider field containing a very large string would allocate the
// entire value merely to discover a model field later in the request.
type captureJSONMetaScanner struct {
	r                 *bufio.Reader
	model             string
	stream            bool
	streamKnown       bool
	detectSignature   bool
	signaturePresent  bool
	detectStopReason  bool
	stopReasonSnake   []byte
	stopReasonCamel   []byte
	stopReasonScratch []byte
}

type captureJSONMetaPath uint8

const (
	captureJSONMetaIgnore captureJSONMetaPath = iota
	captureJSONMetaRoot
	captureJSONMetaModel
	captureJSONMetaStream
	captureJSONMetaRequest
	captureJSONMetaConversationState
	captureJSONMetaCurrentMessage
	captureJSONMetaUserInputMessage
	captureJSONMetaSignature
	captureJSONMetaStopReasonSnake
	captureJSONMetaStopReasonCamel
	captureJSONMetaChoices
	captureJSONMetaCandidates
	captureJSONMetaResponse
	captureJSONMetaChoiceItem
	captureJSONMetaCandidateItem
)

func extractCaptureProviderRequestMetaFromReader(r io.Reader) (model string, stream bool, streamKnown bool, err error) {
	scanner := &captureJSONMetaScanner{r: bufio.NewReaderSize(r, 32<<10)}
	if err := scanner.scanValue(captureJSONMetaRoot, 0); err != nil {
		return "", false, false, err
	}
	return scanner.model, scanner.stream, scanner.streamKnown, nil
}

func (s *captureJSONMetaScanner) scanValue(path captureJSONMetaPath, depth int) error {
	if depth > captureJSONMetaMaxDepth {
		return errors.New("capture metadata JSON nesting exceeds limit")
	}
	b, err := s.readNonSpace()
	if err != nil {
		return err
	}
	switch b {
	case '{':
		return s.scanObject(path, depth+1)
	case '[':
		return s.scanArray(path, depth+1)
	case '"':
		if path == captureJSONMetaSignature {
			nonEmpty, err := s.readJSONStringNonEmpty()
			if err != nil {
				return err
			}
			if nonEmpty {
				s.signaturePresent = true
			}
			return nil
		}
		if path == captureJSONMetaStopReasonSnake || path == captureJSONMetaStopReasonCamel {
			return s.readJSONStopReason(path)
		}
		limit := 0
		if path == captureJSONMetaModel && s.model == "" {
			limit = captureJSONMetaMaxModel
		}
		value, retained, err := s.readJSONString(limit)
		if err != nil {
			return err
		}
		if retained && limit > 0 {
			value = strings.TrimSpace(value)
			s.model = value
		}
		return nil
	default:
		literal, err := s.readJSONLiteral(b, path == captureJSONMetaStream)
		if err != nil {
			return err
		}
		if path == captureJSONMetaStream {
			switch literal {
			case "true":
				s.stream, s.streamKnown = true, true
			case "false":
				s.stream, s.streamKnown = false, true
			}
		}
		return nil
	}
}

func (s *captureJSONMetaScanner) scanObject(path captureJSONMetaPath, depth int) error {
	b, err := s.readNonSpace()
	if err != nil {
		return err
	}
	if b == '}' {
		return nil
	}
	if err := s.r.UnreadByte(); err != nil {
		return err
	}
	for {
		if b, err = s.readNonSpace(); err != nil || b != '"' {
			if err != nil {
				return err
			}
			return errors.New("invalid capture metadata JSON object key")
		}
		key := ""
		stopReasonPath := captureJSONMetaIgnore
		if s.detectStopReason {
			stopReasonPath, err = s.readJSONStopReasonKey()
		} else {
			var retained bool
			key, retained, err = s.readJSONString(captureJSONMetaMaxKey)
			if !retained {
				key = ""
			}
		}
		if err != nil {
			return err
		}
		if b, err = s.readNonSpace(); err != nil || b != ':' {
			if err != nil {
				return err
			}
			return errors.New("invalid capture metadata JSON object separator")
		}
		childPath := captureJSONMetaChildPath(path, key)
		if s.detectSignature && (key == "signature" || key == "thoughtSignature") {
			childPath = captureJSONMetaSignature
		}
		if s.detectStopReason {
			switch {
			case path == captureJSONMetaRoot && stopReasonPath == captureJSONMetaChoices:
				childPath = captureJSONMetaChoices
			case path == captureJSONMetaRoot && stopReasonPath == captureJSONMetaCandidates:
				childPath = captureJSONMetaCandidates
			case path == captureJSONMetaRoot && stopReasonPath == captureJSONMetaResponse:
				childPath = captureJSONMetaResponse
			case path == captureJSONMetaResponse && stopReasonPath == captureJSONMetaCandidates:
				childPath = captureJSONMetaCandidates
			case path == captureJSONMetaChoiceItem && stopReasonPath == captureJSONMetaStopReasonSnake:
				childPath = captureJSONMetaStopReasonSnake
			case path == captureJSONMetaCandidateItem && stopReasonPath == captureJSONMetaStopReasonCamel:
				childPath = captureJSONMetaStopReasonCamel
			}
		}
		if err := s.scanValue(childPath, depth); err != nil {
			return err
		}
		b, err = s.readNonSpace()
		if err != nil {
			return err
		}
		switch b {
		case '}':
			return nil
		case ',':
			continue
		default:
			return errors.New("invalid capture metadata JSON object terminator")
		}
	}
}

// readJSONStopReasonKey consumes a JSON object key without materializing it.
// Provider stop-reason keys are fixed ASCII spellings, so byte matching keeps
// dense choices/candidates arrays allocation-bounded.
func (s *captureJSONMetaScanner) readJSONStopReasonKey() (captureJSONMetaPath, error) {
	keys := [...]string{"finish_reason", "finishReason", "choices", "candidates", "response"}
	paths := [...]captureJSONMetaPath{
		captureJSONMetaStopReasonSnake,
		captureJSONMetaStopReasonCamel,
		captureJSONMetaChoices,
		captureJSONMetaCandidates,
		captureJSONMetaResponse,
	}
	matches := [...]bool{true, true, true, true, true}
	index := 0
	escaped := false
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return captureJSONMetaIgnore, err
		}
		if escaped {
			escaped = false
			matches = [len(keys)]bool{}
			continue
		}
		if b == '\\' {
			escaped = true
			matches = [len(keys)]bool{}
			continue
		}
		if b == '"' {
			for i, key := range keys {
				if matches[i] && index == len(key) {
					return paths[i], nil
				}
			}
			return captureJSONMetaIgnore, nil
		}
		if b < 0x20 {
			return captureJSONMetaIgnore, errors.New("invalid control byte in capture metadata JSON object key")
		}
		for i, key := range keys {
			if index >= len(key) || b != key[index] {
				matches[i] = false
			}
		}
		index++
	}
}

// readJSONStopReason retains only the latest non-empty encoded value in two
// reused capped buffers. Dense response arrays therefore do not allocate per
// candidate merely to discover the terminal finish reason.
func (s *captureJSONMetaScanner) readJSONStopReason(path captureJSONMetaPath) error {
	s.stopReasonScratch = s.stopReasonScratch[:0]
	s.stopReasonScratch = append(s.stopReasonScratch, '"')
	retained := true
	nonEmpty := false
	escaped := false
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return err
		}
		if retained {
			if len(s.stopReasonScratch) >= captureJSONMetaMaxStop+1 {
				retained = false
				s.stopReasonScratch = s.stopReasonScratch[:0]
			} else {
				s.stopReasonScratch = append(s.stopReasonScratch, b)
			}
		}
		if escaped {
			escaped = false
			switch b {
			case 'n', 'r', 't', 'f':
				// Decoded whitespace does not make the reason non-empty.
			case 'u':
				var value rune
				for range 4 {
					h, err := s.r.ReadByte()
					if err != nil {
						return err
					}
					if retained {
						if len(s.stopReasonScratch) >= captureJSONMetaMaxStop+1 {
							retained = false
							s.stopReasonScratch = s.stopReasonScratch[:0]
						} else {
							s.stopReasonScratch = append(s.stopReasonScratch, h)
						}
					}
					value <<= 4
					switch {
					case h >= '0' && h <= '9':
						value += rune(h - '0')
					case h >= 'a' && h <= 'f':
						value += rune(h-'a') + 10
					case h >= 'A' && h <= 'F':
						value += rune(h-'A') + 10
					default:
						return errors.New("invalid unicode escape in capture stop reason JSON string")
					}
				}
				if !unicode.IsSpace(value) {
					nonEmpty = true
				}
			case '"', '\\', '/', 'b':
				nonEmpty = true
			default:
				return errors.New("invalid escape in capture stop reason JSON string")
			}
			continue
		}
		switch {
		case b == '\\':
			escaped = true
		case b == '"':
			if retained && nonEmpty {
				if path == captureJSONMetaStopReasonSnake {
					s.stopReasonSnake = append(s.stopReasonSnake[:0], s.stopReasonScratch...)
				} else {
					s.stopReasonCamel = append(s.stopReasonCamel[:0], s.stopReasonScratch...)
				}
			}
			return nil
		case b < 0x20:
			return errors.New("invalid control byte in capture stop reason JSON string")
		case b < utf8.RuneSelf:
			if !unicode.IsSpace(rune(b)) {
				nonEmpty = true
			}
		default:
			nonEmpty = true
		}
	}
}

// readJSONStringNonEmpty consumes a JSON string after its opening quote and
// reports whether its decoded value contains a non-space rune. It never
// materializes the string, so an 8 MiB provider signature cannot create an
// additional body-sized allocation in capture workers.
func (s *captureJSONMetaScanner) readJSONStringNonEmpty() (bool, error) {
	nonEmpty := false
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return false, err
		}
		switch {
		case b == '"':
			return nonEmpty, nil
		case b == '\\':
			escaped, err := s.r.ReadByte()
			if err != nil {
				return false, err
			}
			switch escaped {
			case 'n', 'r', 't', 'f':
				// Decoded whitespace does not prove a non-empty signature.
			case 'u':
				var value rune
				for range 4 {
					h, err := s.r.ReadByte()
					if err != nil {
						return false, err
					}
					value <<= 4
					switch {
					case h >= '0' && h <= '9':
						value += rune(h - '0')
					case h >= 'a' && h <= 'f':
						value += rune(h-'a') + 10
					case h >= 'A' && h <= 'F':
						value += rune(h-'A') + 10
					default:
						return false, errors.New("invalid unicode escape in capture signature JSON string")
					}
				}
				if !unicode.IsSpace(value) {
					nonEmpty = true
				}
			case '"', '\\', '/', 'b':
				nonEmpty = true
			default:
				return false, errors.New("invalid escape in capture signature JSON string")
			}
		case b < 0x20:
			return false, errors.New("invalid control byte in capture signature JSON string")
		case b < utf8.RuneSelf:
			if !unicode.IsSpace(rune(b)) {
				nonEmpty = true
			}
		default:
			if err := s.r.UnreadByte(); err != nil {
				return false, err
			}
			r, _, err := s.r.ReadRune()
			if err != nil {
				return false, err
			}
			if r == utf8.RuneError || !unicode.IsSpace(r) {
				nonEmpty = true
			}
		}
	}
}

func captureResponseHasNonEmptySignature(js []byte) bool {
	if len(js) == 0 {
		return false
	}
	scanner := &captureJSONMetaScanner{
		r:               bufio.NewReaderSize(bytes.NewReader(js), 32<<10),
		detectSignature: true,
	}
	if err := scanner.scanValue(captureJSONMetaRoot, 0); err != nil {
		return false
	}
	return scanner.signaturePresent
}

func captureResponseLastStopReason(js []byte) string {
	snake, camel := captureResponseStopReasons(js)
	if camel != "" {
		return camel
	}
	return snake
}

func captureResponseStopReasons(js []byte) (snake string, camel string) {
	if len(js) == 0 {
		return "", ""
	}
	scanner := &captureJSONMetaScanner{
		r:                 bufio.NewReaderSize(bytes.NewReader(js), 32<<10),
		detectStopReason:  true,
		stopReasonSnake:   make([]byte, 0, captureJSONMetaMaxStop+2),
		stopReasonCamel:   make([]byte, 0, captureJSONMetaMaxStop+2),
		stopReasonScratch: make([]byte, 0, captureJSONMetaMaxStop+2),
	}
	if err := scanner.scanValue(captureJSONMetaRoot, 0); err != nil {
		return "", ""
	}
	decode := func(raw []byte) string {
		if len(raw) == 0 {
			return ""
		}
		value, err := strconv.Unquote(string(raw))
		if err != nil {
			return ""
		}
		return strings.TrimSpace(value)
	}
	return decode(scanner.stopReasonSnake), decode(scanner.stopReasonCamel)
}

func (s *captureJSONMetaScanner) scanArray(path captureJSONMetaPath, depth int) error {
	b, err := s.readNonSpace()
	if err != nil {
		return err
	}
	if b == ']' {
		return nil
	}
	if err := s.r.UnreadByte(); err != nil {
		return err
	}
	childPath := captureJSONMetaIgnore
	switch path {
	case captureJSONMetaChoices:
		childPath = captureJSONMetaChoiceItem
	case captureJSONMetaCandidates:
		childPath = captureJSONMetaCandidateItem
	}
	for {
		if err := s.scanValue(childPath, depth); err != nil {
			return err
		}
		b, err = s.readNonSpace()
		if err != nil {
			return err
		}
		switch b {
		case ']':
			return nil
		case ',':
			continue
		default:
			return errors.New("invalid capture metadata JSON array terminator")
		}
	}
}

func (s *captureJSONMetaScanner) readNonSpace() (byte, error) {
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			return b, nil
		}
	}
}

// readJSONString consumes a JSON string after its opening quote. It retains at
// most maxEncoded bytes; maxEncoded==0 skips the value without allocating.
func (s *captureJSONMetaScanner) readJSONString(maxEncoded int) (string, bool, error) {
	retained := maxEncoded > 0
	var raw []byte
	if retained {
		raw = make([]byte, 0, min(maxEncoded+2, 256))
		raw = append(raw, '"')
	}
	escaped := false
	for {
		b, err := s.r.ReadByte()
		if err != nil {
			return "", false, err
		}
		if retained {
			if len(raw) >= maxEncoded+1 {
				retained = false
				raw = nil
			} else {
				raw = append(raw, b)
			}
		}
		if escaped {
			escaped = false
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
			return "", false, errors.New("invalid control byte in capture metadata JSON string")
		}
	}
	if !retained {
		return "", false, nil
	}
	value, err := strconv.Unquote(string(raw))
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

func (s *captureJSONMetaScanner) readJSONLiteral(first byte, retain bool) (string, error) {
	var buf []byte
	if retain {
		buf = []byte{first}
	}
	for {
		b, err := s.r.ReadByte()
		if err == io.EOF {
			if retain {
				return string(buf), nil
			}
			return "", nil
		}
		if err != nil {
			return "", err
		}
		switch b {
		case ',', '}', ']':
			if err := s.r.UnreadByte(); err != nil {
				return "", err
			}
			if retain {
				return strings.TrimSpace(string(buf)), nil
			}
			return "", nil
		case ' ', '\t', '\r', '\n':
			if retain {
				return strings.TrimSpace(string(buf)), nil
			}
			return "", nil
		default:
			if retain {
				if len(buf) >= 16 {
					retain = false
					buf = nil
				} else {
					buf = append(buf, b)
				}
			}
		}
	}
}

func captureJSONMetaChildPath(parent captureJSONMetaPath, key string) captureJSONMetaPath {
	switch parent {
	case captureJSONMetaRoot:
		switch key {
		case "model", "modelId":
			return captureJSONMetaModel
		case "stream":
			return captureJSONMetaStream
		case "request":
			return captureJSONMetaRequest
		case "conversationState":
			return captureJSONMetaConversationState
		}
	case captureJSONMetaRequest:
		if key == "model" || key == "modelId" {
			return captureJSONMetaModel
		}
	case captureJSONMetaConversationState:
		if key == "currentMessage" {
			return captureJSONMetaCurrentMessage
		}
	case captureJSONMetaCurrentMessage:
		if key == "userInputMessage" {
			return captureJSONMetaUserInputMessage
		}
	case captureJSONMetaUserInputMessage:
		if key == "modelId" {
			return captureJSONMetaModel
		}
	}
	return captureJSONMetaIgnore
}

func snapshotHTTPRequestBody(req *http.Request) []byte {
	if req == nil {
		return nil
	}
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err == nil {
			defer func() { _ = body.Close() }()
			if data, readErr := io.ReadAll(body); readErr == nil {
				return snapshotBytes(data)
			}
		}
	}
	if req.Body == nil {
		return nil
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		return nil
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	return snapshotBytes(data)
}

// setCaptureUpstreamRequest records the body immediately before the real Do
// call. A later retry replaces it, so the bridge always represents the final
// attempted provider request rather than the inbound client payload.
func setCaptureUpstreamRequest(c *gin.Context, req *http.Request, limit int) {
	token := startCaptureAttempt(c)
	if token.slot == nil {
		return
	}
	body, truncated, requestHash, fullModel, fullStream, fullStreamKnown := snapshotHTTPRequestBodyForCapture(req, limit)
	withCaptureAttempt(token, func(bridge *captureResultBridge) {
		bridge.UpstreamRequest = body
		bridge.UpstreamRequestHash = requestHash
		bridge.RequestTruncated = truncated
		bridge.Truncated = truncated
		bridge.OutboundRequest = req
		bridge.RequestCaptureLimit = normalizeCaptureLimit(limit)
		if req != nil {
			bridge.RequestHeaders = redactHTTPHeader(req.Header)
			if req.URL != nil {
				bridge.UpstreamEndpoint = redactCaptureURL(req.URL)
			}
		}
		bridge.UpstreamModel, bridge.UpstreamStream, bridge.UpstreamStreamKnown = extractCaptureProviderRequestMeta(bridge.Platform, body, bridge.UpstreamEndpoint)
		if fullModel != "" {
			bridge.UpstreamModel = fullModel
		}
		if fullStreamKnown {
			bridge.UpstreamStream, bridge.UpstreamStreamKnown = fullStream, true
		}
	})
}

func setCaptureUpstreamResponse(c *gin.Context, resp *http.Response) {
	if resp == nil {
		return
	}
	withCaptureAttempt(currentCaptureAttempt(c, false), func(bridge *captureResultBridge) {
		if len(bridge.RequestHeaders) == 0 && resp.Request != nil {
			bridge.RequestHeaders = redactHTTPHeader(resp.Request.Header)
		}
		bridge.ResponseHeaders = redactHTTPHeader(resp.Header)
		bridge.HTTPStatus = resp.StatusCode
		if bridge.UpstreamEndpoint == "" && resp.Request != nil && resp.Request.URL != nil {
			bridge.UpstreamEndpoint = redactCaptureURL(resp.Request.URL)
		}
	})
}

type captureUpstreamRequestContextKey struct{}

type captureUpstreamRequestContext struct {
	c     *gin.Context
	limit int
}

func withCaptureUpstreamRequestContext(ctx context.Context, c *gin.Context, limit int) context.Context {
	if ctx == nil || c == nil {
		return ctx
	}
	return context.WithValue(ctx, captureUpstreamRequestContextKey{}, captureUpstreamRequestContext{c: c, limit: normalizeCaptureLimit(limit)})
}

func captureUpstreamRequestLimitFromContext(ctx context.Context) (int, bool) {
	if ctx == nil {
		return 0, false
	}
	captureCtx, ok := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	if !ok || captureCtx.c == nil || captureCtx.limit <= 0 {
		return 0, false
	}
	return captureCtx.limit, true
}

func setCaptureUpstreamRequestFromContext(ctx context.Context, req *http.Request) {
	if ctx == nil {
		return
	}
	captureCtx, _ := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	setCaptureUpstreamRequest(captureCtx.c, req, captureCtx.limit)
}

func setCaptureUpstreamResponseFromContext(ctx context.Context, resp *http.Response) {
	if ctx == nil {
		return
	}
	captureCtx, _ := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	setCaptureUpstreamResponse(captureCtx.c, resp)
}

func markCaptureResultTruncatedFromContext(ctx context.Context) {
	if ctx == nil {
		return
	}
	captureCtx, _ := ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	markCaptureResultTruncated(captureCtx.c)
}

// takeCaptureResult 在 ForwardResult 组装阶段读取采集结果（流式与非流式共用）。
func takeCaptureResult(c *gin.Context) (*captureResultBridge, bool) {
	if c == nil {
		return nil, false
	}
	slot := existingCaptureSlot(c)
	if slot == nil {
		return nil, false
	}
	slot.mu.Lock()
	res := slot.bridge
	slot.bridge = nil
	slot.generation++
	slot.mu.Unlock()
	return res, res != nil
}

// attachCaptureToForwardResult transfers the attempt-local capture bridge to
// the result returned to the handler. It is shared by complete and committed
// partial responses so both paths submit the same real upstream bytes once.
func attachCaptureToForwardResult(c *gin.Context, result *ForwardResult) *ForwardResult {
	if result == nil {
		return nil
	}
	if bridge, ok := takeCaptureResult(c); ok {
		if bridge.RequestCaptureInvalid {
			return result
		}
		result.UpstreamRequest = snapshotBytes(bridge.UpstreamRequest)
		result.UpstreamRequestHash = bridge.UpstreamRequestHash
		result.CaptureRequest = snapshotBytes(bridge.UpstreamRequest)
		result.CaptureRequestHeaders = bridge.RequestHeaders
		result.CaptureResponseHeaders = bridge.ResponseHeaders
		result.CaptureUpstreamEndpoint = redactCaptureEndpoint(bridge.UpstreamEndpoint)
		result.CaptureHTTPStatus = bridge.HTTPStatus
		result.CaptureUpstreamModel = bridge.UpstreamModel
		result.CaptureStream = bridge.UpstreamStream
		result.CaptureStreamKnown = bridge.UpstreamStreamKnown
		if providerRequestID := captureProviderRequestIDBytes(bridge.ResponseHeaders); providerRequestID != "" {
			result.RequestID = providerRequestID
		}
		if bridge.ResponseObserved {
			result.CaptureResponse = snapshotObservedBytes(bridge.Response)
			result.CaptureTruncated = bridge.Truncated
		}
		if platform := firstNonEmpty(bridge.Platform, platformFromCaptureEndpoint(bridge.UpstreamEndpoint)); platform != "" {
			outcome := CaptureOutcomeSuccess
			if result.UpstreamFailed || result.CaptureTerminalError {
				outcome = CaptureOutcomeTerminalError
			}
			if content, enabled := CaptureDecisionFor(c, platform, outcome); enabled {
				result.CaptureContentPolicy = &content
			}
		}
	}
	return result
}

func finalizeForwardResult(c *gin.Context, result *ForwardResult) *ForwardResult {
	return attachCaptureToForwardResult(c, result)
}

// failedForwardResultWithCapture preserves a final HTTP 2xx provider exchange
// that was fully/partially consumed but could not be parsed or completed. It is
// intentionally returned together with the original error so handlers archive
// the exact attempt without treating it as a schedulable success.
func failedForwardResultWithCapture(c *gin.Context, resp *http.Response, model, upstreamModel string, stream bool, startedAt time.Time) *ForwardResult {
	finishCaptureResponse(resp)
	result := &ForwardResult{
		Model:                model,
		UpstreamModel:        upstreamModel,
		Stream:               stream,
		Duration:             time.Since(startedAt),
		UpstreamFailed:       true,
		CaptureTerminalError: true,
	}
	if resp != nil {
		result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("x-goog-request-id"), resp.Header.Get("x-amzn-requestid"))
	}
	return finalizeForwardResult(c, result)
}

// failedForwardResultForError returns a capture-bearing result only when the
// handler must not retry another account. A typed failover error deliberately
// keeps result=nil: the attempt bridge remains in the request context until a
// later successful attempt replaces it or the exhausted-failover sink archives
// the final provider response exactly once.
func failedForwardResultForError(c *gin.Context, resp *http.Response, model, upstreamModel string, stream bool, startedAt time.Time, err error) *ForwardResult {
	var failoverErr *UpstreamFailoverError
	if errors.As(err, &failoverErr) {
		return nil
	}
	return failedForwardResultWithCapture(c, resp, model, upstreamModel, stream, startedAt)
}

// terminalHTTPErrorForwardResult hands a fully consumed provider error response
// to the handler-owned typed terminal sink. Legacy owners keep their existing
// synchronous record construction and therefore receive nil here.
func terminalHTTPErrorForwardResult(c *gin.Context, resp *http.Response, model, upstreamModel string, stream bool, startedAt time.Time, responseComplete bool) *ForwardResult {
	if !captureStreamingAttemptPath(c) {
		return nil
	}
	result := failedForwardResultWithCapture(c, resp, model, upstreamModel, stream, startedAt)
	result.CaptureResponseComplete = responseComplete
	if resp != nil {
		result.CaptureHTTPStatus = resp.StatusCode
	}
	return result
}

// streamErrorForwardResult preserves billing and capture for an upstream
// stream that failed only after semantic output was committed to the client.
// Before that boundary the existing failed/failover behavior remains intact:
// typed failover errors keep result=nil, while terminal local/parse failures
// produce a capture-only UpstreamFailed result.
func streamErrorForwardResult(
	c *gin.Context,
	resp *http.Response,
	model string,
	upstreamModel string,
	startedAt time.Time,
	usage *ClaudeUsage,
	firstTokenMs *int,
	clientDisconnect bool,
	semanticOutput bool,
	err error,
) *ForwardResult {
	if !semanticOutput {
		return failedForwardResultForError(c, resp, model, upstreamModel, true, startedAt, err)
	}
	finishCaptureResponse(resp)
	result := &ForwardResult{
		Model:                model,
		UpstreamModel:        upstreamModel,
		Stream:               true,
		Duration:             time.Since(startedAt),
		FirstTokenMs:         firstTokenMs,
		ClientDisconnect:     clientDisconnect,
		CaptureTerminalError: true,
	}
	if usage != nil {
		result.Usage = *usage
	}
	if resp != nil {
		result.RequestID = firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("x-goog-request-id"), resp.Header.Get("x-amzn-requestid"))
	}
	return finalizeForwardResult(c, result)
}

func attachCaptureToOpenAIForwardResult(c *gin.Context, result *OpenAIForwardResult) *OpenAIForwardResult {
	if result == nil {
		return nil
	}
	if bridge, ok := takeCaptureResult(c); ok && bridge.ResponseObserved {
		if bridge.RequestCaptureInvalid {
			return result
		}
		result.UpstreamRequest = snapshotBytes(bridge.UpstreamRequest)
		result.UpstreamRequestHash = bridge.UpstreamRequestHash
		result.CaptureRequest = snapshotBytes(bridge.UpstreamRequest)
		result.CaptureResponse = snapshotObservedBytes(bridge.Response)
		result.CaptureTruncated = bridge.Truncated
		result.CaptureRequestHeaders = bridge.RequestHeaders
		result.CaptureResponseHeaders = bridge.ResponseHeaders
		result.CaptureUpstreamEndpoint = redactCaptureEndpoint(bridge.UpstreamEndpoint)
		result.CaptureHTTPStatus = bridge.HTTPStatus
		result.CaptureUpstreamModel = bridge.UpstreamModel
		result.CaptureStream = bridge.UpstreamStream
		result.CaptureStreamKnown = bridge.UpstreamStreamKnown
		if providerRequestID := captureProviderRequestIDBytes(bridge.ResponseHeaders); providerRequestID != "" {
			result.RequestID = providerRequestID
		}
		if platform := firstNonEmpty(bridge.Platform, platformFromCaptureEndpoint(bridge.UpstreamEndpoint)); platform != "" {
			outcome := CaptureOutcomeSuccess
			if result.UpstreamFailed || result.CaptureTerminalError {
				outcome = CaptureOutcomeTerminalError
			}
			if content, enabled := CaptureDecisionFor(c, platform, outcome); enabled {
				result.CaptureContentPolicy = &content
			}
		}
	}
	return result
}

func finalizeOpenAIForwardResult(c *gin.Context, result *OpenAIForwardResult, upstreamRequest []byte) *OpenAIForwardResult {
	if result == nil {
		return nil
	}
	// Hashing is required for usage idempotency. Request snapshots belong to the
	// policy-gated attempt bridge; taking another unconditional copy here would
	// allocate up to 16 MiB even when capture is disabled or cannot match.
	if result.UpstreamRequestHash == "" {
		result.UpstreamRequestHash = HashUsageRequestPayload(upstreamRequest)
	}
	return attachCaptureToOpenAIForwardResult(c, result)
}

func platformFromCaptureEndpoint(endpoint string) string {
	host := strings.ToLower(endpoint)
	switch {
	case strings.Contains(host, "api.openai.com") || strings.Contains(host, "/backend-api/codex"):
		return PlatformOpenAI
	case strings.Contains(host, "anthropic"):
		return PlatformAnthropic
	case strings.Contains(host, "generativelanguage") || strings.Contains(host, "googleapis"):
		return PlatformGemini
	case strings.Contains(host, "x.ai"):
		return PlatformGrok
	default:
		return ""
	}
}

// responseColumns 是从原始上游响应体（流式 SSE 或非流式 JSON）轻扫描抽取出的
// 可查询列，供 extractCaptureColumns 写回 CaptureRecord。
type responseColumns struct {
	StopReason          string
	InputTokens         int
	OutputTokens        int
	CacheReadTokens     int
	CacheCreationTokens int
	SignaturePresent    bool
	stopReasonPresent   bool
	inputTokensPresent  bool
	outputTokensPresent bool
	cacheReadPresent    bool
	cacheCreatePresent  bool
	signaturePresentSet bool
}

// extractCaptureSessionID 优先从上游 body 的 metadata.user_id 解出 session_id，
// 无则 fallback 到 body 内 session 提示字段（prompt_cache_key/conversation_id/...）。
func extractCaptureSessionID(body []byte) string {
	for _, path := range []string{"metadata.user_id", "request.metadata.user_id"} {
		if uid := gjson.GetBytes(body, path).String(); uid != "" {
			if parsed := ParseMetadataUserID(uid); parsed != nil && parsed.SessionID != "" {
				return parsed.SessionID
			}
		}
	}
	if conversationID := strings.TrimSpace(gjson.GetBytes(body, "conversationState.conversationId").String()); conversationID != "" {
		return conversationID
	}
	for _, path := range []string{"request.sessionId", "request.session_id", "request.conversation_id"} {
		if sessionID := strings.TrimSpace(gjson.GetBytes(body, path).String()); sessionID != "" {
			if parsed := ParseMetadataUserID(sessionID); parsed != nil && parsed.SessionID != "" {
				return parsed.SessionID
			}
			return sessionID
		}
	}
	return extractBodySessionID(string(body))
}

// extractResponseColumns 轻扫描响应，取 stop_reason/usage/signature 抽取列。
// 流式=按 SSE 行累积（后到覆盖先到）；非流式=单个 JSON。不做完整组装。
func extractResponseColumns(resp []byte, stream bool) responseColumns {
	return extractResponseColumnsForPlatform(resp, stream, "")
}

func extractResponseColumnsForPlatform(resp []byte, stream bool, platform string) responseColumns {
	var cols responseColumns
	setStringValue := func(value string) {
		if value = strings.TrimSpace(value); value != "" {
			cols.StopReason = value
			cols.stopReasonPresent = true
		}
	}
	setString := func(result gjson.Result) {
		if result.Type == gjson.String {
			setStringValue(result.String())
		}
	}
	setInt := func(result gjson.Result, target *int, present *bool) {
		if !result.Exists() || !nonNegativeIntegerGJSON(result) {
			return
		}
		value := result.Int()
		if uint64(value) > uint64(^uint32(0)) {
			return
		}
		*target = int(value)
		*present = true
	}
	setGeminiOutput := func(usage gjson.Result) {
		if !usage.IsObject() {
			return
		}
		candidates := usage.Get("candidatesTokenCount")
		thoughts := usage.Get("thoughtsTokenCount")
		if !candidates.Exists() && !thoughts.Exists() {
			return
		}
		value, ok := checkedAddNonNegativeGJSON(candidates, thoughts)
		if !ok || uint64(value) > uint64(^uint32(0)) {
			return
		}
		cols.OutputTokens = int(value)
		cols.outputTokensPresent = true
	}
	nonEmptyString := func(result gjson.Result) bool {
		return result.Type == gjson.String && strings.TrimSpace(result.String()) != ""
	}
	apply := func(js []byte) {
		setString(gjson.GetBytes(js, "stop_reason"))
		setString(gjson.GetBytes(js, "delta.stop_reason"))
		finishReason, finishReasonCamel := captureResponseStopReasons(js)
		setStringValue(finishReason)
		setString(gjson.GetBytes(js, "response.status"))
		setStringValue(finishReasonCamel)

		setInt(gjson.GetBytes(js, "usage.input_tokens"), &cols.InputTokens, &cols.inputTokensPresent)
		setInt(gjson.GetBytes(js, "usage.prompt_tokens"), &cols.InputTokens, &cols.inputTokensPresent)
		setInt(gjson.GetBytes(js, "message.usage.input_tokens"), &cols.InputTokens, &cols.inputTokensPresent)
		setInt(gjson.GetBytes(js, "response.usage.input_tokens"), &cols.InputTokens, &cols.inputTokensPresent)
		setInt(gjson.GetBytes(js, "usageMetadata.promptTokenCount"), &cols.InputTokens, &cols.inputTokensPresent)
		setInt(gjson.GetBytes(js, "response.usageMetadata.promptTokenCount"), &cols.InputTokens, &cols.inputTokensPresent)

		setInt(gjson.GetBytes(js, "usage.output_tokens"), &cols.OutputTokens, &cols.outputTokensPresent)
		setInt(gjson.GetBytes(js, "usage.completion_tokens"), &cols.OutputTokens, &cols.outputTokensPresent)
		setInt(gjson.GetBytes(js, "response.usage.output_tokens"), &cols.OutputTokens, &cols.outputTokensPresent)
		setGeminiOutput(gjson.GetBytes(js, "usageMetadata"))
		setGeminiOutput(gjson.GetBytes(js, "response.usageMetadata"))

		setInt(gjson.GetBytes(js, "usage.cache_read_input_tokens"), &cols.CacheReadTokens, &cols.cacheReadPresent)
		setInt(gjson.GetBytes(js, "usage.prompt_tokens_details.cached_tokens"), &cols.CacheReadTokens, &cols.cacheReadPresent)
		setInt(gjson.GetBytes(js, "response.usage.input_tokens_details.cached_tokens"), &cols.CacheReadTokens, &cols.cacheReadPresent)
		setInt(gjson.GetBytes(js, "usageMetadata.cachedContentTokenCount"), &cols.CacheReadTokens, &cols.cacheReadPresent)
		setInt(gjson.GetBytes(js, "response.usageMetadata.cachedContentTokenCount"), &cols.CacheReadTokens, &cols.cacheReadPresent)
		setInt(gjson.GetBytes(js, "usage.cache_creation_input_tokens"), &cols.CacheCreationTokens, &cols.cacheCreatePresent)
		// 流式 message_start 事件把 usage（含 cache 明细）挂在 message.usage 下。
		setInt(gjson.GetBytes(js, "message.usage.cache_read_input_tokens"), &cols.CacheReadTokens, &cols.cacheReadPresent)
		setInt(gjson.GetBytes(js, "message.usage.cache_creation_input_tokens"), &cols.CacheCreationTokens, &cols.cacheCreatePresent)
		setInt(gjson.GetBytes(js, "message.usage.output_tokens"), &cols.OutputTokens, &cols.outputTokensPresent)
		if platform == PlatformOpenAI {
			openAIUsage := gjson.GetBytes(js, "usage")
			if !openAIUsage.IsObject() {
				openAIUsage = gjson.GetBytes(js, "response.usage")
			}
			if openAIUsage.IsObject() {
				setInt(firstExistingOpenAIUsageCounter(openAIUsage, "input_tokens", "prompt_tokens"), &cols.InputTokens, &cols.inputTokensPresent)
				setInt(firstExistingOpenAIUsageCounter(openAIUsage, "output_tokens", "completion_tokens"), &cols.OutputTokens, &cols.outputTokensPresent)
				setInt(firstExistingOpenAIUsageCounter(openAIUsage,
					"input_tokens_details.cached_tokens", "prompt_tokens_details.cached_tokens",
					"cache_read_input_tokens", "cache_read_tokens", "cached_tokens"), &cols.CacheReadTokens, &cols.cacheReadPresent)
				setInt(firstExistingOpenAIUsageCounter(openAIUsage,
					"input_tokens_details.cache_write_tokens", "prompt_tokens_details.cache_write_tokens",
					"input_tokens_details.cache_creation_tokens", "prompt_tokens_details.cache_creation_tokens",
					"cache_write_tokens", "cache_creation_input_tokens", "cache_write_input_tokens", "cache_creation_tokens"), &cols.CacheCreationTokens, &cols.cacheCreatePresent)
			}
		}
		// fast-path guard: 先做字符串命中再走 content ForEach 深扫，避免逐块解析开销。
		if bytes.Contains(js, []byte(`"signature"`)) || bytes.Contains(js, []byte(`"thoughtSignature"`)) || bytes.Contains(js, []byte("signature_delta")) {
			if nonEmptyString(gjson.GetBytes(js, "signature")) ||
				nonEmptyString(gjson.GetBytes(js, "thoughtSignature")) ||
				nonEmptyString(gjson.GetBytes(js, "delta.signature")) ||
				(gjson.GetBytes(js, "delta.type").String() == "signature_delta" && nonEmptyString(gjson.GetBytes(js, "delta.signature"))) {
				cols.SignaturePresent = true
				cols.signaturePresentSet = true
			}
			gjson.GetBytes(js, "content").ForEach(func(_, b gjson.Result) bool {
				if nonEmptyString(b.Get("signature")) || nonEmptyString(b.Get("thoughtSignature")) {
					cols.SignaturePresent = true
					cols.signaturePresentSet = true
					return false
				}
				return true
			})
			if captureResponseHasNonEmptySignature(js) {
				cols.SignaturePresent = true
				cols.signaturePresentSet = true
			}
		}
	}
	if platform == PlatformKiro && len(resp) > 0 {
		if parsed, err := kiropkg.ParseNonStreamingEventStreamWithContext(bytes.NewReader(resp), "capture", kiropkg.KiroRequestContext{}); err == nil && parsed != nil {
			setStringValue(parsed.StopReason)
			presence := parsed.Usage.ProviderUsagePresence()
			setObservedToken := func(value int, observed bool, target *int, present *bool) {
				if !observed || value < 0 || uint64(value) > uint64(^uint32(0)) {
					return
				}
				*target = value
				*present = true
			}
			setObservedToken(parsed.Usage.InputTokens, presence.InputTokens, &cols.InputTokens, &cols.inputTokensPresent)
			setObservedToken(parsed.Usage.OutputTokens, presence.OutputTokens, &cols.OutputTokens, &cols.outputTokensPresent)
			setObservedToken(parsed.Usage.CacheReadInputTokens, presence.CacheReadTokens, &cols.CacheReadTokens, &cols.cacheReadPresent)
			setObservedToken(parsed.Usage.CacheCreationInputTokens, presence.CacheCreationTokens, &cols.CacheCreationTokens, &cols.cacheCreatePresent)
			return cols
		}
	}
	providerSSE := stream || bytes.Contains(resp, []byte("\ndata:")) || bytes.HasPrefix(bytes.TrimSpace(resp), []byte("data:")) || bytes.HasPrefix(bytes.TrimSpace(resp), []byte("event:"))
	if !providerSSE {
		apply(resp)
		return cols
	}
	sc := bufio.NewScanner(bytes.NewReader(resp))
	// A non-truncated capture may contain exactly captureHardMaxBodyBytes in a
	// single unterminated SSE line. Scanner's max token size is exclusive at
	// that boundary, so retain one byte of bounded probe capacity.
	sc.Buffer(make([]byte, 0, 64*1024), captureHardMaxBodyBytes+1)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if bytes.HasPrefix(line, []byte("data:")) {
			payload := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(payload) > 0 && !bytes.Equal(payload, []byte("[DONE]")) {
				apply(payload)
			}
		}
	}
	return cols
}

// redactedHeaderKeys 需剥离的凭证类 header（小写匹配）。
var redactedHeaderKeys = map[string]struct{}{
	"authorization":        {},
	"x-api-key":            {},
	"api-key":              {}, // Azure-style bare api-key header
	"cookie":               {},
	"set-cookie":           {},
	"proxy-authorization":  {},
	"x-goog-api-key":       {},
	"x-amz-security-token": {}, // AWS STS temporary session credential
}

func captureHeaderIsSensitive(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	if _, exact := redactedHeaderKeys[lower]; exact {
		return true
	}
	compact := strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(lower)
	for _, diagnostic := range []string{"requestid", "xrequestid", "requestidheader", "ratelimit", "contenttype", "contentlength", "useragent", "anthropicversion", "anthropicbeta"} {
		if compact == diagnostic {
			return false
		}
	}
	for _, marker := range []string{"authentication", "authorization", "credential", "signature", "password", "passwd", "secret", "bearertoken", "accesstoken", "refreshtoken", "sessiontoken", "securitytoken", "apikey", "apitoken", "authkey", "authtoken"} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	if captureQueryKeyIsSensitive(lower) {
		return true
	}
	// Accounts may inject arbitrary relay credentials through custom_headers.
	// Match credential-bearing header name segments without hiding useful
	// diagnostics such as request IDs, rate-limit headers, model, beta, or
	// content negotiation metadata.
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(lower)
	for _, segment := range strings.Split(normalized, "_") {
		switch segment {
		case "auth", "authorization", "token", "key", "secret", "password", "passwd", "credential", "credentials", "signature":
			return true
		}
	}
	return false
}

// redactHeadersJSON 剥离凭证类头后序列化为 JSON。保留影响模型行为/诊断的头
// （anthropic-version、anthropic-beta、x-request-id、限流头等）。nil/空 -> nil。
func redactHeadersJSON(h map[string][]string) []byte {
	if len(h) == 0 {
		return nil
	}
	clean := make(map[string][]string, len(h))
	for k, v := range h {
		if captureHeaderIsSensitive(k) {
			continue
		}
		clean[k] = v
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return nil
	}
	return b
}

// redactHTTPHeader 是 http.Header 的适配。
func redactHTTPHeader(h http.Header) []byte { return redactHeadersJSON(map[string][]string(h)) }

// SnapshotForCapture 返回 src 的受限独立副本（<= limit 字节），供 handler 采集请求体。
func SnapshotForCapture(src []byte, limit int) []byte { b, _ := captureWithLimit(src, limit); return b }

// SnapshotForCaptureWithFlag 与 SnapshotForCapture 相同，但额外返回是否被截断，
// 供调用方把请求截断并入 CaptureRecord.Truncated。
func SnapshotForCaptureWithFlag(src []byte, limit int) ([]byte, bool) {
	return captureWithLimit(src, limit)
}

// CaptureRequestID 返回上游 request_id；为空时兜底生成 UUID，
// 仅用于归档记录（不影响返回客户端）。满足 PDF「全局唯一 request_id」。
func CaptureRequestID(upstream string) string {
	if s := strings.TrimSpace(upstream); s != "" {
		return s
	}
	return "cap_" + uuid.NewString()
}

// buildErrorCaptureRecord 组装一条“上游错误响应”归档记录。请求/响应体均受 limit
// 截断并独立拷贝；头部从上游 http.Response 取（脱敏）。所有字段只反映上游相关信息。
// 返回 nil 表示无需归档（reqBody 与 respBody 都为空）。
func buildErrorCaptureRecord(resp *http.Response, platform, requestedModel, upstreamModel, upstreamEndpoint string, stream bool, reqBody, respBody []byte, limit int) *CaptureRecord {
	if len(reqBody) == 0 && len(respBody) == 0 {
		return nil
	}
	rawReq, requestTruncated := captureWithLimit(reqBody, limit)
	rawResp, responseTruncated := captureWithLimit(respBody, limit)
	rec := &CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         platform,
		RequestedModel:   requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: redactCaptureEndpoint(upstreamEndpoint),
		Stream:           stream,
		RawRequest:       rawReq,
		RawResponse:      rawResp,
		Truncated:        requestTruncated || responseTruncated,
	}
	if resp != nil {
		rec.HTTPStatus = resp.StatusCode
		rec.RequestID = resp.Header.Get("x-request-id")
		if resp.Request != nil {
			rec.RequestHeaders = redactHTTPHeader(resp.Request.Header)
		}
		rec.ResponseHeaders = redactHTTPHeader(resp.Header)
	}
	return rec
}

func captureEndpointFromResponse(resp *http.Response) string {
	if resp == nil || resp.Request == nil || resp.Request.URL == nil {
		return ""
	}
	return redactCaptureURL(resp.Request.URL)
}

func captureRequestHeadersFromResponse(resp *http.Response) http.Header {
	if resp == nil || resp.Request == nil {
		return nil
	}
	return resp.Request.Header.Clone()
}

// BuildTerminalErrorCaptureRecord materializes only a final upstream HTTP
// failure. Callers must explicitly mark that an actual error-status response
// was received; transport, local scheduling, and errors embedded in an HTTP 200
// stream are deliberately excluded even when they reuse HTTP-like status codes.
func BuildTerminalErrorCaptureRecord(c *gin.Context, platform string, failure *UpstreamFailoverError, limit int) *CaptureRecord {
	if c == nil || failure == nil || failure.StatusCode <= 0 || !failure.HasUpstreamHTTPResponse {
		return nil
	}
	content, enabled := CaptureDecisionFor(c, platform, CaptureOutcomeTerminalError)
	if !enabled {
		return nil
	}
	bridge, hasBridge := takeCaptureResult(c)
	if !hasBridge || bridge.RequestCaptureInvalid || bridge.UpstreamRequest == nil {
		return nil
	}

	requestedModel := captureRequestedModel(c)
	var upstreamModel string
	var stream bool
	if value, ok := c.Get("parsed_request"); ok {
		if parsed, valid := value.(*ParsedRequest); valid && parsed != nil {
			if requestedModel == "" {
				requestedModel = parsed.Model
			}
			stream = parsed.Stream
		}
	}

	rawRequest, requestTruncated := captureWithLimit(bridge.UpstreamRequest, limit)
	endpoint := strings.TrimSpace(failure.UpstreamEndpoint)
	httpStatus := failure.StatusCode
	requestHeaders := redactHTTPHeader(failure.RequestHeaders)
	responseHeaders := redactHTTPHeader(failure.ResponseHeaders)
	if hasBridge {
		requestTruncated = bridge.RequestTruncated || requestTruncated
		if len(bridge.RequestHeaders) > 0 {
			requestHeaders = snapshotBytes(bridge.RequestHeaders)
		}
		if len(bridge.ResponseHeaders) > 0 {
			responseHeaders = snapshotBytes(bridge.ResponseHeaders)
		}
		if bridge.UpstreamEndpoint != "" {
			endpoint = bridge.UpstreamEndpoint
		}
		if bridge.ResponseObserved && bridge.HTTPStatus > 0 {
			httpStatus = bridge.HTTPStatus
		}
		if bridge.UpstreamModel != "" {
			upstreamModel = bridge.UpstreamModel
		}
		if bridge.UpstreamStreamKnown {
			stream = bridge.UpstreamStream
		}
	}
	rawResponse, responseTruncated := captureWithLimit(failure.ResponseBody, limit)
	if hasBridge && bridge.ResponseObserved {
		rawResponse = snapshotBytes(bridge.Response)
		responseTruncated = bridge.ResponseTruncated
	}
	if model, outboundStream, streamKnown := extractCaptureProviderRequestMeta(platform, rawRequest, endpoint); (upstreamModel == "" && model != "") || (!hasBridge || !bridge.UpstreamStreamKnown) && streamKnown {
		if upstreamModel == "" && model != "" {
			upstreamModel = model
		}
		if (!hasBridge || !bridge.UpstreamStreamKnown) && streamKnown {
			stream = outboundStream
		}
	}
	if requestedModel == "" {
		requestedModel = upstreamModel
	}
	if upstreamModel == "" {
		upstreamModel = requestedModel
	}
	requestID := captureProviderRequestIDBytes(responseHeaders)
	rec := &CaptureRecord{
		CapturedAt:       time.Now().UTC(),
		Platform:         platform,
		RequestID:        requestID,
		RequestedModel:   requestedModel,
		UpstreamModel:    upstreamModel,
		UpstreamEndpoint: redactCaptureEndpoint(endpoint),
		Stream:           stream,
		HTTPStatus:       httpStatus,
		RawRequest:       rawRequest,
		RawResponse:      rawResponse,
		RequestHeaders:   requestHeaders,
		ResponseHeaders:  responseHeaders,
		Truncated:        requestTruncated || responseTruncated,
		ContentPolicy:    &content,
	}
	if rec.RequestID == "" {
		rec.RequestID = CaptureRequestID("")
	}
	return rec
}

func extractCaptureProviderRequestMeta(platform string, rawRequest []byte, endpoint string) (model string, stream bool, streamKnown bool) {
	for _, path := range []string{
		"model",
		"conversationState.currentMessage.userInputMessage.modelId",
		"modelId",
		"request.model",
		"request.modelId",
	} {
		if value := strings.TrimSpace(gjson.GetBytes(rawRequest, path).String()); value != "" {
			model = value
			break
		}
	}
	if streamValue := gjson.GetBytes(rawRequest, "stream"); streamValue.Exists() && (streamValue.Type == gjson.True || streamValue.Type == gjson.False) {
		stream = streamValue.Bool()
		streamKnown = true
	}

	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err == nil && parsed != nil {
		path, _ := url.PathUnescape(parsed.EscapedPath())
		if idx := strings.Index(path, "/models/"); idx >= 0 {
			segment := path[idx+len("/models/"):]
			if colon := strings.IndexByte(segment, ':'); colon >= 0 {
				if model == "" {
					model = strings.TrimSpace(segment[:colon])
				}
				action := strings.ToLower(strings.TrimSpace(segment[colon+1:]))
				if strings.Contains(action, "streamgeneratecontent") {
					stream, streamKnown = true, true
				} else if strings.Contains(action, "generatecontent") || strings.Contains(action, "counttokens") {
					stream, streamKnown = false, true
				}
			}
		}
		if idx := strings.Index(path, "/model/"); idx >= 0 {
			segment := path[idx+len("/model/"):]
			if invoke := strings.Index(segment, "/invoke"); invoke >= 0 {
				if model == "" {
					model = strings.TrimSpace(segment[:invoke])
				}
				stream = strings.Contains(segment[invoke:], "response-stream")
				streamKnown = true
			}
		}
		lowerPath := strings.ToLower(path)
		if strings.Contains(lowerPath, ":streamgeneratecontent") {
			stream, streamKnown = true, true
		} else if strings.Contains(lowerPath, ":generatecontent") || strings.Contains(lowerPath, ":counttokens") {
			stream, streamKnown = false, true
		}
	}
	if strings.EqualFold(strings.TrimSpace(platform), PlatformKiro) && isKiroNativeEventStreamEndpoint(endpoint) {
		stream, streamKnown = true, true
	}
	return model, stream, streamKnown
}

// isKiroNativeEventStreamEndpoint recognizes only the runtime endpoints built
// by buildKiroEndpoints. KIRO API-key relays retain PlatformKiro while using a
// caller-configured Anthropic-compatible /v1/messages endpoint, so platform
// alone cannot identify the provider wire protocol. Native MCP is JSON rather
// than event-stream and intentionally does not match here.
func isKiroNativeEventStreamEndpoint(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed == nil {
		return false
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil || path != "/generateAssistantResponse" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "runtime.us-east-1.kiro.dev" {
		return true
	}
	return strings.HasPrefix(host, "q.") && strings.HasSuffix(host, ".amazonaws.com")
}

func isBedrockStreamingCaptureEndpoint(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed == nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	const hostPrefix = "bedrock-runtime."
	const hostSuffix = ".amazonaws.com"
	if !strings.HasPrefix(host, hostPrefix) || !strings.HasSuffix(host, hostSuffix) || len(host) <= len(hostPrefix)+len(hostSuffix) {
		return false
	}
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil {
		return false
	}
	return strings.HasPrefix(path, "/model/") && strings.HasSuffix(path, "/invoke-with-response-stream")
}

func captureHeaderValue(raw []byte, name string) string {
	if len(raw) == 0 || strings.TrimSpace(name) == "" {
		return ""
	}
	var headers map[string][]string
	if err := json.Unmarshal(raw, &headers); err != nil {
		return ""
	}
	for key, values := range headers {
		if strings.EqualFold(key, name) && len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func captureProviderRequestID(header http.Header) string {
	for _, name := range []string{"x-request-id", "request-id", "x-goog-request-id", "xai-request-id", "x-amzn-requestid", "x-amz-request-id"} {
		if value := strings.TrimSpace(header.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func captureProviderRequestIDBytes(raw []byte) string {
	for _, name := range []string{"x-request-id", "request-id", "x-goog-request-id", "xai-request-id", "x-amzn-requestid", "x-amz-request-id"} {
		if value := strings.TrimSpace(captureHeaderValue(raw, name)); value != "" {
			return value
		}
	}
	return ""
}

// ExtractCaptureMetadataForCompatibility keeps legacy capture fixtures on the
// bounded sidecar extractor during migration. Production provider handlers do
// not call this adapter; they keep their existing lifecycle until protocol
// attempts are introduced in the later migration tasks.
func ExtractCaptureMetadataForCompatibility(record *CaptureRecord) (model.Extracted, error) {
	if record == nil {
		return model.Extracted{}, nil
	}
	format := model.PayloadJSON
	trimmedResponse := bytes.TrimSpace(record.RawResponse)
	if strings.EqualFold(strings.TrimSpace(record.Platform), PlatformKiro) &&
		len(trimmedResponse) > 0 && trimmedResponse[0] != '{' && trimmedResponse[0] != '[' &&
		!bytes.HasPrefix(trimmedResponse, []byte("data:")) && !bytes.HasPrefix(trimmedResponse, []byte("event:")) {
		format = model.PayloadAWSEventStream
	} else if record.Stream || bytes.HasPrefix(trimmedResponse, []byte("data:")) ||
		bytes.HasPrefix(trimmedResponse, []byte("event:")) || bytes.Contains(record.RawResponse, []byte("\ndata:")) {
		format = model.PayloadSSE
	}
	return captureextract.FromReaders(context.Background(), captureextract.Input{
		Format:   format,
		Request:  bytes.NewReader(record.RawRequest),
		Response: bytes.NewReader(record.RawResponse),
		Initial: model.Extracted{
			SessionID:           record.SessionID,
			ThinkingEffort:      record.ThinkingEffort,
			ThinkingType:        record.ThinkingType,
			SignaturePresent:    record.SignaturePresent,
			InputTokens:         captureUInt32(record.InputTokens),
			OutputTokens:        captureUInt32(record.OutputTokens),
			CacheReadTokens:     captureUInt32(record.CacheReadTokens),
			CacheCreationTokens: captureUInt32(record.CacheCreationTokens),
			StopReason:          record.StopReason,
		},
	})
}

// captureUInt32 preserves the compatibility extractor's fail-closed handling
// for invalid token counters without depending on the retired native writer.
func captureUInt32(value int) uint32 {
	if value < 0 || uint64(value) > uint64(^uint32(0)) {
		return 0
	}
	return uint32(value)
}

// extractCaptureColumns 在 worker 内填充 rec 的抽取列，供归档写入前调用。
func extractCaptureColumns(rec *CaptureRecord) {
	if rec.SessionID == "" {
		rec.SessionID = extractCaptureSessionID(rec.RawRequest)
	}

	// 仅当 submit 侧未预填时，才从 raw_request 回退抽取。
	// Bedrock/Kiro 等 body 里 output_config 可能已被剥离/翻译，故 submit 侧优先用
	// ParsedRequest.OutputEffort 预填；此处不覆盖已有值。
	if rec.ThinkingEffort == "" {
		effortValue := firstNonEmpty(
			gjson.GetBytes(rec.RawRequest, "output_config.effort").String(),
			gjson.GetBytes(rec.RawRequest, "additionalModelRequestFields.output_config.effort").String(),
			gjson.GetBytes(rec.RawRequest, "request.output_config.effort").String(),
		)
		if effort := NormalizeClaudeOutputEffort(effortValue); effort != nil {
			rec.ThinkingEffort = *effort
		}
	}
	if rec.ThinkingType == "" {
		rec.ThinkingType = firstNonEmpty(
			gjson.GetBytes(rec.RawRequest, "thinking.type").String(),
			gjson.GetBytes(rec.RawRequest, "additionalModelRequestFields.thinking.type").String(),
			gjson.GetBytes(rec.RawRequest, "request.thinking.type").String(),
		)
	}

	cols := extractResponseColumnsForPlatform(rec.RawResponse, rec.Stream, rec.Platform)
	if cols.stopReasonPresent {
		rec.StopReason = cols.StopReason
	}
	if cols.inputTokensPresent {
		rec.InputTokens = cols.InputTokens
	}
	if cols.outputTokensPresent {
		rec.OutputTokens = cols.OutputTokens
	}
	if cols.cacheReadPresent {
		rec.CacheReadTokens = cols.CacheReadTokens
	}
	if cols.cacheCreatePresent {
		rec.CacheCreationTokens = cols.CacheCreationTokens
	}
	if cols.signaturePresentSet {
		rec.SignaturePresent = cols.SignaturePresent
	}
}

// ApplyCaptureContentPolicy clears disabled persistence fields. Call it only
// after extractCaptureColumns so searchable metadata survives body suppression.
func ApplyCaptureContentPolicy(rec *CaptureRecord, policy CaptureContentPolicy) {
	if rec == nil {
		return
	}
	if !policy.RawRequest {
		rec.RawRequest = nil
	}
	if !policy.RawResponse {
		rec.RawResponse = nil
	}
	if !policy.RequestHeaders {
		rec.RequestHeaders = nil
	}
	if !policy.ResponseHeaders {
		rec.ResponseHeaders = nil
	}
}
