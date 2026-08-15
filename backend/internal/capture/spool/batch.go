package spool

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	batchManifestVersion  uint16 = 1
	batchAckVersion       uint16 = 1
	batchRowOverheadBytes int64  = 1 << 20
	maxBatchManifestBytes        = 8 << 20
	maxBatchAckBytes             = 64 << 10
)

var (
	ErrBatchByteLimit   = errors.New("batch_byte_limit")
	ErrBatchRecordLimit = errors.New("batch_record_limit")
	ErrBatchRecord      = errors.New("batch record is not a member")
	ErrBatchNotAcked    = errors.New("batch is not durably acked")
)

type BatchManifest struct {
	Version   uint16        `json:"version"`
	BatchID   uuid.UUID     `json:"batch_id"`
	CreatedAt time.Time     `json:"created_at"`
	Records   []BatchRecord `json:"records"`
}

type BatchRecord struct {
	CaptureID      uuid.UUID `json:"capture_id"`
	ManifestSHA256 string    `json:"manifest_sha256"`
	StoredBytes    int64     `json:"stored_bytes"`
}

type batchAck struct {
	Version        uint16    `json:"version"`
	BatchID        uuid.UUID `json:"batch_id"`
	ManifestSHA256 string    `json:"manifest_sha256"`
}

type Batch struct {
	ID      uuid.UUID
	Records []RecordRef

	records      []RecordRef
	manifestHash string
}

func (b *Batch) DeduplicationToken() string {
	if b == nil {
		return ""
	}
	return b.ID.String()
}

func (b *Batch) OpenRecord(captureID uuid.UUID) (RecordRef, error) {
	if b == nil {
		return RecordRef{}, ErrBatchRecord
	}
	for i := range b.records {
		if b.records[i].CaptureID == captureID {
			return cloneRecordRefs(b.records[i : i+1])[0], nil
		}
	}
	return RecordRef{}, ErrBatchRecord
}

func (s *Store) NextBatch(maxRecords int, maxBytes int64) (*Batch, error) {
	s.batchMu.Lock()
	defer s.batchMu.Unlock()

	if err := s.recoverAckedLocked(); err != nil {
		return nil, err
	}
	pending, err := s.pendingBatchLocked()
	if err != nil || pending != nil {
		return pending, err
	}

	ready := s.Ready()
	if len(ready) == 0 {
		return nil, nil
	}
	if maxRecords <= 0 {
		return nil, ErrBatchRecordLimit
	}
	if len(ready) > maxRecords {
		ready = ready[:maxRecords]
	}
	selected := make([]RecordRef, 0, len(ready))
	var selectedBytes int64
	for _, ref := range ready {
		rowBytes := ref.StoredBytes + batchRowOverheadBytes
		if ref.StoredBytes < 0 || rowBytes < batchRowOverheadBytes || rowBytes > maxBytes-selectedBytes {
			if len(selected) == 0 {
				return nil, ErrBatchByteLimit
			}
			break
		}
		selected = append(selected, ref)
		selectedBytes += rowBytes
	}

	manifest := BatchManifest{
		Version:   batchManifestVersion,
		BatchID:   uuid.New(),
		CreatedAt: time.Now().UTC(),
		Records:   make([]BatchRecord, 0, len(selected)),
	}
	canonical := make([]RecordRef, 0, len(selected))
	for _, selectedRef := range selected {
		ref, err := validateRecord(selectedRef.Path, s.validation)
		if err != nil {
			return nil, fmt.Errorf("validate selected record %s: %w", selectedRef.CaptureID, err)
		}
		hash, err := fileSHA256(filepath.Join(ref.Path, manifestName))
		if err != nil {
			return nil, fmt.Errorf("hash selected manifest %s: %w", ref.CaptureID, err)
		}
		canonical = append(canonical, ref)
		manifest.Records = append(manifest.Records, BatchRecord{
			CaptureID:      ref.CaptureID,
			ManifestSHA256: hash,
			StoredBytes:    ref.StoredBytes,
		})
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode batch manifest: %w", err)
	}
	if len(encoded) > maxBatchManifestBytes {
		return nil, fmt.Errorf("batch manifest exceeds limit: %w", ErrBatchRecordLimit)
	}
	encodedAck, err := json.Marshal(batchAck{
		Version:        batchAckVersion,
		BatchID:        manifest.BatchID,
		ManifestSHA256: sha256Hex(encoded),
	})
	if err != nil {
		return nil, fmt.Errorf("size batch ack: %w", err)
	}
	reservation, err := s.capacity.reserveOperationalFilesUnblocking(int64(len(encoded)), int64(len(encodedAck)))
	if err != nil {
		return nil, err
	}
	defer reservation.Release()

	tempPath := filepath.Join(s.sendingDir, manifest.BatchID.String()+".manifest.tmp")
	finalPath := filepath.Join(s.sendingDir, manifest.BatchID.String()+".manifest")
	if err := s.writeBatchMetadata(tempPath, finalPath, encoded, "batch.tmp", "batch.manifest"); err != nil {
		return nil, err
	}
	return newBatch(manifest.BatchID, canonical, sha256Hex(encoded)), nil
}

