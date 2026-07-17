//go:build unit

package kiro

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestImageTokensForDimensions(t *testing.T) {
	require.Equal(t, 54, imageTokensForDimensions(200, 200))
	require.Equal(t, 1334, imageTokensForDimensions(1000, 1000))
	require.Equal(t, 1533, imageTokensForDimensions(2000, 1000))
	require.Equal(t, 1600, imageTokensForDimensions(0, 100))
}

func TestEstimateImageTokensSupportedFormats(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		data      []byte
	}{
		{name: "png", mediaType: "image/png", data: encodeImageForTokenTest(t, "png", 200, 200)},
		{name: "jpeg", mediaType: "image/jpeg", data: encodeImageForTokenTest(t, "jpeg", 200, 200)},
		{name: "gif", mediaType: "image/gif", data: encodeImageForTokenTest(t, "gif", 200, 200)},
		{name: "webp", mediaType: "image/webp", data: webpConfigForTokenTest(200, 200)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := base64.StdEncoding.EncodeToString(tt.data)
			require.Equal(t, 54, EstimateImageTokens(context.Background(), tt.mediaType, encoded))
			require.Equal(t, 54, EstimateImageTokens(context.Background(), tt.mediaType, "data:"+tt.mediaType+";base64,"+encoded))
			require.Equal(t, 54, EstimateImageTokens(context.Background(), "", base64.RawStdEncoding.EncodeToString(tt.data)))
		})
	}
}

func TestEstimateImageTokensUsesDimensionsNotEncodedLength(t *testing.T) {
	flat := image.NewRGBA(image.Rect(0, 0, 512, 512))
	var flatPNG bytes.Buffer
	require.NoError(t, png.Encode(&flatPNG, flat))

	noisy := image.NewRGBA(image.Rect(0, 0, 512, 512))
	for y := 0; y < 512; y++ {
		for x := 0; x < 512; x++ {
			noisy.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: uint8(x ^ y), A: 255})
		}
	}
	var noisyPNG bytes.Buffer
	require.NoError(t, png.Encode(&noisyPNG, noisy))
	require.Greater(t, noisyPNG.Len(), flatPNG.Len())

	flatTokens := EstimateImageTokens(context.Background(), "image/png", base64.StdEncoding.EncodeToString(flatPNG.Bytes()))
	noisyTokens := EstimateImageTokens(context.Background(), "image/png", base64.StdEncoding.EncodeToString(noisyPNG.Bytes()))
	require.Equal(t, 350, flatTokens)
	require.Equal(t, flatTokens, noisyTokens)
}

func TestEstimateImageTokensMalformedAndOversizedDataUseFallback(t *testing.T) {
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "image/png", "not-base64"))
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "image/png", "data:image/png,not-base64"))
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "image/png", base64.StdEncoding.EncodeToString([]byte("not an image"))))
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", ""))
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "image/png", strings.Repeat("A", kiroImageEncodedMaxBytes+1)))
}

func TestValidateKiroRemoteImageURLRejectsUnsafeTargets(t *testing.T) {
	unsafeURLs := []string{
		"file:///tmp/image.png",
		"https://user:secret@example.com/image.png",
		"https://example.com:99999/image.png",
		"https://example.com:/image.png",
		"https://localhost/image.png",
		"https://metadata.google.internal/latest/meta-data",
		"http://127.0.0.1/image.png",
		"http://10.1.2.3/image.png",
		"http://169.254.169.254/latest/meta-data",
		"http://100.64.0.1/image.png",
		"http://192.0.2.1/image.png",
		"http://[::1]/image.png",
		"http://[2001:db8::1]/image.png",
		"http://[64:ff9b::a9fe:a9fe]/latest/meta-data",
		"http://[2002:a9fe:a9fe::1]/latest/meta-data",
	}
	for _, rawURL := range unsafeURLs {
		t.Run(rawURL, func(t *testing.T) {
			_, err := validateKiroRemoteImageURL(rawURL)
			require.Error(t, err)
		})
	}

	parsed, err := validateKiroRemoteImageURL("https://images.example.com:443/a.png")
	require.NoError(t, err)
	require.Equal(t, "images.example.com", parsed.Hostname())
}

