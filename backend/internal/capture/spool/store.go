package spool

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/Wei-Shaw/sub2api/internal/capture/protocol"
	"github.com/google/uuid"
	"github.com/klauspost/compress/zstd"
)

const (
	defaultMaxBytes                 int64  = 12 << 30
	defaultMinFreeBytes             int64  = 8 << 30
	defaultMaxBodyBytes             int64  = 32 << 20
	defaultMaxHeaderBytes           int64  = 1 << 20
	defaultMaxActiveAttempts               = 32
	defaultOperationalHeadroomBytes int64  = 16 << 20
	attemptOverheadBytes            int64  = 1 << 20
	manifestName                           = "manifest.json"
	manifestTempName                       = "manifest.tmp"
	legacySpoolVersion              uint16 = 1
	spoolVersion                    uint16 = 2
	captureVersion                  uint16 = 2
	maxManifestBytes                       = 2 << 20
)

var (
	ErrTooManyAttempts     = errors.New("ipc_backpressure")
	ErrSpoolCorrupt        = errors.New("spool_corrupt")
	ErrAttemptClosed       = errors.New("capture attempt is closed")
	errLegacyLimitsUnknown = errors.New("legacy spool manifest has ambiguous content limits")
)

type Config struct {
	RootDir                  string
	MaxBytes                 int64
	MinFreeBytes             int64
	MaxBodyBytes             int64
	MaxHeaderBytes           int64
	MaxActiveAttempts        int
	OperationalHeadroomBytes int64

	eventHook       func(string)
	openFile        func(string, int, os.FileMode) (*os.File, error)
	beforeBatchOpen func(string, string)
}

type RecoveryReport struct {
	Ready              []RecordRef
	AppliedCorruptions []AppliedCorruption
	OrphansDeleted     int
	CorruptDeleted     int
	UnavailableRecords int
}

type diskManifest struct {
	model.Manifest
	BodyLimitBytes   uint64 `json:"body_limit_bytes"`
	HeaderLimitBytes uint64 `json:"header_limit_bytes"`
}

type RecordRef struct {
	CaptureID      uuid.UUID
	Path           string
	Manifest       model.Manifest
	StoredBytes    int64
	AllocatedBytes int64
	ReadyAt        time.Time
}

type validationFile interface {
	io.Reader
	io.Seeker
	io.Closer
}

type validationOps struct {
	lstat          func(string) (os.FileInfo, error)
	open           func(string) (validationFile, error)
	readDir        func(string) ([]os.DirEntry, error)
	allocatedBytes func(string) (int64, error)
	decodedBytes   func(int64)
}

type Store struct {
	config Config

	partialDir              string
	readyDir                string
	sendingDir              string
	quarantineDir           string
	capacity                *Capacity
	validation              validationOps
	batchSyncDirectory      func(string) error
	readySyncDirectory      func(*os.File) error
	quarantineSyncDirectory func(*os.File) error

	attemptSlots chan struct{}
	lifecycleMu  sync.RWMutex

	readyMu sync.RWMutex
	ready   []RecordRef
	batchMu sync.Mutex

	recoverMu            sync.Mutex
	dropMu               sync.Mutex
	dropped              map[string]uint64
	accountedCorruptions map[CorruptionID]struct{}
}

var _ protocol.SessionFactory = (*Store)(nil)

