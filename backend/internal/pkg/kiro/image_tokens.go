package kiro

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "golang.org/x/image/webp"
	"golang.org/x/sync/singleflight"
)

const (
	kiroImageTokenLongEdge      = 1568
	kiroImageTokenMaxPixels     = 1_150_000
	kiroImagePixelsPerToken     = 750
	kiroImageTokenFallback      = 1600
	kiroImageTokenCacheMaxItems = 256
	kiroImageTokenSuccessTTL    = 5 * time.Minute
	kiroImageTokenFailureTTL    = 30 * time.Second
	kiroImageTokenMaxRedirects  = 3
	kiroImageTokenMaxURLBytes   = 8 << 10
	kiroImageEncodedMaxBytes    = ((kiroRemoteImageMaxBytes + 2) / 3) * 4
	kiroImageTokenDialTimeout   = 5 * time.Second
	kiroImageTokenKeepAlive     = 30 * time.Second
)

var kiroImageBlockedHostnames = map[string]struct{}{
	"localhost":                  {},
	"localhost.localdomain":      {},
	"metadata":                   {},
	"metadata.google.internal":   {},
	"metadata.goog":              {},
	"instance-data":              {},
	"instance-data.ec2.internal": {},
}

// net.IP.IsGlobalUnicast includes private and documentation ranges, so keep an
// explicit denylist for addresses that must never be reached by user-supplied
// image URLs.
var kiroImageBlockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::/96"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

type imageTokenIPResolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

var (
	kiroImageTokenResolver = imageTokenIPResolver(net.DefaultResolver)
	kiroImageTokenDialer   = &net.Dialer{
		Timeout:   kiroImageTokenDialTimeout,
		KeepAlive: kiroImageTokenKeepAlive,
	}
	kiroImageTokenDialContext = kiroImageTokenDialer.DialContext
	kiroImageTokenNow         = time.Now
	kiroImageTokenHTTPClient  = newKiroImageTokenHTTPClient()
)

type imageTokenCacheEntry struct {
	tokens    int
	expiresAt time.Time
	createdAt time.Time
}

var imageTokenEstimates = struct {
	sync.Mutex
	entries map[string]imageTokenCacheEntry
}{entries: make(map[string]imageTokenCacheEntry)}

var imageTokenEstimateGroup singleflight.Group

// EstimateImageTokens estimates Kiro visual tokens from image dimensions.
// Local sources are parsed through bounded streaming decoders. Remote sources
// are fetched only through the SSRF-safe client below.
func EstimateImageTokens(ctx context.Context, mediaType, source string) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return kiroImageTokenFallback
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return kiroImageTokenFallback
	}
	if isRemoteImageURL(source) {
		return estimateRemoteImageTokens(ctx, source)
	}

	if data, ok := imageDataURLPayload(source); ok {
		source = data
	}
	if tokens, ok := estimateBase64ImageTokens(ctx, mediaType, source); ok {
		return tokens
	}
	return kiroImageTokenFallback
}

func estimateRemoteImageTokens(ctx context.Context, rawURL string) int {
	now := kiroImageTokenNow()
	if tokens, ok := loadImageTokenCache(rawURL, now); ok {
		return tokens
	}

	result := imageTokenEstimateGroup.DoChan(rawURL, func() (any, error) {
		now := kiroImageTokenNow()
		if tokens, ok := loadImageTokenCache(rawURL, now); ok {
			return tokens, nil
		}
		tokens, ok := fetchRemoteImageTokens(ctx, rawURL)
		ttl := kiroImageTokenSuccessTTL
		if !ok {
			tokens = kiroImageTokenFallback
			ttl = kiroImageTokenFailureTTL
		}
		storeImageTokenCache(rawURL, tokens, ttl, kiroImageTokenNow())
		return tokens, nil
	})

	select {
	case <-ctx.Done():
		return kiroImageTokenFallback
	case resolved := <-result:
		if resolved.Err != nil {
			return kiroImageTokenFallback
		}
		tokens, ok := resolved.Val.(int)
		if !ok || tokens < 1 {
			return kiroImageTokenFallback
		}
		return tokens
	}
}