func TestEstimateImageTokensRemoteURLCachesSingleflightsAndPreventsDNSRebinding(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.8.8")}},
	}}
	pngBody := encodeImageForTokenTest(t, "png", 200, 200)
	var requests atomic.Int32
	var dialed sync.Map
	installImageTokenNetworkHooks(t, resolver, scriptedImageTokenDialer(func(address string, req *http.Request) *http.Response {
		requests.Add(1)
		dialed.Store(address, struct{}{})
		time.Sleep(20 * time.Millisecond)
		return imageTokenHTTPResponse(req, http.StatusOK, pngBody, nil)
	}))

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan int, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- EstimateImageTokens(context.Background(), "", "http://images.example.com/image.png")
		}()
	}
	wg.Wait()
	close(results)
	for tokens := range results {
		require.Equal(t, 54, tokens)
	}
	require.Equal(t, int32(1), requests.Load())
	require.Equal(t, 1, resolver.CallCount("images.example.com"))
	_, dialedVerifiedIP := dialed.Load("8.8.8.8:80")
	require.True(t, dialedVerifiedIP, "dial must use the verified IP literal, not resolve the hostname again")

	require.Equal(t, 54, EstimateImageTokens(context.Background(), "", "http://images.example.com/image.png"))
	require.Equal(t, int32(1), requests.Load(), "success must be cached")
}

func TestEstimateImageTokensRemoteFailureCacheExpires(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.4.4")}},
	}}
	now := time.Unix(1_700_000_000, 0)
	var requests atomic.Int32
	installImageTokenNetworkHooks(t, resolver, scriptedImageTokenDialer(func(_ string, req *http.Request) *http.Response {
		requests.Add(1)
		return imageTokenHTTPResponse(req, http.StatusNotFound, nil, nil)
	}))
	setImageTokenClockForTest(t, func() time.Time { return now })

	const rawURL = "http://images.example.com/missing.png"
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", rawURL))
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", rawURL))
	require.Equal(t, int32(1), requests.Load())

	now = now.Add(kiroImageTokenFailureTTL + time.Nanosecond)
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", rawURL))
	require.Equal(t, int32(2), requests.Load(), "failure cache must expire")
}

func TestEstimateImageTokensRemoteSuccessCacheExpires(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("1.1.1.1")}},
	}}
	now := time.Unix(1_700_000_000, 0)
	var requests atomic.Int32
	pngBody := encodeImageForTokenTest(t, "png", 200, 200)
	installImageTokenNetworkHooks(t, resolver, scriptedImageTokenDialer(func(_ string, req *http.Request) *http.Response {
		requests.Add(1)
		return imageTokenHTTPResponse(req, http.StatusOK, pngBody, nil)
	}))
	setImageTokenClockForTest(t, func() time.Time { return now })

	const rawURL = "http://images.example.com/image.png"
	require.Equal(t, 54, EstimateImageTokens(context.Background(), "", rawURL))
	require.Equal(t, 54, EstimateImageTokens(context.Background(), "", rawURL))
	require.Equal(t, int32(1), requests.Load())

	now = now.Add(kiroImageTokenSuccessTTL + time.Nanosecond)
	require.Equal(t, 54, EstimateImageTokens(context.Background(), "", rawURL))
	require.Equal(t, int32(2), requests.Load(), "success cache must expire")
}

func TestEstimateImageTokensRejectsMixedPrivateDNSWithoutDialing(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("169.254.169.254")}},
	}}
	var dials atomic.Int32
	installImageTokenNetworkHooks(t, resolver, func(context.Context, string, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("must not dial")
	})

	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", "http://images.example.com/image.png"))
	require.Zero(t, dials.Load())
}

