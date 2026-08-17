package tlsfingerprint

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var errTimeoutProbe = errors.New("timeout probe")

func TestFingerprintDialersHaveBoundedConnectTimeout(t *testing.T) {
	profile := &Profile{Name: "timeout-test"}
	httpProxy, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)
	socksProxy, err := url.Parse("socks5h://127.0.0.1:1080")
	require.NoError(t, err)

	direct := NewDialer(profile, nil)
	httpDialer := NewHTTPProxyDialer(profile, httpProxy)
	socksDialer := NewSOCKS5ProxyDialer(profile, socksProxy)

	for name, dialer := range map[string]*net.Dialer{
		"direct":     direct.networkDialer,
		"http_proxy": httpDialer.networkDialer,
		"socks5":     socksDialer.networkDialer,
	} {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, dialer)
			require.Equal(t, defaultConnectTimeout, dialer.Timeout)
			require.Equal(t, defaultConnectKeepAlive, dialer.KeepAlive)
		})
	}
}

func TestFingerprintTLSHandshakeContextIsBounded(t *testing.T) {
	ctx, cancel := withTLSHandshakeTimeout(context.Background())
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	remaining := time.Until(deadline)
	require.Greater(t, remaining, time.Duration(0))
	require.LessOrEqual(t, remaining, defaultTLSHandshakeTimeout)
}

func TestFingerprintTLSHandshakeContextPreservesShorterCallerDeadline(t *testing.T) {
	callerCtx, callerCancel := context.WithTimeout(context.Background(), time.Second)
	defer callerCancel()

	ctx, cancel := withTLSHandshakeTimeout(callerCtx)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	callerDeadline, callerOK := callerCtx.Deadline()
	require.True(t, callerOK)
	require.False(t, deadline.After(callerDeadline))
}

func TestFingerprintDirectDialPassesBoundedContextToCustomBaseDialer(t *testing.T) {
	var observedDeadline time.Time
	dialer := NewDialer(&Profile{Name: "timeout-test"}, func(ctx context.Context, _, _ string) (net.Conn, error) {
		observedDeadline, _ = ctx.Deadline()
		return nil, errTimeoutProbe
	})

	_, err := dialer.DialTLSContext(context.Background(), "tcp", "upstream.example:443")

	require.ErrorIs(t, err, errTimeoutProbe)
	require.False(t, observedDeadline.IsZero(), "custom direct dialer must receive a bounded connect context")
	remaining := time.Until(observedDeadline)
	require.Greater(t, remaining, time.Duration(0))
	require.LessOrEqual(t, remaining, defaultConnectTimeout)
}
