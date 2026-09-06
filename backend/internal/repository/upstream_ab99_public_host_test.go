package repository

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/dns/dnsmessage"
)

// All DNS, proxy and destination sockets are local fixtures. The proxy emulates
// split DNS: a hostname checked as public locally routes to its private origin.
func TestUpstreamAB99PublicHostsOnlyProxyMustNotReachPrivateOrigin(t *testing.T) {
	installPublicHostTestDNS(t)
	var privateHits atomic.Int64
	privateOrigin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		privateHits.Add(1)
		_, _ = w.Write([]byte("private-image"))
	}))
	t.Cleanup(privateOrigin.Close)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Hostname() != "split-dns.example" {
			http.Error(w, "unexpected target", 400)
			return
		}
		response, err := http.Get(privateOrigin.URL)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = response.Body.Close() }()
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(proxy.Close)
	req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), http.MethodGet, "http://split-dns.example/private-image", nil)
	require.NoError(t, err)
	response, err := NewHTTPUpstream(nil).Do(req, proxy.URL, 1, 1)
	if response != nil {
		_ = response.Body.Close()
	}
	t.Logf("public-only request error=%v, private fixture GET count=%d", err, privateHits.Load())
	require.Zero(t, privateHits.Load(), "public precheck must not allow a private-origin GET through proxy DNS")
}

// This boundary dialer routes the supplied numeric address to a local fixture;
// production DNS validation, HTTP transport, redirects and TLS remain real.
func TestUpstreamAB99PublicHostsOnlyPinsDirectDialAndPreservesIdentity(t *testing.T) {
	installPublicHostTestDNS(t)
	hosts := make(chan string, 4)
	snis := make(chan string, 4)
	target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hosts <- r.Host
		_, _ = w.Write([]byte("image bytes"))
	}))
	target.TLS = &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		snis <- hello.ServerName
		return nil, nil
	}}
	target.StartTLS()
	t.Cleanup(target.Close)
	roots := x509.NewCertPool()
	roots.AddCert(target.Certificate())
	dials := make(chan string, 4)
	base := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials <- addr
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}}
	t.Cleanup(base.CloseIdleConnections)
	svc, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)
	req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), "GET", "https://example.com/a.png", nil)
	require.NoError(t, err)
	client := svc.httpClientForUpstreamRequest(&http.Client{Transport: base}, req)
	resp, err := client.Do(req)
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "image bytes", string(data))
	require.Equal(t, "203.0.113.10:443", <-dials, "dial must receive the validated IP, not a hostname to resolve again")
	require.Equal(t, "example.com", <-hosts)
	require.Equal(t, "example.com", <-snis)
	require.Equal(t, "example.com", req.URL.Host, "caller URL must not be rewritten")
	require.Empty(t, base.TLSClientConfig.ServerName, "cached TLS settings must not be mutated")
	bad, err := http.NewRequestWithContext(req.Context(), "GET", "https://wrong.example/a.png", nil)
	require.NoError(t, err)
	_, err = client.Do(bad)
	require.ErrorContains(t, err, "certificate", "pinning must not disable origin certificate verification")
}