func fetchRemoteImageTokens(ctx context.Context, rawURL string) (int, bool) {
	parsed, err := validateKiroRemoteImageURL(rawURL)
	if err != nil {
		return 0, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return 0, false
	}
	req.Header.Set("Accept", "image/*,*/*;q=0.8")
	resp, err := kiroImageTokenHTTPClient.Do(req)
	if err != nil {
		return 0, false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, false
	}
	if resp.ContentLength > kiroRemoteImageMaxBytes {
		return 0, false
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, kiroRemoteImageMaxBytes+1))
	if err != nil || len(body) == 0 || len(body) > kiroRemoteImageMaxBytes {
		return 0, false
	}
	return estimateImageBytesTokens(ctx, body)
}

func newKiroImageTokenHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dialKiroImageVerifiedIP,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       kiroImageTokenKeepAlive,
		TLSHandshakeTimeout:   kiroImageTokenDialTimeout,
		ResponseHeaderTimeout: kiroImageTokenDialTimeout,
	}
	return &http.Client{
		Timeout:   kiroRemoteImageTimeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > kiroImageTokenMaxRedirects {
				return errors.New("too many image redirects")
			}
			_, err := validateKiroRemoteImageURL(req.URL.String())
			return err
		},
	}
}

func validateKiroRemoteImageURL(rawURL string) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || len(trimmed) > kiroImageTokenMaxURLBytes {
		return nil, errors.New("invalid image URL length")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed == nil || parsed.Host == "" || parsed.Opaque != "" {
		return nil, errors.New("invalid image URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("image URL scheme is not allowed")
	}
	if parsed.User != nil {
		return nil, errors.New("image URL userinfo is not allowed")
	}
	if strings.HasSuffix(parsed.Host, ":") {
		return nil, errors.New("image URL port is not allowed")
	}
	host := normalizeKiroImageHostname(parsed.Hostname())
	if host == "" || isKiroImageBlockedHostname(host) {
		return nil, errors.New("image URL host is not allowed")
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, errors.New("image URL port is not allowed")
		}
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		if addr.Zone() != "" || !isKiroImagePublicAddr(addr) {
			return nil, errors.New("image URL address is not public")
		}
	}
	parsed.Fragment = ""
	return parsed, nil
}

func normalizeKiroImageHostname(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func isKiroImageBlockedHostname(host string) bool {
	host = normalizeKiroImageHostname(host)
	if host == "" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	_, blocked := kiroImageBlockedHostnames[host]
	return blocked
}

func isKiroImagePublicAddr(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}
	for _, prefix := range kiroImageBlockedPrefixes {
		if prefix.Contains(addr) {
			return false
		}
	}
	return true
}

// dialKiroImageVerifiedIP resolves exactly once for this connection, validates
// every answer, and dials a validated IP literal. The dialer never resolves the
// hostname a second time, closing the DNS rebinding window.
func dialKiroImageVerifiedIP(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	host = normalizeKiroImageHostname(host)
	if host == "" || isKiroImageBlockedHostname(host) {
		return nil, &net.AddrError{Err: "blocked by image SSRF policy", Addr: address}
	}

	var addrs []net.IPAddr
	if literal, err := netip.ParseAddr(host); err == nil {
		if !isKiroImagePublicAddr(literal) {
			return nil, &net.AddrError{Err: "blocked by image SSRF policy", Addr: address}
		}
		addrs = []net.IPAddr{{IP: net.IP(literal.AsSlice())}}
	} else {
		lookupCtx, cancel := context.WithTimeout(ctx, kiroImageTokenDialTimeout)
		addrs, err = kiroImageTokenResolver.LookupIPAddr(lookupCtx, host)
		cancel()
		if err != nil {
			return nil, err
		}
		if len(addrs) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host}
		}
		for _, resolved := range addrs {
			addr, ok := netip.AddrFromSlice(resolved.IP)
			if !ok || resolved.Zone != "" || !isKiroImagePublicAddr(addr) {
				return nil, &net.AddrError{Err: "blocked by image SSRF policy", Addr: resolved.IP.String()}
			}
		}
	}

	var lastErr error
	for _, resolved := range addrs {
		addr, ok := netip.AddrFromSlice(resolved.IP)
		if !ok {
			continue
		}
		addr = addr.Unmap()
		if network == "tcp4" && !addr.Is4() {
			continue
		}
		if network == "tcp6" && !addr.Is6() {
			continue
		}
		conn, err := kiroImageTokenDialContext(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable public address for %s", host)
	}
	return nil, lastErr
}

