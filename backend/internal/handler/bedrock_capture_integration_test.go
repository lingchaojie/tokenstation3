//go:build unit

package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bedrockHandlerProviderResponse struct {
	status      int
	contentType string
	body        []byte
	requestID   string
}

type bedrockHandlerProvider struct {
	mu        sync.Mutex
	calls     []int64
	requests  map[int64][][]byte
	responses map[int64]bedrockHandlerProviderResponse
}

func (u *bedrockHandlerProvider) response(req *http.Request, accountID int64) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	u.mu.Lock()
	if u.requests == nil {
		u.requests = make(map[int64][][]byte)
	}
	u.calls = append(u.calls, accountID)
	u.requests[accountID] = append(u.requests[accountID], append([]byte(nil), body...))
	provider := u.responses[accountID]
	u.mu.Unlock()
	status := provider.status
	if status == 0 {
		status = http.StatusOK
	}
	contentType := provider.contentType
	if contentType == "" {
		contentType = "application/json"
	}
	requestID := provider.requestID
	if requestID == "" {
		requestID = "bedrock-account-" + strconv.FormatInt(accountID, 10)
	}
	return &http.Response{
		StatusCode: status,
		Header: http.Header{
			"Content-Type":     {contentType},
			"X-Amzn-Requestid": {requestID},
			"X-Provider-Debug": {"bedrock-native"},
		},
		Body: io.NopCloser(bytes.NewReader(provider.body)), Request: req,
	}, nil
}

func (u *bedrockHandlerProvider) Do(req *http.Request, _ string, accountID int64, _ int) (*http.Response, error) {
	return u.response(req, accountID)
}

func (u *bedrockHandlerProvider) DoWithTLS(req *http.Request, _ string, accountID int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.response(req, accountID)
}

func (u *bedrockHandlerProvider) snapshot() ([]int64, map[int64][][]byte) {
	u.mu.Lock()
	defer u.mu.Unlock()
	calls := append([]int64(nil), u.calls...)
	requests := make(map[int64][][]byte, len(u.requests))
	for accountID, bodies := range u.requests {
		for _, body := range bodies {
			requests[accountID] = append(requests[accountID], append([]byte(nil), body...))
		}
	}
	return calls, requests
}

func buildBedrockHandlerChunk(t *testing.T, event map[string]any) []byte {
	t.Helper()
	eventJSON, err := json.Marshal(event)
	require.NoError(t, err)
	return buildBedrockHandlerRawChunk(t, eventJSON)
}

func buildBedrockHandlerRawChunk(t *testing.T, eventJSON []byte) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]string{"bytes": base64.StdEncoding.EncodeToString(eventJSON)})
	require.NoError(t, err)
	return buildBedrockHandlerEnvelopeChunk(t, payload)
}

func buildBedrockHandlerEnvelopeChunk(t *testing.T, payload []byte) []byte {
	t.Helper()

	var headers bytes.Buffer
	for name, value := range map[string]string{":event-type": "chunk", ":message-type": "event"} {
		require.NoError(t, headers.WriteByte(byte(len(name))))
		_, _ = headers.WriteString(name)
		require.NoError(t, headers.WriteByte(7))
		require.NoError(t, binary.Write(&headers, binary.BigEndian, uint16(len(value))))
		_, _ = headers.WriteString(value)
	}
	totalLength := uint32(12 + headers.Len() + len(payload) + 4)
	prelude := make([]byte, 8)
	binary.BigEndian.PutUint32(prelude[0:4], totalLength)
	binary.BigEndian.PutUint32(prelude[4:8], uint32(headers.Len()))

	var frame bytes.Buffer
	_, _ = frame.Write(prelude)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(prelude)))
	_, _ = frame.Write(headers.Bytes())
	_, _ = frame.Write(payload)
	require.NoError(t, binary.Write(&frame, binary.BigEndian, crc32.ChecksumIEEE(frame.Bytes())))
	return frame.Bytes()
}

func bedrockHandlerStream(t *testing.T, events ...map[string]any) []byte {
	t.Helper()
	var stream bytes.Buffer
	for _, event := range events {
		_, _ = stream.Write(buildBedrockHandlerChunk(t, event))
	}
	return stream.Bytes()
}

type bedrockHandlerRun struct {
	recorder *httptest.ResponseRecorder
	captures []*service.CaptureRecord
	usages   []*service.UsageLog
}

