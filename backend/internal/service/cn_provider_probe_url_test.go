package service

// CN 供应商探测端点 URL 安全策略回归测试（review B4）：
// 配额/余额探测不得绕过 security.url_allowlist——base_url 衍生的探测端点
// 必须先过运营者策略，被拒绝时不得发起任何上游请求（API key 不出站）。

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func cnProbeAllowlistConfig(hosts ...string) *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:       true,
				UpstreamHosts: hosts,
			},
		},
	}
}

func TestCNValidateProbeURL_AllowlistPolicy(t *testing.T) {
	cfg := cnProbeAllowlistConfig("api.moonshot.cn", "api.deepseek.com")

	// 白名单内主机放行（保留完整路径）。
	ok, err := cnValidateProbeURL(cfg, "https://api.moonshot.cn/v1/users/me/balance")
	require.NoError(t, err)
	require.Equal(t, "https://api.moonshot.cn/v1/users/me/balance", ok)

	// 白名单外主机拒绝。
	_, err = cnValidateProbeURL(cfg, "https://relay.attacker.example/v1/usages")
	require.Error(t, err)
	require.Contains(t, err.Error(), "rejected by URL security policy")

	// 私网主机拒绝（内网探测面）。
	_, err = cnValidateProbeURL(cfg, "http://169.254.169.254/latest/meta-data")
	require.Error(t, err)

	// 白名单关闭：仅格式校验，任意 https 主机放行。
	formatOnly, err := cnValidateProbeURL(&config.Config{
		Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}},
	}, "https://relay.attacker.example/v1/usages")
	require.NoError(t, err)
	require.Equal(t, "https://relay.attacker.example/v1/usages", formatOnly)
}

// recordingHTTPUpstream 断言探测被策略拒绝时没有任何上游请求发出。
type recordingHTTPUpstream struct{ calls int }

func (u *recordingHTTPUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.calls++
	return nil, context.DeadlineExceeded
}

func (u *recordingHTTPUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls++
	return nil, context.DeadlineExceeded
}

type fakeCNProbeAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *fakeCNProbeAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	return r.account, nil
}

func TestCNProviderQuotaService_RejectsURLBlockedByPolicy(t *testing.T) {
	repo := &fakeCNProbeAccountRepo{account: &Account{
		ID: 1, Platform: PlatformKimi, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{
			"account_mode": "coding",
			"api_key":      "sk-test",
			"base_url":     "https://api.kimi.ai/coding/v1",
		},
	}}
	upstream := &recordingHTTPUpstream{}
	svc := NewCNProviderQuotaService(repo, nil, upstream, cnProbeAllowlistConfig("api.kimi.com"))

	_, err := svc.QueryUsage(context.Background(), 1)
	require.Error(t, err)
	require.Contains(t, err.Error(), "CN_QUOTA_URL_REJECTED")
	require.Zero(t, upstream.calls, "probe must not issue any upstream request when URL policy rejects the target")
}

func TestCNProviderQuotaService_RejectsSpoofedKimiURL(t *testing.T) {
	repo := &fakeCNProbeAccountRepo{account: &Account{
		ID: 1, Platform: PlatformKimi, Type: AccountTypeAPIKey,
		Credentials: map[string]any{
			"account_mode": AccountModeCoding,
			"api_key":      "test-only-placeholder",
			"base_url":     "https://relay.attacker.example/api.kimi.com/coding",
		},
	}}
	upstream := &recordingHTTPUpstream{}
	svc := NewCNProviderQuotaService(repo, nil, upstream, nil)
	_, err := svc.QueryUsage(context.Background(), 1)
	require.ErrorContains(t, err, "CN_QUOTA_NOT_CODING_PLAN")
	require.Zero(t, upstream.calls)
}

type cnQuotaTestRepo struct {
	fakeCNProbeAccountRepo
	updates map[string]any
}

func (r *cnQuotaTestRepo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.updates = updates
	return nil
}

type cnQuotaTestUpstream struct {
	recordingHTTPUpstream
	request      *http.Request
	responseBody string
}

