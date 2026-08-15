package spool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/capture/model"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

const testBatchRowOverhead int64 = 1 << 20

func TestNextBatchIsStableAndRespectsBothLimits(t *testing.T) {
	s := openTestStore(t, nil)
	readyAt := time.Unix(1_700_000_000, 0).UTC()
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000003"),
		uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		uuid.MustParse("00000000-0000-0000-0000-000000000002"),
	}
	commitSizedRecord(t, s, ids[0], 40, readyAt.Add(time.Second))
	commitSizedRecord(t, s, ids[1], 40, readyAt)
	commitSizedRecord(t, s, ids[2], 40, readyAt)
	recoverStore(t, s)

	b, err := s.NextBatch(100, 2*testBatchRowOverhead+80)

	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{ids[1], ids[2]}, batchCaptureIDs(b))
	require.Equal(t, b.ID.String(), b.DeduplicationToken())
	require.FileExists(t, batchManifestPath(s, b.ID))
	for _, id := range ids {
		require.DirExists(t, filepath.Join(s.readyDir, id.String()))
	}

	replayed, err := s.NextBatch(1, 1)
	require.NoError(t, err)
	require.Equal(t, b.ID, replayed.ID)
	require.Equal(t, b.Records, replayed.Records)
	require.Equal(t, b.DeduplicationToken(), replayed.DeduplicationToken())
}

func TestNextBatchRespectsRecordLimit(t *testing.T) {
	s := openTestStore(t, nil)
	readyAt := time.Unix(1_700_000_000, 0).UTC()
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-0000-0000-000000000004"),
		uuid.MustParse("00000000-0000-0000-0000-000000000005"),
		uuid.MustParse("00000000-0000-0000-0000-000000000006"),
	}
	for i, id := range ids {
		commitSizedRecord(t, s, id, 8, readyAt.Add(time.Duration(i)*time.Second))
	}
	recoverStore(t, s)

	b, err := s.NextBatch(2, 64<<20)

	require.NoError(t, err)
	require.Equal(t, ids[:2], batchCaptureIDs(b))
}

func TestConcurrentNextBatchCallersReceiveSameDurableBatch(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000007")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)

	const callers = 8
	start := make(chan struct{})
	results := make(chan *Batch, callers)
	errors := make(chan error, callers)
	for range callers {
		go func() {
			<-start
			b, err := s.NextBatch(100, 64<<20)
			results <- b
			errors <- err
		}()
	}
	close(start)
	var batchID uuid.UUID
	for range callers {
		require.NoError(t, <-errors)
		b := <-results
		require.NotNil(t, b)
		if batchID == uuid.Nil {
			batchID = b.ID
		}
		require.Equal(t, batchID, b.ID)
		require.Equal(t, []uuid.UUID{id}, batchCaptureIDs(b))
	}
	require.Len(t, readDirectoryNames(t, s.sendingDir), 1)
}

func TestNextBatchRejectsFirstRecordAboveByteCeilingWithoutMutation(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000010")
	commitSizedRecord(t, s, id, 40, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)

	b, err := s.NextBatch(100, testBatchRowOverhead+39)

	require.Nil(t, b)
	require.ErrorIs(t, err, ErrBatchByteLimit)
	require.Equal(t, []uuid.UUID{id}, readyCaptureIDs(s))
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
}

func TestNextBatchReturnsNilWithoutWritingMetadataWhenReadyIsEmpty(t *testing.T) {
	s := openTestStore(t, nil)

	b, err := s.NextBatch(0, 0)

	require.NoError(t, err)
	require.Nil(t, b)
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
}

func TestNextBatchRetriesSendingDirectoryFsyncBeforeExposingRenamedManifest(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000008")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	var chmodErr error
	s.config.eventHook = func(event string) {
		if event == "rename:batch.manifest" {
			chmodErr = os.Chmod(s.sendingDir, 0)
		}
	}

	b, err := s.NextBatch(100, 64<<20)

	require.NoError(t, chmodErr)
	require.Nil(t, b)
	require.Error(t, err)
	require.NoError(t, os.Chmod(s.sendingDir, 0o700))
	t.Cleanup(func() { _ = os.Chmod(s.sendingDir, 0o700) })
	entries := readDirectoryNames(t, s.sendingDir)
	require.Len(t, entries, 1)
	require.True(t, strings.HasSuffix(entries[0], ".manifest"))

	recorder := &eventRecorder{}
	s.config.eventHook = recorder.add
	b, err = s.NextBatch(1, 1)

	require.NoError(t, err)
	require.NotNil(t, b)
	require.Equal(t, []uuid.UUID{id}, batchCaptureIDs(b))
	require.Equal(t, []string{"fsync:sending-dir"}, recorder.durableEvents())
}

