package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	mathrand "math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	kiropkg "github.com/Wei-Shaw/sub2api/internal/pkg/kiro"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"go.uber.org/zap"
)

type kiroEndpointConfig struct {
	URL       string
	AmzTarget string
	Name      string
}

// kiroTranslatedStreamBody owns both sides of the translation boundary. The
// shared Anthropic reader only sees the pipe; closing that pipe alone does not
// interrupt a translator blocked in a raw AWS event-stream Read.
type kiroTranslatedStreamBody struct {
	*io.PipeReader
	raw                           io.Closer
	activity                      *providerBodyReadActivity
	done                          <-chan struct{}
	cancel                        context.CancelFunc
	stageSyntheticWebSearchEvents bool
	providerTerminalKnown         atomic.Bool
	providerTerminalObserved      atomic.Bool
	closeOnce                     sync.Once
	closeErr                      error
}

func (b *kiroTranslatedStreamBody) providerReadActivity() *providerBodyReadActivity {
	if b == nil {
		return nil
	}
	return b.activity
}

func (b *kiroTranslatedStreamBody) setProviderTerminalObservation(observed bool) {
	if b == nil {
		return
	}
	b.providerTerminalObserved.Store(observed)
	b.providerTerminalKnown.Store(true)
}

func (b *kiroTranslatedStreamBody) providerTerminalObservation() (bool, bool) {
	if b == nil || !b.providerTerminalKnown.Load() {
		return false, false
	}
	return b.providerTerminalObserved.Load(), true
}

func (b *kiroTranslatedStreamBody) Close() error {
	if b == nil {
		return nil
	}
	b.closeOnce.Do(func() {
		if b.cancel != nil {
			b.cancel()
		}
		b.closeErr = b.PipeReader.Close()
		if b.raw != nil {
			if captureBody, ok := b.raw.(*captureBodyReadCloser); ok {
				// Interrupt the raw AWS read without freezing the capture. The
				// translator may receive a final chunk while Close unblocks it.
				b.closeErr = errors.Join(b.closeErr, captureBody.closeUnderlying())
			} else {
				b.closeErr = errors.Join(b.closeErr, b.raw.Close())
			}
		}
		if b.done != nil {
			<-b.done
		}
		if captureBody, ok := b.raw.(*captureBodyReadCloser); ok {
			captureBody.Finish(captureBody.resp)
		}
	})
	return b.closeErr
}

var (
	buildKiroPayloadWithRequestContext = kiropkg.BuildKiroPayloadWithRequestContext
	estimateKiroClaudeInputTokens      = kiropkg.EstimateClaudeInputTokens
)

func kiroProfileArnForEndpoint(account *Account, endpoint kiroEndpointConfig) string {
	if endpoint.Name == "KiroRuntime" {
		return kiroResolveProfileArnForKRS(account)
	}
	return kiroResolveProfileArnForPayload(account, KiroEndpointModeQ)
}

const kiroInvalidModelTempUnschedDuration = time.Minute

const (
	kiroRetryBaseDelay = 200 * time.Millisecond
	kiroRetryMaxDelay  = 2 * time.Second
)

var kiroRetrySleep = sleepWithContext

// KiroModelNotSupportedError 表示请求模型不在 kiro 账号的 model_mapping 白名单中。
// 应被网关 handler 映射为 400 Bad Request（客户端/配置错误,非上游故障）。
type KiroModelNotSupportedError struct {
	AccountID      int64
	RequestedModel string
}

func (e *KiroModelNotSupportedError) Error() string {
	return fmt.Sprintf("kiro account %d does not support model %q (not in account model_mapping)", e.AccountID, e.RequestedModel)
}

// kiroModelNotInMappingError 返回"请求模型不在 kiro 账号 model_mapping 中"的错误。
// 该错误应被上层映射为 400 Bad Request（客户端/配置错误），不应进入 failover 重试同一账号。
func kiroModelNotInMappingError(account *Account, requestedModel string) error {
	accountID := int64(0)
	if account != nil {
		accountID = account.ID
	}
	return &KiroModelNotSupportedError{AccountID: accountID, RequestedModel: requestedModel}
}

func kiroRetryBackoffDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := kiroRetryBaseDelay * time.Duration(1<<attempt)
	if delay > kiroRetryMaxDelay {
		delay = kiroRetryMaxDelay
	}
	jitterMax := delay / 4
	if jitterMax <= 0 {
		return delay
	}
	return delay + time.Duration(mathrand.Int63n(int64(jitterMax)+1))
}

func sleepKiroRetry(ctx context.Context, attempt int) error {
	return kiroRetrySleep(ctx, kiroRetryBackoffDelay(attempt))
}

func resolveKiroUpstreamModel(mappedModel string) string {
	upstreamModel := kiropkg.MapModel(mappedModel)
	if strings.TrimSpace(upstreamModel) == "" {
		upstreamModel = mappedModel
	}
	return upstreamModel
}

