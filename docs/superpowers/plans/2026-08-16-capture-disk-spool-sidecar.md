# Capture Disk Spool Sidecar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the in-process capture queues and native ClickHouse writer with a fail-open IPC sidecar that durably spools final provider attempts to disk and streams every selected raw field into ClickHouse over embedded-tsnet HTTP.

**Architecture:** The gateway remains the sole owner of final-attempt selection and mirrors request/response bytes through a bounded, non-retrying Unix-socket session. A same-binary `capture-sidecar` process writes immutable zstd spool records, recovers them after restart, and uploads deterministic RowBinary batches through tsnet; ClickHouse is the only long-term store and `/app/data` is transient buffering only.

**Tech Stack:** Go 1.26.6, Unix domain sockets, zstd, HTTP RowBinary, ClickHouse 26.3.17.110, `tailscale.com` v1.102.2 tsnet, Vue 3/TypeScript, PostgreSQL migrations, Prometheus, Docker Compose.

## Global Constraints

- Preserve provider selection, retries, failover, billing, usage, client response bytes, and KIRO forwarding semantics. Read `docs/kiro-upstream-sync.md` before touching KIRO code.
- Capture is fail-open: IPC, sidecar, spool, tsnet, and ClickHouse failures may lose capture data but must never delay, fail, or mutate the proxied request/response.
- Static `gateway.capture.enabled` defaults to `false`; when false, do not start a sidecar, create capture directories/socket, or initialize tsnet.
- Runtime master-off stops accepting new attempts but keeps an already configured sidecar alive to drain existing spool data.
- Store selected raw request/response data permanently only in ClickHouse. Do not add S3, R2, MinIO, an object-store pointer, or a container-local query path.
- Spool maximum physical use is `12884901888` bytes (12 GiB) and filesystem free-space reserve is `8589934592` bytes (8 GiB), covering `partial`, `ready`, and `sending` plus concurrent reservations.
- Reserve the final 16 MiB inside the 12 GiB cap for durable sending manifests and ack markers; new partial content stops before consuming that operational headroom, allowing old ready records to drain at the cap.
- Request and response bodies each store at most a `33554432`-byte prefix; each sanitized request/response header block stores at most `1048576` bytes. Count and SHA-256 every byte actually received by the sidecar, and persist observed length, stored length, hash, completeness, and truncation state independently.
- IPC frames carry at most `65536` payload bytes. The request goroutine gets a bounded dial/write attempt of approximately 1 ms, performs no capture retry, and never waits for fsync or ClickHouse.
- The sidecar uses a 256 MiB Go soft memory limit, one uploader, batches of at most 100 records plus a byte ceiling, and retry backoff from 2 to 60 seconds with jitter.
- The sidecar accepts at most 32 concurrent partial attempts; excess sessions fail open as `ipc_backpressure`. Every zstd encoder uses concurrency 1 and lower-memory mode so active attempts cannot scale goroutines or compression workers with backlog size.
- Start the child with `/proc/self/exe capture-sidecar`, set a Linux parent-death signal, and allow at most 10 seconds for graceful shutdown.
- Use the existing container, image, `/app/data` volume, `restart: unless-stopped`, and administrator binary-update flow. Do not add a Docker service, Docker socket, host Tailscale installation, or argv secrets.
- The network path is sidecar tsnet to tailnet TCP `18000`, Windows loopback `127.0.0.1:18123`, and ClickHouse HTTP `8123`; native TCP `9000` and the old `19000` chain are not used.
- The ClickHouse target does not exist yet. Mock and local-container verification are allowed; any production inspection or mutation requires fresh user approval, and real provider checks must follow the existing configured proxy path.
- Use test-driven development, keep production compatibility adapters explicitly bounded and temporary, run `git diff --check` before every commit, and never stage unrelated untracked files.

---

## File Structure

### New focused packages

- `backend/internal/capture/model/model.go`: versioned wire/spool metadata and status types shared by gateway and sidecar.
- `backend/internal/capture/protocol/frame.go`: fixed frame header, message kinds, size/version validation.
- `backend/internal/capture/protocol/client.go`: fail-open per-attempt client and bounded writes.
- `backend/internal/capture/protocol/server.go`: Unix socket lifecycle and frame-session dispatch.
- `backend/internal/capture/spool/store.go`: partial-record streams, durable commit, abort, startup recovery.
- `backend/internal/capture/spool/capacity.go`: allocated-size scan, concurrent reservations, cap/free-space decisions.
- `backend/internal/capture/spool/attempt.go`: protocol event to zstd file/manifest state machine.
- `backend/internal/capture/spool/batch.go`: immutable sending manifests, ack markers, and cleanup recovery.
- `backend/internal/capture/extract/extract.go`: bounded streaming JSON, SSE, and AWS event-stream metadata extraction.
- `backend/internal/capture/upload/rowbinary.go`: exact table-column RowBinary encoder.
- `backend/internal/capture/upload/http.go`: zstd streaming HTTP INSERT and deterministic dedup token.
- `backend/internal/capture/upload/tsnet.go`: persistent embedded-tsnet node and HTTP dialer.
- `backend/internal/capture/sidecar/runtime.go`: receiver, recovery, uploader loop, status checkpoint, shutdown.
- `backend/cmd/server/capture_sidecar.go`: same-binary subcommand and capture-only config loading.
- `backend/internal/service/capture_sidecar_supervisor.go`: child lifecycle, restart backoff, shutdown.
- `backend/internal/service/capture_sidecar_supervisor_linux.go`: parent-death signal and `oom_score_adj` best effort.

### Existing integration points

- `backend/internal/config/config.go` and config tests: replace queue/native-writer knobs with nested sidecar, spool, tsnet, and HTTP settings.
- `backend/internal/service/conversation_capture_pool.go`: retain the dependency-injection façade, but remove Pond and forward attempts to IPC.
- `backend/internal/service/capture_record.go` and `openai_http_capture.go`: replace whole-body bridges with streaming attempt handles.
- Provider handlers and services under `backend/internal/handler` and `backend/internal/service`: bind every actual upstream attempt, abort retries, and commit only the attempt producing the client-visible result.
- `backend/cmd/server/main.go`, `backend/cmd/server/wire.go`, `backend/internal/service/wire.go`, and `deploy/docker-compose.yml`: wire static startup without creating a second container.
- Admin settings/status handlers, `frontend/src/api/admin/captureSettings.ts`, `frontend/src/views/admin/CaptureSettingsView.vue`, and locale files: expose spool durability and delivery health instead of queue depths.
- Capture health repository, metrics/alerts services, and migration `229_capture_spool_alert_rules.sql`: persist spool/retry health buckets and redefine operational signals without changing the generic runtime-policy setting store.
- Delete `backend/internal/service/clickhouse_archive_writer.go`, `capture_archive_writer_manager.go`, and `capture_byte_gauge.go` after all production callers move to the sidecar.

---

### Task 1: Define Sidecar Configuration and Shared Capture Model

**Files:**
- Create: `backend/internal/capture/model/model.go`
- Create: `backend/internal/capture/model/model_test.go`
- Modify: `backend/internal/config/config.go`
- Modify: `backend/internal/config/config_test.go`
- Create: `backend/internal/config/capture_sidecar_test.go`
- Modify: `deploy/config.example.yaml`

**Interfaces:**
- Produces: `model.ContentPolicy`, `model.BodyStat`, `model.HeaderStat`, `model.Extracted`, `model.Begin`, `model.Final`, `model.Manifest`, and `model.Status`.
- Produces: `config.CaptureConfig` containing `Spool CaptureSpoolConfig`, `Sidecar CaptureSidecarConfig`, `Tailscale CaptureTailscaleConfig`, and `ClickHouse CaptureClickHouseConfig`.
- Consumes: no new interfaces; this task establishes names used by all later tasks.

- [ ] **Step 1: Write failing default, validation, YAML, and model serialization tests**

```go
func TestCaptureDefaultsAreSafeAndDisabled(t *testing.T) {
	var cfg Config
	require.NoError(t, LoadIntoForTest(nil, &cfg))
	require.False(t, cfg.Gateway.Capture.Enabled)
	require.EqualValues(t, 32<<20, cfg.Gateway.Capture.MaxBodyBytes)
	require.EqualValues(t, 1<<20, cfg.Gateway.Capture.MaxHeaderBytes)
	require.EqualValues(t, 12<<30, cfg.Gateway.Capture.Spool.MaxBytes)
	require.EqualValues(t, 8<<30, cfg.Gateway.Capture.Spool.MinFreeBytes)
	require.Equal(t, "/app/data/capture/spool", cfg.Gateway.Capture.Spool.Dir)
	require.Equal(t, "/app/data/capture/capture.sock", cfg.Gateway.Capture.Sidecar.Socket)
	require.Equal(t, 32, cfg.Gateway.Capture.Sidecar.MaxActiveAttempts)
	require.Equal(t, "http://clickhouse-win:18000", cfg.Gateway.Capture.ClickHouse.URL)
}

func TestCaptureRejectsEnabledConfigWithoutSecretsOrAddress(t *testing.T) {
	cfg := validCaptureConfig()
	cfg.ClickHouse.Password = ""
	require.ErrorContains(t, cfg.Validate(), "clickhouse.password")
}

func TestDisabledLegacyCaptureConfigBootsWithoutStartingAnything(t *testing.T) {
	cfg, warnings, err := loadYAMLForTest(`gateway: {capture: {enabled: false, worker_count: 4, queue_size: 100}}`)
	require.NoError(t, err)
	require.False(t, cfg.Gateway.Capture.Enabled)
	require.Contains(t, warnings, "legacy capture queue settings are ignored while disabled")
}

func TestEnabledLegacyCaptureConfigRequiresExplicitMigration(t *testing.T) {
	_, _, err := loadYAMLForTest(`gateway: {capture: {enabled: true, worker_count: 4}}`)
	require.ErrorContains(t, err, "legacy capture setting worker_count")
}

func TestCaptureSecretsAreReachableFromEnvironment(t *testing.T) {
	t.Setenv("GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY", "tskey-auth-test")
	t.Setenv("GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD", "ingest-secret")
	cfg := loadConfigForTest(t)
	require.Equal(t, "tskey-auth-test", cfg.Gateway.Capture.Tailscale.AuthKey)
	require.Equal(t, "ingest-secret", cfg.Gateway.Capture.ClickHouse.Password)
}

func TestManifestRoundTripsBinaryIntegrityMetadata(t *testing.T) {
	want := model.Manifest{SpoolVersion: 1, CaptureVersion: 2, CaptureID: uuid.New(),
		Request: model.BodyStat{ObservedBytes: 99, StoredBytes: 64, SHA256: strings.Repeat("a", 64), Truncated: true}}
	b, err := json.Marshal(want)
	require.NoError(t, err)
	var got model.Manifest
	require.NoError(t, json.Unmarshal(b, &got))
	require.Equal(t, want, got)
}
```

- [ ] **Step 2: Run the focused tests and confirm RED**

```bash
cd backend
go test ./internal/config ./internal/capture/model
```

Expected: FAIL because `internal/capture/model` and the nested capture settings do not exist.

- [ ] **Step 3: Add concrete model/config types and validation**

