package upload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/klauspost/compress/zstd"
)

const (
	maxHTTPResponseBytes           int64 = 64 << 10
	defaultHTTPResponseBodyTimeout       = 5 * time.Second
)

var (
	ErrRetryable      = errors.New("clickhouse_retryable")
	ErrSchema         = errors.New("clickhouse_schema")
	ErrUnauthorized   = errors.New("clickhouse_unauthorized")
	errStopUpload     = errors.New("stop upload stream")
	errUnexpectedPing = errors.New("unexpected ClickHouse ping response")
	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type HTTPConfig struct {
	URL         string
	Database    string
	Table       string
	Username    string
	Password    string
	DialContext DialContextFunc
}

type HTTPUploader struct {
	baseURL  *url.URL
	database string
	table    string
	username string
	password string
	client   *http.Client
	encoder  RowBinaryEncoder

	responseBodyTimeout time.Duration
}

type RetryableError struct {
	StatusCode int
	Cause      error
}

func (e *RetryableError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("ClickHouse HTTP status %d: %s", e.StatusCode, ErrRetryable)
	}
	if e.Cause != nil {
		return fmt.Sprintf("ClickHouse request failed: %v: %s", e.Cause, ErrRetryable)
	}
	return ErrRetryable.Error()
}

func (e *RetryableError) Unwrap() []error { return compactErrors(ErrRetryable, e.Cause) }

type SchemaError struct{ StatusCode int }

func (e *SchemaError) Error() string {
	return fmt.Sprintf("ClickHouse rejected RowBinary with HTTP status %d: %s", e.StatusCode, ErrSchema)
}

func (e *SchemaError) Unwrap() error { return ErrSchema }

type UnauthorizedError struct{ StatusCode int }

func (e *UnauthorizedError) Error() string {
	return fmt.Sprintf("ClickHouse authorization failed with HTTP status %d: %s", e.StatusCode, ErrUnauthorized)
}

func (e *UnauthorizedError) Unwrap() error { return ErrUnauthorized }

func NewHTTPUploader(config HTTPConfig) (*HTTPUploader, error) {
	baseURL, err := url.Parse(config.URL)
	if err != nil || baseURL == nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil {
		return nil, errors.New("valid ClickHouse HTTP URL without userinfo is required")
	}
	if !identifierPattern.MatchString(config.Database) || !identifierPattern.MatchString(config.Table) {
		return nil, errors.New("valid ClickHouse database and table identifiers are required")
	}
	dial := config.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
		dial = dialer.DialContext
	}
	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           dial,
		ForceAttemptHTTP2:     true,
		DisableCompression:    true,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	}
	return &HTTPUploader{
		baseURL:  cloneURL(baseURL),
		database: config.Database,
		table:    config.Table,
		username: config.Username,
		password: config.Password,
		client: &http.Client{
			Transport: transport,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		responseBodyTimeout: defaultHTTPResponseBodyTimeout,
	}, nil
}

func (u *HTTPUploader) Upload(ctx context.Context, batch *spool.Batch) error {
	if u == nil || u.client == nil || u.baseURL == nil {
		return errors.New("ClickHouse HTTP uploader is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := u.encoder.Preflight(ctx, batch); err != nil {
		return err
	}
	endpoint := u.uploadURL(batch)
	pr, pw := io.Pipe()
	requestCtx, cancelRequest := context.WithCancel(ctx)
	defer cancelRequest()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint.String(), pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		_ = pw.CloseWithError(err)
		return err
	}
	req.SetBasicAuth(u.username, u.password)
	req.Header.Set("Content-Encoding", "zstd")
	req.Header.Set("Content-Type", "application/octet-stream")
	encodeErr := make(chan error, 1)
	go func() {
		encodeErr <- u.encodeCompressed(requestCtx, pw, batch)
	}()

	resp, requestErr := u.client.Do(req)
	_ = pr.CloseWithError(errStopUpload)
	streamErr := <-encodeErr
	cancelRequest()
	if streamErr != nil && !errors.Is(streamErr, errStopUpload) && !errors.Is(streamErr, io.ErrClosedPipe) {
		closeBoundedResponse(resp)
		return streamErr
	}
	if err := ctx.Err(); err != nil {
		closeBoundedResponse(resp)
		return err
	}
	return classifyHTTPResult(resp, requestErr)
}