func (s *GatewayService) forwardKiroMessages(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, startTime time.Time) (*ForwardResult, error) {
	if account == nil || parsed == nil {
		return nil, fmt.Errorf("kiro forward: missing account or request")
	}
	captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled && CaptureMayApplyFor(c, string(account.Platform))
	if captureEnabled {
		setCapturePlatform(c, string(account.Platform))
		ctx = withCaptureUpstreamRequestContext(ctx, c, s.cfg.Gateway.Capture.MaxBodyBytes)
	}

	originalModel := parsed.Model
	mappedModel, matched := account.ResolveMappedModel(originalModel)
	if !matched {
		return nil, kiroModelNotInMappingError(account, originalModel)
	}
	body := parsed.Body.Bytes()
	if mappedModel != originalModel {
		body = s.replaceModelInBody(body, mappedModel)
	}
	logger.L().Debug("gateway forward_kiro_messages: request prepared",
		zap.Int64("account_id", account.ID),
		zap.String("auth_method", strings.TrimSpace(account.GetCredential("auth_method"))),
		zap.String("requested_model", originalModel),
		zap.String("mapped_model", mappedModel),
		zap.Bool("has_profile_arn", strings.TrimSpace(account.GetCredential("profile_arn")) != ""),
	)

	if s.shouldEmulateWebSearch(ctx, account, parsed.GroupID, body) {
		parsedForEmulation, err := parsed.CloneForBody(body)
		if err != nil {
			return nil, err
		}
		parsedForEmulation.Model = mappedModel
		return s.handleWebSearchEmulation(ctx, c, account, parsedForEmulation)
	}

	if parsed.Stream {
		resp, _, err := s.openKiroAnthropicStreamResponse(ctx, c, account, parsed, body, mappedModel, originalModel, c.Request.Header, parsed.Group)
		if err != nil {
			var failoverErr *UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: failoverErr.StatusCode,
					Kind:               "failover",
					Message:            sanitizeUpstreamErrorMessage(err.Error()),
				})
				return nil, failoverErr
			}
			safeErr := sanitizeUpstreamErrorMessage(err.Error())
			setOpsUpstreamError(c, 0, safeErr, "")
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			c.JSON(http.StatusBadGateway, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "api_error",
					"message": "Upstream request failed",
				},
			})
			return nil, fmt.Errorf("kiro upstream request failed: %s", safeErr)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 400 {
			return nil, s.handleKiroHTTPError(ctx, resp, c, account, mappedModel, body, true)
		}
		upstreamModel := resolveKiroUpstreamModel(mappedModel)
		streamResult, err := s.handleStreamingResponse(ctx, resp, c, account, startTime, originalModel, mappedModel, false)
		if translatedBody, ok := resp.Body.(*kiroTranslatedStreamBody); ok && streamResult != nil {
			if providerTerminal, known := translatedBody.providerTerminalObservation(); known {
				// Capture completeness is provider-native truth. The translator's
				// synthetic message_stop remains client protocol only.
				streamResult.responseComplete = providerTerminal
			}
		}
		if err != nil {
			resultErr := err
			var failoverErr *UpstreamFailoverError
			if errors.As(err, &failoverErr) && c.Writer.Written() {
				// The generic stream reader may classify a transport break as
				// failover before it sees the translated KIRO pipe state. Once the
				// client has semantic output, replay is forbidden; preserve the
				// observed usage/capture and surface a plain visible error instead.
				resultErr = fmt.Errorf("kiro committed stream failed: %s", sanitizeUpstreamErrorMessage(err.Error()))
			}
			partial := partialStreamUsageResult(c, resp, streamResult, originalModel, upstreamModel, startTime, resultErr)
			if partial == nil {
				if errors.As(err, &failoverErr) {
					// A translated KIRO pipe can fail before semantic output for two
					// different reasons. Transport/parser failures have no final provider
					// HTTP response and must discard the request-only bridge before the
					// next account. A typed WebSearch HTTP failure, however, already owns
					// the final native AWS request/response pair; preserve that bridge for
					// the handler's single terminal-error submission.
					if !failoverErr.HasUpstreamHTTPResponse {
						_, _ = takeCaptureResult(c)
					}
					return nil, err
				}
				_, _ = takeCaptureResult(c)
				return nil, &UpstreamFailoverError{Stage: GatewayFailureStageInference, ClientMessage: sanitizeUpstreamErrorMessage(err.Error())}
			}
			finalizeKiroCapture(c, partial)
			return partial, resultErr
		}
		if streamResult.usage == nil {
			streamResult.usage = &ClaudeUsage{}
		}
		requestID := buildKiroRequestID(resp)
		result := &ForwardResult{
			RequestID:               requestID,
			Usage:                   *streamResult.usage,
			Model:                   originalModel,
			UpstreamModel:           upstreamModel,
			Stream:                  true,
			Duration:                time.Since(startTime),
			FirstTokenMs:            streamResult.firstTokenMs,
			ClientDisconnect:        streamResult.clientDisconnect,
			CaptureResponseComplete: streamResult.responseComplete,
		}
		// 归档：tee 已在 handleStreamingResponse 内累积翻译后的 Anthropic SSE 并写入 gin.Context 桥；
		// 此处取回填入 result，头用暂存的真实上游头（非 pipe 合成头）。汇入 gateway_handler submit 块统一提交。
		finalizeKiroCapture(c, result)
		return result, nil
	}

	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, err
	}
	if tokenType != "oauth" && tokenType != "apikey" {
		return nil, fmt.Errorf("kiro requires oauth or apikey token, got %s", tokenType)
	}
	if isOnlyWebSearchToolInBody(body) {
		webSearchResult, webSearchErr := s.executeKiroWebSearch(ctx, c, account, parsed.Group, body, mappedModel, originalModel, token, c.Request.Header)
		switch {
		case errors.Is(webSearchErr, errKiroWebSearchFallback):
		case webSearchErr == nil:
			upstreamModel := resolveKiroUpstreamModel(mappedModel)
			c.Header("Content-Type", "application/json")
			claudeReqID := kiropkg.NewClaudeRequestID()
			c.Header("x-request-id", claudeReqID)
			c.Header("request-id", claudeReqID)
			c.Data(http.StatusOK, "application/json", webSearchResult.ResponseBody)
			result := &ForwardResult{
				RequestID:     webSearchResult.RequestID,
				Usage:         webSearchResult.Usage,
				Model:         originalModel,
				UpstreamModel: upstreamModel,
				Stream:        false,
				Duration:      time.Since(startTime),
			}
			finalizeKiroCapture(c, result)
			return result, nil
		default:
			var failoverErr *UpstreamFailoverError
			if errors.As(webSearchErr, &failoverErr) {
				appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
					Platform:           account.Platform,
					AccountID:          account.ID,
					AccountName:        account.Name,
					UpstreamStatusCode: failoverErr.StatusCode,
					Kind:               "failover",
					Message:            sanitizeUpstreamErrorMessage(webSearchErr.Error()),
				})
				return nil, failoverErr
			}
			safeErr := sanitizeUpstreamErrorMessage(webSearchErr.Error())
			c.JSON(http.StatusBadGateway, gin.H{
				"type": "error",
				"error": gin.H{
					"type":    "api_error",
					"message": "Upstream request failed",
				},
			})
			result := &ForwardResult{
				Model: originalModel, UpstreamModel: resolveKiroUpstreamModel(mappedModel), Stream: false,
				Duration: time.Since(startTime), UpstreamFailed: true, CaptureTerminalError: true,
			}
			finalizeKiroCapture(c, result)
			return result, fmt.Errorf("kiro upstream request failed: %s", safeErr)
		}
	}

	resp, requestCtx, err := s.executeKiroUpstreamWithParsed(ctx, account, parsed, body, mappedModel, originalModel, token, c.Request.Header)
	if err != nil {
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: failoverErr.StatusCode,
				Kind:               "failover",
				Message:            sanitizeUpstreamErrorMessage(err.Error()),
			})
			return nil, failoverErr
		}
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		c.JSON(http.StatusBadGateway, gin.H{
			"type": "error",
			"error": gin.H{
				"type":    "api_error",
				"message": "Upstream request failed",
			},
		})
		return nil, fmt.Errorf("kiro upstream request failed: %s", safeErr)
	}
	inputTokens := resolveKiroInputTokens(ctx, body, requestCtx)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return nil, s.handleKiroHTTPError(ctx, resp, c, account, mappedModel, body, false)
	}

	cachePlan := s.prepareKiroCacheEmulationUsage(ctx, account, parsed.Group, body, mappedModel, inputTokens)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		cachePlan.commit()
	}
	cacheUsage := cachePlan.result()
	requestCtx.CacheEmulationUsage = cacheUsage.toKiroUsage()
	captureLimit := 0
	if s.cfg != nil {
		captureLimit = s.cfg.Gateway.Capture.MaxBodyBytes
	}
	finishRawCapture := beginCaptureResponse(c, resp, captureEnabled, captureLimit)
	providerBody, readErr := ReadUpstreamResponseBody(resp.Body, s.cfg, c, nil)
	if readErr != nil {
		finishRawCapture()
		return nil, newInvalidProviderResponseFailover(resp, "failed to read KIRO provider event stream: "+sanitizeStreamError(readErr))
	}
	parseResult, err := kiropkg.ParseNonStreamingEventStreamWithContext(bytes.NewReader(providerBody), originalModel, requestCtx)
	finishRawCapture()
	if err != nil {
		return nil, newInvalidProviderResponseFailover(resp, "failed to parse KIRO provider event stream: "+sanitizeStreamError(err))
	}

	c.Header("Content-Type", "application/json")
	requestID := buildKiroRequestID(resp)
	claudeReqID := kiropkg.NewClaudeRequestID()
	c.Header("x-request-id", claudeReqID)
	c.Header("request-id", claudeReqID)
	c.Data(http.StatusOK, "application/json", parseResult.ResponseBody)

	upstreamModel := resolveKiroUpstreamModel(mappedModel)

	result := &ForwardResult{
		RequestID:     requestID,
		Usage:         kiroUsageToClaude(parseResult.Usage, inputTokens),
		Model:         originalModel,
		UpstreamModel: upstreamModel,
		Stream:        false,
		Duration:      time.Since(startTime),
	}
	finalizeKiroCapture(c, result)
	return result, nil
}