```go
type CaptureSpoolConfig struct {
	Dir          string `mapstructure:"dir"`
	MaxBytes     int64 `mapstructure:"max_bytes"`
	MinFreeBytes int64 `mapstructure:"min_free_bytes"`
}

type CaptureSidecarConfig struct {
	Socket           string `mapstructure:"socket"`
	FrameBytes       int64  `mapstructure:"frame_bytes"`
	MemoryLimitBytes int64  `mapstructure:"memory_limit_bytes"`
	MaxActiveAttempts int   `mapstructure:"max_active_attempts"`
}

type CaptureTailscaleConfig struct {
	StateDir string `mapstructure:"state_dir"`
	Hostname string `mapstructure:"hostname"`
	AuthKey  string `mapstructure:"auth_key"`
}

type CaptureClickHouseConfig struct {
	URL                string `mapstructure:"url"`
	Database           string `mapstructure:"database"`
	Table              string `mapstructure:"table"`
	Username           string `mapstructure:"username"`
	Password           string `mapstructure:"password"`
	Compression        string `mapstructure:"compression"`
	BatchMaxRows       int    `mapstructure:"batch_max_rows"`
	BatchMaxBytes      int64  `mapstructure:"batch_max_bytes"`
	BatchMaxIntervalMS int    `mapstructure:"batch_max_interval_ms"`
	DialTimeoutMS      int    `mapstructure:"dial_timeout_ms"`
	WriteTimeoutMS     int    `mapstructure:"write_timeout_ms"`
}

type ContentPolicy struct {
	StoreRequestBody     bool `json:"store_request_body"`
	StoreResponseBody    bool `json:"store_response_body"`
	StoreRequestHeaders  bool `json:"store_request_headers"`
	StoreResponseHeaders bool `json:"store_response_headers"`
}

type PayloadFormat string

const (
	PayloadJSON           PayloadFormat = "json"
	PayloadSSE            PayloadFormat = "sse"
	PayloadAWSEventStream PayloadFormat = "aws_event_stream"
)

type BodyStat struct {
	ObservedBytes uint64 `json:"observed_bytes"`
	StoredBytes   uint64 `json:"stored_bytes"`
	SHA256        string `json:"sha256"`
	Truncated     bool   `json:"truncated"`
	Complete      bool   `json:"complete"`
}

type HeaderStat BodyStat

type Begin struct {
	CaptureID       uuid.UUID     `json:"capture_id"`
	CapturedAt      time.Time     `json:"captured_at"`
	RequestID       string        `json:"request_id"`
	Platform        string        `json:"platform"`
	RequestedModel  string        `json:"requested_model"`
	UpstreamModel   string        `json:"upstream_model"`
	UpstreamEndpoint string       `json:"upstream_endpoint"`
	Stream          bool          `json:"stream"`
	Format          PayloadFormat `json:"format"`
	Policy          ContentPolicy `json:"policy"`
}

type Final struct {
	HTTPStatus          uint16 `json:"http_status"`
	InputTokens         uint32 `json:"input_tokens"`
	OutputTokens        uint32 `json:"output_tokens"`
	CacheReadTokens     uint32 `json:"cache_read_tokens"`
	CacheCreationTokens uint32 `json:"cache_creation_tokens"`
	StopReason          string `json:"stop_reason"`
	ResponseComplete    bool   `json:"response_complete"`
}

type Extracted struct {
	SessionID        string `json:"session_id"`
	ThinkingEffort   string `json:"thinking_effort"`
	ThinkingType     string `json:"thinking_type"`
	SignaturePresent bool   `json:"signature_present"`
	InputTokens      uint32 `json:"input_tokens"`
	OutputTokens     uint32 `json:"output_tokens"`
	CacheReadTokens  uint32 `json:"cache_read_tokens"`
	CacheCreationTokens uint32 `json:"cache_creation_tokens"`
	StopReason       string `json:"stop_reason"`
}

type FileStat struct {
	Name               string `json:"name"`
	CompressedBytes    uint64 `json:"compressed_bytes"`
	UncompressedBytes  uint64 `json:"uncompressed_bytes"`
	CompressedSHA256   string `json:"compressed_sha256"`
	UncompressedSHA256 string `json:"uncompressed_sha256"`
}

type Manifest struct {
	SpoolVersion    uint16      `json:"spool_version"`
	CaptureVersion  uint16      `json:"capture_version"`
	CaptureID       uuid.UUID   `json:"capture_id"`
	Begin           Begin       `json:"begin"`
	Final           Final       `json:"final"`
	Extracted       Extracted   `json:"extracted"`
	Request         BodyStat    `json:"request"`
	Response        BodyStat    `json:"response"`
	RequestHeaders  HeaderStat  `json:"request_headers"`
	ResponseHeaders HeaderStat  `json:"response_headers"`
	Files           []FileStat  `json:"files"`
}

type Status struct {
	HealthSourceID        uuid.UUID         `json:"health_source_id"`
	SpoolReady            bool              `json:"spool_ready"`
	DeliveryReady         bool              `json:"delivery_ready"`
	SpoolUsedBytes        int64             `json:"spool_used_bytes"`
	SpoolMaxBytes         int64             `json:"spool_max_bytes"`
	FilesystemFreeBytes   int64             `json:"filesystem_free_bytes"`
	ReadyRecords          int64             `json:"ready_records"`
	OldestReadyAgeSeconds int64             `json:"oldest_ready_age_seconds"`
	CurrentBatchID        string            `json:"current_batch_id"`
	UploadRetries         uint64            `json:"upload_retries"`
	DroppedRecords        uint64            `json:"dropped_records"`
	DroppedByReason       map[string]uint64 `json:"dropped_by_reason"`
	HealthBuckets         []HealthBucket    `json:"health_buckets"`
	LastUploadAt          *time.Time        `json:"last_upload_at"`
}

type HealthBucket struct {
	Minute         time.Time         `json:"minute"`
	DroppedRecords map[string]uint64 `json:"dropped_records"`
	DroppedBytes   map[string]uint64 `json:"dropped_bytes"`
	UploadRetries  uint64            `json:"upload_retries"`
}
```

`Status` contains no file names, headers, bodies, endpoint credentials, or auth keys. Register every default so Viper `AutomaticEnv` can reach it; the exact secret variables are `GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY` and `GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD`, and neither is logged. Validate that enabled configs have an auth key and HTTP credentials. All three paths must be absolute and remain under `/app/data/capture`; the ClickHouse URL must use HTTP(S), contain no userinfo/query secrets, and target a non-empty database/table. Reject non-positive caps, reserves, frame/body/header sizes, active-attempt/row/byte/interval limits, and timeouts; `frame_bytes` must equal `65536` for protocol v2. Require `batch_max_bytes` to fit one maximum request + response + both header blocks + fixed row overhead, preventing a legal record from starving forever.

- [ ] **Step 4: Replace obsolete example settings**

```yaml
gateway:
  capture:
    enabled: false
    max_body_bytes: 33554432
    max_header_bytes: 1048576
    spool:
      dir: /app/data/capture/spool
      max_bytes: 12884901888
      min_free_bytes: 8589934592
    sidecar:
      socket: /app/data/capture/capture.sock
      frame_bytes: 65536
      memory_limit_bytes: 268435456
      max_active_attempts: 32
    tailscale:
      state_dir: /app/data/capture/tsnet
      hostname: sub2api-capture-writer
      auth_key: ""
    clickhouse:
      url: http://clickhouse-win:18000
      database: llm_archive
      table: model_call_archive
      username: capture_ingest
      password: ""
      compression: zstd
      batch_max_rows: 100
      batch_max_bytes: 134217728
      batch_max_interval_ms: 2000
      dial_timeout_ms: 5000
      write_timeout_ms: 60000
```

Remove active `worker_count`, `queue_size`, `max_queue_bytes`, `writer_queue_size`, native `addr`, and native batching defaults. Retain parse-only legacy fields for one release: with static capture false, ignore them and emit one sanitized warning; with static capture true, fail validation naming the legacy keys and require conversion to spool/HTTP settings. They must never construct the old queue/writer.

- [ ] **Step 5: Run tests and commit**

```bash
cd backend
go test ./internal/config ./internal/capture/model
cd ..
git diff --check
git add backend/internal/capture/model backend/internal/config/config.go backend/internal/config/config_test.go backend/internal/config/capture_sidecar_test.go deploy/config.example.yaml
git commit -m "feat(capture): define sidecar configuration model"
```

Expected: focused tests PASS; commit contains only shared model and configuration changes.

### Task 2: Implement the Bounded IPC Protocol

**Files:**
- Create: `backend/internal/capture/protocol/frame.go`
- Create: `backend/internal/capture/protocol/frame_test.go`
- Create: `backend/internal/capture/protocol/client.go`
- Create: `backend/internal/capture/protocol/client_test.go`
- Create: `backend/internal/capture/protocol/server.go`
- Create: `backend/internal/capture/protocol/server_test.go`

**Interfaces:**
- Consumes: `model.Begin`, `model.Final`, and `model.Status` from Task 1.
- Produces: `protocol.Transport.Begin(ctx context.Context, begin model.Begin) (protocol.Attempt, error)` and `protocol.Transport.Status(ctx context.Context) (model.Status, error)`.
- Produces: `protocol.Attempt` methods `ID() uuid.UUID`, `WriteRequest([]byte) bool`, `WriteResponse([]byte) bool`, `WriteRequestHeaders([]byte) bool`, `WriteResponseHeaders([]byte) bool`, `Finalize(model.Final) bool`, `Commit() bool`, and `Abort()`.
- Produces: `protocol.Server.Serve(context.Context) error`, `Server.Close() error`, `SessionFactory.Open(model.Begin) (SessionSink, error)`, and the per-attempt `SessionSink` consumed by Task 3.

- [ ] **Step 1: Write failing codec tests for exact framing and rejection rules**

```go
func TestFrameHeaderRoundTrip(t *testing.T) {
	id := uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")
	h := Header{Version: 2, Kind: KindRequestChunk, CaptureID: id, Length: 65536}
	b := h.MarshalBinary()
	require.Len(t, b, 28)
	require.Equal(t, []byte("CSP2"), b[:4])
	got, err := ParseHeader(b)
	require.NoError(t, err)
	require.Equal(t, h, got)
}

func TestParseHeaderRejectsOversizedPayload(t *testing.T) {
	b := validHeaderBytes()
	binary.BigEndian.PutUint32(b[24:28], 65537)
	_, err := ParseHeader(b)
	require.ErrorIs(t, err, ErrFrameTooLarge)
}
```

Use the fixed 28-byte header: magic `CSP2`, uint16 protocol version, uint8 kind, one reserved byte, 16 UUID bytes, and uint32 big-endian payload length. Define kinds for handshake, begin, both header blocks, both chunk streams, final, commit, abort, status request, status response, and protocol error.

- [ ] **Step 2: Write failing client/server behavior tests**

```go
func TestAttemptDoesNotRetryOrBlockWhenSocketBackpressures(t *testing.T) {
	dialer := &countingDialer{conn: &deadlineConn{writeErr: os.ErrDeadlineExceeded}}
	c := NewClient(ClientConfig{Dial: dialer.DialContext, WriteTimeout: time.Millisecond})
	a, err := c.Begin(context.Background(), model.Begin{CaptureID: uuid.New()})
	require.NoError(t, err)
	start := time.Now()
	require.False(t, a.WriteResponse(make([]byte, 65536)))
	require.Less(t, time.Since(start), 50*time.Millisecond)
	require.Equal(t, 1, dialer.Calls())
}

func TestServerRejectsVersionMismatchBeforeBegin(t *testing.T) {
	conn := dialTestSocket(t)
	writeHandshake(t, conn, 99)
	require.Equal(t, KindProtocolError, readFrame(t, conn).Kind)
	require.Empty(t, testSink.Begins())
}

func TestServerBoundsAcceptedSessionsBeforeSpawningHandlers(t *testing.T) {
	s := newTestServer(t, ServerConfig{MaxSessions: 32})
	conns := openAndHoldHandshakes(t, s, 32)
	defer closeAll(conns)
	extra := dialTestSocket(t)
	writeHandshake(t, extra, ProtocolVersion)
	require.Error(t, readHandshakeResponse(extra))
	require.Equal(t, 32, s.ActiveHandlers())
}
```

- [ ] **Step 3: Run the protocol tests and confirm RED**

