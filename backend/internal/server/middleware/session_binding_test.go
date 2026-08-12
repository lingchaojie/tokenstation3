//go:build unit

package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type sessionBindingSettingRepo struct {
	value string
	err   error
}

func newSessionBindingSettingService(cfg *config.Config, value string, err error) *service.SettingService {
	return service.NewSettingService(&sessionBindingSettingRepo{value: value, err: err}, cfg)
}

func (r *sessionBindingSettingRepo) GetValue(context.Context, string) (string, error) {
	return r.value, r.err
}

func (*sessionBindingSettingRepo) Get(context.Context, string) (*service.Setting, error) {
	panic("unexpected Get call")
}
func (*sessionBindingSettingRepo) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}
func (*sessionBindingSettingRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	panic("unexpected GetMultiple call")
}
func (*sessionBindingSettingRepo) SetMultiple(context.Context, map[string]string) error {
	panic("unexpected SetMultiple call")
}
func (*sessionBindingSettingRepo) GetAll(context.Context) (map[string]string, error) {
	panic("unexpected GetAll call")
}
func (*sessionBindingSettingRepo) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestSessionBindingContextFollowsForwardedIPSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name           string
		trustForwarded bool
		trustedProxies []string
		wantIP         string
	}{
		{name: "enabled switch takes over raw headers", trustForwarded: true, wantIP: "1.2.3.4"},
		{name: "disabled switch ignores untrusted headers", trustForwarded: false, wantIP: "127.0.0.1"},
		{name: "disabled switch uses configured Gin proxy", trustForwarded: false, trustedProxies: []string{"127.0.0.1"}, wantIP: "1.2.3.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.SetTrustForwardedIPForAPIKeyACL(tc.trustForwarded)
			settingService := newSessionBindingSettingService(cfg, "true", nil)

			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(tc.trustedProxies))
			r.Use(SessionBindingContext(cfg, settingService))
			r.GET("/t", func(c *gin.Context) {
				binding := service.SessionBindingFromContext(c.Request.Context())
				require.NotNil(t, binding)
				require.Equal(t, tc.wantIP, binding.IP)
				require.Equal(t, "test-agent", binding.UserAgent)
				require.Equal(t, tc.wantIP, SecurityClientIP(c))
				c.Status(200)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/t", nil)
			req.RemoteAddr = "127.0.0.1:54321"
			req.Header.Set("X-Real-IP", "1.2.3.4")
			req.Header.Set("User-Agent", "test-agent")
			r.ServeHTTP(w, req)

			require.Equal(t, 200, w.Code)
		})
	}
}

func TestSessionBindingContextSnapshotsForwardedModeAndHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Initial-IP"})
	settingService := newSessionBindingSettingService(cfg, "true", nil)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(SessionBindingContext(cfg, settingService))
	r.GET("/t", func(c *gin.Context) {
		binding := service.SessionBindingFromContext(c.Request.Context())
		require.NotNil(t, binding)
		require.Equal(t, "1.2.3.4", binding.IP)

		cfg.SetForwardedClientIPSettings(false, []string{"X-Changed-IP"})
		require.Equal(t, "1.2.3.4", ip.GetSecurityClientIP(c, false))
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Initial-IP", "1.2.3.4")
	req.Header.Set("X-Changed-IP", "4.4.4.4")
	req.Header.Set("X-Real-IP", "8.8.8.8")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Equal(t, []string{"X-Changed-IP"}, runtimeSettings.Headers)
}

func TestSessionBindingContextDoesNotRewriteRequestUserAgentWhenDisabled(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
		err   error
	}{
		{name: "explicit false", value: "false"},
		{name: "missing setting", err: service.ErrSettingNotFound},
		{name: "setting read error", err: errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			settingService := newSessionBindingSettingService(cfg, tc.value, tc.err)
			const originalUserAgent = "  downstream-observed-agent/1.0  "

			r := gin.New()
			r.Use(SessionBindingContext(cfg, settingService))
			r.GET("/t", func(c *gin.Context) {
				require.Nil(t, service.SessionBindingFromContext(c.Request.Context()))
				require.True(t, enforceSessionBinding(c, nil, settingService, nil, &service.JWTClaims{BindingHash: "different"}))
				require.Equal(t, originalUserAgent, c.Request.Header.Get("User-Agent"))
				c.Status(200)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/t", nil)
			req.Header.Set("User-Agent", originalUserAgent)
			r.ServeHTTP(w, req)
			require.Equal(t, 200, w.Code)
		})
	}
}

func TestSessionBindingContextBoundsBindingUserAgentWithoutRewritingHeader(t *testing.T) {
	cfg := &config.Config{}
	settingService := newSessionBindingSettingService(cfg, "true", nil)
	originalUserAgent := strings.Repeat("u", 2048)
	r := gin.New()
	r.Use(SessionBindingContext(cfg, settingService))
	r.GET("/t", func(c *gin.Context) {
		binding := service.SessionBindingFromContext(c.Request.Context())
		expectedBinding := &service.SessionBinding{IP: binding.IP, UserAgent: strings.Repeat("u", 512)}
		require.Equal(t, expectedBinding.UserAgent, binding.UserAgent)
		require.Equal(t, originalUserAgent, c.Request.Header.Get("User-Agent"))
		require.True(t, enforceSessionBinding(c, nil, settingService, nil, &service.JWTClaims{BindingHash: expectedBinding.Hash()}))
		c.Status(200)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.Header.Set("User-Agent", originalUserAgent)
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
}

// 未经过 SessionBindingContext 注入时（异常挂载顺序/单测直调），回退 trusted_proxies 链，
// 等价于开关关闭时的历史行为。
func TestSecurityClientIPFallsBackWithoutInjectedBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		c.String(200, SecurityClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "9.9.9.9", w.Body.String())
}

func TestRequestSessionBindingPrefersInjectedBinding(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cfg := &config.Config{}
	cfg.SetTrustForwardedIPForAPIKeyACL(true)
	settingService := newSessionBindingSettingService(cfg, "true", nil)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	r.Use(SessionBindingContext(cfg, settingService))
	r.GET("/t", func(c *gin.Context) {
		issued := &service.SessionBinding{IP: "1.2.3.4", UserAgent: "test-agent"}
		require.Equal(t, issued.Hash(), requestSessionBinding(c).Hash())
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/t", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("User-Agent", "test-agent")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
}
