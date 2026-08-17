package spool

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCapacityScansOnceThenUsesCachedAllocationForConstantTimeAdmission(t *testing.T) {
	var scans atomic.Int64
	capacity, err := newCapacity(CapacityConfig{
		RootDir:                  t.TempDir(),
		MaxBytes:                 12 << 30,
		MinFreeBytes:             8 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, func() (usage, error) {
		scans.Add(1)
		return usage{Allocated: 8192, Free: 20 << 30, BlockSize: filesystemBlockSize}, nil
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, scans.Load(), "the recursive inventory is a startup-only operation")

	content, err := capacity.ReserveContent(uuid.New(), 8192)
	require.NoError(t, err)
	frame, err := capacity.ReserveFrame(uuid.New(), 4033)
	require.NoError(t, err)
	require.NoError(t, content.Consume(4096))
	frame.Release()
	got, err := capacity.snapshot()
	require.NoError(t, err)

	require.EqualValues(t, 1, scans.Load(), "Begin, frames, reconciliation, and status must not rescan the spool tree")
	require.EqualValues(t, 8192+4096, got.Allocated)
}

func TestCapacityAdmissionDoesNotWaitForFilesystemMutationAccounting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deleted")
	require.NoError(t, os.WriteFile(path, bytes.Repeat([]byte("x"), 8192), 0o600))
	initial, err := scanUsage(root)
	require.NoError(t, err)
	capacity := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30}, initial)
	mutationStarted := make(chan struct{})
	releaseMutation := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- capacity.trackAllocationDeletion([]string{root, path}, false, func() error {
			if err := os.Remove(path); err != nil {
				return err
			}
			close(mutationStarted)
			<-releaseMutation
			return nil
		})
	}()
	<-mutationStarted
	t.Cleanup(func() {
		select {
		case <-releaseMutation:
		default:
			close(releaseMutation)
		}
	})
	during, err := capacity.snapshot()
	require.NoError(t, err)
	diskDuring, err := scanUsage(root)
	require.NoError(t, err)
	require.GreaterOrEqual(t, during.Allocated, diskDuring.Allocated, "in-flight deletion must remain conservatively charged")

	admitted := make(chan error, 1)
	go func() {
		reservation, err := capacity.ReserveContent(uuid.New(), 4096)
		if err == nil {
			reservation.Release()
		}
		admitted <- err
	}()
	select {
	case err := <-admitted:
		require.NoError(t, err)
	case <-time.After(10 * time.Millisecond):
		t.Fatal("constant-time admission waited behind a filesystem mutation")
	}
	close(releaseMutation)
	require.NoError(t, <-mutationDone)
	after, err := capacity.snapshot()
	require.NoError(t, err)
	diskAfter, err := scanUsage(root)
	require.NoError(t, err)
	require.Equal(t, diskAfter.Allocated, after.Allocated)
}

func TestCapacityAdditiveMutationGrowthIsReservedBeforeFilesystemIO(t *testing.T) {
	root := t.TempDir()
	ready := filepath.Join(root, "ready")
	quarantine := filepath.Join(root, "quarantine")
	require.NoError(t, os.Mkdir(ready, 0o700))
	require.NoError(t, os.Mkdir(quarantine, 0o700))
	const maxBytes = int64(1 << 20)
	capacity := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 maxBytes,
		OperationalHeadroomBytes: 16 << 10,
	}, usage{Allocated: maxBytes - 2*filesystemBlockSize, Free: 20 << 30, BlockSize: filesystemBlockSize})
	mutationStarted := make(chan struct{})
	releaseMutation := make(chan struct{})
	mutationDone := make(chan error, 1)
	go func() {
		mutationDone <- capacity.trackAllocationMutationWithOperationalGrowth([]string{ready, quarantine}, false, func() error {
			close(mutationStarted)
			<-releaseMutation
			return nil
		})
	}()
	<-mutationStarted
	t.Cleanup(func() {
		select {
		case <-releaseMutation:
		default:
			close(releaseMutation)
		}
	})

	_, err := capacity.ReserveContent(uuid.New(), 1)
	require.ErrorIs(t, err, ErrSpoolCap, "admission must not consume the growth reserved by an in-flight quarantine rename")

	close(releaseMutation)
	require.NoError(t, <-mutationDone)
}