```bash
cd backend
go test ./internal/capture/protocol
```

Expected: FAIL because the protocol package is absent.

- [ ] **Step 4: Implement one connection per attempt with terminal fail-open state**

```go
type Attempt interface {
	ID() uuid.UUID
	WriteRequest([]byte) bool
	WriteResponse([]byte) bool
	WriteRequestHeaders([]byte) bool
	WriteResponseHeaders([]byte) bool
	Finalize(model.Final) bool
	Commit() bool
	Abort()
}

type Transport interface {
	Begin(context.Context, model.Begin) (Attempt, error)
	Status(context.Context) (model.Status, error)
	Close() error
}

type SessionFactory interface {
	Open(model.Begin) (SessionSink, error)
}

type SessionSink interface {
	WriteRequestHeaders([]byte) error
	WriteResponseHeaders([]byte) error
	WriteRequest([]byte) error
	WriteResponse([]byte) error
	Finalize(model.Final) error
	Commit() error
	Abort(error)
}
```

Each attempt owns one Unix connection, sends handshake then `BEGIN`, splits byte slices into at most 64 KiB payloads, applies a fresh immediate deadline to every dial/write, and closes the connection immediately after any short write/error so the server aborts its partial; the handle then remains permanently failed. `Commit` and `Abort` also close the connection. Never retry or buffer an unsent frame. Before handing an accepted connection to a goroutine, the server tries to acquire the same 32-session semaphore; when full it immediately closes that accepted connection, so the gateway's bounded write fails open without an unbounded handler count. The server creates its parent directory as `0700`, removes only a pre-existing socket node after `lstat` proves it is a socket (never a symlink/regular file), binds with `0600`, enforces legal message order, and aborts/releases a session on disconnect, panic, or malformed input.

- [ ] **Step 5: Run race tests and commit**

```bash
cd backend
go test -race ./internal/capture/protocol
cd ..
git diff --check
git add backend/internal/capture/protocol
git commit -m "feat(capture): add bounded sidecar ipc protocol"
```

Expected: codec, timeout, disconnect, permissions, ordering, and version-mismatch tests PASS under the race detector.

### Task 3: Build the Durable Partial-to-Ready Spool

**Files:**
- Create: `backend/internal/capture/spool/capacity.go`
- Create: `backend/internal/capture/spool/capacity_test.go`
- Create: `backend/internal/capture/spool/store.go`
- Create: `backend/internal/capture/spool/store_test.go`
- Create: `backend/internal/capture/spool/attempt.go`
- Create: `backend/internal/capture/spool/attempt_test.go`

**Interfaces:**
- Consumes: protocol `SessionSink` events and Task 1 model types.
- Produces: `spool.Open(config Config) (*Store, error)`, `Store.Open(model.Begin) (protocol.SessionSink, error)`, `Store.Recover(context.Context) (RecoveryReport, error)`, `Store.Ready() []RecordRef`, and `Store.Snapshot() model.Status`; `Store` implements `protocol.SessionFactory`.
- Produces: `Capacity.Reserve(recordID uuid.UUID, want int64) (Reservation, error)` and `Reservation.Consume/Release`.

- [ ] **Step 1: Write failing capacity tests for both independent limits**

```go
func TestCapacityRejectsAtPhysicalCap(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30, MinFreeBytes: 8 << 30},
		usage{Allocated: 12<<30 - 4096, Free: 20 << 30})
	_, err := c.Reserve(uuid.New(), 8192)
	require.ErrorIs(t, err, ErrSpoolCap)
}

func TestCapacityRejectsBeforeFreeReserveIsCrossed(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30, MinFreeBytes: 8 << 30},
		usage{Allocated: 1 << 30, Free: 8<<30 + 4096})
	_, err := c.Reserve(uuid.New(), 8192)
	require.ErrorIs(t, err, ErrFreeReserve)
}
```

Also test concurrent reservations cannot collectively oversubscribe, scans include allocated blocks from all three spool directories, and release/commit accounting cannot go negative.

```go
func TestWorstCaseFrameReservationPrecedesDiskWrite(t *testing.T) {
	c, fs := newCapacityWithRecordingFS(t, usage{Allocated: 12<<30 - 32<<10, Free: 20 << 30})
	err := c.BeforeFrame(uuid.New(), bytes.Repeat([]byte{0xff}, 64<<10))
	require.ErrorIs(t, err, ErrSpoolCap)
	require.Zero(t, fs.WriteCalls())
}

func TestAdmissionLeavesSixteenMiBForSendingMetadata(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30, OperationalHeadroomBytes: 16 << 20},
		usage{Allocated: 12<<30 - 16<<20, Free: 20 << 30})
	_, err := c.ReserveContent(uuid.New(), 1)
	require.ErrorIs(t, err, ErrSpoolCap)
	require.NoError(t, c.ReserveOperational(64<<10))
}
```

- [ ] **Step 2: Write failing durability, policy, and restart tests**

```go
func TestCommitFsyncsThenAtomicallyPublishesReadyRecord(t *testing.T) {
	fs := newRecordingFS(t)
	s := openTestStore(t, fs)
	a := beginAttempt(t, s, policyAll())
	require.NoError(t, a.WriteRequest([]byte{0xff, 0x00, 0x61}))
	require.NoError(t, a.Finalize(model.Final{HTTPStatus: 200}))
	require.NoError(t, a.Commit())
	require.Equal(t, []string{
		"fsync:request.zst", "fsync:response.zst", "fsync:manifest.tmp",
		"fsync:partial-record-dir", "rename:partial-to-ready", "fsync:ready-dir",
	}, fs.DurableEvents())
}

func TestDisabledBodyPolicyNeverCreatesRawFile(t *testing.T) {
	s := openTestStore(t, nil)
	a := beginAttempt(t, s, model.ContentPolicy{StoreRequestBody: false})
	require.NoError(t, a.WriteRequest(bytes.Repeat([]byte("x"), 4096)))
	require.NoError(t, a.Commit())
	require.NoFileExists(t, readyPath(t, a.ID(), "request.zst"))
	require.EqualValues(t, 4096, readManifest(t, a.ID()).Request.ObservedBytes)
}

func TestRecoverDeletesPartialAndKeepsReady(t *testing.T) {
	seedPartial(t, "orphan")
	seedReady(t, validRecord())
	report, err := openTestStore(t, nil).Recover(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, report.OrphansDeleted)
	require.Len(t, report.Ready, 1)
}

func TestThirtyThirdActiveAttemptIsRejectedWithoutOpeningFiles(t *testing.T) {
	s := openTestStoreWithMaxAttempts(t, 32)
	for i := 0; i < 32; i++ { require.NotNil(t, beginAttempt(t, s, policyAll())) }
	_, err := s.Open(model.Begin{CaptureID: uuid.New()})
	require.ErrorIs(t, err, ErrTooManyAttempts)
	require.Equal(t, 32, countPartialDirs(t))
}
```

- [ ] **Step 3: Run spool tests and confirm RED**

```bash
cd backend
go test ./internal/capture/spool
```

Expected: FAIL because store, capacity, and attempt state machines do not exist.

- [ ] **Step 4: Implement streaming zstd records and exact durable commit order**

```go
type Config struct {
	RootDir       string
	MaxBytes      int64
	MinFreeBytes  int64
	MaxBodyBytes  int64
	MaxHeaderBytes int64
	MaxActiveAttempts int
	OperationalHeadroomBytes int64
}

type RecoveryReport struct {
	Ready           []RecordRef
	OrphansDeleted  int
	CorruptDeleted  int
}
```

Create `partial`, `ready`, and `sending` as `0700`. Acquire a 32-slot attempt semaphore before creating a partial directory and release it on every commit/abort/error; map saturation to `ipc_backpressure`. Reserve 1 MiB of directory/manifest/file overhead before creating each partial. Stream raw binary to separate zstd files only when the per-column content policy allows it; configure each writer with `zstd.WithEncoderConcurrency(1)` and `zstd.WithLowerEncoderMem(true)`, and always update SHA-256 and observed-byte counts incrementally. Stop storing beyond the direction-specific limit while continuing counts/hash for bytes that actually reach the sidecar. Close request writers when response framing begins so an attempt does not keep both directions' encoders live. On abort/disconnect, close and delete partial. On commit, close/fsync files, write/fsync versioned JSON manifest temp, fsync the partial directory, rename within the filesystem, then fsync `ready`. Before each frame reaches the filesystem, pessimistically reserve the encoder's `MaxEncodedSize(frame)` rounded up to the filesystem block size; after flush/fsync, reconcile reservation against `stat.blocks*512`. Check both cap and free reserve against `scannedAllocated + concurrentReserved`, and stop new partial admission at `maxBytes - 16 MiB`; the separate operational reservation may consume only that final headroom for batch manifests/acks. If either content limit trips, abort that record without deleting older ready records.

- [ ] **Step 5: Add corruption and crash-point table tests**

```go
func TestRecoverCrashPoints(t *testing.T) {
	for _, tc := range []struct{name, layout string; wantReady, wantDeleted int}{
		{"partial without manifest", "partial-body", 0, 1},
		{"complete partial before rename", "partial-complete", 0, 1},
		{"ready valid", "ready-valid", 1, 0},
		{"ready checksum mismatch", "ready-corrupt", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) { assertRecovery(t, tc.layout, tc.wantReady, tc.wantDeleted) })
	}
}
```

Verify manifest file lengths/checksums against compressed and uncompressed streams. Classify corrupt records as `spool_corrupt`, delete them so they cannot block the queue, and expose the count in `RecoveryReport`.

- [ ] **Step 6: Run race tests and commit**

```bash
cd backend
go test -race ./internal/capture/spool
cd ..
git diff --check
git add backend/internal/capture/spool
git commit -m "feat(capture): add durable bounded disk spool"
```

Expected: all capacity, binary payload, truncation, content-policy, fsync ordering, restart, and corruption tests PASS.

### Task 4: Extract Metadata Without Whole-Body Materialization

**Files:**
- Create: `backend/internal/capture/extract/extract.go`
- Create: `backend/internal/capture/extract/extract_test.go`
- Modify: `backend/internal/capture/spool/attempt.go`
- Modify: `backend/internal/capture/spool/attempt_test.go`
- Modify: `backend/internal/service/capture_record.go`
- Modify: `backend/internal/service/capture_record_test.go`

**Interfaces:**
- Consumes: each request/response chunk as it reaches the sidecar, before content-policy storage decisions, plus `model.Begin`/`model.Final`.
- Produces: `extract.New(ctx context.Context, format model.PayloadFormat) (extract.Stream, error)` where `Stream` has bounded `FeedRequest([]byte) error`, `FeedResponse([]byte) error`, and `Finalize(model.Final) (model.Extracted, error)` methods.
- Produces: `extract.FromReaders(ctx context.Context, in Input) (model.Extracted, error)` as a fixture/compatibility helper that feeds the same `Stream` implementation.
- Produces temporarily: `service.ExtractCaptureMetadataForCompatibility(record *CaptureRecord)`, implemented through the same bounded parser for legacy unit tests only.

- [ ] **Step 1: Write failing bounded JSON/SSE/AWS tests**

