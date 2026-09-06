package repository

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/util/urlvalidator"
)

// publicHostsOnlyTransport is confined to opt-in public downloads. Each hop
// owns its transport, so it cannot reuse an unvalidated API/proxy connection or
// mutate the cached client's routing/TLS policy. The URL stays unchanged for
// Host, SNI, certificate verification and relative redirects.
type publicHostsOnlyTransport struct {
	base http.RoundTripper
}

func (t *publicHostsOnlyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	transport, ok := base.(*http.Transport)
	//nolint:staticcheck // Reject legacy custom TLS dialers too: they can bypass IP pinning.
	if !ok || transport.DialTLS != nil || transport.DialTLSContext != nil {
		return nil, errors.New("public download transport cannot safely pin its destination")
	}
	if req.URL == nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return nil, errors.New("public download requires an HTTP or HTTPS URL")
	}
	var proxyURL *url.URL
	var err error
	if transport.Proxy != nil {
		proxyURL, err = transport.Proxy(req)
		if err != nil {
			return nil, err
		}
	}
	if proxyURL != nil && proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		return nil, errors.New("public download proxy cannot safely pin its destination")
	}
	clone := transport.Clone()
	clone.Proxy = nil
	clone.DisableKeepAlives = true
	defer clone.CloseIdleConnections()
	if clone.TLSClientConfig != nil {
		clone.TLSClientConfig = clone.TLSClientConfig.Clone()
		clone.TLSClientConfig.ServerName = req.URL.Hostname()
		clone.TLSClientConfig.InsecureSkipVerify = false
	}
	dial := transport.DialContext
	if dial == nil {
		dial = newUpstreamDialer().DialContext
	}
	clone.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		ctx, cancel := context.WithTimeout(ctx, defaultUpstreamDialTimeout)
		defer cancel()
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if urlvalidator.IsBlockedHost(host) {
			return nil, errors.New("public download host is not allowed")
		}
		lookupCtx, lookupCancel := context.WithTimeout(ctx, 5*time.Second)
		ips, err := net.DefaultResolver.LookupIP(lookupCtx, "ip", host)
		lookupCancel()
		if err != nil {
			return nil, fmt.Errorf("public download DNS resolution failed: %w", err)
		}
		if len(ips) == 0 {
			return nil, errors.New("public download DNS returned no addresses")
		}
		// Validate the complete answer before making any connection. Never pass
		// the hostname to a second resolver (including the account's SOCKS proxy).
		for _, ip := range ips {
			if !ip.IsGlobalUnicast() || urlvalidator.IsBlockedHost(ip.String()) {
				return nil, errors.New("public download resolved IP is not allowed")
			}
		}
		for _, ip := range ips {
			target := net.JoinHostPort(ip.String(), port)
			var conn net.Conn
			if proxyURL == nil {
				conn, err = dial(ctx, network, target)
			} else {
				conn, err = dialPublicDownloadTunnel(ctx, proxyURL, target, transport.TLSClientConfig)
			}
			if err == nil {
				return conn, nil
			}
			if ctx.Err() != nil {
				break
			}
		}
		return nil, err
	}
	return clone.RoundTrip(req)
}

// Both HTTP and HTTPS destinations use CONNECT: an ordinary HTTP proxy GET
// would put the original Host back into the absolute URI and resolve it again.
// A proxy refusing an IP tunnel fails closed; we never retry without the proxy.
func dialPublicDownloadTunnel(ctx context.Context, proxyURL *url.URL, target string, tlsConfig *tls.Config) (net.Conn, error) {
	port := proxyURL.Port()
	if port == "" {
		port = "80"
		if proxyURL.Scheme == "https" {
			port = "443"
		}
	}
	conn, err := newUpstreamDialer().DialContext(ctx, "tcp", net.JoinHostPort(proxyURL.Hostname(), port))
	if err != nil {
		return nil, err
	}
	success := false
	defer func() {
		if !success {
			_ = conn.Close()
		}
	}()
	rawConn := conn
	stop := context.AfterFunc(ctx, func() { _ = rawConn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	if proxyURL.Scheme == "https" {
		cfg := &tls.Config{}
		if tlsConfig != nil {
			cfg = tlsConfig.Clone()
		}
		cfg.ServerName = proxyURL.Hostname()
		cfg.InsecureSkipVerify = false
		cfg.NextProtos = []string{"http/1.1"}
		secure := tls.Client(conn, cfg)
		if err := secure.HandshakeContext(ctx); err != nil {
			return nil, err
		}
		conn = secure
	}
	connect := &http.Request{Method: http.MethodConnect, URL: &url.URL{Opaque: target}, Host: target, Header: make(http.Header)}
	if proxyURL.User != nil {
		password, _ := proxyURL.User.Password()
		connect.Header.Set("Proxy-Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(proxyURL.User.Username()+":"+password)))
	}
	if err := connect.Write(conn); err != nil {
		return nil, err
	}
	// Match net/http's default response-header budget. Lift the limit only
	// after parsing CONNECT, so it never truncates the tunneled image stream.
	limited := &io.LimitedReader{R: conn, N: 10 << 20}
	reader := bufio.NewReader(limited)
	resp, err := http.ReadResponse(reader, connect)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("public download proxy CONNECT failed: %d", resp.StatusCode)
	}
	limited.N = math.MaxInt64
	if err := conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	if !stop() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	success = true
	return &publicDownloadBufferedConn{Conn: conn, reader: reader}, nil
}

type publicDownloadBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *publicDownloadBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }
