package service

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// ArchiveWriter 把一条 CaptureRecord 写入归档存储。实现必须自身吸收所有
// 错误（不得外溢到调用方转发路径）；此处返回的 error 仅用于内部计数/日志。
type ArchiveWriter interface {
	Write(ctx context.Context, rec *CaptureRecord) error
	Stop()
}

// noopArchiveWriter 在 capture 关闭时注入，零副作用。
type noopArchiveWriter struct{}

func (noopArchiveWriter) Write(context.Context, *CaptureRecord) error { return nil }
func (noopArchiveWriter) Stop()                                       {}

// captureVersion 写入 capture_version 列，用于未来 schema 演进时区分记录版本。
const captureVersion = 1

// errArchiveQueueFull 在批量写入通道已满时返回；调用方（capture worker）据此
// 计数/丢弃，绝不能阻塞转发主路径。
var errArchiveQueueFull = errors.New("capture: clickhouse batch queue full")

// b2u8 把 bool 映射为 ClickHouse UInt8 (0/1)。
func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}

// archiveCreateTableDDL 渲染归档表建表语句；列顺序与 flush() 里 batch.Append
// 的参数顺序严格一致，修改任一处都必须同步另一处。
func archiveCreateTableDDL(database, table string) string {
	return fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.%s (
  captured_at        DateTime64(3) DEFAULT now64(3),
  request_id         String,
  session_id         String,
  platform           LowCardinality(String),
  requested_model    LowCardinality(String),
  upstream_model     LowCardinality(String),
  upstream_endpoint  String,
  stream             UInt8,
  http_status        UInt16,
  stop_reason        LowCardinality(String),
  thinking_effort    LowCardinality(String),
  thinking_type      LowCardinality(String),
  input_tokens       UInt32,
  output_tokens      UInt32,
  cache_read_tokens  UInt32,
  cache_creation_tokens UInt32,
  signature_present  UInt8,
  is_truncated       UInt8,
  capture_version    UInt16,
  raw_request        String CODEC(ZSTD(3)),
  raw_response       String CODEC(ZSTD(3)),
  request_headers    String CODEC(ZSTD(3)),
  response_headers   String CODEC(ZSTD(3))
) ENGINE = MergeTree
PARTITION BY toYYYYMM(captured_at)
ORDER BY (session_id, captured_at, request_id)
SETTINGS index_granularity = 8192`, database, table)
}

// clickHouseArchiveWriter 把 CaptureRecord 异步批量写入 ClickHouse。
// Write 只做非阻塞入队；真正的建连/建表/发送错误全部在内部吸收并记日志，
// 绝不向调用方（转发主路径）传播 panic 或阻塞。
type clickHouseArchiveWriter struct {
	conn     clickhouse.Conn
	database string
	table    string

	batchCh     chan *CaptureRecord
	batchMax    int
	batchWait   time.Duration
	sendTimeout time.Duration

	stopOnce sync.Once
	done     chan struct{}
	wg       sync.WaitGroup
}

// newClickHouseArchiveWriter 建连、Ping、建表、启动 batcher。任一步失败返回 error，
// 调用方据此降级为 noopArchiveWriter，绝不阻塞启动。
func newClickHouseArchiveWriter(cc config.GatewayCaptureConfig) (ArchiveWriter, error) {
	chCfg := cc.ClickHouse

	dialTimeout := time.Duration(chCfg.DialTimeoutMs) * time.Millisecond
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	readTimeout := time.Duration(chCfg.ReadTimeoutMs) * time.Millisecond
	if readTimeout <= 0 {
		readTimeout = 10 * time.Second
	}

	var compression *clickhouse.Compression
	switch chCfg.Compression {
	case "lz4":
		compression = &clickhouse.Compression{Method: clickhouse.CompressionLZ4}
	case "zstd":
		compression = &clickhouse.Compression{Method: clickhouse.CompressionZSTD}
	case "none", "":
		compression = &clickhouse.Compression{Method: clickhouse.CompressionNone}
	default:
		return nil, fmt.Errorf("capture: clickhouse unknown compression %q", chCfg.Compression)
	}

	var tlsConfig *tls.Config
	if chCfg.Secure {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	maxOpenConns := chCfg.MaxOpenConns
	if maxOpenConns <= 0 {
		maxOpenConns = 4
	}

	opts := &clickhouse.Options{
		Addr: chCfg.Addr,
		Auth: clickhouse.Auth{
			Database: chCfg.Database,
			Username: chCfg.Username,
			Password: chCfg.Password,
		},
		DialTimeout:  dialTimeout,
		ReadTimeout:  readTimeout,
		Compression:  compression,
		TLS:          tlsConfig,
		MaxOpenConns: maxOpenConns,
	}

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("capture: clickhouse open: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if err := conn.Ping(pingCtx); err != nil {
		return nil, fmt.Errorf("capture: clickhouse ping: %w", err)
	}

	execCtx, cancelExec := context.WithTimeout(context.Background(), readTimeout)
	defer cancelExec()
	if err := conn.Exec(execCtx, archiveCreateTableDDL(chCfg.Database, chCfg.Table)); err != nil {
		return nil, fmt.Errorf("capture: clickhouse create table: %w", err)
	}

	batchMax := cc.BatchMaxSize
	if batchMax <= 0 {
		batchMax = 1
	}
	batchWait := time.Duration(cc.BatchMaxIntervalMs) * time.Millisecond
	if batchWait <= 0 {
		batchWait = time.Second
	}

	chanSize := batchMax * 4
	if chanSize < 1024 {
		chanSize = 1024
	}

	w := &clickHouseArchiveWriter{
		conn:        conn,
		database:    chCfg.Database,
		table:       chCfg.Table,
		batchCh:     make(chan *CaptureRecord, chanSize),
		batchMax:    batchMax,
		batchWait:   batchWait,
		sendTimeout: readTimeout,
		done:        make(chan struct{}),
	}

	w.wg.Add(1)
	go w.runBatcher()

	return w, nil
}

// Write 非阻塞入队；队列满时返回 errArchiveQueueFull，调用方计数丢弃即可。
func (w *clickHouseArchiveWriter) Write(_ context.Context, rec *CaptureRecord) error {
	select {
	case w.batchCh <- rec:
		return nil
	default:
		return errArchiveQueueFull
	}
}

// runBatcher 累积记录，达到 batchMax 或 ticker 到期时落盘；done 关闭后
// flush 剩余记录再退出。
func (w *clickHouseArchiveWriter) runBatcher() {
	defer w.wg.Done()

	ticker := time.NewTicker(w.batchWait)
	defer ticker.Stop()

	batch := make([]*CaptureRecord, 0, w.batchMax)

	for {
		select {
		case rec := <-w.batchCh:
			batch = append(batch, rec)
			if len(batch) >= w.batchMax {
				w.flush(batch)
				batch = make([]*CaptureRecord, 0, w.batchMax)
			}
		case <-ticker.C:
			if len(batch) > 0 {
				w.flush(batch)
				batch = make([]*CaptureRecord, 0, w.batchMax)
			}
		case <-w.done:
			// drain 已入队但未处理的记录，再做最后一次 flush。
			for {
				select {
				case rec := <-w.batchCh:
					batch = append(batch, rec)
				default:
					if len(batch) > 0 {
						w.flush(batch)
					}
					return
				}
			}
		}
	}
}

// flush 把一批记录 PrepareBatch+Append+Send 到 ClickHouse。任何错误在此
// 吸收（记日志），不向上传播。
func (w *clickHouseArchiveWriter) flush(batch []*CaptureRecord) {
	if len(batch) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), w.sendTimeout)
	defer cancel()

	insertSQL := fmt.Sprintf("INSERT INTO %s.%s", w.database, w.table)
	chBatch, err := w.conn.PrepareBatch(ctx, insertSQL)
	if err != nil {
		logger.L().With(
			zap.String("component", "service.clickhouse_archive_writer"),
			zap.Error(err),
			zap.Int("batch_size", len(batch)),
		).Error("capture.clickhouse.prepare_batch_failed")
		return
	}
	// Close 在 Send 之后为 no-op；在 Append 失败等早退路径上负责释放底层连接资源。
	defer func() { _ = chBatch.Close() }()

	for _, rec := range batch {
		if err := chBatch.Append(
			rec.CapturedAt,
			rec.RequestID,
			rec.SessionID,
			rec.Platform,
			rec.RequestedModel,
			rec.UpstreamModel,
			rec.UpstreamEndpoint,
			b2u8(rec.Stream),
			uint16(rec.HTTPStatus),
			rec.StopReason,
			rec.ThinkingEffort,
			rec.ThinkingType,
			uint32(rec.InputTokens),
			uint32(rec.OutputTokens),
			uint32(rec.CacheReadTokens),
			uint32(rec.CacheCreationTokens),
			b2u8(rec.SignaturePresent),
			b2u8(rec.Truncated),
			uint16(captureVersion),
			string(rec.RawRequest),
			string(rec.RawResponse),
			string(rec.RequestHeaders),
			string(rec.ResponseHeaders),
		); err != nil {
			logger.L().With(
				zap.String("component", "service.clickhouse_archive_writer"),
				zap.Error(err),
				zap.String("request_id", rec.RequestID),
			).Error("capture.clickhouse.append_failed")
			return
		}
	}

	if err := chBatch.Send(); err != nil {
		logger.L().With(
			zap.String("component", "service.clickhouse_archive_writer"),
			zap.Error(err),
			zap.Int("batch_size", len(batch)),
		).Error("capture.clickhouse.send_failed")
		return
	}
}

// Stop 停止 batcher，flush 剩余记录，关闭底层连接。安全多次调用。
func (w *clickHouseArchiveWriter) Stop() {
	w.stopOnce.Do(func() {
		close(w.done)
		w.wg.Wait()
		if w.conn != nil {
			if err := w.conn.Close(); err != nil {
				logger.L().With(
					zap.String("component", "service.clickhouse_archive_writer"),
					zap.Error(err),
				).Error("capture.clickhouse.close_failed")
			}
		}
	})
}