func TestUpstreamAB99PublicHostsOnlyHTTPProxyPinsConnectForBothSchemes(t *testing.T) {
	installPublicHostTestDNS(t)
	for _, tc := range []struct {
		scheme      string
		secureProxy bool
	}{{"http", false}, {"https", false}, {"http", true}, {"https", true}} {
		t.Run(tc.scheme+"/tls_proxy="+strconv.FormatBool(tc.secureProxy), func(t *testing.T) {
			scheme := tc.scheme
			hosts := make(chan string, 2)
			target := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hosts <- r.Host
				_, _ = w.Write([]byte("public image"))
			}))
			roots := x509.NewCertPool()
			port := "80"
			if scheme == "https" {
				target.StartTLS()
				roots.AddCert(target.Certificate())
				port = "443"
			} else {
				target.Start()
			}
			t.Cleanup(target.Close)
			connects := make(chan string, 2)
			proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				connects <- r.Method + " " + r.Host
				if r.Method != "CONNECT" || r.Host != "203.0.113.10:"+port || r.Header.Get("Proxy-Authorization") != "Basic dXNlcjpwYXNz" {
					http.Error(w, "expected authenticated IP-pinned tunnel", 400)
					return
				}
				origin, err := net.Dial("tcp", target.Listener.Addr().String())
				if err != nil {
					http.Error(w, "fixture failed", http.StatusBadGateway)
					return
				}
				defer func() { _ = origin.Close() }()
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					http.Error(w, "fixture cannot hijack", http.StatusInternalServerError)
					return
				}
				conn, rw, err := hijacker.Hijack()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				_, _ = rw.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
				_ = rw.Flush()
				done := make(chan struct{})
				go func() { _, _ = io.Copy(origin, rw); _ = origin.Close(); close(done) }()
				_, _ = io.Copy(conn, origin)
				_ = conn.Close()
				<-done
			}))
			if tc.secureProxy {
				proxy.StartTLS()
				roots.AddCert(proxy.Certificate())
			} else {
				proxy.Start()
			}
			t.Cleanup(proxy.Close)
			proxyURL, err := url.Parse(proxy.URL)
			require.NoError(t, err)
			proxyURL.User = url.UserPassword("user", "pass")
			base := &http.Transport{Proxy: http.ProxyURL(proxyURL), TLSClientConfig: &tls.Config{RootCAs: roots}}
			t.Cleanup(base.CloseIdleConnections)
			req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), "GET", scheme+"://example.com/a.png", nil)
			require.NoError(t, err)
			svc, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
			require.True(t, ok)
			resp, err := svc.httpClientForUpstreamRequest(&http.Client{Transport: base}, req).Do(req)
			require.NoError(t, err)
			data, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, "public image", string(data))
			require.Equal(t, "CONNECT 203.0.113.10:"+port, <-connects)
			require.Equal(t, "example.com", <-hosts)
		})
	}
}

func installPublicHostTestDNS(t *testing.T) {
	installPublicHostTestDNSAnswers(t, func(string) [][4]byte { return [][4]byte{{203, 0, 113, 10}} })
}

func installPublicHostTestDNSAnswers(t *testing.T, answers func(string) [][4]byte) {
	t.Helper()
	dns, err := net.ListenPacket("udp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = dns.Close() })
	go func() {
		buf := make([]byte, 4096)
		for {
			n, peer, err := dns.ReadFrom(buf)
			if err != nil {
				return
			}
			var msg dnsmessage.Message
			if msg.Unpack(buf[:n]) != nil {
				continue
			}
			msg.Response = true
			msg.RecursionAvailable = true
			for _, q := range msg.Questions {
				if q.Type == dnsmessage.TypeA {
					for _, address := range answers(q.Name.String()) {
						msg.Answers = append(msg.Answers, dnsmessage.Resource{
							Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET},
							Body:   &dnsmessage.AResource{A: address},
						})
					}
				}
			}
			body, err := msg.Pack()
			if err == nil {
				_, _ = dns.WriteTo(body, peer)
			}
		}
	}()
	oldResolver := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp", dns.LocalAddr().String())
	}}
	t.Cleanup(func() { net.DefaultResolver = oldResolver })
}