func (u *cnQuotaTestUpstream) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	u.calls++
	u.request = req
	body := u.responseBody
	if body == "" {
		body = `{"limits":[{"detail":{"limit":100,"remaining":75}}],"usage":{"limit":1000,"remaining":800}}`
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestCNProviderQuotaService_KimiKeepsMalformedReportedWindows(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(24 * time.Hour).Format(time.RFC3339)
	cases := []struct {
		name        string
		body        string
		usedKey     string
		resetKey    string
		shouldPause bool
	}{
		{
			name:        "malformed legacy weekly keeps the old limit",
			body:        `{"limits":[{"detail":{"limit":100,"remaining":75}}],"usage":{"limit":"invalid","remaining":0}}`,
			usedKey:     "kimi_weekly_used_percent",
			resetKey:    "kimi_weekly_reset_at",
			shouldPause: true,
		},
		{
			name:        "malformed new weekly keeps the old limit",
			body:        `{"limits":[{"detail":{"limit":100,"remaining":75}}],"usages":{"limit_7d":{"used_ratio":"invalid"}}}`,
			usedKey:     "kimi_weekly_used_percent",
			resetKey:    "kimi_weekly_reset_at",
			shouldPause: true,
		},
		{
			name:        "malformed monthly code keeps the old shared limit",
			body:        `{"limits":[{"detail":{"limit":100,"remaining":75}}],"usages":{"limit_month_total":{"used_ratio":0.25},"limit_month_code":{"used_ratio":"invalid"}}}`,
			usedKey:     "kimi_monthly_used_percent",
			resetKey:    "kimi_monthly_reset_at",
			shouldPause: true,
		},
		{
			name:     "absent weekly after plan change clears the old limit",
			body:     `{"limits":[{"detail":{"limit":100,"remaining":75}}],"usages":{"limit_month_total":{"used_ratio":0.25}}}`,
			usedKey:  "kimi_weekly_used_percent",
			resetKey: "kimi_weekly_reset_at",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			account := &Account{ID: 1, Platform: PlatformKimi, Type: AccountTypeAPIKey,
				Credentials: map[string]any{
					"account_mode": AccountModeCoding,
					"api_key":      "test-only-placeholder",
					"base_url":     "https://api.kimi.com/coding/v1",
				},
				Extra: map[string]any{tc.usedKey: 100.0, tc.resetKey: reset},
			}
			repo := &cnQuotaTestRepo{fakeCNProbeAccountRepo: fakeCNProbeAccountRepo{account: account}}
			upstream := &cnQuotaTestUpstream{responseBody: tc.body}
			svc := NewCNProviderQuotaService(repo, nil, upstream, cnProbeAllowlistConfig("api.kimi.com"))

			result, err := svc.QueryUsage(context.Background(), account.ID)
			require.NoError(t, err)
			require.True(t, result.Persisted)
			require.Equal(t, 25.0, repo.updates["kimi_5h_used_percent"])
			account.Extra = maps.Clone(account.Extra)
			maps.Copy(account.Extra, repo.updates)
			decision := EvaluateAccountSchedulingThreshold(account, map[string]int{PlatformKimi: 90}, now)
			require.Equal(t, tc.shouldPause, decision.ShouldPause)
			if tc.shouldPause {
				require.Equal(t, 100.0, account.Extra[tc.usedKey])
				require.Equal(t, reset, account.Extra[tc.resetKey])
			} else {
				require.Nil(t, account.Extra[tc.usedKey])
				require.Nil(t, account.Extra[tc.resetKey])
			}
		})
	}
}