func TestBatchOpenRecordOnlyOpensMembers(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000020")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(1, 64<<20)
	require.NoError(t, err)

	ref, err := b.OpenRecord(id)
	require.NoError(t, err)
	require.Equal(t, id, ref.CaptureID)
	require.DirExists(t, ref.Path)

	_, err = b.OpenRecord(uuid.New())
	require.Error(t, err)
}

func TestManifestIsDurableBeforeBatchIsReturned(t *testing.T) {
	recorder := &eventRecorder{}
	s := openTestStore(t, nil)
	commitSizedRecord(t, s, uuid.New(), 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	s.config.eventHook = recorder.add

	b, err := s.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.NotNil(t, b)
	require.Equal(t, []string{
		"fsync:batch.tmp",
		"rename:batch.manifest",
		"fsync:sending-dir",
	}, recorder.durableEvents())
	require.FileExists(t, batchManifestPath(s, b.ID))
	require.NoFileExists(t, batchManifestPath(s, b.ID)+".tmp")
}

func TestBatchManifestPersistsOrderedIDsHashesAndUncompressedSizes(t *testing.T) {
	s := openTestStore(t, nil)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000021")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000022")
	readyAt := time.Unix(1_700_000_000, 0).UTC()
	commitSizedRecord(t, s, first, 7, readyAt)
	commitSizedRecord(t, s, second, 11, readyAt.Add(time.Second))
	recoverStore(t, s)

	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	encoded, err := os.ReadFile(batchManifestPath(s, b.ID))
	require.NoError(t, err)
	var manifest BatchManifest
	require.NoError(t, json.Unmarshal(encoded, &manifest))

	require.Equal(t, b.ID, manifest.BatchID)
	require.Equal(t, []uuid.UUID{first, second}, []uuid.UUID{
		manifest.Records[0].CaptureID,
		manifest.Records[1].CaptureID,
	})
	require.Equal(t, []int64{7, 11}, []int64{
		manifest.Records[0].StoredBytes,
		manifest.Records[1].StoredBytes,
	})
	for i, id := range []uuid.UUID{first, second} {
		recordManifest, err := os.ReadFile(readyPath(s, id, manifestName))
		require.NoError(t, err)
		sum := sha256.Sum256(recordManifest)
		require.Equal(t, hex.EncodeToString(sum[:]), manifest.Records[i].ManifestSHA256)
	}
}

func TestNextBatchOperationalMetadataMayUseHeadroomBelowFreeReserve(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000030")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	s.capacity.usageFn = func() (usage, error) {
		return usage{Free: 1 << 20, BlockSize: filesystemBlockSize}, nil
	}

	b, err := s.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.NotNil(t, b)
	require.FileExists(t, batchManifestPath(s, b.ID))
	require.Zero(t, s.capacity.reservedBytes())
}

func TestNextBatchOperationalMetadataCannotReserveMoreThanActualFreeSpace(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000035")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	s.capacity.usageFn = func() (usage, error) {
		return usage{Free: filesystemBlockSize, BlockSize: filesystemBlockSize}, nil
	}

	b, err := s.NextBatch(100, 64<<20)

	require.Nil(t, b)
	require.ErrorIs(t, err, ErrFreeReserve)
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
	require.Equal(t, []uuid.UUID{id}, readyCaptureIDs(s))
}

func TestNextBatchReservesManifestAndAckWithoutExceedingOperationalHeadroom(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000031")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	s.capacity.usageFn = func() (usage, error) {
		return usage{
			Allocated:            s.config.OperationalHeadroomBytes - filesystemBlockSize,
			OperationalAllocated: s.config.OperationalHeadroomBytes - filesystemBlockSize,
			Free:                 20 << 30,
			BlockSize:            filesystemBlockSize,
		}, nil
	}

	b, err := s.NextBatch(100, 64<<20)

	require.Nil(t, b)
	require.ErrorIs(t, err, ErrSpoolCap)
	require.Equal(t, []uuid.UUID{id}, readyCaptureIDs(s))
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
	require.Zero(t, s.capacity.reservedBytes())
}

