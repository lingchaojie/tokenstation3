//go:build unit

package service

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestKiroOverageAuthorityPreservesTypedStreamingFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	until := time.Now().Add(time.Hour)
	repo := &kiroOverageAuthorityRepo{until: &until}
	account := newKiroCacheTransactionAccount(813, "WEBSEARCH-OVERAGE-PARK")
	body := []byte(`{"model":"claude-sonnet-4-6","stream":true,"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search for current events"}]}`)
	parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
	require.NoError(t, err)
	upstream := &queuedHTTPUpstream{}
	transport := &recordingCaptureTransport{}
	svc := &GatewayService{
		accountRepo: repo, httpUpstream: upstream, kiroCooldownStore: &stubKiroCooldownStore{},
		tlsFPProfileService: &TLSFingerprintProfileService{},
		cfg: &config.Config{Gateway: config.GatewayConfig{Capture: config.GatewayCaptureConfig{
			Enabled: true, MaxBodyBytes: 64 << 10, MaxHeaderBytes: 16 << 10,
		}}},
		capturePool: newConversationCapturePoolForTransport(transport, func() bool { return true }),
	}
	t.Cleanup(svc.capturePool.Stop)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
	enableCaptureForTest(t, c)
	SetCaptureRequestedModel(c, parsed.Model)

	result, err := svc.Forward(c.Request.Context(), c, account, parsed)

	require.Nil(t, result)
	var failure *UpstreamFailoverError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, http.StatusPaymentRequired, failure.StatusCode)
	require.True(t, failure.ShouldRetryNextAccount())
	require.False(t, failure.RetryableOnSameAccount)
	require.False(t, failure.HasUpstreamHTTPResponse)
	require.Empty(t, rec.Body.String())
	require.Empty(t, upstream.requests)
	require.Empty(t, transport.Attempts())
}
