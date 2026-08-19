package spool

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/extract"
	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

type Attempt struct {
	mu sync.Mutex

	store                   *Store
	begin                   model.Begin
	final                   model.Final
	partialPath             string
	overhead                Reservation
	terminal                bool
	published               bool
	inResponse              bool
	slotOnce                sync.Once
	extractor               extract.Stream
	extracted               model.Extracted
	extractorClosed         bool
	logger                  *slog.Logger
	extractionWarningLogged bool
	finalSet                bool

	request         *contentStream
	response        *contentStream
	requestHeaders  *contentStream
	responseHeaders *contentStream
}

type contentStream struct {
	name       string
	enabled    bool
	limit      int64
	observed   uint64
	stored     uint64
	truncated  bool
	complete   bool
	fullHash   hash.Hash
	storedHash hash.Hash
	file       *os.File
	encoder    *zstd.Encoder
	closed     bool
	allocated  int64
	reserved   []Reservation
}

func newAttempt(store *Store, begin model.Begin, partialPath string, overhead Reservation) *Attempt {
	newStream := func(name string, enabled bool, limit int64) *contentStream {
		return &contentStream{
			name:       name,
			enabled:    enabled,
			limit:      limit,
			fullHash:   sha256.New(),
			storedHash: sha256.New(),
		}
	}
	format := begin.Format
	if format == "" {
		format = model.PayloadJSON
	}
	metadataExtractor, extractionErr := extract.New(context.Background(), format)
	attempt := &Attempt{
		store:           store,
		begin:           begin,
		partialPath:     partialPath,
		overhead:        overhead,
		request:         newStream("request.zst", begin.Policy.StoreRequestBody, store.config.MaxBodyBytes),
		response:        newStream("response.zst", begin.Policy.StoreResponseBody, store.config.MaxBodyBytes),
		requestHeaders:  newStream("request_headers.zst", begin.Policy.StoreRequestHeaders, store.config.MaxHeaderBytes),
		responseHeaders: newStream("response_headers.zst", begin.Policy.StoreResponseHeaders, store.config.MaxHeaderBytes),
		extractor:       metadataExtractor,
		logger:          slog.Default(),
	}
	if extractionErr != nil {
		attempt.recordExtractionWarningLocked()
	}
	return attempt
}

func (a *Attempt) ID() uuid.UUID { return a.begin.CaptureID }

func (a *Attempt) createBodyFiles() error {
	for _, stream := range []*contentStream{a.request, a.response} {
		if !stream.enabled {
			continue
		}
		file, err := a.store.config.openFile(filepath.Join(a.partialPath, stream.name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", stream.name, err)
		}
		stream.file = file
	}
	return nil
}

func (a *Attempt) WriteRequestHeaders(payload []byte) error {
	return a.write(a.requestHeaders, payload, false)
}

func (a *Attempt) WriteResponseHeaders(payload []byte) error {
	return a.write(a.responseHeaders, payload, true)
}

func (a *Attempt) WriteRequest(payload []byte) error {
	return a.write(a.request, payload, false)
}

func (a *Attempt) WriteResponse(payload []byte) error {
	return a.write(a.response, payload, true)
}

func (a *Attempt) write(stream *contentStream, payload []byte, response bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return ErrAttemptClosed
	}
	if response && !a.inResponse {
		if err := a.closeRequestWritersLocked(); err != nil {
			a.abortLocked(err)
			return err
		}
		a.inResponse = true
	}
	if !response && a.inResponse {
		return errors.New("request content after response framing")
	}
	if len(payload) == 0 {
		return nil
	}
	if a.extractor != nil {
		var extractionErr error
		switch stream {
		case a.request:
			extractionErr = a.extractor.FeedRequest(payload)
		case a.response:
			extractionErr = a.extractor.FeedResponse(payload)
		}
		if extractionErr != nil {
			a.recordExtractionWarningLocked()
		}
	}
	_, _ = stream.fullHash.Write(payload)
	stream.observed += uint64(len(payload))
	if !stream.enabled {
		return nil
	}
	storeBytes := int64(len(payload))
	if stream.limit > 0 {
		remaining := stream.limit - int64(stream.stored)
		if remaining < 0 {
			remaining = 0
		}
		if storeBytes > remaining {
			storeBytes = remaining
			stream.truncated = true
		}
	}
	if storeBytes == 0 {
		return nil
	}
	reservation, err := a.store.capacity.ReserveFrame(a.ID(), int(storeBytes))
	if err != nil {
		a.store.recordDrop(err)
		a.abortLocked(err)
		return err
	}
	if err := a.ensureEncoderLocked(stream); err != nil {
		reservation.Release()
		a.abortLocked(err)
		return err
	}
	prefix := payload[:int(storeBytes)]
	if _, err := stream.encoder.Write(prefix); err != nil {
		reservation.Release()
		a.abortLocked(err)
		return fmt.Errorf("write %s: %w", stream.name, err)
	}
	_, _ = stream.storedHash.Write(prefix)
	stream.stored += uint64(storeBytes)
	stream.reserved = append(stream.reserved, reservation)
	return nil
}

func (a *Attempt) Finalize(final model.Final) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return ErrAttemptClosed
	}
	a.final = final
	a.finalSet = true
	return nil
}

func (a *Attempt) Commit() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return ErrAttemptClosed
	}
	if err := a.commitLocked(); err != nil {
		if !a.published {
			a.abortLocked(err)
		} else {
			a.terminal = true
			a.releaseResourcesLocked()
		}
		return err
	}
	a.terminal = true
	a.releaseResourcesLocked()
	return nil
}

func (a *Attempt) Abort(cause error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.terminal {
		return
	}
	a.abortLocked(cause)
}

func (a *Attempt) abortWithoutLock(cause error) {
	a.abortLocked(cause)
}

func (a *Attempt) abortLocked(_ error) {
	if a.terminal {
		return
	}
	a.abortExtractorLocked()
	for _, stream := range a.streams() {
		_ = a.closeEncoderLocked(stream)
		if stream.file != nil {
			_ = stream.file.Close()
			stream.file = nil
		}
	}
	if !a.published {
		cleanupErr := a.store.capacity.trackAllocationDeletion([]string{a.store.partialDir}, false, func() error {
			return os.RemoveAll(a.partialPath)
		})
		if cleanupErr != nil {
			if allocated, err := allocatedBytes(a.partialPath); err == nil {
				_ = a.overhead.Consume(allocated)
			} else if !errors.Is(err, os.ErrNotExist) {
				// Keep every pessimistic reservation charged until restart rather
				// than undercount an orphan whose exact remainder is unknowable.
				a.overhead = Reservation{}
				for _, stream := range a.streams() {
					stream.reserved = nil
				}
			}
		}
	}
	a.terminal = true
	a.releaseResourcesLocked()
}

