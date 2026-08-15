//go:build integration

package upload

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const pinnedClickHouseImage = "clickhouse/clickhouse-server:26.3.17.110"

func TestClickHouseHTTPZstdRowBinaryRoundTripAndDedup(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	endpoint, adminUser, adminPassword := requireIntegrationClickHouse(t, ctx, pinnedClickHouseImage)
	createModelCallArchiveFixture(t, ctx, endpoint, adminUser, adminPassword)
	batch, expected := binaryFixtureBatch(t)
	uploader, err := NewHTTPUploader(HTTPConfig{
		URL:      endpoint,
		Database: "llm_archive",
		Table:    "model_call_archive",
		Username: "capture_ingest",
		Password: "integration-ingest",
	})
	require.NoError(t, err)
	require.NoError(t, uploader.Probe(ctx))

	require.NoError(t, uploader.Upload(ctx, batch))
	require.NoError(t, uploader.Upload(ctx, batch))
	require.Equal(t, uint64(len(expected)), queryCount(t, ctx, endpoint, adminUser, adminPassword))
	assertRawBytesAndHashesRoundTrip(t, ctx, endpoint, adminUser, adminPassword, expected)
}

func requireIntegrationClickHouse(t *testing.T, ctx context.Context, image string) (string, string, string) {
	t.Helper()
	require.Equal(t, pinnedClickHouseImage, image)
	const adminUser = "integration_admin"
	const adminPassword = "integration-admin"
	container, err := testcontainers.Run(ctx, image,
		testcontainers.WithExposedPorts("8123/tcp"),
		testcontainers.WithEnv(map[string]string{
			"CLICKHOUSE_USER":                      adminUser,
			"CLICKHOUSE_PASSWORD":                  adminPassword,
			"CLICKHOUSE_DEFAULT_ACCESS_MANAGEMENT": "1",
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/ping").
				WithPort("8123/tcp").
				WithBasicAuth(adminUser, adminPassword).
				WithResponseMatcher(func(body io.Reader) bool {
					payload, readErr := io.ReadAll(io.LimitReader(body, 5))
					return readErr == nil && string(payload) == "Ok.\n"
				}).
				WithStartupTimeout(2*time.Minute),
		),
	)
	require.NoError(t, err, "start exact pinned ClickHouse image %s", image)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		require.NoError(t, container.Terminate(cleanupCtx))
	})
	host, err := container.Host(ctx)
	require.NoError(t, err)
	port, err := container.MappedPort(ctx, "8123/tcp")
	require.NoError(t, err)
	return "http://" + host + ":" + port.Port(), adminUser, adminPassword
}

func createModelCallArchiveFixture(t *testing.T, ctx context.Context, endpoint, username, password string) {
	t.Helper()
	statements := []string{
		"CREATE DATABASE llm_archive",
		`CREATE TABLE llm_archive.model_call_archive
(
    captured_at DateTime64(3) DEFAULT now64(3),
    capture_id UUID,
    ingest_batch_id UUID,
    request_id String,
    session_id String,
    platform LowCardinality(String),
    requested_model LowCardinality(String),
    upstream_model LowCardinality(String),
    upstream_endpoint String,
    stream UInt8,
    http_status UInt16,
    stop_reason LowCardinality(String),
    thinking_effort LowCardinality(String),
    thinking_type LowCardinality(String),
    input_tokens UInt32,
    output_tokens UInt32,
    cache_read_tokens UInt32,
    cache_creation_tokens UInt32,
    signature_present UInt8,
    is_truncated UInt8,
    request_truncated UInt8,
    response_truncated UInt8,
    request_observed_bytes UInt64,
    request_stored_bytes UInt64,
    response_observed_bytes UInt64,
    response_stored_bytes UInt64,
    request_sha256 FixedString(64),
    response_sha256 FixedString(64),
    spool_version UInt16,
    capture_version UInt16,
    raw_request String CODEC(ZSTD(3)),
    raw_response String CODEC(ZSTD(3)),
    request_headers String CODEC(ZSTD(3)),
    response_headers String CODEC(ZSTD(3))
)
ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (session_id, captured_at, request_id, capture_id)
SETTINGS index_granularity = 8192, non_replicated_deduplication_window = 100000`,
		"CREATE USER capture_ingest IDENTIFIED WITH plaintext_password BY 'integration-ingest'",
		"GRANT INSERT ON llm_archive.model_call_archive TO capture_ingest",
	}
	for _, statement := range statements {
		clickHouseQuery(t, ctx, endpoint, username, password, statement)
	}
}

type integrationRaw struct {
	request  []byte
	response []byte
}

