package upload

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/spool"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

const (
	rowBinaryBufferSize = 64 << 10
	maxRawBodyBytes     = 32 << 20
)

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

type RowBinaryEncoder struct {
	openRawFile func(string) (io.ReadCloser, error)
}

func (e RowBinaryEncoder) ValidateBatch(batch *spool.Batch) error {
	return e.Preflight(context.Background(), batch)
}

func (RowBinaryEncoder) Preflight(ctx context.Context, batch *spool.Batch) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if batch == nil || batch.ID == uuid.Nil || len(batch.Records) == 0 {
		return corruptError("batch identity or records")
	}
	seen := make(map[uuid.UUID]struct{}, len(batch.Records))
	for i := range batch.Records {
		ref := &batch.Records[i]
		if _, exists := seen[ref.CaptureID]; exists {
			return corruptError("duplicate capture ID")
		}
		seen[ref.CaptureID] = struct{}{}
		if err := spool.ValidateRecordRef(ctx, *ref); err != nil {
			return fmt.Errorf("validate capture %s: %w", ref.CaptureID, err)
		}
	}
	return nil
}

func (e RowBinaryEncoder) EncodeBatch(ctx context.Context, dst io.Writer, batch *spool.Batch) error {
	if dst == nil {
		return errors.New("rowbinary destination is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := e.Preflight(ctx, batch); err != nil {
		return err
	}
	return e.encodeBatchValidated(ctx, dst, batch)
}

func (e RowBinaryEncoder) encodeBatchValidated(ctx context.Context, dst io.Writer, batch *spool.Batch) error {
	if dst == nil {
		return errors.New("rowbinary destination is required")
	}
	if batch == nil || batch.ID == uuid.Nil || len(batch.Records) == 0 {
		return corruptError("batch identity or records")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	buffer := make([]byte, rowBinaryBufferSize)
	for i := range batch.Records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := e.encodeRecord(ctx, dst, batch.ID, &batch.Records[i], buffer); err != nil {
			return fmt.Errorf("encode capture %s: %w", batch.Records[i].CaptureID, err)
		}
	}
	return nil
}

func (e RowBinaryEncoder) encodeRecord(ctx context.Context, dst io.Writer, batchID uuid.UUID, ref *spool.RecordRef, buffer []byte) error {
	m := &ref.Manifest
	requestTruncated := m.Request.Truncated
	responseTruncated := m.Response.Truncated || !m.Response.Complete
	isTruncated := requestTruncated || responseTruncated || m.RequestHeaders.Truncated || m.ResponseHeaders.Truncated

	writes := []func() error{
		func() error { return writeDateTime64Millis(dst, m.Begin.CapturedAt) },
		func() error { return writeUUID(dst, ref.CaptureID) },
		func() error { return writeUUID(dst, batchID) },
		func() error { return writeString(dst, m.Begin.RequestID) },
		func() error { return writeString(dst, m.Extracted.SessionID) },
		func() error { return writeString(dst, m.Begin.Platform) },
		func() error { return writeString(dst, m.Begin.RequestedModel) },
		func() error { return writeString(dst, m.Begin.UpstreamModel) },
		func() error { return writeString(dst, m.Begin.UpstreamEndpoint) },
		func() error { return writeBool(dst, m.Begin.Stream) },
		func() error { return writeUint16(dst, m.Final.HTTPStatus) },
		func() error { return writeString(dst, m.Extracted.StopReason) },
		func() error { return writeString(dst, m.Extracted.ThinkingEffort) },
		func() error { return writeString(dst, m.Extracted.ThinkingType) },
		func() error { return writeUint32(dst, m.Extracted.InputTokens) },
		func() error { return writeUint32(dst, m.Extracted.OutputTokens) },
		func() error { return writeUint32(dst, m.Extracted.CacheReadTokens) },
		func() error { return writeUint32(dst, m.Extracted.CacheCreationTokens) },
		func() error { return writeBool(dst, m.Extracted.SignaturePresent) },
		func() error { return writeBool(dst, isTruncated) },
		func() error { return writeBool(dst, requestTruncated) },
		func() error { return writeBool(dst, responseTruncated) },
		func() error { return writeUint64(dst, m.Request.ObservedBytes) },
		func() error { return writeUint64(dst, m.Request.StoredBytes) },
		func() error { return writeUint64(dst, m.Response.ObservedBytes) },
		func() error { return writeUint64(dst, m.Response.StoredBytes) },
		func() error { return writeFixedString64(dst, m.Request.SHA256) },
		func() error { return writeFixedString64(dst, m.Response.SHA256) },
		func() error { return writeUint16(dst, m.SpoolVersion) },
		func() error { return writeUint16(dst, m.CaptureVersion) },
	}
	for _, write := range writes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := write(); err != nil {
			return err
		}
	}
	fields := []rawField{
		{name: "request.zst", enabled: m.Begin.Policy.StoreRequestBody, stat: m.Request},
		{name: "response.zst", enabled: m.Begin.Policy.StoreResponseBody, stat: m.Response},
		{name: "request_headers.zst", enabled: m.Begin.Policy.StoreRequestHeaders, stat: model.BodyStat(m.RequestHeaders)},
		{name: "response_headers.zst", enabled: m.Begin.Policy.StoreResponseHeaders, stat: model.BodyStat(m.ResponseHeaders)},
	}
	files := make(map[string]model.FileStat, len(m.Files))
	for _, file := range m.Files {
		files[file.Name] = file
	}
	for _, field := range fields {
		file, exists := files[field.name]
		if err := e.writeRawField(ctx, dst, ref.Path, field, file, exists, buffer); err != nil {
			return err
		}
	}
	return nil
}