func TestUpstreamAB99PublicHostsOnlyRechecksDNSAtDialAndRedirect(t *testing.T) {
	var rebindingLookups atomic.Int64
	installPublicHostTestDNSAnswers(t, func(host string) [][4]byte {
		switch host {
		case "mixed.example.":
			return [][4]byte{{203, 0, 113, 10}, {10, 0, 0, 1}}
		case "private.example.":
			return [][4]byte{{127, 0, 0, 1}}
		case "rebind.example.":
			if rebindingLookups.Add(1) > 1 {
				return [][4]byte{{127, 0, 0, 1}}
			}
		}
		return [][4]byte{{203, 0, 113, 10}}
	})
	var requests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/next", http.StatusFound)
			return
		}
		http.Redirect(w, r, "http://private.example/image", http.StatusFound)
	}))
	t.Cleanup(target.Close)
	var dials atomic.Int64
	base := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, network, target.Listener.Addr().String())
	}}
	t.Cleanup(base.CloseIdleConnections)
	svc, ok := NewHTTPUpstream(nil).(*httpUpstreamService)
	require.True(t, ok)
	for _, host := range []string{"mixed.example", "rebind.example", "[::1]", "[::ffff:127.0.0.1]"} {
		req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), "GET", "http://"+host+"/image", nil)
		require.NoError(t, err)
		if host == "rebind.example" {
			require.NoError(t, svc.validateRequestHost(req))
		}
		_, err = svc.httpClientForUpstreamRequest(&http.Client{Transport: base}, req).Do(req)
		require.Error(t, err, host)
		require.Zero(t, dials.Load(), "unsafe DNS or literal IP must be rejected before the dial boundary")
	}
	req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), "GET", "http://example.com/start", nil)
	require.NoError(t, err)
	_, err = svc.httpClientForUpstreamRequest(&http.Client{Transport: base}, req).Do(req)
	require.ErrorContains(t, err, "not allowed")
	require.Equal(t, int64(2), requests.Load(), "relative public redirect works, private hop is never sent")
	require.Equal(t, int64(2), dials.Load(), "each redirect gets an isolated pinned connection")
}

func TestUpstreamAB99PublicHostsOnlySOCKSUsesNumericDestination(t *testing.T) {
	installPublicHostTestDNS(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })
	destinations := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		var greeting [2]byte
		if _, err := io.ReadFull(conn, greeting[:]); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(greeting[1])); err != nil {
			return
		}
		_, _ = conn.Write([]byte{5, 0})
		var header [4]byte
		if _, err := io.ReadFull(conn, header[:]); err != nil {
			return
		}
		if header != [4]byte{5, 1, 0, 1} {
			destinations <- "not IPv4 CONNECT"
			return
		}
		var address [6]byte
		if _, err := io.ReadFull(conn, address[:]); err != nil {
			return
		}
		destinations <- net.JoinHostPort(net.IP(address[:4]).String(), strconv.Itoa(int(binary.BigEndian.Uint16(address[4:]))))
		_, _ = conn.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
		r, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		defer func() { _ = r.Body.Close() }()
		if r.Host != "example.com" {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 5\r\nConnection: close\r\n\r\nimage")
	}()
	req, err := http.NewRequestWithContext(service.WithHTTPUpstreamPublicHostsOnly(t.Context()), "GET", "http://example.com/image", nil)
	require.NoError(t, err)
	resp, err := NewHTTPUpstream(nil).Do(req, "socks5h://"+listener.Addr().String(), 1, 1)
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, "image", string(data))
	require.Equal(t, "203.0.113.10:80", <-destinations)
	<-done
}

func TestUpstreamAB99PublicHostsOnlyConnectCancellation(t *testing.T) {
	installPublicHostTestDNS(t)
	ctx, cancel := context.WithCancel(service.WithHTTPUpstreamPublicHostsOnly(t.Context()))
	defer cancel()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cancel()
		<-r.Context().Done()
	}))
	t.Cleanup(proxy.Close)
	req, err := http.NewRequestWithContext(ctx, "GET", "http://example.com/image", nil)
	require.NoError(t, err)
	_, err = NewHTTPUpstream(nil).Do(req, proxy.URL, 1, 1)
	require.ErrorIs(t, err, context.Canceled)
}

func TestUpstreamAB99PublicHostsOnlyBoundsProxyResponseHeaders(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Oversized", strings.Repeat("x", 11<<20))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	conn, err := dialPublicDownloadTunnel(ctx, proxyURL, "203.0.113.10:443", nil)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err, "the isolated tunnel must retain a bounded proxy-header read")
}