```go
func TestExtractSSEAcrossArbitraryChunkBoundaries(t *testing.T) {
	r := &chunkReader{Chunks: [][]byte{[]byte("data: {\"us"), []byte("age\":{\"input_tokens\":7}}\n\n")}}
	got, err := FromReaders(context.Background(), Input{Format: model.PayloadSSE, Response: r})
	require.NoError(t, err)
	require.EqualValues(t, 7, got.InputTokens)
}

func TestExtractAWSFramesUsesBoundedScratch(t *testing.T) {
	payload := awsEventStreamFixture(t, "contentBlockDelta", bytes.Repeat([]byte("x"), 2<<20))
	got, stats := measureExtraction(t, Input{Format: model.PayloadAWSEventStream, Response: bytes.NewReader(payload)})
	require.NotNil(t, got)
	require.Less(t, stats.PeakRetainedBytes, int64(4<<20))
}

func TestDisabledRawStorageStillExtractsWithoutCreatingContentFile(t *testing.T) {
	s := openStoreWithExtractor(t)
	a := beginAttempt(t, s, model.ContentPolicy{StoreResponseBody: false})
	require.NoError(t, a.WriteResponse([]byte("data: {\"usage\":{\"output_tokens\":9}}\n\n")))
	require.NoError(t, a.Commit())
	m := readManifest(t, a.ID())
	require.EqualValues(t, 9, m.Extracted.OutputTokens)
	require.NoFileExists(t, readyPath(t, a.ID(), "response.zst"))
}
```

Cover ordinary JSON, OpenAI/Anthropic SSE including `[DONE]`, AWS event-stream CRC/header boundaries, malformed/truncated inputs, non-UTF-8 raw content, and disabled-content policy metadata.

- [ ] **Step 2: Run extraction tests and confirm RED**

```bash
cd backend
go test ./internal/capture/extract ./internal/service -run 'Extract|CaptureRecord'
```

Expected: FAIL because reader-based extraction does not exist.

- [ ] **Step 3: Implement bounded streaming parsers**

```go
type Stream interface {
	FeedRequest([]byte) error
	FeedResponse([]byte) error
	Finalize(model.Final) (model.Extracted, error)
}

func New(ctx context.Context, format model.PayloadFormat) (Stream, error) {
	switch format {
	case model.PayloadJSON:
		return newJSONStream(ctx), nil
	case model.PayloadSSE:
		return newSSEStream(ctx), nil
	case model.PayloadAWSEventStream:
		return newAWSEventStream(ctx), nil
	default:
		return nil, ErrUnsupportedFormat
	}
}
```

Use `json.Decoder` behind a fixed-capacity pipe/decoder goroutine for JSON, a custom bounded line/event state machine rather than Scanner's default token assumptions for SSE, and length-checked incremental frame parsing for AWS event-stream. Limit each individual decoded metadata token/event to 1 MiB and cancel/join decoder goroutines on abort. Retain only metadata fields; never call `io.ReadAll` on a request/response body. Feed the extractor before deciding whether the same chunk is persisted, so a disabled raw column is observed for metadata/hash/length and discarded immediately. During commit, finalize extraction, merge authoritative `Final` usage/stop fields, then write the immutable manifest. A malformed provider payload leaves allowed raw content and terminal metadata commit-able, records a sanitized extraction warning, and does not turn capture into a proxy failure.

- [ ] **Step 4: Route the compatibility extractor through readers and prove parity**

```go
func TestCompatibilityExtractionMatchesExistingFixtures(t *testing.T) {
	for _, fixture := range allProviderCaptureFixtures(t) {
		legacy := fixture.Expected
		got, err := ExtractCaptureMetadataForCompatibility(fixture.Record)
		require.NoError(t, err)
		require.Equal(t, legacy, got)
	}
}
```

Keep this adapter bounded to unit-test and migration call sites; mark no production handler as migrated until Task 9/10 supplies a protocol attempt.

- [ ] **Step 5: Run tests and commit**

```bash
cd backend
go test ./internal/capture/extract ./internal/service -run 'Extract|CaptureRecord'
cd ..
git diff --check
git add backend/internal/capture/extract backend/internal/capture/spool/attempt.go backend/internal/capture/spool/attempt_test.go backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go
git commit -m "refactor(capture): stream provider metadata extraction"
```

Expected: fixture parity passes and large-input retention stays below the asserted bound.

### Task 5: Add Immutable Batch Manifests, Ack Markers, and Recovery

**Files:**
- Create: `backend/internal/capture/spool/batch.go`
- Create: `backend/internal/capture/spool/batch_test.go`
- Modify: `backend/internal/capture/spool/store.go`
- Modify: `backend/internal/capture/spool/store_test.go`

**Interfaces:**
- Consumes: `spool.RecordRef` and capacity accounting from Task 3.
- Produces: `Store.NextBatch(maxRecords int, maxBytes int64) (*Batch, error)`, `Store.MarkAcked(*Batch) error`, and `Store.CleanupAcked(*Batch) error`.
- Produces: `Batch.ID uuid.UUID`, `Batch.Records []RecordRef`, `Batch.OpenRecord(uuid.UUID)`, and stable `Batch.DeduplicationToken() string`.

- [ ] **Step 1: Write failing batch-selection and durability tests**

```go
func TestNextBatchIsStableAndRespectsBothLimits(t *testing.T) {
	s := storeWithReadyRecords(t, []recordSize{{"a", 40}, {"b", 40}, {"c", 40}})
	b, err := s.NextBatch(100, 90)
	require.NoError(t, err)
	require.Equal(t, []string{"a", "b"}, ids(b.Records))
	require.NotEmpty(t, b.DeduplicationToken())
	require.FileExists(t, sendingManifestPath(t, b.ID))
}

func TestManifestIsDurableBeforeBatchIsReturned(t *testing.T) {
	fs := newRecordingFS(t)
	b, err := storeWithFS(t, fs).NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.Equal(t, []string{"fsync:batch.tmp", "rename:batch.manifest", "fsync:sending-dir"}, fs.BatchEvents())
	require.NotNil(t, b)
}
```

Selection must be deterministic by ready timestamp then capture ID. The byte ceiling uses manifest uncompressed stored sizes and includes row metadata overhead conservatively.

- [ ] **Step 2: Write failing recovery tests for every ack window**

```go
func TestRecoveryUsesSameBatchAfterRemoteCommitBeforeLocalAck(t *testing.T) {
	b := seedSendingManifestWithoutAck(t, []string{"a", "b"})
	s := reopenStore(t)
	got, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID)
	require.Equal(t, b.Records, got.Records)
}

func TestRecoveryAfterAckOnlyCleansWithoutReupload(t *testing.T) {
	seedAckedBatchWithHalfCleanedRecords(t)
	s := reopenStore(t)
	require.NoError(t, s.RecoverAcked())
	require.Empty(t, s.PendingBatches())
	require.Empty(t, s.Ready())
}
```

Also cover a crash after manifest temp fsync, after manifest rename, after ack temp fsync, after ack rename, and after each individual ready-record deletion.

- [ ] **Step 3: Run batch tests and confirm RED**

```bash
cd backend
go test ./internal/capture/spool -run 'Batch|Ack|Sending|Recovery'
```

Expected: FAIL because durable batches and ack recovery are absent.

- [ ] **Step 4: Implement manifest-before-send and ack-before-delete semantics**

```go
type BatchManifest struct {
	Version   uint16        `json:"version"`
	BatchID   uuid.UUID     `json:"batch_id"`
	CreatedAt time.Time     `json:"created_at"`
	Records   []BatchRecord `json:"records"`
}

type BatchRecord struct {
	CaptureID     uuid.UUID `json:"capture_id"`
	ManifestSHA256 string   `json:"manifest_sha256"`
	StoredBytes   int64     `json:"stored_bytes"`
}
```

Reserve manifest plus ack-marker worst-case allocated blocks from Task 3's 16 MiB operational headroom. Write `sending/<batch_id>.manifest.tmp`, fsync, rename to `.manifest`, and fsync `sending` before exposing a batch. Keep record directories in `ready`; a sending manifest references immutable IDs and hashes. After a successful upload, write/fsync/rename `<batch_id>.acked`, fsync the directory, then delete ready records idempotently, fsync `ready`, delete the manifest/marker, and fsync `sending`. On startup, an unacked manifest always wins over selecting a new batch and reuses exactly the same ID/order; an acked manifest performs cleanup without upload. If unrelated host growth has already crossed the 8 GiB reserve, allow only this pre-bounded operational metadata write because successful cleanup releases much more ready data; never admit a new partial in that state.

- [ ] **Step 5: Run race tests and commit**

```bash
cd backend
go test -race ./internal/capture/spool
cd ..
git diff --check
git add backend/internal/capture/spool
git commit -m "feat(capture): make upload batches crash recoverable"
```

Expected: all crash-window tests PASS and repeated cleanup remains idempotent.

### Task 6: Stream zstd RowBinary into ClickHouse HTTP

**Files:**
- Create: `backend/internal/capture/upload/rowbinary.go`
- Create: `backend/internal/capture/upload/rowbinary_test.go`
- Create: `backend/internal/capture/upload/http.go`
- Create: `backend/internal/capture/upload/http_test.go`
- Create: `backend/internal/capture/upload/clickhouse_integration_test.go`

**Interfaces:**
- Consumes: Task 5 `spool.Batch` and Task 4 extraction.
- Produces: `upload.RowBinaryEncoder.EncodeBatch(ctx context.Context, dst io.Writer, batch *spool.Batch) error`.
- Produces: `upload.HTTPUploader.Upload(ctx context.Context, batch *spool.Batch) error` and `HTTPUploader.Probe(ctx context.Context) error`, with typed `ErrRetryable`, `ErrSchema`, and `ErrUnauthorized` classification; local manifest/body checksum failures remain `spool_corrupt`.
- Produces: an injectable `DialContext` used by Task 7's tsnet transport.

- [ ] **Step 1: Write failing exact-binary encoder tests**

```go
func TestEncodeUUIDUsesClickHouseUInt128ByteOrder(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeUUID(&out, uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff")))
	require.Equal(t, "7766554433221100ffeeddccbbaa9988", hex.EncodeToString(out.Bytes()))
}

func TestEncodeDateTime64MillisLittleEndian(t *testing.T) {
	var out bytes.Buffer
	require.NoError(t, writeDateTime64Millis(&out, time.UnixMilli(123456789)))
	require.Equal(t, []byte{0x15, 0xcd, 0x5b, 0x07, 0, 0, 0, 0}, out.Bytes())
}
```

Add a golden row fixture matching this exact `llm_archive.model_call_archive` order from the Windows runbook:

```go
var insertColumns = []string{
	"captured_at", "capture_id", "ingest_batch_id", "request_id", "session_id",
	"platform", "requested_model", "upstream_model", "upstream_endpoint", "stream",
	"http_status", "stop_reason", "thinking_effort", "thinking_type", "input_tokens",
	"output_tokens", "cache_read_tokens", "cache_creation_tokens", "signature_present",
	"is_truncated", "request_truncated", "response_truncated", "request_observed_bytes",
	"request_stored_bytes", "response_observed_bytes", "response_stored_bytes",
	"request_sha256", "response_sha256", "spool_version", "capture_version",
	"raw_request", "raw_response", "request_headers", "response_headers",
}
```

Encode `DateTime64(3)`, UUID, String/LowCardinality(String) input representation, UInt8/16/32/64, and FixedString(64) exactly. The encoder must reject a hash not exactly 64 lowercase hex characters before starting the HTTP request.

- [ ] **Step 2: Write failing HTTP request and streaming-memory tests**

```go
func TestUploadUsesZstdRowBinaryAndStableDedupToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "capture_ingest", user)
		require.Equal(t, "secret", pass)
		require.Equal(t, "zstd", r.Header.Get("Content-Encoding"))
		require.Contains(t, r.URL.Query().Get("query"), "FORMAT RowBinary")
		require.Equal(t, testBatch.ID.String(), r.URL.Query().Get("insert_deduplication_token"))
		assertZstdRowBinaryBody(t, r.Body, expectedRows)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	require.NoError(t, newTestUploader(srv.URL).Upload(context.Background(), testBatch))
}

func TestEncodeLargeBatchDoesNotMaterializeBodyStrings(t *testing.T) {
	batch := batchWithCompressedBodies(t, 1, 32<<20, 32<<20)
	stats := measureUpload(t, batch)
	require.Less(t, stats.PeakRetainedBytes, int64(64<<20))
}

func TestProbeUsesSameDialerAndBasicAuthWithoutTablePrivileges(t *testing.T) {
	srv := newAuthenticatedPingServer(t, "capture_ingest", "secret")
	defer srv.Close()
	require.NoError(t, newTestUploader(srv.URL).Probe(context.Background()))
}
```