func Open(config Config) (*Store, error) {
	if config.RootDir == "" {
		return nil, errors.New("spool root directory is required")
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = defaultMaxBytes
	}
	if config.MinFreeBytes == 0 {
		config.MinFreeBytes = defaultMinFreeBytes
	}
	if config.MaxBodyBytes == 0 {
		config.MaxBodyBytes = defaultMaxBodyBytes
	}
	if config.MaxHeaderBytes == 0 {
		config.MaxHeaderBytes = defaultMaxHeaderBytes
	}
	if config.MaxActiveAttempts == 0 {
		config.MaxActiveAttempts = defaultMaxActiveAttempts
	}
	if config.MaxActiveAttempts > defaultMaxActiveAttempts {
		config.MaxActiveAttempts = defaultMaxActiveAttempts
	}
	if config.OperationalHeadroomBytes == 0 {
		config.OperationalHeadroomBytes = defaultOperationalHeadroomBytes
	}
	if config.openFile == nil {
		config.openFile = os.OpenFile
	}
	if config.MaxBytes <= 0 || config.MinFreeBytes < 0 || config.MaxBodyBytes < 0 || config.MaxHeaderBytes < 0 || config.MaxActiveAttempts < 1 {
		return nil, errors.New("invalid spool configuration")
	}
	if config.MaxBytes > defaultMaxBytes ||
		config.MinFreeBytes < defaultMinFreeBytes ||
		config.MaxBodyBytes > defaultMaxBodyBytes ||
		config.MaxHeaderBytes > defaultMaxHeaderBytes ||
		config.OperationalHeadroomBytes != defaultOperationalHeadroomBytes {
		return nil, errors.New("spool configuration exceeds safety envelope")
	}
	if config.OperationalHeadroomBytes >= config.MaxBytes {
		return nil, errors.New("invalid spool operational headroom")
	}

	if err := ensurePrivateDirectory(config.RootDir); err != nil {
		return nil, fmt.Errorf("create spool root: %w", err)
	}
	partialDir := filepath.Join(config.RootDir, "partial")
	readyDir := filepath.Join(config.RootDir, "ready")
	sendingDir := filepath.Join(config.RootDir, "sending")
	quarantineDir := filepath.Join(config.RootDir, "quarantine")
	for _, directory := range []string{partialDir, readyDir, sendingDir, quarantineDir} {
		if err := ensurePrivateDirectory(directory); err != nil {
			return nil, fmt.Errorf("create spool directory %s: %w", filepath.Base(directory), err)
		}
	}
	capacity, err := newCapacity(CapacityConfig{
		RootDir:                  config.RootDir,
		MaxBytes:                 config.MaxBytes,
		MinFreeBytes:             config.MinFreeBytes,
		OperationalHeadroomBytes: config.OperationalHeadroomBytes,
	}, nil)
	if err != nil {
		return nil, err
	}
	validation := filesystemValidationOps()
	return &Store{
		config:                  config,
		partialDir:              partialDir,
		readyDir:                readyDir,
		sendingDir:              sendingDir,
		quarantineDir:           quarantineDir,
		capacity:                capacity,
		validation:              validation,
		batchSyncDirectory:      syncDirectory,
		readySyncDirectory:      syncOpenedDirectory,
		quarantineSyncDirectory: syncOpenedDirectory,
		attemptSlots:            make(chan struct{}, config.MaxActiveAttempts),
		dropped:                 make(map[string]uint64),
		accountedCorruptions:    make(map[CorruptionID]struct{}),
	}, nil
}

func (s *Store) Open(begin model.Begin) (protocol.SessionSink, error) {
	if begin.CaptureID == uuid.Nil {
		return nil, errors.New("capture ID is required")
	}
	s.lifecycleMu.RLock()
	releaseLifecycle := true
	defer func() {
		if releaseLifecycle {
			s.lifecycleMu.RUnlock()
		}
	}()
	select {
	case s.attemptSlots <- struct{}{}:
	default:
		s.recordDrop(ErrTooManyAttempts)
		return nil, ErrTooManyAttempts
	}
	releaseSlot := true
	defer func() {
		if releaseSlot {
			<-s.attemptSlots
		}
	}()

	overhead, err := s.capacity.ReserveContent(begin.CaptureID, attemptOverheadBytes)
	if err != nil {
		s.recordDrop(err)
		return nil, err
	}
	partialPath := filepath.Join(s.partialDir, begin.CaptureID.String())
	if err := os.Mkdir(partialPath, 0o700); err != nil {
		overhead.Release()
		return nil, fmt.Errorf("create partial record: %w", err)
	}
	a := newAttempt(s, begin, partialPath, overhead)
	if err := a.createBodyFiles(); err != nil {
		releaseSlot = false
		releaseLifecycle = false
		a.abortWithoutLock(err)
		return nil, err
	}
	releaseSlot = false
	releaseLifecycle = false
	return a, nil
}