func TestEstimateImageTokensRejectsRedirectToPrivateTarget(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.8.8")}},
	}}
	var requests atomic.Int32
	installImageTokenNetworkHooks(t, resolver, scriptedImageTokenDialer(func(_ string, req *http.Request) *http.Response {
		requests.Add(1)
		return imageTokenHTTPResponse(req, http.StatusFound, nil, http.Header{
			"Location": {"http://169.254.169.254/latest/meta-data"},
		})
	}))

	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", "http://images.example.com/image.png"))
	require.Equal(t, int32(1), requests.Load(), "private redirect must be rejected before another dial")
}

func TestEstimateImageTokensLimitsRedirects(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.8.8")}},
	}}
	var requests atomic.Int32
	installImageTokenNetworkHooks(t, resolver, scriptedImageTokenDialer(func(_ string, req *http.Request) *http.Response {
		requests.Add(1)
		return imageTokenHTTPResponse(req, http.StatusFound, nil, http.Header{
			"Location": {"/redirect"},
		})
	}))

	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", "http://images.example.com/image.png"))
	require.LessOrEqual(t, requests.Load(), int32(kiroImageTokenMaxRedirects+1))
}

func TestEstimateImageTokensRejectsOversizedRemoteBody(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.8.8")}},
	}}
	installImageTokenNetworkHooks(t, resolver, scriptedImageTokenDialer(func(_ string, req *http.Request) *http.Response {
		contentLength := int64(kiroRemoteImageMaxBytes + 1)
		if req.URL.Path == "/chunked" {
			contentLength = -1
		}
		return imageTokenHTTPStreamingResponse(req, http.StatusOK, io.LimitReader(repeatedByteReader('x'), kiroRemoteImageMaxBytes+1), contentLength)
	}))

	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", "http://images.example.com/declared"))
	require.Equal(t, 1600, EstimateImageTokens(context.Background(), "", "http://images.example.com/chunked"))
}

func TestEstimateImageTokensRemoteRespectsContext(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	resolver := &stubImageTokenResolver{answers: map[string][]net.IPAddr{
		"images.example.com": {{IP: net.ParseIP("8.8.8.8")}},
	}}
	connectionClosed := make(chan struct{})
	installImageTokenNetworkHooks(t, resolver, func(_ context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer close(connectionClosed)
			defer server.Close()
			_, _ = http.ReadRequest(bufio.NewReader(server))
			_, _ = server.Read(make([]byte, 1))
		}()
		return client, nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	require.Equal(t, 1600, EstimateImageTokens(ctx, "", "http://images.example.com/slow"))
	require.Less(t, time.Since(started), 500*time.Millisecond)
	select {
	case <-connectionClosed:
	case <-time.After(time.Second):
		t.Fatal("request context cancellation did not close the connection")
	}
	_, _, _ = imageTokenEstimateGroup.Do("http://images.example.com/slow", func() (any, error) { return nil, nil })
}

func TestImageTokenHTTPClientHasBoundedTimeout(t *testing.T) {
	require.Equal(t, kiroRemoteImageTimeout, kiroImageTokenHTTPClient.Timeout)
	transport, ok := kiroImageTokenHTTPClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, kiroImageTokenDialTimeout, transport.TLSHandshakeTimeout)
	require.Equal(t, kiroImageTokenDialTimeout, transport.ResponseHeaderTimeout)
}

func TestImageTokenCacheIsBounded(t *testing.T) {
	resetImageTokenEstimateStateForTest()
	now := time.Now()
	for i := 0; i <= kiroImageTokenCacheMaxItems; i++ {
		storeImageTokenCache(string(rune(i+1)), i+1, time.Minute, now.Add(time.Duration(i)*time.Nanosecond))
	}
	imageTokenEstimates.Lock()
	defer imageTokenEstimates.Unlock()
	require.Len(t, imageTokenEstimates.entries, kiroImageTokenCacheMaxItems)
	_, hasOldest := imageTokenEstimates.entries[string(rune(1))]
	require.False(t, hasOldest)
}