- [ ] **Step 3: Run unit tests and confirm RED**

```bash
cd backend
go test ./internal/capture/upload
```

Expected: FAIL because uploader and RowBinary encoder do not exist.

- [ ] **Step 4: Implement pipe-based HTTP streaming and error classification**

```go
func (u *HTTPUploader) Upload(ctx context.Context, batch *spool.Batch) error {
	pr, pw := io.Pipe()
	encodeErr := make(chan error, 1)
	go func() {
		err := u.encodeCompressed(ctx, pw, batch)
		_ = pw.CloseWithError(err)
		encodeErr <- err
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.endpoint(batch), pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		<-encodeErr
		return err
	}
	req.SetBasicAuth(u.username, u.password)
	req.Header.Set("Content-Encoding", "zstd")
	resp, err := u.client.Do(req)
	if err != nil { _ = pr.CloseWithError(err) }
	streamErr := <-encodeErr
	if streamErr != nil { return streamErr }
	return classifyHTTPResult(resp, err)
}
```

The query must list every schema column explicitly before `FORMAT RowBinary` and set `insert_deduplication_token=<batch UUID>`. Decode spool zstd one field at a time, feed RowBinary directly into the request zstd writer through an `io.Pipe`, close the pipe with the first encoder error, and drain/close bounded response bodies. `Probe` sends authenticated `GET /ping` through the same injected transport and accepts only the exact successful ClickHouse response. Treat network errors, 408, 425, 429, and 5xx as retryable; treat 401/403 as unauthorized delivery-down; treat ClickHouse schema/type/data rejections as `ErrSchema` and retain the entire immutable batch for administrator repair and exact replay. Only a checksum/format failure proven against one local immutable record is `spool_corrupt`; delete that record through the spool recovery path before selecting a new batch.

- [ ] **Step 5: Verify against the pinned real ClickHouse image**

```go
//go:build integration

func TestClickHouseHTTPZstdRowBinaryRoundTripAndDedup(t *testing.T) {
	dsn := requireIntegrationClickHouse(t, "clickhouse/clickhouse-server:26.3.17.110")
	createModelCallArchiveFixture(t, dsn, "non_replicated_deduplication_window=100000")
	batch := binaryFixtureBatch(t)
	u := uploaderFor(t, dsn)
	require.NoError(t, u.Upload(context.Background(), batch))
	require.NoError(t, u.Upload(context.Background(), batch))
	require.Equal(t, uint64(1), queryCountByCaptureID(t, dsn, batch.Records[0].CaptureID))
	assertRawBytesAndHashesRoundTrip(t, dsn, batch)
}
```

Run the repository's integration harness (or a disposable local Docker container) with:

```bash
cd backend
go test -tags=integration ./internal/capture/upload -run ClickHouseHTTPZstdRowBinaryRoundTripAndDedup -v
```

Expected: PASS against exactly `clickhouse/clickhouse-server:26.3.17.110`; the second identical batch is deduplicated and binary content round-trips byte-for-byte. If Docker is unavailable, record the test as environment-blocked and do not claim ClickHouse compatibility complete.

- [ ] **Step 6: Run unit tests and commit**

```bash
cd backend
go test -race ./internal/capture/upload
cd ..
git diff --check
git add backend/internal/capture/upload
git commit -m "feat(capture): stream rowbinary batches over http"
```

Expected: unit tests PASS; the commit includes the integration test even when local Docker is unavailable.

### Task 7: Add Embedded tsnet and the Sidecar Runtime

**Files:**
- Create: `backend/internal/capture/upload/tsnet.go`
- Create: `backend/internal/capture/upload/tsnet_test.go`
- Create: `backend/internal/capture/sidecar/runtime.go`
- Create: `backend/internal/capture/sidecar/runtime_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`

**Interfaces:**
- Consumes: Tasks 2-6 protocol server, spool store, batch recovery, and HTTP uploader.
- Produces: `upload.NewTSNetDialer(config TSNetConfig) (*TSNetDialer, error)`, `TSNetDialer.DialContext`, and `TSNetDialer.Close`.
- Produces: `sidecar.New(config Config, deps Dependencies) (*Runtime, error)`, `Runtime.Run(context.Context) error`, and `Runtime.Shutdown(context.Context) error`.

- [ ] **Step 1: Write failing persistent-tsnet construction tests**

```go
func TestTSNetServerUsesPersistentStateAndTaggedAuthKey(t *testing.T) {
	factory := &fakeTSNetFactory{}
	d, err := NewTSNetDialer(TSNetConfig{
		Dir: t.TempDir(), Hostname: "sub2api-capture-writer", AuthKey: "tskey-auth-test", Factory: factory.New,
	})
	require.NoError(t, err)
	require.False(t, factory.Server.Ephemeral)
	require.Equal(t, "sub2api-capture-writer", factory.Server.Hostname)
	require.Equal(t, "tskey-auth-test", factory.Server.AuthKey)
	require.NoError(t, d.Close())
}
```

The test logger must prove auth keys and ClickHouse passwords are never emitted.

- [ ] **Step 2: Write failing runtime recovery, retry, status, and shutdown tests**

```go
func TestRuntimeDrainsRecoveredReadyRecordsAfterRestart(t *testing.T) {
	seedReady(t, validRecord())
	u := &recordingUploader{results: []error{io.ErrUnexpectedEOF, nil}}
	r := newRuntime(t, u)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go require.NoError(t, r.Run(ctx))
	require.Eventually(t, func() bool { return u.Successes() == 1 && readyCount(t) == 0 }, time.Second, 10*time.Millisecond)
}

func TestClickHouseOutageRetainsReadyDataAndDoesNotCountAsLoss(t *testing.T) {
	u := &recordingUploader{always: upload.ErrRetryable}
	r := newRuntime(t, u)
	r.runOneUploadCycle(t)
	require.Equal(t, 1, readyCount(t))
	require.EqualValues(t, 0, r.Status().DroppedRecords)
	require.GreaterOrEqual(t, r.Status().UploadRetries, uint64(1))
}

func TestIdleRuntimeProbesDeliveryWithoutCreatingArchiveRows(t *testing.T) {
	u := &recordingUploader{}
	r := newRuntimeWithClock(t, u, fakeClockAt(time.Now()))
	r.advance(30 * time.Second)
	require.Equal(t, 1, u.Probes())
	require.True(t, r.Status().DeliveryReady)
	require.Zero(t, u.Uploads())
}
```

Also assert only one upload runs at once, retry delays jitter between 2 and 60 seconds, existing `ready` data drains independently of new IPC arrivals, status checkpoints contain no body/header data, SIGTERM completes current fsync/ack work, and hard timeout leaves recoverable manifests. Runtime master-off admission itself is tested at the gateway façade in Task 9; the sidecar never connects to PostgreSQL or invents a second runtime-policy source.

- [ ] **Step 3: Run tests and confirm RED**

```bash
cd backend
go test ./internal/capture/upload ./internal/capture/sidecar
```

Expected: FAIL because tsnet transport and orchestration do not exist.

- [ ] **Step 4: Pin tsnet and implement its HTTP dialer**

```bash
cd backend
go get tailscale.com@v1.102.2
```

Construct `tsnet.Server{Dir: <data>/tsnet, Hostname: ..., AuthKey: ..., Ephemeral: false}` and route the HTTP transport's `DialContext` through `server.Dial(ctx, network, address)`. Start lazily in the sidecar only, close on shutdown, store node state under `/app/data/capture/tsnet`, and redact secret values from structured errors/logging.

- [ ] **Step 5: Implement receiver/recovery/uploader orchestration**

```go
func (r *Runtime) Run(ctx context.Context) error {
	if _, err := r.store.Recover(ctx); err != nil { return err }
	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() error { return r.receiver.Serve(ctx) })
	group.Go(func() error { return r.uploadLoop(ctx) })
	group.Go(func() error { return r.statusLoop(ctx) })
	return group.Wait()
}
```

Initialize spool before the socket, recover acked/unacked batches before selecting new ones, and keep one uploader. Retry remote failures without deleting records. When no batch is ready, probe `/ping` at most once every 30 seconds through the same tsnet HTTP client; upload work takes priority and no probe runs concurrently with an upload. Set `delivery_ready` on the most recent successful upload/probe and clear it on a failed attempt. The statically enabled sidecar accepts valid IPC sessions whenever capacity permits; the main process's runtime policy gate decides whether to create new sessions, while the uploader always drains existing spool. Persist an atomic small status checkpoint with a stable random `health_source_id`, current/previous minute cumulative reason buckets, gauges, counters, and timestamps only. Reload the same source ID/counters after sidecar restart so Task 11 can use idempotent `GREATEST` upserts; rotate the previous minute bucket only after it has appeared in a successful status response. Never checkpoint raw body/header/secret content.

- [ ] **Step 6: Run race tests, tidy dependencies, and commit**

```bash
cd backend
go test -race ./internal/capture/upload ./internal/capture/sidecar
go mod tidy
go test ./internal/capture/...
cd ..
git diff --check
git add backend/internal/capture backend/go.mod backend/go.sum
git commit -m "feat(capture): run spool uploader through embedded tsnet"
```

Expected: runtime recovery and outage tests PASS; `go.mod` pins `tailscale.com v1.102.2`.

### Task 8: Add the Same-Binary Sidecar Command and Supervisor

**Files:**
- Create: `backend/cmd/server/capture_sidecar.go`
- Create: `backend/cmd/server/capture_sidecar_test.go`
- Create: `backend/internal/service/capture_sidecar_supervisor.go`
- Create: `backend/internal/service/capture_sidecar_supervisor_test.go`
- Create: `backend/internal/service/capture_sidecar_supervisor_linux.go`
- Create: `backend/internal/service/capture_sidecar_supervisor_other.go`
- Modify: `backend/cmd/server/main.go`
- Create: `backend/cmd/server/main_test.go`
- Modify: `backend/cmd/server/wire.go`
- Modify generated: `backend/cmd/server/wire_gen.go`
- Modify generated: `backend/cmd/server/wire_gen_test.go`
- Modify: `backend/internal/service/wire.go`
- Verify unchanged topology: `deploy/docker-compose.yml`

**Interfaces:**
- Consumes: Task 7 `sidecar.Runtime` and Task 1 static capture config.
- Produces: CLI `sub2api capture-sidecar` and `service.CaptureSidecarSupervisor.Start/Stop`.
- Produces: supervisor state fields `Running`, `RestartCount`, `LastExitAt`, and `LastErrorClass` for Task 11.

- [ ] **Step 1: Write failing command-dispatch and minimal-initialization tests**

```go
func TestCaptureSidecarDispatchesBeforeServerFlags(t *testing.T) {
	deps := &fakeMainDeps{}
	code := run([]string{"sub2api", "capture-sidecar"}, deps)
	require.Equal(t, 0, code)
	require.True(t, deps.SidecarStarted)
	require.False(t, deps.PostgresOpened)
	require.False(t, deps.RedisOpened)
	require.False(t, deps.HTTPServerStarted)
}

func TestStaticDisabledDoesNotTouchCaptureFilesystem(t *testing.T) {
	root := filepath.Join(t.TempDir(), "capture")
	c := startGatewayForTest(t, configWithCapture(false, root))
	require.NoDirExists(t, root)
	require.Nil(t, c.CaptureSupervisor)
}

func TestCaptureSidecarHelpNeedsNoSecretsAndTouchesNoState(t *testing.T) {
	deps := &fakeMainDeps{}
	code := run([]string{"sub2api", "capture-sidecar", "--help"}, deps)
	require.Equal(t, 0, code)
	require.Contains(t, deps.Stdout(), "capture-sidecar")
	require.False(t, deps.ConfigLoaded)
	require.False(t, deps.FilesystemTouched)
}
```

