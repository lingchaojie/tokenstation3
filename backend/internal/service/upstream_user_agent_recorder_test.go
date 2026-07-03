package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

type upstreamUserAgentIdentityCacheStub struct {
	fp *Fingerprint
}

func (s *upstreamUserAgentIdentityCacheStub) GetFingerprint(ctx context.Context, accountID int64) (*Fingerprint, error) {
	return s.fp, nil
}

func (s *upstreamUserAgentIdentityCacheStub) SetFingerprint(ctx context.Context, accountID int64, fp *Fingerprint) error {
	s.fp = fp
	return nil
}

func (s *upstreamUserAgentIdentityCacheStub) GetMaskedSessionID(ctx context.Context, accountID int64) (string, error) {
	return "", nil
}

func (s *upstreamUserAgentIdentityCacheStub) SetMaskedSessionID(ctx context.Context, accountID int64, sessionID string) error {
	return nil
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

func TestGatewayServiceBuildUpstreamRequestMimicUsesCachedFingerprintUserAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("User-Agent", "opencode/1.14.48 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13")

	repo := &accountUpstreamUserAgentRepoStub{}
	identityCache := &upstreamUserAgentIdentityCacheStub{fp: &Fingerprint{
		ClientID:                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		UserAgent:               "claude-cli/2.1.199 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.94.0",
		StainlessOS:             "Linux",
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v26.3.0",
		UpdatedAt:               time.Now().Unix(),
	}}
	svc := &GatewayService{
		identityService: NewIdentityService(identityCache),
		upstreamUARepo:  repo,
	}
	account := &Account{ID: 14, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"model":"claude-opus-4-8","system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.161.abc; cc_entrypoint=cli;"}],"messages":[]}`)

	req, wireBody, err := svc.buildUpstreamRequest(context.Background(), c, account, body, "oauth-token", "oauth", "claude-opus-4-8", true, true)

	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.199 (external, cli)", getHeaderRaw(req.Header, "User-Agent"))
	require.Equal(t, "x64", getHeaderRaw(req.Header, "X-Stainless-Arch"))
	require.Equal(t, "v26.3.0", getHeaderRaw(req.Header, "X-Stainless-Runtime-Version"))
	require.Contains(t, string(wireBody), "cc_version=2.1.199.abc")
	require.Equal(t, []accountUpstreamUserAgentRecord{{
		accountID: 14,
		userAgent: "claude-cli/2.1.199 (external, cli)",
	}}, repo.records)
}

func TestGatewayServiceBuildCountTokensRequestMimicUsesCachedFingerprintUserAgent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
	c.Request.Header.Set("User-Agent", "opencode/1.14.48 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.13")

	repo := &accountUpstreamUserAgentRepoStub{}
	identityCache := &upstreamUserAgentIdentityCacheStub{fp: &Fingerprint{
		ClientID:                "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		UserAgent:               "claude-cli/2.1.199 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.94.0",
		StainlessOS:             "Linux",
		StainlessArch:           "x64",
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v26.3.0",
		UpdatedAt:               time.Now().Unix(),
	}}
	svc := &GatewayService{
		identityService: NewIdentityService(identityCache),
		upstreamUARepo:  repo,
	}
	account := &Account{ID: 14, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	body := []byte(`{"model":"claude-opus-4-8","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)

	req, _, err := svc.buildCountTokensRequest(context.Background(), c, account, body, "oauth-token", "oauth", "claude-opus-4-8", true)

	require.NoError(t, err)
	require.Equal(t, "claude-cli/2.1.199 (external, cli)", getHeaderRaw(req.Header, "User-Agent"))
	require.Equal(t, "x64", getHeaderRaw(req.Header, "X-Stainless-Arch"))
	require.Equal(t, "v26.3.0", getHeaderRaw(req.Header, "X-Stainless-Runtime-Version"))
	require.Equal(t, []accountUpstreamUserAgentRecord{{
		accountID: 14,
		userAgent: "claude-cli/2.1.199 (external, cli)",
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

	headers, _, _ := svc.buildOpenAIWSHeaders(
		context.Background(),
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
