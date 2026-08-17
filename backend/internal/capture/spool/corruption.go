package spool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	legacyCorruptionTombstoneVersion uint16 = 1
	corruptionTombstoneVersion       uint16 = 2
	corruptionSuffix                        = ".corrupt"
	corruptionTempSuffix                    = ".corrupt.tmp"
	maxCorruptionTombstoneBytes             = 64 << 10
	corruptionNameDomain                    = "capture-ready-corruption-v1\x00"
)

// CorruptionID is a secret-free durable transaction identity. Canonical
// capture records retain their UUID identity; malformed names use a full
// domain-separated SHA-256 digest of the enumerated directory entry.
type CorruptionID string

type AppliedCorruption struct {
	ID        CorruptionID
	CaptureID uuid.UUID
}

type corruptionTombstone struct {
	Version        uint16
	ID             CorruptionID
	CaptureID      uuid.UUID
	AliasCaptureID uuid.UUID
	NameSHA256     string
	Applied        bool
}

type corruptionTombstoneWire struct {
	Version        uint16     `json:"version"`
	ID             string     `json:"id,omitempty"`
	CaptureID      *uuid.UUID `json:"capture_id,omitempty"`
	AliasCaptureID *uuid.UUID `json:"alias_capture_id,omitempty"`
	NameSHA256     string     `json:"name_sha256,omitempty"`
	Applied        bool       `json:"applied"`
}

type readyEntryInspection struct {
	sourceName    string
	canonicalTwin bool
}

type corruptionDirectoryView struct {
	ready           *os.File
	quarantine      *os.File
	readyNames      []string
	quarantineNames []string
}