func (s *GatewayService) openKiroAnthropicStreamResponse(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest, anthropicBody []byte, mappedModel, requestModel string, headers http.Header, group *Group) (*http.Response, int, error) {
	token, tokenType, err := s.GetAccessToken(ctx, account)
	if err != nil {
		return nil, 0, err
	}
	// Kiro 直连 AWS 支持两类 token:OAuth access_token 与 API Key(ksk_*)。
	// API Key 模式下 GetAccessToken 返回 tokenType "apikey"(无需刷新)。
	if tokenType != "oauth" && tokenType != "apikey" {
		return nil, 0, fmt.Errorf("kiro requires oauth or apikey token, got %s", tokenType)
	}

	if isOnlyWebSearchToolInBody(anthropicBody) {
		inputTokens := estimateKiroInputTokensForRequest(ctx, anthropicBody, mappedModel, requestModel, headers)
		cachePlan := s.prepareKiroCacheEmulationUsage(ctx, account, group, anthropicBody, mappedModel, inputTokens)
		pr, pw := io.Pipe()
		translatorCtx, cancelTranslator := context.WithCancel(ctx)
		translatorDone := make(chan struct{})
		headers := make(http.Header)
		headers.Set("Content-Type", "text/event-stream")
		go func() {
			defer close(translatorDone)
			defer cancelTranslator()
			streamErr := s.streamKiroWebSearchAsAnthropic(translatorCtx, c, account, anthropicBody, mappedModel, requestModel, token, inputTokens, headers, pw, cachePlan)
			if streamErr != nil {
				_ = pw.CloseWithError(streamErr)
				return
			}
			_ = pw.Close()
		}()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     headers,
			// The inner WebSearch loop owns and publishes the provider-native
			// AWS response. Use the same marker as the normal KIRO translator so
			// the outer Anthropic stream reader cannot overwrite it with SSE.
			Body: &kiroTranslatedStreamBody{
				PipeReader: pr, done: translatorDone, cancel: cancelTranslator,
				stageSyntheticWebSearchEvents: true,
			},
		}, inputTokens, nil
	}

	resp, requestCtx, err := s.executeKiroUpstreamWithParsed(ctx, account, parsed, anthropicBody, mappedModel, requestModel, token, headers)
	if err != nil {
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) {
			return nil, 0, err
		}
		return nil, 0, err
	}
	inputTokens := resolveKiroInputTokens(ctx, anthropicBody, requestCtx)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, inputTokens, nil
	}
	// 归档：暂存真实上游头（脱敏），供 forwardKiroMessages 组装 CaptureRecord 时取回。
	// 流式返回的是 pipe 响应（合成头），真实上游头只在此处可见。
	if s.cfg != nil && s.cfg.Gateway.Capture.Enabled && CaptureMayApplyFor(c, string(account.Platform)) {
		stashKiroCaptureHeaders(c, resp)
	}
	captureEnabled := s.cfg != nil && s.cfg.Gateway.Capture.Enabled &&
		account != nil && CaptureMayApplyFor(c, string(account.Platform))
	captureLimit := 0
	if s.cfg != nil {
		captureLimit = s.cfg.Gateway.Capture.MaxBodyBytes
	}
	finishRawCapture := beginCaptureResponse(c, resp, captureEnabled, captureLimit)
	cachePlan := s.prepareKiroCacheEmulationUsage(ctx, account, group, anthropicBody, mappedModel, inputTokens)
	cachePlan.commit()
	cacheUsage := cachePlan.result()
	requestCtx.CacheEmulationUsage = cacheUsage.toKiroUsage()

	pr, pw := io.Pipe()
	translatorCtx, cancelTranslator := context.WithCancel(ctx)
	translatorDone := make(chan struct{})
	wrappedHeaders := resp.Header.Clone()
	wrappedHeaders.Set("Content-Type", "text/event-stream")
	claudeReqID := kiropkg.NewClaudeRequestID()
	wrappedHeaders.Set("x-request-id", claudeReqID)
	wrappedHeaders.Set("request-id", claudeReqID)
	rawReadActivity := newProviderBodyReadActivity(resp.Body)
	translatedBody := &kiroTranslatedStreamBody{
		PipeReader: pr, raw: resp.Body, activity: rawReadActivity, done: translatorDone, cancel: cancelTranslator,
	}

	go func() {
		defer close(translatorDone)
		defer cancelTranslator()
		defer func() { _ = resp.Body.Close() }()
		streamResult, streamErr := kiropkg.StreamEventStreamAsAnthropicWithContext(translatorCtx, rawReadActivity, pw, requestModel, inputTokens, requestCtx)
		translatedBody.setProviderTerminalObservation(streamResult != nil && streamResult.ProviderTerminalObserved)
		if streamErr != nil {
			drainCaptureResponseRemainderBounded(translatorCtx, resp.Body, captureOverflowDrainTimeout)
		}
		// Publish the provider-native AWS event-stream before closing the pipe.
		// The outer Anthropic/WebChat reader only sees translated SSE and must not
		// win the result assembly race.
		finishRawCapture()
		if streamErr != nil {
			_, _ = io.WriteString(pw, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"stream interrupted\"}}\n\n")
			_ = pw.CloseWithError(streamErr)
			return
		}
		_ = pw.Close()
	}()

	return &http.Response{
		StatusCode: resp.StatusCode,
		Header:     wrappedHeaders,
		Body:       translatedBody,
	}, inputTokens, nil
}

