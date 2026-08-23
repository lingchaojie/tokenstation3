package upload

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// goldenRowHex is hand-derived from the runbook's 34 columns. It deliberately
// does not use insertColumns or any production encoding helper.
const goldenRowHex = "15cd5b0700000000" + // captured_at: DateTime64(3)
	"7766554433221100ffeeddccbbaa9988" + // capture_id: UUID
	"8899aabbccddeeff0011223344556677" + // ingest_batch_id: UUID
	"0172" + // request_id: "r"
	"0173" + // session_id: "s"
	"0170" + // platform: "p"
	"016d" + // requested_model: "m"
	"0175" + // upstream_model: "u"
	"022f76" + // upstream_endpoint: "/v"
	"01" + // stream: UInt8(true)
	"0102" + // http_status: UInt16(513)
	"0178" + // stop_reason: "x"
	"0165" + // thinking_effort: "e"
	"0174" + // thinking_type: "t"
	"01000000" + // input_tokens: UInt32(1)
	"02000000" + // output_tokens: UInt32(2)
	"03000000" + // cache_read_tokens: UInt32(3)
	"04000000" + // cache_creation_tokens: UInt32(4)
	"01" + // signature_present: UInt8(true)
	"01" + // is_truncated: UInt8(true)
	"00" + // request_truncated: UInt8(false)
	"01" + // response_truncated: UInt8(true)
	"0100000000000000" + // request_observed_bytes: UInt64(1)
	"0100000000000000" + // request_stored_bytes: UInt64(1)
	"1300000000000000" + // response_observed_bytes: UInt64(19)
	"0200000000000000" + // response_stored_bytes: UInt64(2)
	"38633235373438393230363366393935666466373536626365303766343663316135313933653534636435323833376564393165333230303863636634316163" + // request_sha256: FixedString(64)
	"37393732343264336431383132636334376265653936346166613964666333326334623030336265656137633537386164343937366530636332373232313031" + // response_sha256: FixedString(64)
	"0200" + // spool_version: UInt16(2)
	"0200" + // capture_version: UInt16(2)
	"0152" + // raw_request: "R"
	"027b22" + // raw_response prefix: 0x7b 0x22
	"03484452" + // request_headers: "HDR"
	"0448454144" // response_headers: "HEAD"

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

func TestPrimitiveEncodersRejectSilentShortWrites(t *testing.T) {
	tests := []struct {
		name  string
		write func(io.Writer) error
	}{
		{name: "datetime64", write: func(dst io.Writer) error { return writeDateTime64Millis(dst, time.UnixMilli(123456789)) }},
		{name: "uuid", write: func(dst io.Writer) error {
			return writeUUID(dst, uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff"))
		}},
		{name: "string", write: func(dst io.Writer) error { return writeString(dst, "metadata") }},
		{name: "fixed string", write: func(dst io.Writer) error { return writeFixedString64(dst, strings.Repeat("a", 64)) }},
		{name: "uint64", write: func(dst io.Writer) error { return writeUint64(dst, 123) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.ErrorIs(t, tt.write(silentShortWriter{}), io.ErrShortWrite)
		})
	}
}

func TestEncodeBatchMatchesIndependentGoldenRow(t *testing.T) {
	batch := goldenFixtureBatch(t)
	var out bytes.Buffer

	require.NoError(t, (RowBinaryEncoder{}).EncodeBatch(context.Background(), &out, batch))
	require.Equal(t, goldenRowHex, hex.EncodeToString(out.Bytes()))
}

func TestEncodeBatchWritesEmptyStringsForPolicyDisabledRawFields(t *testing.T) {
	batch := fixtureBatch(t, fixtureOptions{
		Policy:   model.ContentPolicy{},
		Request:  []byte("not stored request"),
		Response: []byte("not stored response"),
	})
	var out bytes.Buffer

	require.NoError(t, (RowBinaryEncoder{}).EncodeBatch(context.Background(), &out, batch))
	encoded := out.Bytes()
	require.Equal(t, []byte{0, 0, 0, 0}, encoded[len(encoded)-4:])
}

func TestValidateBatchRejectsEveryMalformedFixedHash(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*spool.RecordRef)
	}{
		{name: "request hash length", mutate: func(ref *spool.RecordRef) { ref.Manifest.Request.SHA256 = "abc" }},
		{name: "response hash uppercase", mutate: func(ref *spool.RecordRef) {
			ref.Manifest.Response.SHA256 = strings.ToUpper(ref.Manifest.Response.SHA256)
		}},
		{name: "request header hash", mutate: func(ref *spool.RecordRef) { ref.Manifest.RequestHeaders.SHA256 = strings.Repeat("g", 64) }},
		{name: "response header hash", mutate: func(ref *spool.RecordRef) { ref.Manifest.ResponseHeaders.SHA256 = "" }},
		{name: "compressed file hash", mutate: func(ref *spool.RecordRef) { ref.Manifest.Files[0].CompressedSHA256 = strings.Repeat("A", 64) }},
		{name: "uncompressed file hash", mutate: func(ref *spool.RecordRef) { ref.Manifest.Files[0].UncompressedSHA256 = strings.Repeat("0", 63) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batch := goldenFixtureBatch(t)
			tt.mutate(&batch.Records[0])
			err := (RowBinaryEncoder{}).ValidateBatch(batch)
			require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
		})
	}
}