// QuarantineCorrupt durably retires only the named private batch member. It
// deliberately does not take lifecycleMu, so a live IPC attempt cannot block
// unrelated backlog delivery or admission.
func (s *Store) QuarantineCorrupt(batch *Batch, captureID uuid.UUID) (AppliedCorruption, error) {
	if batch == nil || batch.ID == uuid.Nil || captureID == uuid.Nil {
		return AppliedCorruption{}, ErrBatchRecord
	}
	readyName := ""
	for _, record := range batch.records {
		if record.CaptureID != captureID {
			continue
		}
		if filepath.Clean(filepath.Dir(record.Path)) != filepath.Clean(s.readyDir) {
			return AppliedCorruption{}, ErrBatchRecord
		}
		readyName = filepath.Base(record.Path)
		break
	}
	if readyName != captureID.String() {
		return AppliedCorruption{}, ErrBatchRecord
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	return s.quarantineReadyEntryLocked(&batch.ID, readyName)
}

func (s *Store) CleanupCorruption(id CorruptionID) error {
	if !validCorruptionID(id) {
		return ErrSpoolCorrupt
	}
	s.batchMu.Lock()
	defer s.batchMu.Unlock()
	path := corruptionPath(s, id)
	tombstone, err := s.readCorruptionTombstone(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || !tombstone.Applied || tombstone.ID != id {
		return ErrSpoolCorrupt
	}
	return s.removeSendingPathLocked(path, "delete:corruption", false)
}

func (s *Store) quarantineReadyEntryLocked(batchID *uuid.UUID, readyName string) (AppliedCorruption, error) {
	tombstone, err := newCorruptionTombstone(readyName)
	if err != nil {
		return AppliedCorruption{}, err
	}
	path := corruptionPath(s, tombstone.ID)
	persisted, err := s.readCorruptionTombstone(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := s.writeCorruptionTombstoneLocked(tombstone); err != nil {
			return AppliedCorruption{}, err
		}
	} else if err != nil || persisted.ID != tombstone.ID {
		return AppliedCorruption{}, ErrSpoolCorrupt
	} else {
		tombstone = persisted
	}
	return s.applyCorruptionTombstoneLocked(batchID, tombstone)
}

func (s *Store) applyCorruptionTombstoneLocked(batchID *uuid.UUID, tombstone corruptionTombstone) (AppliedCorruption, error) {
	view, err := openCorruptionDirectoryView(s)
	if err != nil {
		return AppliedCorruption{}, err
	}
	defer view.close()
	inspection, err := inspectReadyEntryNames(tombstone, view.readyNames)
	if err != nil {
		return AppliedCorruption{}, err
	}
	destinationFound := exactNamePresent(view.quarantineNames, string(tombstone.ID))
	if inspection.sourceName != "" && destinationFound {
		return AppliedCorruption{}, ErrSpoolCorrupt
	}

	if tombstone.CaptureID != uuid.Nil {
		if batchID != nil {
			if err := s.retireExactBatchReferenceLocked(*batchID, tombstone.CaptureID); err != nil {
				return AppliedCorruption{}, err
			}
		} else if err := s.retirePendingBatchReferencesLocked(tombstone.CaptureID); err != nil {
			return AppliedCorruption{}, err
		}
	} else if tombstone.AliasCaptureID != uuid.Nil {
		if inspection.sourceName != "" && inspection.canonicalTwin {
			canonicalManifest, err := readReadyManifestAt(view.ready, tombstone.AliasCaptureID.String())
			if err != nil {
				return AppliedCorruption{}, ErrSpoolCorrupt
			}
			if err := s.retirePendingBatchReferencesExceptManifestLocked(tombstone.AliasCaptureID, sha256Hex(canonicalManifest)); err != nil {
				return AppliedCorruption{}, err
			}
		} else if inspection.sourceName != "" {
			// Before canonical-name enforcement, NextBatch could publish a
			// manifest from this alias. With no canonical twin, a manifest
			// referencing the parsed UUID can only be an unusable derivative
			// of the alias loss and must not be counted again later.
			if err := s.retirePendingBatchReferencesLocked(tombstone.AliasCaptureID); err != nil {
				return AppliedCorruption{}, err
			}
		}
	}
	if !tombstone.Applied {
		if err := s.moveCorruptReadyEntryLocked(tombstone, view, inspection); err != nil {
			return AppliedCorruption{}, err
		}
		if tombstone.CaptureID != uuid.Nil {
			s.removeReady(tombstone.CaptureID)
		}
		var removedAllocated int64
		removeErr := s.capacity.trackAllocationDeletion([]string{s.quarantineDir}, false, func() error {
			var err error
			removedAllocated, err = removeDirectoryEntryNoFollowAllocated(view.quarantine, string(tombstone.ID))
			return err
		})
		if removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return AppliedCorruption{}, fmt.Errorf("delete quarantined ready record: %w", removeErr)
		}
		if removeErr == nil {
			if err := s.capacity.releaseAllocated(removedAllocated, false); err != nil {
				return AppliedCorruption{}, fmt.Errorf("account quarantined ready record deletion: %w", err)
			}
		}
		s.event("delete:quarantined-ready-record")
		if err := s.quarantineSyncDirectory(view.quarantine); err != nil {
			return AppliedCorruption{}, fmt.Errorf("fsync quarantine directory after delete: %w", err)
		}
		s.event("fsync:quarantine-dir")
		tombstone.Applied = true
		if err := s.writeCorruptionTombstoneLocked(tombstone); err != nil {
			return AppliedCorruption{}, err
		}
	}
	s.accountCorruption(tombstone.ID)
	return AppliedCorruption{ID: tombstone.ID, CaptureID: tombstone.CaptureID}, nil
}

func (s *Store) moveCorruptReadyEntryLocked(tombstone corruptionTombstone, view *corruptionDirectoryView, inspection readyEntryInspection) error {
	sourceName := inspection.sourceName
	destinationName := string(tombstone.ID)
	if sourceName != "" {
		if err := s.capacity.trackAllocationMutationWithOperationalGrowth([]string{s.readyDir, s.quarantineDir}, false, func() error {
			return renameDirectoryEntryNoReplace(view.ready, sourceName, view.quarantine, destinationName)
		}); err != nil {
			return fmt.Errorf("quarantine ready record: %w", err)
		}
		s.event("rename:ready-to-quarantine")
	}
	if err := s.readySyncDirectory(view.ready); err != nil {
		return fmt.Errorf("fsync ready directory: %w", err)
	}
	s.event("fsync:ready-dir")
	if err := s.quarantineSyncDirectory(view.quarantine); err != nil {
		return fmt.Errorf("fsync quarantine directory: %w", err)
	}
	s.event("fsync:quarantine-dir")
	return nil
}

func inspectReadyEntryNames(tombstone corruptionTombstone, names []string) (readyEntryInspection, error) {
	var inspection readyEntryInspection
	if tombstone.NameSHA256 == "" {
		name := tombstone.CaptureID.String()
		if exactNamePresent(names, name) {
			inspection.sourceName = name
		}
		return inspection, nil
	}
	canonicalName := ""
	if tombstone.AliasCaptureID != uuid.Nil {
		canonicalName = tombstone.AliasCaptureID.String()
	}
	for _, name := range names {
		if canonicalName != "" && name == canonicalName {
			inspection.canonicalTwin = true
		}
		if isCanonicalCaptureName(name) || opaqueReadyNameID(name) != tombstone.ID {
			continue
		}
		parsedAlias := uuid.Nil
		if parsed, err := uuid.Parse(name); err == nil {
			parsedAlias = parsed
		}
		if parsedAlias != tombstone.AliasCaptureID || inspection.sourceName != "" {
			return readyEntryInspection{}, ErrSpoolCorrupt
		}
		inspection.sourceName = name
	}
	return inspection, nil
}

func openCorruptionDirectoryView(s *Store) (*corruptionDirectoryView, error) {
	ready, err := openBatchDirectory(s.readyDir)
	if err != nil {
		return nil, fmt.Errorf("open ready directory: %w", err)
	}
	quarantine, err := openBatchDirectory(s.quarantineDir)
	if err != nil {
		_ = ready.Close()
		return nil, fmt.Errorf("open quarantine directory: %w", err)
	}
	view := &corruptionDirectoryView{ready: ready, quarantine: quarantine}
	view.readyNames, err = readExactDirectoryNames(ready)
	if err == nil {
		view.quarantineNames, err = readExactDirectoryNames(quarantine)
	}
	if err != nil {
		view.close()
		return nil, fmt.Errorf("read corruption directory view: %w", err)
	}
	return view, nil
}

func (view *corruptionDirectoryView) close() {
	_ = view.quarantine.Close()
	_ = view.ready.Close()
}

func readExactDirectoryNames(directory *os.File) ([]string, error) {
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(entries))
	for index := range entries {
		names[index] = entries[index].Name()
	}
	return names, nil
}