func runBedrockMessagesHandler(
	t *testing.T,
	groupID, userID int64,
	accounts []*service.Account,
	upstream *bedrockHandlerProvider,
	requestBody string,
	terminalOnly bool,
) bedrockHandlerRun {
	t.Helper()
	group := &service.Group{ID: groupID, Hydrated: true, Platform: service.PlatformAnthropic, Status: service.StatusActive, RateMultiplier: 1}
	for _, account := range accounts {
		account.AccountGroups = []service.AccountGroup{{AccountID: account.ID, GroupID: groupID}}
	}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	cfg.Security.URLAllowlist.Enabled = false
	cfg.Gateway.MaxAccountSwitches = len(accounts)
	cfg.Gateway.Capture.Enabled = true
	cfg.Gateway.Capture.MaxBodyBytes = 1 << 20
	settings := newEnabledCaptureSettingService(t, cfg)
	if terminalOnly {
		settings = newTerminalOnlyCaptureSettingService(t, cfg)
	}
	scheduler := service.NewSchedulerSnapshotService(&fakeSchedulerCache{accounts: accounts}, nil, nil, nil, nil)
	billingCache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(billingCache.Stop)
	captureRecords := make(chan *service.CaptureRecord, 4)
	capturePool := service.NewConversationCapturePoolForUnitTest(captureRecords)
	usageRepo := &gatewayAnthropicUsageRepo{}
	gateway := service.NewGatewayService(
		&antigravityCaptureAccountRepo{}, &fakeGroupRepo{group: group}, usageRepo, nil, nil, nil, nil, nil, cfg, scheduler, nil,
		service.NewBillingService(cfg, nil), nil, billingCache, nil, upstream, &service.DeferredService{},
		nil, nil, nil, nil, nil, nil, settings, &service.TLSFingerprintProfileService{}, nil, nil, nil, nil, capturePool,
	)
	handler := NewGatewayHandler(
		gateway, nil, nil, nil, nil, service.NewConcurrencyService(&fakeConcurrencyCache{}), billingCache, nil,
		service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg, settings, capturePool,
	)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, EndpointMessages, bytes.NewBufferString(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{
		ID: groupID + 90, UserID: userID, GroupID: &groupID, Status: service.StatusActive,
		Group: group, User: &service.User{ID: userID, Status: service.StatusActive, Concurrency: 10, Balance: 100},
	})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: userID, Concurrency: 10})
	handler.Messages(c)
	capturePool.Stop()

	var captures []*service.CaptureRecord
	for len(captureRecords) > 0 {
		captures = append(captures, <-captureRecords)
	}
	return bedrockHandlerRun{recorder: recorder, captures: captures, usages: usageRepo.snapshot()}
}

func newBedrockHandlerAccount(id int64, priority int) *service.Account {
	return &service.Account{
		ID: id, Name: "bedrock-handler", Platform: service.PlatformAnthropic, Type: service.AccountTypeBedrock,
		Status: service.StatusActive, Schedulable: true, Concurrency: 1, Priority: priority,
		Credentials: map[string]any{
			"auth_mode": "apikey", "api_key": "bedrock-provider-secret", "aws_region": "us-east-1",
			"model_mapping": map[string]any{"claude-sonnet-4-6": "anthropic.claude-3-5-sonnet-20240620-v1:0"},
		},
	}
}

func TestBedrockMessagesHandlerFinalHTTPErrorCapturesNativeExchangeExactlyOnce(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const groupID, userID, accountID = int64(9930), int64(9931), int64(9932)
	errorBody := []byte(`{"message":"bedrock rejected final request","type":"invalid_request"}`)
	upstream := &bedrockHandlerProvider{responses: map[int64]bedrockHandlerProviderResponse{
		accountID: {status: http.StatusBadRequest, body: errorBody, requestID: "bedrock-terminal-request-id"},
	}}
	inbound := `{"model":"claude-sonnet-4-6","stream":false,"max_tokens":64,"messages":[{"role":"user","content":"hello"}]}`
	got := runBedrockMessagesHandler(t, groupID, userID,
		[]*service.Account{newBedrockHandlerAccount(accountID, 1)}, upstream, inbound, true)

	_, requests := upstream.snapshot()
	require.Equal(t, http.StatusBadRequest, got.recorder.Code)
	require.Len(t, got.captures, 1)
	record := got.captures[0]
	require.Equal(t, service.PlatformAnthropic, record.Platform)
	require.Equal(t, http.StatusBadRequest, record.HTTPStatus)
	require.Equal(t, "bedrock-terminal-request-id", record.RequestID)
	require.Equal(t, errorBody, record.RawResponse)
	require.Equal(t, requests[accountID][len(requests[accountID])-1], record.RawRequest)
	require.NotEqual(t, []byte(inbound), record.RawRequest)
	require.Contains(t, string(record.RawRequest), "anthropic_version")
	require.Contains(t, string(record.ResponseHeaders), "X-Amzn-Requestid")
	require.Contains(t, string(record.ResponseHeaders), "X-Provider-Debug")
	require.NotContains(t, string(record.ResponseHeaders), "X-Request-Id")
	require.NotContains(t, string(record.RequestHeaders), "bedrock-provider-secret")
	require.Empty(t, got.usages)
}
