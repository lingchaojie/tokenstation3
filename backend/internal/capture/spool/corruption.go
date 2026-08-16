package spool

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	corruptionTombstoneVersion  uint16 = 1
	corruptionSuffix                   = ".corrupt"
	corruptionTempSuffix               = ".corrupt.tmp"
	maxCorruptionTombstoneBytes        = 64 << 10
)

type AppliedCorruption struct {
	CaptureID uuid.UUID
}

type corruptionTombstone struct {
	Version   uint16    `json:"version"`
	CaptureID uuid.UUID `json:"capture_id"`
	Applied   bool      `json:"applied"`
}

// QuarantineCorrupt durably retires only the named ready record and its exact
// derivative batch metadata. It deliberately does not take lifecycleMu, so a
// live IPC attempt cannot block unrelated backlog delivery or admission.
func (s *Store) QuarantineCorrupt(batch *Batch, captureID uuid.UUID) (AppliedCorruption, error) {
	if batch == nil || batch.ID == uuid.Nil || captureID == uuid.Nil {
		return AppliedCorruption{}, ErrBatchRecord
	}
	found := false
	for _, record := range batch.records {
		if record.CaptureID == captureID {
			found = true
			break
		}
	}
	if !found {
		return AppliedCorruption{}, ErrBatchRecord
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	return s.quarantineCorruptLocked(&batch.ID, captureID)
}

func (s *Store) CleanupCorruption(captureID uuid.UUID) error {
	if captureID == uuid.Nil {
		return ErrSpoolCorrupt
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	path := corruptionPath(s, captureID)
	tombstone, err := s.readCorruptionTombstone(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !tombstone.Applied || tombstone.CaptureID != captureID {
		return ErrSpoolCorrupt
	}
	return s.removeSendingPathLocked(path, "delete:corruption", false)
}

func (s *Store) quarantineCorruptLocked(batchID *uuid.UUID, captureID uuid.UUID) (AppliedCorruption, error) {
	path := corruptionPath(s, captureID)
	tombstone, err := s.readCorruptionTombstone(path)
	if errors.Is(err, os.ErrNotExist) {
		tombstone = corruptionTombstone{Version: corruptionTombstoneVersion, CaptureID: captureID}
		if err := s.writeCorruptionTombstoneLocked(tombstone); err != nil {
			return AppliedCorruption{}, err
		}
	} else if err != nil || tombstone.CaptureID != captureID {
		return AppliedCorruption{}, ErrSpoolCorrupt
	}

	if batchID != nil {
		if err := s.retireExactBatchReferenceLocked(*batchID, captureID); err != nil {
			return AppliedCorruption{}, err
		}
	} else if err := s.retirePendingBatchReferencesLocked(captureID); err != nil {
		return AppliedCorruption{}, err
	}
	if !tombstone.Applied {
		readyPath := filepath.Join(s.readyDir, captureID.String())
		if err := os.RemoveAll(readyPath); err != nil {
			return AppliedCorruption{}, fmt.Errorf("delete corrupt ready record: %w", err)
		}
		s.event("delete:corrupt-ready-record")
		if err := s.readySyncDirectory(s.readyDir); err != nil {
			return AppliedCorruption{}, fmt.Errorf("fsync ready directory: %w", err)
		}
		s.event("fsync:ready-dir")
		tombstone.Applied = true
		if err := s.writeCorruptionTombstoneLocked(tombstone); err != nil {
			return AppliedCorruption{}, err
		}
	}
	s.removeReady(captureID)
	s.accountCorruption(captureID)
	return AppliedCorruption{CaptureID: captureID}, nil
}

func (s *Store) retireExactBatchReferenceLocked(batchID, captureID uuid.UUID) error {
	path := batchManifestPath(s, batchID)
	manifest, _, err := s.readBatchManifest(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	found := false
	for _, record := range manifest.Records {
		if record.CaptureID == captureID {
			found = true
			break
		}
	}
	if !found {
		return ErrBatchRecord
	}
	return s.removeSendingPathLocked(path, "delete:corrupt-batch.manifest", false)
}

func (s *Store) recoverCorruptionsLocked() ([]AppliedCorruption, error) {
	entries, err := os.ReadDir(s.sendingDir)
	if err != nil {
		return nil, fmt.Errorf("read sending directory: %w", err)
	}
	var applied []AppliedCorruption
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), corruptionSuffix) {
			continue
		}
		tombstone, err := s.readCorruptionTombstone(filepath.Join(s.sendingDir, entry.Name()))
		if err != nil || tombstone.CaptureID.String()+corruptionSuffix != entry.Name() {
			return nil, ErrSpoolCorrupt
		}
		corruption, err := s.quarantineCorruptLocked(nil, tombstone.CaptureID)
		if err != nil {
			return nil, err
		}
		applied = appendCorruption(applied, corruption)
	}
	return applied, nil
}

func (s *Store) accountCorruption(captureID uuid.UUID) {
	s.dropMu.Lock()
	defer s.dropMu.Unlock()
	if _, exists := s.accountedCorruptions[captureID]; exists {
		return
	}
	s.accountedCorruptions[captureID] = struct{}{}
	s.dropped[ErrSpoolCorrupt.Error()]++
}

func (s *Store) writeCorruptionTombstoneLocked(tombstone corruptionTombstone) error {
	encoded, err := json.Marshal(tombstone)
	if err != nil || len(encoded) > maxCorruptionTombstoneBytes {
		return ErrSpoolCorrupt
	}
	reservation, err := s.capacity.reserveOperationalFilesUnblocking(int64(len(encoded)))
	if err != nil {
		return err
	}
	defer reservation.Release()
	finalPath := corruptionPath(s, tombstone.CaptureID)
	tempPath := finalPath + ".tmp"
	if err := os.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove corruption temp: %w", err)
	}
	return s.writeBatchMetadata(tempPath, finalPath, encoded, "corruption.tmp", "corruption")
}

func (s *Store) readCorruptionTombstone(path string) (corruptionTombstone, error) {
	encoded, err := s.readBoundedFile(path, maxCorruptionTombstoneBytes)
	if err != nil {
		return corruptionTombstone{}, err
	}
	var tombstone corruptionTombstone
	if err := decodeStrictJSON(encoded, &tombstone); err != nil ||
		tombstone.Version != corruptionTombstoneVersion || tombstone.CaptureID == uuid.Nil {
		return corruptionTombstone{}, ErrSpoolCorrupt
	}
	return tombstone, nil
}

func corruptionPath(s *Store, captureID uuid.UUID) string {
	return filepath.Join(s.sendingDir, captureID.String()+corruptionSuffix)
}

func appendCorruption(items []AppliedCorruption, item AppliedCorruption) []AppliedCorruption {
	for _, existing := range items {
		if existing.CaptureID == item.CaptureID {
			return items
		}
	}
	return append(items, item)
}