func TestCapacityFreeSpaceProbeRunsOutsideAdmissionLedgerLock(t *testing.T) {
	capacity := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30}, usage{Free: 20 << 30})
	probeStarted := make(chan struct{})
	releaseProbe := make(chan struct{})
	capacity.mu.Lock()
	capacity.currentFree = func() (int64, error) {
		close(probeStarted)
		<-releaseProbe
		return 20 << 30, nil
	}
	capacity.mu.Unlock()
	reservationDone := make(chan error, 1)
	go func() {
		reservation, err := capacity.ReserveContent(uuid.New(), 4096)
		if err == nil {
			reservation.Release()
		}
		reservationDone <- err
	}()
	<-probeStarted
	t.Cleanup(func() {
		select {
		case <-releaseProbe:
		default:
			close(releaseProbe)
		}
	})

	ledgerResponsive := make(chan struct{})
	go func() {
		_ = capacity.reservedBytes()
		close(ledgerResponsive)
	}()
	select {
	case <-ledgerResponsive:
	case <-time.After(10 * time.Millisecond):
		t.Fatal("filesystem free-space probe held the admission ledger lock")
	}
	close(releaseProbe)
	require.NoError(t, <-reservationDone)
}

func TestCapacityFailedReconciliationRetainsPessimisticReservation(t *testing.T) {
	capacity := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 12 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, usage{Free: 20 << 30})
	reservation, err := capacity.reserveOperational(4096)
	require.NoError(t, err)

	err = reservation.consumeAllocationDelta(-8192)

	require.ErrorIs(t, err, ErrSpoolCorrupt)
	require.EqualValues(t, 4096, capacity.reservedBytes(), "failed accounting must remain conservatively charged")
	reservation.Release()
	require.Zero(t, capacity.reservedBytes())
}

func TestCapacityCachedAccountingMatchesDiskAcrossAttemptBatchQuarantineAndRecovery(t *testing.T) {
	root := filepath.Join(t.TempDir(), "spool")
	store := openTestStoreAt(t, root, nil)
	requireCachedUsageMatchesScan(t, store)

	aborted := beginAttempt(t, store, policyAll())
	require.NoError(t, aborted.WriteRequest(bytes.Repeat([]byte("rollback"), 2048)))
	aborted.Abort(errors.New("test rollback"))
	requireCachedUsageMatchesScan(t, store)

	committed := beginAttempt(t, store, policyAll())
	require.NoError(t, committed.WriteRequest(bytes.Repeat([]byte("committed"), 2048)))
	require.NoError(t, committed.Commit())
	requireCachedUsageMatchesScan(t, store)

	batch, err := store.NextBatch(10, 64<<20)
	require.NoError(t, err)
	require.NotNil(t, batch)
	requireCachedUsageMatchesScan(t, store)
	require.NoError(t, store.MarkAcked(batch))
	requireCachedUsageMatchesScan(t, store)
	require.NoError(t, store.CleanupAcked(batch))
	requireCachedUsageMatchesScan(t, store)

	quarantined := beginAttempt(t, store, policyAll())
	require.NoError(t, quarantined.WriteResponse([]byte(`{"stop_reason":"stop"}`)))
	require.NoError(t, quarantined.Commit())
	batch, err = store.NextBatch(10, 64<<20)
	require.NoError(t, err)
	_, err = store.QuarantineCorrupt(batch, quarantined.ID())
	require.NoError(t, err)
	requireCachedUsageMatchesScan(t, store)

	orphanPath := filepath.Join(store.partialDir, uuid.New().String())
	require.NoError(t, os.Mkdir(orphanPath, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(orphanPath, "orphan"), bytes.Repeat([]byte("x"), 8192), 0o600))
	reopened := openTestStoreAt(t, root, nil)
	requireCachedUsageMatchesScan(t, reopened)
	_, err = reopened.Recover(context.Background())
	require.NoError(t, err)
	requireCachedUsageMatchesScan(t, reopened)
}

func requireCachedUsageMatchesScan(t *testing.T, store *Store) {
	t.Helper()
	want, err := scanUsage(store.config.RootDir)
	require.NoError(t, err)
	got, err := store.capacity.snapshot()
	require.NoError(t, err)
	require.Equal(t, want.Allocated, got.Allocated)
	require.Equal(t, want.OperationalAllocated, got.OperationalAllocated)
}

func TestCapacityRejectsAtPhysicalCap(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30, MinFreeBytes: 8 << 30},
		usage{Allocated: 12<<30 - 4096, Free: 20 << 30})

	_, err := c.Reserve(uuid.New(), 8192)

	require.ErrorIs(t, err, ErrSpoolCap)
}