func (u *HTTPUploader) Probe(ctx context.Context) error {
	if u == nil || u.client == nil || u.baseURL == nil {
		return errors.New("ClickHouse HTTP uploader is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	responseTimeout := u.responseBodyTimeout
	if responseTimeout <= 0 {
		responseTimeout = defaultHTTPResponseBodyTimeout
	}
	requestCtx, cancelRequest := context.WithTimeout(ctx, responseTimeout)
	defer cancelRequest()
	endpoint := cloneURL(u.baseURL)
	endpoint.Path = "/ping"
	endpoint.RawPath = ""
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(u.username, u.password)
	resp, requestErr := u.client.Do(req)
	if err := ctx.Err(); err != nil {
		cancelRequest()
		closeBoundedResponse(resp)
		return err
	}
	if requestErr != nil {
		cancelRequest()
		closeBoundedResponse(resp)
		return &RetryableError{Cause: requestErr}
	}
	if resp == nil {
		return &RetryableError{Cause: errors.New("empty ClickHouse response")}
	}
	stopDeadlineClose := func() bool { return true }
	if resp.Body != nil {
		stopDeadlineClose = context.AfterFunc(requestCtx, func() {
			_ = resp.Body.Close()
		})
	}
	body, readErr := readBoundedResponse(resp.Body, int64(len("Ok.\n"))+1)
	_ = stopDeadlineClose()
	if readErr != nil {
		return &RetryableError{Cause: readErr}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return classifyHTTPStatus(resp.StatusCode)
	}
	if string(body) != "Ok.\n" {
		return &RetryableError{Cause: errUnexpectedPing}
	}
	return nil
}

func (u *HTTPUploader) encodeCompressed(ctx context.Context, pipe *io.PipeWriter, batch *spool.Batch) error {
	encoder, err := zstd.NewWriter(pipe, zstd.WithEncoderConcurrency(1), zstd.WithLowerEncoderMem(true))
	if err != nil {
		wrapped := fmt.Errorf("create HTTP zstd encoder: %w", err)
		_ = pipe.CloseWithError(wrapped)
		return wrapped
	}
	encodeErr := u.encoder.encodeBatchValidated(ctx, encoder, batch)
	if encodeErr != nil {
		_ = pipe.CloseWithError(encodeErr)
		_ = encoder.Close()
		return encodeErr
	}
	if err := encoder.Close(); err != nil {
		wrapped := fmt.Errorf("close HTTP zstd encoder: %w", err)
		_ = pipe.CloseWithError(wrapped)
		return wrapped
	}
	if err := pipe.Close(); err != nil {
		return fmt.Errorf("close HTTP upload pipe: %w", err)
	}
	return nil
}

func (u *HTTPUploader) uploadURL(batch *spool.Batch) *url.URL {
	endpoint := cloneURL(u.baseURL)
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	query := endpoint.Query()
	query.Set("query", "INSERT INTO "+u.database+"."+u.table+" ("+strings.Join(insertColumns, ", ")+") FORMAT RowBinary")
	query.Set("insert_deduplication_token", batch.DeduplicationToken())
	endpoint.RawQuery = query.Encode()
	return endpoint
}

func classifyHTTPResult(resp *http.Response, requestErr error) error {
	closeBoundedResponse(resp)
	if requestErr != nil {
		return &RetryableError{Cause: requestErr}
	}
	if resp == nil {
		return &RetryableError{Cause: errors.New("empty ClickHouse response")}
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return classifyHTTPStatus(resp.StatusCode)
}

func classifyHTTPStatus(status int) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return &UnauthorizedError{StatusCode: status}
	case status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || (status >= 500 && status <= 599):
		return &RetryableError{StatusCode: status}
	default:
		return &SchemaError{StatusCode: status}
	}
}

func closeBoundedResponse(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.CopyN(io.Discard, resp.Body, maxHTTPResponseBytes+1)
	_ = resp.Body.Close()
}

func readBoundedResponse(body io.ReadCloser, limit int64) ([]byte, error) {
	if body == nil {
		return nil, errors.New("empty ClickHouse response body")
	}
	payload, err := io.ReadAll(io.LimitReader(body, limit))
	closeErr := body.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	return payload, nil
}

func cloneURL(value *url.URL) *url.URL {
	cloned := *value
	return &cloned
}

func compactErrors(values ...error) []error {
	compacted := make([]error, 0, len(values))
	for _, value := range values {
		if value != nil {
			compacted = append(compacted, value)
		}
	}
	return compacted
}