type rawField struct {
	name    string
	enabled bool
	stat    model.BodyStat
}

func writeRawField(ctx context.Context, dst io.Writer, recordPath string, field rawField, fileStat model.FileStat, fileExists bool, buffer []byte) error {
	return (RowBinaryEncoder{}).writeRawField(ctx, dst, recordPath, field, fileStat, fileExists, buffer)
}

func (e RowBinaryEncoder) writeRawField(ctx context.Context, dst io.Writer, recordPath string, field rawField, fileStat model.FileStat, fileExists bool, buffer []byte) error {
	if !field.enabled {
		return writeStringLength(dst, 0)
	}
	if err := writeStringLength(dst, field.stat.StoredBytes); err != nil {
		return err
	}
	if !fileExists {
		if field.stat.StoredBytes != 0 {
			return corruptError("missing raw field")
		}
		return nil
	}
	path := filepath.Join(recordPath, field.name)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return corruptError("missing raw field")
		}
		return fmt.Errorf("lstat raw field %s: %w", field.name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || uint64(info.Size()) != fileStat.CompressedBytes {
		return corruptError("raw field type or length")
	}
	opener := e.openRawFile
	if opener == nil {
		opener = func(path string) (io.ReadCloser, error) { return os.Open(path) }
	}
	f, err := opener(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return corruptError("missing raw field")
		}
		return fmt.Errorf("open raw field %s: %w", field.name, err)
	}
	compressedHash := sha256.New()
	tracked := &errorTrackingReader{reader: &contextReader{ctx: ctx, reader: io.TeeReader(f, compressedHash)}}
	zr, err := zstd.NewReader(
		bufio.NewReaderSize(tracked, rowBinaryBufferSize),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(maxRawBodyBytes),
		zstd.WithDecoderMaxWindow(maxRawBodyBytes),
	)
	if err != nil {
		_ = f.Close()
		if tracked.err != nil {
			return fmt.Errorf("read raw field %s: %w", field.name, tracked.err)
		}
		return corruptError("raw field zstd header")
	}
	uncompressedHash := sha256.New()
	written, decodeErr, writeErr := copyDecodedField(dst, zr, uncompressedHash, field.stat.StoredBytes, buffer)
	var extra [1]byte
	extraN, extraErr := 0, error(nil)
	if decodeErr == nil && writeErr == nil && written == int64(field.stat.StoredBytes) {
		extraN, extraErr = zr.Read(extra[:])
	}
	zr.Close()
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if decodeErr != nil {
		if tracked.err != nil {
			return fmt.Errorf("read raw field %s: %w", field.name, tracked.err)
		}
		return fmt.Errorf("decode raw field %s: %w", field.name, spool.ErrSpoolCorrupt)
	}
	if tracked.err != nil {
		return fmt.Errorf("read raw field %s: %w", field.name, tracked.err)
	}
	if closeErr != nil {
		return fmt.Errorf("close raw field %s: %w", field.name, closeErr)
	}
	if written != int64(field.stat.StoredBytes) || extraN != 0 || !errors.Is(extraErr, io.EOF) {
		return corruptError("raw field uncompressed length")
	}
	if tracked.count != fileStat.CompressedBytes || hex.EncodeToString(compressedHash.Sum(nil)) != fileStat.CompressedSHA256 {
		return corruptError("raw field compressed checksum")
	}
	if hex.EncodeToString(uncompressedHash.Sum(nil)) != fileStat.UncompressedSHA256 {
		return corruptError("raw field uncompressed checksum")
	}
	return nil
}