func exactNamePresent(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
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
		if err != nil || string(tombstone.ID)+corruptionSuffix != entry.Name() {
			return nil, ErrSpoolCorrupt
		}
		corruption, err := s.applyCorruptionTombstoneLocked(nil, tombstone)
		if err != nil {
			return nil, err
		}
		applied = appendCorruption(applied, corruption)
	}
	return applied, nil
}

func (s *Store) accountCorruption(id CorruptionID) {
	s.dropMu.Lock()
	defer s.dropMu.Unlock()
	if _, exists := s.accountedCorruptions[id]; exists {
		return
	}
	s.accountedCorruptions[id] = struct{}{}
	s.dropped[ErrSpoolCorrupt.Error()]++
}

func (s *Store) writeCorruptionTombstoneLocked(tombstone corruptionTombstone) error {
	wire := corruptionTombstoneWire{
		Version:    tombstone.Version,
		ID:         string(tombstone.ID),
		NameSHA256: tombstone.NameSHA256,
		Applied:    tombstone.Applied,
	}
	if tombstone.CaptureID != uuid.Nil {
		captureID := tombstone.CaptureID
		wire.CaptureID = &captureID
	}
	if tombstone.AliasCaptureID != uuid.Nil {
		aliasCaptureID := tombstone.AliasCaptureID
		wire.AliasCaptureID = &aliasCaptureID
	}
	if tombstone.Version == legacyCorruptionTombstoneVersion {
		wire.ID = ""
		wire.AliasCaptureID = nil
		wire.NameSHA256 = ""
	}
	encoded, err := json.Marshal(wire)
	if err != nil || len(encoded) > maxCorruptionTombstoneBytes {
		return ErrSpoolCorrupt
	}
	reservation, err := s.capacity.reserveOperationalFilesUnblocking(int64(len(encoded)))
	if err != nil {
		return err
	}
	finalPath := corruptionPath(s, tombstone.ID)
	tempPath := finalPath + ".tmp"
	if err := s.capacity.trackAllocationDeletion([]string{s.sendingDir, tempPath}, true, func() error {
		return os.Remove(tempPath)
	}); err != nil && !errors.Is(err, os.ErrNotExist) {
		reservation.Release()
		return fmt.Errorf("remove corruption temp: %w", err)
	}
	return s.writeBatchMetadata(tempPath, finalPath, encoded, "corruption.tmp", "corruption", reservation)
}