func (a *Attempt) commitLocked() error {
	a.finishExtractionLocked()
	for _, stream := range a.streams() {
		if stream.file != nil && stream.encoder == nil && !stream.closed {
			if err := a.ensureEncoderLocked(stream); err != nil {
				return err
			}
		}
		if err := a.closeEncoderLocked(stream); err != nil {
			return err
		}
	}

	files := make([]model.FileStat, 0, 4)
	for _, stream := range a.streams() {
		if stream.file == nil {
			continue
		}
		if err := stream.file.Sync(); err != nil {
			return fmt.Errorf("fsync %s: %w", stream.name, err)
		}
		a.store.event("fsync:" + stream.name)
		fileStat, err := a.fileStatLocked(stream)
		if err != nil {
			return err
		}
		files = append(files, fileStat)
		if err := stream.file.Close(); err != nil {
			return fmt.Errorf("close %s: %w", stream.name, err)
		}
		stream.file = nil
	}
	a.request.complete = true
	a.requestHeaders.complete = true
	a.responseHeaders.complete = true
	a.response.complete = a.final.ResponseComplete
	manifest := model.Manifest{
		SpoolVersion:    spoolVersion,
		CaptureVersion:  captureVersion,
		CaptureID:       a.ID(),
		Begin:           a.begin,
		Final:           a.final,
		Extracted:       a.extracted,
		Request:         a.request.bodyStat(),
		Response:        a.response.bodyStat(),
		RequestHeaders:  model.HeaderStat(a.requestHeaders.bodyStat()),
		ResponseHeaders: model.HeaderStat(a.responseHeaders.bodyStat()),
		Files:           files,
	}
	encoded, err := json.Marshal(diskManifest{
		Manifest:         manifest,
		BodyLimitBytes:   uint64(a.store.config.MaxBodyBytes),
		HeaderLimitBytes: uint64(a.store.config.MaxHeaderBytes),
	})
	if err != nil {
		return fmt.Errorf("encode spool manifest: %w", err)
	}
	manifestTempPath := filepath.Join(a.partialPath, manifestTempName)
	manifestFile, err := a.store.config.openFile(manifestTempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create spool manifest: %w", err)
	}
	if _, err := manifestFile.Write(encoded); err != nil {
		_ = manifestFile.Close()
		return fmt.Errorf("write spool manifest: %w", err)
	}
	if err := manifestFile.Sync(); err != nil {
		_ = manifestFile.Close()
		return fmt.Errorf("fsync spool manifest: %w", err)
	}
	a.store.event("fsync:" + manifestTempName)
	if err := manifestFile.Close(); err != nil {
		return fmt.Errorf("close spool manifest: %w", err)
	}
	if err := os.Rename(manifestTempPath, filepath.Join(a.partialPath, manifestName)); err != nil {
		return fmt.Errorf("publish spool manifest: %w", err)
	}
	if err := syncDirectory(a.partialPath); err != nil {
		return fmt.Errorf("fsync partial record directory: %w", err)
	}
	a.store.event("fsync:partial-record-dir")
	allocated, err := allocatedBytes(a.partialPath)
	if err != nil {
		return fmt.Errorf("measure committed record: %w", err)
	}

	readyPath := filepath.Join(a.store.readyDir, a.ID().String())
	if err := a.store.capacity.trackAllocationMutation([]string{a.store.partialDir, a.store.readyDir}, false, func() error {
		return os.Rename(a.partialPath, readyPath)
	}); err != nil {
		return fmt.Errorf("publish ready record: %w", err)
	}
	a.published = true
	a.store.event("rename:partial-to-ready")
	if err := a.overhead.Consume(allocated); err != nil {
		return fmt.Errorf("account committed record: %w", err)
	}
	for _, stream := range a.streams() {
		for _, reservation := range stream.reserved {
			reservation.Release()
		}
		stream.reserved = nil
	}
	if err := syncDirectory(a.store.readyDir); err != nil {
		return fmt.Errorf("fsync ready directory: %w", err)
	}
	a.store.event("fsync:ready-dir")
	a.store.publish(RecordRef{
		CaptureID:      a.ID(),
		Path:           readyPath,
		Manifest:       manifest,
		StoredBytes:    manifestStoredBytes(manifest),
		AllocatedBytes: allocated,
		ReadyAt:        time.Now(),
	})
	return nil
}

func (a *Attempt) finishExtractionLocked() {
	if a.extractor == nil || a.extractorClosed {
		return
	}
	a.extractorClosed = true
	var extracted model.Extracted
	var err error
	if a.finalSet {
		extracted, err = a.extractor.Finalize(a.final)
	} else if finalizer, ok := a.extractor.(interface {
		FinalizeWithoutFinal() (model.Extracted, error)
	}); ok {
		extracted, err = finalizer.FinalizeWithoutFinal()
	} else {
		extracted, err = a.extractor.Finalize(model.Final{})
	}
	a.extracted = extracted
	if err != nil {
		a.recordExtractionWarningLocked()
	}
}