type stubImageTokenResolver struct {
	mu      sync.Mutex
	answers map[string][]net.IPAddr
	calls   map[string]int
}

func (r *stubImageTokenResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[host]++
	answers, ok := r.answers[host]
	if !ok {
		return nil, &net.DNSError{Err: "no test answer", Name: host}
	}
	return append([]net.IPAddr(nil), answers...), nil
}

func (r *stubImageTokenResolver) CallCount(host string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[host]
}

func installImageTokenNetworkHooks(t *testing.T, resolver imageTokenIPResolver, dial func(context.Context, string, string) (net.Conn, error)) {
	t.Helper()
	oldResolver := kiroImageTokenResolver
	oldDial := kiroImageTokenDialContext
	kiroImageTokenResolver = resolver
	kiroImageTokenDialContext = dial
	t.Cleanup(func() {
		kiroImageTokenHTTPClient.CloseIdleConnections()
		kiroImageTokenResolver = oldResolver
		kiroImageTokenDialContext = oldDial
		resetImageTokenEstimateStateForTest()
	})
}

func setImageTokenClockForTest(t *testing.T, now func() time.Time) {
	t.Helper()
	oldNow := kiroImageTokenNow
	kiroImageTokenNow = now
	t.Cleanup(func() { kiroImageTokenNow = oldNow })
}

func scriptedImageTokenDialer(handler func(address string, req *http.Request) *http.Response) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, _, address string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(2 * time.Second))
			req, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			resp := handler(address, req)
			if resp == nil {
				return
			}
			resp.Close = true
			if resp.Header == nil {
				resp.Header = make(http.Header)
			}
			resp.Header.Set("Connection", "close")
			_ = resp.Write(server)
		}()
		select {
		case <-ctx.Done():
			_ = client.Close()
			return nil, ctx.Err()
		default:
			return client, nil
		}
	}
}

func imageTokenHTTPResponse(req *http.Request, status int, body []byte, header http.Header) *http.Response {
	return imageTokenHTTPStreamingResponse(req, status, bytes.NewReader(body), int64(len(body)), header)
}

func imageTokenHTTPStreamingResponse(req *http.Request, status int, body io.Reader, contentLength int64, headers ...http.Header) *http.Response {
	header := make(http.Header)
	if len(headers) > 0 && headers[0] != nil {
		header = headers[0].Clone()
	}
	return &http.Response{
		StatusCode:    status,
		Status:        http.StatusText(status),
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        header,
		Body:          io.NopCloser(body),
		ContentLength: contentLength,
		Request:       req,
	}
}

type repeatedByteReader byte

func (r repeatedByteReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}

func encodeImageForTokenTest(t *testing.T, format string, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	var buf bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&buf, img)
	case "jpeg":
		err = jpeg.Encode(&buf, img, nil)
	case "gif":
		err = gif.Encode(&buf, img, nil)
	default:
		t.Fatalf("unsupported test image format %q", format)
	}
	require.NoError(t, err)
	return buf.Bytes()
}

func webpConfigForTokenTest(width, height int) []byte {
	data := make([]byte, 30)
	copy(data[0:4], "RIFF")
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(data)-8))
	copy(data[8:12], "WEBP")
	copy(data[12:16], "VP8X")
	binary.LittleEndian.PutUint32(data[16:20], 10)
	writeUint24LE(data[24:27], width-1)
	writeUint24LE(data[27:30], height-1)
	return data
}

func writeUint24LE(dst []byte, value int) {
	dst[0] = byte(value)
	dst[1] = byte(value >> 8)
	dst[2] = byte(value >> 16)
}

func resetImageTokenEstimateStateForTest() {
	imageTokenEstimates.Lock()
	imageTokenEstimates.entries = make(map[string]imageTokenCacheEntry)
	imageTokenEstimates.Unlock()
}