func (s *Store) readCorruptionTombstone(path string) (corruptionTombstone, error) {
	encoded, err := s.readBoundedFile(path, maxCorruptionTombstoneBytes)
	if err != nil {
		return corruptionTombstone{}, err
	}
	var wire corruptionTombstoneWire
	if err := decodeStrictJSON(encoded, &wire); err != nil {
		return corruptionTombstone{}, ErrSpoolCorrupt
	}
	switch wire.Version {
	case legacyCorruptionTombstoneVersion:
		if wire.CaptureID == nil || *wire.CaptureID == uuid.Nil || wire.ID != "" || wire.AliasCaptureID != nil || wire.NameSHA256 != "" {
			return corruptionTombstone{}, ErrSpoolCorrupt
		}
		return corruptionTombstone{
			Version: wire.Version, ID: CorruptionID(wire.CaptureID.String()), CaptureID: *wire.CaptureID, Applied: wire.Applied,
		}, nil
	case corruptionTombstoneVersion:
		id := CorruptionID(wire.ID)
		if !validOpaqueCorruptionID(id) || wire.NameSHA256 != wire.ID || wire.CaptureID != nil {
			return corruptionTombstone{}, ErrSpoolCorrupt
		}
		aliasCaptureID := uuid.Nil
		if wire.AliasCaptureID != nil {
			if *wire.AliasCaptureID == uuid.Nil {
				return corruptionTombstone{}, ErrSpoolCorrupt
			}
			aliasCaptureID = *wire.AliasCaptureID
		}
		return corruptionTombstone{
			Version: wire.Version, ID: id, AliasCaptureID: aliasCaptureID, NameSHA256: wire.NameSHA256, Applied: wire.Applied,
		}, nil
	default:
		return corruptionTombstone{}, ErrSpoolCorrupt
	}
}

func newCorruptionTombstone(readyName string) (corruptionTombstone, error) {
	if !validReadyEntryName(readyName) {
		return corruptionTombstone{}, ErrSpoolCorrupt
	}
	if captureID, err := uuid.Parse(readyName); err == nil && readyName == captureID.String() {
		return corruptionTombstone{
			Version: legacyCorruptionTombstoneVersion, ID: CorruptionID(captureID.String()), CaptureID: captureID,
		}, nil
	}
	id := opaqueReadyNameID(readyName)
	aliasCaptureID := uuid.Nil
	if parsed, err := uuid.Parse(readyName); err == nil {
		aliasCaptureID = parsed
	}
	return corruptionTombstone{
		Version: corruptionTombstoneVersion, ID: id, AliasCaptureID: aliasCaptureID, NameSHA256: string(id),
	}, nil
}

func opaqueReadyNameID(readyName string) CorruptionID {
	digest := sha256.Sum256([]byte(corruptionNameDomain + readyName))
	return CorruptionID(hex.EncodeToString(digest[:]))
}

func isCanonicalCaptureName(name string) bool {
	id, err := uuid.Parse(name)
	return err == nil && name == id.String()
}

func validReadyEntryName(name string) bool {
	return name != "" && name != "." && name != ".." && name == filepath.Base(name)
}

func validCorruptionID(id CorruptionID) bool {
	if validOpaqueCorruptionID(id) {
		return true
	}
	parsed, err := uuid.Parse(string(id))
	return err == nil && string(id) == parsed.String()
}

func validOpaqueCorruptionID(id CorruptionID) bool {
	if len(id) != sha256.Size*2 || strings.ToLower(string(id)) != string(id) {
		return false
	}
	decoded, err := hex.DecodeString(string(id))
	return err == nil && len(decoded) == sha256.Size
}

func corruptionPath(s *Store, id CorruptionID) string {
	return filepath.Join(s.sendingDir, string(id)+corruptionSuffix)
}

func appendCorruption(items []AppliedCorruption, item AppliedCorruption) []AppliedCorruption {
	for _, existing := range items {
		if existing.ID == item.ID {
			return items
		}
	}
	return append(items, item)
}
