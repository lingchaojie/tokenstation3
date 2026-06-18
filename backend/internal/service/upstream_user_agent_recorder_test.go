package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountUpstreamUserAgentRepoStub struct {
	records []accountUpstreamUserAgentRecord
}

type accountUpstreamUserAgentRecord struct {
	accountID int64
	userAgent string
}

func (s *accountUpstreamUserAgentRepoStub) Record(ctx context.Context, accountID int64, userAgent string) error {
	s.records = append(s.records, accountUpstreamUserAgentRecord{
		accountID: accountID,
		userAgent: userAgent,
	})
	return nil
}

func (s *accountUpstreamUserAgentRepoStub) ListByAccountID(ctx context.Context, accountID int64, limit int) ([]AccountUpstreamUserAgent, error) {
	return nil, nil
}

func TestGatewayServiceRecordUpstreamUserAgentUsesFinalWireHeader(t *testing.T) {
	repo := &accountUpstreamUserAgentRepoStub{}
	svc := &GatewayService{upstreamUARepo: repo}
	account := &Account{ID: 42, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	req := httptest.NewRequest("POST", "https://api.anthropic.com/v1/messages", nil)
	setHeaderRaw(req.Header, "User-Agent", "claude-cli/2.1.162 (external, cli)")

	svc.recordUpstreamUserAgent(context.Background(), account, req)

	require.Equal(t, []accountUpstreamUserAgentRecord{{
		accountID: 42,
		userAgent: "claude-cli/2.1.162 (external, cli)",
	}}, repo.records)
}

func TestOpenAIGatewayServiceRecordUpstreamUserAgentUsesFinalWireHeader(t *testing.T) {
	repo := &accountUpstreamUserAgentRepoStub{}
	svc := &OpenAIGatewayService{upstreamUARepo: repo}
	account := &Account{ID: 77, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	req := httptest.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	req.Header.Set("user-agent", "codex_cli_rs/0.125.0 (Ubuntu 22.4.0; x86_64) xterm-256color")

	svc.recordUpstreamUserAgent(context.Background(), account, req)

	require.Equal(t, []accountUpstreamUserAgentRecord{{
		accountID: 77,
		userAgent: "codex_cli_rs/0.125.0 (Ubuntu 22.4.0; x86_64) xterm-256color",
	}}, repo.records)
}

func TestOpenAIGatewayServiceBuildOpenAIWSHeadersRecordsFinalFallbackUserAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "Mozilla/5.0")

	repo := &accountUpstreamUserAgentRepoStub{}
	svc := &OpenAIGatewayService{upstreamUARepo: repo}
	account := &Account{ID: 88, Platform: PlatformOpenAI, Type: AccountTypeOAuth}

	headers, _ := svc.buildOpenAIWSHeaders(
		c,
		account,
		"token",
		OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
		false,
		"",
		"",
		"",
	)

	require.Equal(t, codexCLIUserAgent, headers.Get("user-agent"))
	require.Equal(t, []accountUpstreamUserAgentRecord{{
		accountID: 88,
		userAgent: codexCLIUserAgent,
	}}, repo.records)
}