func (s *GatewayService) executeKiroUpstream(ctx context.Context, account *Account, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header) (*http.Response, kiropkg.KiroRequestContext, error) {
	return s.executeKiroUpstreamWithParsed(ctx, account, nil, anthropicBody, mappedModel, requestModel, token, headers)
}

func (s *GatewayService) executeKiroUpstreamWithParsed(ctx context.Context, account *Account, parsed *ParsedRequest, anthropicBody []byte, mappedModel, requestModel, token string, headers http.Header) (*http.Response, kiropkg.KiroRequestContext, error) {
	var requestCtx kiropkg.KiroRequestContext
	machineID := ensureKiroMachineIDPersisted(ctx, s.accountRepo, account)
	accountKey := buildKiroAccountKey(account)
	if err := s.checkAndWaitKiroCooldown(ctx, accountKey); err != nil {
		if failoverErr := asKiroCooldownFailoverError(err); failoverErr != nil {
			return nil, requestCtx, failoverErr
		}
		return nil, requestCtx, err
	}

	// KRS 模式：确保 profileArn 已解析（ListAvailableProfiles + 回填持久化）
	mode := kiroEndpointModeForRequest(account, parsed)
	s.ensureKiroProfileArnForRequest(ctx, account, token, mode)

	modelID := kiropkg.MapModel(mappedModel)
	currentToken := token
	endpoints := buildKiroEndpoints(account, mode)
	proxyURL := kiroProxyURL(account)
	tlsProfile := s.tlsFPProfileService.ResolveTLSProfile(account)
	maxRetries := 2
	if len(endpoints) == 0 {
		return nil, requestCtx, fmt.Errorf("kiro upstream endpoints exhausted")
	}
	baseProfileArn := kiroProfileArnForEndpoint(account, endpoints[0])
	buildResult, err := s.buildKiroPayloadForAccountWithArn(
		ctx, account, parsed, anthropicBody, modelID, currentToken,
		requestModel, headers, baseProfileArn,
	)
	if err != nil {
		return nil, requestCtx, err
	}
	requestCtx = buildResult.Context

	for idx, endpoint := range endpoints {
		profileArn := kiroProfileArnForEndpoint(account, endpoint)
		payload, err := s.retargetKiroPayloadForProfile(account, parsed, anthropicBody, modelID, buildResult.Payload, baseProfileArn, profileArn)
		if err != nil {
			return nil, requestCtx, err
		}
		logKiroStatelessReplay(account, payload)

		for attempt := 0; attempt <= maxRetries; attempt++ {
			req, err := newKiroJSONRequest(ctx, endpoint.URL, payload, currentToken, accountKey, machineID, endpoint.AmzTarget, account)
			if err != nil {
				return nil, requestCtx, err
			}

			s.beginKiroNativeCaptureAttempt(ctx, account, req, payload)
			resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, tlsProfile)
			s.beginKiroNativeCaptureResponse(ctx, resp)
			if err != nil {
				if attempt < maxRetries {
					abortKiroNativeCaptureAttempt(ctx)
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					continue
				}
				abortKiroNativeCaptureAttempt(ctx)
				return nil, requestCtx, err
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				dumpKiro429ResponseForDebug(resp, account.ID, endpoint.URL, endpoint.Name)

				_, err := s.markKiro429(ctx, account.ID, accountKey)
				if err != nil {
					abortKiroNativeCaptureAttempt(ctx)
					_ = resp.Body.Close()
					return nil, requestCtx, err
				}
				if idx+1 < len(endpoints) {
					abortKiroNativeCaptureAttempt(ctx)
					_ = resp.Body.Close()
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					break
				}
				return resp, requestCtx, nil
			}

			if resp.StatusCode == http.StatusRequestTimeout || (resp.StatusCode >= 500 && resp.StatusCode < 600) {
				if attempt < maxRetries {
					abortKiroNativeCaptureAttempt(ctx)
					_ = resp.Body.Close()
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					continue
				}
				if idx+1 < len(endpoints) {
					abortKiroNativeCaptureAttempt(ctx)
					_ = resp.Body.Close()
					if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
						return nil, requestCtx, sleepErr
					}
					break
				}
				return resp, requestCtx, nil
			}

			if resp.StatusCode == http.StatusPaymentRequired {
				respBody, _, readErr := s.readKiroUpstreamErrorBody(ctx, resp)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, newProviderHTTPError(account, resp, respBody, false)
				}
				classification := classifyKiroHTTPError(resp.StatusCode, string(respBody))
				if classification.Category == kiroErrorMonthlyRequest {
					s.markKiroMonthlyRequestCountRateLimited(ctx, account, string(respBody))
				}
				return nil, requestCtx, &UpstreamFailoverError{
					StatusCode:              resp.StatusCode,
					ResponseBody:            respBody,
					RequestHeaders:          captureRequestHeadersFromResponse(resp),
					ResponseHeaders:         resp.Header.Clone(),
					UpstreamEndpoint:        endpoint.URL,
					HasUpstreamHTTPResponse: true,
				}
			}

			if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
				respBody, _, readErr := s.readKiroUpstreamErrorBody(ctx, resp)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, newProviderHTTPError(account, resp, respBody, false)
				}

				if resp.StatusCode == http.StatusForbidden && isKiroSuspendedBody(respBody) {
					if _, err := s.markKiroSuspended(ctx, accountKey); err != nil {
						return nil, requestCtx, err
					}
					resetHTTPResponseBody(resp, respBody)
					return resp, requestCtx, nil
				}

				if s.kiroTokenProvider != nil && (resp.StatusCode == http.StatusUnauthorized || isKiroTokenErrorBody(respBody)) && attempt < maxRetries {
					refreshedToken, refreshErr := s.kiroTokenProvider.ForceRefreshAccessToken(ctx, account)
					if refreshErr == nil && strings.TrimSpace(refreshedToken) != "" {
						abortKiroNativeCaptureAttempt(ctx)
						currentToken = refreshedToken
						machineID = ensureKiroMachineIDPersisted(ctx, s.accountRepo, account)
						accountKey = buildKiroAccountKey(account)
						if sleepErr := sleepKiroRetry(ctx, attempt); sleepErr != nil {
							return nil, requestCtx, sleepErr
						}
						continue
					}
					if refreshErr != nil && isNonRetryableRefreshError(refreshErr) {
						resetHTTPResponseBody(resp, respBody)
						return resp, requestCtx, nil
					}
				}

				if classifyKiroHTTPError(resp.StatusCode, string(respBody)).Category == kiroErrorAuthError {
					s.markKiroAuthTemporarilyUnavailable(ctx, account, resp.StatusCode, string(respBody))
				}

				resetHTTPResponseBody(resp, respBody)
				return resp, requestCtx, nil
			}

			if resp.StatusCode == http.StatusBadRequest {
				respBody, _, readErr := s.readKiroUpstreamErrorBody(ctx, resp)
				_ = resp.Body.Close()
				if readErr != nil {
					return nil, requestCtx, newTerminalProviderHTTPError(account, resp, respBody)
				}
				classification := classifyKiroHTTPError(resp.StatusCode, string(respBody))
				logKiroBadRequestClassification(classification, account, mappedModel, resp.Header, respBody)
				resetHTTPResponseBody(resp, respBody)
				return resp, requestCtx, nil
			}

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				if err := s.markKiroSuccess(ctx, account.ID, accountKey); err != nil {
					abortKiroNativeCaptureAttempt(ctx)
					_ = resp.Body.Close()
					return nil, requestCtx, err
				}
			}
			return resp, requestCtx, nil
		}
	}
	return nil, requestCtx, fmt.Errorf("kiro upstream endpoints exhausted")
}