func (s *Store) MarkAcked(batch *Batch) error {
	if batch == nil || batch.ID == uuid.Nil {
		return errors.New("batch is required")
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()

	manifestPath := batchManifestPath(s, batch.ID)
	manifest, encodedManifest, err := readBatchManifest(manifestPath)
	if err != nil {
		return err
	}
	if manifest.BatchID != batch.ID || (batch.manifestHash != "" && sha256Hex(encodedManifest) != batch.manifestHash) {
		return ErrSpoolCorrupt
	}
	ackPath := batchAckPath(s, batch.ID)
	if _, err := os.Lstat(ackPath); err == nil {
		ack, _, readErr := readBatchAck(ackPath)
		if readErr != nil {
			if errors.Is(readErr, ErrSpoolCorrupt) {
				if removeErr := s.removeSendingPathLocked(ackPath, "delete:corrupt-batch.acked", true); removeErr != nil {
					return removeErr
				}
			}
			return readErr
		}
		if ack.BatchID != batch.ID || ack.ManifestSHA256 != sha256Hex(encodedManifest) {
			if err := s.removeSendingPathLocked(ackPath, "delete:corrupt-batch.acked", true); err != nil {
				return err
			}
			return ErrSpoolCorrupt
		}
		return s.syncSendingDirectoryLocked()
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("lstat batch ack: %w", err)
	}

	if err := s.discardTempLocked(ackPath+".tmp", false); err != nil {
		return err
	}
	ack := batchAck{
		Version:        batchAckVersion,
		BatchID:        batch.ID,
		ManifestSHA256: sha256Hex(encodedManifest),
	}
	encoded, err := json.Marshal(ack)
	if err != nil {
		return fmt.Errorf("encode batch ack: %w", err)
	}
	reservation, err := s.capacity.reserveOperationalFilesUnblocking(int64(len(encoded)))
	if err != nil {
		return err
	}
	defer reservation.Release()
	return s.writeBatchMetadata(ackPath+".tmp", ackPath, encoded, "batch.acked.tmp", "batch.acked")
}

func (s *Store) CleanupAcked(batch *Batch) error {
	if batch == nil || batch.ID == uuid.Nil {
		return errors.New("batch is required")
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	return s.cleanupAckedBatchLocked(batch.ID)
}

func (s *Store) RecoverAcked() error {
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	return s.recoverAckedLocked()
}

func (s *Store) PendingBatches() []*Batch {
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	if err := s.recoverAckedLocked(); err != nil {
		return nil
	}
	entries, err := os.ReadDir(s.sendingDir)
	if err != nil {
		return nil
	}
	var batches []*Batch
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".manifest") {
			continue
		}
		id, err := uuid.Parse(strings.TrimSuffix(entry.Name(), ".manifest"))
		if err != nil {
			continue
		}
		if _, err := os.Lstat(batchAckPath(s, id)); err == nil {
			continue
		}
		batch, err := s.loadBatchLocked(filepath.Join(s.sendingDir, entry.Name()))
		if err == nil {
			batches = append(batches, batch)
		}
	}
	if len(batches) > 0 {
		if err := s.syncSendingDirectoryLocked(); err != nil {
			return nil
		}
	}
	return batches
}

