package spool

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	spoolVersion                    uint16 = 1
	captureVersion                  uint16 = 2
	maxManifestBytes                       = 2 << 20
)

var (
	ErrTooManyAttempts = errors.New("ipc_backpressure")
	ErrSpoolCorrupt    = errors.New("spool_corrupt")
	ErrAttemptClosed   = errors.New("capture attempt is closed")
)

type Config struct {
	RootDir                  string
	MaxBytes                 int64
	MinFreeBytes             int64
	MaxBodyBytes             int64
	MaxHeaderBytes           int64
	MaxActiveAttempts        int
	OperationalHeadroomBytes int64

	eventHook func(string)
	openFile  func(string, int, os.FileMode) (*os.File, error)
}

type RecoveryReport struct {
	Ready          []RecordRef
	OrphansDeleted int
	CorruptDeleted int
}

type RecordRef struct {
	CaptureID      uuid.UUID
	Path           string
	Manifest       model.Manifest
	StoredBytes    int64
	AllocatedBytes int64
	ReadyAt        time.Time
}

type Store struct {
	config Config

	partialDir string
	readyDir   string
	sendingDir string
	capacity   *Capacity

	attemptSlots chan struct{}
	lifecycleMu  sync.RWMutex

	readyMu sync.RWMutex
	ready   []RecordRef

	recoverMu sync.Mutex
	dropMu    sync.Mutex
	dropped   map[string]uint64
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
	if config.OperationalHeadroomBytes < 0 || config.OperationalHeadroomBytes >= config.MaxBytes {
		return nil, errors.New("invalid spool operational headroom")
	}

	if err := ensurePrivateDirectory(config.RootDir); err != nil {
		return nil, fmt.Errorf("create spool root: %w", err)
	}
	partialDir := filepath.Join(config.RootDir, "partial")
	readyDir := filepath.Join(config.RootDir, "ready")
	sendingDir := filepath.Join(config.RootDir, "sending")
	for _, directory := range []string{partialDir, readyDir, sendingDir} {
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
	return &Store{
		config:       config,
		partialDir:   partialDir,
		readyDir:     readyDir,
		sendingDir:   sendingDir,
		capacity:     capacity,
		attemptSlots: make(chan struct{}, config.MaxActiveAttempts),
		dropped:      make(map[string]uint64),
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

	report := RecoveryReport{}
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
		ref, validateErr := validateRecord(path)
		if validateErr != nil {
			if removeErr := os.RemoveAll(path); removeErr != nil {
				return report, fmt.Errorf("delete corrupt ready record %s: %w", entry.Name(), removeErr)
			}
			report.CorruptDeleted++
			s.recordDrop(ErrSpoolCorrupt)
			continue
		}
		ready = append(ready, ref)
	}
	if report.CorruptDeleted > 0 {
		_ = syncDirectory(s.readyDir)
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

func validateRecord(path string) (RecordRef, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return RecordRef{}, ErrSpoolCorrupt
	}
	manifestPath := filepath.Join(path, manifestName)
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Size() > maxManifestBytes {
		return RecordRef{}, ErrSpoolCorrupt
	}
	f, err := os.Open(manifestPath)
	if err != nil {
		return RecordRef{}, ErrSpoolCorrupt
	}
	decoder := json.NewDecoder(io.LimitReader(f, maxManifestBytes+1))
	decoder.DisallowUnknownFields()
	var manifest model.Manifest
	decodeErr := decoder.Decode(&manifest)
	if decodeErr == nil {
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			decodeErr = ErrSpoolCorrupt
		}
	}
	closeErr := f.Close()
	if decodeErr != nil || closeErr != nil || manifest.SpoolVersion != spoolVersion || manifest.CaptureVersion != captureVersion {
		return RecordRef{}, ErrSpoolCorrupt
	}
	id, err := uuid.Parse(filepath.Base(path))
	if err != nil || id != manifest.CaptureID || manifest.Begin.CaptureID != id {
		return RecordRef{}, ErrSpoolCorrupt
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return RecordRef{}, ErrSpoolCorrupt
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
		if err := verifyContentFile(filepath.Join(path, fileStat.Name), fileStat); err != nil {
			return RecordRef{}, ErrSpoolCorrupt
		}
	}
	for _, entry := range entries {
		if _, ok := allowed[entry.Name()]; !ok {
			return RecordRef{}, ErrSpoolCorrupt
		}
	}
	if !statsMatchFiles(manifest, fileStats) {
		return RecordRef{}, ErrSpoolCorrupt
	}
	allocated, err := allocatedBytes(path)
	if err != nil {
		return RecordRef{}, ErrSpoolCorrupt
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

func verifyContentFile(path string, want model.FileStat) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || uint64(info.Size()) != want.CompressedBytes {
		return ErrSpoolCorrupt
	}
	f, err := os.Open(path)
	if err != nil {
		return ErrSpoolCorrupt
	}
	compressedHash := sha256.New()
	compressedBytes, err := io.Copy(compressedHash, f)
	if err != nil || uint64(compressedBytes) != want.CompressedBytes || hex.EncodeToString(compressedHash.Sum(nil)) != want.CompressedSHA256 {
		_ = f.Close()
		return ErrSpoolCorrupt
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return ErrSpoolCorrupt
	}
	zr, err := zstd.NewReader(bufio.NewReaderSize(f, 64<<10), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		_ = f.Close()
		return ErrSpoolCorrupt
	}
	uncompressedHash := sha256.New()
	uncompressedBytes, copyErr := io.Copy(uncompressedHash, zr)
	zr.Close()
	closeErr := f.Close()
	if copyErr != nil || closeErr != nil || uint64(uncompressedBytes) != want.UncompressedBytes || hex.EncodeToString(uncompressedHash.Sum(nil)) != want.UncompressedSHA256 {
		return ErrSpoolCorrupt
	}
	return nil
}

func statsMatchFiles(manifest model.Manifest, files map[string]model.FileStat) bool {
	checks := []struct {
		name         string
		stat         model.BodyStat
		enabled      bool
		wantComplete bool
	}{
		{
			name:         "request.zst",
			stat:         manifest.Request,
			enabled:      manifest.Begin.Policy.StoreRequestBody,
			wantComplete: true,
		},
		{
			name:         "response.zst",
			stat:         manifest.Response,
			enabled:      manifest.Begin.Policy.StoreResponseBody,
			wantComplete: manifest.Final.ResponseComplete,
		},
		{
			name:         "request_headers.zst",
			stat:         model.BodyStat(manifest.RequestHeaders),
			enabled:      manifest.Begin.Policy.StoreRequestHeaders,
			wantComplete: true,
		},
		{
			name:         "response_headers.zst",
			stat:         model.BodyStat(manifest.ResponseHeaders),
			enabled:      manifest.Begin.Policy.StoreResponseHeaders,
			wantComplete: true,
		},
	}
	for _, check := range checks {
		file, exists := files[check.name]
		if !validColumnStat(check.stat, check.enabled, check.wantComplete, file, exists) {
			return false
		}
	}
	return true
}

func validColumnStat(stat model.BodyStat, enabled, wantComplete bool, file model.FileStat, fileExists bool) bool {
	if stat.StoredBytes > stat.ObservedBytes || !validSHA256(stat.SHA256) || stat.Complete != wantComplete {
		return false
	}
	wantTruncated := enabled && stat.StoredBytes < stat.ObservedBytes
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
	if len(value) != sha256.Size*2 {
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