- [ ] **Step 2: Write failing supervisor lifecycle/update-safety tests**

```go
func TestSupervisorUsesProcSelfExeAndParentDeathSignal(t *testing.T) {
	runner := &fakeRunner{executable: "/app/sub2api"}
	s := NewCaptureSidecarSupervisor(validConfig(), runner)
	require.NoError(t, s.Start(context.Background()))
	require.Equal(t, "/proc/self/exe", runner.Command.Path)
	require.Equal(t, []string{"capture-sidecar"}, runner.Command.Args)
	require.Equal(t, syscall.SIGTERM, runner.Command.Pdeathsig)
}

func TestSupervisorRestartsWithBackoffButGatewayStartupSucceeds(t *testing.T) {
	runner := crashingRunner(3)
	s := NewCaptureSidecarSupervisor(validConfig(), runner)
	require.NoError(t, s.Start(context.Background()))
	require.Eventually(t, func() bool { return s.Status().RestartCount >= 3 }, time.Second, 10*time.Millisecond)
	require.Equal(t, []time.Duration{2*time.Second, 4*time.Second, 8*time.Second}, runner.DelaysWithoutJitter())
}
```

Also test the 60-second cap, jitter bounds, SIGTERM then 10-second kill, parent unexpected exit, a replaced `/app/sub2api` path still restarting `/proc/self/exe`, and sidecar start failure not failing gateway construction.

- [ ] **Step 3: Run focused tests and confirm RED**

```bash
cd backend
go test ./cmd/server ./internal/service -run 'Sidecar|Supervisor|StaticDisabled|ProcSelfExe'
```

Expected: FAIL because subcommand dispatch and supervisor do not exist.

- [ ] **Step 4: Implement early subcommand dispatch and capture-only config**

```go
func run(args []string, deps mainDependencies) int {
	if len(args) > 1 && args[1] == "capture-sidecar" {
		return runCaptureSidecar(args[2:], deps)
	}
	return runGateway(args[1:], deps)
}
```

Dispatch before the existing `flag` parser and before setup/version/server dependency initialization. `runCaptureSidecar` loads only logging and `gateway.capture` static values, applies `debug.SetMemoryLimit(256<<20)`, validates secrets without printing them, and runs Task 7's runtime until SIGTERM/SIGINT.

- [ ] **Step 5: Implement the supervisor without changing Compose topology**

```go
cmd := exec.Command("/proc/self/exe", "capture-sidecar")
applyParentDeathSignal(cmd)
cmd.Env = os.Environ()
cmd.Stdout, cmd.Stderr = logWriter, logWriter
```

Start only when static capture is true. Restart 2-60 seconds with jitter, reset backoff after a stable interval, expose status atomically, and adjust child `/proc/<pid>/oom_score_adj` to `500` best-effort without failing startup. Do not attach the child to an `exec.CommandContext` whose cancellation skips graceful signaling. On parent shutdown send SIGTERM, wait 10 seconds, then kill. The gateway continues using its existing nil/no-op capture dependency if static capture is false or the child is down.

- [ ] **Step 6: Confirm Compose remains one service and commit**

```bash
cd backend
go generate ./cmd/server
go test -race ./cmd/server ./internal/service -run 'Sidecar|Supervisor|StaticDisabled|ProcSelfExe'
go build ./cmd/server
cd ..
docker compose -f deploy/docker-compose.yml config --services
git diff --check
git add backend/cmd/server/capture_sidecar.go backend/cmd/server/capture_sidecar_test.go backend/cmd/server/main.go backend/cmd/server/main_test.go backend/cmd/server/wire.go backend/cmd/server/wire_gen.go backend/cmd/server/wire_gen_test.go backend/internal/service/capture_sidecar_supervisor.go backend/internal/service/capture_sidecar_supervisor_test.go backend/internal/service/capture_sidecar_supervisor_linux.go backend/internal/service/capture_sidecar_supervisor_other.go backend/internal/service/wire.go
git commit -m "feat(capture): supervise same-binary sidecar"
```

Expected: service output contains only the existing application service; disabled startup has no capture filesystem/process activity; supervisor tests PASS.

### Task 9: Replace Whole-Body Capture Bridges with Streaming Attempt Handles

**Files:**
- Modify: `backend/internal/service/conversation_capture_pool.go`
- Modify: `backend/internal/service/conversation_capture_pool_test.go`
- Modify: `backend/internal/service/conversation_capture_unit_support.go`
- Modify: `backend/internal/service/capture_context.go`
- Modify: `backend/internal/service/capture_context_test.go`
- Modify: `backend/internal/service/capture_record.go`
- Modify: `backend/internal/service/capture_record_test.go`
- Modify: `backend/internal/service/openai_http_capture.go`
- Modify: `backend/internal/service/openai_http_capture_test.go`
- Modify: `backend/internal/service/gateway_upstream_response.go`
- Modify: `backend/internal/handler/gateway_handler.go`
- Modify: `backend/internal/handler/openai_gateway_handler.go`

**Interfaces:**
- Consumes: Task 2 `protocol.Transport` and `protocol.Attempt`; Task 1 content policy.
- Produces: `service.ConversationCapturePool.Begin(context.Context, model.Begin) (*CaptureAttempt, bool)` and a `CaptureAttempt` façade with `ID() uuid.UUID`, four `Write*([]byte) bool` methods, `Finalize(model.Final) bool`, `Commit() bool`, and `Abort()`; it owns no raw body slice.
- Produces: `service.PrepareCaptureScope(ctx context.Context, transport protocol.Transport, begin model.Begin) (context.Context, *CaptureAttempt)`.
- Preserves: `NewConversationCapturePoolForUnitTest(chan *CaptureRecord)` through a bounded test transport that reconstructs records outside production code.

- [ ] **Step 1: Write failing tests proving no queue and no response ownership**

```go
func TestConversationCapturePoolHasNoWorkerOrWriterQueue(t *testing.T) {
	transport := &recordingTransport{}
	p := NewConversationCapturePool(transport, policyOn())
	a, ok := p.Begin(context.Background(), testBegin())
	require.True(t, ok)
	require.True(t, a.WriteResponse([]byte("chunk")))
	require.True(t, a.Commit())
	require.Equal(t, 1, transport.Begins())
	require.Equal(t, 0, runtimeQueueCount(p))
}

func TestRuntimeMasterOffDoesNotBeginButLeavesTransportOpen(t *testing.T) {
	transport := &recordingTransport{}
	p := NewConversationCapturePool(transport, policyOff())
	_, ok := p.Begin(context.Background(), testBegin())
	require.False(t, ok)
	require.Equal(t, 0, transport.Begins())
	require.False(t, transport.Closed())
}

func TestResponseTeeForwardsOnlyBytesReadByProviderConsumer(t *testing.T) {
	source := &trackingReadCloser{Reader: strings.NewReader("abcdef")}
	a := &recordingAttempt{}
	r := newCaptureResponseReader(source, a)
	buf := make([]byte, 3)
	_, err := io.ReadFull(r, buf)
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), a.ResponseBytes())
	require.EqualValues(t, 3, source.BytesRead())
}
```

Also assert capture does not perform an extra read, delay close, retry a failed IPC write, or retain request/response capacity after the wrapper returns.

- [ ] **Step 2: Write failing exact-once final-attempt tests**

```go
func TestRetryAbortsOldAttemptAndCommitsClientVisibleAttempt(t *testing.T) {
	transport := &recordingTransport{}
	result := runGatewayRetryFixture(t, transport, []upstreamResult{{status: 503}, {status: 200}})
	require.Equal(t, 200, result.Status)
	require.Equal(t, []terminalState{Aborted, Committed}, transport.TerminalStates())
	require.Equal(t, result.RawFinalRequest, transport.Attempts()[1].RequestBytes())
	require.Equal(t, result.RawFinalResponse, transport.Attempts()[1].ResponseBytes())
}
```

Cover final real upstream errors as committed, locally synthesized errors as not committed, disconnect before commit as `pre_commit_disconnect`, and content-policy-off columns as never sent.

- [ ] **Step 3: Run focused tests and confirm RED**

```bash
cd backend
go test ./internal/service ./internal/handler -run 'ConversationCapturePool|CaptureScope|ResponseTee|FinalAttempt|PreCommit'
```

Expected: FAIL because the current Pond pool and `captureResultBridge` own complete bodies.

- [ ] **Step 4: Turn the pool into a compatibility façade over IPC**

```go
type CaptureAttempt struct {
	attempt protocol.Attempt
	failed  atomic.Bool
}

func (a *CaptureAttempt) WriteResponse(p []byte) bool {
	if a == nil || a.failed.Load() { return false }
	if !a.attempt.WriteResponse(p) {
		a.failed.Store(true)
		return false
	}
	return true
}
```

Remove Pond startup/shutdown, worker counts, queue byte gauges, record submission, and internal buffering from production construction. `Begin` first consults the existing atomic runtime policy; master-off returns nil without closing the transport or sidecar, so its uploader can drain. When enabled, `Begin` obtains one IPC session and returns nil on unavailable/backpressure. The request scope owns an attempt, all terminal paths call exactly one `Commit` or `Abort`, and a failed capture handle becomes a cheap no-op without touching the proxied result.

- [ ] **Step 5: Stream both request and response at existing wire boundaries**

```go
type captureResponseReader struct {
	upstream io.ReadCloser
	attempt  *CaptureAttempt
}

func (r *captureResponseReader) Read(p []byte) (int, error) {
	n, err := r.upstream.Read(p)
	if n > 0 { r.attempt.WriteResponse(p[:n]) }
	return n, err
}
```

Frame the already-existing final wire request slice without cloning it. Replace `captureResultBridge`, `sseTee`, and OpenAI HTTP response buffers with wrappers that mirror only bytes naturally written/read. Sanitize headers in the gateway before passing them to the attempt. The final side-effect sink that already commits usage/billing and returns the client-visible outcome is also the only location allowed to call capture `Commit`.

- [ ] **Step 6: Preserve fixture ergonomics with a test-only bounded transport**

```go
func NewConversationCapturePoolForUnitTest(out chan<- *CaptureRecord) *ConversationCapturePool {
	return NewConversationCapturePool(newRecordReconstructingTestTransport(out, 32<<20), policyAll())
}
```

The adapter may reconstruct at most 32 MiB per direction for existing assertions, must live in unit-support code, and must never be selected by server/container wiring. Convert tests that inspect queue depths to IPC failure/terminal-state assertions.

- [ ] **Step 7: Run race tests and commit**

```bash
cd backend
go test -race ./internal/service ./internal/handler -run 'Capture|FinalAttempt|ResponseTee|PreCommit'
cd ..
git diff --check
git add backend/internal/service/conversation_capture_pool.go backend/internal/service/conversation_capture_pool_test.go backend/internal/service/conversation_capture_unit_support.go backend/internal/service/capture_context.go backend/internal/service/capture_context_test.go backend/internal/service/capture_record.go backend/internal/service/capture_record_test.go backend/internal/service/openai_http_capture.go backend/internal/service/openai_http_capture_test.go backend/internal/service/gateway_upstream_response.go backend/internal/handler/gateway_handler.go backend/internal/handler/openai_gateway_handler.go
git commit -m "refactor(capture): stream final attempts to sidecar"
```

Expected: migrated bridge tests PASS under race; production code contains no record queue or whole-response capture buffer.

### Task 10: Audit and Migrate Every Provider-Native Capture Boundary