// kiroKRSEndpointURL 是 Kiro 自家前置网关（KRS = Kiro Runtime Service）的固定 URL。
// KRS 仅支持 us-east-1 / eu-central-1 两个 region；这里固定走 us-east-1。
const kiroKRSEndpointURL = "https://runtime.us-east-1.kiro.dev/generateAssistantResponse"

func buildKiroEndpoints(account *Account, mode string) []kiroEndpointConfig {
	if mode == KiroEndpointModeKRS {
		return []kiroEndpointConfig{
			{
				URL:  kiroKRSEndpointURL,
				Name: "KiroRuntime",
			},
		}
	}
	region := kiroAPIRegion(account)
	endpoints := []kiroEndpointConfig{
		{
			URL:  fmt.Sprintf("https://q.%s.amazonaws.com/generateAssistantResponse", region),
			Name: "AmazonQ",
		},
	}
	if mode == KiroEndpointModeAuto {
		endpoints = append(endpoints, kiroEndpointConfig{
			URL:  kiroKRSEndpointURL,
			Name: "KiroRuntime",
		})
	}
	return endpoints
}

// kiroEndpointModeForRequest 从 ParsedRequest 取 group 配置的 Kiro endpoint 模式；
// parsed/Group 为 nil 时安全兜底为 "q"。
//
// API Key 账号强制走 Q 端点(q.{region}.amazonaws.com):KRS 网关
// (runtime.us-east-1.kiro.dev)是 Kiro 自家 OAuth 网关,只认 OAuth/IdC token +
// profileArn,不接受 AWS 的 ksk_ API Key(会返回 403 "bearer token invalid")。
// 与 kiro.rs 一致——其 API Key 模式也只走 q.{region}.amazonaws.com。
func kiroEndpointModeForRequest(account *Account, parsed *ParsedRequest) string {
	if account != nil && account.Type == AccountTypeAPIKey {
		return KiroEndpointModeQ
	}
	if parsed == nil || parsed.Group == nil {
		return KiroEndpointModeQ
	}
	return resolveKiroEndpointMode(account, parsed.Group)
}

func (s *GatewayService) buildKiroPayloadForAccountWithArn(ctx context.Context, account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, token, requestModel string, headers http.Header, profileArn string) (*kiropkg.KiroBuildResult, error) {
	_ = s
	_ = token
	anthropicBody = prepareKiroPayloadBodyForRequestModel(anthropicBody, requestModel)
	buildResult, err := buildKiroPayloadWithRequestContext(ctx, anthropicBody, modelID, profileArn, "AI_EDITOR", headers)
	if err != nil {
		return nil, err
	}
	if stableID := stableKiroConversationID(account, parsed, anthropicBody, modelID, profileArn); stableID != "" {
		if next, setErr := sjson.SetBytes(buildResult.Payload, "conversationState.conversationId", stableID); setErr == nil {
			buildResult.Payload = next
		}
	}
	return buildResult, nil
}

func (s *GatewayService) retargetKiroPayloadForProfile(account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID string, payload []byte, currentProfileArn, nextProfileArn string) ([]byte, error) {
	if currentProfileArn == nextProfileArn {
		return payload, nil
	}
	var err error
	if nextProfileArn == "" {
		payload, err = sjson.DeleteBytes(payload, "profileArn")
	} else {
		payload, err = sjson.SetBytes(payload, "profileArn", nextProfileArn)
	}
	if err != nil {
		return nil, err
	}
	if stableID := stableKiroConversationID(account, parsed, anthropicBody, modelID, nextProfileArn); stableID != "" {
		payload, err = sjson.SetBytes(payload, "conversationState.conversationId", stableID)
		if err != nil {
			return nil, err
		}
	}
	return payload, nil
}

func stableKiroConversationID(account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, profileArn string) string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("SUB2API_KIRO_CONVERSATION_ID_MODE"))) {
	case "random", "uuid", "off", "false", "0":
		return ""
	}
	seed := stableKiroConversationSeed(account, parsed, anthropicBody, modelID, profileArn)
	if seed == "" {
		return ""
	}
	return generateSessionUUID(seed)
}