func TestNextBatchOperationalReservationUsesMeasuredFilesystemBlockSize(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000034")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	s.capacity.usageFn = func() (usage, error) {
		return usage{
			Allocated:            s.config.OperationalHeadroomBytes - 8192,
			OperationalAllocated: s.config.OperationalHeadroomBytes - 8192,
			Free:                 20 << 30,
			BlockSize:            8192,
		}, nil
	}

	b, err := s.NextBatch(100, 64<<20)

	require.Nil(t, b)
	require.ErrorIs(t, err, ErrSpoolCap)
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
	require.Equal(t, []uuid.UUID{id}, readyCaptureIDs(s))
}

func TestNextBatchNeverExceedsPhysicalCap(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000032")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	s.capacity.usageFn = func() (usage, error) {
		return usage{
			Allocated: s.config.MaxBytes - filesystemBlockSize,
			Free:      20 << 30,
			BlockSize: filesystemBlockSize,
		}, nil
	}

	b, err := s.NextBatch(100, 64<<20)

	require.Nil(t, b)
	require.ErrorIs(t, err, ErrSpoolCap)
	require.Equal(t, []uuid.UUID{id}, readyCaptureIDs(s))
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
	require.Zero(t, s.capacity.reservedBytes())
}

func TestNextBatchMetadataCreateFailurePreservesReadyAndReleasesReservation(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000033")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	openFile := s.config.openFile
	injected := errors.New("injected batch manifest create failure")
	s.config.openFile = func(path string, flags int, mode os.FileMode) (*os.File, error) {
		if strings.HasSuffix(path, ".manifest.tmp") {
			return nil, injected
		}
		return openFile(path, flags, mode)
	}

	b, err := s.NextBatch(100, 64<<20)

	require.Nil(t, b)
	require.ErrorIs(t, err, injected)
	require.Equal(t, []uuid.UUID{id}, readyCaptureIDs(s))
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
	require.Zero(t, s.capacity.reservedBytes())

	s.config.openFile = openFile
	b, err = s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NotNil(t, b)
}

func TestRecoveryUsesSameBatchAfterRemoteCommitBeforeLocalAck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000040")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000041")
	readyAt := time.Unix(1_700_000_000, 0).UTC()
	commitSizedRecord(t, s, first, 8, readyAt)
	commitSizedRecord(t, s, second, 8, readyAt.Add(time.Second))
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(1, 1)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, b.ID, got.ID)
	require.Equal(t, b.Records, got.Records)
	require.Equal(t, b.DeduplicationToken(), got.DeduplicationToken())
	require.Len(t, reopened.PendingBatches(), 1)
}

func TestRecoveryAfterManifestTempFsyncDiscardsUnpublishedTempAndReselects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000050")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	abandoned, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	finalPath := batchManifestPath(s, abandoned.ID)
	tempPath := finalPath + ".tmp"
	require.NoError(t, os.Rename(finalPath, tempPath))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	require.NoFileExists(t, tempPath)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotEqual(t, abandoned.ID, got.ID)
	require.Equal(t, []uuid.UUID{id}, batchCaptureIDs(got))
}

func TestRecoveryAfterManifestRenameReplaysExactManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000051")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID)
	require.Equal(t, b.Records, got.Records)
}

func TestAckMarkerIsDurableBeforeMarkAckedReturns(t *testing.T) {
	recorder := &eventRecorder{}
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000060")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	s.config.eventHook = recorder.add

	err = s.MarkAcked(b)

	require.NoError(t, err)
	require.Equal(t, []string{
		"fsync:batch.acked.tmp",
		"rename:batch.acked",
		"fsync:sending-dir",
	}, recorder.durableEvents())
	require.FileExists(t, batchAckPath(s, b.ID))
	require.NoFileExists(t, batchAckPath(s, b.ID)+".tmp")
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))
}