func TestCapacityRejectsBeforeFreeReserveIsCrossed(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 12 << 30, MinFreeBytes: 8 << 30},
		usage{Allocated: 1 << 30, Free: 8<<30 + 4096})

	_, err := c.Reserve(uuid.New(), 8192)

	require.ErrorIs(t, err, ErrFreeReserve)
}

func TestSidecarRestartPreservesOldReadyDataAtCapAndFreeReserve(t *testing.T) {
	for _, test := range []struct {
		name      string
		usage     usage
		wantError error
	}{
		{name: "12 GiB physical cap", usage: usage{Allocated: 12 << 30, Free: 1 << 40}, wantError: ErrSpoolCap},
		{name: "8 GiB free reserve", usage: usage{Allocated: 1 << 20, Free: 8 << 30}, wantError: ErrFreeReserve},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "spool")
			store := openTestStoreAt(t, root, nil)
			ready := beginAttempt(t, store, policyAll())
			require.NoError(t, ready.WriteRequest([]byte("preserve old ready data")))
			require.NoError(t, ready.Commit())

			setCapacityUsage(store.capacity, test.usage)
			newer := beginAttemptWithoutFailure(t, store, policyAll())
			require.ErrorIs(t, newer.err, test.wantError)
			require.Nil(t, newer.attempt)
			require.DirExists(t, readyPath(store, ready.ID(), "."))

			reopened := openTestStoreAt(t, root, nil)
			recoverStore(t, reopened)
			refs := reopened.Ready()
			require.Len(t, refs, 1)
			require.Equal(t, ready.ID(), refs[0].CaptureID)
		})
	}
}

func TestConcurrentReservationsCannotCollectivelyOversubscribe(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 64 << 10}, usage{Free: 1 << 30})
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := c.Reserve(uuid.New(), 48<<10)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	var accepted, rejected int
	for err := range results {
		switch {
		case err == nil:
			accepted++
		case isCapacityError(err):
			rejected++
		default:
			t.Fatalf("unexpected reservation error: %v", err)
		}
	}
	require.Equal(t, 1, accepted)
	require.Equal(t, 1, rejected)
}

func TestCapacityScanIncludesAllocatedBlocksFromEverySpoolDirectory(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"partial", "ready", "sending"} {
		path := filepath.Join(root, directory)
		require.NoError(t, os.Mkdir(path, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(path, "allocated"), make([]byte, 4096), 0o600))
	}

	got, err := scanUsage(root)

	require.NoError(t, err)
	require.GreaterOrEqual(t, got.Allocated, int64(3*4096))
	require.Greater(t, got.Free, int64(0))
}

func TestCapacityScanIncludesRenamedPinnedDirectoryWithoutFollowingSymlink(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"partial", "ready", "sending", "quarantine"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, directory), 0o700))
	}
	pinnedReady := filepath.Join(root, "ready-pinned")
	require.NoError(t, os.Rename(filepath.Join(root, "ready"), pinnedReady))
	require.NoError(t, os.Mkdir(filepath.Join(root, "ready"), 0o700))
	baseline, err := scanUsage(root)
	require.NoError(t, err)

	ownedSibling := filepath.Join(pinnedReady, "unrelated-allocated-sibling")
	require.NoError(t, os.WriteFile(ownedSibling, make([]byte, filesystemBlockSize), 0o600))
	ownedInfo, err := os.Stat(ownedSibling)
	require.NoError(t, err)
	ownedAllocated := allocatedFileInfo(ownedInfo)
	require.Positive(t, ownedAllocated)
	external := filepath.Join(t.TempDir(), "external-allocated-target")
	require.NoError(t, os.WriteFile(external, make([]byte, 8<<20), 0o600))
	externalInfo, err := os.Stat(external)
	require.NoError(t, err)
	externalAllocated := allocatedFileInfo(externalInfo)
	require.NoError(t, os.Symlink(external, filepath.Join(root, "external-link")))

	got, err := scanUsage(root)
	require.NoError(t, err)
	require.GreaterOrEqual(t, got.Allocated, baseline.Allocated+ownedAllocated)
	require.Less(t, got.Allocated-baseline.Allocated, ownedAllocated+externalAllocated,
		"the scanner must not charge blocks owned by a symlink target outside the spool root")

	capacity, err := newCapacity(CapacityConfig{
		RootDir:  root,
		MaxBytes: baseline.Allocated + ownedAllocated,
	}, nil)
	require.NoError(t, err)
	_, err = capacity.ReserveContent(uuid.New(), 1)
	require.ErrorIs(t, err, ErrSpoolCap)
}