func (s *Store) Recover(ctx context.Context) (RecoveryReport, error) {
	s.recoverMu.Lock()
	defer s.recoverMu.Unlock()
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.batchMu.Lock()
	defer s.batchMu.Unlock()

	report := RecoveryReport{}
	if err := s.recoverAckedLocked(); err != nil {
		return report, err
	}
	applied, err := s.recoverCorruptionsLocked()
	if err != nil {
		return report, err
	}
	report.AppliedCorruptions = append(report.AppliedCorruptions, applied...)
	partials, err := os.ReadDir(s.partialDir)
	if err != nil {
		return report, err
	}
	for _, entry := range partials {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		if err := os.RemoveAll(filepath.Join(s.partialDir, entry.Name())); err != nil {
			return report, fmt.Errorf("delete orphan partial %s: %w", entry.Name(), err)
		}
		report.OrphansDeleted++
	}
	if report.OrphansDeleted > 0 {
		_ = syncDirectory(s.partialDir)
	}

	entries, err := os.ReadDir(s.readyDir)
	if err != nil {
		return report, err
	}
	ready := make([]RecordRef, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		path := filepath.Join(s.readyDir, entry.Name())
		ref, validateErr := validateRecord(path, s.validation)
		if validateErr != nil {
			if errors.Is(validateErr, errLegacyLimitsUnknown) {
				report.UnavailableRecords++
				continue
			}
			if !errors.Is(validateErr, ErrSpoolCorrupt) {
				return report, fmt.Errorf("validate ready record %s: %w", entry.Name(), validateErr)
			}
			corruption, quarantineErr := s.quarantineReadyEntryLocked(nil, entry.Name())
			if quarantineErr != nil {
				return report, fmt.Errorf("quarantine corrupt ready record: %w", quarantineErr)
			}
			report.AppliedCorruptions = appendCorruption(report.AppliedCorruptions, corruption)
			report.CorruptDeleted++
			continue
		}
		ready = append(ready, ref)
	}
	sortRecordRefs(ready)
	report.Ready = cloneRecordRefs(ready)
	s.readyMu.Lock()
	s.ready = ready
	s.readyMu.Unlock()
	return report, nil
}

func (s *Store) Ready() []RecordRef {
	s.readyMu.RLock()
	defer s.readyMu.RUnlock()
	return cloneRecordRefs(s.ready)
}

func (s *Store) Snapshot() model.Status {
	current, err := s.capacity.snapshot()
	ready := s.Ready()
	status := model.Status{
		SpoolReady:      err == nil,
		SpoolMaxBytes:   s.config.MaxBytes,
		ReadyRecords:    int64(len(ready)),
		DroppedByReason: make(map[string]uint64),
	}
	if err == nil {
		status.SpoolUsedBytes = current.Allocated
		status.FilesystemFreeBytes = current.Free
	}
	if len(ready) > 0 {
		age := time.Since(ready[0].ReadyAt)
		if age > 0 {
			status.OldestReadyAgeSeconds = int64(age / time.Second)
		}
	}
	s.dropMu.Lock()
	for reason, count := range s.dropped {
		status.DroppedByReason[reason] = count
		status.DroppedRecords += count
	}
	s.dropMu.Unlock()
	return status
}

func (s *Store) publish(ref RecordRef) {
	s.readyMu.Lock()
	s.ready = append(s.ready, ref)
	sortRecordRefs(s.ready)
	s.readyMu.Unlock()
}

func (s *Store) recordDrop(err error) {
	reason := ""
	switch {
	case errors.Is(err, ErrSpoolCap):
		reason = ErrSpoolCap.Error()
	case errors.Is(err, ErrFreeReserve):
		reason = ErrFreeReserve.Error()
	case errors.Is(err, ErrTooManyAttempts):
		reason = ErrTooManyAttempts.Error()
	case errors.Is(err, ErrSpoolCorrupt):
		reason = ErrSpoolCorrupt.Error()
	}
	if reason == "" {
		return
	}
	s.dropMu.Lock()
	s.dropped[reason]++
	s.dropMu.Unlock()
}