func copyDecodedField(dst io.Writer, src io.Reader, digest io.Writer, expected uint64, buffer []byte) (int64, error, error) {
	var written int64
	remaining := expected
	for remaining > 0 {
		chunk := buffer
		if uint64(len(chunk)) > remaining {
			chunk = chunk[:remaining]
		}
		n, readErr := src.Read(chunk)
		if n > 0 {
			_, _ = digest.Write(chunk[:n])
			if err := writeAll(dst, chunk[:n]); err != nil {
				return written, nil, err
			}
			written += int64(n)
			remaining -= uint64(n)
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return written, nil, nil
			}
			return written, readErr, nil
		}
		if n == 0 {
			return written, io.ErrNoProgress, nil
		}
	}
	return written, nil, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type errorTrackingReader struct {
	reader io.Reader
	count  uint64
	err    error
}

func (r *errorTrackingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.count += uint64(n)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

func writeDateTime64Millis(dst io.Writer, value time.Time) error {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], uint64(value.UnixMilli()))
	return writeAll(dst, encoded[:])
}

func writeUUID(dst io.Writer, value uuid.UUID) error {
	var encoded [16]byte
	for i := range 8 {
		encoded[i] = value[7-i]
		encoded[8+i] = value[15-i]
	}
	return writeAll(dst, encoded[:])
}

func writeString(dst io.Writer, value string) error {
	if err := writeStringLength(dst, uint64(len(value))); err != nil {
		return err
	}
	return writeText(dst, value)
}

func writeStringLength(dst io.Writer, length uint64) error {
	var encoded [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(encoded[:], length)
	return writeAll(dst, encoded[:n])
}

func writeFixedString64(dst io.Writer, value string) error {
	if !validFixedHash(value) {
		return corruptError("fixed hash")
	}
	return writeText(dst, value)
}

func writeBool(dst io.Writer, value bool) error {
	var encoded byte
	if value {
		encoded = 1
	}
	return writeAll(dst, []byte{encoded})
}

func writeUint16(dst io.Writer, value uint16) error {
	var encoded [2]byte
	binary.LittleEndian.PutUint16(encoded[:], value)
	return writeAll(dst, encoded[:])
}

func writeUint32(dst io.Writer, value uint32) error {
	var encoded [4]byte
	binary.LittleEndian.PutUint32(encoded[:], value)
	return writeAll(dst, encoded[:])
}

func writeUint64(dst io.Writer, value uint64) error {
	var encoded [8]byte
	binary.LittleEndian.PutUint64(encoded[:], value)
	return writeAll(dst, encoded[:])
}

func validFixedHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func corruptError(detail string) error {
	return fmt.Errorf("%s: %w", detail, spool.ErrSpoolCorrupt)
}

func writeAll(dst io.Writer, payload []byte) error {
	for len(payload) > 0 {
		n, err := dst.Write(payload)
		if err != nil {
			return err
		}
		if n <= 0 || n > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func writeText(dst io.Writer, value string) error {
	n, err := io.WriteString(dst, value)
	if err != nil {
		return err
	}
	if n != len(value) {
		return io.ErrShortWrite
	}
	return nil
}