**Files:**
- Modify: `backend/internal/service/gateway_service.go`
- Modify: `backend/internal/service/openai_gateway_service.go`
- Modify: `backend/internal/service/openai_gateway_passthrough.go`
- Modify: `backend/internal/service/kiro_capture.go`
- Modify: `backend/internal/service/kiro_runtime.go`
- Modify: `backend/internal/service/web_chat_capture.go`
- Modify: `backend/internal/service/web_chat_dispatch.go`
- Modify: `backend/internal/handler/gateway_handler_chat_completions.go`
- Modify: `backend/internal/handler/gateway_handler_responses.go`
- Modify: `backend/internal/handler/gemini_v1beta_handler.go`
- Modify: provider capture tests named below.
- Create: `backend/internal/service/capture_heap_retention_test.go`

**Interfaces:**
- Consumes: Task 9 `CaptureAttempt` and test transport.
- Produces: no new public API; every real upstream attempt uses the same begin/abort/commit lifecycle.
- Preserves: KIRO final AWS request/event-stream bytes, Anthropic API-key passthrough, Bedrock, Antigravity, Gemini native, OpenAI Responses/Chat Completions/Grok/raw-cc, WebSocket, and web-chat semantics.

- [ ] **Step 1: Generate and review a complete production-call-site inventory**

```bash
cd /home/alvin/tokenstation3
rg -n 'CaptureRecord|capturePool|captureResultBridge|PrepareCaptureScope|Write(Request|Response)|\.Commit\(|\.Abort\(' backend/internal/handler backend/internal/service --glob '*.go'
```

Classify every result as: migrated wire boundary, test-only compatibility adapter, metadata-only admin/health code, or obsolete code to delete in Task 12. Save the reviewed inventory as a table in the Task 10 commit message body so a future upstream merge can audit new call sites.

- [ ] **Step 2: Extend provider integration tests to assert attempt ownership**

```go
func assertOnlyClientVisibleAttemptCommitted(t *testing.T, tr *recordingTransport, wantRequest, wantResponse []byte) {
	t.Helper()
	require.Equal(t, 1, tr.CommitCount())
	committed := tr.CommittedAttempt()
	require.Equal(t, wantRequest, committed.RequestBytes())
	require.Equal(t, wantResponse, committed.ResponseBytes())
	for _, a := range tr.AttemptsBefore(committed.ID()) { require.Equal(t, Aborted, a.State()) }
}
```

Apply this assertion to:

- `backend/internal/handler/antigravity_capture_integration_test.go`
- `backend/internal/handler/bedrock_capture_integration_test.go`
- `backend/internal/handler/gemini_native_capture_integration_test.go`
- `backend/internal/handler/gateway_anthropic_apikey_stream_integration_test.go`
- `backend/internal/handler/kiro_terminal_capture_integration_test.go`
- `backend/internal/handler/openai_capture_snapshot_test.go`
- `backend/internal/handler/openai_forward_side_effects_integration_test.go`
- `backend/internal/handler/openai_grok_final_result_integration_test.go`
- `backend/internal/handler/openai_raw_cc_failover_integration_test.go`
- `backend/internal/service/account_test_service_kiro_test.go` (both non-streaming and streaming WebSearch/MCP final AWS pair cases)
- `backend/internal/service/antigravity_retry_capture_test.go`
- `backend/internal/service/gateway_bedrock_capture_test.go`
- `backend/internal/service/gateway_anthropic_apikey_passthrough_test.go`
- `backend/internal/service/kiro_compat_terminal_capture_test.go`
- `backend/internal/service/openai_gateway_grok_error_capture_test.go`
- `backend/internal/service/openai_gateway_partial_capture_test.go`
- `backend/internal/service/openai_ws_protocol_forward_test.go`
- `backend/internal/service/web_chat_final_request_test.go`

- [ ] **Step 3: Run the provider capture tests and confirm RED where callers remain old**

```bash
cd backend
go test ./internal/handler ./internal/service -run 'Capture|FinalRequest|FinalResult|Failover|ForwardSideEffects'
```

Expected: at least each not-yet-migrated provider fails terminal-state or raw-wire assertions.

- [ ] **Step 4: Migrate each actual upstream send/read boundary**

```go
attempt, _ := capture.Begin(ctx, beginForUpstreamAttempt(req, account, provider))
if attempt != nil {
	attempt.WriteRequestHeaders(sanitizeCaptureHeaders(outbound.Header))
	attempt.WriteRequest(finalWireBody)
}
resp, err := sendUpstream(ctx, outbound)
if shouldRetry(err, resp) {
	attempt.Abort()
	continue
}
return finishClientVisibleAttempt(ctx, attempt, resp, err)
```

Use each provider's existing retry/failover decision; do not create a second interpretation. For KIRO, preserve the final AWS-signed request bytes and raw AWS event-stream response, existing error mapping, account status updates, and fallback behavior. For streaming paths, tee bytes at the provider reader/client writer already used by the gateway, without additional reads. For WebSocket, begin one attempt per real upstream session and commit/abort at the existing terminal side-effect boundary.

- [ ] **Step 5: Add an end-to-end retained-heap regression test**

```go
func TestFiveHundredLargeCapturesDoNotAccumulateInGatewayHeap(t *testing.T) {
	transport := &discardingBoundedTransport{}
	baseline := forceGCAndReadHeap(t)
	for i := 0; i < 500; i++ {
		runCaptureFixture(t, transport, 8<<20, 8<<20)
	}
	after := forceGCAndReadHeap(t)
	require.Less(t, after-baseline, uint64(64<<20))
	require.Equal(t, 500, transport.TerminalAttempts())
}
```

Run this test repeatedly (`-count=5`) and compare post-GC retained heap, not transient allocation totals. Its transport must accept chunks without storing them, so it detects gateway retention rather than test-fixture storage.

- [ ] **Step 6: Run all provider/race tests and commit**

```bash
cd backend
go test -race ./internal/handler ./internal/service -run 'Capture|FinalRequest|FinalResult|Failover|ForwardSideEffects'
go test ./internal/service -run FiveHundredLargeCaptures -count=5
cd ..
git diff --check
git add backend/internal/handler backend/internal/service
git commit -m "refactor(capture): migrate all provider attempt boundaries"
```

Expected: all provider fixtures preserve prior client/billing behavior, exactly one terminal real attempt commits, and retained heap stays within the test bound.

### Task 11: Replace Queue Health with Spool and Delivery Operations

**Files:**
- Modify: `backend/internal/service/capture_health.go`
- Modify: `backend/internal/service/capture_health_port.go`
- Modify: `backend/internal/service/capture_health_reporter.go`
- Modify: `backend/internal/service/capture_health_test.go`
- Modify: `backend/internal/service/capture_health_reporter_test.go`
- Modify: `backend/internal/repository/capture_health_repo.go`
- Modify: `backend/internal/repository/capture_health_repo_test.go`
- Modify: `backend/internal/service/capture_admin_service.go`
- Modify: `backend/internal/service/capture_admin_service_test.go`
- Modify: `backend/internal/handler/admin/capture_handler.go`
- Modify: `backend/internal/handler/admin/capture_handler_test.go`
- Modify: `backend/internal/service/ops_alert_evaluator_service.go`
- Modify: `backend/internal/service/ops_alert_evaluator_service_test.go`
- Create: `backend/migrations/229_capture_spool_alert_rules.sql`
- Create: `backend/migrations/capture_spool_alert_rules_migration_test.go`
- Modify: `frontend/src/api/admin/captureSettings.ts`
- Modify: `frontend/src/api/__tests__/admin.captureSettings.spec.ts`
- Modify: `frontend/src/stores/captureHealth.ts`
- Modify: `frontend/src/stores/__tests__/captureHealth.spec.ts`
- Modify: `frontend/src/views/admin/CaptureSettingsView.vue`
- Modify: `frontend/src/views/admin/__tests__/CaptureSettingsView.spec.ts`
- Modify: `frontend/src/i18n/locales/zh/admin/captureSettings.ts`
- Modify: `frontend/src/i18n/locales/en/admin/captureSettings.ts`

**Interfaces:**
- Consumes: Task 7 runtime status and Task 8 supervisor status.
- Produces: admin fields `sidecar_running`, `spool_ready`, `delivery_ready`, `spool_used_bytes`, `spool_max_bytes`, `filesystem_free_bytes`, `ready_records`, `oldest_ready_age_seconds`, `current_batch_id`, `sidecar_restart_count`, `upload_retries`, and `last_upload_at`.
- Produces: metrics `capture_ready`, `capture_delivery_ready`, `capture_spool_usage_percent`, and reasoned `capture_dropped_records`; removes `capture_writer_failures` after alert migration.

- [ ] **Step 1: Write failing backend semantic tests**

```go
func TestInfrastructureReadyMeansLocalAcceptanceNotRemoteDelivery(t *testing.T) {
	view := buildCaptureView(staticOn(), runtimeOn(), supervisorRunning(), status{
		SpoolReady: true, DeliveryReady: false, ReadyRecords: 42,
	})
	require.True(t, view.Ready)
	require.True(t, view.SidecarRunning)
	require.True(t, view.SpoolReady)
	require.False(t, view.DeliveryReady)
	require.EqualValues(t, 42, view.ReadyRecords)
}

func TestUploadRetryIsNotReportedAsDroppedCapture(t *testing.T) {
	health := newCaptureHealthTracker("instance", time.Now)
	health.recordUploadRetry(io.ErrUnexpectedEOF)
	s := health.snapshot()
	require.EqualValues(t, 1, s.UploadRetries)
	require.Zero(t, s.DroppedRecords)
}
```

Add exact loss-reason cases: `ipc_unavailable`, `ipc_backpressure`, `sidecar_down`, `spool_cap`, `spool_free_reserve`, `spool_corrupt`, and `pre_commit_disconnect`. Verify logged/stored errors contain no secrets, raw headers, or bodies.

- [ ] **Step 2: Write failing migration/evaluator tests**

```go
func TestCaptureSpoolMigrationCreatesSeventyEightyFiveNinetyFiveRules(t *testing.T) {
	sql := readMigration(t, "229_capture_spool_alert_rules.sql")
	for _, threshold := range []string{"70", "85", "95"} { require.Contains(t, sql, threshold) }
	require.Contains(t, sql, "capture_spool_usage_percent")
	require.Contains(t, sql, "capture_delivery_ready")
	for _, column := range []string{"spool_used_bytes_peak", "ready_records_peak", "oldest_ready_age_seconds_peak", "upload_retries", "sidecar_restarts"} {
		require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS "+column)
	}
}

func TestSpoolUsageMetricUsesPhysicalCap(t *testing.T) {
	got := evaluateMetric("capture_spool_usage_percent", snapshot{SpoolUsedBytes: 9 << 30, SpoolMaxBytes: 12 << 30})
	require.Equal(t, 75.0, got)
}
```

Migration 229 must add non-negative `BIGINT NOT NULL DEFAULT 0` health-event columns `spool_used_bytes_peak`, `ready_records_peak`, `oldest_ready_age_seconds_peak`, `upload_retries`, and `sidecar_restarts` without dropping the old queue columns, so rollback/history remains readable. It must insert three spool thresholds at 70/85/95 percent, add a delivery-down rule, update the seeded `capture_ready` description to local sidecar/spool semantics, disable obsolete writer-failure rules without deleting alert history, and be idempotent. The reporter uses sidecar `health_source_id` as the event instance identity, and repository upserts use `GREATEST` for cumulative drop/retry/restart values and gauges, making repeated status polls idempotent.

- [ ] **Step 3: Write failing frontend contract and rendering tests**