func (s *Store) event(event string) {
	if s.config.eventHook != nil {
		s.config.eventHook(event)
	}
}

func (s *Store) releaseAttemptSlot() {
	<-s.attemptSlots
	s.lifecycleMu.RUnlock()
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

// ValidateRecordRef re-runs the canonical spool validation against the record
// on disk and verifies that the supplied immutable reference still matches it,
// preserving the validator's corruption, context, and filesystem error classes.
func ValidateRecordRef(ctx context.Context, ref RecordRef) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if ref.CaptureID == uuid.Nil || ref.Path == "" {
		return ErrSpoolCorrupt
	}
	canonical, err := validateRecord(ref.Path, contextualValidationOps(ctx))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if canonical.CaptureID != ref.CaptureID ||
		canonical.Path != ref.Path ||
		canonical.StoredBytes != ref.StoredBytes ||
		!equalManifest(canonical.Manifest, ref.Manifest) {
		return ErrSpoolCorrupt
	}
	return nil
}

func equalManifest(left, right model.Manifest) bool {
	if len(left.Files) == 0 {
		left.Files = nil
	}
	if len(right.Files) == 0 {
		right.Files = nil
	}
	return reflect.DeepEqual(left, right)
}

func filesystemValidationOps() validationOps {
	return validationOps{
		lstat:          os.Lstat,
		open:           func(path string) (validationFile, error) { return os.Open(path) },
		readDir:        os.ReadDir,
		allocatedBytes: allocatedBytes,
	}
}

func contextualValidationOps(ctx context.Context) validationOps {
	base := filesystemValidationOps()
	return validationOps{
		lstat: func(path string) (os.FileInfo, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			info, err := base.lstat(path)
			if err == nil {
				err = ctx.Err()
			}
			return info, err
		},
		open: func(path string) (validationFile, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			file, err := base.open(path)
			if err != nil {
				return nil, err
			}
			if err := ctx.Err(); err != nil {
				_ = file.Close()
				return nil, err
			}
			return &contextValidationFile{ctx: ctx, validationFile: file}, nil
		},
		readDir: func(path string) ([]os.DirEntry, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			entries, err := base.readDir(path)
			if err == nil {
				err = ctx.Err()
			}
			return entries, err
		},
		allocatedBytes: func(path string) (int64, error) {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			allocated, err := base.allocatedBytes(path)
			if err == nil {
				err = ctx.Err()
			}
			return allocated, err
		},
	}
}

func validateRecord(path string, validation validationOps) (RecordRef, error) {
	info, err := validateRecordDirectory(path, validation)
	if err != nil {
		return RecordRef{}, err
	}
	encoded, err := readRecordManifest(path, validation)
	if err != nil {
		return RecordRef{}, err
	}
	return validateRecordManifestBytes(path, validation, info, encoded)
}

func validateRecordWithManifestBytes(path string, validation validationOps, encoded []byte) (RecordRef, error) {
	info, err := validateRecordDirectory(path, validation)
	if err != nil {
		return RecordRef{}, err
	}
	if int64(len(encoded)) > maxManifestBytes {
		return RecordRef{}, ErrSpoolCorrupt
	}
	return validateRecordManifestBytes(path, validation, info, encoded)
}