func TestReservationConsumeAndReleaseNeverMakeAccountingNegative(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 1 << 20}, usage{Free: 1 << 30})
	r, err := c.Reserve(uuid.New(), 4096)
	require.NoError(t, err)

	require.NoError(t, r.Consume(8192))
	r.Release()
	r.Release()

	require.Zero(t, c.reservedBytes())
}

func TestAdmissionLeavesSixteenMiBForSendingMetadata(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 12 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, usage{Allocated: 12<<30 - 16<<20, Free: 20 << 30})

	_, err := c.ReserveContent(uuid.New(), 1)

	require.ErrorIs(t, err, ErrSpoolCap)
	require.NoError(t, c.ReserveOperational(64<<10))
}

func TestOperationalAdmissionCheckDoesNotLeakReservation(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 12 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, usage{Allocated: 12<<30 - 16<<20, Free: 20 << 30})

	require.NoError(t, c.ReserveOperational(64<<10))

	require.Zero(t, c.reservedBytes())
}

func TestOperationalReservationsCannotExceedDedicatedHeadroom(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 12 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, usage{Free: 20 << 30})

	_, err := c.reserveOperational(16<<20 + 1)

	require.ErrorIs(t, err, ErrSpoolCap)
}

func TestOperationalReservationIncludesExistingSendingAllocation(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 12 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, usage{Allocated: 16<<20 - 4096, Free: 20 << 30, OperationalAllocated: 16<<20 - 4096})

	_, err := c.reserveOperational(8192)

	require.ErrorIs(t, err, ErrSpoolCap)
}

func TestContentAdmissionDoesNotChargeOperationalAllocationAgainstContentRegion(t *testing.T) {
	const (
		maxBytes  = int64(12 << 30)
		headroom  = int64(16 << 20)
		operating = int64(8 << 20)
	)
	contentAllocated := maxBytes - headroom - 8192
	c := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 maxBytes,
		OperationalHeadroomBytes: headroom,
	}, usage{
		Allocated:            contentAllocated + operating,
		OperationalAllocated: operating,
		Free:                 20 << 30,
	})

	reservation, err := c.ReserveContent(uuid.New(), 4096)

	require.NoError(t, err)
	reservation.Release()
}

func TestContentAdmissionStillCountsOperationalAllocationAtPhysicalCap(t *testing.T) {
	const operating = int64(8 << 20)
	c := newTestCapacity(t, CapacityConfig{
		MaxBytes:                 12 << 30,
		OperationalHeadroomBytes: 16 << 20,
	}, usage{
		Allocated:            12<<30 - 4096,
		OperationalAllocated: operating,
		Free:                 20 << 30,
	})

	_, err := c.ReserveContent(uuid.New(), 8192)

	require.ErrorIs(t, err, ErrSpoolCap)
}

func TestWorstCaseReservationUsesZstdMaxEncodedSizeRoundedToFilesystemBlock(t *testing.T) {
	// For the configured encoder, MaxEncodedSize(4033) is 4048. This catches
	// replacing the codec bound with a looser guess that rejects an admissible
	// frame at a filesystem-block boundary.
	require.EqualValues(t, 4096, worstCaseFrameReservation(4033))
}

func TestFrameReservationUsesTheMeasuredFilesystemBlockSize(t *testing.T) {
	c := newTestCapacity(t, CapacityConfig{MaxBytes: 8191}, usage{
		Free:      1 << 30,
		BlockSize: 8192,
	})

	err := c.BeforeFrame(uuid.New(), make([]byte, 4033))

	require.ErrorIs(t, err, ErrSpoolCap)
}

func newTestCapacity(t *testing.T, config CapacityConfig, current usage) *Capacity {
	t.Helper()
	c, err := newCapacity(config, func() (usage, error) { return current, nil })
	require.NoError(t, err)
	return c
}

func setCapacityUsage(capacity *Capacity, current usage) {
	capacity.mu.Lock()
	defer capacity.mu.Unlock()
	capacity.allocated = current.Allocated
	capacity.operationalAllocated = current.OperationalAllocated
	capacity.free = current.Free
	capacity.blockSize = current.BlockSize
	if capacity.blockSize <= 0 {
		capacity.blockSize = filesystemBlockSize
	}
	capacity.currentFree = nil
}