func stableKiroConversationSeed(account *Account, parsed *ParsedRequest, anthropicBody []byte, modelID, profileArn string) string {
	var anchorType, anchor string
	if parsed != nil {
		if explicitID := strings.TrimSpace(parsed.ExplicitSessionID); explicitID != "" {
			anchorType, anchor = "explicit", explicitID
		} else if metadataUserID := strings.TrimSpace(parsed.MetadataUserID); metadataUserID != "" {
			anchorType, anchor = "metadata", metadataUserID
		} else if systemText := extractTextFromSystemRaw(parsed.SystemRaw()); systemText != "" {
			anchorType, anchor = "system", systemText
		}
	}
	if anchor == "" && len(anthropicBody) > 0 {
		if systemText := extractTextFromSystemRaw([]byte(gjson.GetBytes(anthropicBody, "system").Raw)); systemText != "" {
			anchorType, anchor = "system", systemText
		} else if firstUserText := extractFirstUserText(anthropicBody); firstUserText != "" {
			anchorType, anchor = "first_user", firstUserText
		}
	}
	if anchor == "" {
		return ""
	}

	var sb strings.Builder
	_, _ = sb.WriteString("kiro-conversation-v1|")
	if account != nil {
		_, _ = sb.WriteString("account:")
		_, _ = sb.WriteString(strconv.FormatInt(account.ID, 10))
		_, _ = sb.WriteString("|credential:")
		_, _ = sb.WriteString(kiroCacheCredentialIdentity(account))
		_, _ = sb.WriteString("|")
	}
	if parsed != nil && parsed.SessionContext != nil {
		_, _ = sb.WriteString("api_key:")
		_, _ = sb.WriteString(strconv.FormatInt(parsed.SessionContext.APIKeyID, 10))
		_, _ = sb.WriteString("|")
	}
	_, _ = sb.WriteString("model:")
	_, _ = sb.WriteString(strings.TrimSpace(modelID))
	_, _ = sb.WriteString("|profile:")
	_, _ = sb.WriteString(strings.TrimSpace(profileArn))
	_, _ = sb.WriteString("|anchor:")
	_, _ = sb.WriteString(anchorType)
	_, _ = sb.WriteString(":")
	_, _ = sb.WriteString(anchor)
	return sb.String()
}

func logKiroStatelessReplay(account *Account, payload []byte) {
	if account == nil {
		return
	}
	conversationID := gjson.GetBytes(payload, "conversationState.conversationId").String()
	systemPrompt := gjson.GetBytes(payload, "conversationState.history.0.userInputMessage.content").String()
	currentContent := gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.content").String()
	logger.L().Info("kiro.stateless_replay",
		zap.Int64("selected_account_id", account.ID),
		zap.Bool("stateless_replay", true),
		zap.Int("history_count", len(gjson.GetBytes(payload, "conversationState.history").Array())),
		zap.Bool("has_agent_continuation_id", gjson.GetBytes(payload, "conversationState.agentContinuationId").Exists()),
		zap.String("conversation_id_hash", hashKiroLogString(conversationID)),
		zap.String("payload_hash_no_conversation_id", hashKiroPayloadWithoutConversationID(payload)),
		zap.String("system_prompt_hash", hashKiroLogString(systemPrompt)),
		zap.Int("system_prompt_len", len(systemPrompt)),
		zap.String("current_content_hash", hashKiroLogString(currentContent)),
		zap.Int("tool_count", len(gjson.GetBytes(payload, "conversationState.currentMessage.userInputMessage.userInputMessageContext.tools").Array())),
	)
}

func hashKiroPayloadWithoutConversationID(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	normalized := payload
	if next, err := sjson.DeleteBytes(payload, "conversationState.conversationId"); err == nil {
		normalized = next
	}
	return strconv.FormatUint(xxhash.Sum64(normalized), 36)
}

func hashKiroLogString(value string) string {
	if value == "" {
		return ""
	}
	return strconv.FormatUint(xxhash.Sum64String(value), 36)
}

func prepareKiroPayloadBodyForRequestModel(anthropicBody []byte, requestModel string) []byte {
	requestModel = strings.TrimSpace(requestModel)
	if requestModel == "" || !strings.Contains(strings.ToLower(requestModel), "thinking") {
		return anthropicBody
	}
	bodyModel := strings.TrimSpace(gjson.GetBytes(anthropicBody, "model").String())
	if bodyModel == "" || strings.EqualFold(bodyModel, requestModel) || strings.Contains(strings.ToLower(bodyModel), "thinking") {
		return anthropicBody
	}
	if next, ok := setJSONValueBytes(anthropicBody, "model", requestModel); ok {
		return next
	}
	return anthropicBody
}

func (s *GatewayService) markKiroAuthTemporarilyUnavailable(ctx context.Context, account *Account, statusCode int, body string) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	until := time.Now().Add(10 * time.Minute)
	reason := fmt.Sprintf("kiro auth failure (%d): %s", statusCode, strings.TrimSpace(body))
	_ = s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason)
}

func (s *GatewayService) markKiroMonthlyRequestCountRateLimited(ctx context.Context, account *Account, body string) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	resetAt := nextKiroMonthlyResetUTC(time.Now())
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
		logger.L().Warn("kiro monthly request count rate-limit failed",
			zap.Int64("account_id", account.ID),
			zap.Time("reset_at", resetAt),
			zap.Error(err),
		)
		return
	}
	reason := "kiro monthly request count exhausted (402): MONTHLY_REQUEST_COUNT"
	if trimmed := strings.TrimSpace(body); trimmed != "" {
		reason = fmt.Sprintf("%s body=%s", reason, truncateForLog([]byte(trimmed), 512))
	}
	logger.L().Warn("kiro monthly request count rate-limited",
		zap.Int64("account_id", account.ID),
		zap.Time("reset_at", resetAt),
		zap.String("reason", reason),
	)
}

func nextKiroMonthlyResetUTC(now time.Time) time.Time {
	utc := now.UTC()
	year, month, _ := utc.Date()
	return time.Date(year, month+1, 1, 0, 0, 0, 0, time.UTC)
}