func validateRecordDirectory(path string, validation validationOps) (os.FileInfo, error) {
	info, err := validation.lstat(path)
	if err != nil {
		return nil, fmt.Errorf("lstat ready record: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrSpoolCorrupt
	}
	return info, nil
}

func readRecordManifest(path string, validation validationOps) ([]byte, error) {
	manifestPath := filepath.Join(path, manifestName)
	manifestInfo, err := validation.lstat(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSpoolCorrupt
		}
		return nil, fmt.Errorf("lstat ready manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Size() < 0 || manifestInfo.Size() > maxManifestBytes {
		return nil, ErrSpoolCorrupt
	}
	f, err := validation.open(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("open ready manifest: %w", err)
	}
	trackedManifest := &validationReadTracker{validationFile: f}
	encoded, readErr := io.ReadAll(io.LimitReader(trackedManifest, maxManifestBytes+1))
	closeErr := f.Close()
	if closeErr != nil {
		return nil, fmt.Errorf("close ready manifest: %w", closeErr)
	}
	if readErr != nil {
		if trackedManifest.readErr != nil {
			return nil, fmt.Errorf("read ready manifest: %w", trackedManifest.readErr)
		}
		return nil, fmt.Errorf("read ready manifest: %w", readErr)
	}
	if int64(len(encoded)) > maxManifestBytes {
		return nil, ErrSpoolCorrupt
	}
	return encoded, nil
}

func validateRecordManifestBytes(path string, validation validationOps, info os.FileInfo, encoded []byte) (RecordRef, error) {
	var disk diskManifest
	if err := decodeStrictJSON(encoded, &disk); err != nil {
		return RecordRef{}, ErrSpoolCorrupt
	}
	manifest := disk.Manifest
	legacyLimits := false
	switch {
	case manifest.SpoolVersion == spoolVersion &&
		disk.BodyLimitBytes > 0 && disk.BodyLimitBytes <= uint64(defaultMaxBodyBytes) &&
		disk.HeaderLimitBytes > 0 && disk.HeaderLimitBytes <= uint64(defaultMaxHeaderBytes):
	case manifest.SpoolVersion == legacySpoolVersion &&
		disk.BodyLimitBytes == 0 && disk.HeaderLimitBytes == 0:
		disk.BodyLimitBytes = uint64(defaultMaxBodyBytes)
		disk.HeaderLimitBytes = uint64(defaultMaxHeaderBytes)
		legacyLimits = true
	default:
		return RecordRef{}, ErrSpoolCorrupt
	}
	if manifest.CaptureVersion != captureVersion {
		return RecordRef{}, ErrSpoolCorrupt
	}
	readyName := filepath.Base(path)
	id, err := uuid.Parse(readyName)
	if err != nil || readyName != id.String() || id != manifest.CaptureID || manifest.Begin.CaptureID != id {
		return RecordRef{}, ErrSpoolCorrupt
	}

	entries, err := validation.readDir(path)
	if err != nil {
		return RecordRef{}, fmt.Errorf("read ready record directory: %w", err)
	}
	allowed := map[string]struct{}{manifestName: {}}
	fileStats := make(map[string]model.FileStat, len(manifest.Files))
	for _, fileStat := range manifest.Files {
		if !validContentFileName(fileStat.Name) {
			return RecordRef{}, ErrSpoolCorrupt
		}
		if _, duplicate := fileStats[fileStat.Name]; duplicate {
			return RecordRef{}, ErrSpoolCorrupt
		}
		fileStats[fileStat.Name] = fileStat
		allowed[fileStat.Name] = struct{}{}
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return RecordRef{}, ErrSpoolCorrupt
		}
	}
	ambiguousLegacyLimits := false
	if !statsMatchFiles(manifest, fileStats, disk.BodyLimitBytes, disk.HeaderLimitBytes) {
		if legacyLimits {
			bodyLimit, headerLimit, plausible := inferredLegacyLimits(manifest, fileStats)
			if plausible && statsMatchFiles(manifest, fileStats, bodyLimit, headerLimit) {
				ambiguousLegacyLimits = true
			}
		}
		if !ambiguousLegacyLimits {
			return RecordRef{}, ErrSpoolCorrupt
		}
	}
	for _, fileStat := range manifest.Files {
		if err := verifyContentFile(filepath.Join(path, fileStat.Name), fileStat, validation); err != nil {
			return RecordRef{}, err
		}
	}
	allocated, err := validation.allocatedBytes(path)
	if err != nil {
		return RecordRef{}, fmt.Errorf("scan ready record allocation: %w", err)
	}
	if ambiguousLegacyLimits {
		return RecordRef{}, errLegacyLimitsUnknown
	}
	return RecordRef{
		CaptureID:      id,
		Path:           path,
		Manifest:       manifest,
		StoredBytes:    manifestStoredBytes(manifest),
		AllocatedBytes: allocated,
		ReadyAt:        info.ModTime(),
	}, nil
}