func TestCNProviderQuotaService_KeepMalformedWeeklySnapshots(t *testing.T) {
	now := time.Now().UTC()
	reset := now.Add(24 * time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		name, provider, host, baseURL, mode string
		malformed, absent                   string
	}{
		{
			name: "zhipu token plan", provider: PlatformZhipu, host: "open.bigmodel.cn",
			baseURL: "https://open.bigmodel.cn/api/coding/paas/v4", mode: AccountModeCoding,
			malformed: `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":25},{"type":"TOKENS_LIMIT","unit":6,"percentage":"invalid"}]}}`,
			absent:    `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":25},{"type":"CREDIT_LIMIT","unit":6,"percentage":0}]}}`,
		},
		{
			name: "zhipu credit plan", provider: PlatformZhipu, host: "open.bigmodel.cn",
			baseURL: "https://open.bigmodel.cn/api/coding/paas/v4", mode: AccountModeCoding,
			malformed: `{"success":true,"data":{"limits":[{"type":"CREDIT_LIMIT","unit":3,"percentage":25},{"type":"CREDIT_LIMIT","unit":6,"percentage":"invalid"}]}}`,
			absent:    `{"success":true,"data":{"limits":[{"type":"CREDIT_LIMIT","unit":3,"percentage":25}]}}`,
		},
		{
			name: "minimax", provider: PlatformMiniMax, host: "api.minimax.io",
			baseURL: "https://api.minimax.io/v1", mode: AccountModeCoding,
			malformed: `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":75,"current_weekly_status":1,"current_weekly_remaining_percent":"invalid"}]}`,
			absent:    `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":75,"current_weekly_status":0,"current_weekly_remaining_percent":0}]}`,
		},
		{
			name: "opencode go", provider: PlatformOpenCodeGo, host: "opencode.ai",
			baseURL: DefaultOpenCodeGoBaseURL, mode: AccountModeGo,
			malformed: `{"usage":{"rolling":{"percent":25},"weekly":{"percent":"invalid"}}}`,
			absent:    `{"usage":{"rolling":{"percent":25}}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usedKey := cnExtraKey(tc.provider, cnExtraSuffixWeeklyUsed)
			resetKey := cnExtraKey(tc.provider, cnExtraSuffixWeeklyReset)
			account := &Account{
				ID: 1, Platform: tc.provider, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-only-placeholder", "account_mode": tc.mode, "base_url": tc.baseURL},
				Extra:       map[string]any{usedKey: 100.0, resetKey: reset},
			}
			repo := &cnQuotaTestRepo{fakeCNProbeAccountRepo: fakeCNProbeAccountRepo{account: account}}
			upstream := &cnQuotaTestUpstream{responseBody: tc.malformed}
			svc := NewCNProviderQuotaService(repo, nil, upstream, cnProbeAllowlistConfig(tc.host))
			applyProbe := func() {
				t.Helper()
				result, err := svc.QueryUsage(context.Background(), account.ID)
				require.NoError(t, err)
				require.True(t, result.Persisted)
				require.Equal(t, 25.0, repo.updates[cnExtraKey(tc.provider, cnExtraSuffix5hUsed)])
				maps.Copy(account.Extra, repo.updates)
			}
			limits := map[string]int{tc.provider: 90}
			applyProbe()
			require.True(t, EvaluateAccountSchedulingThreshold(account, limits, now).ShouldPause)
			require.Equal(t, 100.0, account.Extra[usedKey])
			require.Equal(t, reset, account.Extra[resetKey])

			upstream.responseBody = tc.absent
			applyProbe()
			require.False(t, EvaluateAccountSchedulingThreshold(account, limits, now).ShouldPause)
			require.Nil(t, account.Extra[usedKey])
			require.Nil(t, account.Extra[resetKey])
		})
	}
}

func TestCNProviderQuotaService_KeepMalformedWeeklyReset(t *testing.T) {
	now := time.Now().UTC()
	oldReset := now.Add(24 * time.Hour).Format(time.RFC3339)
	newReset := now.Add(48 * time.Hour).Format(time.RFC3339)
	for _, tc := range []struct {
		name, provider, host, baseURL, mode string
		resetField, payload                 string
	}{
		{"kimi", PlatformKimi, "api.kimi.com", "https://api.kimi.com/coding/v1", AccountModeCoding,
			"reset_time", `{"usages":{"limit_5h":{"used_ratio":0.25},"limit_7d":{"used_ratio":1%s}}}`},
		{"kimi legacy", PlatformKimi, "api.kimi.com", "https://api.kimi.com/coding/v1", AccountModeCoding,
			"resetTime", `{"limits":[{"detail":{"limit":100,"remaining":75}}],"usage":{"limit":100,"remaining":0%s}}`},
		{"zhipu", PlatformZhipu, "open.bigmodel.cn", "https://open.bigmodel.cn/api/coding/paas/v4", AccountModeCoding,
			"nextResetTime", `{"success":true,"data":{"limits":[{"type":"TOKENS_LIMIT","unit":3,"percentage":25},{"type":"TOKENS_LIMIT","unit":6,"percentage":100%s}]}}`},
		{"minimax", PlatformMiniMax, "api.minimax.io", "https://api.minimax.io/v1", AccountModeCoding,
			"weekly_end_time", `{"model_remains":[{"model_name":"general","current_interval_remaining_percent":75,"current_weekly_status":1,"current_weekly_remaining_percent":0%s}]}`},
		{"opencode go", PlatformOpenCodeGo, "opencode.ai", DefaultOpenCodeGoBaseURL, AccountModeGo,
			"resetsAt", `{"usage":{"rolling":{"percent":25},"weekly":{"percent":100%s}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usedKey := cnExtraKey(tc.provider, cnExtraSuffixWeeklyUsed)
			resetKey := cnExtraKey(tc.provider, cnExtraSuffixWeeklyReset)
			account := &Account{
				ID: 1, Platform: tc.provider, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test-only-placeholder", "account_mode": tc.mode, "base_url": tc.baseURL},
				Extra:       map[string]any{usedKey: 95.0, resetKey: oldReset},
			}
			repo := &cnQuotaTestRepo{fakeCNProbeAccountRepo: fakeCNProbeAccountRepo{account: account}}
			upstream := &cnQuotaTestUpstream{}
			svc := NewCNProviderQuotaService(repo, nil, upstream, cnProbeAllowlistConfig(tc.host))
			applyProbe := func(resetMember string) {
				t.Helper()
				upstream.responseBody = fmt.Sprintf(tc.payload, resetMember)
				result, err := svc.QueryUsage(context.Background(), account.ID)
				require.NoError(t, err)
				require.True(t, result.Persisted)
				maps.Copy(account.Extra, repo.updates)
				require.Equal(t, 100.0, account.Extra[usedKey])
			}
			limits := map[string]int{tc.provider: 90}
			applyProbe(fmt.Sprintf(`,%q:%q`, tc.resetField, "invalid"))
			require.Equal(t, oldReset, account.Extra[resetKey])
			require.True(t, EvaluateAccountSchedulingThreshold(account, limits, now).ShouldPause)

			applyProbe(fmt.Sprintf(`,%q:%q`, tc.resetField, newReset))
			require.Equal(t, newReset, account.Extra[resetKey])

			applyProbe("")
			require.Nil(t, account.Extra[resetKey])
			require.False(t, EvaluateAccountSchedulingThreshold(account, limits, now).ShouldPause)
		})
	}
}

func TestCNProviderQuotaService_KimiOfficialHosts(t *testing.T) {
	for _, host := range []string{"api.kimi.com", "api.kimi.ai"} {
		for _, path := range []string{"/coding", "/coding/", "/coding/v1", "/coding/v1/"} {
			t.Run(host+path, func(t *testing.T) {
				repo := &cnQuotaTestRepo{fakeCNProbeAccountRepo: fakeCNProbeAccountRepo{account: &Account{
					ID: 1, Platform: PlatformKimi, Type: AccountTypeAPIKey,
					Credentials: map[string]any{
						"account_mode": AccountModeCoding,
						"api_key":      "test-only-placeholder",
						"base_url":     "https://" + host + path,
					},
				}}}
				upstream := &cnQuotaTestUpstream{}
				svc := NewCNProviderQuotaService(repo, nil, upstream, cnProbeAllowlistConfig(host))
				result, err := svc.QueryUsage(context.Background(), 1)
				require.NoError(t, err)
				require.True(t, result.Success)
				require.True(t, result.CredentialValid)
				require.True(t, result.Persisted)
				require.Equal(t, PlatformKimi, result.Provider)
				require.Len(t, result.Tiers, 2)
				require.Equal(t, float64(25), result.Tiers[0].UsedPercent)
				require.Equal(t, float64(20), result.Tiers[1].UsedPercent)
				require.Equal(t, float64(25), repo.updates["kimi_5h_used_percent"])
				require.Equal(t, 1, upstream.calls)
				require.Equal(t, http.MethodGet, upstream.request.Method)
				require.Equal(t, "https://"+host+"/coding/v1/usages", upstream.request.URL.String())
				require.Equal(t, "Bearer test-only-placeholder", upstream.request.Header.Get("Authorization"))
			})
		}
	}
}

// deepseek payg 账号自定义 base_url → 余额端点落在中转主机上，必须先过策略。
func TestCNProviderBalanceService_RejectsURLBlockedByPolicy(t *testing.T) {
	repo := &fakeCNProbeAccountRepo{account: &Account{
		ID: 2, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{
			"account_mode": "payg",
			"api_key":      "sk-test",
			"base_url":     "https://relay.attacker.example",
		},
	}}
	upstream := &recordingHTTPUpstream{}
	svc := NewCNProviderBalanceService(repo, nil, upstream, cnProbeAllowlistConfig("api.deepseek.com"))

	_, err := svc.QueryBalance(context.Background(), 2)
	require.Error(t, err)
	require.Contains(t, err.Error(), "CN_BALANCE_URL_REJECTED")
	require.Zero(t, upstream.calls, "probe must not issue any upstream request when URL policy rejects the target")
}

// 白名单包含官方主机的正常路径：URL 校验通过后才发出上游请求（此处允许到达
// httpUpstream 层即视为通过校验，不发真实网络）。
func TestCNProviderBalanceService_OfficialHostPassesValidation(t *testing.T) {
	repo := &fakeCNProbeAccountRepo{account: &Account{
		ID: 3, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{
			"account_mode": "payg",
			"api_key":      "sk-test",
		},
	}}
	upstream := &recordingHTTPUpstream{}
	svc := NewCNProviderBalanceService(repo, nil, upstream, cnProbeAllowlistConfig("api.deepseek.com"))

	_, _ = svc.QueryBalance(context.Background(), 3)
	require.Equal(t, 1, upstream.calls, "official host must pass URL policy and reach the upstream layer")
}