func resetHTTPResponseBody(resp *http.Response, body []byte) {
	if resp == nil {
		return
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
}

func estimateKiroInputTokens(ctx context.Context, body []byte) int {
	requestModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	return estimateKiroInputTokensForRequest(ctx, body, requestModel, requestModel, nil)
}

func estimateKiroInputTokensForRequest(ctx context.Context, body []byte, mappedModel, requestModel string, headers http.Header) int {
	if len(body) == 0 {
		return 0
	}
	preparedBody := prepareKiroPayloadBodyForRequestModel(body, requestModel)
	modelID := kiropkg.MapModel(mappedModel)
	if tokens, err := estimateKiroClaudeInputTokens(ctx, preparedBody, modelID, "AI_EDITOR", headers); err == nil {
		return tokens
	}
	tokens := len(body) / 4
	if tokens == 0 {
		return 1
	}
	return tokens
}

func resolveKiroInputTokens(ctx context.Context, body []byte, requestCtx kiropkg.KiroRequestContext) int {
	if requestCtx.EstimatedInputTokens > 0 {
		return requestCtx.EstimatedInputTokens
	}
	return estimateKiroInputTokens(ctx, body)
}

func kiroUsageToClaude(usage kiropkg.Usage, fallbackInput int) ClaudeUsage {
	inputTokens := usage.InputTokens
	if inputTokens == 0 && !usage.HasResolvedInputTokens() {
		inputTokens = fallbackInput
	}
	return ClaudeUsage{
		InputTokens:              inputTokens,
		OutputTokens:             usage.OutputTokens,
		CacheReadInputTokens:     usage.CacheReadInputTokens,
		CacheCreationInputTokens: usage.CacheCreationInputTokens,
		CacheCreation5mTokens:    usage.CacheCreation5mInputTokens,
		CacheCreation1hTokens:    usage.CacheCreation1hInputTokens,
		KiroCredits:              usage.KiroCredits,
	}
}

func (s *GatewayService) markKiroInvalidModelRateLimited(ctx context.Context, account *Account, mappedModel string) {
	if s == nil || s.accountRepo == nil || account == nil || account.Type != AccountTypeOAuth {
		return
	}
	// mappedModel is already the account mapping result. Persist it directly so
	// modelRateLimitKeysForRequest resolves the same key without mapping twice,
	// and an invalid model does not evict the account's other KIRO models.
	modelKey := strings.TrimSpace(mappedModel)
	if modelKey == "" {
		return
	}
	resetAt := time.Now().Add(kiroInvalidModelTempUnschedDuration)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, modelKey, resetAt); err != nil {
		logger.L().Warn("kiro invalid model rate-limit failed",
			zap.Int64("account_id", account.ID),
			zap.String("mapped_model", modelKey),
			zap.Time("reset_at", resetAt),
			zap.Error(err),
		)
		return
	}
	logger.L().Warn("kiro invalid model rate-limited",
		zap.Int64("account_id", account.ID),
		zap.String("mapped_model", modelKey),
		zap.Time("reset_at", resetAt),
	)
}

func (s *GatewayService) handleKiroHTTPError(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, mappedModel string, requestBody []byte, stream bool) error {
	respBody, _, _ := s.readKiroUpstreamErrorBody(ctx, resp)
	return s.handleKiroHTTPErrorBody(ctx, resp, c, account, mappedModel, requestBody, respBody, true)
}

func (s *GatewayService) handleKiroHTTPErrorBody(ctx context.Context, resp *http.Response, c *gin.Context, account *Account, mappedModel string, requestBody, respBody []byte, writeClient bool) error {
	upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
	upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)
	if upstreamMsg == "" {
		upstreamMsg = strings.TrimSpace(string(respBody))
	}
	classification := classifyKiroHTTPError(resp.StatusCode, string(respBody))
	if resp.StatusCode == http.StatusBadRequest {
		logKiroBadRequestClassification(classification, account, "", resp.Header, respBody)
	}
	if classification.Category == kiroErrorMonthlyRequest {
		s.markKiroMonthlyRequestCountRateLimited(ctx, account, string(respBody))
	}
	if classification.Category == kiroErrorBadRequestInvalidModel && account != nil && account.Type == AccountTypeOAuth {
		s.markKiroInvalidModelRateLimited(ctx, account, mappedModel)
		event := s.buildKiroInvalidModelUpstreamEvent(account, resp, upstreamMsg, mappedModel, requestBody, c)
		appendOpsUpstreamError(c, event)
		return newProviderHTTPError(account, resp, respBody, false)
	}

	if resp.StatusCode == http.StatusPaymentRequired || s.shouldFailoverUpstreamError(resp.StatusCode) {
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: resp.StatusCode,
			UpstreamRequestID:  buildKiroRequestID(resp),
			Kind:               "failover",
			Message:            upstreamMsg,
		})
		// 429 已经被 executeKiroUpstreamWithParsed → markKiro429 完整处理（Redis 1-5min
		// 指数退避 + DB rate_limit_reset_at 同步）。这里再走 HandleUpstreamError 会进入
		// handle429 → apply429FallbackRateLimit，把 DB cooldown 反写成 5s flat，
		// 直接抹掉我们刚算好的退避时长。所以 429 跳过通用 handler。
		if s.rateLimitService != nil && resp.StatusCode != http.StatusTooManyRequests {
			s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody)
		}
		return newProviderHTTPError(account, resp, respBody, false)
	}

	setOpsUpstreamError(c, resp.StatusCode, upstreamMsg, "")
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform:           account.Platform,
		AccountID:          account.ID,
		AccountName:        account.Name,
		UpstreamStatusCode: resp.StatusCode,
		UpstreamRequestID:  buildKiroRequestID(resp),
		Kind:               "http_error",
		Message:            upstreamMsg,
	})
	// Return a terminal typed failure after writing the provider-specific JSON.
	// The normal handler owns archival; direct WebChat compatibility callers use
	// submitWebChatFinalGatewayErrorCapture at their own terminal boundary. The
	// service must not submit here or native handlers would archive this attempt
	// twice (once here and once in handleFailoverExhausted).
	failure := newTerminalProviderHTTPError(account, resp, respBody)
	if writeClient {
		c.JSON(mapUpstreamStatusCode(resp.StatusCode), gin.H{
			"type": "error",
			"error": gin.H{
				"type":    claudeErrorType(resp.StatusCode),
				"message": coalesceKiroErrorMessage(resp.StatusCode, upstreamMsg),
			},
		})
	}
	return failure
}

func (s *GatewayService) readKiroUpstreamErrorBody(ctx context.Context, resp *http.Response) ([]byte, bool, error) {
	const fallbackLimit = 2 << 20
	var captureCtx captureUpstreamRequestContext
	if ctx != nil {
		captureCtx, _ = ctx.Value(captureUpstreamRequestContextKey{}).(captureUpstreamRequestContext)
	}
	captureActive := captureCtx.c != nil
	readLimit := fallbackLimit
	if captureActive && captureCtx.limit > readLimit {
		readLimit = captureCtx.limit
	}
	if captureActive && captureCtx.limit == 0 {
		readBody, err := readAllWithProviderIdle(ctx, resp.Body, resolveProviderBodyIdleTimeout(s.cfg), func(reader io.Reader) ([]byte, error) {
			return io.ReadAll(reader)
		})
		setCaptureResult(captureCtx.c, resp, readBody, err != nil)
		functionalBody := readBody
		functionalTruncated := err != nil
		if len(functionalBody) > fallbackLimit {
			functionalBody = functionalBody[:fallbackLimit]
			functionalTruncated = true
		}
		return functionalBody, functionalTruncated, err
	}
	readBody, readTruncated, err := readUpstreamBodyWithCeiling(ctx, resp, readLimit, resolveProviderBodyIdleTimeout(s.cfg))
	if captureActive {
		captured, captureTruncated := captureWithLimit(readBody, captureCtx.limit)
		setCaptureResult(captureCtx.c, resp, captured, captureTruncated || readTruncated)
	}
	functionalBody := readBody
	functionalTruncated := readTruncated
	if len(functionalBody) > fallbackLimit {
		functionalBody = functionalBody[:fallbackLimit]
		functionalTruncated = true
	}
	return functionalBody, functionalTruncated, err
}