func manifestStoredBytes(manifest model.Manifest) int64 {
	return int64(manifest.Request.StoredBytes) +
		int64(manifest.Response.StoredBytes) +
		int64(manifest.RequestHeaders.StoredBytes) +
		int64(manifest.ResponseHeaders.StoredBytes)
}

func verifyContentFile(path string, want model.FileStat, validation validationOps) error {
	info, err := validation.lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrSpoolCorrupt
		}
		return fmt.Errorf("lstat content file: %w", err)
	}
	if !info.Mode().IsRegular() || uint64(info.Size()) != want.CompressedBytes {
		return ErrSpoolCorrupt
	}
	f, err := validation.open(path)
	if err != nil {
		return fmt.Errorf("open content file: %w", err)
	}
	trackedContent := &validationReadTracker{validationFile: f}
	compressedHash := sha256.New()
	compressedBytes, err := io.Copy(compressedHash, trackedContent)
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("read compressed content: %w", err)
	}
	if uint64(compressedBytes) != want.CompressedBytes || hex.EncodeToString(compressedHash.Sum(nil)) != want.CompressedSHA256 {
		_ = f.Close()
		return ErrSpoolCorrupt
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return fmt.Errorf("seek compressed content: %w", err)
	}
	trackedContent.readErr = nil
	zr, err := zstd.NewReader(
		bufio.NewReaderSize(trackedContent, 64<<10),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxMemory(uint64(defaultMaxBodyBytes)),
		zstd.WithDecoderMaxWindow(uint64(defaultMaxBodyBytes)),
	)
	if err != nil {
		_ = f.Close()
		if trackedContent.readErr != nil {
			return fmt.Errorf("read compressed content: %w", trackedContent.readErr)
		}
		return ErrSpoolCorrupt
	}
	uncompressedHash := sha256.New()
	uncompressedBytes, copyErr := io.Copy(uncompressedHash, io.LimitReader(zr, int64(want.UncompressedBytes)+1))
	if validation.decodedBytes != nil {
		validation.decodedBytes(uncompressedBytes)
	}
	zr.Close()
	closeErr := f.Close()
	if closeErr != nil {
		return fmt.Errorf("close content file: %w", closeErr)
	}
	if copyErr != nil {
		if trackedContent.readErr != nil {
			return fmt.Errorf("read compressed content: %w", trackedContent.readErr)
		}
		return ErrSpoolCorrupt
	}
	if uint64(uncompressedBytes) != want.UncompressedBytes || hex.EncodeToString(uncompressedHash.Sum(nil)) != want.UncompressedSHA256 {
		return ErrSpoolCorrupt
	}
	return nil
}

type validationReadTracker struct {
	validationFile
	readErr error
}

type contextValidationFile struct {
	ctx context.Context
	validationFile
}

func (f *contextValidationFile) Read(payload []byte) (int, error) {
	if err := f.ctx.Err(); err != nil {
		return 0, err
	}
	return f.validationFile.Read(payload)
}

func (r *validationReadTracker) Read(p []byte) (int, error) {
	n, err := r.validationFile.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}
	return n, err
}

func statsMatchFiles(manifest model.Manifest, files map[string]model.FileStat, bodyLimit, headerLimit uint64) bool {
	checks := []struct {
		name         string
		stat         model.BodyStat
		enabled      bool
		wantComplete bool
		limit        uint64
	}{
		{
			name:         "request.zst",
			stat:         manifest.Request,
			enabled:      manifest.Begin.Policy.StoreRequestBody,
			wantComplete: true,
			limit:        bodyLimit,
		},
		{
			name:         "response.zst",
			stat:         manifest.Response,
			enabled:      manifest.Begin.Policy.StoreResponseBody,
			wantComplete: manifest.Final.ResponseComplete,
			limit:        bodyLimit,
		},
		{
			name:         "request_headers.zst",
			stat:         model.BodyStat(manifest.RequestHeaders),
			enabled:      manifest.Begin.Policy.StoreRequestHeaders,
			wantComplete: true,
			limit:        headerLimit,
		},
		{
			name:         "response_headers.zst",
			stat:         model.BodyStat(manifest.ResponseHeaders),
			enabled:      manifest.Begin.Policy.StoreResponseHeaders,
			wantComplete: true,
			limit:        headerLimit,
		},
	}
	for _, check := range checks {
		file, exists := files[check.name]
		if !validColumnStat(check.stat, check.enabled, check.wantComplete, check.limit, file, exists) {
			return false
		}
	}
	return true
}