```ts
it('distinguishes local acceptance from remote delivery', async () => {
  mockSettings({ ready: true, sidecar_running: true, spool_ready: true, delivery_ready: false,
    spool_used_bytes: 9 * 2 ** 30, spool_max_bytes: 12 * 2 ** 30, ready_records: 42 })
  const wrapper = mountCaptureSettings()
  await flushPromises()
  expect(wrapper.text()).toContain('ClickHouse 传输异常')
  expect(wrapper.text()).toContain('本地 Spool 正常，数据将自动续传')
  expect(wrapper.text()).toContain('9 GiB / 12 GiB')
  expect(wrapper.text()).not.toContain('Writer queue')
})
```

Cover static-off (`sidecar not started`), runtime-off (`not accepting new captures; draining backlog`), sidecar-down, cap/free-reserve, backlog age, restart count, and upload retry copy in both locale dictionaries.

- [ ] **Step 4: Run all new tests and confirm RED**

```bash
cd backend
go test ./internal/service ./internal/repository ./internal/handler/admin ./migrations -run 'Capture|Spool|Delivery'
cd ../frontend
pnpm exec vitest run src/api/__tests__/admin.captureSettings.spec.ts src/stores/__tests__/captureHealth.spec.ts src/views/admin/__tests__/CaptureSettingsView.spec.ts
```

Expected: FAIL on missing status fields, metrics, migration, and UI text.

- [ ] **Step 5: Implement status ingestion and operational semantics**

```go
type CaptureSettingsView struct {
	Ready                 bool       `json:"ready"`
	SidecarRunning        bool       `json:"sidecar_running"`
	SpoolReady            bool       `json:"spool_ready"`
	DeliveryReady         bool       `json:"delivery_ready"`
	SpoolUsedBytes        int64      `json:"spool_used_bytes"`
	SpoolMaxBytes         int64      `json:"spool_max_bytes"`
	FilesystemFreeBytes   int64      `json:"filesystem_free_bytes"`
	ReadyRecords          int64      `json:"ready_records"`
	OldestReadyAgeSeconds int64      `json:"oldest_ready_age_seconds"`
	CurrentBatchID        string     `json:"current_batch_id"`
	SidecarRestartCount   uint64     `json:"sidecar_restart_count"`
	UploadRetries         uint64     `json:"upload_retries"`
	LastUploadAt          *time.Time `json:"last_upload_at"`
}

type CaptureHealthEvent struct {
	MinuteBucket             time.Time `json:"minute_bucket"`
	InstanceID               string    `json:"instance_id"`
	Reason                   string    `json:"reason"`
	DroppedRecords           int64     `json:"dropped_records"`
	DroppedBytes             int64     `json:"dropped_bytes"`
	SpoolUsedBytesPeak       int64     `json:"spool_used_bytes_peak"`
	ReadyRecordsPeak         int64     `json:"ready_records_peak"`
	OldestReadyAgeSecondsPeak int64    `json:"oldest_ready_age_seconds_peak"`
	UploadRetries            int64     `json:"upload_retries"`
	SidecarRestarts          int64     `json:"sidecar_restarts"`
	LastError                string    `json:"last_error"`
}
```

Define `Ready = SidecarRunning && SpoolReady`; `DeliveryReady` is separate. Obtain live status through the bounded protocol status request, fall back to the body's non-sensitive atomic checkpoint if the sidecar is down, and combine it with supervisor state. Runtime master-off updates acceptance policy while leaving the child and uploader running. Count only irrecoverable/local admission events as dropped; ClickHouse/tsnet retries increment delivery retry metrics.

- [ ] **Step 6: Update migration, repository contract, API, and UI**

```bash
cd backend
go test ./migrations -run CaptureSpool
cd ../frontend
pnpm exec vitest run src/api/__tests__/admin.captureSettings.spec.ts src/stores/__tests__/captureHealth.spec.ts src/views/admin/__tests__/CaptureSettingsView.spec.ts
```

Replace queue cards/labels/types with spool usage, available filesystem bytes, backlog record count/age, sidecar lifecycle, delivery state, and last successful upload. Do not expose the ClickHouse password, tsnet auth key, full private address, raw body, raw headers, or spool file path through the admin API.

- [ ] **Step 7: Run focused backend/frontend suites and commit**

```bash
cd backend
go test -race ./internal/service ./internal/repository ./internal/handler/admin ./migrations -run 'Capture|Spool|Delivery'
cd ../frontend
pnpm exec vitest run src/api/__tests__/admin.captureSettings.spec.ts src/stores/__tests__/captureHealth.spec.ts src/views/admin/__tests__/CaptureSettingsView.spec.ts
cd ..
git diff --check
git add backend/internal/service backend/internal/repository backend/internal/handler/admin backend/migrations/229_capture_spool_alert_rules.sql backend/migrations/capture_spool_alert_rules_migration_test.go frontend/src/api frontend/src/stores frontend/src/views/admin/CaptureSettingsView.vue frontend/src/views/admin/__tests__/CaptureSettingsView.spec.ts frontend/src/i18n/locales/zh/admin/captureSettings.ts frontend/src/i18n/locales/en/admin/captureSettings.ts
git commit -m "feat(capture): expose spool and delivery health"
```

Expected: backend and frontend tests PASS; old writer queue wording and metrics no longer appear in active settings/alert paths.

### Task 12: Remove the Old Writer and Run Release/Failure Verification

**Files:**
- Delete: `backend/internal/service/clickhouse_archive_writer.go`
- Delete: `backend/internal/service/clickhouse_archive_writer_test.go`
- Delete: `backend/internal/service/capture_archive_writer_manager.go`
- Delete: `backend/internal/service/capture_archive_writer_manager_test.go`
- Delete: `backend/internal/service/capture_byte_gauge.go`
- Delete: `backend/internal/service/capture_byte_gauge_test.go`
- Modify: `backend/go.mod`
- Modify: `backend/go.sum`
- Modify: `docs/superpowers/specs/2026-08-16-capture-disk-spool-sidecar-design.md`
- Modify: `docs/capture-clickhouse-windows-deployment.md`

**Interfaces:**
- Consumes: the complete sidecar implementation from Tasks 1-11.
- Produces: a single release binary and clean dependency graph with no native ClickHouse capture writer, Pond capture queue, or obsolete active queue settings.

- [ ] **Step 1: Prove all production references have been migrated**

```bash
cd /home/alvin/tokenstation3
rg -n 'ClickHouseArchiveWriter|captureArchiveWriterManager|captureByteGauge|pond\.New|writer_queue_size|max_queue_bytes|clickhouse-go' backend --glob '*.go' --glob 'go.mod'
rg -n 'NewConversationCapturePoolForUnitTest' backend/internal --glob '*.go'
```

Expected: the first command returns only files scheduled for deletion or explicit config migration tests; the second returns test files only. If a production call site appears, migrate and test it before deleting anything.

- [ ] **Step 2: Delete obsolete code and tidy the dependency graph**

```diff
*** Begin Patch
*** Delete File: backend/internal/service/clickhouse_archive_writer.go
*** Delete File: backend/internal/service/clickhouse_archive_writer_test.go
*** Delete File: backend/internal/service/capture_archive_writer_manager.go
*** Delete File: backend/internal/service/capture_archive_writer_manager_test.go
*** Delete File: backend/internal/service/capture_byte_gauge.go
*** Delete File: backend/internal/service/capture_byte_gauge_test.go
*** End Patch
```

Apply that exact deletion patch, then run:

```bash
cd backend
go mod tidy
```

Confirm `github.com/ClickHouse/clickhouse-go/v2` is removed if no unrelated package imports it; retain Pond only if a non-capture subsystem still uses it.

- [ ] **Step 3: Update implementation-state documentation**

```markdown
- 状态：已实现并通过本地单元、竞态、故障恢复与 ClickHouse 26.3.17.110 集成验证
- 默认：`gateway.capture.enabled=false`；关闭时不启动 sidecar/tsnet，也不创建 spool
- 恢复：`ready` 和未 ack 的固定 batch 会在容器重启后自动续传
- 资源：`max_active_attempts=32`、`batch_max_bytes=134217728`，12 GiB cap 内预留 16 MiB 发送元数据空间
- Secret 环境变量：`GATEWAY_CAPTURE_TAILSCALE_AUTH_KEY` 与 `GATEWAY_CAPTURE_CLICKHOUSE_PASSWORD`
```

Only state the ClickHouse integration as passed if Task 6 actually ran against the pinned image. Otherwise document it as a deployment prerequisite with the exact pending command. Keep the Windows runbook's `18000 -> 18123 -> 8123` topology and explicit no-native-9000 warning.

- [ ] **Step 4: Run generators, full static checks, and all test suites**

```bash
cd /home/alvin/tokenstation3
make check-generate
make build-backend
make -C backend test-unit
make -C backend test-integration
cd backend
golangci-lint run ./...
cd ..
make test-frontend
make build-frontend
docker compose -f deploy/docker-compose.yml config
git diff --check
```

Expected: all commands PASS. If integration services are not available, preserve exact failing output and do not reinterpret an environment skip as a product pass.

- [ ] **Step 5: Execute the local crash/restart acceptance matrix**

```bash
cd backend
go test -tags=integration ./internal/capture/sidecar ./internal/capture/spool ./internal/capture/upload \
  -run 'CrashBeforeCommit|CrashAfterReadyRename|CrashAfterRemoteCommit|CrashAfterAck|ClickHouseOutage|SidecarRestart|GatewayRestart' -count=3 -v
```

The fixtures must verify:

- failure before sidecar receives durable commit may record `pre_commit_disconnect` but never changes the client result;
- a ready record survives sidecar, gateway, and container-process restart simulations;
- an unacked sending manifest reuses the exact batch ID and record set;
- an acked batch only finishes cleanup and never reuploads;
- ClickHouse/tsnet outage grows backlog and retry counters without incrementing drops;
- 12 GiB cap and 8 GiB reserve reject new captures only and preserve old ready data;
- static off starts no sidecar/socket/tsnet; runtime off drains but accepts no new records;
- SIGTERM waits up to 10 seconds and leaves a recoverable state at every forced exit point.

- [ ] **Step 6: Build the release artifact and inspect its command/config surface**

```bash
cd backend
go build -o /tmp/sub2api-capture-plan-check ./cmd/server
/tmp/sub2api-capture-plan-check capture-sidecar --help
/tmp/sub2api-capture-plan-check --version
cd ..
docker compose -f deploy/docker-compose.yml config --services
```

Expected: one binary exposes both modes, Compose still has one application service, and help output contains no secret values.

- [ ] **Step 7: Commit cleanup and verification evidence**

```bash
git diff --check
git status --short
git add backend docs/superpowers/specs/2026-08-16-capture-disk-spool-sidecar-design.md docs/capture-clickhouse-windows-deployment.md
git commit -m "refactor(capture): remove in-memory archive pipeline"
git show --stat --oneline HEAD
```

Expected: commit deletes only the old capture queue/native writer, contains dependency/doc updates supported by executed evidence, and leaves unrelated user files unstaged.

---

## Final Review Gate

- [ ] Confirm every requirement in `docs/superpowers/specs/2026-08-16-capture-disk-spool-sidecar-design.md` maps to a completed task and test.
- [ ] Confirm `docs/kiro-upstream-sync.md` was followed and KIRO terminal capture fixtures are unchanged except for transport plumbing.
- [ ] Confirm static-off, runtime-off, ClickHouse-offline, sidecar-crash, gateway-restart, partial-write, spool-cap, free-reserve, corrupt-record, pre-ack crash, and post-ack crash paths have explicit passing evidence.
- [ ] Confirm no API/log/status/argv exposes raw capture content, sanitized-away headers, ClickHouse password, tsnet auth key, or spool filenames.
- [ ] Confirm no production environment was changed. Production enablement, secrets, restart, Windows deployment, and a real provider smoke test remain a separately approved rollout.
- [ ] Confirm Git history contains focused commits, the final tracked worktree is clean, and unrelated untracked files remain untouched.