func claudeErrorType(statusCode int) string {
	switch statusCode {
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusServiceUnavailable:
		return "overloaded_error"
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	default:
		return "api_error"
	}
}

func (s *GatewayService) buildKiroInvalidModelUpstreamEvent(account *Account, resp *http.Response, upstreamMsg, mappedModel string, requestBody []byte, c *gin.Context) OpsUpstreamErrorEvent {
	_ = s
	requestedModel := strings.TrimSpace(gjson.GetBytes(requestBody, "model").String())
	hasTools := gjson.GetBytes(requestBody, "tools").Exists()
	hasAdaptiveThinking := strings.EqualFold(strings.TrimSpace(gjson.GetBytes(requestBody, "thinking.type").String()), "adaptive")
	hasContext1MBeta := false
	if c != nil {
		hasContext1MBeta = strings.Contains(c.GetHeader("Anthropic-Beta"), "context-1m")
	}
	return OpsUpstreamErrorEvent{
		Platform:            account.Platform,
		AccountID:           account.ID,
		AccountName:         account.Name,
		UpstreamStatusCode:  resp.StatusCode,
		UpstreamRequestID:   buildKiroRequestID(resp),
		Kind:                "failover",
		Message:             upstreamMsg,
		RequestedModel:      requestedModel,
		MappedModel:         strings.TrimSpace(mappedModel),
		KiroModelID:         kiropkg.MapModel(mappedModel),
		HasTools:            hasTools,
		HasAdaptiveThinking: hasAdaptiveThinking,
		HasContext1MBeta:    hasContext1MBeta,
	}
}

func logKiroBadRequestClassification(classification kiroErrorClassification, account *Account, model string, headers http.Header, body []byte) {
	if classification.StatusCode != http.StatusBadRequest {
		return
	}
	var accountID int64
	if account != nil {
		accountID = account.ID
	}
	logger.L().Warn("kiro upstream bad request classified",
		zap.String("category", classification.Category),
		zap.Int("status", classification.StatusCode),
		zap.Int64("account_id", accountID),
		zap.String("model", strings.TrimSpace(model)),
		zap.String("request_id", headers.Get("x-request-id")),
		zap.String("body_excerpt", truncateForLog(body, 512)),
	)
}

// dumpKiro429ResponseForDebug installs a transparent first-2KB observer on a
// Kiro 429 response. The functional classifier remains the sole reader, so
// debug sampling can never block waiting for a probe byte or consume provider
// data ahead of the normal bounded response-body path.
func dumpKiro429ResponseForDebug(resp *http.Response, accountID int64, endpointURL, endpointName string) {
	if resp == nil || resp.Body == nil {
		return
	}
	headers := map[string]string{}
	for k, v := range resp.Header {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "ratelimit") || strings.Contains(lk, "retry") || strings.Contains(lk, "reset") ||
			lk == "content-type" || lk == "x-amzn-requestid" || lk == "x-amzn-errortype" {
			headers[k] = strings.Join(v, ",")
		}
	}

	resp.Body = &kiro429DebugReadCloser{
		ReadCloser:   resp.Body,
		accountID:    accountID,
		endpointURL:  endpointURL,
		endpointName: endpointName,
		contentType:  resp.Header.Get("Content-Type"),
		headers:      headers,
	}
}

type kiro429DebugReadCloser struct {
	io.ReadCloser
	mu           sync.Mutex
	readers      sync.WaitGroup
	logOnce      sync.Once
	sample       []byte
	truncated    bool
	accountID    int64
	endpointURL  string
	endpointName string
	contentType  string
	headers      map[string]string
}

func (r *kiro429DebugReadCloser) Read(p []byte) (int, error) {
	r.readers.Add(1)
	defer r.readers.Done()
	n, err := r.ReadCloser.Read(p)
	if n > 0 {
		const maxBytes = 2048
		r.mu.Lock()
		remaining := maxBytes - len(r.sample)
		if remaining > n {
			remaining = n
		}
		if remaining > 0 {
			r.sample = append(r.sample, p[:remaining]...)
		}
		if remaining < n {
			r.truncated = true
		}
		r.mu.Unlock()
	}
	if err != nil {
		r.log(err)
	}
	return n, err
}

func (r *kiro429DebugReadCloser) Close() error {
	if r == nil || r.ReadCloser == nil {
		return nil
	}
	err := r.ReadCloser.Close()
	r.readers.Wait()
	r.log(nil)
	return err
}

func (r *kiro429DebugReadCloser) log(readErr error) {
	if r == nil {
		return
	}
	r.logOnce.Do(func() {
		r.mu.Lock()
		sample := append([]byte(nil), r.sample...)
		truncated := r.truncated
		r.mu.Unlock()
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			logger.L().Warn("kiro.429_debug_read_failed",
				zap.Int64("account_id", r.accountID),
				zap.String("endpoint", r.endpointName),
				zap.Error(readErr),
			)
		}
		logger.L().Warn("kiro.429_raw_response",
			zap.Int64("account_id", r.accountID),
			zap.String("endpoint_url", r.endpointURL),
			zap.String("endpoint_name", r.endpointName),
			zap.String("content_type", r.contentType),
			zap.Any("relevant_headers", r.headers),
			zap.Int("body_bytes", len(sample)),
			zap.Bool("truncated", truncated),
			zap.String("body_sample", string(sample)),
		)
	})
}

func coalesceKiroErrorMessage(statusCode int, upstreamMsg string) string {
	if upstreamMsg != "" {
		return upstreamMsg
	}
	switch statusCode {
	case http.StatusTooManyRequests:
		return "Rate limit exceeded"
	case http.StatusForbidden:
		return "Access denied"
	case http.StatusUnauthorized:
		return "Authentication failed"
	default:
		return "Upstream request failed"
	}
}