func TestEncodeBatchClassifiesCompressedContentCorruptionAsSpoolCorrupt(t *testing.T) {
	batch := goldenFixtureBatch(t)
	path := filepath.Join(batch.Records[0].Path, "request.zst")
	compressed, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotEmpty(t, compressed)
	compressed[len(compressed)/2] ^= 0xff
	require.NoError(t, os.WriteFile(path, compressed, 0o600))

	err = (RowBinaryEncoder{}).EncodeBatch(context.Background(), &bytes.Buffer{}, batch)
	require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
	var corrupt *CorruptRecordError
	require.ErrorAs(t, err, &corrupt)
	require.Equal(t, batch.Records[0].CaptureID, corrupt.CaptureID)
	var schemaErr *SchemaError
	require.False(t, errors.As(err, &schemaErr))
}

func TestPreflightCorruptionIdentifiesExactCaptureWithoutErrorStringParsing(t *testing.T) {
	batch := goldenFixtureBatch(t)
	wantID := batch.Records[0].CaptureID
	secretPath := batch.Records[0].Path
	batch.Records[0].Manifest.Request.SHA256 = "invalid"

	err := (RowBinaryEncoder{}).Preflight(context.Background(), batch)

	var corrupt *CorruptRecordError
	require.ErrorAs(t, err, &corrupt)
	require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
	require.Equal(t, wantID, corrupt.CaptureID)
	require.NotContains(t, err.Error(), wantID.String())
	require.NotContains(t, err.Error(), secretPath)
}

func TestWriteRawFieldRejectsInvalidZstdWhenDeclaredLengthIsZero(t *testing.T) {
	batch := fixtureBatch(t, fixtureOptions{
		Policy: model.ContentPolicy{StoreRequestBody: true},
		Final:  model.Final{ResponseComplete: true},
	})
	rewriteFixtureFile(t, batch, "request.zst", []byte("not-zstd"), nil)
	ref := &batch.Records[0]
	var fileStat model.FileStat
	for _, candidate := range ref.Manifest.Files {
		if candidate.Name == "request.zst" {
			fileStat = candidate
			break
		}
	}
	require.Equal(t, "request.zst", fileStat.Name)

	err := writeRawField(
		context.Background(),
		io.Discard,
		ref.Path,
		rawField{name: "request.zst", enabled: true, stat: ref.Manifest.Request},
		fileStat,
		true,
		make([]byte, rowBinaryBufferSize),
	)
	require.ErrorIs(t, err, spool.ErrSpoolCorrupt)
}