func (s *Store) pendingBatchLocked() (*Batch, error) {
	entries, err := os.ReadDir(s.sendingDir)
	if err != nil {
		return nil, fmt.Errorf("read sending directory: %w", err)
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".manifest") {
			continue
		}
		path := filepath.Join(s.sendingDir, entry.Name())
		batch, err := s.loadBatchLocked(path)
		if err != nil {
			if errors.Is(err, ErrSpoolCorrupt) {
				if removeErr := s.removeSendingPathLocked(path, "delete:corrupt-batch.manifest", true); removeErr != nil {
					return nil, removeErr
				}
				continue
			}
			return nil, err
		}
		if err := s.syncSendingDirectoryLocked(); err != nil {
			return nil, err
		}
		return batch, nil
	}
	return nil, nil
}

func (s *Store) loadBatchLocked(path string) (*Batch, error) {
	manifest, encoded, err := readBatchManifest(path)
	if err != nil {
		return nil, err
	}
	if manifest.BatchID.String()+".manifest" != filepath.Base(path) {
		return nil, ErrSpoolCorrupt
	}
	records := make([]RecordRef, 0, len(manifest.Records))
	for _, batchRecord := range manifest.Records {
		recordPath := filepath.Join(s.readyDir, batchRecord.CaptureID.String())
		ref, err := validateRecord(recordPath, s.validation)
		if err != nil {
			if errors.Is(err, ErrSpoolCorrupt) || errors.Is(err, os.ErrNotExist) {
				return nil, ErrSpoolCorrupt
			}
			return nil, fmt.Errorf("validate sending record %s: %w", batchRecord.CaptureID, err)
		}
		hash, err := fileSHA256(filepath.Join(recordPath, manifestName))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, ErrSpoolCorrupt
			}
			return nil, fmt.Errorf("hash sending record %s: %w", batchRecord.CaptureID, err)
		}
		if hash != batchRecord.ManifestSHA256 || ref.StoredBytes != batchRecord.StoredBytes {
			return nil, ErrSpoolCorrupt
		}
		records = append(records, ref)
	}
	return newBatch(manifest.BatchID, records, sha256Hex(encoded)), nil
}

func (s *Store) recoverAckedLocked() error {
	entries, err := os.ReadDir(s.sendingDir)
	if err != nil {
		return fmt.Errorf("read sending directory: %w", err)
	}
	manifests := make(map[uuid.UUID]string)
	acks := make(map[uuid.UUID]string)
	for _, entry := range entries {
		path := filepath.Join(s.sendingDir, entry.Name())
		switch {
		case strings.HasSuffix(entry.Name(), ".manifest.tmp"):
			if err := s.discardTempLocked(path, true); err != nil {
				return err
			}
		case strings.HasSuffix(entry.Name(), ".acked.tmp"):
			if err := s.discardTempLocked(path, false); err != nil {
				return err
			}
		case strings.HasSuffix(entry.Name(), ".manifest"):
			manifest, _, readErr := readBatchManifest(path)
			if readErr != nil || manifest.BatchID.String()+".manifest" != entry.Name() {
				if readErr != nil && !errors.Is(readErr, ErrSpoolCorrupt) {
					return readErr
				}
				if err := s.removeSendingPathLocked(path, "delete:corrupt-batch.manifest", true); err != nil {
					return err
				}
				continue
			}
			manifests[manifest.BatchID] = path
		case strings.HasSuffix(entry.Name(), ".acked"):
			ack, _, readErr := readBatchAck(path)
			if readErr != nil || ack.BatchID.String()+".acked" != entry.Name() {
				if readErr != nil && !errors.Is(readErr, ErrSpoolCorrupt) {
					return readErr
				}
				if err := s.removeSendingPathLocked(path, "delete:corrupt-batch.acked", true); err != nil {
					return err
				}
				continue
			}
			acks[ack.BatchID] = path
		default:
			if err := s.removeSendingPathLocked(path, "delete:corrupt-sending-metadata", true); err != nil {
				return err
			}
		}
	}

	for id, ackPath := range acks {
		manifestPath, hasManifest := manifests[id]
		if !hasManifest {
			if err := s.removeSendingPathLocked(ackPath, "delete:batch.acked", false); err != nil {
				return err
			}
			continue
		}
		_, encodedManifest, err := readBatchManifest(manifestPath)
		if err != nil {
			return err
		}
		ack, _, err := readBatchAck(ackPath)
		if err != nil {
			return err
		}
		if ack.ManifestSHA256 != sha256Hex(encodedManifest) {
			if err := s.removeSendingPathLocked(ackPath, "delete:corrupt-batch.acked", true); err != nil {
				return err
			}
			continue
		}
		if err := s.cleanupAckedBatchLocked(id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) discardTempLocked(path string, manifest bool) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("lstat sending temp: %w", err)
	}
	var readErr error
	if manifest {
		parsed, _, err := readBatchManifest(path)
		readErr = err
		if err == nil && parsed.BatchID.String()+".manifest.tmp" != filepath.Base(path) {
			readErr = ErrSpoolCorrupt
		}
	} else {
		parsed, _, err := readBatchAck(path)
		readErr = err
		if err == nil && parsed.BatchID.String()+".acked.tmp" != filepath.Base(path) {
			readErr = ErrSpoolCorrupt
		}
	}
	if readErr != nil && !errors.Is(readErr, ErrSpoolCorrupt) {
		return readErr
	}
	return s.removeSendingPathLocked(path, "delete:batch.tmp", errors.Is(readErr, ErrSpoolCorrupt))
}