func inferredLegacyLimits(manifest model.Manifest, files map[string]model.FileStat) (uint64, uint64, bool) {
	bodyLimit, ok := inferredLegacyLimit(uint64(defaultMaxBodyBytes), []struct {
		stat    model.BodyStat
		enabled bool
	}{
		{stat: manifest.Request, enabled: manifest.Begin.Policy.StoreRequestBody},
		{stat: manifest.Response, enabled: manifest.Begin.Policy.StoreResponseBody},
	})
	if !ok {
		return 0, 0, false
	}
	headerLimit, ok := inferredLegacyLimit(uint64(defaultMaxHeaderBytes), []struct {
		stat    model.BodyStat
		enabled bool
	}{
		{stat: model.BodyStat(manifest.RequestHeaders), enabled: manifest.Begin.Policy.StoreRequestHeaders},
		{stat: model.BodyStat(manifest.ResponseHeaders), enabled: manifest.Begin.Policy.StoreResponseHeaders},
	})
	if !ok || !statsMatchFiles(manifest, files, bodyLimit, headerLimit) {
		return 0, 0, false
	}
	return bodyLimit, headerLimit, true
}

func inferredLegacyLimit(maximum uint64, columns []struct {
	stat    model.BodyStat
	enabled bool
}) (uint64, bool) {
	limit := maximum
	found := false
	for _, column := range columns {
		if !column.enabled || !column.stat.Truncated {
			continue
		}
		if column.stat.StoredBytes == 0 || column.stat.StoredBytes > maximum {
			return 0, false
		}
		if found && limit != column.stat.StoredBytes {
			return 0, false
		}
		limit = column.stat.StoredBytes
		found = true
	}
	return limit, true
}

func validColumnStat(stat model.BodyStat, enabled, wantComplete bool, limit uint64, file model.FileStat, fileExists bool) bool {
	if stat.StoredBytes > stat.ObservedBytes || !validSHA256(stat.SHA256) || stat.Complete != wantComplete {
		return false
	}
	wantStored := uint64(0)
	if enabled {
		wantStored = min(stat.ObservedBytes, limit)
	}
	if stat.StoredBytes != wantStored {
		return false
	}
	wantTruncated := enabled && stat.ObservedBytes > limit
	if stat.Truncated != wantTruncated {
		return false
	}
	if !enabled && (stat.StoredBytes != 0 || fileExists) {
		return false
	}
	if fileExists && file.UncompressedBytes != stat.StoredBytes {
		return false
	}
	if !fileExists && stat.StoredBytes != 0 {
		return false
	}
	if stat.ObservedBytes == stat.StoredBytes {
		wantHash := emptySHA256()
		if fileExists {
			wantHash = file.UncompressedSHA256
		}
		if stat.SHA256 != wantHash {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func emptySHA256() string {
	sum := sha256.Sum256(nil)
	return hex.EncodeToString(sum[:])
}

func validContentFileName(name string) bool {
	switch name {
	case "request.zst", "response.zst", "request_headers.zst", "response_headers.zst":
		return true
	default:
		return false
	}
}

func sortRecordRefs(records []RecordRef) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].ReadyAt.Equal(records[j].ReadyAt) {
			return strings.Compare(records[i].CaptureID.String(), records[j].CaptureID.String()) < 0
		}
		return records[i].ReadyAt.Before(records[j].ReadyAt)
	})
}

func cloneRecordRefs(records []RecordRef) []RecordRef {
	cloned := make([]RecordRef, len(records))
	for i := range records {
		cloned[i] = records[i]
		cloned[i].Manifest.Files = append([]model.FileStat(nil), records[i].Manifest.Files...)
	}
	return cloned
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	err = directory.Sync()
	closeErr := directory.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func syncOpenedDirectory(directory *os.File) error {
	return directory.Sync()
}