func goldenFixtureBatch(t *testing.T) *spool.Batch {
	t.Helper()
	batch := fixtureBatch(t, fixtureOptions{
		CapturedAt: time.UnixMilli(123456789).UTC(),
		CaptureID:  uuid.MustParse("00112233-4455-6677-8899-aabbccddeeff"),
		Policy: model.ContentPolicy{
			StoreRequestBody:     true,
			StoreResponseBody:    true,
			StoreRequestHeaders:  true,
			StoreResponseHeaders: true,
		},
		RequestID:        "r",
		Platform:         "p",
		RequestedModel:   "m",
		UpstreamModel:    "u",
		Endpoint:         "/v",
		Stream:           true,
		BodyLimitBytes:   2,
		HeaderLimitBytes: 4,
		Request:          []byte("R"),
		Response:         []byte(`{"stop_reason":"x"}`),
		RequestHeaders:   []byte("HDR"),
		ResponseHeaders:  []byte("HEAD"),
		Final: model.Final{
			HTTPStatus:          513,
			InputTokens:         1,
			OutputTokens:        2,
			CacheReadTokens:     3,
			CacheCreationTokens: 4,
			ResponseComplete:    true,
		},
	})
	batch.Records[0].Manifest.Extracted.SessionID = "s"
	batch.Records[0].Manifest.Extracted.ThinkingEffort = "e"
	batch.Records[0].Manifest.Extracted.ThinkingType = "t"
	batch.Records[0].Manifest.Extracted.SignaturePresent = true
	rewriteFixtureManifest(t, batch)
	batch.ID = uuid.MustParse("ffeeddcc-bbaa-9988-7766-554433221100")
	return batch
}

type fixtureOptions struct {
	CapturedAt       time.Time
	CaptureID        uuid.UUID
	Policy           model.ContentPolicy
	RequestID        string
	Platform         string
	RequestedModel   string
	UpstreamModel    string
	Endpoint         string
	Stream           bool
	BodyLimitBytes   int64
	HeaderLimitBytes int64

	Request         []byte
	Response        []byte
	RequestHeaders  []byte
	ResponseHeaders []byte
	Final           model.Final
	WriteFixture    func(t *testing.T, sink interface {
		WriteRequestHeaders([]byte) error
		WriteResponseHeaders([]byte) error
		WriteRequest([]byte) error
		WriteResponse([]byte) error
	})
}

func fixtureBatch(t *testing.T, opts fixtureOptions) *spool.Batch {
	t.Helper()
	if opts.CapturedAt.IsZero() {
		opts.CapturedAt = time.UnixMilli(123456789).UTC()
	}
	if opts.CaptureID == uuid.Nil {
		opts.CaptureID = uuid.New()
	}
	store, err := spool.Open(spool.Config{
		RootDir:        t.TempDir(),
		MaxBodyBytes:   opts.BodyLimitBytes,
		MaxHeaderBytes: opts.HeaderLimitBytes,
	})
	require.NoError(t, err)
	sink, err := store.Open(model.Begin{
		CaptureID:        opts.CaptureID,
		CapturedAt:       opts.CapturedAt,
		RequestID:        opts.RequestID,
		Platform:         opts.Platform,
		RequestedModel:   opts.RequestedModel,
		UpstreamModel:    opts.UpstreamModel,
		UpstreamEndpoint: opts.Endpoint,
		Stream:           opts.Stream,
		Format:           model.PayloadJSON,
		Policy:           opts.Policy,
	})
	require.NoError(t, err)
	if opts.WriteFixture != nil {
		opts.WriteFixture(t, sink)
	} else {
		require.NoError(t, sink.WriteRequestHeaders(opts.RequestHeaders))
		require.NoError(t, sink.WriteRequest(opts.Request))
		require.NoError(t, sink.WriteResponseHeaders(opts.ResponseHeaders))
		require.NoError(t, sink.WriteResponse(opts.Response))
	}
	require.NoError(t, sink.Finalize(opts.Final))
	require.NoError(t, sink.Commit())
	batch, err := store.NextBatch(100, 256<<20)
	require.NoError(t, err)
	require.NotNil(t, batch)
	return batch
}

type silentShortWriter struct{}

func (silentShortWriter) Write(payload []byte) (int, error) {
	if len(payload) <= 1 {
		return 0, nil
	}
	return len(payload) / 2, nil
}