func binaryFixtureBatch(t *testing.T) (*spool.Batch, map[uuid.UUID]integrationRaw) {
	t.Helper()
	store, err := spool.Open(spool.Config{RootDir: t.TempDir()})
	require.NoError(t, err)
	fixtures := []struct {
		id       uuid.UUID
		format   model.PayloadFormat
		request  []byte
		response []byte
	}{
		{
			id:       uuid.MustParse("10000000-0000-0000-0000-000000000001"),
			format:   model.PayloadJSON,
			request:  []byte(`{"model":"json-fixture"}`),
			response: []byte(`{"stop_reason":"end_turn"}`),
		},
		{
			id:       uuid.MustParse("20000000-0000-0000-0000-000000000002"),
			format:   model.PayloadSSE,
			request:  []byte(`{"model":"sse-fixture"}`),
			response: []byte("event: message_start\ndata: {\"message\":{}}\n\nevent: message_stop\ndata: {}\n\n"),
		},
		{
			id:       uuid.MustParse("30000000-0000-0000-0000-000000000003"),
			format:   model.PayloadAWSEventStream,
			request:  []byte(`{"model":"aws-eventstream-fixture"}`),
			response: []byte{0, 0, 0, 16, 0, 0, 0, 0, 0x12, 0x34, 0x56, 0x78, 0x90, 0xab, 0xcd, 0xef},
		},
		{
			id:       uuid.MustParse("40000000-0000-0000-0000-000000000004"),
			format:   model.PayloadJSON,
			request:  []byte{'n', 'u', 'l', 0, 0xff},
			response: []byte{0xfe, 0xfd, 0, 0x80, '\n'},
		},
	}
	expected := make(map[uuid.UUID]integrationRaw, len(fixtures))
	for i, fixture := range fixtures {
		sink, openErr := store.Open(model.Begin{
			CaptureID:        fixture.id,
			CapturedAt:       time.UnixMilli(123456789 + int64(i)).UTC(),
			RequestID:        fmt.Sprintf("integration-%d", i),
			Platform:         "integration",
			RequestedModel:   "fixture",
			UpstreamModel:    "fixture-upstream",
			UpstreamEndpoint: "/fixture",
			Stream:           fixture.format != model.PayloadJSON,
			Format:           fixture.format,
			Policy: model.ContentPolicy{
				StoreRequestBody:     true,
				StoreResponseBody:    true,
				StoreRequestHeaders:  true,
				StoreResponseHeaders: true,
			},
		})
		require.NoError(t, openErr)
		require.NoError(t, sink.WriteRequestHeaders([]byte(`{"content-type":["application/octet-stream"]}`)))
		require.NoError(t, sink.WriteRequest(fixture.request))
		require.NoError(t, sink.WriteResponseHeaders([]byte(`{"content-type":["application/octet-stream"]}`)))
		require.NoError(t, sink.WriteResponse(fixture.response))
		require.NoError(t, sink.Finalize(model.Final{HTTPStatus: 200, StopReason: "end_turn", ResponseComplete: true}))
		require.NoError(t, sink.Commit())
		expected[fixture.id] = integrationRaw{request: fixture.request, response: fixture.response}
	}
	batch, err := store.NextBatch(100, 256<<20)
	require.NoError(t, err)
	require.NotNil(t, batch)
	return batch, expected
}

func queryCount(t *testing.T, ctx context.Context, endpoint, username, password string) uint64 {
	t.Helper()
	body := clickHouseQuery(t, ctx, endpoint, username, password, "SELECT count() FROM llm_archive.model_call_archive FORMAT TabSeparated")
	count, err := strconv.ParseUint(strings.TrimSpace(string(body)), 10, 64)
	require.NoError(t, err)
	return count
}

func assertRawBytesAndHashesRoundTrip(t *testing.T, ctx context.Context, endpoint, username, password string, expected map[uuid.UUID]integrationRaw) {
	t.Helper()
	type row struct {
		CaptureID      string `json:"capture_id"`
		RequestHex     string `json:"request_hex"`
		ResponseHex    string `json:"response_hex"`
		RequestSHA256  string `json:"request_sha256"`
		ResponseSHA256 string `json:"response_sha256"`
	}
	query := "SELECT toString(capture_id) AS capture_id, hex(raw_request) AS request_hex, hex(raw_response) AS response_hex, request_sha256, response_sha256 FROM llm_archive.model_call_archive FORMAT JSONEachRow"
	body := clickHouseQuery(t, ctx, endpoint, username, password, query)
	scanner := bufio.NewScanner(bytes.NewReader(body))
	seen := make(map[uuid.UUID]bool, len(expected))
	for scanner.Scan() {
		var got row
		require.NoError(t, json.Unmarshal(scanner.Bytes(), &got))
		id := uuid.MustParse(got.CaptureID)
		want, ok := expected[id]
		require.True(t, ok, "unexpected capture ID %s", id)
		require.Equal(t, strings.ToUpper(hex.EncodeToString(want.request)), got.RequestHex)
		require.Equal(t, strings.ToUpper(hex.EncodeToString(want.response)), got.ResponseHex)
		require.Equal(t, sha256HexForIntegration(want.request), got.RequestSHA256)
		require.Equal(t, sha256HexForIntegration(want.response), got.ResponseSHA256)
		seen[id] = true
	}
	require.NoError(t, scanner.Err())
	require.Len(t, seen, len(expected))
}

func clickHouseQuery(t *testing.T, ctx context.Context, endpoint, username, password, query string) []byte {
	t.Helper()
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), strings.NewReader(query))
	require.NoError(t, err)
	req.SetBasicAuth(username, password)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, "ClickHouse query failed: %s", body)
	return body
}

func sha256HexForIntegration(payload []byte) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