func (s *Store) cleanupAckedBatchLocked(id uuid.UUID) error {
	manifestPath := batchManifestPath(s, id)
	ackPath := batchAckPath(s, id)
	manifest, encodedManifest, manifestErr := readBatchManifest(manifestPath)
	if errors.Is(manifestErr, os.ErrNotExist) {
		if _, err := os.Lstat(ackPath); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return fmt.Errorf("lstat batch ack: %w", err)
		}
		ack, _, err := readBatchAck(ackPath)
		if err != nil {
			return err
		}
		if ack.BatchID != id {
			return ErrSpoolCorrupt
		}
		return s.removeSendingPathLocked(ackPath, "delete:batch.acked", false)
	}
	if manifestErr != nil {
		return manifestErr
	}
	ack, _, err := readBatchAck(ackPath)
	if errors.Is(err, os.ErrNotExist) {
		return ErrBatchNotAcked
	}
	if err != nil {
		return err
	}
	if ack.BatchID != id || ack.ManifestSHA256 != sha256Hex(encodedManifest) {
		return ErrBatchNotAcked
	}
	if err := s.syncSendingDirectoryLocked(); err != nil {
		return err
	}

	for _, record := range manifest.Records {
		path := filepath.Join(s.readyDir, record.CaptureID.String())
		if err := os.RemoveAll(path); err != nil {
			return fmt.Errorf("delete acked ready record %s: %w", record.CaptureID, err)
		}
		s.removeReady(record.CaptureID)
		s.event("delete:ready-record")
	}
	if err := syncDirectory(s.readyDir); err != nil {
		return fmt.Errorf("fsync ready directory: %w", err)
	}
	s.event("fsync:ready-dir")
	if err := os.Remove(manifestPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete batch manifest: %w", err)
	}
	s.event("delete:batch.manifest")
	if err := s.syncSendingDirectoryLocked(); err != nil {
		return fmt.Errorf("fsync sending directory after manifest delete: %w", err)
	}
	if err := os.Remove(ackPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete batch ack: %w", err)
	}
	s.event("delete:batch.acked")
	if err := s.syncSendingDirectoryLocked(); err != nil {
		return fmt.Errorf("fsync sending directory after ack delete: %w", err)
	}
	return nil
}

func (s *Store) removeReady(id uuid.UUID) {
	s.readyMu.Lock()
	defer s.readyMu.Unlock()
	for i := range s.ready {
		if s.ready[i].CaptureID == id {
			s.ready = append(s.ready[:i], s.ready[i+1:]...)
			return
		}
	}
}