func TestMarkAckedRetriesSendingDirectoryFsyncBeforeAcceptingRenamedAck(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000065")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	var chmodErr error
	s.config.eventHook = func(event string) {
		if event == "rename:batch.acked" {
			chmodErr = os.Chmod(s.sendingDir, 0)
		}
	}

	err = s.MarkAcked(b)

	require.NoError(t, chmodErr)
	require.Error(t, err)
	require.NoError(t, os.Chmod(s.sendingDir, 0o700))
	t.Cleanup(func() { _ = os.Chmod(s.sendingDir, 0o700) })
	require.FileExists(t, batchManifestPath(s, b.ID))
	require.FileExists(t, batchAckPath(s, b.ID))
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))

	recorder := &eventRecorder{}
	s.config.eventHook = recorder.add
	err = s.MarkAcked(b)

	require.NoError(t, err)
	require.Equal(t, []string{"fsync:sending-dir"}, recorder.durableEvents())
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))
}

func TestMarkAckedCreateFailurePreservesManifestAndReadyAndReleasesReservation(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000063")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	openFile := s.config.openFile
	injected := errors.New("injected ack create failure")
	s.config.openFile = func(path string, flags int, mode os.FileMode) (*os.File, error) {
		if strings.HasSuffix(path, ".acked.tmp") {
			return nil, injected
		}
		return openFile(path, flags, mode)
	}

	err = s.MarkAcked(b)

	require.ErrorIs(t, err, injected)
	require.FileExists(t, batchManifestPath(s, b.ID))
	require.NoFileExists(t, batchAckPath(s, b.ID))
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))
	require.Zero(t, s.capacity.reservedBytes())
	s.config.openFile = openFile
	replayed, err := s.NextBatch(1, 1)
	require.NoError(t, err)
	require.Equal(t, b.ID, replayed.ID)
}

func TestCleanupWithoutAckPreservesManifestAndReady(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000064")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)

	err = s.CleanupAcked(b)

	require.ErrorIs(t, err, ErrBatchNotAcked)
	require.FileExists(t, batchManifestPath(s, b.ID))
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))
}

func TestRecoveryAfterAckTempFsyncReplaysUnackedManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000061")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))
	ackPath := batchAckPath(s, b.ID)
	tempPath := ackPath + ".tmp"
	require.NoError(t, os.Rename(ackPath, tempPath))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	require.NoFileExists(t, tempPath)
	got, err := reopened.NextBatch(1, 1)

	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID)
	require.Equal(t, b.Records, got.Records)
}

func TestRecoveryAfterAckRenameCleansWithoutReupload(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000062")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, reopened.PendingBatches())
	require.Empty(t, reopened.Ready())
	require.NoDirExists(t, filepath.Join(reopened.readyDir, id.String()))
	require.Empty(t, readDirectoryNames(t, reopened.sendingDir))
}

func TestRecoveryAfterEachIndividualReadyDeletionIsIdempotent(t *testing.T) {
	for deletedBeforeCrash := 0; deletedBeforeCrash <= 3; deletedBeforeCrash++ {
		t.Run(fmt.Sprintf("deleted_%d", deletedBeforeCrash), func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			s := openTestStoreAt(t, root, nil)
			readyAt := time.Unix(1_700_000_000, 0).UTC()
			ids := []uuid.UUID{
				uuid.MustParse("00000000-0000-0000-0000-000000000071"),
				uuid.MustParse("00000000-0000-0000-0000-000000000072"),
				uuid.MustParse("00000000-0000-0000-0000-000000000073"),
			}
			for i, id := range ids {
				commitSizedRecord(t, s, id, 8, readyAt.Add(time.Duration(i)*time.Second))
			}
			recoverStore(t, s)
			b, err := s.NextBatch(100, 64<<20)
			require.NoError(t, err)
			require.NoError(t, s.MarkAcked(b))
			for _, id := range ids[:deletedBeforeCrash] {
				require.NoError(t, os.RemoveAll(filepath.Join(s.readyDir, id.String())))
			}
			require.NoError(t, syncDirectory(s.readyDir))

			reopened := openTestStoreAt(t, root, nil)
			recoverStore(t, reopened)
			require.NoError(t, reopened.RecoverAcked())
			got, err := reopened.NextBatch(100, 64<<20)

			require.NoError(t, err)
			require.Nil(t, got)
			require.Empty(t, reopened.Ready())
			require.Empty(t, reopened.PendingBatches())
			require.Empty(t, readDirectoryNames(t, reopened.readyDir))
			require.Empty(t, readDirectoryNames(t, reopened.sendingDir))
		})
	}
}