func estimateBase64ImageTokens(ctx context.Context, mediaType, encoded string) (int, bool) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" || len(encoded) > kiroImageEncodedMaxBytes || ctx.Err() != nil {
		return 0, false
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
		decoder := base64.NewDecoder(encoding, strings.NewReader(encoded))
		if tokens, ok := estimateImageReaderTokens(ctx, io.LimitReader(decoder, kiroRemoteImageMaxBytes+1)); ok {
			return tokens, true
		}
	}
	return 0, false
}

func estimateImageBytesTokens(ctx context.Context, data []byte) (int, bool) {
	if len(data) == 0 || len(data) > kiroRemoteImageMaxBytes {
		return 0, false
	}
	return estimateImageReaderTokens(ctx, bytes.NewReader(data))
}

func estimateImageReaderTokens(ctx context.Context, reader io.Reader) (int, bool) {
	if ctx.Err() != nil {
		return 0, false
	}
	cfg, _, err := image.DecodeConfig(reader)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || ctx.Err() != nil {
		return 0, false
	}
	return imageTokensForDimensions(cfg.Width, cfg.Height), true
}

func imageTokensForDimensions(width, height int) int {
	if width <= 0 || height <= 0 {
		return kiroImageTokenFallback
	}
	w, h := float64(width), float64(height)
	scale := math.Min(1, math.Min(float64(kiroImageTokenLongEdge)/w, float64(kiroImageTokenLongEdge)/h))
	if pixels := w * h; pixels*scale*scale > kiroImageTokenMaxPixels {
		scale = math.Min(scale, math.Sqrt(float64(kiroImageTokenMaxPixels)/pixels))
	}
	resizedWidth := math.Max(1, math.Floor(w*scale))
	resizedHeight := math.Max(1, math.Floor(h*scale))
	return max(1, int(math.Ceil(resizedWidth*resizedHeight/kiroImagePixelsPerToken)))
}

func imageDataURLPayload(value string) (string, bool) {
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", false
	}
	comma := strings.IndexByte(value, ',')
	if comma < 0 || comma > 4<<10 || !strings.Contains(strings.ToLower(value[:comma]), ";base64") {
		return "", false
	}
	return strings.TrimSpace(value[comma+1:]), true
}

func isRemoteImageURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func loadImageTokenCache(key string, now time.Time) (int, bool) {
	imageTokenEstimates.Lock()
	defer imageTokenEstimates.Unlock()
	entry, ok := imageTokenEstimates.entries[key]
	if !ok {
		return 0, false
	}
	if !now.Before(entry.expiresAt) {
		delete(imageTokenEstimates.entries, key)
		return 0, false
	}
	return entry.tokens, true
}

func storeImageTokenCache(key string, tokens int, ttl time.Duration, now time.Time) {
	imageTokenEstimates.Lock()
	defer imageTokenEstimates.Unlock()
	for cachedKey, entry := range imageTokenEstimates.entries {
		if !now.Before(entry.expiresAt) {
			delete(imageTokenEstimates.entries, cachedKey)
		}
	}
	if _, exists := imageTokenEstimates.entries[key]; !exists && len(imageTokenEstimates.entries) >= kiroImageTokenCacheMaxItems {
		keys := make([]string, 0, len(imageTokenEstimates.entries))
		for cachedKey := range imageTokenEstimates.entries {
			keys = append(keys, cachedKey)
		}
		sort.Slice(keys, func(i, j int) bool {
			return imageTokenEstimates.entries[keys[i]].createdAt.Before(imageTokenEstimates.entries[keys[j]].createdAt)
		})
		delete(imageTokenEstimates.entries, keys[0])
	}
	imageTokenEstimates.entries[key] = imageTokenCacheEntry{tokens: tokens, expiresAt: now.Add(ttl), createdAt: now}
}