func (a *Attempt) recordExtractionWarningLocked() {
	if a.extractionWarningLogged {
		return
	}
	a.extractionWarningLogged = true
	a.logger.Warn(
		"capture metadata extraction failed",
		"capture_id", a.ID().String(),
		"error_category", "metadata_extraction_failed",
	)
}

func (a *Attempt) abortExtractorLocked() {
	if a.extractor == nil || a.extractorClosed {
		return
	}
	a.extractorClosed = true
	if aborter, ok := a.extractor.(interface{ Abort() }); ok {
		aborter.Abort()
		return
	}
	_, _ = a.extractor.Finalize(model.Final{})
}

func (a *Attempt) ensureEncoderLocked(stream *contentStream) error {
	if stream.encoder != nil {
		return nil
	}
	if stream.closed {
		return errors.New("content stream is closed")
	}
	if stream.file == nil {
		file, err := a.store.config.openFile(filepath.Join(a.partialPath, stream.name), os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return fmt.Errorf("create %s: %w", stream.name, err)
		}
		stream.file = file
	}
	encoder, err := zstd.NewWriter(stream.file, zstd.WithEncoderConcurrency(1), zstd.WithLowerEncoderMem(true))
	if err != nil {
		return fmt.Errorf("open zstd writer %s: %w", stream.name, err)
	}
	stream.encoder = encoder
	return nil
}

func (a *Attempt) closeRequestWritersLocked() error {
	for _, stream := range []*contentStream{a.requestHeaders, a.request} {
		if err := a.closeEncoderLocked(stream); err != nil {
			return err
		}
	}
	return nil
}

func (a *Attempt) closeEncoderLocked(stream *contentStream) error {
	if stream.closed {
		return nil
	}
	if stream.encoder != nil {
		if err := stream.encoder.Close(); err != nil {
			return fmt.Errorf("close zstd writer %s: %w", stream.name, err)
		}
		stream.encoder = nil
		a.store.event("close-writer:" + stream.name)
	}
	stream.closed = true
	return nil
}

func (a *Attempt) fileStatLocked(stream *contentStream) (model.FileStat, error) {
	info, err := stream.file.Stat()
	if err != nil {
		return model.FileStat{}, fmt.Errorf("stat %s: %w", stream.name, err)
	}
	stream.allocated = allocatedFileInfo(info)
	f, err := os.Open(filepath.Join(a.partialPath, stream.name))
	if err != nil {
		return model.FileStat{}, fmt.Errorf("open %s for checksum: %w", stream.name, err)
	}
	compressedHash := sha256.New()
	_, copyErr := io.Copy(compressedHash, f)
	closeErr := f.Close()
	if copyErr != nil {
		return model.FileStat{}, fmt.Errorf("checksum %s: %w", stream.name, copyErr)
	}
	if closeErr != nil {
		return model.FileStat{}, fmt.Errorf("close checksum reader %s: %w", stream.name, closeErr)
	}
	return model.FileStat{
		Name:               stream.name,
		CompressedBytes:    uint64(info.Size()),
		UncompressedBytes:  stream.stored,
		CompressedSHA256:   hex.EncodeToString(compressedHash.Sum(nil)),
		UncompressedSHA256: hex.EncodeToString(stream.storedHash.Sum(nil)),
	}, nil
}

func (a *Attempt) streams() []*contentStream {
	return []*contentStream{a.request, a.response, a.requestHeaders, a.responseHeaders}
}

func (a *Attempt) releaseResourcesLocked() {
	for _, stream := range a.streams() {
		for _, reservation := range stream.reserved {
			reservation.Release()
		}
		stream.reserved = nil
	}
	a.overhead.Release()
	a.slotOnce.Do(a.store.releaseAttemptSlot)
}

func (s *contentStream) bodyStat() model.BodyStat {
	return model.BodyStat{
		ObservedBytes: s.observed,
		StoredBytes:   s.stored,
		SHA256:        hex.EncodeToString(s.fullHash.Sum(nil)),
		Truncated:     s.truncated,
		Complete:      s.complete,
	}
}