func TestRecoveryAfterManifestProofDeletionOnlyRetiresOrphanAck(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000074")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))
	require.NoError(t, os.RemoveAll(filepath.Join(s.readyDir, id.String())))
	require.NoError(t, syncDirectory(s.readyDir))
	require.NoError(t, os.Remove(batchManifestPath(s, b.ID)))
	require.NoError(t, syncDirectory(s.sendingDir))
	require.FileExists(t, batchAckPath(s, b.ID))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.Nil(t, got)
	require.Empty(t, readDirectoryNames(t, reopened.readyDir))
	require.Empty(t, readDirectoryNames(t, reopened.sendingDir))
}

func TestCleanupAckedMakesReadyDurableBeforeRetiringAckProof(t *testing.T) {
	recorder := &eventRecorder{}
	s := openTestStore(t, nil)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000080")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000081")
	readyAt := time.Unix(1_700_000_000, 0).UTC()
	commitSizedRecord(t, s, first, 8, readyAt)
	commitSizedRecord(t, s, second, 8, readyAt.Add(time.Second))
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))
	s.config.eventHook = recorder.add

	err = s.CleanupAcked(b)

	require.NoError(t, err)
	require.Equal(t, []string{
		"fsync:sending-dir",
		"delete:ready-record",
		"delete:ready-record",
		"fsync:ready-dir",
		"delete:batch.manifest",
		"fsync:sending-dir",
		"delete:batch.acked",
		"fsync:sending-dir",
	}, recorder.durableEvents())
	require.Empty(t, readDirectoryNames(t, s.readyDir))
	require.Empty(t, readDirectoryNames(t, s.sendingDir))
}

func TestCorruptAckCannotOverrideValidUnackedManifest(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000090")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))
	require.NoError(t, os.WriteFile(batchAckPath(s, b.ID), []byte("{broken"), 0o600))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(1, 1)

	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID)
	require.Equal(t, b.Records, got.Records)
	require.NoFileExists(t, batchAckPath(reopened, b.ID))
	require.FileExists(t, batchManifestPath(reopened, b.ID))
	require.DirExists(t, filepath.Join(reopened.readyDir, id.String()))
	require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
}

func TestPendingBatchesTreatsCorruptAckAsUnacked(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000098")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))
	require.NoError(t, os.WriteFile(batchAckPath(s, b.ID), []byte("{broken"), 0o600))
	require.NoError(t, syncDirectory(s.sendingDir))

	pending := s.PendingBatches()

	require.Len(t, pending, 1)
	require.Equal(t, b.ID, pending[0].ID)
	require.NoFileExists(t, batchAckPath(s, b.ID))
	require.FileExists(t, batchManifestPath(s, b.ID))
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))
	require.EqualValues(t, 1, s.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
}

func TestCorruptSendingManifestDoesNotBlockUnrelatedReadyRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	first := uuid.MustParse("00000000-0000-0000-0000-000000000091")
	second := uuid.MustParse("00000000-0000-0000-0000-000000000092")
	readyAt := time.Unix(1_700_000_000, 0).UTC()
	commitSizedRecord(t, s, first, 8, readyAt)
	commitSizedRecord(t, s, second, 8, readyAt.Add(time.Second))
	recoverStore(t, s)
	corrupt, err := s.NextBatch(1, 64<<20)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(batchManifestPath(s, corrupt.ID), []byte("{broken"), 0o600))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotEqual(t, corrupt.ID, got.ID)
	require.Equal(t, []uuid.UUID{first, second}, batchCaptureIDs(got))
	require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
	require.DirExists(t, filepath.Join(reopened.readyDir, first.String()))
	require.DirExists(t, filepath.Join(reopened.readyDir, second.String()))
}

func TestSendingManifestDirectoryIsDeterministicCorruptionAndDoesNotBlockReady(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000095")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	badID := uuid.MustParse("00000000-0000-0000-0000-000000000096")
	require.NoError(t, os.Mkdir(batchManifestPath(s, badID), 0o700))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, []uuid.UUID{id}, batchCaptureIDs(got))
	require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
}

func TestSymlinkAckCannotDeleteReadyRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000097")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.NoError(t, s.MarkAcked(b))
	ackPath := batchAckPath(s, b.ID)
	ack, err := os.ReadFile(ackPath)
	require.NoError(t, err)
	externalAck := filepath.Join(t.TempDir(), "external-ack")
	require.NoError(t, os.WriteFile(externalAck, ack, 0o600))
	require.NoError(t, os.Remove(ackPath))
	require.NoError(t, os.Symlink(externalAck, ackPath))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(1, 1)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, b.ID, got.ID)
	require.DirExists(t, filepath.Join(reopened.readyDir, id.String()))
	require.NoFileExists(t, ackPath)
	require.FileExists(t, externalAck)
	require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
}

func TestCorruptSendingTempDoesNotBlockReadyRecords(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	s := openTestStoreAt(t, root, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000093")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	corrupt, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	finalPath := batchManifestPath(s, corrupt.ID)
	tempPath := finalPath + ".tmp"
	require.NoError(t, os.Rename(finalPath, tempPath))
	require.NoError(t, os.WriteFile(tempPath, []byte("{broken"), 0o600))
	require.NoError(t, syncDirectory(s.sendingDir))

	reopened := openTestStoreAt(t, root, nil)
	recoverStore(t, reopened)
	got, err := reopened.NextBatch(100, 64<<20)

	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotEqual(t, corrupt.ID, got.ID)
	require.Equal(t, []uuid.UUID{id}, batchCaptureIDs(got))
	require.EqualValues(t, 1, reopened.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])
}

func TestTransientSendingManifestReadFailurePreservesManifestAndReady(t *testing.T) {
	s := openTestStore(t, nil)
	id := uuid.MustParse("00000000-0000-0000-0000-000000000094")
	commitSizedRecord(t, s, id, 8, time.Unix(1_700_000_000, 0).UTC())
	recoverStore(t, s)
	b, err := s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	manifestPath := batchManifestPath(s, b.ID)
	require.NoError(t, os.Chmod(manifestPath, 0))
	t.Cleanup(func() { _ = os.Chmod(manifestPath, 0o600) })

	got, err := s.NextBatch(100, 64<<20)

	require.Nil(t, got)
	require.Error(t, err)
	require.FileExists(t, manifestPath)
	require.DirExists(t, filepath.Join(s.readyDir, id.String()))
	require.Zero(t, s.Snapshot().DroppedByReason[ErrSpoolCorrupt.Error()])

	require.NoError(t, os.Chmod(manifestPath, 0o600))
	got, err = s.NextBatch(100, 64<<20)
	require.NoError(t, err)
	require.Equal(t, b.ID, got.ID)
}

func commitSizedRecord(t *testing.T, s *Store, id uuid.UUID, size int, readyAt time.Time) {
	t.Helper()
	sink, err := s.Open(model.Begin{
		CaptureID:  id,
		CapturedAt: readyAt,
		Policy: model.ContentPolicy{
			StoreRequestBody: true,
		},
	})
	require.NoError(t, err)
	a := sink.(*Attempt)
	payload := bytes.Repeat([]byte(" "), size)
	if size == 1 {
		payload[0] = '0'
	} else if size >= 2 {
		payload[0] = '{'
		payload[size-1] = '}'
	}
	require.NoError(t, a.WriteRequest(payload))
	require.NoError(t, a.Commit())
	recordPath := filepath.Join(s.readyDir, id.String())
	require.NoError(t, os.Chtimes(recordPath, readyAt, readyAt))
}

func recoverStore(t *testing.T, s *Store) {
	t.Helper()
	_, err := s.Recover(context.Background())
	require.NoError(t, err)
}

func batchCaptureIDs(batch *Batch) []uuid.UUID {
	if batch == nil {
		return nil
	}
	ids := make([]uuid.UUID, len(batch.Records))
	for i := range batch.Records {
		ids[i] = batch.Records[i].CaptureID
	}
	return ids
}

func readyCaptureIDs(s *Store) []uuid.UUID {
	ready := s.Ready()
	ids := make([]uuid.UUID, len(ready))
	for i := range ready {
		ids[i] = ready[i].CaptureID
	}
	return ids
}

func readDirectoryNames(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	require.NoError(t, err)
	names := make([]string, len(entries))
	for i := range entries {
		names[i] = entries[i].Name()
	}
	return names
}