func (s *Store) removeSendingPathLocked(path, event string, corrupt bool) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove sending metadata %s: %w", filepath.Base(path), err)
	}
	s.event(event)
	if err := s.syncSendingDirectoryLocked(); err != nil {
		return fmt.Errorf("fsync sending directory after metadata removal: %w", err)
	}
	if corrupt {
		s.recordDrop(ErrSpoolCorrupt)
	}
	return nil
}

func (s *Store) writeBatchMetadata(tempPath, finalPath string, encoded []byte, tempEvent, finalEvent string) error {
	f, err := s.config.openFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", filepath.Base(tempPath), err)
	}
	if _, err := io.Copy(f, bytes.NewReader(encoded)); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", filepath.Base(tempPath), err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync %s: %w", filepath.Base(tempPath), err)
	}
	s.event("fsync:" + tempEvent)
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", filepath.Base(tempPath), err)
	}
	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("publish %s: %w", filepath.Base(finalPath), err)
	}
	s.event("rename:" + finalEvent)
	return s.syncSendingDirectoryLocked()
}

func (s *Store) syncSendingDirectoryLocked() error {
	if err := syncDirectory(s.sendingDir); err != nil {
		return fmt.Errorf("fsync sending directory: %w", err)
	}
	s.event("fsync:sending-dir")
	return nil
}

func readBatchManifest(path string) (BatchManifest, []byte, error) {
	encoded, err := readBoundedFile(path, maxBatchManifestBytes)
	if err != nil {
		return BatchManifest{}, nil, fmt.Errorf("read batch manifest: %w", err)
	}
	var manifest BatchManifest
	if err := decodeStrictJSON(encoded, &manifest); err != nil {
		return BatchManifest{}, nil, ErrSpoolCorrupt
	}
	if manifest.Version != batchManifestVersion || manifest.BatchID == uuid.Nil || manifest.CreatedAt.IsZero() || len(manifest.Records) == 0 {
		return BatchManifest{}, nil, ErrSpoolCorrupt
	}
	seen := make(map[uuid.UUID]struct{}, len(manifest.Records))
	for _, record := range manifest.Records {
		if record.CaptureID == uuid.Nil || record.StoredBytes < 0 || !validSHA256(record.ManifestSHA256) || strings.ToLower(record.ManifestSHA256) != record.ManifestSHA256 {
			return BatchManifest{}, nil, ErrSpoolCorrupt
		}
		if _, duplicate := seen[record.CaptureID]; duplicate {
			return BatchManifest{}, nil, ErrSpoolCorrupt
		}
		seen[record.CaptureID] = struct{}{}
	}
	return manifest, encoded, nil
}

func readBatchAck(path string) (batchAck, []byte, error) {
	encoded, err := readBoundedFile(path, maxBatchAckBytes)
	if err != nil {
		return batchAck{}, nil, fmt.Errorf("read batch ack: %w", err)
	}
	var ack batchAck
	if err := decodeStrictJSON(encoded, &ack); err != nil {
		return batchAck{}, nil, ErrSpoolCorrupt
	}
	if ack.Version != batchAckVersion || ack.BatchID == uuid.Nil || !validSHA256(ack.ManifestSHA256) || strings.ToLower(ack.ManifestSHA256) != ack.ManifestSHA256 {
		return batchAck{}, nil, ErrSpoolCorrupt
	}
	return ack, encoded, nil
}

func readBoundedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > limit {
		return nil, ErrSpoolCorrupt
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	encoded, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if int64(len(encoded)) > limit {
		return nil, ErrSpoolCorrupt
	}
	return encoded, nil
}

func batchManifestPath(s *Store, id uuid.UUID) string {
	return filepath.Join(s.sendingDir, id.String()+".manifest")
}

func batchAckPath(s *Store, id uuid.UUID) string {
	return filepath.Join(s.sendingDir, id.String()+".acked")
}

func decodeStrictJSON(encoded []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrSpoolCorrupt
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(f, maxManifestBytes+1))
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func newBatch(id uuid.UUID, records []RecordRef, manifestHash string) *Batch {
	privateRecords := cloneRecordRefs(records)
	return &Batch{
		ID:           id,
		Records:      cloneRecordRefs(privateRecords),
		records:      privateRecords,
		manifestHash: manifestHash,
	}
}
